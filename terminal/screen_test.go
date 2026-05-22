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
