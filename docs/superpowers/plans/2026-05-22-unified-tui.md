# Unified TUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Merge the hub TUI and daemon client into a single Bubble Tea TUI that manages local daemon sessions with focus-big/grid layout.

**Architecture:** The TUI connects to the daemon via a new `subscribe_all` protocol that streams output from all sessions simultaneously. The TUI reuses `hub/`'s layout and rendering code (vt10x, focus-big, grid, panel rendering). The daemon is extended with tagged frame types and a multi-session subscription mode.

**Tech Stack:** Go, Bubble Tea, lipgloss, vt10x, Unix sockets

---

## File Structure

| File | Role |
|------|------|
| `daemon/protocol.go` | **Modify**: Add new frame types and subscribe_all action |
| `daemon/server.go` | **Modify**: Add subscribe_all handler, broadcast tagged output |
| `daemon/subscriber.go` | **Create**: Multi-session subscriber that forwards all output |
| `daemon/subscriber_test.go` | **Create**: Tests for subscriber |
| `cmd/codetty/tui.go` | **Create**: Bubble Tea model for unified TUI |
| `cmd/codetty/tui_session.go` | **Create**: Per-session vt10x state for TUI |
| `cmd/codetty/tui_input.go` | **Create**: Key handling + Ctrl+A prefix commands |
| `cmd/codetty/tui_conn.go` | **Create**: Daemon connection management for TUI |
| `cmd/codetty/cli.go` | **Modify**: Replace runAttachLoop with TUI launch |
| `cmd/codetty/main.go` | **Modify**: Update routing for bare `codetty` |

---

### Task 1: Extend daemon protocol with multi-session frame types

**Files:**
- Modify: `daemon/protocol.go`

- [ ] **Step 1: Add new frame types and action constant**

Add to `daemon/protocol.go` after existing frame types:

```go
const (
	// ... existing ...
	FrameTaggedOutput  FrameType = 0x10 // S→C: session-tagged output
	FrameTaggedInput   FrameType = 0x11 // C→S: session-tagged input
	FrameSessionEvent  FrameType = 0x12 // S→C: JSON session lifecycle event
	FrameCreateSession FrameType = 0x13 // C→S: JSON create session request
	FrameKillSession   FrameType = 0x14 // C→S: JSON kill session request
)
```

Add to action constants:

```go
ActionSubscribeAll Action = "subscribe_all"
```

- [ ] **Step 2: Add tagged frame encode/decode helpers**

```go
// EncodeTaggedOutput prepends session name length + name to output data.
func EncodeTaggedOutput(session string, data []byte) []byte {
	nameBytes := []byte(session)
	buf := make([]byte, 1+len(nameBytes)+len(data))
	buf[0] = byte(len(nameBytes))
	copy(buf[1:], nameBytes)
	copy(buf[1+len(nameBytes):], data)
	return buf
}

// DecodeTaggedOutput extracts session name and payload from tagged frame.
func DecodeTaggedOutput(payload []byte) (session string, data []byte, err error) {
	if len(payload) < 1 {
		return "", nil, fmt.Errorf("tagged frame too short")
	}
	nameLen := int(payload[0])
	if len(payload) < 1+nameLen {
		return "", nil, fmt.Errorf("tagged frame name truncated")
	}
	session = string(payload[1 : 1+nameLen])
	data = payload[1+nameLen:]
	return session, data, nil
}

// EncodeTaggedInput prepends session name to input data (same format as output).
func EncodeTaggedInput(session string, data []byte) []byte {
	return EncodeTaggedOutput(session, data) // same encoding
}

// DecodeTaggedInput extracts session name and input from tagged frame.
func DecodeTaggedInput(payload []byte) (session string, data []byte, err error) {
	return DecodeTaggedOutput(payload)
}
```

- [ ] **Step 3: Add SessionEvent struct**

```go
// SessionEvent describes a session lifecycle change (for FrameSessionEvent).
type SessionEvent struct {
	Type     string      `json:"type"` // "created", "exited", "snapshot"
	Session  string      `json:"session"`
	Info     SessionInfo `json:"info,omitempty"`
	ExitCode int         `json:"exit_code,omitempty"`
	Cols     uint16      `json:"cols,omitempty"`
	Rows     uint16      `json:"rows,omitempty"`
}

// CreateSessionRequest is the payload for FrameCreateSession.
type CreateSessionRequest struct {
	Name    string   `json:"name,omitempty"`
	Command []string `json:"command,omitempty"`
	Cols    uint16   `json:"cols"`
	Rows    uint16   `json:"rows"`
}

// KillSessionRequest is the payload for FrameKillSession.
type KillSessionRequest struct {
	Name string `json:"name"`
}
```

- [ ] **Step 4: Add test for tagged encode/decode round-trip**

Add to `daemon/protocol_test.go`:

```go
func TestTaggedOutputRoundTrip(t *testing.T) {
	data := []byte("hello world")
	encoded := EncodeTaggedOutput("my-session", data)
	name, decoded, err := DecodeTaggedOutput(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if name != "my-session" {
		t.Errorf("name = %q, want my-session", name)
	}
	if string(decoded) != "hello world" {
		t.Errorf("data = %q, want hello world", string(decoded))
	}
}

func TestTaggedOutputEmptyPayload(t *testing.T) {
	encoded := EncodeTaggedOutput("sess", nil)
	name, data, err := DecodeTaggedOutput(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if name != "sess" || len(data) != 0 {
		t.Errorf("got name=%q data=%v", name, data)
	}
}
```

- [ ] **Step 5: Run tests and commit**

Run: `go test ./daemon/ -run TestTagged -v`
Expected: PASS

```bash
git add daemon/protocol.go daemon/protocol_test.go
git commit -m "feat(daemon): add multi-session tagged frame protocol"
```

---

### Task 2: Implement daemon subscriber for multi-session streaming

**Files:**
- Create: `daemon/subscriber.go`
- Create: `daemon/subscriber_test.go`
- Modify: `daemon/server.go`
- Modify: `daemon/session.go`

- [ ] **Step 1: Create subscriber.go**

```go
package daemon

import (
	"net"
	"sync"
)

// Subscriber is a multi-session client that receives output from ALL sessions.
type Subscriber struct {
	conn   net.Conn
	mu     sync.Mutex
	closed bool
}

func newSubscriber(conn net.Conn) *Subscriber {
	return &Subscriber{conn: conn}
}

// SendTaggedOutput sends session-tagged output to the subscriber.
func (s *Subscriber) SendTaggedOutput(session string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	WriteFrame(s.conn, FrameTaggedOutput, EncodeTaggedOutput(session, data))
}

// SendSessionEvent sends a session lifecycle event.
func (s *Subscriber) SendSessionEvent(evt SessionEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	WriteFrame(s.conn, FrameSessionEvent, MarshalJSON(evt))
}

// Close marks the subscriber as closed.
func (s *Subscriber) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

// Run reads frames from the subscriber and dispatches them.
func (s *Subscriber) Run(srv *Server) {
	defer s.Close()

	for {
		ft, payload, err := ReadFrame(s.conn)
		if err != nil {
			return
		}

		switch ft {
		case FrameTaggedInput:
			session, data, err := DecodeTaggedInput(payload)
			if err != nil {
				continue
			}
			if sess := srv.SessionByName(session); sess != nil {
				sess.Terminal.Write(data)
			}

		case FrameResize:
			var rp ResizePayload
			if json.Unmarshal(payload, &rp) == nil {
				srv.resizeAllSessions(rp.Cols, rp.Rows)
			}

		case FrameCreateSession:
			var req CreateSessionRequest
			if json.Unmarshal(payload, &req) == nil {
				srv.createSessionForSubscriber(s, req)
			}

		case FrameKillSession:
			var req KillSessionRequest
			if json.Unmarshal(payload, &req) == nil {
				srv.killSessionForSubscriber(req)
			}

		case FrameDetach:
			return
		}
	}
}
```

- [ ] **Step 2: Add subscriber tracking and broadcast to server.go**

Add fields to `Server`:

```go
type Server struct {
	// ... existing fields ...
	subscribers []*Subscriber
}
```

Add methods to `server.go`:

```go
func (s *Server) addSubscriber(sub *Subscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscribers = append(s.subscribers, sub)
}

func (s *Server) removeSubscriber(sub *Subscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, ss := range s.subscribers {
		if ss == sub {
			s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
			return
		}
	}
}

func (s *Server) broadcastToSubscribers(session string, data []byte) {
	s.mu.Lock()
	subs := append([]*Subscriber(nil), s.subscribers...)
	s.mu.Unlock()
	for _, sub := range subs {
		sub.SendTaggedOutput(session, data)
	}
}

func (s *Server) notifySubscribers(evt SessionEvent) {
	s.mu.Lock()
	subs := append([]*Subscriber(nil), s.subscribers...)
	s.mu.Unlock()
	for _, sub := range subs {
		sub.SendSessionEvent(evt)
	}
}

func (s *Server) resizeAllSessions(cols, rows uint16) {
	s.mu.Lock()
	sessions := make([]*DaemonSession, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.mu.Unlock()
	for _, sess := range sessions {
		sess.Resize(cols, rows)
	}
}

func (s *Server) createSessionForSubscriber(sub *Subscriber, req CreateSessionRequest) {
	name := req.Name
	if name == "" {
		s.mu.Lock()
		name = autoSessionName(req.Command, s.sessions)
		s.mu.Unlock()
	}
	cmd := req.Command
	if len(cmd) == 0 {
		cmd = []string{terminal.DefaultShell()}
	}
	sess, err := newDaemonSession(name, cmd, req.Cols, req.Rows)
	if err != nil {
		sub.SendSessionEvent(SessionEvent{Type: "error", Session: name})
		return
	}
	s.mu.Lock()
	s.sessions[name] = sess
	s.mu.Unlock()
	go s.watchSession(sess)
	sub.SendSessionEvent(SessionEvent{Type: "created", Session: name, Info: sess.Info()})
}

func (s *Server) killSessionForSubscriber(req KillSessionRequest) {
	s.mu.Lock()
	sess, exists := s.sessions[req.Name]
	s.mu.Unlock()
	if exists {
		sess.Kill()
	}
}
```

- [ ] **Step 3: Add handleSubscribeAll to server.go**

```go
func (s *Server) handleSubscribeAll(conn net.Conn, req Request) {
	sub := newSubscriber(conn)
	s.addSubscriber(sub)
	defer s.removeSubscriber(sub)

	// Send current session list as snapshot events
	s.mu.Lock()
	for _, sess := range s.sessions {
		if !sess.isExited() {
			snap := sess.Screen.Snapshot()
			cols, rows := sess.Screen.Size()
			sub.SendSessionEvent(SessionEvent{
				Type:    "snapshot",
				Session: sess.Name,
				Info:    sess.Info(),
				Cols:    cols,
				Rows:    rows,
			})
			if len(snap) > 0 {
				sub.SendTaggedOutput(sess.Name, snap)
			}
		}
	}
	s.mu.Unlock()

	// Send response after snapshots
	WriteFrame(conn, FrameResponse, MarshalJSON(Response{OK: true}))

	// Run subscriber loop (blocks until detach/disconnect)
	sub.Run(s)
}
```

Add to `handleConn` switch:

```go
case ActionSubscribeAll:
	s.handleSubscribeAll(conn, req)
```

- [ ] **Step 4: Add subscriber broadcast call in session.go readLoop**

Modify `DaemonSession.broadcastOutput` to also notify subscribers via a server reference. Add `server` field to `DaemonSession`:

```go
type DaemonSession struct {
	// ... existing fields ...
	server *DaemonSession // removed - use callback instead
}
```

Actually, cleaner: add a broadcast callback set by server:

In `session.go`, add field:
```go
type DaemonSession struct {
	// ... existing ...
	onOutput func(session string, data []byte) // subscriber broadcast hook
}
```

In `readLoop`, after `s.broadcastOutput(data)`:
```go
if s.onOutput != nil {
	s.onOutput(s.Name, data)
}
```

In `server.go` `handleNewSession`, after creating session:
```go
sess.onOutput = s.broadcastToSubscribers
```

- [ ] **Step 5: Notify subscribers on session events**

In `watchSession`, after deleting the session from map:
```go
s.notifySubscribers(SessionEvent{Type: "exited", Session: sess.Name, ExitCode: sess.exitCode})
```

In `handleNewSession`, after adding to map:
```go
s.notifySubscribers(SessionEvent{Type: "created", Session: name, Info: sess.Info()})
```

- [ ] **Step 6: Write subscriber_test.go**

```go
//go:build !windows

package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestSubscriber_ReceivesAllSessionOutput(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	// Create a session first
	c1, _ := net.Dial("unix", sockPath)
	WriteFrame(c1, FrameRequest, MarshalJSON(Request{
		Action: ActionNewSession, Session: "s1",
		Command: []string{"echo", "hello"}, Cols: 80, Rows: 24, NoHub: true,
	}))
	ReadFrame(c1) // response
	WriteFrame(c1, FrameDetach, nil)
	c1.Close()

	time.Sleep(100 * time.Millisecond)

	// Subscribe
	c2, _ := net.Dial("unix", sockPath)
	defer c2.Close()
	WriteFrame(c2, FrameRequest, MarshalJSON(Request{Action: ActionSubscribeAll}))

	// Should get snapshot event + tagged output + response
	c2.SetReadDeadline(time.Now().Add(2 * time.Second))
	gotSnapshot := false
	gotResponse := false
	for i := 0; i < 10; i++ {
		ft, payload, err := ReadFrame(c2)
		if err != nil {
			break
		}
		switch ft {
		case FrameSessionEvent:
			var evt SessionEvent
			json.Unmarshal(payload, &evt)
			if evt.Type == "snapshot" && evt.Session == "s1" {
				gotSnapshot = true
			}
		case FrameResponse:
			var resp Response
			json.Unmarshal(payload, &resp)
			if resp.OK {
				gotResponse = true
			}
		case FrameTaggedOutput:
			// snapshot data
		}
		if gotSnapshot && gotResponse {
			break
		}
	}
	if !gotSnapshot {
		t.Error("did not receive snapshot event")
	}
	if !gotResponse {
		t.Error("did not receive OK response")
	}
}

func TestSubscriber_ReceivesLiveOutput(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	// Subscribe first
	sub, _ := net.Dial("unix", sockPath)
	defer sub.Close()
	WriteFrame(sub, FrameRequest, MarshalJSON(Request{Action: ActionSubscribeAll}))
	// Read response
	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	ReadFrame(sub) // response

	// Create a session
	c1, _ := net.Dial("unix", sockPath)
	WriteFrame(c1, FrameRequest, MarshalJSON(Request{
		Action: ActionNewSession, Session: "live",
		Command: []string{"echo", "world"}, Cols: 80, Rows: 24, NoHub: true,
	}))
	ReadFrame(c1)
	c1.Close()

	// Subscriber should get created event + tagged output
	gotCreated := false
	gotOutput := false
	for i := 0; i < 20; i++ {
		ft, payload, err := ReadFrame(sub)
		if err != nil {
			break
		}
		switch ft {
		case FrameSessionEvent:
			var evt SessionEvent
			json.Unmarshal(payload, &evt)
			if evt.Type == "created" && evt.Session == "live" {
				gotCreated = true
			}
		case FrameTaggedOutput:
			name, _, _ := DecodeTaggedOutput(payload)
			if name == "live" {
				gotOutput = true
			}
		}
		if gotCreated && gotOutput {
			break
		}
	}
	if !gotCreated {
		t.Error("subscriber did not receive created event")
	}
	if !gotOutput {
		t.Error("subscriber did not receive tagged output")
	}
}

func TestSubscriber_CreateSession(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	sub, _ := net.Dial("unix", sockPath)
	defer sub.Close()
	WriteFrame(sub, FrameRequest, MarshalJSON(Request{Action: ActionSubscribeAll}))
	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	ReadFrame(sub) // response

	// Create session via subscriber
	WriteFrame(sub, FrameCreateSession, MarshalJSON(CreateSessionRequest{
		Name: "from-sub", Command: []string{"echo", "hi"}, Cols: 80, Rows: 24,
	}))

	// Should receive created event
	gotCreated := false
	for i := 0; i < 10; i++ {
		ft, payload, err := ReadFrame(sub)
		if err != nil {
			break
		}
		if ft == FrameSessionEvent {
			var evt SessionEvent
			json.Unmarshal(payload, &evt)
			if evt.Type == "created" && evt.Session == "from-sub" {
				gotCreated = true
				break
			}
		}
	}
	if !gotCreated {
		t.Error("did not receive created event from subscriber-initiated session")
	}
}
```

- [ ] **Step 7: Run tests and commit**

Run: `go test ./daemon/ -v -run TestSubscriber -timeout 15s`
Expected: PASS

```bash
git add daemon/protocol.go daemon/protocol_test.go daemon/subscriber.go daemon/subscriber_test.go daemon/server.go daemon/session.go
git commit -m "feat(daemon): add subscribe_all multi-session streaming"
```

---

### Task 3: Create TUI session state management

**Files:**
- Create: `cmd/codetty/tui_session.go`

- [ ] **Step 1: Create tui_session.go**

```go
package main

import (
	"io"
	"sync"

	"github.com/hinshun/vt10x"
)

// tuiSession represents one daemon session's display state in the TUI.
type tuiSession struct {
	Name      string
	Command   string
	Cols      uint16
	Rows      uint16
	Exited    bool
	ExitCode  int
	CreatedAt string

	term vt10x.Terminal
	mu   sync.Mutex
}

func newTUISession(name, command string, cols, rows uint16) *tuiSession {
	term := vt10x.New(vt10x.WithSize(int(cols), int(rows)), vt10x.WithWriter(io.Discard))
	return &tuiSession{
		Name:    name,
		Command: command,
		Cols:    cols,
		Rows:    rows,
		term:    term,
	}
}

// WriteOutput feeds PTY output into the vt10x terminal.
func (s *tuiSession) WriteOutput(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Write(data)
}

// RenderANSI renders the terminal screen at the given dimensions.
// Returns lines with ANSI color codes and cursor position.
func (s *tuiSession) RenderANSI(w, h int) (lines []string, curX, curY int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Lock()
	defer s.term.Unlock()
	return renderTerminalANSI(s.term, w, h)
}

// SnapshotANSI returns a snapshot for panel rendering.
func (s *tuiSession) SnapshotANSI(w, h int) terminalSnap {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Lock()
	defer s.term.Unlock()
	lines, curX, curY := renderTerminalANSI(s.term, w, h)
	cursor := s.term.Cursor()
	return terminalSnap{
		lines:         lines,
		cursorX:       curX,
		cursorY:       curY,
		cursorVisible: s.term.CursorVisible(),
		wrapEnabled:   cursor.X >= 0, // placeholder
	}
}

type terminalSnap struct {
	lines         []string
	cursorX       int
	cursorY       int
	cursorVisible bool
	wrapEnabled   bool
}

// Resize updates the vt10x terminal size.
func (s *tuiSession) Resize(cols, rows uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Cols = cols
	s.Rows = rows
	s.term.Resize(int(cols), int(rows))
}
```

- [ ] **Step 2: Create renderTerminalANSI helper**

This reuses the hub's rendering logic. Add at bottom of `tui_session.go`:

```go
import (
	"fmt"
	"strings"

	"github.com/mattn/go-runewidth"
)

// vt10x Glyph.Mode flags
const (
	glyphReverse   = 1 << 0
	glyphUnderline = 1 << 1
	glyphBold      = 1 << 2
	glyphItalic    = 1 << 4
)

func renderTerminalANSI(term vt10x.Terminal, w, h int) ([]string, int, int) {
	cols, rows := term.Size()
	cursor := term.Cursor()
	curVisible := term.CursorVisible()

	lastVisibleRow := 0
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			g := term.Cell(x, y)
			if g.Char != 0 && g.Char != ' ' {
				lastVisibleRow = y
				break
			}
		}
	}

	startRow := 0
	contentRows := lastVisibleRow + 1
	if contentRows > h {
		startRow = contentRows - h
	}

	lines := make([]string, h)
	curX, curY := -1, -1

	for displayRow := 0; displayRow < h; displayRow++ {
		srcRow := startRow + displayRow
		if srcRow >= rows {
			lines[displayRow] = strings.Repeat(" ", w)
			continue
		}

		var sb strings.Builder
		visualCol := 0
		for x := 0; x < cols && visualCol < w; x++ {
			g := term.Cell(x, srcRow)
			ch := g.Char
			if ch == 0 {
				ch = ' '
			}
			cw := runewidth.RuneWidth(ch)
			if cw == 0 {
				cw = 1
			}
			if visualCol+cw > w {
				break
			}

			isCursor := curVisible && x == cursor.X && srcRow == cursor.Y
			if isCursor {
				curX = visualCol
				curY = displayRow
			}

			ansiStart, ansiEnd := glyphANSI(g, isCursor)
			sb.WriteString(ansiStart)
			sb.WriteRune(ch)
			sb.WriteString(ansiEnd)
			visualCol += cw
		}
		for visualCol < w {
			sb.WriteByte(' ')
			visualCol++
		}
		lines[displayRow] = sb.String()
	}
	return lines, curX, curY
}

func glyphANSI(g vt10x.Glyph, isCursor bool) (string, string) {
	var start strings.Builder
	needReset := false

	fg := g.FG
	bg := g.BG
	if g.Mode&glyphReverse != 0 || isCursor {
		fg, bg = bg, fg
	}
	if fg != 0 {
		fmt.Fprintf(&start, "\x1b[38;5;%dm", fg)
		needReset = true
	}
	if bg != 0 {
		fmt.Fprintf(&start, "\x1b[48;5;%dm", bg)
		needReset = true
	}
	if g.Mode&glyphBold != 0 {
		start.WriteString("\x1b[1m")
		needReset = true
	}
	if g.Mode&glyphUnderline != 0 {
		start.WriteString("\x1b[4m")
		needReset = true
	}
	if g.Mode&glyphItalic != 0 {
		start.WriteString("\x1b[3m")
		needReset = true
	}

	if needReset {
		return start.String(), "\x1b[0m"
	}
	return "", ""
}
```

- [ ] **Step 3: Build to verify compilation**

Run: `go build ./cmd/codetty/`
Expected: success

- [ ] **Step 4: Commit**

```bash
git add cmd/codetty/tui_session.go
git commit -m "feat(tui): add session display state with vt10x rendering"
```

---

### Task 4: Create TUI daemon connection handler

**Files:**
- Create: `cmd/codetty/tui_conn.go`

- [ ] **Step 1: Create tui_conn.go**

```go
package main

import (
	"encoding/json"
	"net"

	"codetty/daemon"

	tea "github.com/charmbracelet/bubbletea"
)

// daemonConn manages the connection between TUI and daemon.
type daemonConn struct {
	conn    net.Conn
	program *tea.Program
}

// Tea messages from daemon connection
type daemonOutputMsg struct {
	Session string
	Data    []byte
}

type daemonSessionEventMsg struct {
	Event daemon.SessionEvent
}

type daemonDisconnectedMsg struct {
	Err error
}

func newDaemonConn(sockPath string) (*daemonConn, error) {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, err
	}
	return &daemonConn{conn: conn}, nil
}

// Subscribe sends subscribe_all and starts the read loop.
func (dc *daemonConn) Subscribe(p *tea.Program) error {
	dc.program = p

	daemon.WriteFrame(dc.conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{
		Action: daemon.ActionSubscribeAll,
	}))

	go dc.readLoop()
	return nil
}

func (dc *daemonConn) readLoop() {
	for {
		ft, payload, err := daemon.ReadFrame(dc.conn)
		if err != nil {
			dc.program.Send(daemonDisconnectedMsg{Err: err})
			return
		}

		switch ft {
		case daemon.FrameTaggedOutput:
			session, data, err := daemon.DecodeTaggedOutput(payload)
			if err != nil {
				continue
			}
			dc.program.Send(daemonOutputMsg{Session: session, Data: data})

		case daemon.FrameSessionEvent:
			var evt daemon.SessionEvent
			if json.Unmarshal(payload, &evt) == nil {
				dc.program.Send(daemonSessionEventMsg{Event: evt})
			}

		case daemon.FrameResponse:
			// Initial OK response, ignore

		}
	}
}

// SendInput sends tagged input to a session.
func (dc *daemonConn) SendInput(session string, data []byte) {
	daemon.WriteFrame(dc.conn, daemon.FrameTaggedInput, daemon.EncodeTaggedInput(session, data))
}

// SendResize sends resize to all sessions.
func (dc *daemonConn) SendResize(cols, rows uint16) {
	daemon.WriteFrame(dc.conn, daemon.FrameResize, daemon.MarshalJSON(daemon.ResizePayload{
		Cols: cols, Rows: rows,
	}))
}

// CreateSession requests the daemon to create a new session.
func (dc *daemonConn) CreateSession(name string, command []string, cols, rows uint16) {
	daemon.WriteFrame(dc.conn, daemon.FrameCreateSession, daemon.MarshalJSON(daemon.CreateSessionRequest{
		Name: name, Command: command, Cols: cols, Rows: rows,
	}))
}

// KillSession requests the daemon to kill a session.
func (dc *daemonConn) KillSession(name string) {
	daemon.WriteFrame(dc.conn, daemon.FrameKillSession, daemon.MarshalJSON(daemon.KillSessionRequest{
		Name: name,
	}))
}

// Detach sends detach and closes the connection.
func (dc *daemonConn) Detach() {
	daemon.WriteFrame(dc.conn, daemon.FrameDetach, nil)
	dc.conn.Close()
}
```

- [ ] **Step 2: Build to verify**

Run: `go build ./cmd/codetty/`
Expected: success

- [ ] **Step 3: Commit**

```bash
git add cmd/codetty/tui_conn.go
git commit -m "feat(tui): add daemon connection handler with subscribe_all"
```

---

### Task 5: Create Bubble Tea TUI model

**Files:**
- Create: `cmd/codetty/tui.go`
- Create: `cmd/codetty/tui_input.go`

- [ ] **Step 1: Create tui.go with Model struct and View**

```go
package main

import (
	"fmt"
	"strings"

	"codetty/terminal"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	tuiTitleStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("86")).Bold(true)

	tuiStatusStyle = lipgloss.NewStyle().
		Background(lipgloss.Color("235")).
		Foreground(lipgloss.Color("250")).
		Padding(0, 1)

	tuiFocusBorder = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("86"))

	tuiNormalBorder = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240"))
)

type layoutMode int

const (
	modeFocusBig layoutMode = iota
	modeGrid
)

// tuiModel is the Bubble Tea model for the unified codetty TUI.
type tuiModel struct {
	conn     *daemonConn
	sessions []*tuiSession
	order    []string // session names in display order
	selected int
	mode     layoutMode
	prefix   bool // Ctrl+A prefix active
	width    int
	height   int

	// Initial focus target (from new/attach command)
	focusTarget string
	// Auto-create session on start
	autoCreate  []string // command to auto-create
}

func newTUIModel(conn *daemonConn, focusTarget string, autoCreate []string) *tuiModel {
	return &tuiModel{
		conn:        conn,
		mode:        modeFocusBig,
		width:       120,
		height:      40,
		focusTarget: focusTarget,
		autoCreate:  autoCreate,
	}
}

func (m *tuiModel) Init() tea.Cmd {
	return tea.Batch(tea.WindowSize(), tea.EnableMouseCellMotion)
}

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.conn.SendResize(uint16(msg.Width), uint16(msg.Height))

	case daemonOutputMsg:
		if sess := m.sessionByName(msg.Session); sess != nil {
			sess.WriteOutput(msg.Data)
		}

	case daemonSessionEventMsg:
		m.handleSessionEvent(msg.Event)
		return m, tea.ClearScreen

	case daemonDisconnectedMsg:
		return m, tea.Quit

	case tea.KeyMsg:
		return m, m.handleKey(msg)

	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			if idx := m.hitTest(msg.X, msg.Y); idx >= 0 {
				m.selected = idx
			}
		}
	}
	return m, nil
}

func (m *tuiModel) View() string {
	if len(m.sessions) == 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			tuiTitleStyle.Render("⬡ codetty")+"\n\n"+
				"No sessions. Creating one...")
	}

	var content string
	if m.mode == modeFocusBig {
		content = m.focusBigView()
	} else {
		content = m.gridView()
	}
	return content
}

func (m *tuiModel) handleSessionEvent(evt daemon.SessionEvent) {
	switch evt.Type {
	case "snapshot", "created":
		// Add or update session
		sess := m.sessionByName(evt.Session)
		if sess == nil {
			cols, rows := evt.Cols, evt.Rows
			if cols == 0 {
				cols = uint16(m.width)
			}
			if rows == 0 {
				rows = uint16(m.height)
			}
			sess = newTUISession(evt.Session, evt.Info.Command, cols, rows)
			sess.CreatedAt = evt.Info.CreatedAt
			m.sessions = append(m.sessions, sess)
			m.order = append(m.order, evt.Session)
		}
		// Focus on target if specified
		if m.focusTarget != "" && evt.Session == m.focusTarget {
			m.selected = m.indexByName(evt.Session)
			m.focusTarget = ""
		}
	case "exited":
		if sess := m.sessionByName(evt.Session); sess != nil {
			sess.Exited = true
			sess.ExitCode = evt.ExitCode
		}
		// Remove from order after brief display
		m.removeSession(evt.Session)
	}

	// Auto-create if we just subscribed and there are no sessions
	if len(m.sessions) == 0 && m.autoCreate != nil {
		cmd := m.autoCreate
		m.autoCreate = nil
		m.conn.CreateSession("", cmd, uint16(m.width), uint16(m.height))
	}
}

func (m *tuiModel) sessionByName(name string) *tuiSession {
	for _, s := range m.sessions {
		if s.Name == name {
			return s
		}
	}
	return nil
}

func (m *tuiModel) indexByName(name string) int {
	for i, n := range m.order {
		if n == name {
			return i
		}
	}
	return 0
}

func (m *tuiModel) removeSession(name string) {
	for i, s := range m.sessions {
		if s.Name == name {
			m.sessions = append(m.sessions[:i], m.sessions[i+1:]...)
			break
		}
	}
	for i, n := range m.order {
		if n == name {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	if m.selected >= len(m.sessions) {
		m.selected = max(0, len(m.sessions)-1)
	}
}

func (m *tuiModel) orderedSessions() []*tuiSession {
	result := make([]*tuiSession, 0, len(m.order))
	for _, name := range m.order {
		if s := m.sessionByName(name); s != nil {
			result = append(result, s)
		}
	}
	return result
}

func (m *tuiModel) selectedSession() *tuiSession {
	ordered := m.orderedSessions()
	if len(ordered) == 0 || m.selected >= len(ordered) {
		return nil
	}
	return ordered[m.selected]
}
```

- [ ] **Step 2: Create tui_input.go with key handling**

```go
package main

import (
	"codetty/terminal"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *tuiModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	// Ctrl+A prefix handling
	if msg.Type == tea.KeyCtrlA {
		if m.prefix {
			// Double Ctrl+A → send literal Ctrl+A
			m.prefix = false
			m.forwardKey(msg)
			return nil
		}
		m.prefix = true
		return nil
	}

	if m.prefix {
		m.prefix = false
		return m.handlePrefixKey(msg)
	}

	// Grid mode: arrow keys navigate, Enter selects
	if m.mode == modeGrid {
		return m.handleGridKey(msg)
	}

	// Focus mode: forward to session
	m.forwardKey(msg)
	return nil
}

func (m *tuiModel) handlePrefixKey(msg tea.KeyMsg) tea.Cmd {
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
		switch msg.Runes[0] {
		case 'd': // Detach
			m.conn.Detach()
			return tea.Quit
		case 'x': // Kill current session
			if sess := m.selectedSession(); sess != nil {
				m.conn.KillSession(sess.Name)
			}
			return nil
		case 'c': // Create new session
			m.conn.CreateSession("", []string{terminal.DefaultShell()}, uint16(m.width), uint16(m.height))
			return nil
		case 'n': // Next session
			m.selectDelta(1)
			return nil
		case 'p': // Previous session
			m.selectDelta(-1)
			return nil
		case 'g': // Toggle grid
			if m.mode == modeFocusBig {
				m.mode = modeGrid
			} else {
				m.mode = modeFocusBig
			}
			return nil
		case '1', '2', '3', '4', '5', '6', '7', '8', '9':
			idx := int(msg.Runes[0]-'0') - 1
			if idx < len(m.sessions) {
				m.selected = idx
				m.mode = modeFocusBig
			}
			return nil
		}
	}
	return nil
}

func (m *tuiModel) handleGridKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEnter:
		m.mode = modeFocusBig
		return nil
	case tea.KeyLeft:
		m.selectDelta(-1)
	case tea.KeyRight:
		m.selectDelta(1)
	case tea.KeyUp:
		m.selectDelta(-1)
	case tea.KeyDown:
		m.selectDelta(1)
	default:
		// In grid mode, still allow typing to go to focused session
		m.forwardKey(msg)
	}
	return nil
}

func (m *tuiModel) forwardKey(msg tea.KeyMsg) {
	sess := m.selectedSession()
	if sess == nil {
		return
	}
	raw := tuiKeyToBytes(msg)
	if len(raw) > 0 {
		m.conn.SendInput(sess.Name, raw)
	}
}

func (m *tuiModel) selectDelta(delta int) {
	n := len(m.sessions)
	if n == 0 {
		return
	}
	m.selected = ((m.selected + delta) % n + n) % n
}

// tuiKeyToBytes converts a tea.KeyMsg to raw terminal bytes.
func tuiKeyToBytes(msg tea.KeyMsg) []byte {
	switch msg.Type {
	case tea.KeyEnter:
		return []byte{'\r'}
	case tea.KeyTab:
		return []byte{'\t'}
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyEscape:
		return []byte{0x1b}
	case tea.KeyCtrlC:
		return []byte{0x03}
	case tea.KeyCtrlD:
		return []byte{0x04}
	case tea.KeyCtrlZ:
		return []byte{0x1a}
	case tea.KeyCtrlL:
		return []byte{0x0c}
	case tea.KeyCtrlA:
		return []byte{0x01}
	case tea.KeyUp:
		return []byte{0x1b, '[', 'A'}
	case tea.KeyDown:
		return []byte{0x1b, '[', 'B'}
	case tea.KeyRight:
		return []byte{0x1b, '[', 'C'}
	case tea.KeyLeft:
		return []byte{0x1b, '[', 'D'}
	case tea.KeyHome:
		return []byte{0x1b, '[', 'H'}
	case tea.KeyEnd:
		return []byte{0x1b, '[', 'F'}
	case tea.KeyDelete:
		return []byte{0x1b, '[', '3', '~'}
	case tea.KeyPgUp:
		return []byte{0x1b, '[', '5', '~'}
	case tea.KeyPgDown:
		return []byte{0x1b, '[', '6', '~'}
	case tea.KeySpace:
		return []byte{' '}
	case tea.KeyRunes:
		return []byte(string(msg.Runes))
	default:
		// Handle Ctrl+letter
		if msg.Type >= tea.KeyCtrlA && msg.Type <= tea.KeyCtrlZ {
			return []byte{byte(msg.Type)}
		}
		return nil
	}
}
```

- [ ] **Step 3: Build to verify**

Run: `go build ./cmd/codetty/`
Expected: success

- [ ] **Step 4: Commit**

```bash
git add cmd/codetty/tui.go cmd/codetty/tui_input.go
git commit -m "feat(tui): add Bubble Tea model with focus-big/grid layout"
```

---

### Task 6: Add TUI layout rendering (focus-big + grid + panels)

**Files:**
- Create: `cmd/codetty/tui_view.go`

- [ ] **Step 1: Create tui_view.go with focus-big and grid rendering**

```go
package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	tuiMinPanelW = 14
	tuiMinPanelH = 3
)

func (m *tuiModel) focusBigView() string {
	sessions := m.orderedSessions()
	n := len(sessions)
	if n == 0 || m.selected >= n {
		return m.gridView()
	}

	status := m.statusBar()
	statusH := lipgloss.Height(status)
	availableH := m.height - statusH
	if availableH < 1 {
		return status
	}

	if n == 1 {
		sess := sessions[m.selected]
		title := fmt.Sprintf("[1] %s", sess.Name)
		panel := m.renderPanel(sess, title, m.width, availableH, true)
		return lipgloss.JoinVertical(lipgloss.Left, panel, status)
	}

	if m.width >= 120 {
		return lipgloss.JoinVertical(lipgloss.Left, m.focusBigWide(sessions, availableH), status)
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.focusBigNarrow(sessions, availableH), status)
}

func (m *tuiModel) focusBigWide(sessions []*tuiSession, availH int) string {
	primary := sessions[m.selected]
	mainW := m.width * 7 / 10
	sideW := m.width - mainW
	if sideW < tuiMinPanelW {
		sideW = tuiMinPanelW
		mainW = m.width - sideW
	}

	mainTitle := fmt.Sprintf("[%d] %s", m.selected+1, primary.Name)
	mainPanel := m.renderPanel(primary, mainTitle, mainW, availH, true)

	// Side panels
	secondaryCount := len(sessions) - 1
	slotH := availH / secondaryCount
	if slotH < tuiMinPanelH {
		slotH = tuiMinPanelH
	}
	visible := availH / slotH
	if visible > secondaryCount {
		visible = secondaryCount
	}

	var sides []string
	idx := 0
	for i, sess := range sessions {
		if i == m.selected {
			continue
		}
		if idx >= visible {
			break
		}
		h := slotH
		if idx == visible-1 {
			h = availH - slotH*idx // last gets remainder
		}
		title := fmt.Sprintf("[%d] %s", i+1, sess.Name)
		sides = append(sides, m.renderPanel(sess, title, sideW, h, false))
		idx++
	}

	side := lipgloss.JoinVertical(lipgloss.Left, sides...)
	return lipgloss.JoinHorizontal(lipgloss.Top, mainPanel, side)
}

func (m *tuiModel) focusBigNarrow(sessions []*tuiSession, availH int) string {
	primary := sessions[m.selected]
	mainH := availH * 7 / 10
	sideH := availH - mainH

	mainTitle := fmt.Sprintf("[%d] %s", m.selected+1, primary.Name)
	mainPanel := m.renderPanel(primary, mainTitle, m.width, mainH, true)

	// Bottom: show one secondary
	var bottomPanel string
	for i, sess := range sessions {
		if i != m.selected {
			title := fmt.Sprintf("[%d] %s", i+1, sess.Name)
			bottomPanel = m.renderPanel(sess, title, m.width, sideH, false)
			break
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, mainPanel, bottomPanel)
}

func (m *tuiModel) gridView() string {
	sessions := m.orderedSessions()
	n := len(sessions)
	if n == 0 {
		return m.statusBar()
	}

	status := m.statusBar()
	statusH := lipgloss.Height(status)
	availH := m.height - statusH

	cols := gridCols(n, m.width)
	rows := (n + cols - 1) / cols
	panelW := m.width / cols
	panelH := availH / rows
	if panelH < tuiMinPanelH {
		panelH = tuiMinPanelH
	}

	var rowViews []string
	for r := 0; r < rows; r++ {
		var colViews []string
		for c := 0; c < cols; c++ {
			idx := r*cols + c
			if idx >= n {
				colViews = append(colViews, strings.Repeat(" ", panelW))
				continue
			}
			sess := sessions[idx]
			title := fmt.Sprintf("[%d] %s", idx+1, sess.Name)
			focused := idx == m.selected
			if focused {
				title = "▶ " + title
			}
			colViews = append(colViews, m.renderPanel(sess, title, panelW, panelH, focused))
		}
		rowViews = append(rowViews, lipgloss.JoinHorizontal(lipgloss.Top, colViews...))
	}

	grid := lipgloss.JoinVertical(lipgloss.Left, rowViews...)
	return lipgloss.JoinVertical(lipgloss.Left, grid, status)
}

func (m *tuiModel) renderPanel(sess *tuiSession, title string, w, h int, focused bool) string {
	style := tuiNormalBorder.Width(w - 2).Height(h - 2)
	if focused {
		style = tuiFocusBorder.Width(w - 2).Height(h - 2)
	}

	// Truncate title to fit
	maxTitleW := w - 4
	if maxTitleW > 0 && lipgloss.Width(title) > maxTitleW {
		title = ansi.Truncate(title, maxTitleW, "…")
	}

	// Render terminal content
	innerW := w - 2 // border
	innerH := h - 3 // border + title line
	if innerW < 1 || innerH < 1 {
		return style.Render(title)
	}

	lines, _, _ := sess.RenderANSI(innerW, innerH)
	content := strings.Join(lines, "\n")

	header := lipgloss.NewStyle().Bold(focused).Render(title)
	return style.Render(header + "\n" + content)
}

func (m *tuiModel) statusBar() string {
	sess := m.selectedSession()
	var name string
	if sess != nil {
		name = sess.Name
	}
	n := len(m.sessions)

	left := fmt.Sprintf(" [%s] %d/%d", name, m.selected+1, n)
	right := " Ctrl+A ? help "
	mid := strings.Repeat(" ", max(0, m.width-lipgloss.Width(left)-lipgloss.Width(right)))

	return tuiStatusStyle.Width(m.width).Render(left + mid + right)
}

func (m *tuiModel) hitTest(x, y int) int {
	// Simple hit test for grid mode
	if m.mode != modeGrid {
		return -1
	}
	n := len(m.sessions)
	if n == 0 {
		return -1
	}
	cols := gridCols(n, m.width)
	panelW := m.width / cols
	statusH := 1
	availH := m.height - statusH
	rows := (n + cols - 1) / cols
	panelH := availH / rows

	col := x / panelW
	row := y / panelH
	idx := row*cols + col
	if idx >= 0 && idx < n {
		return idx
	}
	return -1
}

func gridCols(n, width int) int {
	if n <= 1 {
		return 1
	}
	if width >= 160 && n >= 4 {
		return 4
	}
	if width >= 120 && n >= 3 {
		return 3
	}
	if n >= 2 {
		return 2
	}
	return 1
}
```

- [ ] **Step 2: Build to verify**

Run: `go build ./cmd/codetty/`
Expected: success

- [ ] **Step 3: Commit**

```bash
git add cmd/codetty/tui_view.go
git commit -m "feat(tui): add focus-big and grid layout rendering"
```

---

### Task 7: Wire TUI into CLI commands and update main.go

**Files:**
- Modify: `cmd/codetty/cli.go`
- Modify: `cmd/codetty/main.go`

- [ ] **Step 1: Add startTUI function to cli.go**

Replace `runAttachLoop` and update `cmdNew` / `cmdAttach` to use TUI. Add at end of `cli.go`:

```go
func startTUI(sockPath string, focusTarget string, autoCreate []string) {
	conn, err := newDaemonConn(sockPath)
	if err != nil {
		log.Fatalf("connect to daemon: %v", err)
	}

	model := newTUIModel(conn, focusTarget, autoCreate)
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())

	if err := conn.Subscribe(p); err != nil {
		log.Fatalf("subscribe: %v", err)
	}

	if _, err := p.Run(); err != nil {
		log.Fatalf("TUI: %v", err)
	}

	fmt.Fprintf(os.Stderr, "[detached]\n")
}
```

- [ ] **Step 2: Update cmdNew to use TUI**

Replace the current `cmdNew` function body (after creating the session via daemon request) with TUI launch. The session creation is now handled inside the TUI via `autoCreate`:

```go
func cmdNew(args []string) {
	fs := flag.NewFlagSet("new", flag.ExitOnError)
	session := fs.String("s", "", "session name")
	noHub := fs.Bool("no-hub", false, "do not connect to hub")
	hubURL := fs.String("hub-url", "", "hub WebSocket URL")
	insecure := fs.Bool("insecure", false, "skip TLS verification for hub")
	certFile := fs.String("cert", "", "client TLS certificate for hub")
	keyFile := fs.String("key", "", "client TLS key for hub")
	caFile := fs.String("ca", "", "CA certificate for hub verification")
	fs.Parse(args)

	command := fs.Args()
	if len(command) == 0 {
		command = []string{terminal.DefaultShell()}
	}

	sockPath := defaultSocketPath()
	if err := ensureDaemon(sockPath); err != nil {
		log.Fatalf("cannot start daemon: %v", err)
	}

	// Create session via single-shot request, then launch TUI
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}

	cols, rows := terminalSize()
	req := daemon.Request{
		Action:      daemon.ActionNewSession,
		Session:     *session,
		Command:     command,
		Cols:        cols,
		Rows:        rows,
		NoHub:       *noHub,
		HubServer:   *hubURL,
		HubInsecure: *insecure,
		HubCertFile: *certFile,
		HubKeyFile:  *keyFile,
		HubCAFile:   *caFile,
	}
	daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(req))
	ft, payload, err := daemon.ReadFrame(conn)
	if err != nil {
		log.Fatalf("read response: %v", err)
	}
	if ft != daemon.FrameResponse {
		log.Fatalf("unexpected frame: %d", ft)
	}
	var resp daemon.Response
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		log.Fatalf("create session: %s", resp.Error)
	}
	conn.Close()

	// Launch TUI focused on new session
	startTUI(sockPath, resp.Session, nil)
}
```

- [ ] **Step 3: Update cmdAttach to use TUI**

```go
func cmdAttach(args []string) {
	fs := flag.NewFlagSet("attach", flag.ExitOnError)
	session := fs.String("s", "", "session name (default: latest)")
	fs.Parse(args)

	sockPath := defaultSocketPath()
	if err := ensureDaemon(sockPath); err != nil {
		log.Fatalf("no daemon running")
	}

	startTUI(sockPath, *session, nil)
}
```

- [ ] **Step 4: Update main.go for bare `codetty` behavior**

Change the `main()` function so bare `codetty` (no args) launches TUI with auto-create:

```go
func main() {
	if len(os.Args) < 2 {
		cmdDefault()
		return
	}
	// ... rest unchanged ...
}

func cmdDefault() {
	sockPath := defaultSocketPath()
	if err := ensureDaemon(sockPath); err != nil {
		log.Fatalf("cannot start daemon: %v", err)
	}
	// Launch TUI — it will auto-create a shell if no sessions exist
	startTUI(sockPath, "", []string{terminal.DefaultShell()})
}
```

- [ ] **Step 5: Remove old runAttachLoop and related dead code**

Remove `runAttachLoop`, `handleNotification`, and the old raw-mode input handling from `cli.go`.

- [ ] **Step 6: Build and verify**

Run: `go build ./cmd/codetty/`
Expected: success

- [ ] **Step 7: Commit**

```bash
git add cmd/codetty/cli.go cmd/codetty/main.go
git commit -m "feat: wire TUI into new/attach/default commands"
```

---

### Task 8: Integration test for TUI subscribe flow

**Files:**
- Modify: `daemon/subscriber_test.go`

- [ ] **Step 1: Add full lifecycle test**

Add to `daemon/subscriber_test.go`:

```go
func TestSubscriber_Detach(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	// Create session to keep server alive
	c1, _ := net.Dial("unix", sockPath)
	WriteFrame(c1, FrameRequest, MarshalJSON(Request{
		Action: ActionNewSession, Session: "bg",
		Command: []string{"sleep", "60"}, Cols: 80, Rows: 24, NoHub: true,
	}))
	ReadFrame(c1)
	WriteFrame(c1, FrameDetach, nil)
	c1.Close()

	time.Sleep(50 * time.Millisecond)

	// Subscribe and detach
	sub, _ := net.Dial("unix", sockPath)
	WriteFrame(sub, FrameRequest, MarshalJSON(Request{Action: ActionSubscribeAll}))
	sub.SetReadDeadline(time.Now().Add(2 * time.Second))

	// Consume until response
	for {
		ft, _, err := ReadFrame(sub)
		if err != nil || ft == FrameResponse {
			break
		}
	}

	// Detach
	WriteFrame(sub, FrameDetach, nil)
	sub.Close()

	// Server should still be running
	time.Sleep(50 * time.Millisecond)
	c2, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal("server stopped after subscriber detach")
	}
	c2.Close()
}

func TestSubscriber_KillSession(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	// Keep server alive with extra session
	c1, _ := net.Dial("unix", sockPath)
	WriteFrame(c1, FrameRequest, MarshalJSON(Request{
		Action: ActionNewSession, Session: "keep",
		Command: []string{"sleep", "60"}, Cols: 80, Rows: 24, NoHub: true,
	}))
	ReadFrame(c1)
	WriteFrame(c1, FrameDetach, nil)
	c1.Close()

	// Subscribe
	sub, _ := net.Dial("unix", sockPath)
	defer sub.Close()
	WriteFrame(sub, FrameRequest, MarshalJSON(Request{Action: ActionSubscribeAll}))
	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	// Drain initial events
	for {
		ft, _, err := ReadFrame(sub)
		if err != nil || ft == FrameResponse {
			break
		}
	}

	// Create session via subscriber
	WriteFrame(sub, FrameCreateSession, MarshalJSON(CreateSessionRequest{
		Name: "tokill", Command: []string{"sleep", "60"}, Cols: 80, Rows: 24,
	}))
	time.Sleep(100 * time.Millisecond)

	// Kill it via subscriber
	WriteFrame(sub, FrameKillSession, MarshalJSON(KillSessionRequest{Name: "tokill"}))
	time.Sleep(300 * time.Millisecond)

	// Should get exited event
	gotExited := false
	for i := 0; i < 10; i++ {
		ft, payload, err := ReadFrame(sub)
		if err != nil {
			break
		}
		if ft == FrameSessionEvent {
			var evt SessionEvent
			json.Unmarshal(payload, &evt)
			if evt.Type == "exited" && evt.Session == "tokill" {
				gotExited = true
				break
			}
		}
	}
	if !gotExited {
		t.Error("did not receive exited event after kill")
	}
}
```

- [ ] **Step 2: Run tests**

Run: `go test ./daemon/ -v -run TestSubscriber -timeout 15s`
Expected: PASS

- [ ] **Step 3: Run full test suite**

Run: `go test ./... -timeout 30s`
Expected: all PASS

- [ ] **Step 4: Commit**

```bash
git add daemon/subscriber_test.go
git commit -m "test(daemon): add subscriber detach and kill tests"
```

---

### Task 9: End-to-end verification and cleanup

**Files:**
- Modify: `cmd/codetty/main.go` (help text update)

- [ ] **Step 1: Update help text**

```go
const usage = `codetty — terminal multiplexer with hub integration

Usage:
  codetty                                      Open TUI (auto-create shell if no sessions)
  codetty [new] [-s name] [--] [command ...]   Create session and open TUI
  codetty attach [-s name]                     Open TUI focused on session (alias: a)
  codetty ls                                   List active sessions
  codetty kill [-s name]                       Kill a session
  codetty kill-server                          Stop the daemon and all sessions
  codetty hub [flags]                          Start the hub WebSocket/TUI server

TUI Keys (Ctrl+A prefix):
  d       Detach (exit TUI, sessions keep running)
  x       Kill focused session
  c       Create new session
  n/p     Next/previous session
  g       Toggle grid overview
  1-9     Jump to session by number

Options:
  -s, --session NAME   Target session name
  --no-hub             Do not connect session to hub
  --hub-url URL        Hub WebSocket URL
  --insecure           Skip TLS verification
  --help, -h           Show this help
`
```

- [ ] **Step 2: Build final binary**

Run: `./build.sh`
Expected: success, single `codetty` binary

- [ ] **Step 3: Run full test suite**

Run: `go test ./... -timeout 30s`
Expected: all PASS

- [ ] **Step 4: Final commit**

```bash
git add cmd/codetty/main.go
git commit -m "docs: update help text for unified TUI"
```

---

## Summary of Execution Order

1. **Task 1**: Protocol extension (tagged frames, subscribe_all)
2. **Task 2**: Server-side subscriber implementation
3. **Task 3**: TUI session state (vt10x rendering)
4. **Task 4**: TUI daemon connection handler
5. **Task 5**: Bubble Tea model + input handling
6. **Task 6**: Layout rendering (focus-big, grid, panels)
7. **Task 7**: Wire into CLI commands (new, attach, default)
8. **Task 8**: Integration tests for subscriber
9. **Task 9**: Help text, build verification, cleanup
