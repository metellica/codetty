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
	modeFocusBig layoutMode = iota
	modeGrid
)

// tuiModel is the Bubble Tea model for the unified codetty TUI.
type tuiModel struct {
	conn     *daemonConn
	sessions []*tuiSession
	order    []string // session names in display order
	selected int
	mode     layoutMode
	prefix   bool // Ctrl+A prefix active
	width    int
	height   int

	// Initial focus target (from new/attach command)
	focusTarget string
	// Auto-create session on start
	autoCreate []string // command to auto-create
}

func newTUIModel(conn *daemonConn, focusTarget string, autoCreate []string) *tuiModel {
	return &tuiModel{
		conn:        conn,
		mode:        modeFocusBig,
		width:       120,
		height:      40,
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
		if m.conn != nil {
			m.conn.SendResize(uint16(msg.Width), uint16(msg.Height))
		}

	case daemonOutputMsg:
		if sess := m.sessionByName(msg.Session); sess != nil {
			sess.WriteOutput(msg.Data)
		}

	case daemonSessionEventMsg:
		m.handleSessionEvent(msg.Event)
		return m, tea.ClearScreen

	case daemonSubscribedMsg:
		m.maybeAutoCreate()

	case daemonDisconnectedMsg:
		return m, tea.Quit

	case tea.KeyMsg:
		return m, m.handleKey(msg)

	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			if idx := m.hitTest(msg.X, msg.Y); idx >= 0 {
				m.selected = idx
			}
		}
	}
	return m, nil
}

func (m *tuiModel) View() string {
	if len(m.sessions) == 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			tuiTitleStyle.Render("⬡ codetty")+"\n\n"+
				"No sessions. Creating one...")
	}

	if m.mode == modeFocusBig {
		return m.focusBigView()
	}
	return m.gridView()
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
		if m.focusTarget != "" && evt.Session == m.focusTarget {
			m.selected = m.indexByName(evt.Session)
			m.focusTarget = ""
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
	if m.selected >= orderedLen {
		m.selected = max(0, orderedLen-1)
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

func (m *tuiModel) selectedSession() *tuiSession {
	ordered := m.orderedSessions()
	if len(ordered) == 0 || m.selected >= len(ordered) {
		return nil
	}
	return ordered[m.selected]
}

func (m *tuiModel) maybeAutoCreate() {
	if len(m.sessions) > 0 || m.autoCreate == nil {
		return
	}
	cmd := m.autoCreate
	m.autoCreate = nil
	if m.conn != nil {
		m.conn.CreateSession("", cmd, uint16(m.width), uint16(m.height))
	}
}
