package main

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/hinshun/vt10x"
)

const (
	snapshotGlyphReverse   = 1 << 0
	snapshotGlyphUnderline = 1 << 1
	snapshotGlyphBold      = 1 << 2
	snapshotGlyphItalic    = 1 << 4
)

type ScreenMirror struct {
	mu      sync.Mutex
	term    vt10x.Terminal
	written bool
}

func NewScreenMirror(cols, rows uint16) *ScreenMirror {
	return &ScreenMirror{
		term: vt10x.New(vt10x.WithSize(int(cols), int(rows)), vt10x.WithWriter(io.Discard)),
	}
}

func (s *ScreenMirror) Write(data []byte) {
	if len(data) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.term.Write(data)
	s.written = true
}

func (s *ScreenMirror) Resize(cols, rows uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.term.Resize(int(cols), int(rows))
}

func (s *ScreenMirror) Size() (uint16, uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.term.Lock()
	defer s.term.Unlock()
	cols, rows := s.term.Size()
	return uint16(cols), uint16(rows)
}

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
	b.WriteString(snapshotModeSequences(mode))
	b.WriteString("\033[2J")

	var prevFG, prevBG vt10x.Color = vt10x.DefaultFG, vt10x.DefaultBG
	var prevMode int16
	needReset := false

	for y := 0; y < rows; y++ {
		fmt.Fprintf(&b, "\033[%d;1H", y+1)
		for x := 0; x < cols; x++ {
			g := s.term.Cell(x, y)
			if g.Char == 0 {
				g.Char = ' '
			}

			if g.FG != prevFG || g.BG != prevBG || g.Mode != prevMode || (needReset && (g.FG != vt10x.DefaultFG || g.BG != vt10x.DefaultBG || g.Mode != 0)) {
				b.WriteString(snapshotANSIAttrs(g.FG, g.BG, g.Mode))
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

func snapshotModeSequences(mode vt10x.ModeFlag) string {
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

func snapshotReplay(outputCh <-chan []byte, screen *ScreenMirror, replayMu *sync.Mutex) []byte {
	replayMu.Lock()
	defer replayMu.Unlock()

	drainOutput(outputCh)
	return screen.Snapshot()
}

func drainOutput(outputCh <-chan []byte) {
	for {
		select {
		case <-outputCh:
		default:
			return
		}
	}
}

func snapshotANSIAttrs(fg, bg vt10x.Color, mode int16) string {
	parts := []string{"0"}

	if mode&snapshotGlyphBold != 0 {
		parts = append(parts, "1")
	}
	if mode&snapshotGlyphItalic != 0 {
		parts = append(parts, "3")
	}
	if mode&snapshotGlyphUnderline != 0 {
		parts = append(parts, "4")
	}
	if mode&snapshotGlyphReverse != 0 {
		parts = append(parts, "7")
	}

	if fg != vt10x.DefaultFG {
		parts = append(parts, snapshotFGColor(fg))
	}
	if bg != vt10x.DefaultBG {
		parts = append(parts, snapshotBGColor(bg))
	}

	return fmt.Sprintf("\033[%sm", strings.Join(parts, ";"))
}

func snapshotFGColor(c vt10x.Color) string {
	if c < 8 {
		return fmt.Sprintf("%d", 30+c)
	}
	if c < 16 {
		return fmt.Sprintf("%d", 90+c-8)
	}
	return fmt.Sprintf("38;5;%d", c)
}

func snapshotBGColor(c vt10x.Color) string {
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
