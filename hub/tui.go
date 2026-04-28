package hub

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	panelStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240"))

	focusedPanelStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("86"))

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
		// No state update needed; screen content comes from vt10x on render

	case exitFullscreenMsg:
		m.fullscreen = false
		if m.selected < len(m.sessions) {
			panelW, panelH := m.panelSize()
			m.sessions[m.selected].SendResize(uint16(panelW), uint16(panelH))
		}

	case tea.KeyMsg:
		if m.fullscreen {
			return m, nil
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab", "l", "right":
			if len(m.sessions) > 0 {
				m.selected = (m.selected + 1) % len(m.sessions)
			}
		case "shift+tab", "h", "left":
			if len(m.sessions) > 0 {
				m.selected = (m.selected - 1 + len(m.sessions)) % len(m.sessions)
			}
		case "j", "down":
			if len(m.sessions) > 0 {
				cols := m.gridCols()
				next := m.selected + cols
				if next < len(m.sessions) {
					m.selected = next
				}
			}
		case "k", "up":
			if len(m.sessions) > 0 {
				cols := m.gridCols()
				next := m.selected - cols
				if next >= 0 {
					m.selected = next
				}
			}
		case "enter", "f":
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

func (m *Model) gridCols() int {
	n := len(m.sessions)
	if n <= 1 {
		return 1
	}
	if n <= 4 {
		return 2
	}
	return 3
}

func (m *Model) panelSize() (int, int) {
	n := len(m.sessions)
	if n == 0 {
		return m.width - 4, m.height - 4
	}
	cols := m.gridCols()
	rows := (n + cols - 1) / cols
	panelW := (m.width / cols) - 2
	panelH := ((m.height - 2) / rows) - 2
	if panelW < 10 {
		panelW = 10
	}
	if panelH < 3 {
		panelH = 3
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
				colViews = append(colViews, strings.Repeat(" ", panelW+4))
				continue
			}
			sess := m.sessions[idx]
			content := renderScreen(sess.Screen(), panelW, panelH-1)
			title := fmt.Sprintf(" [%d] %s ", idx+1, sess.Title)

			style := panelStyle
			if idx == m.selected {
				style = focusedPanelStyle
				title = fmt.Sprintf(" ▶ [%d] %s ", idx+1, sess.Title)
			}

			panel := style.Width(panelW).Height(panelH).Render(
				titleStyle.Render(title) + "\n" + content,
			)
			colViews = append(colViews, panel)
		}
		rowViews = append(rowViews, lipgloss.JoinHorizontal(lipgloss.Top, colViews...))
	}

	grid := lipgloss.JoinVertical(lipgloss.Left, rowViews...)
	help := statusBarStyle.Width(m.width).Render(
		fmt.Sprintf("Tab/←→/↑↓: navigate  Enter/f: fullscreen  q: quit  clients: %d", n),
	)
	return lipgloss.JoinVertical(lipgloss.Left, grid, help)
}

func renderScreen(screen string, w, h int) string {
	lines := strings.Split(screen, "\n")
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	var sb strings.Builder
	for i, l := range lines {
		if i > 0 {
			sb.WriteRune('\n')
		}
		runes := []rune(l)
		if len(runes) > w {
			runes = runes[:w]
		}
		sb.WriteString(string(runes))
	}
	return sb.String()
}

// Tea messages
type exitFullscreenMsg struct{}
