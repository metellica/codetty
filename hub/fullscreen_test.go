package hub

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFullscreenHandlerRunClearsScreenAndEnablesWrap(t *testing.T) {
	sess := NewClientSession("claude", "claude", 20, 6, nil)
	sess.WriteOutput([]byte("\x1b[2J\x1b[Hheader\x1b[5;4Hprompt"))

	var stdout bytes.Buffer
	handler := &fullscreenHandler{
		session: sess,
		hub:     New(),
		stdin:   bytes.NewReader([]byte{0x01}),
		stdout:  &stdout,
	}

	if err := handler.Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got := stdout.String()
	// Should contain screen clear
	if !strings.Contains(got, "\x1b[2J") {
		t.Fatalf("fullscreen should clear screen, got %q", got)
	}
	// Should enable line wrap
	if !strings.Contains(got, "\x1b[?7h") {
		t.Fatalf("fullscreen should enable wrap, got %q", got)
	}
	// Should disable newline mode to preserve normal terminal line discipline.
	if !strings.Contains(got, "\x1b[20l") {
		t.Fatalf("fullscreen should disable newline mode, got %q", got)
	}
	// Should show cursor
	if !strings.Contains(got, "\x1b[?25h") {
		t.Fatalf("fullscreen should show cursor, got %q", got)
	}
}

func TestFullscreenHandlerRunDoesNotInterleaveLiveOutputWithSnapshot(t *testing.T) {
	h := New()
	sess := NewClientSession("claude", "claude", 20, 6, nil)
	sess.WriteOutput([]byte("\x1b[2J\x1b[Hheader\x1b[5;4Hprompt"))

	liveDone := make(chan struct{})
	stdout := &triggerWriter{}
	stdout.onFirstWrite = func() {
		go func() {
			h.fsMu.RLock()
			if h.fullscreenID == sess.ID && h.fsWriter != nil {
				h.fsWriter.Write([]byte("LIVE"))
			}
			h.fsMu.RUnlock()
			close(liveDone)
		}()
		select {
		case <-liveDone:
		case <-time.After(20 * time.Millisecond):
		}
	}

	handler := &fullscreenHandler{
		session: sess,
		hub:     h,
		stdin:   bytes.NewReader([]byte{0x01}),
		stdout:  stdout,
	}

	if err := handler.Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	select {
	case <-liveDone:
	case <-time.After(time.Second):
		t.Fatal("live output did not complete")
	}

	got := stdout.String()
	liveIdx := strings.Index(got, "LIVE")
	// Live output should appear after the initial setup sequence
	clearIdx := strings.Index(got, "\x1b[2J")
	if liveIdx < 0 || clearIdx < 0 {
		t.Fatalf("test setup should produce live output and clear screen, got %q", got)
	}
	if liveIdx < clearIdx {
		t.Fatalf("live output should appear after screen clear, live=%d clear=%d output=%q", liveIdx, clearIdx, got)
	}
}

type triggerWriter struct {
	mu           sync.Mutex
	buf          bytes.Buffer
	triggered    bool
	onFirstWrite func()
}

func (w *triggerWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	trigger := !w.triggered
	if trigger {
		w.triggered = true
	}
	n, err := w.buf.Write(p)
	w.mu.Unlock()
	if trigger && w.onFirstWrite != nil {
		w.onFirstWrite()
	}
	return n, err
}

func (w *triggerWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestFullscreenHandlerExitsWhenSessionDisconnects(t *testing.T) {
	h := New()
	sess := NewClientSession("fs-exit", "test", 80, 24, nil)

	// blockingReader blocks until its done channel is closed, simulating
	// a real stdin that has no input.
	br := &blockingStdinReader{done: make(chan struct{})}

	var stdout bytes.Buffer
	handler := &fullscreenHandler{
		session: sess,
		hub:     h,
		stdin:   br,
		stdout:  &stdout,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- handler.Run()
	}()

	// Wait for fullscreen to be set up.
	time.Sleep(50 * time.Millisecond)
	if got := h.GetFullscreenID(); got != "fs-exit" {
		t.Fatalf("expected fullscreen ID 'fs-exit', got %q", got)
	}

	// Simulate the session disconnecting by closing the done channel.
	h.fsMu.Lock()
	if h.fsDoneCh != nil {
		close(h.fsDoneCh)
		h.fsDoneCh = nil
	}
	h.fsMu.Unlock()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run() returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fullscreen handler did not exit after session disconnect")
	}

	// Fullscreen state should be cleared.
	if got := h.GetFullscreenID(); got != "" {
		t.Fatalf("expected empty fullscreen ID after exit, got %q", got)
	}
}

// blockingStdinReader blocks Read until done is closed, then returns io.EOF.
type blockingStdinReader struct {
	done chan struct{}
}

func (r *blockingStdinReader) Read(p []byte) (int, error) {
	<-r.done
	return 0, io.EOF
}

