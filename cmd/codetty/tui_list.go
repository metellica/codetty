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
	// Selected session: a low-contrast selection highlight (VS Code Dark+
	// selection blue), visibly distinct from the gray cursor bar.
	listSelectedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("254")).
				Background(lipgloss.Color("#264f78"))
	listExitedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	listIdleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	// listAccentStyle colors the left accent border for the active session,
	// similar to VS Code's focus indicator.
	listAccentStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#007fd4"))
)

// listAccentChar is the left accent indicator for the active session.
const listAccentChar = "▎"

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

// buildListLines renders sidebar rows as a tree: one header per session
// (with expand/collapse indicator) followed by tab rows when expanded.
// Agent tabs get a special ◆ indicator.
func (m *tuiModel) buildListLines(sessions []*tuiSession, innerW int) []listLine {
	var lines []listLine
	for i, sess := range sessions {
		isSelected := i == m.selected
		contentW := innerW
		if isSelected {
			contentW = innerW - 1 // reserve 1 column for accent bar
			if contentW < 1 {
				contentW = 1
			}
		}

		collapsed := m.listCollapsed[sess.Name]
		// Determine if session has agents to show in tree.
		hasAgent := false
		for _, tab := range sess.Tabs {
			if tab.Agent != "" {
				hasAgent = true
				break
			}
		}
		arrow := " "
		if hasAgent {
			arrow = "▾"
			if collapsed {
				arrow = "▸"
			}
		}

		label := fmt.Sprintf("%s %d %s", arrow, i+1, sess.Name)
		if isSelected {
			label = fmt.Sprintf("%s%d %s", arrow, i+1, sess.Name)
		}
		if sess.Exited {
			label += " (exited)"
		}
		if lipgloss.Width(label) > contentW {
			label = ansi.Truncate(label, contentW, "…")
		}
		pad := strings.Repeat(" ", max(0, contentW-lipgloss.Width(label)))
		var text string
		switch {
		case m.listFocus && i == m.listCursor && m.listCursorTab < 0:
			text = listCursorStyle.Render(label + pad)
		case isSelected:
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
		if isSelected {
			text = listAccentStyle.Render(listAccentChar) + text
		}
		lines = append(lines, listLine{text: text, sessIdx: i, tabIdx: -1, header: true})

		// Show agent children when expanded.
		if collapsed || !hasAgent {
			continue
		}
		for ti, tab := range sess.Tabs {
			if tab.Agent == "" {
				continue
			}
			state := effectiveAgentState(tab)
			word := state
			if word == "" {
				word = "idle"
			}

			indent := "    "
			if isSelected {
				indent = "   " // 1 char less for accent bar
			}
			tabLabel := fmt.Sprintf("%s◆ %s %s", indent, tab.Agent, word)

			if lipgloss.Width(tabLabel) > contentW {
				tabLabel = ansi.Truncate(tabLabel, contentW, "…")
			}

			// Style based on agent state
			var style lipgloss.Style
			switch state {
			case "blocked":
				style = lipgloss.NewStyle().Bold(true).Foreground(agentBlockedColor)
			case "working":
				style = lipgloss.NewStyle().Foreground(agentWorkingColor)
			case "done":
				style = lipgloss.NewStyle().Bold(true).Foreground(agentDoneColor)
			default:
				style = listIdleStyle
			}

			text := style.Render(tabLabel)
			if isSelected {
				text = listAccentStyle.Render(listAccentChar) + text
			}
			lines = append(lines, listLine{text: text, sessIdx: i, tabIdx: ti})
		}
	}
	return lines
}

// listView renders list mode: session tree sidebar on the left, big panel
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

	row := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, mainPanel)
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
		title += " ↑↓←→"
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
// only session headers (agent rows are display-only, not selectable).
func (m *tuiModel) listPositions(sessions []*tuiSession) []listPos {
	var pos []listPos
	for i := range sessions {
		pos = append(pos, listPos{sessIdx: i, tabIdx: -1})
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
// sidebar: sessions with blocked agents first, then done, then the rest.
func (m *tuiModel) listShiftTabPositions() []listPos {
	sessions := m.visibleSessions()
	var blocked, done, rest []listPos
	for i, sess := range sessions {
		hasBlocked, hasDone := false, false
		for _, tab := range sess.Tabs {
			if tab.Agent == "" {
				continue
			}
			state := effectiveAgentState(tab)
			if state == "blocked" {
				hasBlocked = true
			} else if state == "done" {
				hasDone = true
			}
		}
		switch {
		case hasBlocked:
			blocked = append(blocked, listPos{sessIdx: i, tabIdx: -1})
		case hasDone:
			done = append(done, listPos{sessIdx: i, tabIdx: -1})
		default:
			rest = append(rest, listPos{sessIdx: i, tabIdx: -1})
		}
	}
	result := append(blocked, done...)
	return append(result, rest...)
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
		positions := m.listPositions(sessions)
		pi := m.listCursorPos(positions)
		if msg.Type == tea.KeyUp {
			pi = (pi - 1 + len(positions)) % len(positions)
		} else {
			pi = (pi + 1) % len(positions)
		}
		m.listCursor = positions[pi].sessIdx
		m.listCursorTab = -1
	case tea.KeyLeft:
		// Collapse agent tree for current session
		if m.listCursor < n {
			m.listCollapsed[sessions[m.listCursor].Name] = true
		}
	case tea.KeyRight:
		// Expand agent tree for current session
		if m.listCursor < n {
			m.listCollapsed[sessions[m.listCursor].Name] = false
		}
	case tea.KeyEnter:
		m.selected = m.listCursor
		m.inputFocus = -1
		m.listFocus = false
		// Auto-switch to priority tab: noticed agent > active tab > first.
		if m.listCursor < n {
			sess := sessions[m.listCursor]
			targetTab := sessionPriorityTab(sess)
			if targetTab >= 0 && targetTab != sess.ActiveTab {
				m.requestTabSwitch(sess.Name, targetTab)
			}
		}
	case tea.KeyEscape:
		m.listFocus = false
	default:
		m.listFocus = false
	}
	return nil
}

// sessionPriorityTab returns the tab to show when selecting a session.
// Priority: a) first noticed agent (blocked > done), b) current active tab,
// c) first tab. Returns -1 if the session has no tabs.
func sessionPriorityTab(sess *tuiSession) int {
	for ti, tab := range sess.Tabs {
		if tab.Agent != "" && effectiveAgentState(tab) == "blocked" {
			return ti
		}
	}
	for ti, tab := range sess.Tabs {
		if tab.Agent != "" && effectiveAgentState(tab) == "done" {
			return ti
		}
	}
	if sess.ActiveTab >= 0 && sess.ActiveTab < len(sess.Tabs) {
		return sess.ActiveTab
	}
	if len(sess.Tabs) > 0 {
		return 0
	}
	return -1
}

// listHitTest maps a screen coordinate inside the sidebar to a visible
// session index. Returns (-1, -1) when the position is not on a list entry.
func (m *tuiModel) listHitTest(x, y int) (sessIdx, tabIdx int) {
	sessions := m.visibleSessions()
	if len(sessions) == 0 {
		return -1, -1
	}
	_, sideW := m.listLayoutWidths()
	availH := m.height - 1 // status bar
	innerW := sideW - 2
	innerH := availH - 3
	localX := x - 1 // sidebar is on the left; skip left border
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
