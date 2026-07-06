package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codetty/config"
	"codetty/daemon"
	"codetty/terminal"

	tea "github.com/charmbracelet/bubbletea"
	xterm "golang.org/x/term"
)

// startupConfig holds TUI startup parameters.
type startupConfig struct {
	focusTarget string
	layout      string            // "focus-big", "grid", "fullscreen"
	order       []string          // desired session display order
	hidden      map[string]bool   // sessions hidden on startup
	autoCreate  []string          // command to auto-create if no sessions exist
}

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

	startTUI(sockPath, startupConfig{focusTarget: resp.Session})
}

func cmdAttach(args []string) {
	fs := flag.NewFlagSet("attach", flag.ExitOnError)
	session := fs.String("s", "", "session name (default: latest)")
	fs.Parse(args)

	sockPath := defaultSocketPath()
	if err := ensureDaemon(sockPath); err != nil {
		log.Fatalf("no daemon running")
	}

	if *session == "" {
		*session = latestSession(sockPath)
	}

	startTUI(sockPath, startupConfig{focusTarget: *session})
}

// latestSession queries the daemon for the most recently created active session.
func latestSession(sockPath string) string {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return ""
	}
	defer conn.Close()

	daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{Action: daemon.ActionList}))
	ft, payload, err := daemon.ReadFrame(conn)
	if err != nil || ft != daemon.FrameResponse {
		return ""
	}
	var resp daemon.Response
	if err := json.Unmarshal(payload, &resp); err != nil || !resp.OK {
		return ""
	}

	var latestName string
	var latest time.Time
	for _, s := range resp.Sessions {
		t, err := time.Parse(time.RFC3339, s.CreatedAt)
		if err != nil {
			continue
		}
		if t.After(latest) {
			latest = t
			latestName = s.Name
		}
	}
	return latestName
}

func startTUI(sockPath string, sc startupConfig) {
	conn, err := newDaemonConn(sockPath)
	if err != nil {
		log.Fatalf("connect to daemon: %v", err)
	}

	model := newTUIModel(conn, sc.focusTarget, sc.autoCreate)

	// Apply startup layout mode.
	switch sc.layout {
	case "grid":
		model.mode = modeGrid
	case "fullscreen":
		model.mode = modeFullscreen
	}

	// Apply hidden sessions and desired order from config.
	for name := range sc.hidden {
		model.hidden[name] = true
	}
	if len(sc.order) > 0 {
		model.desiredOrder = sc.order
	}

	if cfg, err := config.Load(); err != nil {
		log.Printf("config: %v (using default keybindings)", err)
	} else {
		model.prefixKey, model.prefixLabel = parsePrefixKey(cfg.Keybindings.Prefix)
	}
	iw := newImeWriter(os.Stdout)
	model.imeWriter = iw
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithOutput(iw))

	if err := conn.Subscribe(p); err != nil {
		log.Fatalf("subscribe: %v", err)
	}

	err = runWithAutowrapDisabled(os.Stdout, func() error {
		_, runErr := p.Run()
		return runErr
	})
	if err != nil {
		log.Fatalf("TUI: %v", err)
	}

	fmt.Fprintf(os.Stderr, "[detached]\n")
}

func cmdUp(args []string) {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	configFile := fs.String("f", "", "config file path")
	fs.Parse(args)

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}

	cfg, cfgPath, err := config.LoadSessions(*configFile, cwd)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	configDir := filepath.Dir(cfgPath)
	sessions, err := cfg.Resolve(configDir)
	if err != nil {
		log.Fatalf("resolve config: %v", err)
	}

	sockPath := defaultSocketPath()
	if err := ensureDaemon(sockPath); err != nil {
		log.Fatalf("cannot start daemon: %v", err)
	}

	existing := listExistingSessions(sockPath)
	cols, rows := terminalSize()

	for _, s := range sessions {
		if existing[s.Name] {
			fmt.Fprintf(os.Stderr, "session %q already exists, skipping\n", s.Name)
			continue
		}

		switch s.Type {
		case "tmux":
			if err := setupTmuxSession(s); err != nil {
				log.Printf("setup tmux %q: %v", s.Name, err)
				continue
			}
			// "=" forces exact session-name matching in tmux.
			cmd := []string{"tmux", "attach-session", "-t", "=" + s.TmuxSession}
			if err := createDaemonSession(sockPath, s.Name, cmd, cols, rows, "", nil, daemon.SessionTypeTmux, s.TmuxSession); err != nil {
				log.Printf("create session %q: %v", s.Name, err)
				continue
			}
			fmt.Fprintf(os.Stderr, "created tmux session %q\n", s.Name)

		default: // native
			cmd := s.Command
			if len(cmd) == 0 {
				cmd = []string{terminal.DefaultShell()}
			}
			if err := createDaemonSession(sockPath, s.Name, cmd, cols, rows, s.Dir, envToSlice(s.Env), "", ""); err != nil {
				log.Printf("create session %q: %v", s.Name, err)
				continue
			}
			fmt.Fprintf(os.Stderr, "created session %q\n", s.Name)
		}
	}

	// Build startup config for TUI.
	sc := startupConfig{
		focusTarget: cfg.Focus,
		layout:      cfg.Layout,
	}
	for _, s := range sessions {
		sc.order = append(sc.order, s.Name)
		if s.Hidden {
			if sc.hidden == nil {
				sc.hidden = make(map[string]bool)
			}
			sc.hidden[s.Name] = true
		}
	}
	if sc.focusTarget == "" && len(sessions) > 0 {
		sc.focusTarget = sessions[0].Name
	}

	startTUI(sockPath, sc)
}

func setupTmuxSession(s config.ResolvedSession) error {
	if s.TmuxAttachOnly {
		if !daemon.TmuxSessionExists(s.TmuxSession) {
			return fmt.Errorf("tmux session %q not found", s.TmuxSession)
		}
		return nil
	}
	windows := make([]daemon.TmuxWindowSetup, len(s.Windows))
	for i, w := range s.Windows {
		windows[i] = daemon.TmuxWindowSetup{
			Name: w.Name, Command: w.Command, Dir: w.Dir,
		}
	}
	_, err := daemon.SetupTmuxSessionWithWindows(s.TmuxSession, windows, envToSlice(s.Env))
	return err
}

// envToSlice converts an env map to sorted "KEY=value" entries.
func envToSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(env))
	for _, k := range keys {
		result = append(result, k+"="+env[k])
	}
	return result
}

func listExistingSessions(sockPath string) map[string]bool {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil
	}
	defer conn.Close()

	daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{Action: daemon.ActionList}))
	ft, payload, err := daemon.ReadFrame(conn)
	if err != nil || ft != daemon.FrameResponse {
		return nil
	}
	var resp daemon.Response
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		return nil
	}

	result := make(map[string]bool)
	for _, s := range resp.Sessions {
		result[s.Name] = true
	}
	return result
}

func createDaemonSession(sockPath, name string, cmd []string, cols, rows uint16, dir string, env []string, sessType, tmuxTarget string) error {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	req := daemon.Request{
		Action:     daemon.ActionNewSession,
		Session:    name,
		Command:    cmd,
		Cols:       cols,
		Rows:       rows,
		Dir:        dir,
		Env:        env,
		Type:       sessType,
		TmuxTarget: tmuxTarget,
		NoAttach:   true,
	}
	daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(req))

	ft, payload, err := daemon.ReadFrame(conn)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if ft != daemon.FrameResponse {
		return fmt.Errorf("unexpected frame type: %d", ft)
	}
	var resp daemon.Response
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
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
