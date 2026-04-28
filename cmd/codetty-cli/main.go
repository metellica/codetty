package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"codetty/protocol"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	xterm "golang.org/x/term"
)

func main() {
	serverURL := flag.String("server", "ws://localhost:8080/ws", "hub WebSocket URL")
	clientID := flag.String("id", mustHostname(), "client identifier")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: codetty-cli --server ws://hub:8080/ws --id NAME -- <command> [args...]\n")
		os.Exit(1)
	}

	// Determine terminal size
	cols, rows := uint16(220), uint16(50)
	fd := int(os.Stdin.Fd())
	isTTY := xterm.IsTerminal(fd)
	if isTTY {
		w, h, err := xterm.GetSize(fd)
		if err == nil {
			cols, rows = uint16(w), uint16(h)
		}
	}

	// Spawn the command in a PTY
	cmd := exec.Command(args[0], args[1:]...)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		log.Fatalf("pty.Start: %v", err)
	}
	defer ptmx.Close()

	title := fmt.Sprintf("%s@%s", args[0], *clientID)

	// Signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Process exit channel
	doneCh := make(chan error, 1)
	go func() { doneCh <- cmd.Wait() }()

	// Local terminal: raw mode for interactive use
	if isTTY {
		oldState, err := xterm.MakeRaw(fd)
		if err == nil {
			defer xterm.Restore(fd, oldState)
		}
	}

	// Local stdin → PTY (always, not just for TTY)
	go func() { io.Copy(ptmx, os.Stdin) }()

	// Single PTY reader → local stdout + hub channel
	outputCh := make(chan []byte, 256)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				os.Stdout.Write(buf[:n])
				cp := make([]byte, n)
				copy(cp, buf[:n])
				select {
				case outputCh <- cp:
				default: // drop if hub not keeping up
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Run connection loop (reconnects on failure)
	go connectionLoop(*serverURL, *clientID, title, cols, rows, ptmx, outputCh, doneCh)

	// Wait for process exit or signal
	select {
	case <-sigCh:
	case err := <-doneCh:
		if err != nil {
			log.Printf("command exited: %v", err)
		}
	}
}

func connectionLoop(serverURL, clientID, title string, cols, rows uint16, ptmx *os.File, outputCh <-chan []byte, done <-chan error) {
	delay := time.Second
	maxDelay := 30 * time.Second

	for {
		err := runConnection(serverURL, clientID, title, cols, rows, ptmx, outputCh, done)
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

func runConnection(serverURL, clientID, title string, cols, rows uint16, ptmx *os.File, outputCh <-chan []byte, done <-chan error) error {
	conn, _, err := websocket.DefaultDialer.Dial(serverURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

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
				ptmx.Write(payload)
			case protocol.MsgResize:
				p, err := protocol.DecodeResize(payload)
				if err == nil {
					pty.Setsize(ptmx, &pty.Winsize{Cols: p.Cols, Rows: p.Rows})
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

func mustHostname() string {
	h, _ := os.Hostname()
	if h == "" {
		return "unknown"
	}
	return h
}
