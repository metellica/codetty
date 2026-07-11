package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// tuiListSidebarW is the preferred sidebar width (columns) in list mode.
const tuiListSidebarW = 28

var (
	listCursorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("238"))
	// Selected session: a subtle low-contrast background instead of a marker.
	listSelectedStyle = lipgloss.NewStyle().Background(lipgloss.Color("236"))
	listExitedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	listIdleStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)

// listLayoutWidths returns the big panel and sidebar widths for list mode.
func (m *tuiModel) listLayoutWidths() (mainW, sideW int) {
	sideW = tuiListSidebarW
	if sideW > m.width/2 {
		sideW = m.width / 2
	}
	if sideW < tuiMinPanelW {
		sideW = tuiMinPanelW
	}
	mainW = m.width - sideW
	if mainW < 1 {
		mainW = 1
	}
	return mainW, sideW
}

// listLine is one rendered sidebar row, tagged with the session (and tab,
// for agent rows) it belongs to so rendering and mouse hit-testing stay in
// sync.
type listLine struct {
	text    string
	sessIdx int
	tabIdx  int // tab index for agent detail rows, -1 for header rows
	header  bool // true for the session name row (vs. an agent detail row)
}

// buildListLines renders sidebar rows: one header per session followed by
// one row per agent-bearing tab (a session can have 0..N agents).
func (m *tuiModel) buildListLines(sessions []*tuiSession, innerW int) []listLine {
	var lines []listLine
	for i, sess := range sessions {
		label := fmt.Sprintf(" %d %s", i+1, sess.Name)
		if sess.Exited {
			label += " (exited)"
		}
		if lipgloss.Width(label) > innerW {
			label = ansi.Truncate(label, innerW, "…")
		}
		pad := strings.Repeat(" ", max(0, innerW-lipgloss.Width(label)))
		var text string
		switch {
		case m.listFocus && i == m.listCursor && m.listCursorTab < 0:
			text = listCursorStyle.Render(label + pad)
		case i == m.selected:
			style := listSelectedStyle
			if sess.Exited {
				style = style.Foreground(lipgloss.Color("243"))
			}
			text = style.Render(label + pad)
		case sess.Exited:
			text = listExitedStyle.Render(label)
		default:
			text = label
		}
		lines = append(lines, listLine{text: text, sessIdx: i, tabIdx: -1, header: true})

		for ti, tab := range sess.Tabs {
			if tab.Agent == "" {
				continue
			}
			state := effectiveAgentState(tab)
			badge := agentBadge(state)
			if badge == "" {
				badge = "·"
			}
			word := state
			if word == "" {
				word = "idle"
			}
			agentLabel := fmt.Sprintf("    %s %s %s", badge, tab.Agent, word)
			if lipgloss.Width(agentLabel) > innerW {
				agentLabel = ansi.Truncate(agentLabel, innerW, "…")
			}
			style := listIdleStyle
			switch state {
			case "blocked":
				style = lipgloss.NewStyle().Bold(true).Foreground(agentBlockedColor)
			case "working":
				style = lipgloss.NewStyle().Foreground(agentWorkingColor)
			case "done":
				style = lipgloss.NewStyle().Bold(true).Foreground(agentDoneColor)
			}
			text := style.Render(agentLabel)
			if m.listFocus && i == m.listCursor && ti == m.listCursorTab {
				agentPad := strings.Repeat(" ", max(0, innerW-lipgloss.Width(agentLabel)))
				text = listCursorStyle.Render(agentLabel + agentPad)
			}
			lines = append(lines, listLine{text: text, sessIdx: i, tabIdx: ti})
		}
	}
	return lines
}

// listView renders list mode: big panel on the left, session list sidebar
// on the right.
func (m *tuiModel) listView() string {
	sessions := m.visibleSessions()
	n := len(sessions)
	if n == 0 {
		return m.gridView()
	}
	if m.selected >= n {
		m.selected = n - 1
	}

	status := m.statusBar()
	statusH := lipgloss.Height(status)
	availH := m.height - statusH
	if availH < 1 {
		return status
	}

	mainW, sideW := m.listLayoutWidths()
	primary := sessions[m.selected]
	mainTitle := fmt.Sprintf("▶ [%d] %s", m.selected+1, primary.Name)
	mainPanel := m.renderPanel(primary, mainTitle, mainW, availH, !m.listFocus)
	sidebar := m.renderSessionList(sessions, sideW, availH)

	row := lipgloss.JoinHorizontal(lipgloss.Top, mainPanel, sidebar)
	return lipgloss.JoinVertical(lipgloss.Left, row, status)
}

// renderSessionList renders the sidebar panel with the session list.
func (m *tuiModel) renderSessionList(sessions []*tuiSession, w, h int) string {
	style := tuiNormalBorder.Width(w - 2).Height(h - 2)
	if m.listFocus {
		style = tuiFocusBorder.Width(w - 2).Height(h - 2)
	}

	innerW := w - 2
	innerH := h - 3 // borders + title line
	if innerW < 1 {
		innerW = 1
	}

	title := " Sessions"
	if m.listFocus {
		title += "  ↑/↓ Enter"
	}
	if lipgloss.Width(title) > innerW {
		title = ansi.Truncate(title, innerW, "…")
	}
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("232")).
		Background(sidePanelTitleColor).
		Width(innerW).
		Render(title)

	if innerH < 1 {
		return style.Render(header)
	}

	lines := m.buildListLines(sessions, innerW)

	// Clamp scroll, then keep the cursor's row visible while the sidebar
	// has keyboard focus.
	maxScroll := max(0, len(lines)-innerH)
	m.listScroll = min(max(m.listScroll, 0), maxScroll)
	if m.listFocus {
		cursorLine := 0
		for li, ln := range lines {
			if ln.sessIdx == m.listCursor && ln.tabIdx == m.listCursorTab {
				cursorLine = li
				break
			}
		}
		if cursorLine < m.listScroll {
			m.listScroll = cursorLine
		} else if cursorLine >= m.listScroll+innerH {
			m.listScroll = cursorLine - innerH + 1
		}
	}

	end := min(len(lines), m.listScroll+innerH)
	rows := make([]string, 0, end-m.listScroll)
	for _, ln := range lines[m.listScroll:end] {
		rows = append(rows, ln.text)
	}
	return style.Render(header + "\n" + strings.Join(rows, "\n"))
}

// listPos identifies one navigable sidebar entry: a session header row
// (tabIdx -1) or an agent detail row.
type listPos struct {
	sessIdx int
	tabIdx  int
}

// listPositions returns the navigable sidebar entries in display order:
// each session header followed by its agent rows.
func listPositions(sessions []*tuiSession) []listPos {
	var pos []listPos
	for i, sess := range sessions {
		pos = append(pos, listPos{sessIdx: i, tabIdx: -1})
		for ti, tab := range sess.Tabs {
			if tab.Agent != "" {
				pos = append(pos, listPos{sessIdx: i, tabIdx: ti})
			}
		}
	}
	return pos
}

// listCursorPos finds the current cursor's index in positions, falling
// back to the cursor session's header row when its agent row is gone.
func (m *tuiModel) listCursorPos(positions []listPos) int {
	header := 0
	for pi, p := range positions {
		if p.sessIdx == m.listCursor {
			if p.tabIdx == m.listCursorTab {
				return pi
			}
			if p.tabIdx == -1 {
				header = pi
			}
		}
	}
	return header
}

// listShiftTabPositions returns the Shift+Tab traversal order for the
// sidebar: agents needing attention first (blocked, then done), followed
// by the session header rows.
func (m *tuiModel) listShiftTabPositions() []listPos {
	sessions := m.visibleSessions()
	var blocked, done, headers []listPos
	for i, sess := range sessions {
		headers = append(headers, listPos{sessIdx: i, tabIdx: -1})
		for ti, tab := range sess.Tabs {
			if tab.Agent == "" {
				continue
			}
			switch effectiveAgentState(tab) {
			case "blocked":
				blocked = append(blocked, listPos{sessIdx: i, tabIdx: ti})
			case "done":
				done = append(done, listPos{sessIdx: i, tabIdx: ti})
			}
		}
	}
	return append(append(blocked, done...), headers...)
}

// handleListKey processes keyboard input while the sidebar has focus.
func (m *tuiModel) handleListKey(msg tea.KeyMsg) tea.Cmd {
	sessions := m.visibleSessions()
	n := len(sessions)
	if n == 0 {
		m.listFocus = false
		return nil
	}
	if m.listCursor >= n {
		m.listCursor = n - 1
		m.listCursorTab = -1
	}
	switch msg.Type {
	case tea.KeyUp, tea.KeyDown:
		positions := listPositions(sessions)
		pi := m.listCursorPos(positions)
		if msg.Type == tea.KeyUp {
			pi = (pi - 1 + len(positions)) % len(positions)
		} else {
			pi = (pi + 1) % len(positions)
		}
		m.listCursor = positions[pi].sessIdx
		m.listCursorTab = positions[pi].tabIdx
	case tea.KeyEnter:
		m.selected = m.listCursor
		m.inputFocus = -1
		m.listFocus = false
		// Enter on an agent row also activates that agent's tab.
		if m.listCursorTab >= 0 && m.listCursor < n {
			sess := sessions[m.listCursor]
			if m.listCursorTab < len(sess.Tabs) && m.listCursorTab != sess.ActiveTab {
				m.requestTabSwitch(sess.Name, m.listCursorTab)
			}
		}
	case tea.KeyEscape:
		m.listFocus = false
	default:
		// Any other key returns focus to the big panel without forwarding,
		// so stray typing can't leak into the PTY.
		m.listFocus = false
	}
	return nil
}

// listHitTest maps a screen coordinate inside the sidebar to a visible
// session index and, for agent detail rows, the tab index of the clicked
// agent. Returns (-1, -1) when the position is not on a list entry and
// tabIdx -1 for session header rows.
func (m *tuiModel) listHitTest(x, y int) (sessIdx, tabIdx int) {
	sessions := m.visibleSessions()
	if len(sessions) == 0 {
		return -1, -1
	}
	mainW, sideW := m.listLayoutWidths()
	availH := m.height - 1 // status bar
	innerW := sideW - 2
	innerH := availH - 3
	localX := x - mainW - 1
	localY := y - 2 // top border + title line
	if localX < 0 || localX >= innerW || localY < 0 || localY >= innerH {
		return -1, -1
	}
	lines := m.buildListLines(sessions, innerW)
	li := m.listScroll + localY
	if li < 0 || li >= len(lines) {
		return -1, -1
	}
	return lines[li].sessIdx, lines[li].tabIdx
}
