package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"codetty/daemon"
	"codetty/terminal"

	xterm "golang.org/x/term"
)

func cmdNew(args []string) {
	fs := flag.NewFlagSet("new", flag.ExitOnError)
	session := fs.String("s", "", "session name")
	noHub := fs.Bool("no-hub", false, "do not connect to hub")
	hubURL := fs.String("hub-url", "", "hub WebSocket URL")
	insecure := fs.Bool("insecure", false, "skip TLS verification")
	fs.Parse(args)

	command := fs.Args()
	if len(command) == 0 {
		command = []string{terminal.DefaultShell()}
	}

	sockPath := defaultSocketPath()
	if err := ensureDaemon(sockPath); err != nil {
		log.Fatalf("cannot start daemon: %v", err)
	}

	cols, rows := terminalSize()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		log.Fatalf("connect to daemon: %v", err)
	}
	defer conn.Close()

	req := daemon.Request{
		Action:      daemon.ActionNewSession,
		Session:     *session,
		Command:     command,
		Cols:        cols,
		Rows:        rows,
		NoHub:       *noHub,
		HubServer:   *hubURL,
		HubInsecure: *insecure,
	}
	daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(req))

	ft, payload, err := daemon.ReadFrame(conn)
	if err != nil {
		log.Fatalf("read response: %v", err)
	}
	if ft != daemon.FrameResponse {
		log.Fatalf("unexpected frame type: %d", ft)
	}
	var resp daemon.Response
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		log.Fatalf("new session failed: %s", resp.Error)
	}

	runAttachLoop(conn, resp.Session)
}

func cmdAttach(args []string) {
	fs := flag.NewFlagSet("attach", flag.ExitOnError)
	session := fs.String("s", "", "session name (default: latest)")
	fs.Parse(args)

	sockPath := defaultSocketPath()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		log.Fatalf("daemon not running (cannot connect to %s)", sockPath)
	}
	defer conn.Close()

	cols, rows := terminalSize()
	req := daemon.Request{
		Action:  daemon.ActionAttach,
		Session: *session,
		Cols:    cols,
		Rows:    rows,
	}
	daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(req))

	ft, payload, err := daemon.ReadFrame(conn)
	if err != nil {
		log.Fatalf("read response: %v", err)
	}
	if ft != daemon.FrameResponse {
		log.Fatalf("unexpected frame type: %d", ft)
	}
	var resp daemon.Response
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "error: %s\n", resp.Error)
		os.Exit(1)
	}

	runAttachLoop(conn, resp.Session)
}

func cmdList(args []string) {
	sockPath := defaultSocketPath()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		fmt.Println("no daemon running")
		return
	}
	defer conn.Close()

	daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{Action: daemon.ActionList}))
	ft, payload, err := daemon.ReadFrame(conn)
	if err != nil {
		log.Fatalf("read response: %v", err)
	}
	if ft != daemon.FrameResponse {
		log.Fatal("unexpected response")
	}
	var resp daemon.Response
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		log.Fatalf("list failed: %s", resp.Error)
	}

	if len(resp.Sessions) == 0 {
		fmt.Println("no sessions")
		return
	}

	fmt.Printf("%-16s %-12s %-8s %-10s %s\n", "NAME", "COMMAND", "CLIENTS", "HUB", "CREATED")
	for _, s := range resp.Sessions {
		fmt.Printf("%-16s %-12s %-8d %-10s %s\n", s.Name, s.Command, s.Attached, s.HubStatus, s.CreatedAt)
	}
}

func cmdKill(args []string) {
	fs := flag.NewFlagSet("kill", flag.ExitOnError)
	session := fs.String("s", "", "session name")
	fs.Parse(args)

	if *session == "" && fs.NArg() > 0 {
		*session = fs.Arg(0)
	}
	if *session == "" {
		log.Fatal("specify session name: codetty kill -s NAME")
	}

	sockPath := defaultSocketPath()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		log.Fatal("no daemon running")
	}
	defer conn.Close()

	daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{Action: daemon.ActionKill, Session: *session}))
	ft, payload, _ := daemon.ReadFrame(conn)
	if ft != daemon.FrameResponse {
		log.Fatal("unexpected response")
	}
	var resp daemon.Response
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		log.Fatalf("kill failed: %s", resp.Error)
	}
	fmt.Printf("killed session %q\n", *session)
}

func cmdKillServer(args []string) {
	sockPath := defaultSocketPath()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		fmt.Println("no daemon running")
		return
	}
	defer conn.Close()

	daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{Action: daemon.ActionKillServer}))
	daemon.ReadFrame(conn)
	fmt.Println("daemon stopped")
}

// runAttachLoop runs the terminal raw mode loop, forwarding I/O between
// the local terminal and the daemon session.
func runAttachLoop(conn net.Conn, sessionName string) {
	oldState, err := xterm.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		log.Fatalf("raw mode: %v", err)
	}
	defer xterm.Restore(int(os.Stdin.Fd()), oldState)

	// Handle terminal resize
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go func() {
		for range sigCh {
			cols, rows := terminalSize()
			daemon.WriteFrame(conn, daemon.FrameResize, daemon.MarshalJSON(daemon.ResizePayload{Cols: cols, Rows: rows}))
		}
	}()

	doneCh := make(chan struct{})

	// Read from daemon → stdout
	go func() {
		defer close(doneCh)
		for {
			ft, payload, err := daemon.ReadFrame(conn)
			if err != nil {
				return
			}
			switch ft {
			case daemon.FrameOutput:
				os.Stdout.Write(payload)
			case daemon.FrameNotification:
				var n daemon.Notification
				json.Unmarshal(payload, &n)
				handleNotification(n, sessionName)
				if n.Event == "session_exited" {
					return
				}
			}
		}
	}()

	// Read from stdin → daemon (with Ctrl+A prefix handling)
	go func() {
		buf := make([]byte, 4096)
		prefixActive := false
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}
			data := buf[:n]

			if prefixActive {
				prefixActive = false
				if len(data) > 0 {
					switch data[0] {
					case 'd': // detach
						daemon.WriteFrame(conn, daemon.FrameDetach, nil)
						conn.Close()
						return
					case 'n': // next session
						daemon.WriteFrame(conn, daemon.FrameSessionSwitch, daemon.MarshalJSON(daemon.SwitchPayload{Target: "__next"}))
						continue
					case 'p': // prev session
						daemon.WriteFrame(conn, daemon.FrameSessionSwitch, daemon.MarshalJSON(daemon.SwitchPayload{Target: "__prev"}))
						continue
					case 0x01: // Ctrl+A literal
						daemon.WriteFrame(conn, daemon.FrameInput, []byte{0x01})
						if len(data) > 1 {
							daemon.WriteFrame(conn, daemon.FrameInput, data[1:])
						}
						continue
					}
				}
			}

			// Check for Ctrl+A prefix
			for i, b := range data {
				if b == 0x01 { // Ctrl+A
					// Send everything before the prefix
					if i > 0 {
						daemon.WriteFrame(conn, daemon.FrameInput, data[:i])
					}
					if i+1 < len(data) {
						// Immediate character after prefix
						switch data[i+1] {
						case 'd':
							daemon.WriteFrame(conn, daemon.FrameDetach, nil)
							conn.Close()
							return
						case 'n':
							daemon.WriteFrame(conn, daemon.FrameSessionSwitch, daemon.MarshalJSON(daemon.SwitchPayload{Target: "__next"}))
						case 'p':
							daemon.WriteFrame(conn, daemon.FrameSessionSwitch, daemon.MarshalJSON(daemon.SwitchPayload{Target: "__prev"}))
						case 0x01:
							daemon.WriteFrame(conn, daemon.FrameInput, []byte{0x01})
						}
						// Continue with remaining data
						if i+2 < len(data) {
							daemon.WriteFrame(conn, daemon.FrameInput, data[i+2:])
						}
					} else {
						prefixActive = true
					}
					goto nextRead
				}
			}
			daemon.WriteFrame(conn, daemon.FrameInput, data)
		nextRead:
		}
	}()

	<-doneCh
	fmt.Fprintf(os.Stderr, "\r\n[detached from %s]\r\n", sessionName)
}

func handleNotification(n daemon.Notification, currentSession string) {
	switch n.Event {
	case "session_exited":
		fmt.Fprintf(os.Stderr, "\r\n[session %s exited (code %d)]\r\n", n.Session, n.ExitCode)
	case "session_switched":
		fmt.Fprintf(os.Stderr, "\r\n[switched to %s]\r\n", n.Session)
	}
}

func terminalSize() (uint16, uint16) {
	w, h, err := xterm.GetSize(int(os.Stdin.Fd()))
	if err != nil || w <= 0 || h <= 0 {
		return 80, 24
	}
	return uint16(w), uint16(h)
}

// ensureSocketDir computes the socket directory path.
func ensureSocketDir() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		return fmt.Sprintf("/tmp/codetty-%d", os.Getuid())
	}
	return dir
}

// formatSessionList formats session info for display.
func formatSessionList(sessions []daemon.SessionInfo) string {
	if len(sessions) == 0 {
		return "no sessions"
	}
	var sb strings.Builder
	for _, s := range sessions {
		fmt.Fprintf(&sb, "%s: %s (clients: %d, hub: %s)\n", s.Name, s.Command, s.Attached, s.HubStatus)
	}
	return sb.String()
}
