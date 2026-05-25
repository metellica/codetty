package main

import (
	"codetty/daemon"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// tmuxPickerItem represents a tmux session in the picker.
type tmuxPickerItem struct {
	Name     string
	Selected bool
}

// tmuxPicker is the state for the tmux session picker overlay.
type tmuxPicker struct {
	items  []tmuxPickerItem
	cursor int
	active bool
}

// openTmuxPicker discovers tmux sessions and opens the picker.
// Already-managed tmux sessions are pre-selected.
func (m *tuiModel) openTmuxPicker() {
	sessions, _ := daemon.DiscoverTmuxSessions()
	if len(sessions) == 0 {
		return
	}

	// Build set of currently managed tmux targets
	managed := make(map[string]bool)
	for _, sess := range m.sessions {
		if sess.Type == "tmux" {
			// Extract tmux target name from display name "[tmux] foo" → "foo"
			target := sess.Name
			if len(target) > 7 && target[:7] == "[tmux] " {
				target = target[7:]
			}
			managed[target] = true
		}
	}

	items := make([]tmuxPickerItem, len(sessions))
	hasManaged := false
	for i, name := range sessions {
		sel := managed[name]
		if sel {
			hasManaged = true
		}
		items[i] = tmuxPickerItem{Name: name, Selected: sel}
	}

	// First time (nothing managed): default all selected
	if !hasManaged {
		for i := range items {
			items[i].Selected = true
		}
	}

	m.tmuxPicker = tmuxPicker{
		items:  items,
		cursor: 0,
		active: true,
	}
}

// handleTmuxPickerKey processes input while the tmux picker is open.
func (m *tuiModel) handleTmuxPickerKey(msg tea.KeyMsg) tea.Cmd {
	p := &m.tmuxPicker

	switch msg.Type {
	case tea.KeyEscape:
		p.active = false
		return nil
	case tea.KeyEnter:
		m.applyTmuxPickerSelection()
		p.active = false
		return nil
	case tea.KeyUp:
		if p.cursor > 0 {
			p.cursor--
		}
	case tea.KeyDown:
		if p.cursor < len(p.items)-1 {
			p.cursor++
		}
	case tea.KeySpace:
		if p.cursor < len(p.items) {
			p.items[p.cursor].Selected = !p.items[p.cursor].Selected
		}
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case 'a':
				for i := range p.items {
					p.items[i].Selected = true
				}
			case 'n':
				for i := range p.items {
					p.items[i].Selected = false
				}
			case 'j':
				if p.cursor < len(p.items)-1 {
					p.cursor++
				}
			case 'k':
				if p.cursor > 0 {
					p.cursor--
				}
			}
		}
	}
	return nil
}

// applyTmuxPickerSelection attaches selected and kills unselected tmux sessions.
func (m *tuiModel) applyTmuxPickerSelection() {
	if m.conn == nil {
		return
	}

	// Build set of currently managed tmux targets
	managed := make(map[string]string) // tmux target → display name
	for _, sess := range m.sessions {
		if sess.Type == "tmux" {
			target := sess.Name
			if len(target) > 7 && target[:7] == "[tmux] " {
				target = target[7:]
			}
			managed[target] = sess.Name
		}
	}

	for _, item := range m.tmuxPicker.items {
		displayName, isManaged := managed[item.Name]
		if item.Selected && !isManaged {
			// Attach: create the tmux session in daemon
			cols, rows := m.focusedContentSize()
			m.conn.AttachTmuxSession(item.Name, uint16(cols), uint16(rows))
		} else if !item.Selected && isManaged {
			// Detach: kill the daemon session wrapping this tmux session
			m.conn.KillSession(displayName)
		}
	}
}

// renderTmuxPicker renders the tmux picker overlay.
func (m *tuiModel) renderTmuxPicker(base string) string {
	p := &m.tmuxPicker

	// Picker dimensions
	maxW := 50
	if m.width < maxW+4 {
		maxW = m.width - 4
	}
	if maxW < 20 {
		maxW = 20
	}

	var lines []string
	lines = append(lines, " Tmux Sessions")
	lines = append(lines, " "+strings.Repeat("─", maxW-3))

	for i, item := range p.items {
		check := "○"
		if item.Selected {
			check = "●"
		}
		cursor := "  "
		if i == p.cursor {
			cursor = "▸ "
		}
		line := fmt.Sprintf(" %s%s %s", cursor, check, item.Name)
		// Truncate if too long
		if len(line) > maxW-1 {
			line = line[:maxW-4] + "..."
		}
		lines = append(lines, line)
	}

	lines = append(lines, " "+strings.Repeat("─", maxW-3))
	lines = append(lines, " ↑↓/jk navigate  Space toggle  a all  n none")
	lines = append(lines, " Enter confirm   Esc cancel")

	content := strings.Join(lines, "\n")

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("86")).
		Padding(0, 1).
		Width(maxW)

	box := boxStyle.Render(content)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
		box,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceForeground(lipgloss.Color("0")))
}
