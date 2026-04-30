package hub

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFullscreenHandlerRunRestoresRemoteCursorAfterSnapshot(t *testing.T) {
	sess := NewClientSession("claude", "claude", 20, 6, nil)
	sess.WriteOutput([]byte("\x1b[2J\x1b[Hheader\x1b[5;4Hprompt"))
	_, curX, curY := sess.RenderANSI(int(sess.Cols), int(sess.Rows))
	if curX < 0 || curY < 0 {
		t.Fatal("test setup should leave a visible cursor")
	}

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

	want := fmt.Sprintf("\x1b[%d;%dH", curY+1, curX+1)
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("fullscreen snapshot should restore cursor with %q, got %q", want, stdout.String())
	}
}

func TestFullscreenHandlerRunResetsLocalTerminalStateBeforeSnapshot(t *testing.T) {
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
	resetState := strings.Index(got, "\x1b[0m\x1b[r\x1b[?6l")
	clearScreen := strings.Index(got, "\x1b[2J\x1b[H")
	firstPaintedLine := strings.Index(got, "\x1b[1;1H")
	if resetState < 0 || clearScreen < 0 || firstPaintedLine < 0 {
		t.Fatalf("snapshot should reset local terminal state, clear, and paint; got %q", got)
	}
	if !(resetState < clearScreen && clearScreen < firstPaintedLine) {
		t.Fatalf("snapshot should reset state before clearing and painting, reset=%d clear=%d paint=%d output=%q", resetState, clearScreen, firstPaintedLine, got)
	}
}

func TestFullscreenHandlerRunDisablesAutowrapWhilePaintingSnapshot(t *testing.T) {
	sess := NewClientSession("claude", "claude", 20, 6, nil)
	sess.WriteOutput([]byte("\x1b[2J\x1b[H" + strings.Repeat("x", int(sess.Cols)) + "\x1b[5;4Hprompt"))
	_, curX, curY := sess.RenderANSI(int(sess.Cols), int(sess.Rows))

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
	disableWrap := strings.Index(got, "\x1b[?7l")
	firstPaintedLine := strings.Index(got, "\x1b[1;1H")
	enableWrap := strings.Index(got, "\x1b[?7h")
	cursorRestore := strings.LastIndex(got, fmt.Sprintf("\x1b[%d;%dH", curY+1, curX+1))
	if disableWrap < 0 || firstPaintedLine < 0 || enableWrap < 0 || cursorRestore < 0 {
		t.Fatalf("snapshot should disable autowrap, paint, re-enable autowrap, then restore cursor; got %q", got)
	}
	if !(disableWrap < firstPaintedLine && firstPaintedLine < enableWrap && enableWrap < cursorRestore) {
		t.Fatalf("unexpected snapshot control order: disable=%d paint=%d enable=%d cursor=%d output=%q", disableWrap, firstPaintedLine, enableWrap, cursorRestore, got)
	}
}

func TestFullscreenHandlerRunPreservesDisabledAutowrapAfterSnapshot(t *testing.T) {
	sess := NewClientSession("claude", "claude", 20, 6, nil)
	sess.WriteOutput([]byte("\x1b[?7l\x1b[2J\x1b[H" + strings.Repeat("x", int(sess.Cols)) + "\x1b[5;4Hprompt"))

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
	lastDisableWrap := strings.LastIndex(got, "\x1b[?7l")
	lastEnableWrap := strings.LastIndex(got, "\x1b[?7h")
	cursorRestore := strings.LastIndex(got, "\x1b[5;10H")
	if lastDisableWrap < 0 || cursorRestore < 0 {
		t.Fatalf("snapshot should restore disabled autowrap and cursor, got %q", got)
	}
	if lastDisableWrap < lastEnableWrap || lastDisableWrap > cursorRestore {
		t.Fatalf("snapshot should leave autowrap disabled before cursor restore, disable=%d enable=%d cursor=%d output=%q", lastDisableWrap, lastEnableWrap, cursorRestore, got)
	}
}

func TestFullscreenHandlerRunRestoresHiddenRemoteCursorAfterSnapshot(t *testing.T) {
	sess := NewClientSession("claude", "claude", 20, 6, nil)
	sess.WriteOutput([]byte("\x1b[2J\x1b[Hheader\x1b[?25l\x1b[5;4Hprompt"))

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
	cursorRestore := strings.LastIndex(got, "\x1b[5;10H")
	hideCursor := strings.LastIndex(got, "\x1b[?25l")
	if cursorRestore < 0 {
		t.Fatalf("fullscreen snapshot should restore hidden cursor position, got %q", got)
	}
	if hideCursor < cursorRestore {
		t.Fatalf("fullscreen snapshot should keep hidden cursor hidden after restore, cursor=%d hide=%d output=%q", cursorRestore, hideCursor, got)
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
	liveOutput := strings.Index(got, "LIVE")
	finalCursorState := strings.LastIndex(got, "\x1b[?25")
	if liveOutput < 0 || finalCursorState < 0 {
		t.Fatalf("test setup should produce live output and final cursor state, got %q", got)
	}
	if liveOutput < finalCursorState {
		t.Fatalf("live output interleaved before snapshot completed, live=%d finalCursorState=%d output=%q", liveOutput, finalCursorState, got)
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
