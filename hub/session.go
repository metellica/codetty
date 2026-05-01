package hub

import (
	"fmt"
	"io"
	"strings"

	"codetty/protocol"

	"github.com/hinshun/vt10x"
)

// vt10x Glyph.Mode flags (unexported in vt10x package)
const (
	glyphReverse   = 1 << 0
	glyphUnderline = 1 << 1
	glyphBold      = 1 << 2
	glyphItalic    = 1 << 4
)

// ClientSession represents one connected terminal client.
type ClientSession struct {
	ID     string
	Title  string
	User   string
	Cols   uint16
	Rows   uint16
	term   vt10x.Terminal
	writer *ConnWriter
}

type terminalSnapshot struct {
	lines         []string
	cursorX       int
	cursorY       int
	cursorVisible bool
	wrapEnabled   bool
}

// NewClientSession creates a session with a vt10x virtual terminal.
// Pass nil for writer if no WebSocket connection is available (testing).
func NewClientSession(id, title string, cols, rows uint16, writer *ConnWriter) *ClientSession {
	term := vt10x.New(vt10x.WithSize(int(cols), int(rows)), vt10x.WithWriter(io.Discard))
	return &ClientSession{
		ID:     id,
		Title:  title,
		Cols:   cols,
		Rows:   rows,
		term:   term,
		writer: writer,
	}
}

// WriteOutput feeds raw PTY output bytes into the vt10x terminal.
func (s *ClientSession) WriteOutput(data []byte) {
	s.term.Write(data)
}

// Screen returns the current virtual terminal screen content as a string.
// vt10x.String() handles its own locking internally.
func (s *ClientSession) Screen() string {
	return s.term.String()
}

func (s *ClientSession) SnapshotANSI(w, h int) terminalSnapshot {
	s.term.Lock()
	defer s.term.Unlock()
	lines, _, _ := s.renderANSILocked(w, h)
	cursor := s.term.Cursor()
	return terminalSnapshot{
		lines:         lines,
		cursorX:       cursor.X,
		cursorY:       cursor.Y,
		cursorVisible: s.term.CursorVisible(),
		wrapEnabled:   s.term.Mode()&vt10x.ModeWrap != 0,
	}
}

// RenderANSI renders the terminal screen with ANSI colors and cursor.
// Returns exactly h lines, each of visual width w, with ANSI color codes.
// Also returns the cursor position relative to the visible area (-1,-1 if not visible).
func (s *ClientSession) RenderANSI(w, h int) (lines []string, curX, curY int) {
	s.term.Lock()
	defer s.term.Unlock()
	return s.renderANSILocked(w, h)
}

func (s *ClientSession) renderANSILocked(w, h int) (lines []string, curX, curY int) {
	cols, rows := s.term.Size()
	cursor := s.term.Cursor()
	curVisible := s.term.CursorVisible()

	// Find last non-blank row
	// Find first and last non-blank rows
	firstContent := -1
	lastVisibleRow := 0
	for y := 0; y < rows; y++ {
		blank := true
		for x := 0; x < cols; x++ {
			g := s.term.Cell(x, y)
			if glyphHasVisibleContent(g) {
				blank = false
				break
			}
		}
		if !blank {
			if firstContent < 0 {
				firstContent = y
			}
			lastVisibleRow = y
		}
	}
	lastContent := lastVisibleRow
	if lastContent+1 < rows {
		lastContent++
	}

	var startY int
	if rows > h {
		anchorY := cursor.Y
		if firstContent >= 0 && lastVisibleRow > anchorY {
			anchorY = lastVisibleRow
		}
		startY = anchorY - h + 1
		if startY < 0 {
			startY = 0
		}
		maxStart := rows - h
		if startY > maxStart {
			startY = maxStart
		}
	} else if firstContent <= 0 {
		startY = 0
	} else {
		startY = lastContent - h + 1
		if startY < 0 {
			startY = 0
		}
	}
	endY := startY + h
	if endY > rows {
		endY = rows
	}

	// Cursor relative to visible area
	curX, curY = -1, -1
	if curVisible && cursor.Y >= startY && cursor.Y < endY {
		curX = cursor.X
		curY = cursor.Y - startY
	}

	lines = make([]string, h)
	for i := 0; i < h; i++ {
		y := startY + i
		if y >= rows {
			lines[i] = strings.Repeat(" ", w)
			continue
		}
		lines[i] = s.renderLine(y, w, cols, curVisible && y == cursor.Y, cursor.X)
	}
	return lines, curX, curY
}

func glyphHasVisibleContent(g vt10x.Glyph) bool {
	if g.Char != 0 && g.Char != ' ' {
		return true
	}
	if g.BG != vt10x.DefaultBG {
		return true
	}
	return g.Mode&(glyphReverse|glyphUnderline) != 0
}

// renderLine renders one row of the terminal with ANSI colors.
func (s *ClientSession) renderLine(y, w, cols int, hasCursor bool, cursorX int) string {
	var sb strings.Builder
	sb.Grow(w * 4) // rough estimate

	var prevFG, prevBG vt10x.Color = vt10x.DefaultFG, vt10x.DefaultBG
	var prevMode int16
	needReset := false

	for x := 0; x < w; x++ {
		var g vt10x.Glyph
		if x < cols {
			g = s.term.Cell(x, y)
		}
		if g.Char == 0 {
			g.Char = ' '
		}

		isCursor := hasCursor && x == cursorX

		fg, bg, mode := g.FG, g.BG, g.Mode
		if isCursor {
			// Show cursor as reverse video
			mode |= glyphReverse
		}

		if fg != prevFG || bg != prevBG || mode != prevMode || (needReset && (fg != vt10x.DefaultFG || bg != vt10x.DefaultBG || mode != 0)) {
			sb.WriteString(ansiAttrs(fg, bg, mode))
			prevFG, prevBG, prevMode = fg, bg, mode
			needReset = fg != vt10x.DefaultFG || bg != vt10x.DefaultBG || mode != 0
		}

		sb.WriteRune(g.Char)
	}

	if needReset {
		sb.WriteString("\033[0m")
	}
	return sb.String()
}

// ansiAttrs builds an SGR escape sequence for the given attributes.
func ansiAttrs(fg, bg vt10x.Color, mode int16) string {
	var parts []string
	parts = append(parts, "0") // reset first

	if mode&glyphBold != 0 {
		parts = append(parts, "1")
	}
	if mode&glyphItalic != 0 {
		parts = append(parts, "3")
	}
	if mode&glyphUnderline != 0 {
		parts = append(parts, "4")
	}
	if mode&glyphReverse != 0 {
		parts = append(parts, "7")
	}

	if fg != vt10x.DefaultFG {
		parts = append(parts, fgColor(fg))
	}
	if bg != vt10x.DefaultBG {
		parts = append(parts, bgColor(bg))
	}

	return fmt.Sprintf("\033[%sm", strings.Join(parts, ";"))
}

func fgColor(c vt10x.Color) string {
	if c < 8 {
		return fmt.Sprintf("%d", 30+c)
	}
	if c < 16 {
		return fmt.Sprintf("%d", 90+c-8)
	}
	return fmt.Sprintf("38;5;%d", c)
}

func bgColor(c vt10x.Color) string {
	if c < 8 {
		return fmt.Sprintf("%d", 40+c)
	}
	if c < 16 {
		return fmt.Sprintf("%d", 100+c-8)
	}
	return fmt.Sprintf("48;5;%d", c)
}

// SendInput sends keyboard input to the remote client.
func (s *ClientSession) SendInput(data []byte) {
	if s.writer != nil {
		s.writer.Send(protocol.EncodeInput(data))
	}
}

// SendResize sends a resize command to the remote client and updates local state.
func (s *ClientSession) SendResize(cols, rows uint16) {
	s.Cols = cols
	s.Rows = rows
	s.term.Resize(int(cols), int(rows))
	if s.writer != nil {
		msg, err := protocol.EncodeResize(cols, rows)
		if err == nil {
			s.writer.Send(msg)
		}
	}
}

// Close releases resources. Safe to call multiple times.
func (s *ClientSession) Close() {
	if s.writer != nil {
		s.writer.Close()
		s.writer = nil
	}
}
