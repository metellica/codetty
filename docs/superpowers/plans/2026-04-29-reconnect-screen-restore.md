# Reconnect Screen Restore Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When a client reconnects to the hub (after server restart or network drop), immediately restore the terminal display instead of showing a black screen.

**Architecture:** Client maintains a 256KB ring buffer of raw PTY output. On every (re)connection, after `MsgRegister`, the client sends `MsgReplay` containing the buffered bytes. The hub feeds this into the new session's vt10x terminal, reconstructing the screen.

**Tech Stack:** Go, gorilla/websocket, hinshun/vt10x, creack/pty

---

## File Structure

| File | Role | Action |
|------|------|--------|
| `protocol/protocol.go` | Protocol message types and encoding | Modify: add `MsgReplay` const + `EncodeReplay()` |
| `protocol/protocol_test.go` | Protocol unit tests | Modify: add `MsgReplay` test |
| `cmd/codetty-cli/ringbuf.go` | Ring buffer data structure | Create |
| `cmd/codetty-cli/ringbuf_test.go` | Ring buffer unit tests | Create |
| `cmd/codetty-cli/main.go` | CLI entry point | Modify: integrate ring buffer, send replay on connect |
| `hub/hub.go` | Hub connection handler | Modify: handle `MsgReplay` in `HandleConn` |
| `hub/hub_test.go` | Hub integration tests | Modify: add replay integration test |

---

### Task 1: Add MsgReplay to Protocol

**Files:**
- Modify: `protocol/protocol.go:7-12` (const block)
- Modify: `protocol/protocol_test.go` (add test)

- [ ] **Step 1: Write the failing test for EncodeReplay**

Add to `protocol/protocol_test.go`:

```go
func TestEncodeReplay(t *testing.T) {
	data := []byte("hello\x1b[32mworld\x1b[0m")
	encoded := EncodeReplay(data)
	if MsgType(encoded[0]) != MsgReplay {
		t.Fatalf("expected type %c, got %c", MsgReplay, encoded[0])
	}
	if string(encoded[1:]) != string(data) {
		t.Fatalf("payload mismatch: got %q, want %q", encoded[1:], data)
	}
}
```

Also add a replay entry to the `TestParseType` table:

```go
{"replay", []byte("Rhello"), MsgReplay, true},
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/logsim-mount/codetty && go test ./protocol/ -run TestEncodeReplay -v`
Expected: compilation error — `MsgReplay` and `EncodeReplay` undefined.

- [ ] **Step 3: Implement MsgReplay constant and EncodeReplay function**

In `protocol/protocol.go`, add `MsgReplay` to the const block:

```go
const (
	// cli → hub
	MsgRegister MsgType = 'r'
	MsgOutput   MsgType = 'o'
	MsgReplay   MsgType = 'R' // buffered PTY output for screen recovery

	// hub → cli
	MsgInput  MsgType = 'i'
	MsgResize MsgType = 's'
)
```

Add the `EncodeReplay` function after `EncodeOutput`:

```go
func EncodeReplay(data []byte) []byte {
	return append([]byte{byte(MsgReplay)}, data...)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /root/logsim-mount/codetty && go test ./protocol/ -v`
Expected: all tests PASS including `TestEncodeReplay` and updated `TestParseType`.

- [ ] **Step 5: Commit**

```bash
cd /root/logsim-mount/codetty
git add protocol/protocol.go protocol/protocol_test.go
git commit -m "feat(protocol): add MsgReplay message type for screen restore

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

### Task 2: Implement RingBuffer

**Files:**
- Create: `cmd/codetty-cli/ringbuf.go`
- Create: `cmd/codetty-cli/ringbuf_test.go`

- [ ] **Step 1: Write failing tests for RingBuffer**

Create `cmd/codetty-cli/ringbuf_test.go`:

```go
package main

import (
	"bytes"
	"testing"
)

func TestRingBuffer_EmptyBytes(t *testing.T) {
	rb := NewRingBuffer(64)
	got := rb.Bytes()
	if len(got) != 0 {
		t.Fatalf("expected empty, got %d bytes", len(got))
	}
}

func TestRingBuffer_WriteWithinCapacity(t *testing.T) {
	rb := NewRingBuffer(64)
	rb.Write([]byte("hello"))
	rb.Write([]byte(" world"))

	got := rb.Bytes()
	if !bytes.Equal(got, []byte("hello world")) {
		t.Fatalf("got %q, want %q", got, "hello world")
	}
}

func TestRingBuffer_WrapAround(t *testing.T) {
	rb := NewRingBuffer(8)
	rb.Write([]byte("ABCDEFGH")) // fills exactly
	rb.Write([]byte("IJ"))       // overwrites A,B

	got := rb.Bytes()
	want := []byte("CDEFGHIJ")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRingBuffer_WriteLargerThanCapacity(t *testing.T) {
	rb := NewRingBuffer(4)
	rb.Write([]byte("ABCDEFGH"))

	got := rb.Bytes()
	want := []byte("EFGH")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRingBuffer_MultipleWraps(t *testing.T) {
	rb := NewRingBuffer(4)
	rb.Write([]byte("AB"))
	rb.Write([]byte("CD"))
	rb.Write([]byte("EF"))
	rb.Write([]byte("GH"))

	got := rb.Bytes()
	want := []byte("EFGH")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/logsim-mount/codetty && go test ./cmd/codetty-cli/ -run TestRingBuffer -v`
Expected: compilation error — `NewRingBuffer` undefined.

- [ ] **Step 3: Implement RingBuffer**

Create `cmd/codetty-cli/ringbuf.go`:

```go
package main

import "sync"

// RingBuffer is a fixed-size circular buffer for raw PTY output.
// Thread-safe: Write and Bytes can be called from different goroutines.
type RingBuffer struct {
	mu   sync.Mutex
	buf  []byte
	size int
	pos  int
	full bool
}

// NewRingBuffer creates a ring buffer with the given capacity in bytes.
func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		buf:  make([]byte, size),
		size: size,
	}
}

// Write appends data to the ring buffer, overwriting oldest data if full.
func (r *RingBuffer) Write(data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// If data is larger than buffer, only keep the tail
	if len(data) >= r.size {
		copy(r.buf, data[len(data)-r.size:])
		r.pos = 0
		r.full = true
		return
	}

	n := copy(r.buf[r.pos:], data)
	if n < len(data) {
		// Wrapped around
		copy(r.buf, data[n:])
		r.pos = len(data) - n
		r.full = true
	} else {
		r.pos += n
		if r.pos == r.size {
			r.pos = 0
			r.full = true
		}
	}
}

// Bytes returns the buffer contents in chronological order.
// Returns a new slice (safe to use without locking).
func (r *RingBuffer) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.full {
		if r.pos == 0 {
			return nil
		}
		out := make([]byte, r.pos)
		copy(out, r.buf[:r.pos])
		return out
	}

	out := make([]byte, r.size)
	n := copy(out, r.buf[r.pos:])
	copy(out[n:], r.buf[:r.pos])
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /root/logsim-mount/codetty && go test ./cmd/codetty-cli/ -run TestRingBuffer -v`
Expected: all 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/logsim-mount/codetty
git add cmd/codetty-cli/ringbuf.go cmd/codetty-cli/ringbuf_test.go
git commit -m "feat(cli): add RingBuffer for PTY output replay

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

### Task 3: Integrate RingBuffer into Client and Send Replay on Connect

**Files:**
- Modify: `cmd/codetty-cli/main.go:76-99` (PTY reader + connectionLoop call)
- Modify: `cmd/codetty-cli/main.go:111-132` (connectionLoop signature)
- Modify: `cmd/codetty-cli/main.go:134-151` (runConnection — send replay after register)

- [ ] **Step 1: Modify PTY reader to write to ring buffer**

In `cmd/codetty-cli/main.go`, create the ring buffer before the PTY reader goroutine (after line 75, before line 77). Change the PTY reader and connectionLoop call.

Replace lines 77-99:

```go
	// Single PTY reader → local stdout + hub channel
	outputCh := make(chan []byte, 256)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				os.Stdout.Write(buf[:n])
				cp := make([]byte, n)
				copy(cp, buf[:n])
				select {
				case outputCh <- cp:
				default: // drop if hub not keeping up
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Run connection loop (reconnects on failure)
	go connectionLoop(*serverURL, *clientID, title, cols, rows, ptmx, outputCh, doneCh)
```

with:

```go
	// Ring buffer for replay on reconnect (256KB)
	replayBuf := NewRingBuffer(256 * 1024)

	// Single PTY reader → local stdout + hub channel + replay buffer
	outputCh := make(chan []byte, 256)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				os.Stdout.Write(buf[:n])
				cp := make([]byte, n)
				copy(cp, buf[:n])
				replayBuf.Write(cp)
				select {
				case outputCh <- cp:
				default: // drop if hub not keeping up
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Run connection loop (reconnects on failure)
	go connectionLoop(*serverURL, *clientID, title, cols, rows, ptmx, outputCh, doneCh, replayBuf)
```

- [ ] **Step 2: Update connectionLoop and runConnection signatures**

Replace the `connectionLoop` function (lines 111-132):

```go
func connectionLoop(serverURL, clientID, title string, cols, rows uint16, ptmx *os.File, outputCh <-chan []byte, done <-chan error, replayBuf *RingBuffer) {
	delay := time.Second
	maxDelay := 30 * time.Second

	for {
		err := runConnection(serverURL, clientID, title, cols, rows, ptmx, outputCh, done, replayBuf)
		if err == nil {
			return
		}

		select {
		case <-done:
			return
		case <-time.After(delay):
		}

		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}
```

- [ ] **Step 3: Send MsgReplay after MsgRegister in runConnection**

Replace `runConnection` function (lines 134-210). The only change is adding the `replayBuf` parameter and sending replay after register:

```go
func runConnection(serverURL, clientID, title string, cols, rows uint16, ptmx *os.File, outputCh <-chan []byte, done <-chan error, replayBuf *RingBuffer) error {
	conn, _, err := websocket.DefaultDialer.Dial(serverURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	regMsg, err := protocol.EncodeRegister(protocol.RegisterPayload{
		ID:    clientID,
		Title: title,
		Cols:  cols,
		Rows:  rows,
	})
	if err != nil {
		return fmt.Errorf("encode register: %w", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, regMsg); err != nil {
		return fmt.Errorf("send register: %w", err)
	}

	// Send buffered output for screen restore
	if replay := replayBuf.Bytes(); len(replay) > 0 {
		msg := protocol.EncodeReplay(replay)
		if err := conn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
			return fmt.Errorf("send replay: %w", err)
		}
	}

	errCh := make(chan error, 2)
	stopCh := make(chan struct{})
	defer close(stopCh)

	// Output channel → hub
	go func() {
		for {
			select {
			case data, ok := <-outputCh:
				if !ok {
					errCh <- nil
					return
				}
				msg := protocol.EncodeOutput(data)
				if werr := conn.WriteMessage(websocket.BinaryMessage, msg); werr != nil {
					errCh <- fmt.Errorf("write output: %w", werr)
					return
				}
			case <-stopCh:
				return
			}
		}
	}()

	// Hub → PTY input / resize
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				errCh <- fmt.Errorf("read: %w", err)
				return
			}
			msgType, payload, ok := protocol.ParseType(msg)
			if !ok {
				continue
			}
			switch msgType {
			case protocol.MsgInput:
				ptmx.Write(payload)
			case protocol.MsgResize:
				p, err := protocol.DecodeResize(payload)
				if err == nil {
					pty.Setsize(ptmx, &pty.Winsize{Cols: p.Cols, Rows: p.Rows})
				}
			}
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-done:
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(1000, "process exited"))
		return nil
	}
}
```

- [ ] **Step 4: Build to verify compilation**

Run: `cd /root/logsim-mount/codetty && go build ./cmd/codetty-cli/`
Expected: successful build, no errors.

- [ ] **Step 5: Run all existing tests to verify no regressions**

Run: `cd /root/logsim-mount/codetty && go test ./... -v`
Expected: all existing tests PASS.

- [ ] **Step 6: Commit**

```bash
cd /root/logsim-mount/codetty
git add cmd/codetty-cli/main.go
git commit -m "feat(cli): send replay buffer on reconnect

Integrates RingBuffer into PTY reader loop and sends MsgReplay
after MsgRegister on every connection.

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

### Task 4: Handle MsgReplay in Hub

**Files:**
- Modify: `hub/hub.go:82-113` (HandleConn switch statement)
- Modify: `hub/hub_test.go` (add replay integration test)

- [ ] **Step 1: Write the failing integration test**

Add to `hub/hub_test.go`:

```go
func TestHub_ReplayRestoresScreen(t *testing.T) {
	h := New()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		h.HandleConn(conn)
	}))
	defer s.Close()

	clientConn, _, err := websocket.DefaultDialer.Dial("ws"+s.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer clientConn.Close()

	// Register session
	regMsg, _ := protocol.EncodeRegister(protocol.RegisterPayload{
		ID: "replay-test", Title: "test", Cols: 80, Rows: 24,
	})
	clientConn.WriteMessage(websocket.BinaryMessage, regMsg)
	time.Sleep(100 * time.Millisecond)

	// Send replay data (simulates buffered PTY output)
	replayData := []byte("restored content\r\nline two")
	clientConn.WriteMessage(websocket.BinaryMessage, protocol.EncodeReplay(replayData))
	time.Sleep(100 * time.Millisecond)

	// Verify screen was restored
	sess := h.Session("replay-test")
	if sess == nil {
		t.Fatal("session not found")
	}
	screen := sess.Screen()
	if !strings.Contains(screen, "restored content") {
		t.Fatalf("screen should contain 'restored content', got:\n%s", screen)
	}
	if !strings.Contains(screen, "line two") {
		t.Fatalf("screen should contain 'line two', got:\n%s", screen)
	}
}
```

Add `"strings"` to the import block of `hub/hub_test.go` if not already present.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd /root/logsim-mount/codetty && go test ./hub/ -run TestHub_ReplayRestoresScreen -v`
Expected: compilation error — `EncodeReplay` undefined (protocol change from Task 1 should make this compile, but the hub will silently ignore the unknown message type, so the screen check will fail with empty screen).

Note: if Task 1 is already done, the test will compile but fail because `HandleConn` doesn't handle `MsgReplay` yet — the hub reads the message but falls through the switch without acting on it, so vt10x stays empty.

- [ ] **Step 3: Handle MsgReplay in HandleConn**

In `hub/hub.go`, modify the switch statement inside `HandleConn` (after the `case protocol.MsgOutput:` block, before the closing `}`). Add a new case between the existing `MsgOutput` case and the closing brace of the switch:

Replace lines 82-113:

```go
		switch msgType {
		case protocol.MsgRegister:
			p, err := protocol.DecodeRegister(payload)
			if err != nil {
				log.Printf("bad register payload: %v", err)
				return
			}
			writer := NewConnWriter(conn)
			sess = NewClientSession(p.ID, p.Title, p.Cols, p.Rows, writer)
			h.addSession(sess)

		case protocol.MsgReplay:
			if sess == nil {
				return
			}
			sess.WriteOutput(payload)
			if h.program != nil {
				h.fsMu.RLock()
				inFullscreen := h.fullscreenID != ""
				h.fsMu.RUnlock()
				if !inFullscreen {
					h.program.Send(ScreenUpdateMsg{ID: sess.ID, Data: nil})
				}
			}

		case protocol.MsgOutput:
			if sess == nil {
				return
			}
			sess.WriteOutput(payload)

			// In fullscreen mode, forward raw output to the fullscreen writer.
			// Skip program.Send entirely — bubbletea's event loop is blocked
			// inside tea.Exec and its msgs channel is unbuffered, so Send
			// would block forever.
			h.fsMu.RLock()
			inFullscreen := h.fullscreenID != ""
			if h.fullscreenID == sess.ID && h.fsWriter != nil {
				h.fsWriter.Write(payload)
			}
			h.fsMu.RUnlock()

			if h.program != nil && !inFullscreen {
				h.program.Send(ScreenUpdateMsg{ID: sess.ID, Data: payload})
			}
		}
```

- [ ] **Step 4: Run the replay test to verify it passes**

Run: `cd /root/logsim-mount/codetty && go test ./hub/ -run TestHub_ReplayRestoresScreen -v`
Expected: PASS.

- [ ] **Step 5: Run all tests to verify no regressions**

Run: `cd /root/logsim-mount/codetty && go test ./... -v`
Expected: all tests PASS.

- [ ] **Step 6: Build both binaries to verify compilation**

Run: `cd /root/logsim-mount/codetty && go build ./cmd/codetty-cli/ && go build ./cmd/codetty-hub/`
Expected: successful build, no errors.

- [ ] **Step 7: Commit**

```bash
cd /root/logsim-mount/codetty
git add hub/hub.go hub/hub_test.go
git commit -m "feat(hub): handle MsgReplay for screen restore on reconnect

Feed replay data into vt10x on reconnect, send single TUI refresh.

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```
