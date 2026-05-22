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
)

// fullscreenView renders only the selected session filling the entire terminal.
func (m *tuiModel) fullscreenView() string {
	sess := m.selectedSession()
	if sess == nil {
		m.mode = modeFocusBig
		return m.focusBigView()
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

	mainTitle := fmt.Sprintf("[%d] %s", m.selected+1, primary.Name)
	mainPanel := m.renderPanel(primary, mainTitle, mainW, availH, true)

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
		sides = append(sides, m.renderPanel(sess, title, sideW, h, false))
		idx++
	}

	side := lipgloss.JoinVertical(lipgloss.Left, sides...)
	return lipgloss.JoinHorizontal(lipgloss.Top, mainPanel, side)
}

func (m *tuiModel) focusBigNarrow(sessions []*tuiSession, availH int) string {
	primary := sessions[m.selected]
	mainH := availH * 7 / 10
	sideH := availH - mainH

	mainTitle := fmt.Sprintf("[%d] %s", m.selected+1, primary.Name)
	mainPanel := m.renderPanel(primary, mainTitle, m.width, mainH, true)

	var bottomPanel string
	for i, sess := range sessions {
		if i != m.selected {
			title := fmt.Sprintf("[%d] %s", i+1, sess.Name)
			bottomPanel = m.renderPanel(sess, title, m.width, sideH, false)
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
			focused := idx == m.selected
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

	lines, _, _ := sess.RenderANSI(innerW, innerH)
	content := strings.Join(lines, "\n")

	header := lipgloss.NewStyle().Bold(focused).Render(title)
	return style.Render(header + "\n" + content)
}

func (m *tuiModel) statusBar() string {
	sess := m.selectedSession()
	var name string
	if sess != nil {
		name = sess.Name
	}
	n := len(m.sessions)

	left := fmt.Sprintf(" [%s] %d/%d", name, m.selected+1, n)
	right := " Ctrl+A ? help "
	contentW := max(0, m.width-2)
	mid := strings.Repeat(" ", max(0, contentW-lipgloss.Width(left)-lipgloss.Width(right)))

	return tuiStatusStyle.Width(m.width).Render(left + mid + right)
}

func (m *tuiModel) hitTest(x, y int) int {
	if m.mode != modeGrid {
		return -1
	}
	n := len(m.sessions)
	if n == 0 {
		return -1
	}
	cols := gridCols(n, m.width)
	panelW := m.width / cols
	statusH := 1
	availH := m.height - statusH
	rows := (n + cols - 1) / cols
	panelH := availH / rows

	col := x / panelW
	row := y / panelH
	idx := row*cols + col
	if idx >= 0 && idx < n {
		return idx
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
