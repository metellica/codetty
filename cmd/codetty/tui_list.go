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
	// listActiveStyle is used for the selected session (blue background).
	listActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("254")).
			Background(lipgloss.Color("#264f78"))
	// listCursorStyle is used for the keyboard-browsing cursor (Shift+Tab)
	// before Enter confirms — a distinct gray background.
	listCursorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("238"))
	listExitedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	listIdleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
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
// sync. Box border rows carry the session index with tabIdx -1 so clicking
// a border still selects the session.
type listLine struct {
	text    string
	sessIdx int
	tabIdx  int  // tab index for agent detail rows, -1 for header/border rows
	header  bool // true for the session name row (vs. agent/border rows)
}

// listSessionBorderColor picks the box border color for one session entry,
// mirroring the big-panel convention: blocked always wins, then selection,
// then cursor, then agent activity, then exited/normal.
func listSessionBorderColor(sess *tuiSession, isSelected, isCursor bool) lipgloss.Color {
	state := sessionAgentState(sess)
	switch {
	case state == "blocked":
		return agentBlockedColor
	case isSelected:
		return lipgloss.Color("86")
	case isCursor:
		return lipgloss.Color("250")
	case state == "working":
		return agentWorkingColor
	case state == "done":
		return agentDoneColor
	case sess.Exited:
		return lipgloss.Color("238")
	}
	return lipgloss.Color("240")
}

// buildListLines renders sidebar rows: one rounded box per session framing
// the session header row and its agent tab rows.
func (m *tuiModel) buildListLines(sessions []*tuiSession, innerW int) []listLine {
	var lines []listLine
	boxW := max(1, innerW-2) // content width between the │ borders
	for i, sess := range sessions {
		isSelected := i == m.selected
		isCursor := m.listFocus && i == m.listCursor && i != m.selected

		border := lipgloss.NewStyle().
			Foreground(listSessionBorderColor(sess, isSelected, isCursor))
		edge := strings.Repeat("─", boxW)
		side := border.Render("│")
		wrap := func(content string) string { return side + content + side }

		lines = append(lines, listLine{
			text: border.Render("╭" + edge + "╮"), sessIdx: i, tabIdx: -1})

		label := fmt.Sprintf(" %d %s", i+1, sess.Name)
		if sess.Exited {
			label += " (exited)"
		}
		if lipgloss.Width(label) > boxW {
			label = ansi.Truncate(label, boxW, "…")
		}
		pad := strings.Repeat(" ", max(0, boxW-lipgloss.Width(label)))
		var text string
		switch {
		case isCursor:
			text = listCursorStyle.Render(label + pad)
		case isSelected:
			style := listActiveStyle
			if sess.Exited {
				style = style.Foreground(lipgloss.Color("243"))
			}
			text = style.Render(label + pad)
		case sess.Exited:
			text = listExitedStyle.Render(label) + pad
		default:
			text = label + pad
		}
		lines = append(lines, listLine{text: wrap(text), sessIdx: i, tabIdx: -1, header: true})

		// Show agent children.
		for ti, tab := range sess.Tabs {
			if tab.Agent == "" {
				continue
			}
			state := effectiveAgentState(tab)
			word := state
			if word == "" {
				word = "idle"
			}

			var badge string
			switch state {
			case "working":
				badge = agentBadge(state)
			case "blocked":
				badge = "■"
			case "done":
				badge = "●"
			default:
				badge = "○"
			}
			tabLabel := fmt.Sprintf("   %s %s %s", badge, tab.Agent, word)
			if tab.Remote != "" {
				tabLabel = fmt.Sprintf("   %s %s@%s %s", badge, tab.Agent, tab.Remote, word)
			}
			if lipgloss.Width(tabLabel) > boxW {
				tabLabel = ansi.Truncate(tabLabel, boxW, "…")
			}

			tabPad := strings.Repeat(" ", max(0, boxW-lipgloss.Width(tabLabel)))

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

			if isCursor {
				style = style.Background(listCursorStyle.GetBackground())
				text = style.Render(tabLabel + tabPad)
			} else if isSelected {
				style = style.Background(listActiveStyle.GetBackground())
				text = style.Render(tabLabel + tabPad)
			} else {
				text = style.Render(tabLabel) + tabPad
			}
			lines = append(lines, listLine{text: wrap(text), sessIdx: i, tabIdx: ti})
		}

		lines = append(lines, listLine{
			text: border.Render("╰" + edge + "╯"), sessIdx: i, tabIdx: -1})
	}
	return lines
}

// listView renders list mode: session tree sidebar on the left, big panel
// on the right.
func (m *tuiModel) listView() string {
	sessions := m.visibleSessions()
	n := len(sessions)
	if n == 0 {
		return m.statusBar()
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
	mainPanel := m.renderBigPanelNoTitle(primary, mainW, availH, !m.listFocus)
	if !m.listFocus {
		m.stashBigPanelCursor(primary, sideW, 0, mainW, availH)
	}
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
		title += " ↑↓"
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

	// Clamp scroll, then keep the cursor session's whole box visible while
	// the sidebar has keyboard focus (top edge wins when it doesn't fit).
	maxScroll := max(0, len(lines)-innerH)
	m.listScroll = min(max(m.listScroll, 0), maxScroll)
	if m.listFocus {
		first, last := -1, -1
		for li, ln := range lines {
			if ln.sessIdx == m.listCursor {
				if first == -1 {
					first = li
				}
				last = li
			}
		}
		if first >= 0 {
			if last >= m.listScroll+innerH {
				m.listScroll = last - innerH + 1
			}
			if first < m.listScroll {
				m.listScroll = first
			}
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
	case tea.KeyEnter:
		// Auto-switch to priority tab: noticed agent > active tab > first.
		m.promoteToBig(m.listCursor)
		m.listFocus = false
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
