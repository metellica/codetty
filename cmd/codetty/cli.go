package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"codetty/daemon"
	"codetty/terminal"

	tea "github.com/charmbracelet/bubbletea"
	xterm "golang.org/x/term"
)

func cmdNew(args []string) {
	fs := flag.NewFlagSet("new", flag.ExitOnError)
	session := fs.String("s", "", "session name")
	fs.Parse(args)

	command := fs.Args()
	if len(command) == 0 {
		command = []string{terminal.DefaultShell()}
	}

	sockPath := defaultSocketPath()
	if err := ensureDaemon(sockPath); err != nil {
		log.Fatalf("cannot start daemon: %v", err)
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		log.Fatalf("connect to daemon: %v", err)
	}

	cols, rows := terminalSize()
	req := daemon.Request{
		Action:  daemon.ActionNewSession,
		Session: *session,
		Command: command,
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
		log.Fatalf("new session failed: %s", resp.Error)
	}
	conn.Close()

	startTUI(sockPath, resp.Session, nil)
}

func cmdAttach(args []string) {
	fs := flag.NewFlagSet("attach", flag.ExitOnError)
	session := fs.String("s", "", "session name (default: latest)")
	fs.Parse(args)

	sockPath := defaultSocketPath()
	if err := ensureDaemon(sockPath); err != nil {
		log.Fatalf("no daemon running")
	}

	startTUI(sockPath, *session, nil)
}

func startTUI(sockPath string, focusTarget string, autoCreate []string) {
	conn, err := newDaemonConn(sockPath)
	if err != nil {
		log.Fatalf("connect to daemon: %v", err)
	}

	model := newTUIModel(conn, focusTarget, autoCreate)
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())

	if err := conn.Subscribe(p); err != nil {
		log.Fatalf("subscribe: %v", err)
	}

	if _, err := p.Run(); err != nil {
		log.Fatalf("TUI: %v", err)
	}

	fmt.Fprintf(os.Stderr, "[detached]\n")
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

	fmt.Printf("%-16s %-12s %-8s %s\n", "NAME", "COMMAND", "CLIENTS", "CREATED")
	for _, s := range resp.Sessions {
		fmt.Printf("%-16s %-12s %-8d %s\n", s.Name, s.Command, s.Attached, s.CreatedAt)
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
		fmt.Fprintf(&sb, "%s: %s (clients: %d)\n", s.Name, s.Command, s.Attached)
	}
	return sb.String()
}
