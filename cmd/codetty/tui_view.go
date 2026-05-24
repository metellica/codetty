package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	tuiMinPanelW = 14
	tuiMinPanelH = 3
	// Side panels shorter than this threshold show only the title, no content.
	tuiSidePanelContentThreshold = 6
)

// sessionColors is a palette for distinguishing side panel titles at a glance.
var sessionColors = []lipgloss.Color{
	lipgloss.Color("62"),  // steel blue
	lipgloss.Color("132"), // mauve
	lipgloss.Color("72"),  // teal
	lipgloss.Color("172"), // orange
	lipgloss.Color("134"), // purple
	lipgloss.Color("107"), // olive green
	lipgloss.Color("167"), // salmon
	lipgloss.Color("68"),  // cornflower
}

// sessionColor returns a stable color for a session based on its index.
func sessionColor(idx int) lipgloss.Color {
	return sessionColors[idx%len(sessionColors)]
}

// fullscreenView renders only the selected session filling the entire terminal.
func (m *tuiModel) fullscreenView() string {
	sess := m.selectedSession()
	if sess == nil {
		m.mode = modeFocusBig
		return m.focusBigView()
	}
	if m.sel.session == sess.Name && (m.sel.active || m.sel.startX != m.sel.endX || m.sel.startY != m.sel.endY) {
		selFn := func(col, row int) bool {
			return m.selectionCovers(sess.Name, col, row)
		}
		lines := sess.RenderANSIWithSelection(m.width, m.height, selFn)
		return strings.Join(lines, "\n")
	}
	lines, _, _ := sess.RenderANSI(m.width, m.height)
	return strings.Join(lines, "\n")
}

func (m *tuiModel) focusBigView() string {
	sessions := m.orderedSessions()
	n := len(sessions)
	if n == 0 || m.selected >= n {
		return m.gridView()
	}

	status := m.statusBar()
	statusH := lipgloss.Height(status)
	availableH := m.height - statusH
	if availableH < 1 {
		return status
	}

	if n == 1 {
		sess := sessions[m.selected]
		title := fmt.Sprintf("[1] %s", sess.Name)
		panel := m.renderPanel(sess, title, m.width, availableH, true)
		return lipgloss.JoinVertical(lipgloss.Left, panel, status)
	}

	if m.width >= 120 {
		return lipgloss.JoinVertical(lipgloss.Left, m.focusBigWide(sessions, availableH), status)
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.focusBigNarrow(sessions, availableH), status)
}

func (m *tuiModel) focusBigWide(sessions []*tuiSession, availH int) string {
	primary := sessions[m.selected]
	mainW := m.width * 7 / 10
	sideW := m.width - mainW
	if sideW < tuiMinPanelW {
		sideW = tuiMinPanelW
		mainW = m.width - sideW
	}

	mainFocused := m.inputFocus < 0 || m.inputFocus == m.selected
	mainTitle := fmt.Sprintf("[%d] %s", m.selected+1, primary.Name)
	if mainFocused {
		mainTitle = "▶ " + mainTitle
	}
	mainPanel := m.renderPanel(primary, mainTitle, mainW, availH, mainFocused)

	secondaryCount := len(sessions) - 1
	if secondaryCount == 0 {
		return mainPanel
	}
	slotH := availH / secondaryCount
	if slotH < tuiMinPanelH {
		slotH = tuiMinPanelH
	}
	visible := availH / slotH
	if visible > secondaryCount {
		visible = secondaryCount
	}

	var sides []string
	idx := 0
	for i, sess := range sessions {
		if i == m.selected {
			continue
		}
		if idx >= visible {
			break
		}
		h := slotH
		if idx == visible-1 {
			h = availH - slotH*idx
		}
		title := fmt.Sprintf("[%d] %s", i+1, sess.Name)
		sideFocused := m.inputFocus == i
		if sideFocused {
			title = "▶ " + title
		}
		showContent := h >= tuiSidePanelContentThreshold
		sides = append(sides, m.renderPanelWithColor(sess, title, sideW, h, sideFocused, i, showContent))
		idx++
	}

	side := lipgloss.JoinVertical(lipgloss.Left, sides...)
	return lipgloss.JoinHorizontal(lipgloss.Top, mainPanel, side)
}

func (m *tuiModel) focusBigNarrow(sessions []*tuiSession, availH int) string {
	primary := sessions[m.selected]
	mainH := availH * 7 / 10
	sideH := availH - mainH

	mainFocused := m.inputFocus < 0 || m.inputFocus == m.selected
	mainTitle := fmt.Sprintf("[%d] %s", m.selected+1, primary.Name)
	if mainFocused {
		mainTitle = "▶ " + mainTitle
	}
	mainPanel := m.renderPanel(primary, mainTitle, m.width, mainH, mainFocused)

	var bottomPanel string
	for i, sess := range sessions {
		if i != m.selected {
			title := fmt.Sprintf("[%d] %s", i+1, sess.Name)
			sideFocused := m.inputFocus == i
			if sideFocused {
				title = "▶ " + title
			}
			showContent := sideH >= tuiSidePanelContentThreshold
			bottomPanel = m.renderPanelWithColor(sess, title, m.width, sideH, sideFocused, i, showContent)
			break
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, mainPanel, bottomPanel)
}

func (m *tuiModel) gridView() string {
	sessions := m.orderedSessions()
	n := len(sessions)
	if n == 0 {
		return m.statusBar()
	}

	status := m.statusBar()
	statusH := lipgloss.Height(status)
	availH := m.height - statusH

	cols := gridCols(n, m.width)
	rows := (n + cols - 1) / cols
	panelW := m.width / cols
	panelH := availH / rows
	if panelH < tuiMinPanelH {
		panelH = tuiMinPanelH
	}

	var rowViews []string
	for r := 0; r < rows; r++ {
		var colViews []string
		for c := 0; c < cols; c++ {
			idx := r*cols + c
			if idx >= n {
				colViews = append(colViews, strings.Repeat(" ", panelW))
				continue
			}
			sess := sessions[idx]
			title := fmt.Sprintf("[%d] %s", idx+1, sess.Name)
			focused := idx == m.selected && m.inputFocus < 0
			if m.inputFocus >= 0 {
				focused = idx == m.inputFocus
			}
			if focused {
				title = "▶ " + title
			}
			colViews = append(colViews, m.renderPanel(sess, title, panelW, panelH, focused))
		}
		rowViews = append(rowViews, lipgloss.JoinHorizontal(lipgloss.Top, colViews...))
	}

	grid := lipgloss.JoinVertical(lipgloss.Left, rowViews...)
	return lipgloss.JoinVertical(lipgloss.Left, grid, status)
}

func (m *tuiModel) renderPanel(sess *tuiSession, title string, w, h int, focused bool) string {
	return m.renderPanelWithColor(sess, title, w, h, focused, -1, true)
}

// renderPanelWithColor renders a panel with an optional color index for the title.
// If colorIdx >= 0, the title gets a colored background. If showContent is false,
// only the title is rendered (useful for very small side panels).
func (m *tuiModel) renderPanelWithColor(sess *tuiSession, title string, w, h int, focused bool, colorIdx int, showContent bool) string {
	style := tuiNormalBorder.Width(w - 2).Height(h - 2)
	if focused {
		style = tuiFocusBorder.Width(w - 2).Height(h - 2)
	}

	maxTitleW := w - 4
	if maxTitleW > 0 && lipgloss.Width(title) > maxTitleW {
		title = ansi.Truncate(title, maxTitleW, "…")
	}

	innerW := w - 2
	innerH := h - 3
	if innerW < 1 || innerH < 1 {
		return style.Render(title)
	}

	// Build title with optional color background
	var header string
	if colorIdx >= 0 {
		clr := sessionColor(colorIdx)
		titleStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("255")).
			Background(clr).
			Width(innerW)
		header = titleStyle.Render(" " + title)
	} else {
		header = lipgloss.NewStyle().Bold(focused).Render(title)
	}

	if !showContent {
		// Center the title vertically in the available space
		padding := strings.Repeat("\n", innerH/2)
		return style.Render(padding + header)
	}

	var lines []string
	if m.sel.session == sess.Name && (m.sel.active || m.sel.startX != m.sel.endX || m.sel.startY != m.sel.endY) {
		selFn := func(col, row int) bool {
			return m.selectionCovers(sess.Name, col, row)
		}
		lines = sess.RenderANSIWithSelection(innerW, innerH, selFn)
	} else {
		lines, _, _ = sess.RenderANSI(innerW, innerH)
	}
	content := strings.Join(lines, "\n")

	return style.Render(header + "\n" + content)
}

func (m *tuiModel) statusBar() string {
	if m.renaming {
		prompt := fmt.Sprintf(" Rename: %s█  (Enter confirm, Esc cancel)", m.renameText)
		return tuiStatusStyle.Width(m.width).Render(prompt)
	}
	if m.creating {
		prompt := fmt.Sprintf(" New session name: %s█  (Enter confirm, Esc cancel)", m.createText)
		return tuiStatusStyle.Width(m.width).Render(prompt)
	}

	sess := m.focusedSession()
	var name string
	if sess != nil {
		name = sess.Name
	}
	n := len(m.sessions)

	if m.prefix {
		help := "^A: d detach  c new  x kill  r rename  n/p nav  f full  g grid  b focus  a auto  h/l move  1-9 jump"
		return tuiStatusStyle.Width(m.width).Render(" " + help)
	}

	left := fmt.Sprintf(" [%s] %d/%d", name, m.selected+1, n)
	modeName := "focus"
	switch m.mode {
	case modeGrid:
		modeName = "grid"
	case modeFullscreen:
		modeName = "fullscreen"
	}
	right := fmt.Sprintf(" %s | ^A prefix | ctrl+click switch ", modeName)
	contentW := max(0, m.width-2)
	mid := strings.Repeat(" ", max(0, contentW-lipgloss.Width(left)-lipgloss.Width(right)))

	return tuiStatusStyle.Width(m.width).Render(left + mid + right)
}

func (m *tuiModel) hitTest(x, y int) int {
	sessions := m.orderedSessions()
	n := len(sessions)
	if n == 0 || m.mode == modeFullscreen {
		return -1
	}

	statusH := 1
	availH := m.height - statusH

	if m.mode == modeGrid {
		cols := gridCols(n, m.width)
		panelW := m.width / cols
		rows := (n + cols - 1) / cols
		panelH := availH / rows
		if panelH < tuiMinPanelH {
			panelH = tuiMinPanelH
		}
		col := x / panelW
		row := y / panelH
		idx := row*cols + col
		if idx >= 0 && idx < n {
			return idx
		}
		return -1
	}

	// Focus-big mode
	if n == 1 {
		if y < availH {
			return 0
		}
		return -1
	}

	if m.width >= 120 {
		mainW := m.width * 7 / 10
		if x < mainW {
			return m.selected
		}
		// Sidebar panels
		sideW := m.width - mainW
		if sideW < tuiMinPanelW {
			return m.selected
		}
		secondaryCount := n - 1
		slotH := availH / secondaryCount
		if slotH < tuiMinPanelH {
			slotH = tuiMinPanelH
		}
		visible := availH / slotH
		if visible > secondaryCount {
			visible = secondaryCount
		}
		slot := y / slotH
		if slot >= visible {
			return -1
		}
		idx := 0
		for i := 0; i < n; i++ {
			if i == m.selected {
				continue
			}
			if idx == slot {
				return i
			}
			idx++
		}
	} else {
		// Narrow: top is main, bottom is secondary
		mainH := availH * 7 / 10
		if y < mainH {
			return m.selected
		}
		for i := 0; i < n; i++ {
			if i != m.selected {
				return i
			}
		}
	}
	return -1
}

func gridCols(n, width int) int {
	if n <= 1 {
		return 1
	}
	if width >= 160 && n >= 4 {
		return 4
	}
	if width >= 120 && n >= 3 {
		return 3
	}
	if n >= 2 {
		return 2
	}
	return 1
}
