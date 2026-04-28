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

	case ClientDisconnectedMsg:
		m.sessions = m.hub.Sessions()
		if m.selected >= len(m.sessions) {
			m.selected = max(0, len(m.sessions)-1)
		}

	case ScreenUpdateMsg:
		// Re-render happens automatically; vt10x content read in View()

	case exitFullscreenMsg:
		m.fullscreen = false

	case tea.KeyMsg:
		if m.fullscreen {
			return m, nil
		}
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyCtrlQ:
			return m, tea.Quit
		case tea.KeyTab:
			if len(m.sessions) > 0 {
				m.selected = (m.selected + 1) % len(m.sessions)
			}
		case tea.KeyShiftTab:
			if len(m.sessions) > 0 {
				m.selected = (m.selected - 1 + len(m.sessions)) % len(m.sessions)
			}
		case tea.KeyF1:
			if len(m.sessions) > 0 {
				m.fullscreen = true
				sess := m.sessions[m.selected]
				sess.SendResize(uint16(m.width), uint16(m.height))
				handler := &fullscreenHandler{
					session: sess,
					hub:     m.hub,
				}
				return m, tea.Exec(handler, func(err error) tea.Msg {
					return exitFullscreenMsg{}
				})
			}
		default:
			// Forward all other keys to the selected session
			if m.selected < len(m.sessions) {
				raw := keyToBytes(msg)
				if len(raw) > 0 {
					m.sessions[m.selected].SendInput(raw)
				}
			}
		}
	}
	return m, nil
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
	n := len(m.sessions)
	if n <= 1 {
		return 1
	}

	usableH := m.height - 2
	if usableH < 1 {
		usableH = 1
	}

	bestCols := 1
	bestDist := 999.0
	for c := 1; c <= n; c++ {
		if c > 4 {
			break
		}
		r := (n + c - 1) / c
		pw := m.width / c
		ph := usableH / r
		if ph < 4 || pw < 20 {
			continue
		}
		ratio := float64(pw) / float64(ph)
		dist := ratio - 3.0
		if dist < 0 {
			dist = -dist
		}
		if dist < bestDist {
			bestDist = dist
			bestCols = c
		}
	}
	return bestCols
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
		fmt.Sprintf("Tab/Shift+Tab: switch  F1: fullscreen  Ctrl+C: quit  clients: %d", n),
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
	default:
		// Ctrl+A through Ctrl+Z: type values 1-26 map directly to bytes
		if k.Type >= 1 && k.Type <= 26 {
			return []byte{byte(k.Type)}
		}
		return nil
	}
}

// Tea messages
type exitFullscreenMsg struct{}
