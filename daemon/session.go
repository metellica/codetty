package daemon

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"codetty/agentdetect"
	"codetty/terminal"
)

// SessionType identifies the kind of session.
const (
	SessionTypeNative = "native"
)

// SessionTab represents a single tab (PTY) within a DaemonSession.
type SessionTab struct {
	Name     string
	Command  []string
	Dir      string // working directory; respawned shells start here
	Terminal terminal.Session
	Screen   *terminal.ScreenMirror

	exited    bool
	exitCode  int
	closing   bool // set when the tab is being closed deliberately (CloseTab)
	workspace bool // Dir was explicitly requested (config workspace tab); shell exits respawn instead of auto-closing
	doneCh   chan struct{}

	// Agent detection state (owned by the server's agent poll loop).
	// contentSeq counts PTY output writes so unchanged screens can skip
	// rescanning. agent/agentState are the last published values,
	// protected by the session mutex.
	contentSeq atomic.Uint64
	detector   agentdetect.Detector
	agent      string
	agentState agentdetect.AgentState
}

// DaemonSession represents a managed session within the daemon.
// A session contains one or more tabs, each with its own PTY.
type DaemonSession struct {
	Name    string
	Command []string // command of the first/default tab
	Type    string

	// Legacy single-tab fields kept in sync with the active tab.
	Terminal terminal.Session
	Screen   *terminal.ScreenMirror

	// Multi-tab support (native sessions only)
	Tabs      []*SessionTab
	ActiveTab int // index of the currently active tab

	CreatedAt time.Time
	onOutput  func(session string, data []byte)
	// onTabsChanged is invoked when the daemon changes the tab list on its
	// own (auto-close of an exited shell tab, shell respawn in a tab whose
	// command crashed) so the server can notify subscribers.
	onTabsChanged func(sess *DaemonSession, event string)

	mu       sync.Mutex
	clients  []*Client
	exited   bool
	exitCode int
	killed   bool // session killed deliberately; suppress tab respawn/auto-close
	doneCh   chan struct{}
}

func newDaemonSession(name string, command []string, cols, rows uint16, dir string, env []string, onOutput func(session string, data []byte)) (*DaemonSession, error) {
	term, err := terminal.Start(command, cols, rows, dir, env)
	if err != nil {
		return nil, err
	}

	screen := terminal.NewScreenMirror(cols, rows)
	tab := &SessionTab{
		Name:      "default",
		Command:   command,
		Dir:       dir,
		workspace: dir != "",
		Terminal:  term,
		Screen:    screen,
		doneCh:    make(chan struct{}),
	}

	s := &DaemonSession{
		Name:      name,
		Command:   command,
		Type:      SessionTypeNative,
		Terminal:  term,
		Screen:    screen,
		Tabs:      []*SessionTab{tab},
		ActiveTab: 0,
		CreatedAt: time.Now(),
		onOutput:  onOutput,
		doneCh:    make(chan struct{}),
	}

	go s.readLoopForTab(tab, term)
	go s.waitLoopForTab(tab, term)

	return s, nil
}

// AddTab creates a new tab within this session and returns its index.
// When dir is empty the tab inherits the working directory of the active
// tab (its process's live cwd when readable, else its recorded Dir), so
// new tabs and later shell respawns stay in the session's directory
// instead of falling back to the daemon's cwd.
func (s *DaemonSession) AddTab(name string, command []string, cols, rows uint16, dir string, env []string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	workspace := dir != ""
	if dir == "" {
		dir = s.inheritDirLocked()
	}

	term, err := terminal.Start(command, cols, rows, dir, env)
	if err != nil && dir != "" && !workspace {
		// Inherited dir may have vanished; retry in the daemon's cwd.
		dir = ""
		term, err = terminal.Start(command, cols, rows, dir, env)
	}
	if err != nil {
		return -1, err
	}

	screen := terminal.NewScreenMirror(cols, rows)
	tab := &SessionTab{
		Name:      name,
		Command:   command,
		Dir:       dir,
		workspace: workspace,
		Terminal:  term,
		Screen:    screen,
		doneCh:    make(chan struct{}),
	}

	s.Tabs = append(s.Tabs, tab)
	idx := len(s.Tabs) - 1

	go s.readLoopForTab(tab, term)
	go s.waitLoopForTab(tab, term)

	return idx, nil
}

// inheritDirLocked returns the working directory a new tab should inherit:
// the live cwd of the active tab's process when readable, else the active
// tab's recorded Dir, else the first non-empty tab Dir. Caller holds s.mu.
func (s *DaemonSession) inheritDirLocked() string {
	if len(s.Tabs) == 0 || s.ActiveTab < 0 || s.ActiveTab >= len(s.Tabs) {
		return ""
	}
	active := s.Tabs[s.ActiveTab]
	if active.Terminal != nil {
		if cwd := terminal.ProcessCwd(active.Terminal.Pid()); cwd != "" {
			return cwd
		}
	}
	if active.Dir != "" {
		return active.Dir
	}
	for _, t := range s.Tabs {
		if t.Dir != "" {
			return t.Dir
		}
	}
	return ""
}

// SwitchTab changes the active tab to the given index.
func (s *DaemonSession) SwitchTab(idx int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if idx < 0 || idx >= len(s.Tabs) {
		return fmt.Errorf("tab index %d out of range (0-%d)", idx, len(s.Tabs)-1)
	}

	s.ActiveTab = idx
	tab := s.Tabs[idx]
	s.Terminal = tab.Terminal
	s.Screen = tab.Screen
	return nil
}

// CloseTab closes a tab at the given index. Returns error if it's the last tab.
func (s *DaemonSession) CloseTab(idx int) error {
	s.mu.Lock()
	if idx < 0 || idx >= len(s.Tabs) {
		s.mu.Unlock()
		return fmt.Errorf("tab index %d out of range", idx)
	}
	if len(s.Tabs) == 1 {
		s.mu.Unlock()
		return fmt.Errorf("cannot close the last tab")
	}

	tab := s.Tabs[idx]
	tab.closing = true
	s.Tabs = append(s.Tabs[:idx], s.Tabs[idx+1:]...)

	// Adjust active tab index
	if s.ActiveTab >= len(s.Tabs) {
		s.ActiveTab = len(s.Tabs) - 1
	} else if s.ActiveTab > idx {
		s.ActiveTab--
	}

	// Update Terminal/Screen references to active tab
	activeTab := s.Tabs[s.ActiveTab]
	s.Terminal = activeTab.Terminal
	s.Screen = activeTab.Screen
	s.mu.Unlock()

	// Kill the closed tab's PTY
	tab.Terminal.Kill()
	tab.Terminal.Close()
	return nil
}

// TabCount returns the number of tabs in this session.
func (s *DaemonSession) TabCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.Tabs)
}

// GetActiveTab returns the index of the active tab.
func (s *DaemonSession) GetActiveTab() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ActiveTab
}

// TabInfos returns tab information for all tabs.
func (s *DaemonSession) TabInfos() []TabInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	infos := make([]TabInfo, len(s.Tabs))
	for i, tab := range s.Tabs {
		cmd := ""
		if len(tab.Command) > 0 {
			cmd = tab.Command[0]
		}
		infos[i] = TabInfo{
			Name:       tab.Name,
			Command:    cmd,
			Agent:      tab.agent,
			AgentState: string(tab.agentState),
		}
	}
	return infos
}

// PollAgentStates runs agent detection for every tab in a native session.
// It returns true when any tab's published agent or state changed.
// Called periodically by the server's agent poll loop.
func (s *DaemonSession) PollAgentStates(now time.Time) bool {
	s.mu.Lock()
	tabs := append([]*SessionTab(nil), s.Tabs...)
	s.mu.Unlock()

	changedAny := false
	for _, tab := range tabs {
		if s.pollTabAgentState(tab, now) {
			changedAny = true
		}
	}
	return changedAny
}

func (s *DaemonSession) pollTabAgentState(tab *SessionTab, now time.Time) bool {
	seq := tab.contentSeq.Load()
	if !tab.detector.NeedsScan(seq, now) {
		return false
	}

	s.mu.Lock()
	exited := tab.exited
	s.mu.Unlock()

	agentName := ""
	raw := agentdetect.StateUnknown
	if !exited {
		if name, argv, ok := agentdetect.ForegroundCommand(tab.Terminal.Fd()); ok {
			if spec := agentdetect.Identify(name, argv); spec != nil {
				agentName = spec.Name
				raw = spec.Evaluate(tab.Screen.TailText(0))
			}
		}
	}

	state, changed := tab.detector.Commit(agentName, raw, seq, now)
	if !changed {
		return false
	}

	s.mu.Lock()
	tab.agent = agentName
	tab.agentState = state
	s.mu.Unlock()
	return true
}

func (s *DaemonSession) readLoopForTab(tab *SessionTab, term terminal.Session) {
	buf := make([]byte, 4096)
	for {
		n, err := term.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			tab.Screen.Write(data)
			tab.contentSeq.Add(1)

			// Only broadcast output if this is the active tab
			s.mu.Lock()
			isActive := s.Tabs[s.ActiveTab] == tab
			s.mu.Unlock()

			if isActive {
				s.broadcastOutput(data)
				if s.onOutput != nil {
					broadcast := make([]byte, len(data))
					copy(broadcast, data)
					s.onOutput(s.Name, broadcast)
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *DaemonSession) waitLoopForTab(tab *SessionTab, term terminal.Session) {
	err := term.Wait()
	s.mu.Lock()
	tab.exited = true
	if err != nil {
		tab.exitCode = 1
	}
	// Deliberate kills (session kill, tab close, daemon shutdown) keep the
	// plain exit semantics; only normal tab exits get respawn/auto-close
	// treatment.
	suppress := s.killed || tab.closing
	s.mu.Unlock()
	close(tab.doneCh)

	if !suppress {
		if !isShellCommand(tab.Command) {
			// A non-shell command exited: fall back to the default shell
			// in the same tab so the panel stays usable.
			if s.respawnTabShell(tab) {
				return
			}
		} else if tab.workspace {
			// A shell tab pinned to an explicit workspace dir exited:
			// respawn the shell there instead of auto-closing.
			if s.respawnTabShell(tab) {
				return
			}
		} else if s.autoCloseTab(tab) {
			// A shell tab exited (user typed "exit"): close the tab and
			// fall back to the remaining tabs.
			return
		}
	}

	// Check if all tabs have exited. The exited guard makes concurrent
	// tab exits close doneCh exactly once.
	s.mu.Lock()
	allExited := true
	for _, t := range s.Tabs {
		if !t.exited {
			allExited = false
			break
		}
	}
	if allExited && !s.exited {
		s.exited = true
		s.mu.Unlock()
		close(s.doneCh)
		return
	}
	s.mu.Unlock()
}

// shellNames are command basenames treated as interactive shells: a tab
// running one of these auto-closes when it exits instead of respawning.
var shellNames = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true,
	"ksh": true, "tcsh": true, "csh": true, "nu": true, "elvish": true,
	"pwsh": true, "powershell": true, "cmd": true,
}

// isShellCommand reports whether a tab command is an interactive shell.
func isShellCommand(command []string) bool {
	if len(command) == 0 {
		return true
	}
	name := command[0]
	// Handle both separators so Windows paths parse on any platform.
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSuffix(strings.ToLower(name), ".exe")
	return shellNames[name]
}

// respawnTabShell restarts the default shell inside a tab whose command
// exited. Returns false when the tab is gone or the shell cannot start,
// in which case the caller falls back to the exit path.
func (s *DaemonSession) respawnTabShell(tab *SessionTab) bool {
	cols, rows := tab.Screen.Size()
	shell := terminal.DefaultShell()
	term, err := terminal.Start([]string{shell}, cols, rows, tab.Dir, nil)
	if err != nil && tab.Dir != "" {
		// The recorded dir may have been deleted; retry in the daemon's cwd
		// so the panel stays usable.
		term, err = terminal.Start([]string{shell}, cols, rows, "", nil)
	}
	if err != nil {
		return false
	}

	s.mu.Lock()
	inSession := false
	for _, t := range s.Tabs {
		if t == tab {
			inSession = true
			break
		}
	}
	if !inSession || s.killed {
		s.mu.Unlock()
		term.Kill()
		term.Close()
		return false
	}
	tab.Terminal = term
	tab.Command = []string{shell}
	tab.exited = false
	tab.exitCode = 0
	tab.doneCh = make(chan struct{})
	tab.agent = ""
	tab.agentState = ""
	isActive := s.Tabs[s.ActiveTab] == tab
	if isActive {
		s.Terminal = term
	}
	onTabsChanged := s.onTabsChanged
	s.mu.Unlock()

	notice := []byte("\r\n\x1b[90m[process exited — restarted " + shell + "]\x1b[0m\r\n")
	tab.Screen.Write(notice)
	tab.contentSeq.Add(1)
	if isActive {
		s.broadcastOutput(notice)
		if s.onOutput != nil {
			s.onOutput(s.Name, notice)
		}
	}

	go s.readLoopForTab(tab, term)
	go s.waitLoopForTab(tab, term)

	if onTabsChanged != nil {
		onTabsChanged(s, "tab_created")
	}
	return true
}

// autoCloseTab removes an exited tab from the session, switching the
// active tab if needed. Returns false when it is the last tab, letting
// the normal session-exit path run.
func (s *DaemonSession) autoCloseTab(tab *SessionTab) bool {
	s.mu.Lock()
	idx := -1
	for i, t := range s.Tabs {
		if t == tab {
			idx = i
			break
		}
	}
	if idx < 0 || len(s.Tabs) == 1 {
		s.mu.Unlock()
		return false
	}
	s.Tabs = append(s.Tabs[:idx], s.Tabs[idx+1:]...)
	if s.ActiveTab >= len(s.Tabs) {
		s.ActiveTab = len(s.Tabs) - 1
	} else if s.ActiveTab > idx {
		s.ActiveTab--
	}
	active := s.Tabs[s.ActiveTab]
	s.Terminal = active.Terminal
	s.Screen = active.Screen
	onTabsChanged := s.onTabsChanged
	s.mu.Unlock()

	tab.Terminal.Close()
	if onTabsChanged != nil {
		onTabsChanged(s, "tab_closed")
	}
	return true
}

// readLoop is kept for the legacy single-tab path.
func (s *DaemonSession) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := s.Terminal.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			s.Screen.Write(data)
			s.broadcastOutput(data)
			if s.onOutput != nil {
				broadcast := make([]byte, len(data))
				copy(broadcast, data)
				s.onOutput(s.Name, broadcast)
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *DaemonSession) waitLoop() {
	err := s.Terminal.Wait()
	s.mu.Lock()
	s.exited = true
	if err != nil {
		s.exitCode = 1
	}
	s.mu.Unlock()
	close(s.doneCh)
}

// setTabsChangedCallback installs the daemon-side tab change notifier.
func (s *DaemonSession) setTabsChangedCallback(fn func(sess *DaemonSession, event string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onTabsChanged = fn
}

func (s *DaemonSession) broadcastOutput(data []byte) {
	s.mu.Lock()
	clients := append([]*Client(nil), s.clients...)
	s.mu.Unlock()

	for _, c := range clients {
		c.SendOutput(data)
	}
}

func (s *DaemonSession) addClient(c *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients = append(s.clients, c)
}

func (s *DaemonSession) removeClient(c *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, cl := range s.clients {
		if cl == c {
			s.clients = append(s.clients[:i], s.clients[i+1:]...)
			return
		}
	}
}

func (s *DaemonSession) clientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

func (s *DaemonSession) isExited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited
}

// Resize resizes the active tab's PTY and screen mirror.
func (s *DaemonSession) Resize(cols, rows uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, tab := range s.Tabs {
		tab.Terminal.Resize(cols, rows)
		tab.Screen.Resize(cols, rows)
	}
}

// Kill terminates all tabs in the session.
func (s *DaemonSession) Kill() {
	s.mu.Lock()
	s.killed = true
	tabs := append([]*SessionTab(nil), s.Tabs...)
	s.mu.Unlock()

	for _, tab := range tabs {
		tab.Terminal.Kill()
		tab.Terminal.Close()
	}
}

// Info returns a SessionInfo snapshot for listing.
func (s *DaemonSession) Info() SessionInfo {
	cmd := ""
	if len(s.Command) > 0 {
		cmd = s.Command[0]
	}
	info := SessionInfo{
		Name:      s.Name,
		Command:   cmd,
		Type:      s.Type,
		Attached:  s.clientCount(),
		CreatedAt: s.CreatedAt.Format(time.RFC3339),
	}
	if len(s.Tabs) >= 1 {
		info.Tabs = s.TabInfos()
		info.ActiveTab = s.GetActiveTab()
	}
	return info
}

// autoSessionName generates a unique session name for the given command.
func autoSessionName(command []string, existing map[string]*DaemonSession) string {
	base := "session"
	if len(command) > 0 {
		base = command[0]
	}
	for i := 0; ; i++ {
		name := fmt.Sprintf("%s-%d", base, i)
		if _, exists := existing[name]; !exists {
			return name
		}
	}
}
