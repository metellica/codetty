package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"codetty/certs"
	"codetty/protocol"

	"github.com/gorilla/websocket"
	xterm "golang.org/x/term"
)

const defaultSecureServer = "wss://localhost:8080/ws"
const defaultInsecureServer = "ws://localhost:8080/ws"

// clientConfig holds the raw CLI flag values.
type clientConfig struct {
	ServerURL  string
	ClientID   string
	IDProvided bool
	CertFile   string
	KeyFile    string
	CAFile     string
	CertDir    string
	Insecure   bool
}

// connectionConfig holds the resolved dial parameters used by runConnection.
type connectionConfig struct {
	ServerURL string
	Dialer    *websocket.Dialer
	User      string
}

// parseClientFlags parses codetty-cli flags from args and returns the config,
// remaining non-flag arguments, and any parse error.
func parseClientFlags(args []string) (clientConfig, []string, error) {
	fs := flag.NewFlagSet("codetty-cli", flag.ContinueOnError)

	server := fs.String("server", "", "hub WebSocket URL (default: wss://localhost:8080/ws in secure mode, ws://localhost:8080/ws with --insecure)")
	clientID := fs.String("id", "", "client identifier")
	certFile := fs.String("cert", "", "client certificate file (PEM)")
	keyFile := fs.String("key", "", "client key file (PEM)")
	caFile := fs.String("ca", "", "CA certificate file (PEM)")
	certDir := fs.String("cert-dir", "", "directory containing client.crt, client.key, ca.crt")
	insecure := fs.Bool("insecure", false, "disable mTLS (insecure, not recommended)")

	if err := fs.Parse(args); err != nil {
		return clientConfig{}, nil, err
	}

	cfg := clientConfig{
		ServerURL: *server,
		ClientID:  *clientID,
		CertFile:  *certFile,
		KeyFile:   *keyFile,
		CAFile:    *caFile,
		CertDir:   *certDir,
		Insecure:  *insecure,
	}

	// If --server was not provided, choose a default based on insecure mode.
	serverProvided := false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "server":
			serverProvided = true
		case "id":
			cfg.IDProvided = true
		}
	})
	if !serverProvided {
		if cfg.Insecure {
			cfg.ServerURL = defaultInsecureServer
		} else {
			cfg.ServerURL = defaultSecureServer
		}
	}

	return cfg, fs.Args(), nil
}

// applyCertDir fills in CertFile, KeyFile, and CAFile from CertDir when the
// direct flags were not provided. Direct flags always take precedence.
func applyCertDir(cfg *clientConfig) {
	if cfg.CertDir == "" {
		return
	}
	if cfg.CertFile == "" {
		cfg.CertFile = filepath.Join(cfg.CertDir, "client.crt")
	}
	if cfg.KeyFile == "" {
		cfg.KeyFile = filepath.Join(cfg.CertDir, "client.key")
	}
	if cfg.CAFile == "" {
		cfg.CAFile = filepath.Join(cfg.CertDir, "ca.crt")
	}
}

// prepareConnectionConfig validates a clientConfig and builds a connectionConfig.
// In secure mode (default) all of --cert, --key, and --ca must be provided.
func prepareConnectionConfig(cfg clientConfig) (connectionConfig, error) {
	u, err := url.Parse(cfg.ServerURL)
	if err != nil {
		return connectionConfig{}, fmt.Errorf("invalid server URL: %w", err)
	}

	if cfg.Insecure {
		// Cert flags must not be combined with --insecure (check before applyCertDir
		// so we see the original user-supplied values).
		if cfg.CertFile != "" || cfg.KeyFile != "" || cfg.CAFile != "" || cfg.CertDir != "" {
			return connectionConfig{}, fmt.Errorf(
				"--insecure cannot be combined with cert flags (--cert, --key, --ca, --cert-dir)",
			)
		}
		if u.Scheme != "ws" {
			return connectionConfig{}, fmt.Errorf(
				"--insecure requires a ws:// server URL; use wss:// only with secure mode",
			)
		}
		return connectionConfig{
			ServerURL: cfg.ServerURL,
			Dialer:    websocket.DefaultDialer,
		}, nil
	}

	// Secure mode: require wss:// scheme.
	if u.Scheme != "wss" {
		return connectionConfig{}, fmt.Errorf(
			"secure mode requires a wss:// server URL; use --insecure for ws://",
		)
	}

	applyCertDir(&cfg)

	// Secure mode: require cert/key/ca.
	if cfg.CertFile == "" || cfg.KeyFile == "" || cfg.CAFile == "" {
		return connectionConfig{}, fmt.Errorf(
			"secure mode requires --cert, --key, and --ca (or --cert-dir); " +
				"use --insecure to disable mTLS",
		)
	}

	tlsCfg, err := certs.BuildClientTLSConfig(cfg.CertFile, cfg.KeyFile, cfg.CAFile)
	if err != nil {
		return connectionConfig{}, fmt.Errorf("load TLS config: %w", err)
	}

	user, err := certs.CertificateUser(cfg.CertFile)
	if err != nil {
		return connectionConfig{}, fmt.Errorf("read cert CN: %w", err)
	}

	dialer := &websocket.Dialer{
		TLSClientConfig:  tlsCfg,
		HandshakeTimeout: websocket.DefaultDialer.HandshakeTimeout,
	}

	return connectionConfig{
		ServerURL: cfg.ServerURL,
		Dialer:    dialer,
		User:      user,
	}, nil
}

func main() {
	cfg, args, err := parseClientFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "codetty-cli: %v\n", err)
		os.Exit(2)
	}

	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: codetty-cli [--server URL] [--id NAME] [--cert F] [--key F] [--ca F] [--cert-dir DIR] [--insecure] -- <command> [args...]\n")
		os.Exit(1)
	}

	connCfg, err := prepareConnectionConfig(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "codetty-cli: %v\n", err)
		os.Exit(1)
	}

	if cfg.Insecure {
		log.Println("WARNING: running in insecure mode without mTLS — not recommended for production")
	}

	// Determine the client identity.
	idExplicit := cfg.IDProvided
	wd, _ := os.Getwd()
	hostname, _ := os.Hostname()
	identityID, title := buildClientIdentity(cfg.ClientID, idExplicit, wd, hostname, os.Getpid(), args[0])

	// Determine local console behavior. Windows terminals can expose a usable
	// output handle even when stdin size lookup fails, so keep input/raw mode
	// detection separate from size discovery.
	localTerm := currentLocalTerminalConfig()
	cols, rows := localTerm.cols, localTerm.rows

	term, err := startTerminal(args, cols, rows)
	if err != nil {
		log.Fatalf("start terminal: %v", err)
	}
	defer term.Close()

	// Signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Process exit channel
	doneCh := make(chan error, 1)
	go func() { doneCh <- term.Wait() }()

	// Local terminal: raw mode for interactive use
	if localTerm.inputIsTTY {
		oldState, err := xterm.MakeRaw(localTerm.inputFD)
		if err == nil {
			defer xterm.Restore(localTerm.inputFD, oldState)
		}
	}

	// Local stdin → PTY (always, not just for TTY)
	go func() {
		if err := copyLocalInput(term, os.Stdin); err != nil {
			log.Printf("local input copy stopped: %v", err)
		}
	}()

	screenMirror := NewScreenMirror(cols, rows)
	var replayMu sync.Mutex

	// Single PTY reader → local stdout + hub channel + screen mirror
	outputCh := make(chan []byte, 256)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := term.Read(buf)
			if n > 0 {
				os.Stdout.Write(buf[:n])
				cp := make([]byte, n)
				copy(cp, buf[:n])
				replayMu.Lock()
				screenMirror.Write(cp)
				select {
				case outputCh <- cp:
				default: // drop if hub not keeping up
				}
				replayMu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	// Run connection loop (reconnects on failure)
	go connectionLoop(connCfg, identityID, title, term, outputCh, doneCh, screenMirror, &replayMu)

	// Wait for process exit or signal
	select {
	case <-sigCh:
	case err := <-doneCh:
		if err != nil {
			log.Printf("command exited: %v", err)
		}
	}
}

func connectionLoop(connCfg connectionConfig, clientID, title string, term terminalSession, outputCh <-chan []byte, done <-chan error, screen *ScreenMirror, replayMu *sync.Mutex) {
	delay := time.Second
	maxDelay := 30 * time.Second

	for {
		err := runConnection(connCfg, clientID, title, term, outputCh, done, screen, replayMu)
		if err == nil {
			return
		}

		select {
		case <-done:
			return
		case <-time.After(delay):
		}

		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}

func runConnection(connCfg connectionConfig, clientID, title string, term terminalSession, outputCh <-chan []byte, done <-chan error, screen *ScreenMirror, replayMu *sync.Mutex) error {
	conn, _, err := connCfg.Dialer.Dial(connCfg.ServerURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	cols, rows := screen.Size()
	regMsg, err := protocol.EncodeRegister(protocol.RegisterPayload{
		ID:    clientID,
		Title: title,
		Cols:  cols,
		Rows:  rows,
		User:  connCfg.User,
	})
	if err != nil {
		return fmt.Errorf("encode register: %w", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, regMsg); err != nil {
		return fmt.Errorf("send register: %w", err)
	}

	if replay := snapshotReplay(outputCh, screen, replayMu); len(replay) > 0 {
		msg := protocol.EncodeReplay(replay)
		if err := conn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
			return fmt.Errorf("send replay: %w", err)
		}
	}

	errCh := make(chan error, 2)
	stopCh := make(chan struct{})
	defer close(stopCh)

	// Output channel → hub
	go func() {
		for {
			select {
			case data, ok := <-outputCh:
				if !ok {
					errCh <- nil
					return
				}
				msg := protocol.EncodeOutput(data)
				if werr := conn.WriteMessage(websocket.BinaryMessage, msg); werr != nil {
					errCh <- fmt.Errorf("write output: %w", werr)
					return
				}
			case <-stopCh:
				return
			}
		}
	}()

	// Hub → PTY input / resize
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				errCh <- fmt.Errorf("read: %w", err)
				return
			}
			msgType, payload, ok := protocol.ParseType(msg)
			if !ok {
				continue
			}
			switch msgType {
			case protocol.MsgInput:
				term.Write(payload)
			case protocol.MsgResize:
				p, err := protocol.DecodeResize(payload)
				if err == nil {
					replayMu.Lock()
					if err := term.Resize(p.Cols, p.Rows); err != nil {
						log.Printf("resize terminal: %v", err)
					} else {
						screen.Resize(p.Cols, p.Rows)
					}
					replayMu.Unlock()
				}
			}
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-done:
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(1000, "process exited"))
		return nil
	}
}

func buildClientIdentity(id string, explicit bool, dir string, hostname string, pid int, command string) (string, string) {
	if explicit {
		return id, fmt.Sprintf("%s@%s", command, id)
	}
	base := filepath.Base(dir)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "unknown"
	}
	if hostname == "" {
		hostname = "unknown"
	}
	generatedID := fmt.Sprintf("%s-%s-%d", base, hostname, pid)
	title := fmt.Sprintf("%s@%s#%d", command, base, pid)
	return generatedID, title
}

func flagWasProvided(fs *flag.FlagSet, name string) bool {
	provided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			provided = true
		}
	})
	return provided
}

func copyLocalInput(dst io.Writer, src io.Reader) error {
	buf := make([]byte, 4096)
	var filter focusEventFilter
	for {
		n, err := src.Read(buf)
		if n > 0 {
			filtered := filter.Filter(buf[:n])
			if len(filtered) > 0 {
				if _, werr := dst.Write(filtered); werr != nil {
					return werr
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				if pending := filter.Flush(); len(pending) > 0 {
					if _, werr := dst.Write(pending); werr != nil {
						return werr
					}
				}
				return nil
			}
			return err
		}
	}
}

type focusEventFilter struct {
	pending []byte
}

func (f *focusEventFilter) Filter(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for _, b := range data {
		switch len(f.pending) {
		case 0:
			if b == 0x1b {
				f.pending = append(f.pending, b)
			} else {
				out = append(out, b)
			}
		case 1:
			if b == '[' {
				f.pending = append(f.pending, b)
			} else {
				out = append(out, f.pending...)
				f.pending = f.pending[:0]
				if b == 0x1b {
					f.pending = append(f.pending, b)
				} else {
					out = append(out, b)
				}
			}
		case 2:
			if b == 'I' || b == 'O' {
				f.pending = f.pending[:0]
				continue
			}
			out = append(out, f.pending...)
			f.pending = f.pending[:0]
			if b == 0x1b {
				f.pending = append(f.pending, b)
			} else {
				out = append(out, b)
			}
		}
	}
	return out
}

func (f *focusEventFilter) Flush() []byte {
	if len(f.pending) == 0 {
		return nil
	}
	out := append([]byte(nil), f.pending...)
	f.pending = f.pending[:0]
	return out
}
