package main

import (
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
	modeFocusBig   layoutMode = iota
	modeGrid
	modeFullscreen // no borders, no status bar, just terminal
)

// tuiModel is the Bubble Tea model for the unified codetty TUI.
type tuiModel struct {
	conn     *daemonConn
	sessions []*tuiSession
	order    []string // session names in display order
	selected int      // primary/big panel in focus-big mode
	mode     layoutMode

	prefix   bool // Ctrl+A prefix active
	width    int
	height   int

	// inputFocus overrides which session receives keyboard input.
	// -1 means use m.selected. Set by clicking a side panel.
	// Reset to -1 when m.selected changes (via hotkey/ctrl+click).
	inputFocus int

	// floatingIdx: index of session shown in floating overlay, -1 means none.
	floatingIdx int

	// modeStack records previous states for Ctrl+A f back-navigation.
	modeStack []modeState

	// Rename mode
	renaming   bool
	renameText string

	// Create session mode (type selection + name input)
	creating       bool
	createText     string
	createChooser  bool // true when showing [c]odetty / [t]mux type chooser
	createTmux     bool // true when creating a tmux session (after chooser)

	// Text selection
	sel textSelection

	// Initial focus target (from new/attach command)
	focusTarget string
	// When set, open this session in a floating window once created
	floatingTarget string
	// Auto-create session on start
	autoCreate []string // command to auto-create
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
		conn:        conn,
		mode:        modeFocusBig,
		width:       120,
		height:      40,
		inputFocus:  -1,
		floatingIdx: -1,
		focusTarget: focusTarget,
		autoCreate:  autoCreate,
	}
}

func (m *tuiModel) Init() tea.Cmd {
	return tea.Batch(tea.WindowSize(), tea.EnableMouseCellMotion)
}

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

	case daemonDisconnectedMsg:
		return m, tea.Quit

	case tea.KeyMsg:
		return m, m.handleKey(msg)

	case tea.MouseMsg:
		if m.renaming || m.creating || m.createChooser {
			return m, nil
		}
		return m, m.handleMouse(msg)
	}
	return m, nil
}

func (m *tuiModel) View() string {
	if len(m.sessions) == 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			tuiTitleStyle.Render("⬡ codetty")+"\n\n"+
				"No sessions. Creating one...")
	}

	var base string
	switch m.mode {
	case modeFullscreen:
		base = m.fullscreenView()
	case modeGrid:
		base = m.gridView()
	default:
		base = m.focusBigView()
	}

	// Overlay floating window if active
	if m.floatingIdx >= 0 && m.floatingIdx < len(m.sessions) {
		base = m.overlayFloating(base)
	}

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
			sess.Type = evt.Info.Type
			m.sessions = append(m.sessions, sess)
			m.order = append(m.order, evt.Session)
		}
		if m.focusTarget != "" && evt.Session == m.focusTarget {
			m.selected = m.indexByName(evt.Session)
			m.focusTarget = ""
		}
		if m.floatingTarget != "" && evt.Session == m.floatingTarget {
			idx := m.indexByName(evt.Session)
			if idx >= 0 {
				sess.Temporary = true
				m.openFloating(idx)
			}
			m.floatingTarget = ""
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

	case "exited":
		if sess := m.sessionByName(evt.Session); sess != nil {
			sess.Exited = true
			sess.ExitCode = evt.ExitCode
		}
		m.removeSession(evt.Session)
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

func (m *tuiModel) removeSession(name string) {
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

	orderedLen := len(m.orderedSessions())

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
	if m.mode == modeFullscreen && m.selected == removedIdx {
		m.mode = modeFocusBig
		m.modeStack = nil
	}

	// Adjust selected index
	if removedIdx >= 0 && m.selected > removedIdx {
		m.selected--
	}
	if m.selected >= orderedLen {
		m.selected = max(0, orderedLen-1)
	}

	// Reset inputFocus
	m.inputFocus = -1
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

func (m *tuiModel) selectedSession() *tuiSession {
	ordered := m.orderedSessions()
	if len(ordered) == 0 || m.selected >= len(ordered) {
		return nil
	}
	return ordered[m.selected]
}

// focusedSession returns the session that should receive keyboard input.
// This may differ from selectedSession() when a side panel is clicked.
func (m *tuiModel) focusedSession() *tuiSession {
	ordered := m.orderedSessions()
	if len(ordered) == 0 {
		return nil
	}
	idx := m.selected
	if idx >= len(ordered) {
		return nil
	}
	return ordered[idx]
}

func (m *tuiModel) maybeAutoCreate() {
	if len(m.sessions) > 0 || m.autoCreate == nil {
		return
	}
	cmd := m.autoCreate
	m.autoCreate = nil
	if m.conn != nil {
		cols, rows := m.focusedContentSize()
		m.conn.CreateSession("", cmd, uint16(cols), uint16(rows))
	}
}

// resizeSessionPTYs sends the effective content size of the focused panel to the daemon,
// and resizes local vt10x terminals to match.
func (m *tuiModel) resizeSessionPTYs() {
	if m.conn == nil {
		return
	}
	cols, rows := m.focusedContentSize()
	if cols > 0 && rows > 0 {
		m.conn.SendResize(uint16(cols), uint16(rows))
		for _, sess := range m.sessions {
			sess.Resize(uint16(cols), uint16(rows))
		}
	}
}

// openFloating opens the floating window for the given session index and resizes its PTY.
func (m *tuiModel) openFloating(idx int) {
	m.pushMode()
	m.floatingIdx = idx
	m.inputFocus = -1
	// Resize the floating session to match the floating window content area
	if m.conn != nil && idx >= 0 && idx < len(m.sessions) {
		_, _, cw, ch := m.floatingContentRect()
		if cw > 0 && ch > 0 {
			sess := m.orderedSessions()[idx]
			sess.Resize(uint16(cw), uint16(ch))
			m.conn.SendResizeSession(sess.Name, uint16(cw), uint16(ch))
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

// focusedContentSize returns the inner content dimensions for the focused session.
func (m *tuiModel) focusedContentSize() (int, int) {
	if m.mode == modeFullscreen {
		return m.width, m.height
	}

	statusH := 1
	availH := m.height - statusH

	n := len(m.orderedSessions())
	if n == 0 {
		return m.width, m.height
	}

	if m.mode == modeGrid {
		cols := gridCols(n, m.width)
		rows := (n + cols - 1) / cols
		panelW := m.width / cols
		panelH := availH / rows
		if panelH < tuiMinPanelH {
			panelH = tuiMinPanelH
		}
		// Content inside panel: -2 for borders, -1 for title line
		contentW := panelW - 2
		contentH := panelH - 3
		if contentW < 1 {
			contentW = 1
		}
		if contentH < 1 {
			contentH = 1
		}
		return contentW, contentH
	}

	// Focus-big mode
	if n == 1 {
		contentW := m.width - 2
		contentH := availH - 3
		if contentW < 1 {
			contentW = 1
		}
		if contentH < 1 {
			contentH = 1
		}
		return contentW, contentH
	}

	if m.width >= 120 {
		mainW := m.width * 7 / 10
		sideW := m.width - mainW
		if sideW < tuiMinPanelW {
			sideW = tuiMinPanelW
			mainW = m.width - sideW
		}
		contentW := mainW - 2
		contentH := availH - 3
		if contentW < 1 {
			contentW = 1
		}
		if contentH < 1 {
			contentH = 1
		}
		return contentW, contentH
	}

	// Narrow focus-big
	mainH := availH * 7 / 10
	contentW := m.width - 2
	contentH := mainH - 3
	if contentW < 1 {
		contentW = 1
	}
	if contentH < 1 {
		contentH = 1
	}
	return contentW, contentH
}
