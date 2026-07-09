package main

import (
	"fmt"
	"testing"
)

// assertRenderMatchesSnapshot verifies that the incremental per-line cache in
// RenderANSI produces exactly the same output as the uncached full render.
func assertRenderMatchesSnapshot(t *testing.T, s *tuiSession, w, h int, step string) {
	t.Helper()
	lines, curX, curY := s.RenderANSI(w, h)
	snap := s.SnapshotANSI(w, h)
	for i := range snap.lines {
		if lines[i] != snap.lines[i] {
			t.Fatalf("%s: line %d mismatch\n cached: %q\n   full: %q", step, i, lines[i], snap.lines[i])
		}
	}
	if snap.cursorVisible && (curX != snap.cursorX || curY != snap.cursorY) {
		t.Fatalf("%s: cursor = (%d,%d), want (%d,%d)", step, curX, curY, snap.cursorX, snap.cursorY)
	}
}

// TestRenderANSICacheConsistency drives a session through incremental writes,
// cursor moves, scrolling, and resizes, checking after every step that the
// dirty-line cache never returns stale content.
func TestRenderANSICacheConsistency(t *testing.T) {
	s := newTUISession("test", "sh", 40, 8)
	w, h := 40, 8

	steps := []struct {
		name string
		data string
	}{
		{"initial text", "hello \x1b[31mred\x1b[0m world\r\n"},
		{"second line", "\x1b[1;44mbold on blue\x1b[0m tail\r\n"},
		{"cursor move only", "\x1b[1;3H"},
		{"overwrite one line", "\x1b[2;1H\x1b[2Kreplaced line two"},
		{"wide chars", "\x1b[3;1H\u4f60\u597d\u4e16\u754c wide"},
		{"scroll", "\x1b[8;1H\r\n\r\n\r\nafter scroll\r\n"},
		{"clear screen", "\x1b[2J\x1b[Hfresh"},
		{"hide cursor", "\x1b[?25l"},
		{"show cursor", "\x1b[?25h"},
		{"reverse video", "\x1b[7minverted\x1b[0m"},
	}
	for _, st := range steps {
		s.WriteOutput([]byte(st.data))
		assertRenderMatchesSnapshot(t, s, w, h, st.name)
		// Render again with no new output: must still match.
		assertRenderMatchesSnapshot(t, s, w, h, st.name+" (cached)")
	}

	// Fill enough lines to force startRow scrolling in the render window.
	for i := 0; i < 20; i++ {
		s.WriteOutput([]byte(fmt.Sprintf("line %d with some content\r\n", i)))
		assertRenderMatchesSnapshot(t, s, w, h, fmt.Sprintf("scroll fill %d", i))
	}

	// Resize invalidates the cache.
	s.Resize(30, 6)
	assertRenderMatchesSnapshot(t, s, 30, 6, "after resize")
	s.WriteOutput([]byte("post-resize output\r\n"))
	assertRenderMatchesSnapshot(t, s, 30, 6, "post-resize write")
}

// TestRenderRowANSIRunLength checks that identically-styled adjacent cells
// share a single SGR sequence instead of one per cell.
func TestRenderRowANSIRunLength(t *testing.T) {
	s := newTUISession("test", "sh", 40, 4)
	s.WriteOutput([]byte("\x1b[31mredredred\x1b[0m plain"))
	snap := s.SnapshotANSI(40, 4)
	line := snap.lines[0]
	count := 0
	for i := 0; i+5 <= len(line); i++ {
		if line[i:i+5] == "\x1b[31m" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 red SGR sequence for a run of red cells, got %d in %q", count, line)
	}
}
