package main

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/hinshun/vt10x"
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
	Cols      uint16
	Rows      uint16
	Exited    bool
	ExitCode  int
	CreatedAt string

	term vt10x.Terminal
	mu   sync.Mutex
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
}

// RenderANSI renders the terminal screen at the given dimensions.
func (s *tuiSession) RenderANSI(w, h int) (lines []string, curX, curY int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Lock()
	defer s.term.Unlock()
	return renderTerminalANSI(s.term, w, h)
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
		for x := 0; x < cols && visualCol < w; x++ {
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
			if cw == 1 && ch == ' ' && x > 0 {
				prev := term.Cell(x-1, srcRow)
				if prev.Char != 0 && runewidth.RuneWidth(prev.Char) > 1 {
					continue
				}
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
	if fg != vt10x.DefaultFG {
		fmt.Fprintf(&start, "\x1b[38;5;%dm", fg)
		needReset = true
	}
	if bg != vt10x.DefaultBG {
		fmt.Fprintf(&start, "\x1b[48;5;%dm", bg)
		needReset = true
	}
	if g.Mode&glyphReverse != 0 || isCursor {
		start.WriteString("\x1b[7m")
		needReset = true
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
