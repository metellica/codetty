package main

import (
	"fmt"
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
)

// newScrollbackSession returns a small session whose content has scrolled
// past the screen into the scrollback buffer.
func newScrollbackSession(t *testing.T) *tuiSession {
	t.Helper()
	sess := newTUISession("alpha", "bash", 20, 4)
	for i := 0; i < 20; i++ {
		sess.WriteOutput([]byte(fmt.Sprintf("line-%02d\r\n", i)))
	}
	if !sess.HasScrollback() {
		t.Fatal("expected content in scrollback")
	}
	return sess
}

func stripLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimRight(xansi.Strip(l), " ")
	}
	return out
}

// While scrolled back, rendering with an active selection must show the
// same scrollback view as the plain render — not snap to the live screen
// (which made the display jump back and forth during a drag).
func TestRenderANSIWithSelection_ScrolledBackKeepsView(t *testing.T) {
	sess := newScrollbackSession(t)
	if !sess.ScrollUp(5) {
		t.Fatal("ScrollUp did not move")
	}

	plain, _, _ := sess.RenderANSI(20, 4)
	selFn := func(col, row int) bool { return row == 1 }
	withSel := sess.RenderANSIWithSelection(20, 4, selFn)

	got := stripLines(withSel)
	want := stripLines(plain)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %q, want scrollback view row %q", i, got[i], want[i])
		}
	}
	if !strings.Contains(withSel[1], "\x1b[7m") {
		t.Fatalf("selected row not highlighted: %q", withSel[1])
	}
}

// Extracted text must come from the rows shown on screen, including rows
// that live in the scrollback buffer while scrolled back.
func TestExtractSelectedText_ScrolledBack(t *testing.T) {
	sess := newScrollbackSession(t)
	if !sess.ScrollUp(5) {
		t.Fatal("ScrollUp did not move")
	}
	view, _, _ := sess.RenderANSI(20, 4)

	m := newTUIModel(nil, "", nil)
	m.sessions = []*tuiSession{sess}
	m.sel = textSelection{
		active:  true,
		session: "alpha",
		startY:  1, startX: 0,
		endY: 1, endX: 19,
	}

	got := m.extractSelectedText()
	want := stripLines(view)[1]
	if want == "" || !strings.HasPrefix(want, "line-") {
		t.Fatalf("test setup: unexpected view row %q", want)
	}
	if got != want {
		t.Fatalf("extracted %q, want on-screen row %q", got, want)
	}
}
