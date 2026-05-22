package daemon

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"codetty/protocol"
	"codetty/terminal"

	"github.com/gorilla/websocket"
)

// HubConnConfig holds configuration for a per-session hub connection.
type HubConnConfig struct {
	ServerURL string
	ClientID  string
	Title     string
	Cols      uint16
	Rows      uint16
	Insecure  bool
	CertFile  string
	KeyFile   string
	CAFile    string
	User      string
}

// HubConn manages a WebSocket connection to the hub for one session.
type HubConn struct {
	cfg      HubConnConfig
	screen   *terminal.ScreenMirror
	session  *DaemonSession // back-reference for input/resize
	outputCh chan []byte
	done     chan struct{}
	stopOnce sync.Once

	mu     sync.Mutex
	status string
}

// NewHubConn creates a new hub connection manager.
func NewHubConn(cfg HubConnConfig, sess *DaemonSession) *HubConn {
	return &HubConn{
		cfg:      cfg,
		screen:   terminal.NewScreenMirror(cfg.Cols, cfg.Rows),
		session:  sess,
		outputCh: make(chan []byte, 256),
		done:     make(chan struct{}),
		status:   "connecting",
	}
}

// Start begins the hub connection loop in a goroutine.
func (h *HubConn) Start() {
	go h.connectionLoop()
}

// Stop terminates the hub connection.
func (h *HubConn) Stop() {
	h.stopOnce.Do(func() { close(h.done) })
}

// SendOutput queues output data for transmission to the hub.
func (h *HubConn) SendOutput(data []byte) {
	h.screen.Write(data)
	select {
	case h.outputCh <- data:
	default:
	}
}

// Resize updates the screen dimensions for the hub connection.
func (h *HubConn) Resize(cols, rows uint16) {
	h.screen.Resize(cols, rows)
}

// Status returns the current connection status string.
func (h *HubConn) Status() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status
}

func (h *HubConn) setStatus(s string) {
	h.mu.Lock()
	h.status = s
	h.mu.Unlock()
}

func (h *HubConn) connectionLoop() {
	delay := time.Second
	maxDelay := 30 * time.Second

	for {
		err := h.runConnection()
		if err == nil {
			h.setStatus("stopped")
			return
		}
		if isFatalHubError(err) {
			log.Printf("hub: fatal error, not retrying: %v", err)
			h.setStatus("error")
			return
		}

		h.setStatus("reconnecting")
		select {
		case <-h.done:
			h.setStatus("stopped")
			return
		case <-time.After(delay):
		}
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}

// errHubAuthRejected means the hub rejected our connection.
var errHubAuthRejected = errors.New("hub auth rejected")

func isFatalHubError(err error) bool {
	return errors.Is(err, errHubAuthRejected)
}

func (h *HubConn) runConnection() error {
	dialer, err := h.buildDialer()
	if err != nil {
		return err
	}

	conn, _, err := dialer.Dial(h.cfg.ServerURL, nil)
	if err != nil {
		return fmt.Errorf("dial hub: %w", err)
	}
	defer conn.Close()

	h.setStatus("connected")

	var writeMu sync.Mutex
	writeFrame := func(data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteMessage(websocket.BinaryMessage, data)
	}

	// Register
	cols, rows := h.screen.Size()
	regMsg, err := protocol.EncodeRegister(protocol.RegisterPayload{
		ID:    h.cfg.ClientID,
		Title: h.cfg.Title,
		Cols:  cols,
		Rows:  rows,
		User:  h.cfg.User,
	})
	if err != nil {
		return fmt.Errorf("encode register: %w", err)
	}
	if err := writeFrame(regMsg); err != nil {
		return fmt.Errorf("send register: %w", err)
	}

	// Send replay snapshot
	if replay := h.screen.Snapshot(); len(replay) > 0 {
		msg := protocol.EncodeReplay(replay)
		if err := writeFrame(msg); err != nil {
			return fmt.Errorf("send replay: %w", err)
		}
	}

	errCh := make(chan error, 2)
	stopCh := make(chan struct{})
	defer close(stopCh)

	// Forward output → hub
	go func() {
		for {
			select {
			case data, ok := <-h.outputCh:
				if !ok {
					errCh <- nil
					return
				}
				msg := protocol.EncodeOutput(data)
				if werr := writeFrame(msg); werr != nil {
					errCh <- fmt.Errorf("write output: %w", werr)
					return
				}
			case <-stopCh:
				return
			}
		}
	}()

	// Hub → session (input/resize)
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				var closeErr *websocket.CloseError
				if errors.As(err, &closeErr) &&
					closeErr.Code == websocket.ClosePolicyViolation &&
					strings.Contains(closeErr.Text, "codetty auth rejected") {
					errCh <- fmt.Errorf("%w: %s", errHubAuthRejected, closeErr.Text)
					return
				}
				errCh <- fmt.Errorf("read hub: %w", err)
				return
			}
			msgType, payload, ok := protocol.ParseType(msg)
			if !ok {
				continue
			}
			switch msgType {
			case protocol.MsgInput:
				h.session.Terminal.Write(payload)
			case protocol.MsgResize:
				p, perr := protocol.DecodeResize(payload)
				if perr == nil {
					h.session.Resize(p.Cols, p.Rows)
				}
			}
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-h.done:
		writeMu.Lock()
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(1000, "session stopping"))
		writeMu.Unlock()
		return nil
	}
}

func (h *HubConn) buildDialer() (*websocket.Dialer, error) {
	dialer := &websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	if h.cfg.Insecure || !strings.HasPrefix(h.cfg.ServerURL, "wss://") {
		return dialer, nil
	}

	tlsCfg := &tls.Config{}

	if h.cfg.CertFile != "" && h.cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(h.cfg.CertFile, h.cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client cert: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	if h.cfg.CAFile != "" {
		caCert, err := os.ReadFile(h.cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(caCert)
		tlsCfg.RootCAs = pool
	}

	dialer.TLSClientConfig = tlsCfg
	dialer.Proxy = http.ProxyFromEnvironment
	return dialer, nil
}

