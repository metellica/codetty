package daemon

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"codetty/agentdetect"
	"codetty/terminal"
)

// SessionType identifies the kind of session.
const (
	SessionTypeNative = "native"
	SessionTypeTmux   = "tmux"
)

// SessionTab represents a single tab (PTY) within a DaemonSession.
type SessionTab struct {
	Name     string
	Command  []string
	Terminal terminal.Session
	Screen   *terminal.ScreenMirror

	exited   bool
	exitCode int
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
	Name       string
	Command    []string // command of the first/default tab
	Type       string   // SessionTypeNative or SessionTypeTmux
	TmuxTarget string   // original tmux session name (only when Type==SessionTypeTmux)

	// Legacy single-tab fields (used for tmux sessions or single-tab native sessions)
	Terminal terminal.Session
	Screen   *terminal.ScreenMirror

	// Multi-tab support (native sessions only)
	Tabs      []*SessionTab
	ActiveTab int // index of the currently active tab

	CreatedAt time.Time
	onOutput  func(session string, data []byte)

	mu       sync.Mutex
	clients  []*Client
	exited   bool
	exitCode int
	doneCh   chan struct{}
}

func newDaemonSession(name string, command []string, cols, rows uint16, dir string, env []string, onOutput func(session string, data []byte)) (*DaemonSession, error) {
	term, err := terminal.Start(command, cols, rows, dir, env)
	if err != nil {
		return nil, err
	}

	screen := terminal.NewScreenMirror(cols, rows)
	tab := &SessionTab{
		Name:     "default",
		Command:  command,
		Terminal: term,
		Screen:   screen,
		doneCh:   make(chan struct{}),
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

	go s.readLoopForTab(tab)
	go s.waitLoopForTab(tab)

	return s, nil
}

// AddTab creates a new tab within this session and returns its index.
func (s *DaemonSession) AddTab(name string, command []string, cols, rows uint16, dir string, env []string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Type == SessionTypeTmux {
		return -1, fmt.Errorf("tabs are not supported for tmux sessions")
	}

	term, err := terminal.Start(command, cols, rows, dir, env)
	if err != nil {
		return -1, err
	}

	screen := terminal.NewScreenMirror(cols, rows)
	tab := &SessionTab{
		Name:     name,
		Command:  command,
		Terminal: term,
		Screen:   screen,
		doneCh:   make(chan struct{}),
	}

	s.Tabs = append(s.Tabs, tab)
	idx := len(s.Tabs) - 1

	go s.readLoopForTab(tab)
	go s.waitLoopForTab(tab)

	return idx, nil
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
	if s.Type != SessionTypeNative {
		return false
	}

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

func (s *DaemonSession) readLoopForTab(tab *SessionTab) {
	buf := make([]byte, 4096)
	for {
		n, err := tab.Terminal.Read(buf)
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

func (s *DaemonSession) waitLoopForTab(tab *SessionTab) {
	err := tab.Terminal.Wait()
	s.mu.Lock()
	tab.exited = true
	if err != nil {
		tab.exitCode = 1
	}
	s.mu.Unlock()
	close(tab.doneCh)

	// Check if all tabs have exited
	s.mu.Lock()
	allExited := true
	for _, t := range s.Tabs {
		if !t.exited {
			allExited = false
			break
		}
	}
	if allExited {
		s.exited = true
	}
	s.mu.Unlock()

	if allExited {
		close(s.doneCh)
	}
}

// readLoop is kept for backward compatibility (tmux sessions use single-tab path).
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
	tabs := append([]*SessionTab(nil), s.Tabs...)
	s.mu.Unlock()

	for _, tab := range tabs {
		tab.Terminal.Kill()
		tab.Terminal.Close()
	}
}

// KillWithTmux kills the session and, if it wraps a tmux session, kills that too.
func (s *DaemonSession) KillWithTmux() {
	if s.Type == SessionTypeTmux && s.TmuxTarget != "" {
		KillTmuxSession(s.TmuxTarget)
	}
	s.Kill()
}

// Info returns a SessionInfo snapshot for listing.
func (s *DaemonSession) Info() SessionInfo {
	cmd := ""
	if len(s.Command) > 0 {
		cmd = s.Command[0]
	}
	info := SessionInfo{
		Name:       s.Name,
		Command:    cmd,
		Type:       s.Type,
		TmuxTarget: s.TmuxTarget,
		Attached:   s.clientCount(),
		CreatedAt:  s.CreatedAt.Format(time.RFC3339),
	}
	if s.Type == SessionTypeNative && len(s.Tabs) >= 1 {
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
