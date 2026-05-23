package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hinshun/vt10x"
	"github.com/mattn/go-runewidth"
)

// textSelection tracks in-pane text selection state.
type textSelection struct {
	active   bool   // actively selecting (drag started)
	pending  bool   // mouse pressed but not yet dragged
	session  string // which session is being selected in
	// Coordinates in session-local terminal space (0-based col/row)
	startX, startY int
	endX, endY     int
	// Screen coordinates of the press (for forwarding to PTY)
	screenX, screenY int
}

// panelRegion describes where a session's content is rendered on screen.
type panelRegion struct {
	sessionIdx int
	// Screen coordinates of the content area (inside borders/title)
	contentX, contentY int
	contentW, contentH int
}

// handleMouse processes mouse events for selection and panel switching.
func (m *tuiModel) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if m.mode == modeFullscreen {
		return m.handleFullscreenMouse(msg)
	}

	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button == tea.MouseButtonLeft {
			// Ctrl+Click: switch panel focus (no selection)
			if msg.Ctrl {
				if idx := m.hitTest(msg.X, msg.Y); idx >= 0 {
					m.selected = idx
				}
				m.sel = textSelection{}
				return nil
			}

			// Plain click: start pending selection on focused panel only
			// (click does NOT switch panels — use Ctrl+click for that)
			regions := m.panelRegions()
			sessions := m.orderedSessions()
			for _, r := range regions {
				if r.sessionIdx != m.selected {
					continue
				}
				localX := msg.X - r.contentX
				localY := msg.Y - r.contentY
				if localX >= 0 && localX < r.contentW && localY >= 0 && localY < r.contentH {
					sess := sessions[r.sessionIdx]
					m.sel = textSelection{
						pending:  true,
						session:  sess.Name,
						startX:   localX,
						startY:   localY,
						endX:     localX,
						endY:     localY,
						screenX:  msg.X,
						screenY:  msg.Y,
					}
					return nil
				}
			}
			m.sel = textSelection{}
		}
		// Forward scroll wheel to PTY
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			m.forwardMouseToPTY(msg)
		}

	case tea.MouseActionMotion:
		if m.sel.pending {
			// First motion after press: start real selection
			regions := m.panelRegions()
			for _, r := range regions {
				sess := m.orderedSessions()[r.sessionIdx]
				if sess.Name != m.sel.session {
					continue
				}
				localX := msg.X - r.contentX
				localY := msg.Y - r.contentY
				if localX < 0 {
					localX = 0
				}
				if localX >= r.contentW {
					localX = r.contentW - 1
				}
				if localY < 0 {
					localY = 0
				}
				if localY >= r.contentH {
					localY = r.contentH - 1
				}
				// Only promote to active if actually moved
				if localX != m.sel.startX || localY != m.sel.startY {
					m.sel.pending = false
					m.sel.active = true
					m.sel.endX = localX
					m.sel.endY = localY
				}
				return nil
			}
		} else if m.sel.active {
			regions := m.panelRegions()
			for _, r := range regions {
				sess := m.orderedSessions()[r.sessionIdx]
				if sess.Name != m.sel.session {
					continue
				}
				localX := msg.X - r.contentX
				localY := msg.Y - r.contentY
				if localX < 0 {
					localX = 0
				}
				if localX >= r.contentW {
					localX = r.contentW - 1
				}
				if localY < 0 {
					localY = 0
				}
				if localY >= r.contentH {
					localY = r.contentH - 1
				}
				m.sel.endX = localX
				m.sel.endY = localY
				return nil
			}
		}

	case tea.MouseActionRelease:
		if m.sel.active {
			m.sel.active = false
			if m.sel.startX != m.sel.endX || m.sel.startY != m.sel.endY {
				text := m.extractSelectedText()
				if text != "" {
					return m.copyToClipboard(text)
				}
			}
			m.sel = textSelection{}
		} else if m.sel.pending {
			// Click without drag: forward press+release to PTY
			m.sel.pending = false
			m.forwardClickToPTY(m.sel.startX, m.sel.startY)
			m.sel = textSelection{}
		}
	}
	return nil
}

// handleFullscreenMouse handles mouse in fullscreen mode (entire screen is one session).
func (m *tuiModel) handleFullscreenMouse(msg tea.MouseMsg) tea.Cmd {
	// In fullscreen, forward all mouse events directly to PTY
	// except for drag selection
	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button == tea.MouseButtonLeft {
			sess := m.selectedSession()
			if sess == nil {
				return nil
			}
			m.sel = textSelection{
				pending:  true,
				session:  sess.Name,
				startX:   msg.X,
				startY:   msg.Y,
				endX:     msg.X,
				endY:     msg.Y,
				screenX:  msg.X,
				screenY:  msg.Y,
			}
			return nil
		}
		// Forward scroll and other buttons directly
		m.forwardMouseToPTY(msg)
	case tea.MouseActionMotion:
		if m.sel.pending {
			if msg.X != m.sel.startX || msg.Y != m.sel.startY {
				m.sel.pending = false
				m.sel.active = true
				m.sel.endX = msg.X
				m.sel.endY = msg.Y
			}
		} else if m.sel.active {
			x, y := msg.X, msg.Y
			if x < 0 {
				x = 0
			}
			if x >= m.width {
				x = m.width - 1
			}
			if y < 0 {
				y = 0
			}
			if y >= m.height {
				y = m.height - 1
			}
			m.sel.endX = x
			m.sel.endY = y
		} else {
			m.forwardMouseToPTY(msg)
		}
	case tea.MouseActionRelease:
		if m.sel.active {
			m.sel.active = false
			if m.sel.startX != m.sel.endX || m.sel.startY != m.sel.endY {
				text := m.extractSelectedText()
				if text != "" {
					return m.copyToClipboard(text)
				}
			}
			m.sel = textSelection{}
		} else if m.sel.pending {
			// Click without drag: forward to PTY
			m.sel.pending = false
			m.forwardClickToPTY(m.sel.startX, m.sel.startY)
			m.sel = textSelection{}
		} else {
			m.forwardMouseToPTY(msg)
		}
	}
	return nil
}

// forwardMouseToPTY sends a mouse event as SGR escape sequence to the focused session PTY.
func (m *tuiModel) forwardMouseToPTY(msg tea.MouseMsg) {
	sess := m.selectedSession()
	if sess == nil || m.conn == nil {
		return
	}

	var btn int
	switch msg.Button {
	case tea.MouseButtonLeft:
		btn = 0
	case tea.MouseButtonMiddle:
		btn = 1
	case tea.MouseButtonRight:
		btn = 2
	case tea.MouseButtonWheelUp:
		btn = 64
	case tea.MouseButtonWheelDown:
		btn = 65
	default:
		btn = 3 // release
	}

	if msg.Shift {
		btn |= 4
	}
	if msg.Alt {
		btn |= 8
	}
	if msg.Ctrl {
		btn |= 16
	}
	if msg.Action == tea.MouseActionMotion {
		btn |= 32
	}

	x, y := msg.X+1, msg.Y+1 // SGR uses 1-based coordinates
	var suffix byte = 'M'
	if msg.Action == tea.MouseActionRelease {
		suffix = 'm'
	}
	seq := fmt.Sprintf("\x1b[<%d;%d;%d%c", btn, x, y, suffix)
	m.conn.SendInput(sess.Name, []byte(seq))
}

// forwardClickToPTY sends a click (press+release) to PTY at the given local coords.
func (m *tuiModel) forwardClickToPTY(localX, localY int) {
	sess := m.selectedSession()
	if sess == nil || m.conn == nil {
		return
	}
	x, y := localX+1, localY+1 // SGR 1-based
	press := fmt.Sprintf("\x1b[<0;%d;%dM", x, y)
	release := fmt.Sprintf("\x1b[<0;%d;%dm", x, y)
	m.conn.SendInput(sess.Name, []byte(press+release))
}

// panelRegions computes the on-screen content rectangles for all visible sessions.
func (m *tuiModel) panelRegions() []panelRegion {
	sessions := m.orderedSessions()
	n := len(sessions)
	if n == 0 {
		return nil
	}

	statusH := 1
	availH := m.height - statusH

	if m.mode == modeGrid {
		return m.gridPanelRegions(n, availH)
	}
	return m.focusBigPanelRegions(sessions, n, availH)
}

func (m *tuiModel) gridPanelRegions(n, availH int) []panelRegion {
	cols := gridCols(n, m.width)
	rows := (n + cols - 1) / cols
	panelW := m.width / cols
	panelH := availH / rows
	if panelH < tuiMinPanelH {
		panelH = tuiMinPanelH
	}

	// Panel content: border=1 each side, title line=1
	contentW := panelW - 2
	contentH := panelH - 3
	if contentW < 1 {
		contentW = 1
	}
	if contentH < 1 {
		contentH = 1
	}

	regions := make([]panelRegion, 0, n)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			idx := r*cols + c
			if idx >= n {
				break
			}
			regions = append(regions, panelRegion{
				sessionIdx: idx,
				contentX:   c*panelW + 1,
				contentY:   r*panelH + 2, // border top + title
				contentW:   contentW,
				contentH:   contentH,
			})
		}
	}
	return regions
}

func (m *tuiModel) focusBigPanelRegions(sessions []*tuiSession, n, availH int) []panelRegion {
	if n == 1 {
		contentW := m.width - 2
		contentH := availH - 3
		if contentW < 1 {
			contentW = 1
		}
		if contentH < 1 {
			contentH = 1
		}
		return []panelRegion{{
			sessionIdx: m.selected,
			contentX:   1,
			contentY:   2,
			contentW:   contentW,
			contentH:   contentH,
		}}
	}

	if m.width >= 120 {
		return m.focusBigWidePanelRegions(n, availH)
	}
	return m.focusBigNarrowPanelRegions(n, availH)
}

func (m *tuiModel) focusBigWidePanelRegions(n, availH int) []panelRegion {
	mainW := m.width * 7 / 10
	sideW := m.width - mainW
	if sideW < tuiMinPanelW {
		sideW = tuiMinPanelW
		mainW = m.width - sideW
	}

	mainContentW := mainW - 2
	mainContentH := availH - 3
	if mainContentW < 1 {
		mainContentW = 1
	}
	if mainContentH < 1 {
		mainContentH = 1
	}

	regions := []panelRegion{{
		sessionIdx: m.selected,
		contentX:   1,
		contentY:   2,
		contentW:   mainContentW,
		contentH:   mainContentH,
	}}

	secondaryCount := n - 1
	if secondaryCount == 0 {
		return regions
	}
	slotH := availH / secondaryCount
	if slotH < tuiMinPanelH {
		slotH = tuiMinPanelH
	}
	visible := availH / slotH
	if visible > secondaryCount {
		visible = secondaryCount
	}

	sideContentW := sideW - 2
	if sideContentW < 1 {
		sideContentW = 1
	}

	idx := 0
	for i := 0; i < n; i++ {
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
		contentH := h - 3
		if contentH < 1 {
			contentH = 1
		}
		regions = append(regions, panelRegion{
			sessionIdx: i,
			contentX:   mainW + 1,
			contentY:   idx*slotH + 2,
			contentW:   sideContentW,
			contentH:   contentH,
		})
		idx++
	}
	return regions
}

func (m *tuiModel) focusBigNarrowPanelRegions(n, availH int) []panelRegion {
	mainH := availH * 7 / 10
	sideH := availH - mainH

	mainContentW := m.width - 2
	mainContentH := mainH - 3
	if mainContentW < 1 {
		mainContentW = 1
	}
	if mainContentH < 1 {
		mainContentH = 1
	}

	regions := []panelRegion{{
		sessionIdx: m.selected,
		contentX:   1,
		contentY:   2,
		contentW:   mainContentW,
		contentH:   mainContentH,
	}}

	sideContentW := m.width - 2
	sideContentH := sideH - 3
	if sideContentW < 1 {
		sideContentW = 1
	}
	if sideContentH < 1 {
		sideContentH = 1
	}

	for i := 0; i < n; i++ {
		if i != m.selected {
			regions = append(regions, panelRegion{
				sessionIdx: i,
				contentX:   1,
				contentY:   mainH + 2,
				contentW:   sideContentW,
				contentH:   sideContentH,
			})
			break
		}
	}
	return regions
}

// extractSelectedText reads the selected text from the vt10x terminal buffer.
func (m *tuiModel) extractSelectedText() string {
	sess := m.sessionByName(m.sel.session)
	if sess == nil {
		return ""
	}

	startY, startX := m.sel.startY, m.sel.startX
	endY, endX := m.sel.endY, m.sel.endX

	// Normalize: start should be before end
	if startY > endY || (startY == endY && startX > endX) {
		startY, endY = endY, startY
		startX, endX = endX, startX
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.term.Lock()
	defer sess.term.Unlock()

	cols, rows := sess.term.Size()
	_ = rows

	var lines []string
	for row := startY; row <= endY; row++ {
		if row >= rows {
			break
		}
		sx := 0
		ex := cols - 1
		if row == startY {
			sx = startX
		}
		if row == endY {
			ex = endX
		}
		if sx >= cols {
			sx = cols - 1
		}
		if ex >= cols {
			ex = cols - 1
		}

		var sb strings.Builder
		for x := sx; x <= ex; x++ {
			g := sess.term.Cell(x, row)
			ch := g.Char
			if ch == 0 {
				ch = ' '
			}
			sb.WriteRune(ch)
		}
		line := strings.TrimRight(sb.String(), " ")
		lines = append(lines, line)
	}

	// Trim trailing empty lines
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// copyToClipboard uses OSC 52 to copy text to the system clipboard.
func (m *tuiModel) copyToClipboard(text string) tea.Cmd {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	osc52 := fmt.Sprintf("\x1b]52;c;%s\x1b\\", encoded)
	return func() tea.Msg {
		os.Stdout.Write([]byte(osc52))
		return nil
	}
}

// selectionCovers checks if a cell at (col, row) in session-local coords is selected.
func (m *tuiModel) selectionCovers(sessionName string, col, row int) bool {
	if m.sel.session != sessionName {
		return false
	}
	if !m.sel.active && (m.sel.startX == m.sel.endX && m.sel.startY == m.sel.endY) {
		return false
	}

	startY, startX := m.sel.startY, m.sel.startX
	endY, endX := m.sel.endY, m.sel.endX
	if startY > endY || (startY == endY && startX > endX) {
		startY, endY = endY, startY
		startX, endX = endX, startX
	}

	if row < startY || row > endY {
		return false
	}
	if row == startY && row == endY {
		return col >= startX && col <= endX
	}
	if row == startY {
		return col >= startX
	}
	if row == endY {
		return col <= endX
	}
	return true
}

// RenderANSIWithSelection renders terminal content with selection highlighting.
func (s *tuiSession) RenderANSIWithSelection(w, h int, selFn func(col, row int) bool) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Lock()
	defer s.term.Unlock()
	return renderTerminalANSIWithSelection(s.term, w, h, selFn)
}

func renderTerminalANSIWithSelection(term vt10x.Terminal, w, h int, selFn func(col, row int) bool) []string {
	cols, rows := term.Size()
	cursor := term.Cursor()
	curVisible := term.CursorVisible()

	lastVisibleRow := 0
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			g := term.Cell(x, y)
			if g.Char != 0 && g.Char != ' ' {
				lastVisibleRow = y
				break
			}
		}
	}

	startRow := 0
	contentRows := lastVisibleRow + 1
	if contentRows > h {
		startRow = contentRows - h
	}

	lines := make([]string, h)
	for displayRow := 0; displayRow < h; displayRow++ {
		srcRow := startRow + displayRow
		if srcRow >= rows {
			lines[displayRow] = strings.Repeat(" ", w)
			continue
		}

		var sb strings.Builder
		visualCol := 0
		skipNext := false
		for x := 0; x < cols && visualCol < w; x++ {
			if skipNext {
				skipNext = false
				continue
			}
			g := term.Cell(x, srcRow)
			ch := g.Char
			if ch == 0 {
				ch = ' '
			}
			cw := runewidth.RuneWidth(ch)
			if cw == 0 {
				ch = ' '
				cw = 1
			}
			if visualCol+cw > w {
				break
			}

			isCursor := curVisible && x == cursor.X && srcRow == cursor.Y
			isSelected := selFn != nil && selFn(visualCol, displayRow)

			if isSelected {
				sb.WriteString("\x1b[7m")
				sb.WriteRune(ch)
				sb.WriteString("\x1b[0m")
			} else {
				ansiStart, ansiEnd := glyphANSI(g, isCursor)
				sb.WriteString(ansiStart)
				sb.WriteRune(ch)
				sb.WriteString(ansiEnd)
			}
			visualCol += cw
			if cw > 1 {
				skipNext = true
			}
		}
		for visualCol < w {
			if selFn != nil && selFn(visualCol, displayRow) {
				sb.WriteString("\x1b[7m \x1b[0m")
			} else {
				sb.WriteByte(' ')
			}
			visualCol++
		}
		lines[displayRow] = sb.String()
	}
	return lines
}
