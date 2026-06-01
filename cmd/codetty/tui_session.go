package main

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"codetty/vt10x"

	"github.com/mattn/go-runewidth"
)

// vt10x Glyph.Mode flags (unexported in vt10x package)
const (
	glyphReverse   = 1 << 0
	glyphUnderline = 1 << 1
	glyphBold      = 1 << 2
	glyphItalic    = 1 << 4
)

// tuiSession represents one daemon session's display state in the TUI.
type tuiSession struct {
	Name      string
	Command   string
	Type      string // "native" or "tmux"
	Cols      uint16
	Rows      uint16
	Exited    bool
	ExitCode  int
	CreatedAt string
	Temporary bool // created via Ctrl+A m, killed on floating close

	term vt10x.Terminal
	mu   sync.Mutex

	// Line cache for incremental rendering
	cachedLines    []string
	cachedCursorX  int
	cachedCursorY  int
	cacheW, cacheH int
	outputReceived bool
}

func newTUISession(name, command string, cols, rows uint16) *tuiSession {
	term := vt10x.New(vt10x.WithSize(int(cols), int(rows)), vt10x.WithWriter(io.Discard))
	return &tuiSession{
		Name:    name,
		Command: command,
		Cols:    cols,
		Rows:    rows,
		term:    term,
	}
}

// WriteOutput feeds PTY output into the vt10x terminal.
func (s *tuiSession) WriteOutput(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Write(data)
	s.outputReceived = true
}

// RenderANSI renders the terminal screen at the given dimensions.
func (s *tuiSession) RenderANSI(w, h int) (lines []string, curX, curY int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Lock()
	defer s.term.Unlock()

	// If dimensions changed, invalidate cache
	if s.cacheW != w || s.cacheH != h || len(s.cachedLines) != h {
		s.cachedLines = nil
		s.cacheW = w
		s.cacheH = h
	}

	// If we have a cache and no output was received, return cached version
	if s.cachedLines != nil && !s.outputReceived {
		lines = make([]string, len(s.cachedLines))
		copy(lines, s.cachedLines)
		return lines, s.cachedCursorX, s.cachedCursorY
	}

	// Full render and cache
	s.outputReceived = false
	lines, curX, curY = renderTerminalANSI(s.term, w, h)
	s.cachedLines = make([]string, len(lines))
	copy(s.cachedLines, lines)
	s.cachedCursorX = curX
	s.cachedCursorY = curY
	return lines, curX, curY
}

// SnapshotANSI returns a snapshot for panel rendering.
func (s *tuiSession) SnapshotANSI(w, h int) terminalSnap {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Lock()
	defer s.term.Unlock()
	lines, curX, curY := renderTerminalANSI(s.term, w, h)
	return terminalSnap{
		lines:         lines,
		cursorX:       curX,
		cursorY:       curY,
		cursorVisible: s.term.CursorVisible(),
	}
}

type terminalSnap struct {
	lines         []string
	cursorX       int
	cursorY       int
	cursorVisible bool
}

// Resize updates the vt10x terminal size.
func (s *tuiSession) Resize(cols, rows uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Cols = cols
	s.Rows = rows
	s.term.Resize(int(cols), int(rows))
	s.cachedLines = nil
}

// MouseEnabled returns true if the session's terminal has mouse reporting on.
func (s *tuiSession) MouseEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Lock()
	defer s.term.Unlock()
	return s.term.Mode()&vt10x.ModeMouseMask != 0
}

func renderTerminalANSI(term vt10x.Terminal, w, h int) ([]string, int, int) {
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
	curX, curY := -1, -1

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
			if isCursor {
				curX = visualCol
				curY = displayRow
			}

			ansiStart, ansiEnd := glyphANSI(g, isCursor)
			sb.WriteString(ansiStart)
			sb.WriteRune(ch)
			sb.WriteString(ansiEnd)
			visualCol += cw
			if cw > 1 {
				skipNext = true
			}
		}
		for visualCol < w {
			sb.WriteByte(' ')
			visualCol++
		}
		lines[displayRow] = sb.String()
	}
	return lines, curX, curY
}

func glyphANSI(g vt10x.Glyph, isCursor bool) (string, string) {
	var start strings.Builder
	needReset := false

	fg := g.FG
	bg := g.BG

	if isCursor {
		// Force visible cursor: white background with black text.
		start.WriteString("\x1b[30;107m") // black fg, bright white bg
		needReset = true
	} else {
		if g.Mode&glyphReverse != 0 {
			// Reverse video: swap fg↔bg and emit directly as normal colors.
			// This avoids issues where \x1b[7m with explicit color 0 (black)
			// would produce invisible results.
			visualFG := bg // bg becomes visible foreground
			visualBG := fg // fg becomes visible background
			if visualFG != vt10x.DefaultBG && visualFG != vt10x.DefaultFG {
				start.WriteString(colorToANSIFG(vt10x.Color(uint32(visualFG))))
			}
			if visualBG != vt10x.DefaultFG && visualBG != vt10x.DefaultBG {
				start.WriteString(colorToANSIBG(vt10x.Color(uint32(visualBG))))
			} else {
				// fg was default → visual bg should be default fg color (white)
				start.WriteString("\x1b[7m")
			}
			needReset = true
		} else {
			if fg != vt10x.DefaultFG {
				start.WriteString(colorToANSIFG(fg))
				needReset = true
			}
			if bg != vt10x.DefaultBG {
				start.WriteString(colorToANSIBG(bg))
				needReset = true
			}
		}
	}
	if g.Mode&glyphBold != 0 {
		start.WriteString("\x1b[1m")
		needReset = true
	}
	if g.Mode&glyphUnderline != 0 {
		start.WriteString("\x1b[4m")
		needReset = true
	}
	if g.Mode&glyphItalic != 0 {
		start.WriteString("\x1b[3m")
		needReset = true
	}

	if needReset {
		return start.String(), "\x1b[0m"
	}
	return "", ""
}

// colorToANSIFG converts a vt10x Color to an ANSI foreground escape sequence.
func colorToANSIFG(c vt10x.Color) string {
	v := uint32(c)
	if v < 16 {
		// Standard ANSI colors: use compact SGR codes
		if v < 8 {
			return fmt.Sprintf("\x1b[%dm", 30+v)
		}
		return fmt.Sprintf("\x1b[%dm", 90+v-8)
	}
	if v < 256 {
		return fmt.Sprintf("\x1b[38;5;%dm", v)
	}
	// Truecolor: r<<16 | g<<8 | b
	r := (v >> 16) & 0xFF
	g := (v >> 8) & 0xFF
	b := v & 0xFF
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
}

// colorToANSIBG converts a vt10x Color to an ANSI background escape sequence.
func colorToANSIBG(c vt10x.Color) string {
	v := uint32(c)
	if v < 16 {
		if v < 8 {
			return fmt.Sprintf("\x1b[%dm", 40+v)
		}
		return fmt.Sprintf("\x1b[%dm", 100+v-8)
	}
	if v < 256 {
		return fmt.Sprintf("\x1b[48;5;%dm", v)
	}
	r := (v >> 16) & 0xFF
	g := (v >> 8) & 0xFF
	b := v & 0xFF
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}
