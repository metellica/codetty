package main

import (
	"time"

	"codetty/config"
	"codetty/daemon"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	tuiTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("86")).Bold(true)

	tuiStatusStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("235")).
			Foreground(lipgloss.Color("250")).
			Padding(0, 1)

	tuiFocusBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("86"))

	tuiNormalBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240"))
)

type layoutMode int

const (
	modeList       layoutMode = iota
	modeFullscreen            // no borders, no status bar, just terminal
	modeFocusBig              // list-style big panel + switch-only side panels
)

// tuiModel is the Bubble Tea model for the unified codetty TUI.
type tuiModel struct {
	conn      *daemonConn
	imeWriter *imeWriter // nil in tests; wraps stdout to fix IME cursor position
	sessions  []*tuiSession
	order     []string // session names in display order
	selected  int      // selected session
	mode      layoutMode

	prefix bool // prefix key active (see prefixKey)
	width  int
	height int

	// prefixKey/prefixLabel: the configured prefix key (default Ctrl+A) and
	// its display label (e.g. "Ctrl+A"), set from config after construction.
	prefixKey   tea.KeyType
	prefixLabel string

	// List mode (modeList) sidebar state. listFocus means the sidebar has
	// keyboard focus (Shift+Tab); listCursor is the highlighted entry,
	// listCursorTab the highlighted agent row's tab index (-1 = the session
	// header row) and listScroll the first visible sidebar line.
	listFocus     bool
	listCursor    int
	listCursorTab int
	listScroll    int

	// Focus-big mode side-panel highlight (Shift+Tab): the highlighted
	// visible-session index, -1 when the big panel has keyboard focus.
	// Enter opens the highlighted session in a floating window, Space
	// promotes it to the big panel.
	focusCursor int

	// floatingIdx: index of session shown in floating overlay, -1 means none.
	floatingIdx int

	// modeStack records previous states for Ctrl+A f back-navigation.
	modeStack []modeState

	// Rename mode
	renaming   bool
	renameText string

	// Create session mode
	creating   bool
	createText string

	// Create tab mode
	creatingTab   bool
	createTabText string

	// Text selection
	sel textSelection

	// Selection-drag frame throttling: during rapid mouse-motion drags we
	// reuse the last rendered frame if it is fresher than selViewInterval,
	// so the Bubble Tea message queue never backs up behind slow renders.
	selViewAt       time.Time
	selViewSkip     bool // next View() may return lastView
	selFlushPending bool // a trailing selFlushMsg tick is scheduled
	lastView        string

	// blinkPending is true while an agentBlinkMsg tick is scheduled. The
	// tick self-perpetuates only while some agent is working.
	blinkPending bool

	// mouseFwdDown is true while a left-button press has been forwarded to
	// a mouse-enabled app in the big panel, so the matching drag/release
	// events are forwarded too (and spurious releases are not).
	mouseFwdDown bool

	// Hidden sessions (still running but not displayed)
	hidden map[string]bool

	// Kill confirmation
	confirmKill     bool
	confirmKillName string

	// Session manager overlay
	sessMgr sessionManager

	// Session switcher overlay (fullscreen Shift+Tab)
	switcher sessionSwitcher

	// Initial focus target (from new/attach command)
	focusTarget string
	// When set, open this session in a floating window once created
	floatingTarget string
	// Auto-create session on start
	autoCreate []string // command to auto-create

	// Desired session order from config (applied after initial snapshots).
	desiredOrder []string

	// Pending tabs from config reload (created when session appears).
	pendingReloadTabs map[string][]config.ResolvedTab

	// pendingTabAck records sessions where this client requested a tab
	// switch; the agent-done marker of the new tab is acknowledged when
	// the daemon's tab_switched event arrives.
	pendingTabAck map[string]bool
}

// modeState captures a view state for back-navigation.
type modeState struct {
	mode        layoutMode
	selected    int
	floatingIdx int
}

// pushMode saves current state and transitions to a new mode/floating state.
func (m *tuiModel) pushMode() {
	m.modeStack = append(m.modeStack, modeState{
		mode:        m.mode,
		selected:    m.selected,
		floatingIdx: m.floatingIdx,
	})
}

// popMode restores the previous state. Returns false if stack is empty.
func (m *tuiModel) popMode() bool {
	if len(m.modeStack) == 0 {
		return false
	}
	prev := m.modeStack[len(m.modeStack)-1]
	m.modeStack = m.modeStack[:len(m.modeStack)-1]
	m.mode = prev.mode
	m.selected = prev.selected
	m.floatingIdx = prev.floatingIdx
	return true
}

func newTUIModel(conn *daemonConn, focusTarget string, autoCreate []string) *tuiModel {
	return &tuiModel{
		conn:              conn,
		mode:              modeFocusBig,
		listCursorTab:     -1,
		focusCursor:       -1,
		width:             120,
		height:            40,
		floatingIdx:       -1,
		hidden:            make(map[string]bool),
		pendingReloadTabs: make(map[string][]config.ResolvedTab),
		focusTarget:       focusTarget,
		autoCreate:        autoCreate,
		prefixKey:         tea.KeyCtrlA,
		prefixLabel:       "Ctrl+A",
	}
}

func (m *tuiModel) Init() tea.Cmd {
	return tea.Batch(tea.WindowSize(), tea.EnableMouseCellMotion)
}

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	prevName, prevTab := m.attentionTarget()
	model, cmd := m.update(msg)
	// A done marker is acknowledged only when the user engages with the
	// tab: navigating to it (attention target change caused by key/mouse)
	// or sending input into it (acked at the forwarding sites).
	switch msg.(type) {
	case tea.KeyMsg, tea.MouseMsg:
		if name, tab := m.attentionTarget(); name != "" && (name != prevName || tab != prevTab) {
			m.ackAgentDoneSession(name)
		}
	}
	return model, tea.Batch(cmd, m.scheduleBlink())
}

// agentBlinkInterval is the working-badge solid/hollow animation period.
const agentBlinkInterval = 600 * time.Millisecond

// agentBlinkMsg advances the working-badge animation phase.
type agentBlinkMsg struct{}

// scheduleBlink starts the working-badge animation tick when some agent
// is working and no tick is already in flight.
func (m *tuiModel) scheduleBlink() tea.Cmd {
	if m.blinkPending || !m.anyAgentWorking() {
		return nil
	}
	m.blinkPending = true
	return tea.Tick(agentBlinkInterval, func(time.Time) tea.Msg { return agentBlinkMsg{} })
}

// anyAgentWorking reports whether any tab of any session (including
// hidden ones — the status bar summary shows them) is in working state.
func (m *tuiModel) anyAgentWorking() bool {
	for _, sess := range m.sessions {
		for _, tab := range sess.Tabs {
			if effectiveAgentState(tab) == "working" {
				return true
			}
		}
	}
	return false
}

func (m *tuiModel) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeSessionPTYs()

	case daemonOutputMsg:
		if sess := m.sessionByName(msg.Session); sess != nil {
			sess.WriteOutput(msg.Data)
		}

	case daemonSessionEventMsg:
		m.handleSessionEvent(msg.Event)
		if msg.Event.Type == "created" {
			m.resizeSessionPTYs()
		}
		return m, tea.ClearScreen

	case daemonSubscribedMsg:
		m.maybeAutoCreate()
		m.applyDesiredOrder()
		// Initial snapshots arrive with the daemon-side (stale) session sizes.
		// Resize everything to the actual panel content size now that all
		// initial snapshots have been received.
		if len(m.sessions) > 0 && m.width > 0 && m.height > 0 {
			m.resizeSessionPTYs()
		}

	case daemonDisconnectedMsg:
		// The daemon dropped us — during a hot upgrade it closes all
		// subscriber connections and rebinds the socket. Try to
		// reconnect before giving up.
		if m.conn == nil || m.conn.sockPath == "" {
			return m, tea.Quit
		}
		return m, reconnectCmd(m.conn.sockPath)

	case daemonReconnectedMsg:
		prog := m.conn.program
		m.conn = msg.conn
		// Rebuild session state from the fresh subscribe snapshots.
		m.sessions = nil
		m.order = nil
		m.selected = 0
		m.floatingIdx = -1
		m.listFocus = false
		m.listCursor = 0
		m.listCursorTab = -1
		m.listScroll = 0
		m.focusCursor = -1
		m.sel = textSelection{}
		if prog == nil || m.conn.Subscribe(prog) != nil {
			return m, tea.Quit
		}
		return m, tea.ClearScreen

	case daemonReconnectFailedMsg:
		return m, tea.Quit

	case tea.KeyMsg:
		if m.confirmKill {
			return m, m.handleConfirmKillKey(msg)
		}
		if m.sessMgr.active {
			return m, m.handleSessionManagerKey(msg)
		}
		return m, m.handleKey(msg)

	case tea.MouseMsg:
		if m.renaming || m.creating || m.creatingTab || m.confirmKill || m.sessMgr.active {
			return m, nil
		}
		if m.switcher.active {
			return m, m.handleSwitcherMouse(msg)
		}
		return m, m.handleMouse(msg)

	case selFlushMsg:
		// Trailing edge of a throttled selection drag: render a fresh frame.
		m.selFlushPending = false
		m.selViewAt = time.Now()

	case agentBlinkMsg:
		m.blinkPending = false
		if m.anyAgentWorking() {
			agentBlinkSolid = !agentBlinkSolid
		} else {
			// Animation stops on the solid phase so static renders (and
			// tests) always see ●.
			agentBlinkSolid = true
		}
	}
	return m, nil
}

func (m *tuiModel) View() string {
	// Reuse the previous frame during throttled selection drags. The flag is
	// set only by mouse-motion updates that changed nothing but the selection
	// endpoint, and a trailing selFlushMsg guarantees a fresh final frame.
	if m.selViewSkip {
		m.selViewSkip = false
		if m.lastView != "" {
			return m.lastView
		}
	}

	if len(m.sessions) == 0 {
		hint := "No sessions. Creating one..."
		if m.autoCreate == nil {
			hint = "No sessions. Press " + m.prefixLabel + " c to create one."
		}
		// Only show the status bar when in an interactive overlay state.
		hasOverlay := m.renaming || m.creating || m.creatingTab || m.prefix
		centerH := m.height
		var status string
		if hasOverlay {
			status = m.statusBar()
			centerH = m.height - lipgloss.Height(status)
		}
		if centerH < 1 {
			centerH = 1
		}
		body := lipgloss.Place(m.width, centerH, lipgloss.Center, lipgloss.Center,
			tuiTitleStyle.Render("⬡ codetty")+"\n\n"+hint)
		base := body
		if hasOverlay {
			base += "\n" + status
		}
		return base
	}

	var base string
	switch m.mode {
	case modeFullscreen:
		base = m.fullscreenView()
	case modeFocusBig:
		base = m.focusBigView()
	default:
		base = m.listView()
	}

	// Overlay floating window if active
	if m.floatingIdx >= 0 && m.floatingIdx < len(m.orderedSessions()) {
		base = m.overlayFloating(base)
	}

	// Overlay session manager if active
	if m.sessMgr.active {
		base = m.renderSessionManager(base)
	}

	// Overlay session switcher if active
	if m.switcher.active {
		base = m.renderSwitcher(base)
	}

	// Overlay kill confirmation if active
	if m.confirmKill {
		base = m.overlayConfirmKill(base)
	}

	// Push the focused session's cursor position to the IME writer so that
	// the real terminal cursor lands there after Bubble Tea's flush, enabling
	// correct IME composition-window placement in Windows Terminal.
	if m.imeWriter != nil {
		m.imeWriter.setCursorSeq(m.imeCursorSeq())
	}

	m.lastView = base
	return base
}

func (m *tuiModel) handleSessionEvent(evt daemon.SessionEvent) {
	switch evt.Type {
	case "snapshot", "created":
		sess := m.sessionByName(evt.Session)
		if sess == nil {
			cols, rows := evt.Cols, evt.Rows
			if cols == 0 {
				cols = uint16(m.width)
			}
			if rows == 0 {
				rows = uint16(m.height)
			}
			sess = newTUISession(evt.Session, evt.Info.Command, cols, rows)
			sess.CreatedAt = evt.Info.CreatedAt
			m.sessions = append(m.sessions, sess)
			m.order = append(m.order, evt.Session)
		}
		// Sync tab info from daemon
		m.syncTabInfo(sess, evt.Info)

		if m.focusTarget != "" && evt.Session == m.focusTarget {
			m.selected = m.visibleIndexByName(evt.Session)
			m.focusTarget = ""
		}
		if m.floatingTarget != "" && evt.Session == m.floatingTarget {
			idx := m.visibleIndexByName(evt.Session)
			sess.Temporary = true
			m.openFloating(idx)
			m.floatingTarget = ""
		}

		// Handle pending reload tabs for newly created sessions
		if pending, ok := m.pendingReloadTabs[evt.Session]; ok {
			delete(m.pendingReloadTabs, evt.Session)
			// Create additional tabs (first tab is already the session itself)
			if m.conn != nil && len(pending) > 1 {
				for _, tab := range pending[1:] {
					cmd := tab.Command
					if len(cmd) == 0 {
						cmd = []string{"zsh"}
					}
					m.conn.CreateTab(evt.Session, tab.Name, cmd, tab.Dir, envMapToSlice(tab.Env))
				}
			}
		}

	case "renamed":
		oldName := evt.Session
		newName := evt.Info.Name
		if sess := m.sessionByName(oldName); sess != nil {
			sess.Name = newName
		}
		for i, n := range m.order {
			if n == oldName {
				m.order[i] = newName
				break
			}
		}

	case "tab_created", "tab_closed", "tab_switched":
		if sess := m.sessionByName(evt.Session); sess != nil {
			oldTab := sess.activeTabName()
			m.syncTabInfo(sess, evt.Info)
			newTab := sess.activeTabName()
			// All tabs share one local vt10x: park the outgoing tab's
			// scrollback so it survives the reset, and bring back the
			// incoming tab's stash afterwards.
			if oldTab != newTab {
				sess.stashScrollback(oldTab)
			}
			// Clear the vt10x terminal so stale content from the previous
			// tab doesn't render before the new tab's snapshot arrives.
			sess.resetTerminal()
			if oldTab != newTab {
				sess.restoreScrollback(newTab)
			}
			if evt.Type == "tab_closed" {
				sess.pruneScrollbackStash()
			}
			// Clear mouse mode so stale flags from the previous tab
			// (e.g. lazygit) don't forward SGR sequences to a plain shell.
			// Also clear mouseFwdDown so a dangling release from the tab
			// bar click isn't forwarded to the new tab — but only when the
			// switch happened in the session receiving forwarded mouse
			// input; tab switches in other sessions (e.g. from another
			// attached client) must not drop an in-flight release.
			if evt.Type == "tab_switched" {
				sess.ResetMouseMode()
				if sel := m.selectedSession(); sel != nil && sel.Name == evt.Session {
					m.mouseFwdDown = false
				}
			}
			// A tab switch this client requested means the user navigated
			// to the now-active tab: acknowledge its done marker.
			if evt.Type == "tab_switched" && m.pendingTabAck[evt.Session] {
				delete(m.pendingTabAck, evt.Session)
				m.ackAgentDoneSession(evt.Session)
			}
		}

	case "agent_state":
		if sess := m.sessionByName(evt.Session); sess != nil {
			m.syncTabInfo(sess, evt.Info)
		}

	case "exited":
		if sess := m.sessionByName(evt.Session); sess != nil {
			sess.Exited = true
			sess.ExitCode = evt.ExitCode
		}
		m.removeSession(evt.Session)
	}
}

// syncTabInfo updates a tuiSession's tab list from daemon SessionInfo.
// When the tab bar appears or disappears the PTY is re-fitted so its rows
// match what is actually displayed.
func (m *tuiModel) syncTabInfo(sess *tuiSession, info daemon.SessionInfo) {
	if len(info.Tabs) > 0 {
		hadTabBar := len(sess.Tabs) > 1
		tabs := make([]tuiTab, len(info.Tabs))
		for i, t := range info.Tabs {
			tabs[i] = tuiTab{Name: t.Name, Command: t.Command, Agent: t.Agent, AgentState: t.AgentState}
			if i < len(sess.Tabs) {
				tabs[i].Done = agentDoneAfterUpdate(sess.Tabs[i], tabs[i])
			}
		}
		sess.Tabs = tabs
		sess.ActiveTab = info.ActiveTab
		if hasTabBar := len(sess.Tabs) > 1; hasTabBar != hadTabBar && m.conn != nil {
			if ordered := m.orderedSessions(); m.floatingIdx >= 0 && m.floatingIdx < len(ordered) && ordered[m.floatingIdx] == sess {
				_, _, cw, ch := m.floatingContentRect()
				if cw > 0 && ch > 0 {
					m.resizeSessionPTY(sess, cw, ch)
				}
			} else if cols, rows := m.focusedContentSize(); cols > 0 && rows > 0 {
				// Main panel: info line already accounts for tab bar
				sess.Resize(uint16(cols), uint16(rows))
				m.conn.SendResizeSession(sess.Name, uint16(cols), uint16(rows))
			}
		}
	}
}

// agentDoneAfterUpdate carries or sets the "finished, not yet seen" flag
// across a tab state update. A working agent that reaches idle (or exits)
// is done; any new working/blocked activity clears the flag.
func agentDoneAfterUpdate(old, new tuiTab) bool {
	switch new.AgentState {
	case "working", "blocked":
		return false
	}
	if old.AgentState == "working" {
		return true // finished (idle) or exited since the last update
	}
	return old.Done
}

// attentionTarget returns the session name and active tab index the user's
// input is directed at.
func (m *tuiModel) attentionTarget() (string, int) {
	visible := m.visibleSessions()
	if len(visible) == 0 {
		return "", -1
	}
	idx := m.selected
	if idx < 0 || idx >= len(visible) {
		return "", -1
	}
	if m.floatingIdx >= 0 {
		ordered := m.orderedSessions()
		if m.floatingIdx < len(ordered) {
			sess := ordered[m.floatingIdx]
			return sess.Name, sess.ActiveTab
		}
	}
	sess := visible[idx]
	return sess.Name, sess.ActiveTab
}

// ackAgentDoneSession marks the named session's active tab as seen,
// clearing its agent-done marker.
func (m *tuiModel) ackAgentDoneSession(name string) {
	sess := m.sessionByName(name)
	if sess == nil {
		return
	}
	if sess.ActiveTab >= 0 && sess.ActiveTab < len(sess.Tabs) {
		sess.Tabs[sess.ActiveTab].Done = false
	}
}

func (m *tuiModel) sessionByName(name string) *tuiSession {
	for _, s := range m.sessions {
		if s.Name == name {
			return s
		}
	}
	return nil
}

func (m *tuiModel) indexByName(name string) int {
	for i, n := range m.order {
		if n == name {
			return i
		}
	}
	return 0
}

// visibleIndexByName returns the index of the named session in visibleSessions().
func (m *tuiModel) visibleIndexByName(name string) int {
	for i, s := range m.visibleSessions() {
		if s.Name == name {
			return i
		}
	}
	return 0
}

func (m *tuiModel) removeSession(name string) {
	// Clean up hidden state
	delete(m.hidden, name)

	// Find the ordered index of the removed session before removal
	removedIdx := -1
	for i, n := range m.order {
		if n == name {
			removedIdx = i
			break
		}
	}

	for i, s := range m.sessions {
		if s.Name == name {
			m.sessions = append(m.sessions[:i], m.sessions[i+1:]...)
			break
		}
	}
	for i, n := range m.order {
		if n == name {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}

	visibleLen := len(m.visibleSessions())

	// If the floating window was showing the removed session, close it
	if m.floatingIdx >= 0 {
		if m.floatingIdx == removedIdx {
			m.floatingIdx = -1
			m.modeStack = nil
		} else if m.floatingIdx > removedIdx {
			m.floatingIdx--
		}
	}

	// If in fullscreen and the displayed session was removed, fall back
	if m.mode == modeFullscreen {
		// After removal, if selected is out of bounds, fall back
		if m.selected >= visibleLen {
			m.mode = modeList
			m.modeStack = nil
		}
	}

	// Adjust selected index (now indexes into visible sessions)
	if m.selected >= visibleLen {
		m.selected = max(0, visibleLen-1)
	}
	if m.listCursor >= visibleLen {
		m.listCursor = max(0, visibleLen-1)
		m.listCursorTab = -1
	}
	if m.focusCursor >= visibleLen {
		m.focusCursor = -1
	}
}

func (m *tuiModel) orderedSessions() []*tuiSession {
	result := make([]*tuiSession, 0, len(m.order))
	for _, name := range m.order {
		if s := m.sessionByName(name); s != nil {
			result = append(result, s)
		}
	}
	return result
}

// visibleSessions returns ordered sessions excluding hidden ones.
func (m *tuiModel) visibleSessions() []*tuiSession {
	result := make([]*tuiSession, 0, len(m.order))
	for _, name := range m.order {
		if m.hidden[name] {
			continue
		}
		if s := m.sessionByName(name); s != nil {
			result = append(result, s)
		}
	}
	return result
}

// visibleToOrderIdx converts a visible-sessions index to an index in m.order.
func (m *tuiModel) visibleToOrderIdx(visibleIdx int) int {
	visible := m.visibleSessions()
	if visibleIdx < 0 || visibleIdx >= len(visible) {
		return -1
	}
	name := visible[visibleIdx].Name
	for i, n := range m.order {
		if n == name {
			return i
		}
	}
	return -1
}

// hideSession hides a session from display without killing it.
func (m *tuiModel) hideSession(name string) {
	m.hidden[name] = true
	visible := m.visibleSessions()
	if m.selected >= len(visible) {
		m.selected = max(0, len(visible)-1)
	}
}

// showSession un-hides a previously hidden session.
func (m *tuiModel) showSession(name string) {
	delete(m.hidden, name)
}

func (m *tuiModel) selectedSession() *tuiSession {
	visible := m.visibleSessions()
	if len(visible) == 0 || m.selected >= len(visible) {
		return nil
	}
	return visible[m.selected]
}

// focusedSession returns the session that should receive keyboard input.
func (m *tuiModel) focusedSession() *tuiSession {
	visible := m.visibleSessions()
	if len(visible) == 0 {
		return nil
	}
	idx := m.selected
	if idx >= len(visible) {
		return nil
	}
	return visible[idx]
}

func (m *tuiModel) maybeAutoCreate() {
	if len(m.sessions) > 0 || m.autoCreate == nil {
		return
	}
	cmd := m.autoCreate
	m.autoCreate = nil
	if m.conn != nil {
		cols, rows := m.focusedContentSize()
		m.conn.CreateSession("", cmd, uint16(cols), uint16(rows), "")
	}
}

// applyDesiredOrder reorders m.order to match the config-specified order.
// Called once after all initial session snapshots have arrived.
func (m *tuiModel) applyDesiredOrder() {
	if len(m.desiredOrder) == 0 {
		return
	}

	// Remember the currently selected session name.
	var selectedName string
	if sel := m.selectedSession(); sel != nil {
		selectedName = sel.Name
	}

	// Build new order: desired first, then any extras from daemon.
	newOrder := make([]string, 0, len(m.order))
	added := make(map[string]bool)
	for _, name := range m.desiredOrder {
		if m.sessionByName(name) != nil {
			newOrder = append(newOrder, name)
			added[name] = true
		}
	}
	for _, name := range m.order {
		if !added[name] {
			newOrder = append(newOrder, name)
		}
	}

	m.order = newOrder
	m.desiredOrder = nil

	// Restore selected index after reorder.
	if selectedName != "" {
		m.selected = m.visibleIndexByName(selectedName)
	}
}

// resizeSessionPTYs sends the effective content size of the focused panel to the daemon,
// and resizes local vt10x terminals to match. In list/fullscreen mode the
// bottom info line already includes the tab bar, so no extra deduction is needed.
func (m *tuiModel) resizeSessionPTYs() {
	if m.conn == nil {
		return
	}
	cols, rows := m.focusedContentSize()
	if cols <= 0 || rows <= 0 {
		return
	}
	for _, sess := range m.sessions {
		r := rows
		if r < 1 {
			r = 1
		}
		sess.Resize(uint16(cols), uint16(r))
		m.conn.SendResizeSession(sess.Name, uint16(cols), uint16(r))
	}
}

// resizeSessionPTY resizes one session's local terminal and daemon PTY to
// the given content size. Used by floating windows, whose content area (like
// the big panel's) already excludes the bottom info line.
func (m *tuiModel) resizeSessionPTY(sess *tuiSession, cols, rows int) {
	if rows < 1 {
		rows = 1
	}
	sess.Resize(uint16(cols), uint16(rows))
	m.conn.SendResizeSession(sess.Name, uint16(cols), uint16(rows))
}

// openFloating opens the floating window for the given visible-session index and resizes its PTY.
func (m *tuiModel) openFloating(visIdx int) {
	// Convert visible index to ordered index for floatingIdx
	orderIdx := m.visibleToOrderIdx(visIdx)
	if orderIdx < 0 {
		return
	}
	m.pushMode()
	m.floatingIdx = orderIdx
	// Resize the floating session to match the floating window content area
	if m.conn != nil && orderIdx >= 0 && orderIdx < len(m.orderedSessions()) {
		_, _, cw, ch := m.floatingContentRect()
		if cw > 0 && ch > 0 {
			m.resizeSessionPTY(m.orderedSessions()[orderIdx], cw, ch)
		}
	}
}

// closeFloating closes the floating window and restores the previous state.
// If the floating session is temporary (Ctrl+A m), kill it.
func (m *tuiModel) closeFloating() {
	// Check if the floating session is temporary before popping state
	if m.floatingIdx >= 0 && m.floatingIdx < len(m.orderedSessions()) {
		sess := m.orderedSessions()[m.floatingIdx]
		if sess.Temporary && m.conn != nil {
			m.conn.KillSession(sess.Name)
		}
	}
	if !m.popMode() {
		m.floatingIdx = -1
	}
	m.resizeSessionPTYs()
}

// closeFloatingFocusNext closes the floating window and moves focus to the
// session after the one that was floating (Shift+Tab in floating mode).
func (m *tuiModel) closeFloatingFocusNext() {
	// Locate the floating session's visible position before closing.
	prevVis := -1
	if m.floatingIdx >= 0 && m.floatingIdx < len(m.orderedSessions()) {
		name := m.orderedSessions()[m.floatingIdx].Name
		for i, s := range m.visibleSessions() {
			if s.Name == name {
				prevVis = i
				break
			}
		}
	}
	m.closeFloating()
	n := len(m.visibleSessions())
	if n == 0 {
		return
	}
	if prevVis < 0 {
		prevVis = m.selected
	}
	m.selected = (prevVis + 1) % n
}

// focusedContentSize returns the inner content dimensions for the focused session.
func (m *tuiModel) focusedContentSize() (int, int) {
	if m.mode == modeFullscreen {
		return m.width, m.height - 1 // reserve bottom info line
	}

	statusH := 1
	availH := m.height - statusH

	n := len(m.visibleSessions())
	if n == 0 {
		return m.width, m.height
	}

	if m.mode == modeList {
		mainW, _ := m.listLayoutWidths()
		contentW := mainW - 2
		contentH := availH - 3 // borders + bottom info line
		if contentW < 1 {
			contentW = 1
		}
		if contentH < 1 {
			contentH = 1
		}
		return contentW, contentH
	}

	if m.mode == modeFocusBig {
		mainW := m.width
		if n > 1 {
			mainW, _ = m.focusBigLayoutWidths()
		}
		contentW := mainW - 2
		contentH := availH - 3 // borders + bottom info line
		if contentW < 1 {
			contentW = 1
		}
		if contentH < 1 {
			contentH = 1
		}
		return contentW, contentH
	}
	return m.width, m.height
}

// switchTab switches the active tab in a session by delta (-1 or +1).
// requestTabSwitch asks the daemon to switch a session's active tab and
// marks the session for done-acknowledgement when the switch confirms.
func (m *tuiModel) requestTabSwitch(sessionName string, idx int) {
	if m.conn == nil {
		return
	}
	if m.pendingTabAck == nil {
		m.pendingTabAck = make(map[string]bool)
	}
	m.pendingTabAck[sessionName] = true
	m.conn.SwitchTab(sessionName, idx)
}

func (m *tuiModel) switchTab(sess *tuiSession, delta int) {
	if len(sess.Tabs) <= 1 || m.conn == nil {
		return
	}
	newIdx := sess.ActiveTab + delta
	if newIdx < 0 {
		newIdx = len(sess.Tabs) - 1
	} else if newIdx >= len(sess.Tabs) {
		newIdx = 0
	}
	m.requestTabSwitch(sess.Name, newIdx)
}
