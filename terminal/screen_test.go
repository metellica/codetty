package terminal

import (
	"strings"
	"testing"
)

func TestScreenMirror_WriteAndSnapshot(t *testing.T) {
	sm := NewScreenMirror(80, 24)
	sm.Write([]byte("hello world"))
	snap := sm.Snapshot()
	if len(snap) == 0 {
		t.Fatal("expected non-empty snapshot after write")
	}
	if !strings.Contains(string(snap), "hello world") {
		t.Errorf("snapshot does not contain expected text")
	}
}

func TestScreenMirror_Resize(t *testing.T) {
	sm := NewScreenMirror(80, 24)
	sm.Resize(120, 40)
	cols, rows := sm.Size()
	if cols != 120 || rows != 40 {
		t.Errorf("got %dx%d, want 120x40", cols, rows)
	}
}

func TestScreenMirror_EmptySnapshot(t *testing.T) {
	sm := NewScreenMirror(80, 24)
	snap := sm.Snapshot()
	if snap != nil {
		t.Errorf("expected nil snapshot before any write, got %d bytes", len(snap))
	}
}

func TestDefaultShell(t *testing.T) {
	sh := DefaultShell()
	if sh == "" {
		t.Error("DefaultShell returned empty string")
	}
}

func TestScreenMirror_TailText(t *testing.T) {
	sm := NewScreenMirror(40, 6)
	sm.Write([]byte("line one\r\nline two\r\nEsc to cancel"))

	full := sm.TailText(0)
	if !strings.Contains(full, "line one") || !strings.Contains(full, "Esc to cancel") {
		t.Errorf("full tail missing content: %q", full)
	}

	tail := sm.TailText(4)
	if strings.Contains(tail, "line one") {
		t.Errorf("tail(4) of 6 rows should not include row 0: %q", tail)
	}
	if !strings.Contains(tail, "Esc to cancel") {
		t.Errorf("tail(4) missing bottom content: %q", tail)
	}
}

func TestScreenMirror_TailTextWideChars(t *testing.T) {
	sm := NewScreenMirror(40, 4)
	sm.Write([]byte("宽字符 test"))
	tail := sm.TailText(0)
	if !strings.Contains(tail, "宽字符 test") {
		t.Errorf("wide chars mangled: %q", tail)
	}
}

func TestScreenMirror_TailTextEmpty(t *testing.T) {
	sm := NewScreenMirror(40, 4)
	if got := sm.TailText(0); got != "" {
		t.Errorf("expected empty tail before writes, got %q", got)
	}
}

func TestScreenMirror_SnapshotWideCharsNoExtraSpaces(t *testing.T) {
	// Regression: snapshot of wide (CJK) chars must not insert extra spaces
	// between characters when replayed into a new terminal.
	sm := NewScreenMirror(40, 4)
	sm.Write([]byte("你好世界"))

	snap := sm.Snapshot()
	if len(snap) == 0 {
		t.Fatal("expected non-empty snapshot")
	}

	// Replay into a fresh ScreenMirror and verify text is intact.
	sm2 := NewScreenMirror(40, 4)
	sm2.Write(snap)
	tail := sm2.TailText(0)
	if !strings.Contains(tail, "你好世界") {
		t.Errorf("wide chars corrupted after snapshot replay: %q", tail)
	}
}

func TestScreenMirror_ActiveText(t *testing.T) {
	// Simulate a TUI agent that shows hints at the bottom of the screen.
	sm := NewScreenMirror(40, 20)
	// Working phase: status bar at bottom of screen.
	sm.Write([]byte("Thinking...\r\n"))
	// Move to row 19 (bottom) and write status
	sm.Write([]byte("\x1b[19;1Hesc interrupt"))

	active := sm.ActiveText()
	if !strings.Contains(active, "esc interrupt") {
		t.Errorf("working ActiveText should include bottom hint: %q", active)
	}

	// After finish: clear and write idle status at bottom
	sm.Write([]byte("\x1b[H\x1b[J"))
	sm.Write([]byte("Result done!\r\n"))
	sm.Write([]byte("\x1b[19;1H/ commands · ? help"))

	active = sm.ActiveText()
	if !strings.Contains(active, "/ commands") {
		t.Errorf("idle ActiveText should include idle hint: %q", active)
	}
}

func TestScreenMirror_ActiveTextExcludesOldContent(t *testing.T) {
	// With a 20-row screen and 10-row tail, content at the top is excluded.
	sm := NewScreenMirror(40, 20)
	sm.Write([]byte("old esc interrupt\r\n"))
	// Write new content at bottom
	sm.Write([]byte("\x1b[18;1H/ commands · ? help"))

	active := sm.ActiveText()
	// "esc interrupt" at row 0 should be excluded (only last 10 rows scanned)
	if strings.Contains(active, "old esc interrupt") {
		t.Errorf("ActiveText should exclude old top content: %q", active)
	}
	if !strings.Contains(active, "/ commands") {
		t.Errorf("ActiveText should include bottom content: %q", active)
	}
}

func TestScreenMirror_ActiveTextEmpty(t *testing.T) {
	sm := NewScreenMirror(40, 4)
	if got := sm.ActiveText(); got != "" {
		t.Errorf("expected empty ActiveText before writes, got %q", got)
	}
}
