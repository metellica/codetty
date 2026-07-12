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
