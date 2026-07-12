package terminal

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"codetty/vt10x"
)

const (
	glyphReverse   = 1 << 0
	glyphUnderline = 1 << 1
	glyphBold      = 1 << 2
	glyphItalic    = 1 << 4
)

// ScreenMirror maintains a vt10x terminal model for screen replay/snapshot.
type ScreenMirror struct {
	mu      sync.Mutex
	term    vt10x.Terminal
	written bool
}

// NewScreenMirror creates a new ScreenMirror with the given dimensions.
func NewScreenMirror(cols, rows uint16) *ScreenMirror {
	return &ScreenMirror{
		term: vt10x.New(vt10x.WithSize(int(cols), int(rows)), vt10x.WithWriter(io.Discard)),
	}
}

// Write feeds terminal data into the screen model.
func (s *ScreenMirror) Write(data []byte) {
	if len(data) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.term.Write(data)
	s.written = true
}

// Resize changes the terminal dimensions.
func (s *ScreenMirror) Resize(cols, rows uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.term.Resize(int(cols), int(rows))
}

// Size returns the current terminal dimensions.
func (s *ScreenMirror) Size() (uint16, uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.term.Lock()
	defer s.term.Unlock()
	cols, rows := s.term.Size()
	return uint16(cols), uint16(rows)
}

// TailText returns the bottom n rows of the screen as plain text (lines
// joined by newlines, trailing spaces trimmed). n <= 0 returns the whole
// screen. Wide-character continuation cells (NUL) are skipped.
func (s *ScreenMirror) TailText(n int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.written {
		return ""
	}

	s.term.Lock()
	defer s.term.Unlock()

	cols, rows := s.term.Size()
	start := 0
	if n > 0 && n < rows {
		start = rows - n
	}

	var lines []string
	var line strings.Builder
	for y := start; y < rows; y++ {
		line.Reset()
		line.Grow(cols)
		for x := 0; x < cols; x++ {
			g := s.term.Cell(x, y)
			if g.Char == 0 {
				continue // wide-char continuation cell
			}
			line.WriteRune(g.Char)
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
	}
	return strings.Join(lines, "\n")
}

// Snapshot returns an ANSI byte sequence that reconstructs the current screen.
// Returns nil if nothing has been written yet.
func (s *ScreenMirror) Snapshot() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.written {
		return nil
	}

	s.term.Lock()
	defer s.term.Unlock()

	cols, rows := s.term.Size()
	cursor := s.term.Cursor()
	cursorVisible := s.term.CursorVisible()
	mode := s.term.Mode()

	var b strings.Builder
	b.Grow(rows * (cols + 24))
	b.WriteString("\033[0m")
	b.WriteString(modeSequences(mode))
	b.WriteString("\033[2J")

	var prevFG, prevBG vt10x.Color = vt10x.DefaultFG, vt10x.DefaultBG
	var prevMode int16
	needReset := false

	for y := 0; y < rows; y++ {
		fmt.Fprintf(&b, "\033[%d;1H", y+1)
		for x := 0; x < cols; x++ {
			g := s.term.Cell(x, y)
			if g.Char == 0 {
				continue // skip wide-char continuation cell
			}

			if g.FG != prevFG || g.BG != prevBG || g.Mode != prevMode || (needReset && (g.FG != vt10x.DefaultFG || g.BG != vt10x.DefaultBG || g.Mode != 0)) {
				b.WriteString(ansiAttrs(g.FG, g.BG, g.Mode))
				prevFG, prevBG, prevMode = g.FG, g.BG, g.Mode
				needReset = g.FG != vt10x.DefaultFG || g.BG != vt10x.DefaultBG || g.Mode != 0
			}

			b.WriteRune(g.Char)
		}
	}

	if needReset {
		b.WriteString("\033[0m")
	}

	cursorX := clampInt(cursor.X+1, 1, cols)
	cursorY := clampInt(cursor.Y+1, 1, rows)
	fmt.Fprintf(&b, "\033[%d;%dH", cursorY, cursorX)
	if cursorVisible {
		b.WriteString("\033[?25h")
	} else {
		b.WriteString("\033[?25l")
	}

	return []byte(b.String())
}

func modeSequences(mode vt10x.ModeFlag) string {
	var b strings.Builder

	if mode&vt10x.ModeAltScreen != 0 {
		b.WriteString("\033[?1049h")
	}
	if mode&vt10x.ModeWrap != 0 {
		b.WriteString("\033[?7h")
	} else {
		b.WriteString("\033[?7l")
	}
	if mode&vt10x.ModeAppCursor != 0 {
		b.WriteString("\033[?1h")
	}
	if mode&vt10x.ModeMouseX10 != 0 {
		b.WriteString("\033[?9h")
	}
	if mode&vt10x.ModeMouseButton != 0 {
		b.WriteString("\033[?1000h")
	}
	if mode&vt10x.ModeMouseMotion != 0 {
		b.WriteString("\033[?1002h")
	}
	if mode&vt10x.ModeMouseMany != 0 {
		b.WriteString("\033[?1003h")
	}
	if mode&vt10x.ModeMouseSgr != 0 {
		b.WriteString("\033[?1006h")
	}
	if mode&vt10x.ModeFocus != 0 {
		b.WriteString("\033[?1004h")
	}
	if mode&vt10x.Mode8bit != 0 {
		b.WriteString("\033[?1034h")
	}
	if mode&vt10x.ModeReverse != 0 {
		b.WriteString("\033[?5h")
	}
	if mode&vt10x.ModeInsert != 0 {
		b.WriteString("\033[4h")
	}
	if mode&vt10x.ModeCRLF != 0 {
		b.WriteString("\033[20h")
	}

	return b.String()
}

func ansiAttrs(fg, bg vt10x.Color, mode int16) string {
	parts := []string{"0"}

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

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
