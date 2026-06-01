package main

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"codetty/vt10x"
)

func TestFocusBigView_RendersPrimaryPanelAndStatus(t *testing.T) {
	m := newTUIModel(nil, "", nil)
	m.width = 120
	m.height = 20
	m.selected = 0

	alpha := newTUISession("alpha", "bash", 80, 24)
	alpha.WriteOutput([]byte("hello alpha"))
	beta := newTUISession("beta", "sh", 80, 24)
	beta.WriteOutput([]byte("hello beta"))

	m.sessions = []*tuiSession{alpha, beta}
	m.order = []string{"alpha", "beta"}

	view := xansi.Strip(m.focusBigView())
	for _, want := range []string{"[1] alpha", "[2] beta", "hello alpha", "Ctrl+A prefix", "[alpha] 1/2"} {
		if !strings.Contains(view, want) {
			t.Fatalf("focusBigView() missing %q in:\n%s", want, view)
		}
	}
}

func TestGridView_RendersPanelsAndFocusedMarker(t *testing.T) {
	m := newTUIModel(nil, "", nil)
	m.width = 100
	m.height = 20
	m.selected = 1
	m.mode = modeGrid

	alpha := newTUISession("alpha", "bash", 80, 24)
	alpha.WriteOutput([]byte("hello alpha"))
	beta := newTUISession("beta", "sh", 80, 24)
	beta.WriteOutput([]byte("hello beta"))

	m.sessions = []*tuiSession{alpha, beta}
	m.order = []string{"alpha", "beta"}

	view := xansi.Strip(m.gridView())
	for _, want := range []string{"[1] alpha", "▶ [2] beta", "hello beta", "Ctrl+A prefix", "[beta] 2/2"} {
		if !strings.Contains(view, want) {
			t.Fatalf("gridView() missing %q in:\n%s", want, view)
		}
	}
}

func TestGlyphANSI_CursorOnDefaultColorsUsesWhiteBlock(t *testing.T) {
	start, end := glyphANSI(vt10x.Glyph{FG: vt10x.DefaultFG, BG: vt10x.DefaultBG}, true)
	if !strings.Contains(start, "\x1b[30;107m") {
		t.Fatalf("glyphANSI() start = %q, want bright white cursor block", start)
	}
	if end != "\x1b[0m" {
		t.Fatalf("glyphANSI() end = %q, want reset", end)
	}
}

func TestHitTest_GridModeMapsCoordinatesToPanel(t *testing.T) {
	m := newTUIModel(nil, "", nil)
	m.width = 120
	m.height = 20
	m.mode = modeGrid
	m.sessions = []*tuiSession{
		newTUISession("alpha", "bash", 80, 24),
		newTUISession("beta", "sh", 80, 24),
		newTUISession("gamma", "zsh", 80, 24),
		newTUISession("delta", "fish", 80, 24),
	}
	m.order = []string{"alpha", "beta", "gamma", "delta"}

	if got := m.hitTest(61, 1); got != 1 {
		t.Fatalf("hitTest(61, 1) = %d, want 1", got)
	}
}

func TestDirtyTracking_LinesCachedAndReused(t *testing.T) {
	sess := newTUISession("test", "bash", 40, 10)
	sess.WriteOutput([]byte("line one\r\n"))

	// First render populates the cache
	lines1, _, _ := sess.RenderANSI(40, 10)
	if lines1[0] == "" {
		t.Fatal("first render should produce non-empty first line")
	}

	// Second render without new output should return cached content
	lines2, _, _ := sess.RenderANSI(40, 10)
	if lines2[0] != lines1[0] {
		t.Fatalf("cached render mismatch:\n  got:  %q\n  want: %q", lines2[0], lines1[0])
	}

	// Write new output, cache should update — line 1 now has content
	sess.WriteOutput([]byte("line two\r\n"))
	lines3, _, _ := sess.RenderANSI(40, 10)
	if lines3[1] == lines1[1] {
		t.Fatal("render should reflect new output after write")
	}
}
