package hub

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m *Model) panelSize(n, cols int) (int, int) {
	if n == 0 || cols <= 0 {
		return m.width, m.height - 2
	}
	rows := (n + cols - 1) / cols
	panelW := m.width / cols
	panelH := (m.height - 1) / rows // -1 for status bar
	if panelW < 14 {
		panelW = 14
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
	help := statusBarStyle.Width(m.width).Render(m.statusHelp(n))
	return lipgloss.JoinVertical(lipgloss.Left, grid, help)
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
