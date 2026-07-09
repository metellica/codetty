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

	// Dimmed line cache for side panel rendering
	dimLines  []string
	dimCacheW int
	dimCacheH int
	dimStale  bool
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
	s.dimStale = true
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
	s.dimLines = nil
}

// MouseEnabled returns true if the session's terminal has mouse reporting on.
func (s *tuiSession) MouseEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Lock()
	defer s.term.Unlock()
	return s.term.Mode()&vt10x.ModeMouseMask != 0
}

// RenderDimmed renders terminal content in uniform dim gray, skipping per-cell color processing.
// The result is cached until new output arrives or the size changes, since
// side panels are re-rendered on every View() call.
func (s *tuiSession) RenderDimmed(w, h int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.dimCacheW != w || s.dimCacheH != h {
		s.dimLines = nil
		s.dimCacheW = w
		s.dimCacheH = h
	}
	if s.dimLines != nil && !s.dimStale {
		return s.dimLines
	}

	s.term.Lock()
	lines := renderTerminalDimmed(s.term, w, h)
	s.term.Unlock()
	s.dimLines = lines
	s.dimStale = false
	return lines
}

func renderTerminalDimmed(term vt10x.Terminal, w, h int) []string {
	cols, rows := term.Size()

	lastVisibleRow := findLastVisibleRow(term, cols, rows)
	startRow := 0
	contentRows := lastVisibleRow + 1
	if contentRows > h {
		startRow = contentRows - h
	}

	lines := make([]string, h)
	dimStart := "\x1b[38;5;242m"
	dimEnd := "\x1b[0m"

	var sb strings.Builder
	sb.Grow(w + 20)

	for displayRow := 0; displayRow < h; displayRow++ {
		srcRow := startRow + displayRow
		if srcRow >= rows {
			lines[displayRow] = strings.Repeat(" ", w)
			continue
		}

		sb.Reset()
		sb.WriteString(dimStart)
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
			sb.WriteRune(ch)
			visualCol += cw
			// Only skip the following cell if it is an actual NUL continuation
			// cell. An application may overwrite a continuation cell with a new
			// (possibly wide) character, leaving two leads adjacent; in that
			// case the next cell holds a real glyph that must still be rendered.
			if cw > 1 && x+1 < cols && term.Cell(x+1, srcRow).Char == 0 {
				skipNext = true
			}
		}
		for visualCol < w {
			sb.WriteByte(' ')
			visualCol++
		}
		sb.WriteString(dimEnd)
		lines[displayRow] = sb.String()
	}
	return lines
}

func findLastVisibleRow(term vt10x.Terminal, cols, rows int) int {
	for y := rows - 1; y > 0; y-- {
		for x := 0; x < cols; x++ {
			g := term.Cell(x, y)
			if g.Char != 0 && g.Char != ' ' {
				return y
			}
		}
	}
	return 0
}

func renderTerminalANSI(term vt10x.Terminal, w, h int) ([]string, int, int) {
	cols, rows := term.Size()
	cursor := term.Cursor()
	curVisible := term.CursorVisible()

	lastVisibleRow := findLastVisibleRow(term, cols, rows)

	startRow := 0
	contentRows := lastVisibleRow + 1
	if contentRows > h {
		startRow = contentRows - h
	}

	lines := make([]string, h)
	curX, curY := -1, -1

	var sb strings.Builder
	sb.Grow(w * 4)

	for displayRow := 0; displayRow < h; displayRow++ {
		srcRow := startRow + displayRow
		if srcRow >= rows {
			lines[displayRow] = strings.Repeat(" ", w)
			continue
		}

		sb.Reset()
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

			// A wide glyph owns the following NUL continuation cell. Detect that
			// up front so we can both skip the continuation cell and treat the
			// cursor as resting on the wide glyph when it sits on that cell.
			nextIsCont := cw > 1 && x+1 < cols && term.Cell(x+1, srcRow).Char == 0

			isCursor := curVisible && srcRow == cursor.Y &&
				(x == cursor.X || (nextIsCont && cursor.X == x+1))
			if isCursor {
				curX = visualCol
				curY = displayRow
			}

			ansiStart, ansiEnd := glyphANSI(g, isCursor)
			sb.WriteString(ansiStart)
			sb.WriteRune(ch)
			sb.WriteString(ansiEnd)
			visualCol += cw
			if nextIsCont {
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

var (
	ansiFG [256]string
	ansiBG [256]string
)

func init() {
	for i := 0; i < 16; i++ {
		if i < 8 {
			ansiFG[i] = fmt.Sprintf("\x1b[%dm", 30+i)
			ansiBG[i] = fmt.Sprintf("\x1b[%dm", 40+i)
		} else {
			ansiFG[i] = fmt.Sprintf("\x1b[%dm", 90+i-8)
			ansiBG[i] = fmt.Sprintf("\x1b[%dm", 100+i-8)
		}
	}
	for i := 16; i < 256; i++ {
		ansiFG[i] = fmt.Sprintf("\x1b[38;5;%dm", i)
		ansiBG[i] = fmt.Sprintf("\x1b[48;5;%dm", i)
	}
}

func colorToANSIFG(c vt10x.Color) string {
	v := uint32(c)
	if v < 256 {
		return ansiFG[v]
	}
	r := (v >> 16) & 0xFF
	g := (v >> 8) & 0xFF
	b := v & 0xFF
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
}

func colorToANSIBG(c vt10x.Color) string {
	v := uint32(c)
	if v < 256 {
		return ansiBG[v]
	}
	r := (v >> 16) & 0xFF
	g := (v >> 8) & 0xFF
	b := v & 0xFF
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}
