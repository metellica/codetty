package hub

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("86")).Bold(true)

	statusBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("235")).
			Foreground(lipgloss.Color("250")).
			Padding(0, 1)
)

// Model is the bubbletea model for the hub TUI.
type Model struct {
	hub        *Hub
	sessions   []*ClientSession
	selected   int
	layout     layoutState
	fullscreen bool
	width      int
	height     int
}

func NewModel(h *Hub) *Model {
	return &Model{
		hub:    h,
		width:  120,
		height: 40,
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.WindowSize()
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case ClientConnectedMsg:
		m.sessions = m.hub.Sessions()
		m.layout.syncSessions(m.sessions)
		ordered := m.orderedSessions()
		if m.selected >= len(ordered) {
			m.selected = max(0, len(ordered)-1)
		}

	case ClientDisconnectedMsg:
		m.sessions = m.hub.Sessions()
		m.layout.syncSessions(m.sessions)
		ordered := m.orderedSessions()
		if m.selected >= len(ordered) {
			m.selected = max(0, len(ordered)-1)
		}

	case ScreenUpdateMsg:
		// Re-render happens automatically; vt10x content read in View()

	case exitFullscreenMsg:
		m.fullscreen = false

	case tea.KeyMsg:
		if m.fullscreen {
			return m, nil
		}
		if msg.Type == tea.KeyShiftTab {
			m.selectDelta(1)
			return m, nil
		}
		if msg.Type == tea.KeyCtrlA {
			if m.layout.prefix {
				m.layout.prefix = false
				m.forwardKey(msg)
				return m, nil
			}
			m.layout.prefix = true
			return m, nil
		}
		if m.layout.prefix {
			m.layout.prefix = false
			return m, m.handlePrefixKey(msg)
		}
		m.forwardKey(msg)
	}
	return m, nil
}

func (m *Model) orderedSessions() []*ClientSession {
	return m.layout.orderedSessions(m.sessions)
}

func (m *Model) handlePrefixKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyLeft:
		m.moveSelected(-1)
	case tea.KeyRight:
		m.moveSelected(1)
	case tea.KeyRunes:
		if msg.Alt || len(msg.Runes) != 1 {
			return nil
		}
		switch msg.Runes[0] {
		case 'n':
			m.selectDelta(1)
		case 'p':
			m.selectDelta(-1)
		case 'f':
			return m.enterFullscreen()
		case '\\':
			return tea.Quit
		case 'h':
			m.moveSelected(-1)
		case 'l':
			m.moveSelected(1)
		case '1', '2', '3', '4':
			m.layout.mode = layoutFixedGrid
			m.layout.fixedCols = int(msg.Runes[0] - '0')
		case 'a':
			m.layout.mode = layoutAutoGrid
			m.layout.fixedCols = 0
		case 'b':
			m.layout.mode = layoutFocusBig
		case 'g':
			if m.layout.mode == layoutFocusBig {
				if m.layout.fixedCols > 0 {
					m.layout.mode = layoutFixedGrid
				} else {
					m.layout.mode = layoutAutoGrid
				}
			}
		}
	}
	return nil
}

func (m *Model) forwardKey(msg tea.KeyMsg) {
	ordered := m.orderedSessions()
	if len(ordered) == 0 {
		m.selected = 0
		return
	}
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected >= len(ordered) {
		m.selected = len(ordered) - 1
	}
	raw := keyToBytes(msg)
	if len(raw) > 0 {
		ordered[m.selected].SendInput(raw)
	}
}

func (m *Model) selectDelta(delta int) {
	ordered := m.orderedSessions()
	if len(ordered) == 0 {
		m.selected = 0
		return
	}
	next := (m.selected + delta) % len(ordered)
	if next < 0 {
		next += len(ordered)
	}
	m.selected = next
}

func (m *Model) moveSelected(delta int) {
	ordered := m.orderedSessions()
	if len(ordered) == 0 {
		m.selected = 0
		return
	}
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected >= len(ordered) {
		m.selected = len(ordered) - 1
	}
	m.selected = m.layout.moveSelected(m.selected, delta)
}

func (m *Model) enterFullscreen() tea.Cmd {
	ordered := m.orderedSessions()
	if len(ordered) == 0 {
		m.selected = 0
		return nil
	}
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected >= len(ordered) {
		m.selected = len(ordered) - 1
	}
	m.fullscreen = true
	sess := ordered[m.selected]
	sess.SendResize(uint16(m.width), uint16(m.height))
	handler := &fullscreenHandler{
		session: sess,
		hub:     m.hub,
	}
	return tea.Exec(handler, func(err error) tea.Msg {
		return exitFullscreenMsg{}
	})
}

func (m *Model) View() string {
	if len(m.sessions) == 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			titleStyle.Render("⬡ codetty-hub")+"\n\n"+
				"Waiting for clients to connect...\n\n"+
				lipgloss.NewStyle().Foreground(lipgloss.Color("240")).
					Render("Run: codetty-cli --server ws://<this-host>:8080/ws -- <cmd>"))
	}

	if m.fullscreen {
		return "Fullscreen mode active..."
	}

	if m.layout.mode == layoutFocusBig {
		return m.focusBigView()
	}

	return m.gridView()
}

// keyToBytes converts a bubbletea KeyMsg to raw terminal bytes for PTY input.
func keyToBytes(msg tea.KeyMsg) []byte {
	k := tea.Key(msg)
	switch k.Type {
	case tea.KeyRunes:
		b := []byte(string(k.Runes))
		if k.Alt {
			b = append([]byte{0x1b}, b...)
		}
		return b
	case tea.KeySpace:
		return []byte{' '}
	case tea.KeyEnter:
		return []byte{'\r'}
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyEsc:
		return []byte{0x1b}
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyF1:
		return []byte("\x1bOP")
	case tea.KeyF2:
		return []byte("\x1bOQ")
	case tea.KeyF3:
		return []byte("\x1bOR")
	case tea.KeyF4:
		return []byte("\x1bOS")
	case tea.KeyF5:
		return []byte("\x1b[15~")
	case tea.KeyF6:
		return []byte("\x1b[17~")
	case tea.KeyF7:
		return []byte("\x1b[18~")
	case tea.KeyF8:
		return []byte("\x1b[19~")
	case tea.KeyF9:
		return []byte("\x1b[20~")
	case tea.KeyF10:
		return []byte("\x1b[21~")
	case tea.KeyF11:
		return []byte("\x1b[23~")
	case tea.KeyF12:
		return []byte("\x1b[24~")
	case tea.KeyTab:
		return []byte{'\t'}
	default:
		// Ctrl+A through Ctrl+_ (type values 1-31 map to control bytes)
		if k.Type >= 1 && k.Type <= 31 {
			return []byte{byte(k.Type)}
		}
		return nil
	}
}

// Tea messages
type exitFullscreenMsg struct{}
