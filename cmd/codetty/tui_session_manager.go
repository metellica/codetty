package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// sessionManager is the state for the unified session manager overlay.
type sessionManager struct {
	active bool
	cursor int
}

// sessionManagerItem represents one session row in the manager.
type sessionManagerItem struct {
	Name    string
	Type    string // "native" or "tmux"
	Hidden  bool
	Running bool // true if not exited
}

// openSessionManager opens the session manager overlay.
func (m *tuiModel) openSessionManager() {
	m.sessMgr = sessionManager{
		active: true,
		cursor: 0,
	}
}

// sessionManagerItems builds the list of all sessions (visible + hidden).
func (m *tuiModel) sessionManagerItems() []sessionManagerItem {
	items := make([]sessionManagerItem, 0, len(m.order))
	for _, name := range m.order {
		sess := m.sessionByName(name)
		if sess == nil {
			continue
		}
		items = append(items, sessionManagerItem{
			Name:    name,
			Type:    sess.Type,
			Hidden:  m.hidden[name],
			Running: !sess.Exited,
		})
	}
	return items
}

// handleSessionManagerKey processes input while the session manager is open.
func (m *tuiModel) handleSessionManagerKey(msg tea.KeyMsg) tea.Cmd {
	items := m.sessionManagerItems()
	if len(items) == 0 {
		m.sessMgr.active = false
		return nil
	}

	switch msg.Type {
	case tea.KeyEscape:
		m.sessMgr.active = false
		return nil
	case tea.KeyUp:
		if m.sessMgr.cursor > 0 {
			m.sessMgr.cursor--
		}
	case tea.KeyDown:
		if m.sessMgr.cursor < len(items)-1 {
			m.sessMgr.cursor++
		}
	case tea.KeySpace, tea.KeyEnter:
		// Toggle visibility
		if m.sessMgr.cursor < len(items) {
			item := items[m.sessMgr.cursor]
			if item.Hidden {
				m.showSession(item.Name)
			} else {
				m.hideSession(item.Name)
			}
		}
		return tea.ClearScreen
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case 'j':
				if m.sessMgr.cursor < len(items)-1 {
					m.sessMgr.cursor++
				}
			case 'k':
				if m.sessMgr.cursor > 0 {
					m.sessMgr.cursor--
				}
			case 'x', 'X':
				// Kill with confirmation
				if m.sessMgr.cursor < len(items) {
					item := items[m.sessMgr.cursor]
					m.sessMgr.active = false
					m.confirmKill = true
					m.confirmKillName = item.Name
				}
				return nil
			case 'h':
				// Hide current item
				if m.sessMgr.cursor < len(items) {
					item := items[m.sessMgr.cursor]
					if !item.Hidden {
						m.hideSession(item.Name)
					}
				}
				return tea.ClearScreen
			case 's':
				// Show current item
				if m.sessMgr.cursor < len(items) {
					item := items[m.sessMgr.cursor]
					if item.Hidden {
						m.showSession(item.Name)
					}
				}
				return tea.ClearScreen
			}
		}
	}
	return nil
}

// renderSessionManager renders the session manager overlay.
func (m *tuiModel) renderSessionManager(base string) string {
	items := m.sessionManagerItems()

	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("86")).
		Bold(true).
		Padding(0, 1)

	subtitleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("243")).
		Padding(0, 1)

	itemStyle := lipgloss.NewStyle().
		Padding(0, 1)

	cursorItemStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("237")).
		Foreground(lipgloss.Color("255")).
		Padding(0, 1)

	hiddenStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241"))

	visibleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("78"))

	separatorStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("238"))

	helpKeyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("86"))

	helpDescStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("245"))

	// Calculate width
	longestName := 0
	for _, item := range items {
		if len(item.Name) > longestName {
			longestName = len(item.Name)
		}
	}
	innerW := longestName + 24
	if innerW < 48 {
		innerW = 48
	}
	maxW := m.width * 6 / 10
	if maxW < 48 {
		maxW = m.width - 4
	}
	if innerW > maxW {
		innerW = maxW
	}

	// Build content
	var lines []string
	lines = append(lines, titleStyle.Render("⬡ Session Manager"))

	visibleCount := 0
	for _, item := range items {
		if !item.Hidden {
			visibleCount++
		}
	}
	lines = append(lines, subtitleStyle.Render(
		fmt.Sprintf("%d visible / %d total", visibleCount, len(items))))
	lines = append(lines, "")
	lines = append(lines, separatorStyle.Render(strings.Repeat("─", innerW)))

	for i, item := range items {
		var status string
		if item.Hidden {
			status = hiddenStyle.Render("hidden ")
		} else {
			status = visibleStyle.Render("visible")
		}

		typeTag := ""
		if item.Type == "tmux" {
			typeTag = " [tmux]"
		}

		name := item.Name
		maxName := innerW - 20
		if maxName < 8 {
			maxName = 8
		}
		if len(name) > maxName {
			name = name[:maxName-1] + "…"
		}

		row := fmt.Sprintf("  %s  %s%s", status, name, typeTag)
		if i == m.sessMgr.cursor {
			row = fmt.Sprintf("▸ %s  %s%s", status, name, typeTag)
			lines = append(lines, cursorItemStyle.Width(innerW).Render(row))
		} else {
			lines = append(lines, itemStyle.Render(row))
		}
	}

	lines = append(lines, "")
	lines = append(lines, separatorStyle.Render(strings.Repeat("─", innerW)))
	lines = append(lines, "")

	help1 := helpKeyStyle.Render("↑↓") + helpDescStyle.Render(" nav  ") +
		helpKeyStyle.Render("space/enter") + helpDescStyle.Render(" toggle  ") +
		helpKeyStyle.Render("x") + helpDescStyle.Render(" kill")
	help2 := helpKeyStyle.Render("h") + helpDescStyle.Render(" hide  ") +
		helpKeyStyle.Render("s") + helpDescStyle.Render(" show  ") +
		helpKeyStyle.Render("esc") + helpDescStyle.Render(" close")

	lines = append(lines, " "+help1)
	lines = append(lines, " "+help2)

	content := strings.Join(lines, "\n")

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(1, 3).
		Width(innerW + 10)

	box := boxStyle.Render(content)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
		box,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceForeground(lipgloss.Color("0")))
}
