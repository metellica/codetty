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

	"codetty/config"
	"codetty/daemon"
	"codetty/terminal"

	tea "github.com/charmbracelet/bubbletea"
	xterm "golang.org/x/term"
)

// startupConfig holds TUI startup parameters.
type startupConfig struct {
	focusTarget string
	layout      string          // "list", "focus-big", "fullscreen", "tabs"
	order       []string        // desired session display order
	hidden      map[string]bool // sessions hidden on startup
	autoCreate  []string        // command to auto-create if no sessions exist
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
		Owner:   localShadowID(),
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
	session := fs.String("s", "", "session name (default: last active)")
	fs.Parse(args)

	sockPath := defaultSocketPath()
	if err := ensureDaemon(sockPath); err != nil {
		log.Fatalf("no daemon running")
	}

	// No explicit target: leave focusTarget empty so the daemon-remembered
	// active session (falling back to the latest created) is restored.
	startTUI(sockPath, startupConfig{focusTarget: *session})
}

func startTUI(sockPath string, sc startupConfig) {
	endpoint := localDaemonEndpoint{sockPath: sockPath}
	startTUIWithEndpoints(map[string]daemonEndpoint{endpoint.ID(): endpoint}, sc)
}

func startTUIWithEndpoints(
	endpoints map[string]daemonEndpoint,
	sc startupConfig,
) {
	// Always use multi-peer mode: remote peers can then be added at
	// runtime (remote tabs), with the local daemon as the session owner.
	conn, err := newMultiDaemonConn(endpoints)
	if err != nil {
		log.Fatalf("connect to daemon: %v", err)
	}

	// Ad-hoc remote registry: remotes holding remote tabs from earlier
	// runs (created via the picker, not config) are reconnected in the
	// background so their tabs reappear; part changes keep the file
	// current.
	recorded := loadRecordedRemotes()
	seeds := make(map[string]recordedRemote)
	for name, def := range recorded {
		if e, err := newSSHDaemonEndpoint(name, def); err == nil {
			seeds[e.ID()] = recordedRemote{name: name, def: def}
		}
	}
	conn.remoteSeeds = seeds
	conn.persistRemotes = saveRecordedRemotes

	model := newTUIModel(conn, sc.focusTarget, sc.autoCreate)

	// Apply startup layout mode (default is modeFocusBig).
	switch sc.layout {
	case "fullscreen":
		model.mode = modeFullscreen
	case "list":
		model.mode = modeList
	case "tabs":
		model.mode = modeTabs
	default:
		model.mode = modeFocusBig
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

	reconnectRecordedRemotes(conn, p, recorded)

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
	endpoints, plans, err := buildConfigEndpoints(cfg, sessions, sockPath)
	if err != nil {
		log.Fatalf("resolve endpoints: %v", err)
	}
	if err := ensureEndpoints(endpoints); err != nil {
		log.Fatalf("prepare daemon: %v", err)
	}

	existingByEndpoint := make(map[string]map[string]bool, len(endpoints))
	for id, endpoint := range endpoints {
		current, err := querySessionsEndpoint(endpoint)
		if err != nil {
			log.Fatalf("list sessions on %s: %v", id, err)
		}
		existingByEndpoint[id] = endpointSessionNames(current)
	}
	cols, rows := terminalSize()

	for _, plan := range plans {
		for _, cp := range plan.Containers {
			endpoint := endpoints[cp.EndpointID]
			if existingByEndpoint[cp.EndpointID][cp.Container] {
				fmt.Fprintf(os.Stderr, "session %q already exists on %s, skipping\n", plan.Session.Name, cp.EndpointID)
				continue
			}

			first := cp.Tabs[0]
			cmd := first.Command
			if err := createDaemonSessionEndpoint(
				endpoint, cp.Container, cmd, cols, rows, first.Dir, envToSlice(first.Env), first.Name,
			); err != nil {
				log.Printf("create session %q on %s: %v", plan.Session.Name, cp.EndpointID, err)
				continue
			}
			existingByEndpoint[cp.EndpointID][cp.Container] = true
			fmt.Fprintf(os.Stderr, "created session %q on %s\n", plan.Session.Name, cp.EndpointID)

			for _, tab := range cp.Tabs[1:] {
				tabCmd := tab.Command
				if len(tabCmd) == 0 {
					tabCmd = cmd
				}
				if err := createDaemonTabEndpoint(endpoint, cp.Container, tab.Name, tabCmd, tab.Dir, envToSlice(tab.Env)); err != nil {
					log.Printf("create tab %q in session %q: %v", tab.Name, plan.Session.Name, err)
				} else {
					fmt.Fprintf(os.Stderr, "  created tab %q\n", tab.Name)
				}
			}
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

	startTUIWithEndpoints(endpoints, sc)
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

// listExistingSessions queries the daemon for current sessions.
func listExistingSessions(sockPath string) map[string]bool {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil
	}
	defer conn.Close()

	daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{Action: daemon.ActionList, Owner: localShadowID()}))
	ft, payload, err := daemon.ReadFrame(conn)
	if err != nil || ft != daemon.FrameResponse {
		return nil
	}
	var resp daemon.Response
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		return nil
	}

	names := make(map[string]bool)
	for _, s := range resp.Sessions {
		names[s.Name] = true
	}
	return names
}

func createDaemonSession(sockPath, name string, cmd []string, cols, rows uint16, dir string, env []string, tabName string) error {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	req := daemon.Request{
		Action:   daemon.ActionNewSession,
		Session:  name,
		Command:  cmd,
		Cols:     cols,
		Rows:     rows,
		Dir:      dir,
		Env:      env,
		NoAttach: true,
		TabName:  tabName,
		Owner:    localShadowID(),
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

// createDaemonTab creates a new tab within an existing session via the daemon.
func createDaemonTab(sockPath, session, tabName string, cmd []string, dir string, env []string) error {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	req := daemon.Request{
		Action:  daemon.ActionCreateTab,
		Session: session,
		TabName: tabName,
		Command: cmd,
		Dir:     dir,
		Env:     env,
		Owner:   localShadowID(),
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
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	all := fs.Bool("a", false, "list sessions from all instances and all owners on this host")
	fs.BoolVar(all, "all", false, "list sessions from all instances and all owners on this host")
	fs.Parse(args)

	if *all {
		listAllInstances()
		return
	}

	sockPath := defaultSocketPath()
	sessions, err := querySessions(sockPath, "")
	if err != nil {
		fmt.Println("no daemon running")
		return
	}
	if len(sessions) == 0 {
		fmt.Println("no sessions")
		return
	}
	printSessionTable(sessions)
}

// querySessions dials the daemon at sockPath and returns its session list.
// scope daemon.ScopeAll includes every owner namespace; "" only this
// client's own sessions.
func querySessions(sockPath, scope string) ([]daemon.SessionInfo, error) {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{
		Action: daemon.ActionList,
		Owner:  localShadowID(),
		Scope:  scope,
	}))
	ft, payload, err := daemon.ReadFrame(conn)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if ft != daemon.FrameResponse {
		return nil, fmt.Errorf("unexpected response")
	}
	var resp daemon.Response
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		return nil, fmt.Errorf("list failed: %s", resp.Error)
	}
	return resp.Sessions, nil
}

func printSessionTable(sessions []daemon.SessionInfo) {
	owners := false
	for _, s := range sessions {
		if s.Owner != "" {
			owners = true
			break
		}
	}
	if owners {
		fmt.Printf("%-16s %-10s %-12s %-8s %s\n", "NAME", "OWNER", "COMMAND", "CLIENTS", "CREATED")
		for _, s := range sessions {
			owner := s.Owner
			if owner == "" {
				owner = "(local)"
			}
			fmt.Printf("%-16s %-10s %-12s %-8d %s\n", s.Name, owner, s.Command, s.Attached, s.CreatedAt)
		}
		return
	}
	fmt.Printf("%-16s %-12s %-8s %s\n", "NAME", "COMMAND", "CLIENTS", "CREATED")
	for _, s := range sessions {
		fmt.Printf("%-16s %-12s %-8d %s\n", s.Name, s.Command, s.Attached, s.CreatedAt)
	}
}

// listAllInstances scans the runtime socket directory for codetty daemon
// sockets and lists the sessions of every reachable instance.
func listAllInstances() {
	dir := runtimeSocketDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Println("no daemon running")
		return
	}

	found := false
	for _, e := range entries {
		name := e.Name()
		var instance string
		switch {
		case name == "codetty.sock":
			instance = "(default)"
		case strings.HasPrefix(name, "codetty-") && strings.HasSuffix(name, ".sock"):
			instance = strings.TrimSuffix(strings.TrimPrefix(name, "codetty-"), ".sock")
		default:
			continue
		}

		sessions, err := querySessions(filepath.Join(dir, name), daemon.ScopeAll)
		if err != nil {
			continue // stale socket or unreachable daemon
		}
		if found {
			fmt.Println()
		}
		found = true
		fmt.Printf("instance %s:\n", instance)
		if len(sessions) == 0 {
			fmt.Println("no sessions")
			continue
		}
		printSessionTable(sessions)
	}
	if !found {
		fmt.Println("no daemon running")
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

	daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{Action: daemon.ActionKill, Session: *session, Owner: localShadowID()}))
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
	fs := flag.NewFlagSet("kill-server", flag.ExitOnError)
	all := fs.Bool("all", false, "also kill sessions owned by other hosts (stops the daemon unconditionally)")
	fs.Parse(args)

	sockPath := defaultSocketPath()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		fmt.Println("no daemon running")
		return
	}
	defer conn.Close()

	scope := ""
	if *all {
		scope = daemon.ScopeAll
	}
	daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{
		Action: daemon.ActionKillServer,
		Owner:  localShadowID(),
		Scope:  scope,
	}))
	daemon.ReadFrame(conn)
	fmt.Println("daemon stopped")
}

func cmdUpgrade(args []string) {
	fs := flag.NewFlagSet("upgrade", flag.ExitOnError)
	remote := fs.String("r", "", "remote to upgrade: a [remote.*] name, ssh host, or \"all\"")
	fs.Parse(args)

	if *remote != "" {
		cmdUpgradeRemote(*remote)
		return
	}

	// Determine the binary path to upgrade to
	binaryPath := ""
	if fs.NArg() > 0 {
		binaryPath = fs.Arg(0)
	} else {
		// Default: use the current executable (already rebuilt in place)
		exe, err := os.Executable()
		if err != nil {
			log.Fatalf("cannot determine executable: %v", err)
		}
		binaryPath = exe
	}

	sockPath := defaultSocketPath()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		fmt.Println("no daemon running")
		return
	}
	defer conn.Close()

	daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{
		Action:  daemon.ActionUpgrade,
		Session: binaryPath, // reuse Session field for binary path
	}))

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
		log.Fatalf("upgrade failed: %s", resp.Error)
	}
	fmt.Println("daemon upgrading... TUI clients will reconnect automatically")
}

// cmdUpgradeRemote deploys the local binary to one or all known remotes and
// hot-upgrades their daemons in place (running sessions survive).
func cmdUpgradeRemote(name string) {
	endpoints := make(map[string]*sshDaemonEndpoint)
	add := func(n string, def config.RemoteDef) {
		if _, ok := endpoints[n]; ok {
			return
		}
		if e, err := newSSHDaemonEndpoint(n, def); err == nil {
			endpoints[n] = e
		}
	}
	var cfg *config.SessionsConfig
	if cwd, err := os.Getwd(); err == nil {
		cfg, _, _ = config.LoadSessions("", cwd)
	}
	if cfg != nil {
		for n, def := range cfg.Remote {
			add(n, def)
		}
	}
	for n, def := range loadRecordedRemotes() {
		add(n, def)
	}

	var targets []*sshDaemonEndpoint
	if name == "all" {
		if len(endpoints) == 0 {
			log.Fatal("no remotes known (none in config [remote.*] or the recorded registry)")
		}
		names := make([]string, 0, len(endpoints))
		for n := range endpoints {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			targets = append(targets, endpoints[n])
		}
	} else {
		e := endpoints[name]
		if e == nil {
			// ~/.ssh/config aliases offered in the picker, then any
			// plain SSH host as a last resort.
			for _, t := range loadRemoteTargets() {
				if t.label == name {
					e, _ = t.endpoint.(*sshDaemonEndpoint)
					break
				}
			}
		}
		if e == nil {
			se, err := newSSHDaemonEndpoint(name, config.RemoteDef{Host: name})
			if err != nil {
				log.Fatalf("remote %q: %v", name, err)
			}
			e = se
		}
		targets = append(targets, e)
	}

	failed := false
	for _, e := range targets {
		fmt.Printf("[%s] deploying binary + upgrading daemon...\n", e.name)
		out, err := e.UpgradeDaemon()
		for _, line := range strings.Split(out, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				fmt.Printf("[%s] %s\n", e.name, line)
			}
		}
		if err != nil {
			failed = true
			fmt.Printf("[%s] error: %v\n", e.name, err)
		}
	}
	if failed {
		os.Exit(1)
	}
}

func cmdReload(args []string) {
	fs := flag.NewFlagSet("reload", flag.ExitOnError)
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
	endpoints, plans, err := buildConfigEndpoints(cfg, sessions, sockPath)
	if err != nil {
		log.Fatalf("resolve endpoints: %v", err)
	}
	if err := ensureEndpoints(endpoints); err != nil {
		log.Fatalf("prepare daemon: %v", err)
	}

	existing := make(map[string]map[string]daemon.SessionInfo, len(endpoints))
	for id, endpoint := range endpoints {
		current, err := querySessionsEndpoint(endpoint)
		if err != nil {
			log.Fatalf("list sessions on %s: %v", id, err)
		}
		existing[id] = endpointSessionsByName(current)
	}
	cols, rows := terminalSize()

	for _, plan := range plans {
		for _, cp := range plan.Containers {
			endpoint := endpoints[cp.EndpointID]
			current, exists := existing[cp.EndpointID][cp.Container]
			if !exists {
				// Create the container with its first tab
				first := cp.Tabs[0]
				if err := createDaemonSessionEndpoint(
					endpoint, cp.Container, first.Command, cols, rows, first.Dir, envToSlice(first.Env), first.Name,
				); err != nil {
					log.Printf("create session %q on %s: %v", plan.Session.Name, cp.EndpointID, err)
					continue
				}
				fmt.Fprintf(os.Stderr, "created session %q on %s\n", plan.Session.Name, cp.EndpointID)

				for _, tab := range cp.Tabs[1:] {
					if err := createDaemonTabEndpoint(endpoint, cp.Container, tab.Name, tab.Command, tab.Dir, envToSlice(tab.Env)); err != nil {
						log.Printf("  create tab %q: %v", tab.Name, err)
					}
				}
				continue
			}

			// Container exists — diff tabs via daemon
			currentTabs := make([]string, len(current.Tabs))
			for i, tab := range current.Tabs {
				currentTabs[i] = tab.Name
			}

			desiredNames := make(map[string]bool)
			for _, tab := range cp.Tabs {
				desiredNames[tab.Name] = true
			}

			// Close tabs not in desired (reverse order)
			for i := len(currentTabs) - 1; i >= 0; i-- {
				if !desiredNames[currentTabs[i]] {
					fmt.Fprintf(os.Stderr, "  closing tab %q in %q\n", currentTabs[i], plan.Session.Name)
					if err := closeDaemonTabEndpoint(endpoint, cp.Container, i); err != nil {
						log.Printf("  close tab %q: %v", currentTabs[i], err)
					}
				}
			}

			// Add tabs not in current
			currentSet := make(map[string]bool)
			for _, name := range currentTabs {
				currentSet[name] = true
			}
			for _, tab := range cp.Tabs {
				if !currentSet[tab.Name] {
					if err := createDaemonTabEndpoint(endpoint, cp.Container, tab.Name, tab.Command, tab.Dir, envToSlice(tab.Env)); err != nil {
						log.Printf("  create tab %q: %v", tab.Name, err)
					} else {
						fmt.Fprintf(os.Stderr, "  added tab %q to %q\n", tab.Name, plan.Session.Name)
					}
				}
			}
		}
	}

	fmt.Fprintln(os.Stderr, "config reloaded")
}

// getSessionTabs returns tab names for a session.
func getSessionTabs(sockPath, sessionName string) []string {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil
	}
	defer conn.Close()

	daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{Action: daemon.ActionList, Owner: localShadowID()}))
	ft, payload, err := daemon.ReadFrame(conn)
	if err != nil || ft != daemon.FrameResponse {
		return nil
	}
	var resp daemon.Response
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		return nil
	}

	for _, s := range resp.Sessions {
		if s.Name == sessionName {
			names := make([]string, len(s.Tabs))
			for i, t := range s.Tabs {
				names[i] = t.Name
			}
			return names
		}
	}
	return nil
}

// closeTab closes a tab in a session via the daemon.
func closeTab(sockPath, session string, index int) {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return
	}
	defer conn.Close()

	daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{
		Action:   daemon.ActionCloseTab,
		Session:  session,
		TabIndex: index,
		Owner:    localShadowID(),
	}))
	daemon.ReadFrame(conn)
}

func terminalSize() (uint16, uint16) {
	w, h, err := xterm.GetSize(int(os.Stdin.Fd()))
	if err != nil || w <= 0 || h <= 0 {
		return 80, 24
	}
	return uint16(w), uint16(h)
}

// formatSessionList formats session info for display.
func formatSessionList(sessions []daemon.SessionInfo) string {
	if len(sessions) == 0 {
		return "no sessions"
	}
	var sb strings.Builder
	for _, s := range sessions {
		fmt.Fprintf(&sb, "%s: %s (clients: %d)", s.Name, s.Command, s.Attached)
		if tabs := formatTabList(s.Tabs); tabs != "" {
			fmt.Fprintf(&sb, " [%s]", tabs)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// formatTabList renders tabs with agent states, e.g. "1:shell 2:copilot(blocked)".
// Returns "" when there is nothing worth showing (single tab, no agents).
func formatTabList(tabs []daemon.TabInfo) string {
	hasAgent := false
	for _, t := range tabs {
		if t.Agent != "" {
			hasAgent = true
			break
		}
	}
	if len(tabs) <= 1 && !hasAgent {
		return ""
	}
	parts := make([]string, len(tabs))
	for i, t := range tabs {
		part := fmt.Sprintf("%d:%s", i+1, t.Name)
		if t.Agent != "" {
			part += fmt.Sprintf(" %s(%s)", t.Agent, t.AgentState)
		}
		parts[i] = part
	}
	return strings.Join(parts, " ")
}
