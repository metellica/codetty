package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Preferred switcher panel height: 10 content lines + border (2) + title (1).
const switcherPanelH = 13

// sessionSwitcher is the state for the fullscreen session switcher overlay.
// It lists all visible sessions except the current one as live preview
// panels (rendered like focus-big side panels) so the user can pick a
// session by its current output.
type sessionSwitcher struct {
	active bool
	cursor int // index into switcherCandidates()

	// Screen geometry of the last rendered overlay, used for mouse
	// hit-testing. Updated by renderSwitcher on every View pass.
	boxX, boxY, boxW, boxH int // outer box rect
	gridX, gridY           int // top-left of the first panel
	cols, panelW, panelH   int
}

// switcherCandidates returns visible-session indices excluding m.selected.
func (m *tuiModel) switcherCandidates() []int {
	visible := m.visibleSessions()
	cands := make([]int, 0, len(visible))
	for i := range visible {
		if i != m.selected {
			cands = append(cands, i)
		}
	}
	return cands
}

// openSwitcher opens the session switcher overlay. No-op when there is no
// other session to switch to.
func (m *tuiModel) openSwitcher() {
	if len(m.switcherCandidates()) == 0 {
		return
	}
	m.switcher = sessionSwitcher{active: true, cursor: 0}
}

// handleSwitcherMouse processes mouse events while the session switcher is
// open: left click on a panel switches to that session, click outside the
// box closes the switcher, and the wheel cycles the highlight.
func (m *tuiModel) handleSwitcherMouse(msg tea.MouseMsg) tea.Cmd {
	cands := m.switcherCandidates()
	if len(cands) == 0 {
		m.switcher.active = false
		return tea.ClearScreen
	}
	sw := &m.switcher
	if msg.Action != tea.MouseActionPress {
		return nil
	}

	switch msg.Button {
	case tea.MouseButtonWheelDown:
		sw.cursor = (sw.cursor + 1) % len(cands)
	case tea.MouseButtonWheelUp:
		sw.cursor = (sw.cursor - 1 + len(cands)) % len(cands)
	case tea.MouseButtonLeft:
		if sw.boxW <= 0 || sw.boxH <= 0 {
			return nil // not rendered yet
		}
		// Click outside the box closes the switcher.
		if msg.X < sw.boxX || msg.X >= sw.boxX+sw.boxW ||
			msg.Y < sw.boxY || msg.Y >= sw.boxY+sw.boxH {
			sw.active = false
			return tea.ClearScreen
		}
		// Hit-test the panel grid.
		lx := msg.X - sw.gridX
		ly := msg.Y - sw.gridY
		if lx < 0 || ly < 0 || sw.cols <= 0 || sw.panelW <= 0 || sw.panelH <= 0 {
			return nil
		}
		c := lx / sw.panelW
		r := ly / sw.panelH
		if c >= sw.cols {
			return nil
		}
		ci := r*sw.cols + c
		if ci >= len(cands) {
			return nil
		}
		sw.cursor = ci
		return m.switchToSession(cands[ci])
	}
	return nil
}

// switchToSession makes the given visible-session index the selected
// (fullscreen) session and closes the switcher.
func (m *tuiModel) switchToSession(visIdx int) tea.Cmd {
	m.selected = visIdx
	m.inputFocus = -1
	m.switcher.active = false
	m.resizeSessionPTYs()
	return tea.ClearScreen
}

// switcherGrid computes the switcher grid shape and panel size for n
// candidate panels at the current terminal size. Shared by rendering and
// directional key navigation so both agree on the layout.
func (m *tuiModel) switcherGrid(n int) (cols, rows, panelW, panelH int) {
	// Floating box is 80% of terminal size (like the floating window).
	fw := m.width * 8 / 10
	fh := m.height * 8 / 10
	if fw < 40 {
		fw = min(40, m.width)
	}
	if fh < 10 {
		fh = min(10, m.height)
	}

	// Inner area: box border (2) + horizontal padding (2), header line (1).
	innerW := fw - 4
	gridH := fh - 3
	if innerW < tuiMinPanelW {
		innerW = tuiMinPanelW
	}
	if gridH < tuiMinPanelH {
		gridH = tuiMinPanelH
	}

	// Prefer full-height panels (10 preview lines), adding columns when the
	// candidates don't fit in one column.
	maxRows := gridH / switcherPanelH
	if maxRows < 1 {
		maxRows = 1
	}
	cols = (n + maxRows - 1) / maxRows
	maxCols := innerW / (2 * tuiMinPanelW)
	if maxCols < 1 {
		maxCols = 1
	}
	if cols > maxCols {
		cols = maxCols
	}
	rows = (n + cols - 1) / cols
	panelW = innerW / cols
	panelH = switcherPanelH
	if rows*panelH > gridH {
		panelH = gridH / rows
	}
	if panelH < tuiMinPanelH {
		panelH = tuiMinPanelH
	}
	return cols, rows, panelW, panelH
}

// switcherMove moves the cursor one step in the given direction on the
// switcher grid. Vertical moves keep the column and wrap top↔bottom within
// it; horizontal moves keep the row and wrap left↔right within it.
func (m *tuiModel) switcherMove(dx, dy int, n int) {
	cols, _, _, _ := m.switcherGrid(n)
	r := m.switcher.cursor / cols
	c := m.switcher.cursor % cols

	if dy != 0 {
		nr := r + dy
		if nr < 0 || nr*cols+c >= n {
			if dy > 0 {
				nr = 0 // past the last row: wrap to the first
			} else {
				nr = (n - 1 - c) / cols // above the first row: wrap to the last row that has this column
			}
		}
		m.switcher.cursor = nr*cols + c
		return
	}

	rowLen := min(cols, n-r*cols) // the last row may be partially filled
	nc := (c + dx + rowLen) % rowLen
	m.switcher.cursor = r*cols + nc
}

// handleSwitcherKey processes input while the session switcher is open.
func (m *tuiModel) handleSwitcherKey(msg tea.KeyMsg) tea.Cmd {
	cands := m.switcherCandidates()
	if len(cands) == 0 {
		m.switcher.active = false
		return tea.ClearScreen
	}
	if m.switcher.cursor >= len(cands) {
		m.switcher.cursor = len(cands) - 1
	}

	switch msg.Type {
	case tea.KeyEscape:
		m.switcher.active = false
		return tea.ClearScreen
	case tea.KeyShiftTab, tea.KeyTab:
		m.switcher.cursor = (m.switcher.cursor + 1) % len(cands)
	case tea.KeyDown:
		m.switcherMove(0, 1, len(cands))
	case tea.KeyUp:
		m.switcherMove(0, -1, len(cands))
	case tea.KeyRight:
		m.switcherMove(1, 0, len(cands))
	case tea.KeyLeft:
		m.switcherMove(-1, 0, len(cands))
	case tea.KeyEnter:
		return m.switchToSession(cands[m.switcher.cursor])
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch r := msg.Runes[0]; {
			case r == 'j':
				m.switcherMove(0, 1, len(cands))
			case r == 'k':
				m.switcherMove(0, -1, len(cands))
			case r == 'l':
				m.switcherMove(1, 0, len(cands))
			case r == 'h':
				m.switcherMove(-1, 0, len(cands))
			case r == 'q':
				m.switcher.active = false
				return tea.ClearScreen
			case r >= '1' && r <= '9':
				idx := int(r-'0') - 1
				if idx < len(m.visibleSessions()) {
					if idx == m.selected {
						m.switcher.active = false
						return tea.ClearScreen
					}
					return m.switchToSession(idx)
				}
			}
		}
	}
	return nil
}

// renderSwitcher renders the session switcher overlay: a centered floating
// box containing live preview panels of every visible session except the
// current fullscreen one.
func (m *tuiModel) renderSwitcher(base string) string {
	cands := m.switcherCandidates()
	if len(cands) == 0 {
		m.switcher.active = false
		return base
	}
	if m.switcher.cursor >= len(cands) {
		m.switcher.cursor = len(cands) - 1
	}
	visible := m.visibleSessions()
	n := len(cands)

	cols, rows, panelW, panelH := m.switcherGrid(n)
	showContent := panelH >= tuiSidePanelContentThreshold

	var rowViews []string
	for r := 0; r < rows; r++ {
		var colViews []string
		for c := 0; c < cols; c++ {
			ci := r*cols + c
			if ci >= n {
				colViews = append(colViews, strings.Repeat(" ", panelW))
				continue
			}
			visIdx := cands[ci]
			sess := visible[visIdx]
			title := fmt.Sprintf("[%d] %s", visIdx+1, sess.Name)
			highlighted := ci == m.switcher.cursor
			if highlighted {
				title = "▶ " + title
			}
			colViews = append(colViews, m.renderPanelWithColor(sess, title, panelW, panelH, highlighted, visIdx, showContent))
		}
		rowViews = append(rowViews, lipgloss.JoinHorizontal(lipgloss.Top, colViews...))
	}
	grid := lipgloss.JoinVertical(lipgloss.Left, rowViews...)

	header := lipgloss.NewStyle().
		Foreground(lipgloss.Color("86")).
		Bold(true).
		Render("⬡ Switch Session") +
		lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Render("  ↑↓←→/hjkl move · Tab next · Enter switch · 1-9 jump · Esc cancel")

	content := lipgloss.JoinVertical(lipgloss.Left, header, grid)

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("86")).
		Padding(0, 1)

	box := boxStyle.Render(content)

	// Record screen geometry for mouse hit-testing. lipgloss.Place centers
	// with the smaller half of the gap on the left/top (gap - round(gap/2)).
	boxW := lipgloss.Width(box)
	boxH := lipgloss.Height(box)
	boxX := max(0, (m.width-boxW)/2)
	boxY := max(0, (m.height-boxH)/2)
	m.switcher.boxX, m.switcher.boxY = boxX, boxY
	m.switcher.boxW, m.switcher.boxH = boxW, boxH
	// Content starts after border (1) + padding (1); the grid sits one
	// header row below the top border.
	m.switcher.gridX = boxX + 2
	m.switcher.gridY = boxY + 2
	m.switcher.cols = cols
	m.switcher.panelW = panelW
	m.switcher.panelH = panelH

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
		box,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceForeground(lipgloss.Color("0")))
}
