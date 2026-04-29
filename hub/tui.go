package hub

import (
	"fmt"
	"strings"

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

	return m.gridView()
}

// gridCols picks column count giving panel aspect ratio closest to 3:1.
func (m *Model) gridCols() int {
	return m.layout.gridCols(len(m.sessions), m.width, m.height)
}

func (m *Model) panelSize() (int, int) {
	n := len(m.sessions)
	if n == 0 {
		return m.width, m.height - 2
	}
	cols := m.gridCols()
	rows := (n + cols - 1) / cols
	panelW := m.width / cols
	panelH := (m.height - 1) / rows // -1 for status bar
	if panelW < 14 {
		panelW = 14
	}
	if panelH < 5 {
		panelH = 5
	}
	return panelW, panelH
}

func (m *Model) gridView() string {
	n := len(m.sessions)
	cols := m.gridCols()
	rows := (n + cols - 1) / cols
	panelW, panelH := m.panelSize()

	var rowViews []string
	for r := 0; r < rows; r++ {
		var colViews []string
		for c := 0; c < cols; c++ {
			idx := r*cols + c
			if idx >= n {
				colViews = append(colViews, strings.Repeat(" ", panelW))
				continue
			}
			sess := m.sessions[idx]
			// Content area is inside the border: 2 chars for left/right, 2 lines for top/bottom
			contentW := panelW - 2
			contentH := panelH - 2
			if contentW < 1 {
				contentW = 1
			}
			if contentH < 1 {
				contentH = 1
			}

			contentLines, _, _ := sess.RenderANSI(contentW, contentH)
			content := strings.Join(contentLines, "\n")
			title := fmt.Sprintf("[%d] %s", idx+1, sess.Title)
			focused := idx == m.selected
			if focused {
				title = fmt.Sprintf("▶ [%d] %s", idx+1, sess.Title)
			}

			panel := renderPanel(title, content, panelW, contentH, focused)
			colViews = append(colViews, panel)
		}
		rowViews = append(rowViews, lipgloss.JoinHorizontal(lipgloss.Top, colViews...))
	}

	grid := lipgloss.JoinVertical(lipgloss.Left, rowViews...)
	help := statusBarStyle.Width(m.width).Render(
		fmt.Sprintf("Ctrl+]: switch  Ctrl+F: fullscreen  Ctrl+\\: quit  clients: %d", n),
	)
	return lipgloss.JoinVertical(lipgloss.Left, grid, help)
}

// renderPanel draws a bordered panel with the title embedded in the top border.
//
//	╭─ title ─────────╮
//	│ content          │
//	╰─────────────────╯
func renderPanel(title, content string, totalW, contentH int, focused bool) string {
	borderColor := lipgloss.Color("240")
	titleColor := lipgloss.Color("250")
	if focused {
		borderColor = lipgloss.Color("86")
		titleColor = lipgloss.Color("86")
	}

	bc := lipgloss.NewStyle().Foreground(borderColor)
	tc := lipgloss.NewStyle().Foreground(titleColor).Bold(true)

	innerW := totalW - 2
	if innerW < 0 {
		innerW = 0
	}

	// Top border: ╭─ title ──────╮
	titleRendered := tc.Render(title)
	titleVisW := lipgloss.Width(titleRendered)
	rightPad := innerW - 2 - titleVisW - 1 // "─ " + title + " " + pad + nothing before ╮
	if rightPad < 0 {
		rightPad = 0
	}
	top := bc.Render("╭─") + " " + titleRendered + " " + bc.Render(strings.Repeat("─", rightPad)+"╮")

	// Content lines
	lines := strings.Split(content, "\n")
	for len(lines) < contentH {
		lines = append(lines, "")
	}
	if len(lines) > contentH {
		lines = lines[:contentH]
	}

	var body strings.Builder
	for _, l := range lines {
		visW := lipgloss.Width(l)
		pad := innerW - visW
		if pad < 0 {
			// Content already correct width from RenderANSI; show as-is
			pad = 0
		}
		body.WriteString(bc.Render("│") + l + strings.Repeat(" ", pad) + bc.Render("│") + "\n")
	}

	// Bottom border
	bottom := bc.Render("╰" + strings.Repeat("─", innerW) + "╯")

	return top + "\n" + body.String() + bottom
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
