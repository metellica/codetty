package main

import (
	"io"
	"strings"
	"testing"

	"codetty/vt10x"

	"github.com/charmbracelet/x/ansi"
)

// copilot's line editor inserts a wide char before existing wide text with the
// sequence "中" BS "你好". The lone backspace lands the cursor in the middle of
// the wide "中", so a standard terminal (xterm/st/terminal-multiplexer) erases the orphaned
// half. codetty must match that faithful behaviour instead of leaving a
// half-overwritten wide char that renders as garbage.
func TestCopilotWideInsertMatchesStandardTerminal(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(40, 3), vt10x.WithWriter(io.Discard))

	term.Write([]byte("\r\u276f "))            // "❯ "
	term.Write([]byte("\u4e2d\b\u4f60\u597d")) // 中 BS 你 好

	lines, _, _ := renderTerminalANSI(term, 40, 3)
	got := strings.TrimRight(ansi.Strip(lines[0]), " ")
	// Matches a terminal multiplexer / xterm: the partially-overwritten 中 is erased, leaving a
	// blank in its place. No orphaned continuation cell, no dropped glyph.
	want := "\u276f  \u4f60\u597d" // "❯  你好"
	if got != want {
		t.Fatalf("rendered line = %q, want %q", got, want)
	}
}

// When the cursor rests on a wide character's NUL continuation cell, the
// renderer skips that cell — but it must still draw the cursor on the owning
// wide glyph instead of dropping it (which made the input caret vanish after
// inserting CJK text at the start of a line).
func TestCursorOnWideContinuationCellIsRendered(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(40, 3), vt10x.WithWriter(io.Discard))
	// "中" occupies columns 0 (lead) and 1 (NUL continuation).
	term.Write([]byte("\u4e2d"))
	// Park the cursor on the continuation cell (column 1).
	term.Write([]byte("\b")) // cursor 2 -> 1

	if cur := term.Cursor(); cur.X != 1 {
		t.Fatalf("setup: cursor.X = %d, want 1 (continuation cell)", cur.X)
	}

	lines, curX, curY := renderTerminalANSI(term, 40, 3)
	if curX != 0 || curY != 0 {
		t.Fatalf("cursor coords = (%d,%d), want (0,0) on the wide glyph", curX, curY)
	}
	// The cursor highlight (white block) must be present on the line.
	if !strings.Contains(lines[0], "\x1b[30;107m") {
		t.Fatalf("cursor highlight not rendered: %q", lines[0])
	}
}
