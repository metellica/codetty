package daemon

import (
	"sync"

	"codetty/terminal"
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
}

// HubConn manages a WebSocket connection to the hub for one session.
type HubConn struct {
	cfg      HubConnConfig
	screen   *terminal.ScreenMirror
	outputCh chan []byte
	done     chan struct{}
	stopOnce sync.Once
}

// NewHubConn creates a new hub connection manager.
func NewHubConn(cfg HubConnConfig) *HubConn {
	return &HubConn{
		cfg:      cfg,
		screen:   terminal.NewScreenMirror(cfg.Cols, cfg.Rows),
		outputCh: make(chan []byte, 256),
		done:     make(chan struct{}),
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
	select {
	case <-h.done:
		return "stopped"
	default:
		return "connecting"
	}
}

func (h *HubConn) connectionLoop() {
	// Full implementation in Task 5
	<-h.done
}
