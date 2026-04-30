package hub

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	minPanelW = 14
	minPanelH = 3
)

func (m *Model) panelSize(n, cols, availableH int) (int, int) {
	if n == 0 || cols <= 0 {
		return m.width, availableH
	}
	rows := (n + cols - 1) / cols
	panelW := m.width / cols
	panelH := availableH / rows
	if panelW < minPanelW {
		panelW = minPanelW
	}
	if availableH >= rows*5 && panelH < 5 {
		panelH = 5
	}
	return panelW, panelH
}

func (m *Model) gridView() string {
	sessions := m.orderedSessions()
	n := len(sessions)
	status := m.statusBar(n)
	metrics, ok := m.gridMetrics(n)
	if !ok {
		if renderedLineCount(status) > m.height {
			return constrainRenderedLineCount(status, m.height)
		}
		return status
	}

	var rowViews []string
	for displayRow := 0; displayRow < metrics.visibleRows; displayRow++ {
		r := metrics.startRow + displayRow
		var colViews []string
		for c := 0; c < metrics.cols; c++ {
			idx := r*metrics.cols + c
			if idx >= n {
				colViews = append(colViews, strings.Repeat(" ", metrics.panelW))
				continue
			}
			sess := sessions[idx]
			title := fmt.Sprintf("[%d] %s", idx+1, sess.Title)
			focused := idx == m.selected
			if focused {
				title = fmt.Sprintf("▶ [%d] %s", idx+1, sess.Title)
			}

			panel := m.renderSessionPanelWithOuterHeight(sess, title, metrics.panelW, metrics.panelH, focused)
			colViews = append(colViews, panel)
		}
		rowViews = append(rowViews, lipgloss.JoinHorizontal(lipgloss.Top, colViews...))
	}

	grid := lipgloss.JoinVertical(lipgloss.Left, rowViews...)
	if grid == "" {
		return status
	}
	return lipgloss.JoinVertical(lipgloss.Left, grid, status)
}

func (m *Model) focusBigView() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	sessions := m.orderedSessions()
	n := len(sessions)
	if n == 0 || m.selected < 0 || m.selected >= n {
		return m.gridView()
	}

	status := m.focusBigStatusBar(n)
	statusH := renderedLineCount(status)
	if statusH > m.height {
		status = constrainRenderedLineCount(status, m.height)
		statusH = renderedLineCount(status)
	}
	availableH := m.height - statusH
	if availableH < 0 {
		availableH = 0
	}

	if n == 1 {
		primary := sessions[m.selected]
		mainTitle := fmt.Sprintf("MAIN [%d] %s", m.selected+1, primary.Title)
		layout := m.renderSessionPanelWithOuterHeight(primary, mainTitle, m.width, availableH, true)
		if layout == "" {
			return status
		}
		return lipgloss.JoinVertical(lipgloss.Left, layout, status)
	}

	var layout string
	if m.width >= 120 {
		layout = m.focusBigWideView(sessions, availableH)
	} else {
		layout = m.focusBigNarrowView(sessions, availableH)
	}
	if layout == "" {
		return status
	}
	return lipgloss.JoinVertical(lipgloss.Left, layout, status)
}

func (m *Model) focusBigWideView(sessions []*ClientSession, availableH int) string {
	primary := sessions[m.selected]
	mainTitle := fmt.Sprintf("MAIN [%d] %s", m.selected+1, primary.Title)
	if availableH < minPanelH {
		return m.renderSessionPanelWithOuterHeight(primary, mainTitle, m.width, availableH, true)
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

func (m *Model) focusBigNarrowView(sessions []*ClientSession, availableH int) string {
	primary := sessions[m.selected]
	mainTitle := fmt.Sprintf("MAIN [%d] %s", m.selected+1, primary.Title)
	if availableH < minPanelH*2 {
		return m.renderSessionPanelWithOuterHeight(primary, mainTitle, m.width, availableH, true)
	}

	mainPanelH := availableH * 65 / 100
	if mainPanelH < minPanelH {
		mainPanelH = minPanelH
	}
	if maxMainH := availableH - minPanelH; mainPanelH > maxMainH {
		mainPanelH = maxMainH
	}

	mainPanel := m.renderFocusBigSessionPanel(primary, mainTitle, m.width, mainPanelH-2, true)

	secondaryCount := len(sessions) - 1
	secondaryH := availableH - mainPanelH
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

func renderedLineCount(s string) int {
	trimmed := strings.TrimRight(s, "\n")
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "\n"))
}

func constrainRenderedLineCount(s string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	trimmed := strings.TrimRight(s, "\n")
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) <= maxLines {
		return trimmed
	}
	return strings.Join(lines[:maxLines], "\n")
}

func constrainRenderedWidth(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	trimmed := strings.TrimRight(s, "\n")
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, maxW, "")
	}
	return strings.Join(lines, "\n")
}

func morePanel(hidden, panelW, contentH int) string {
	if panelW <= 0 {
		return ""
	}
	label := fmt.Sprintf("+%d more", hidden)
	if contentH < 1 {
		contentH = 1
	}
	if panelW < 5 {
		return truncateToWidth(label, panelW)
	}
	return renderPanel(fitPanelTitle(label, panelW), truncateToWidth(label, panelW-2), panelW, contentH, false)
}

func (m *Model) renderFocusBigSessionPanel(sess *ClientSession, title string, panelW, contentH int, focused bool) string {
	if contentH < 1 {
		contentH = 1
	}
	if panelW < 5 {
		return m.renderPlainFocusBigSessionPanel(sess, title, panelW, contentH+2)
	}
	return m.renderSessionPanel(sess, fitPanelTitle(title, panelW), panelW, contentH, focused)
}

func (m *Model) renderSessionPanelWithOuterHeight(sess *ClientSession, title string, panelW, panelH int, focused bool) string {
	if panelH <= 0 {
		return ""
	}
	if panelH == 1 {
		return truncateToWidth(title, panelW)
	}
	if panelW < 5 {
		return m.renderPlainFocusBigSessionPanel(sess, title, panelW, panelH)
	}
	contentH := panelH - 2
	contentW := panelW - 2
	if contentW < 1 {
		contentW = 1
	}

	content := ""
	if contentH > 0 {
		contentLines, _, _ := sess.RenderANSI(contentW, contentH)
		content = strings.Join(contentLines, "\n")
	}
	return renderPanel(fitPanelTitle(title, panelW), content, panelW, contentH, focused)
}

func (m *Model) renderPlainFocusBigSessionPanel(sess *ClientSession, title string, panelW, panelH int) string {
	if panelW <= 0 || panelH <= 0 {
		return ""
	}

	lines := make([]string, 0, panelH)
	if title != "" {
		lines = append(lines, truncateToWidth(title, panelW))
	}

	remaining := panelH - len(lines)
	if remaining > 0 {
		contentLines, _, _ := sess.RenderANSI(panelW, remaining)
		for _, line := range contentLines {
			if len(lines) >= panelH {
				break
			}
			lines = append(lines, ansi.Truncate(line, panelW, ""))
		}
	}
	if len(lines) > panelH {
		lines = lines[:panelH]
	}
	return strings.Join(lines, "\n")
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
	if m.width < 5 {
		return truncateToWidth(m.statusHelp(n), m.width)
	}
	return constrainRenderedWidth(statusBarStyle.Width(m.width).Render(m.statusHelp(n)), m.width)
}

func (m *Model) focusBigStatusBar(n int) string {
	return m.statusBar(n)
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
