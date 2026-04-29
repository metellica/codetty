package hub

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	minPanelW = 14
	minPanelH = 3
)

func (m *Model) panelSize(n, cols int) (int, int) {
	if n == 0 || cols <= 0 {
		return m.width, m.height - 2
	}
	rows := (n + cols - 1) / cols
	panelW := m.width / cols
	panelH := (m.height - 1) / rows // -1 for status bar
	if panelW < minPanelW {
		panelW = minPanelW
	}
	if panelH < 5 {
		panelH = 5
	}
	return panelW, panelH
}

func (m *Model) gridView() string {
	sessions := m.orderedSessions()
	n := len(sessions)
	cols := m.layout.gridCols(n, m.width, m.height)
	rows := (n + cols - 1) / cols
	panelW, panelH := m.panelSize(n, cols)

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
	return lipgloss.JoinVertical(lipgloss.Left, grid, m.statusBar(n))
}

func (m *Model) focusBigView() string {
	sessions := m.orderedSessions()
	n := len(sessions)
	if n <= 1 || m.selected < 0 || m.selected >= n {
		return m.gridView()
	}

	var layout string
	if m.width >= 120 {
		layout = m.focusBigWideView(sessions)
	} else {
		layout = m.focusBigNarrowView(sessions)
	}
	return lipgloss.JoinVertical(lipgloss.Left, layout, m.statusBar(n))
}

func (m *Model) focusBigWideView(sessions []*ClientSession) string {
	availableH := m.height - 1
	if availableH < minPanelH {
		availableH = minPanelH
	}

	mainW := m.width * 7 / 10
	sideW := m.width - mainW
	if sideW < minPanelW {
		sideW = minPanelW
		mainW = m.width - sideW
	}
	if mainW < minPanelW {
		mainW = minPanelW
	}

	primary := sessions[m.selected]
	mainTitle := fmt.Sprintf("MAIN [%d] %s", m.selected+1, primary.Title)
	mainPanel := m.renderFocusBigSessionPanel(primary, mainTitle, mainW, availableH-2, true)

	secondaryCount := len(sessions) - 1
	visible, hidden := fitSecondaryCount(availableH, minPanelH, secondaryCount)
	renderedSlots := visible
	if hidden > 0 && availableH >= minPanelH {
		renderedSlots++
	}
	secondaries := make([]string, 0, renderedSlots)
	slotIndex := 0
	for idx, sess := range sessions {
		if idx == m.selected {
			continue
		}
		if len(secondaries) >= visible {
			break
		}
		panelH := distributedSlotSize(availableH, renderedSlots, slotIndex)
		title := fmt.Sprintf("[%d] %s", idx+1, sess.Title)
		secondaries = append(secondaries, m.renderFocusBigSessionPanel(sess, title, sideW, panelH-2, false))
		slotIndex++
	}
	if hidden > 0 && renderedSlots > 0 {
		panelH := distributedSlotSize(availableH, renderedSlots, slotIndex)
		secondaries = append(secondaries, morePanel(hidden, sideW, panelH-2))
	}

	side := lipgloss.JoinVertical(lipgloss.Left, secondaries...)
	return lipgloss.JoinHorizontal(lipgloss.Top, mainPanel, side)
}

func (m *Model) focusBigNarrowView(sessions []*ClientSession) string {
	availableH := m.height - 1
	if availableH < 4 {
		availableH = 4
	}

	mainPanelH := availableH * 65 / 100
	if mainPanelH < 3 {
		mainPanelH = 3
	}
	if mainPanelH > availableH-1 {
		mainPanelH = availableH - 1
	}

	primary := sessions[m.selected]
	mainTitle := fmt.Sprintf("MAIN [%d] %s", m.selected+1, primary.Title)
	mainPanel := m.renderFocusBigSessionPanel(primary, mainTitle, m.width, mainPanelH-2, true)

	secondaryCount := len(sessions) - 1
	secondaryH := availableH - mainPanelH
	if secondaryH < minPanelH {
		secondaryH = minPanelH
	}
	visible, hidden := fitSecondaryCount(m.width, minPanelW, secondaryCount)
	renderedSlots := visible
	if hidden > 0 && m.width >= minPanelW {
		renderedSlots++
	}
	secondaries := make([]string, 0, renderedSlots)
	slotIndex := 0
	for idx, sess := range sessions {
		if idx == m.selected {
			continue
		}
		if len(secondaries) >= visible {
			break
		}
		panelW := distributedSlotSize(m.width, renderedSlots, slotIndex)
		title := fmt.Sprintf("[%d] %s", idx+1, sess.Title)
		secondaries = append(secondaries, m.renderFocusBigSessionPanel(sess, title, panelW, secondaryH-2, false))
		slotIndex++
	}
	if hidden > 0 && renderedSlots > 0 {
		panelW := distributedSlotSize(m.width, renderedSlots, slotIndex)
		secondaries = append(secondaries, morePanel(hidden, panelW, secondaryH-2))
	}

	secondaryRow := lipgloss.JoinHorizontal(lipgloss.Top, secondaries...)
	return lipgloss.JoinVertical(lipgloss.Left, mainPanel, secondaryRow)
}

func fitSecondaryCount(total, minSize, count int) (visible int, hidden int) {
	if count <= 0 {
		return 0, 0
	}
	if total < minSize {
		return 0, count
	}

	maxSlots := total / minSize
	if count <= maxSlots {
		return count, 0
	}
	if maxSlots == 1 {
		return 0, count
	}
	visible = maxSlots - 1
	hidden = count - visible
	return visible, hidden
}

func distributedSlotSize(total, slots, index int) int {
	if slots <= 0 {
		return 0
	}
	size := total / slots
	if index < total%slots {
		size++
	}
	return size
}

func morePanel(hidden, panelW, contentH int) string {
	label := fmt.Sprintf("+%d more", hidden)
	if contentH < 1 {
		contentH = 1
	}
	return renderPanel(fitPanelTitle(label, panelW), truncateToWidth(label, panelW-2), panelW, contentH, false)
}

func (m *Model) renderFocusBigSessionPanel(sess *ClientSession, title string, panelW, contentH int, focused bool) string {
	return m.renderSessionPanel(sess, fitPanelTitle(title, panelW), panelW, contentH, focused)
}

func fitPanelTitle(title string, panelW int) string {
	return truncateToWidth(title, panelW-5)
}

func truncateToWidth(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxW {
		return s
	}
	var b strings.Builder
	width := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if width+rw > maxW {
			break
		}
		b.WriteRune(r)
		width += rw
	}
	return b.String()
}

func (m *Model) renderSessionPanel(sess *ClientSession, title string, panelW, contentH int, focused bool) string {
	contentW := panelW - 2
	if contentW < 1 {
		contentW = 1
	}
	if contentH < 1 {
		contentH = 1
	}

	contentLines, _, _ := sess.RenderANSI(contentW, contentH)
	content := strings.Join(contentLines, "\n")
	return renderPanel(title, content, panelW, contentH, focused)
}

func (m *Model) statusBar(n int) string {
	return statusBarStyle.Width(m.width).Render(m.statusHelp(n))
}

func (m *Model) statusHelp(n int) string {
	if m.layout.prefix {
		return "PREFIX Ctrl+A: n/p switch  f fullscreen  h/l move  1-4 cols  a auto  b focus-big  g grid  \\ quit  Ctrl+A send"
	}
	return fmt.Sprintf("Prefix Ctrl+A | n/p switch | f fullscreen | h/l move | 1-4 cols | a auto | b focus-big | g grid | \\ quit | layout: %s | clients: %d", m.layout.modeName(), n)
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

	bottom := bc.Render("╰" + strings.Repeat("─", innerW) + "╯")

	return top + "\n" + body.String() + bottom
}
