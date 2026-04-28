package hub

import (
	"strings"
	"testing"
	"time"
)

func TestClientSession_WriteOutputAndScreen(t *testing.T) {
	sess := NewClientSession("test1", "test@host", 80, 24, nil)
	defer sess.Close()

	sess.WriteOutput([]byte("Hello, World!\r\n"))
	sess.WriteOutput([]byte("Line two"))

	// vt10x processes writes asynchronously via internal pipe
	time.Sleep(50 * time.Millisecond)

	screen := sess.Screen()
	if !strings.Contains(screen, "Hello, World!") {
		t.Fatalf("screen should contain 'Hello, World!', got:\n%s", screen)
	}
	if !strings.Contains(screen, "Line two") {
		t.Fatalf("screen should contain 'Line two', got:\n%s", screen)
	}
}

func TestClientSession_ScreenSize(t *testing.T) {
	sess := NewClientSession("test2", "test@host", 120, 40, nil)
	defer sess.Close()

	if sess.Cols != 120 || sess.Rows != 40 {
		t.Fatalf("expected 120x40, got %dx%d", sess.Cols, sess.Rows)
	}
}
