package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"codetty/protocol"

	"github.com/gorilla/websocket"
	xterm "golang.org/x/term"
)

func main() {
	serverURL := flag.String("server", "ws://localhost:8080/ws", "hub WebSocket URL")
	clientID := flag.String("id", "", "client identifier")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: codetty-cli --server ws://hub:8080/ws --id NAME -- <command> [args...]\n")
		os.Exit(1)
	}

	idExplicit := flagWasProvided(flag.CommandLine, "id")
	wd, _ := os.Getwd()
	hostname, _ := os.Hostname()
	identityID, title := buildClientIdentity(*clientID, idExplicit, wd, hostname, os.Getpid(), args[0])

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
	go connectionLoop(*serverURL, identityID, title, term, outputCh, doneCh, screenMirror, &replayMu)

	// Wait for process exit or signal
	select {
	case <-sigCh:
	case err := <-doneCh:
		if err != nil {
			log.Printf("command exited: %v", err)
		}
	}
}

func connectionLoop(serverURL, clientID, title string, term terminalSession, outputCh <-chan []byte, done <-chan error, screen *ScreenMirror, replayMu *sync.Mutex) {
	delay := time.Second
	maxDelay := 30 * time.Second

	for {
		err := runConnection(serverURL, clientID, title, term, outputCh, done, screen, replayMu)
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

func runConnection(serverURL, clientID, title string, term terminalSession, outputCh <-chan []byte, done <-chan error, screen *ScreenMirror, replayMu *sync.Mutex) error {
	conn, _, err := websocket.DefaultDialer.Dial(serverURL, nil)
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
