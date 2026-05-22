# Daemon Session (Detach/Attach) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add tmux-like detach/attach to CodeTTY by introducing a daemon server that owns PTY sessions, communicating with thin clients over a Unix socket, while maintaining hub WebSocket connections independently of client state.

**Architecture:** A centralized daemon process (spawned on first use) manages PTY sessions and hub connections. User-facing commands connect to this daemon via a Unix domain socket for terminal I/O. The entire project is merged into a single `codetty` binary with subcommand routing.

**Tech Stack:** Go 1.25, Unix domain sockets (`net` stdlib), `creack/pty`, `hinshun/vt10x`, `gorilla/websocket`, existing `protocol/` and `certs/` packages.

---

## File Structure

```
terminal/                    # NEW: Extracted shared terminal abstractions
  session.go                 # Session interface + startTerminal + defaultShell
  session_unix.go            # Unix PTY implementation (from cmd/codetty-cli/terminal_unix.go)
  session_windows.go         # Windows ConPTY implementation (from cmd/codetty-cli/terminal_windows.go)
  screen.go                  # ScreenMirror (from cmd/codetty-cli/screen_snapshot.go)

daemon/                      # NEW: Daemon server core
  protocol.go                # Local socket frame encode/decode
  protocol_test.go           # Frame round-trip tests
  server.go                  # Server struct, accept loop, lifecycle
  server_test.go             # Server start/stop, accept tests
  session.go                 # Daemon Session (PTY + ScreenMirror + clients)
  session_test.go            # Session create/exit/resize tests
  client.go                  # Client connection: attach, I/O forwarding, detach
  client_test.go             # Client attach/detach/switch tests
  hubconn.go                 # Per-session hub WebSocket management
  hubconn_test.go            # Hub connection tests

cmd/codetty/                 # NEW: Unified binary
  main.go                    # Subcommand routing
  hub.go                     # codetty hub (migrated from cmd/codetty-hub/)
  cli.go                     # new/attach/ls/kill/kill-server subcommand handlers
  server_cmd.go              # __server daemon entry point
  run.go                     # Foreground one-shot mode (current codetty-cli behavior)
  daemonize.go               # Daemon startup/discovery logic
  daemonize_unix.go          # setsid/fork for Unix
  daemonize_windows.go       # detached process for Windows
```

---

## Task 1: Extract `terminal` Package

Extract `terminalSession` interface, platform implementations, and `ScreenMirror` from `cmd/codetty-cli/` into a shared `terminal/` package so both the daemon and legacy `run` mode can use them.

**Files:**
- Create: `terminal/session.go`
- Create: `terminal/session_unix.go`
- Create: `terminal/session_windows.go`
- Create: `terminal/screen.go`
- Create: `terminal/screen_test.go`
- Modify: `cmd/codetty-cli/main.go` (import from `terminal/` instead of local)
- Modify: `cmd/codetty-cli/terminal.go` (replace with thin wrapper or remove)
- Modify: `cmd/codetty-cli/terminal_unix.go` (remove, replaced by terminal/)
- Modify: `cmd/codetty-cli/terminal_windows.go` (remove, replaced by terminal/)
- Modify: `cmd/codetty-cli/screen_snapshot.go` (remove, replaced by terminal/)

- [ ] **Step 1: Create `terminal/session.go`**

```go
package terminal

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
)

var ErrNoCommand = errors.New("no command specified")

// Session abstracts a PTY-backed process.
type Session interface {
	io.ReadWriteCloser
	Resize(cols, rows uint16) error
	Wait() error
}

// Start creates a new PTY session running the given command.
// If args is empty, returns ErrNoCommand.
func Start(args []string, cols, rows uint16) (Session, error) {
	if len(args) == 0 {
		return nil, ErrNoCommand
	}
	return startPlatform(args, cols, rows)
}

// DefaultShell returns the user's default shell command.
func DefaultShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	if runtime.GOOS == "windows" {
		if ps, err := exec.LookPath("powershell.exe"); err == nil {
			return ps
		}
		return "cmd.exe"
	}
	return "/bin/sh"
}
```

- [ ] **Step 2: Create `terminal/session_unix.go`**

```go
//go:build !windows

package terminal

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type unixSession struct {
	file *os.File
	cmd  *exec.Cmd
}

func startPlatform(args []string, cols, rows uint16) (Session, error) {
	cmd := exec.Command(args[0], args[1:]...)
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	return &unixSession{file: file, cmd: cmd}, nil
}

func (t *unixSession) Read(p []byte) (int, error)  { return t.file.Read(p) }
func (t *unixSession) Write(p []byte) (int, error) { return t.file.Write(p) }
func (t *unixSession) Close() error                { return t.file.Close() }

func (t *unixSession) Resize(cols, rows uint16) error {
	return pty.Setsize(t.file, &pty.Winsize{Cols: cols, Rows: rows})
}

func (t *unixSession) Wait() error { return t.cmd.Wait() }
```

- [ ] **Step 3: Create `terminal/session_windows.go`**

Copy from `cmd/codetty-cli/terminal_windows.go`, changing package to `terminal` and type name to match.

- [ ] **Step 4: Create `terminal/screen.go`**

Move `ScreenMirror` from `cmd/codetty-cli/screen_snapshot.go` into `terminal/screen.go`. Export the type and methods. Keep exact logic unchanged.

```go
package terminal

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/hinshun/vt10x"
)

// ScreenMirror maintains a vt10x terminal model for screen replay/snapshot.
type ScreenMirror struct {
	mu      sync.Mutex
	term    vt10x.Terminal
	written bool
}

func NewScreenMirror(cols, rows uint16) *ScreenMirror {
	return &ScreenMirror{
		term: vt10x.New(vt10x.WithSize(int(cols), int(rows)), vt10x.WithWriter(io.Discard)),
	}
}

func (s *ScreenMirror) Write(data []byte) { /* same as existing */ }
func (s *ScreenMirror) Resize(cols, rows uint16) { /* same as existing */ }
func (s *ScreenMirror) Size() (uint16, uint16) { /* same as existing */ }
func (s *ScreenMirror) Snapshot() []byte { /* same as existing */ }
```

- [ ] **Step 5: Write `terminal/screen_test.go`**

```go
package terminal

import "testing"

func TestScreenMirror_WriteAndSnapshot(t *testing.T) {
	sm := NewScreenMirror(80, 24)
	sm.Write([]byte("hello world"))
	snap := sm.Snapshot()
	if len(snap) == 0 {
		t.Fatal("expected non-empty snapshot after write")
	}
	// Snapshot should contain "hello world" somewhere
	if !containsBytes(snap, []byte("hello world")) {
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

func containsBytes(haystack, needle []byte) bool {
	return strings.Contains(string(haystack), string(needle))
}
```

- [ ] **Step 6: Run tests**

Run: `go test ./terminal/ -v`
Expected: PASS

- [ ] **Step 7: Update `cmd/codetty-cli/` to import `terminal/`**

Replace local `terminalSession` with `terminal.Session`, `NewScreenMirror` with `terminal.NewScreenMirror`, etc. Remove old files (`terminal.go`, `terminal_unix.go`, `terminal_windows.go`, `screen_snapshot.go`). Keep `main.go` referencing the new package.

- [ ] **Step 8: Verify existing tests still pass**

Run: `go test ./...`
Expected: All existing tests pass (may need to cross-compile check with `GOOS=windows go test -exec=/bin/true ./...`)

- [ ] **Step 9: Commit**

```bash
git add terminal/ cmd/codetty-cli/
git commit -m "refactor: extract terminal package from cmd/codetty-cli

Move terminalSession interface, platform PTY implementations, and
ScreenMirror into a shared terminal/ package for reuse by the daemon.

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

## Task 2: Daemon Local Protocol

Implement the frame protocol for communication between daemon server and clients over Unix socket.

**Files:**
- Create: `daemon/protocol.go`
- Create: `daemon/protocol_test.go`

- [ ] **Step 1: Write failing tests for frame encode/decode**

```go
package daemon

import (
	"bytes"
	"testing"
)

func TestFrameRoundTrip_Request(t *testing.T) {
	req := Request{
		Action:  ActionNewSession,
		Session: "work",
		Command: []string{"bash"},
		Cols:    80,
		Rows:    24,
	}
	buf := &bytes.Buffer{}
	if err := WriteFrame(buf, FrameRequest, mustMarshalJSON(req)); err != nil {
		t.Fatal(err)
	}
	ft, payload, err := ReadFrame(buf)
	if err != nil {
		t.Fatal(err)
	}
	if ft != FrameRequest {
		t.Fatalf("got frame type %d, want %d", ft, FrameRequest)
	}
	var got Request
	if err := unmarshalJSON(payload, &got); err != nil {
		t.Fatal(err)
	}
	if got.Action != ActionNewSession || got.Session != "work" {
		t.Errorf("request mismatch: %+v", got)
	}
}

func TestFrameRoundTrip_Output(t *testing.T) {
	data := []byte("hello terminal output")
	buf := &bytes.Buffer{}
	if err := WriteFrame(buf, FrameOutput, data); err != nil {
		t.Fatal(err)
	}
	ft, payload, err := ReadFrame(buf)
	if err != nil {
		t.Fatal(err)
	}
	if ft != FrameOutput {
		t.Fatalf("got type %d, want %d", ft, FrameOutput)
	}
	if !bytes.Equal(payload, data) {
		t.Errorf("payload mismatch")
	}
}

func TestFrameRoundTrip_MultipleFrames(t *testing.T) {
	buf := &bytes.Buffer{}
	WriteFrame(buf, FrameInput, []byte("key1"))
	WriteFrame(buf, FrameInput, []byte("key2"))
	WriteFrame(buf, FrameDetach, nil)

	ft1, p1, _ := ReadFrame(buf)
	ft2, p2, _ := ReadFrame(buf)
	ft3, _, _ := ReadFrame(buf)

	if ft1 != FrameInput || string(p1) != "key1" {
		t.Errorf("frame 1 mismatch")
	}
	if ft2 != FrameInput || string(p2) != "key2" {
		t.Errorf("frame 2 mismatch")
	}
	if ft3 != FrameDetach {
		t.Errorf("frame 3 type mismatch: got %d", ft3)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./daemon/ -v -run 'TestFrame'`
Expected: FAIL (package doesn't exist yet)

- [ ] **Step 3: Implement `daemon/protocol.go`**

```go
package daemon

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// FrameType identifies the type of local protocol frame.
type FrameType byte

const (
	FrameRequest      FrameType = 0x01 // C→S: JSON request
	FrameResponse     FrameType = 0x02 // S→C: JSON response
	FrameOutput       FrameType = 0x03 // S→C: raw terminal output
	FrameInput        FrameType = 0x04 // C→S: raw keyboard input
	FrameResize       FrameType = 0x05 // C→S: JSON resize
	FrameDetach       FrameType = 0x06 // C→S: detach (no payload)
	FrameSessionSwitch FrameType = 0x07 // C→S: JSON switch target
	FrameNotification FrameType = 0x08 // S→C: JSON notification
)

// Action identifies the requested operation in a Request frame.
type Action string

const (
	ActionNewSession Action = "new_session"
	ActionAttach     Action = "attach"
	ActionList       Action = "list"
	ActionKill       Action = "kill"
	ActionKillServer Action = "kill_server"
)

// Request is the JSON payload for FrameRequest.
type Request struct {
	Action  Action   `json:"action"`
	Session string   `json:"session,omitempty"`
	Command []string `json:"command,omitempty"`
	Env     []string `json:"env,omitempty"`
	Cols    uint16   `json:"cols,omitempty"`
	Rows    uint16   `json:"rows,omitempty"`
	NoHub   bool     `json:"no_hub,omitempty"`
	// Hub connection config (passed at session creation)
	HubServer   string `json:"hub_server,omitempty"`
	HubCertFile string `json:"hub_cert_file,omitempty"`
	HubKeyFile  string `json:"hub_key_file,omitempty"`
	HubCAFile   string `json:"hub_ca_file,omitempty"`
	HubInsecure bool   `json:"hub_insecure,omitempty"`
}

// Response is the JSON payload for FrameResponse.
type Response struct {
	OK       bool           `json:"ok"`
	Error    string         `json:"error,omitempty"`
	Session  string         `json:"session,omitempty"`
	Sessions []SessionInfo  `json:"sessions,omitempty"`
}

// SessionInfo describes a session in list responses.
type SessionInfo struct {
	Name      string `json:"name"`
	Command   string `json:"command"`
	Attached  int    `json:"attached"`  // number of attached clients
	CreatedAt string `json:"created_at"`
	HubStatus string `json:"hub_status"` // "connected"/"disconnected"/"none"
}

// ResizePayload for FrameResize.
type ResizePayload struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// SwitchPayload for FrameSessionSwitch.
type SwitchPayload struct {
	Target string `json:"target"`
}

// Notification for FrameNotification.
type Notification struct {
	Event    string `json:"event"`
	Session  string `json:"session"`
	ExitCode int    `json:"exit_code,omitempty"`
}

// WriteFrame writes a length-prefixed frame to w.
// Format: [4 bytes big-endian length of type+payload][1 byte type][payload]
func WriteFrame(w io.Writer, ft FrameType, payload []byte) error {
	length := uint32(1 + len(payload)) // type byte + payload
	if err := binary.Write(w, binary.BigEndian, length); err != nil {
		return fmt.Errorf("write frame length: %w", err)
	}
	if _, err := w.Write([]byte{byte(ft)}); err != nil {
		return fmt.Errorf("write frame type: %w", err)
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return fmt.Errorf("write frame payload: %w", err)
		}
	}
	return nil
}

// ReadFrame reads a single frame from r.
func ReadFrame(r io.Reader) (FrameType, []byte, error) {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return 0, nil, err
	}
	if length == 0 {
		return 0, nil, fmt.Errorf("empty frame")
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, fmt.Errorf("read frame body: %w", err)
	}
	return FrameType(buf[0]), buf[1:], nil
}

func mustMarshalJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func unmarshalJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./daemon/ -v -run 'TestFrame'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add daemon/
git commit -m "feat(daemon): add local socket frame protocol

Defines frame types, request/response structures, and length-prefixed
binary encode/decode for daemon-client communication over Unix socket.

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

## Task 3: Daemon Server Core

Implement the server that listens on a Unix socket, accepts client connections, and manages session lifecycle.

**Files:**
- Create: `daemon/server.go`
- Create: `daemon/server_test.go`
- Create: `daemon/session.go`

- [ ] **Step 1: Write failing tests for Server start/stop**

```go
package daemon

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServer_StartAndStop(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	srv := NewServer(sockPath)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// Verify socket exists
	if _, err := os.Stat(sockPath); err != nil {
		t.Fatalf("socket file not created: %v", err)
	}

	// Verify we can connect
	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatalf("cannot connect to server: %v", err)
	}
	conn.Close()

	// Stop should clean up
	srv.Stop()
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Fatal("socket file not cleaned up after stop")
	}
}

func TestServer_NewSession(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	srv := NewServer(sockPath)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	conn, _ := net.Dial("unix", sockPath)
	defer conn.Close()

	// Send new_session request
	req := Request{
		Action:  ActionNewSession,
		Session: "test-session",
		Command: []string{"echo", "hello"},
		Cols:    80,
		Rows:    24,
		NoHub:   true,
	}
	WriteFrame(conn, FrameRequest, mustMarshalJSON(req))

	// Read response
	ft, payload, err := ReadFrame(conn)
	if err != nil {
		t.Fatal(err)
	}
	if ft != FrameResponse {
		t.Fatalf("expected response frame, got %d", ft)
	}
	var resp Response
	unmarshalJSON(payload, &resp)
	if !resp.OK {
		t.Fatalf("new_session failed: %s", resp.Error)
	}
	if resp.Session != "test-session" {
		t.Errorf("got session %q, want %q", resp.Session, "test-session")
	}
}

func TestServer_ListSessions(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	// Create a session first
	conn1, _ := net.Dial("unix", sockPath)
	req := Request{Action: ActionNewSession, Session: "s1", Command: []string{"sleep", "10"}, Cols: 80, Rows: 24, NoHub: true}
	WriteFrame(conn1, FrameRequest, mustMarshalJSON(req))
	ReadFrame(conn1) // consume response
	conn1.Close()

	// List sessions
	conn2, _ := net.Dial("unix", sockPath)
	defer conn2.Close()
	WriteFrame(conn2, FrameRequest, mustMarshalJSON(Request{Action: ActionList}))
	ft, payload, _ := ReadFrame(conn2)
	if ft != FrameResponse {
		t.Fatal("expected response")
	}
	var resp Response
	unmarshalJSON(payload, &resp)
	if !resp.OK || len(resp.Sessions) != 1 {
		t.Fatalf("expected 1 session, got: %+v", resp)
	}
	if resp.Sessions[0].Name != "s1" {
		t.Errorf("session name = %q, want s1", resp.Sessions[0].Name)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./daemon/ -v -run 'TestServer'`
Expected: FAIL

- [ ] **Step 3: Implement `daemon/session.go`**

```go
package daemon

import (
	"sync"
	"time"

	"codetty/terminal"
)

// DaemonSession represents a managed PTY session.
type DaemonSession struct {
	Name      string
	Command   []string
	Terminal  terminal.Session
	Screen    *terminal.ScreenMirror
	CreatedAt time.Time

	mu       sync.Mutex
	clients  []*Client
	exited   bool
	exitCode int
	doneCh   chan struct{}
}

func newDaemonSession(name string, command []string, cols, rows uint16) (*DaemonSession, error) {
	term, err := terminal.Start(command, cols, rows)
	if err != nil {
		return nil, err
	}

	s := &DaemonSession{
		Name:      name,
		Command:   command,
		Terminal:  term,
		Screen:    terminal.NewScreenMirror(cols, rows),
		CreatedAt: time.Now(),
		doneCh:    make(chan struct{}),
	}

	// Read PTY output → screen mirror + all attached clients
	go s.readLoop()
	// Wait for PTY exit
	go s.waitLoop()

	return s, nil
}

func (s *DaemonSession) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := s.Terminal.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			s.Screen.Write(data)
			s.broadcastOutput(data)
		}
		if err != nil {
			return
		}
	}
}

func (s *DaemonSession) waitLoop() {
	err := s.Terminal.Wait()
	s.mu.Lock()
	s.exited = true
	if err != nil {
		// Try to extract exit code
		s.exitCode = 1
	}
	s.mu.Unlock()
	close(s.doneCh)
}

func (s *DaemonSession) broadcastOutput(data []byte) {
	s.mu.Lock()
	clients := append([]*Client(nil), s.clients...)
	s.mu.Unlock()

	for _, c := range clients {
		c.SendOutput(data)
	}
}

func (s *DaemonSession) addClient(c *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients = append(s.clients, c)
}

func (s *DaemonSession) removeClient(c *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, cl := range s.clients {
		if cl == c {
			s.clients = append(s.clients[:i], s.clients[i+1:]...)
			return
		}
	}
}

func (s *DaemonSession) clientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

func (s *DaemonSession) isExited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited
}

// Resize resizes the PTY and screen mirror. Uses minimum of all attached clients.
func (s *DaemonSession) Resize(cols, rows uint16) {
	s.Terminal.Resize(cols, rows)
	s.Screen.Resize(cols, rows)
}

func (s *DaemonSession) Kill() {
	s.Terminal.Close()
}
```

- [ ] **Step 4: Implement `daemon/server.go`**

```go
package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

// Server is the daemon that manages PTY sessions.
type Server struct {
	sockPath string
	listener net.Listener
	sessions map[string]*DaemonSession
	mu       sync.Mutex
	done     chan struct{}
	wg       sync.WaitGroup
}

func NewServer(sockPath string) *Server {
	return &Server{
		sockPath: sockPath,
		sessions: make(map[string]*DaemonSession),
		done:     make(chan struct{}),
	}
}

func (s *Server) Start() error {
	// Remove stale socket
	os.Remove(s.sockPath)

	ln, err := net.Listen("unix", s.sockPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.sockPath, err)
	}
	s.listener = ln

	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

func (s *Server) Stop() {
	close(s.done)
	if s.listener != nil {
		s.listener.Close()
	}
	s.wg.Wait()

	// Kill all sessions
	s.mu.Lock()
	for _, sess := range s.sessions {
		sess.Kill()
	}
	s.mu.Unlock()

	os.Remove(s.sockPath)
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				log.Printf("accept error: %v", err)
				continue
			}
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	ft, payload, err := ReadFrame(conn)
	if err != nil {
		return
	}
	if ft != FrameRequest {
		return
	}

	var req Request
	if err := json.Unmarshal(payload, &req); err != nil {
		s.sendError(conn, "invalid request")
		return
	}

	switch req.Action {
	case ActionNewSession:
		s.handleNewSession(conn, req)
	case ActionAttach:
		s.handleAttach(conn, req)
	case ActionList:
		s.handleList(conn)
	case ActionKill:
		s.handleKill(conn, req)
	case ActionKillServer:
		s.handleKillServer(conn)
	default:
		s.sendError(conn, fmt.Sprintf("unknown action: %s", req.Action))
	}
}

func (s *Server) handleNewSession(conn net.Conn, req Request) {
	name := req.Session
	if name == "" {
		name = s.autoName(req.Command)
	}

	s.mu.Lock()
	if _, exists := s.sessions[name]; exists {
		s.mu.Unlock()
		s.sendError(conn, fmt.Sprintf("session %q already exists", name))
		return
	}
	s.mu.Unlock()

	sess, err := newDaemonSession(name, req.Command, req.Cols, req.Rows)
	if err != nil {
		s.sendError(conn, fmt.Sprintf("start session: %v", err))
		return
	}

	s.mu.Lock()
	s.sessions[name] = sess
	s.mu.Unlock()

	// Watch for session exit
	go s.watchSession(sess)

	// Send success response then enter attach mode
	resp := Response{OK: true, Session: name}
	WriteFrame(conn, FrameResponse, mustMarshalJSON(resp))

	// Attach this client to the new session
	s.runAttachedClient(conn, sess)
}

func (s *Server) handleAttach(conn net.Conn, req Request) {
	s.mu.Lock()
	var sess *DaemonSession
	if req.Session != "" {
		sess = s.sessions[req.Session]
	} else {
		// Find most recently created non-exited session
		var latest time.Time
		for _, ss := range s.sessions {
			if !ss.isExited() && ss.CreatedAt.After(latest) {
				sess = ss
				latest = ss.CreatedAt
			}
		}
	}
	s.mu.Unlock()

	if sess == nil {
		s.sendError(conn, "no session found")
		return
	}

	resp := Response{OK: true, Session: sess.Name}
	WriteFrame(conn, FrameResponse, mustMarshalJSON(resp))
	s.runAttachedClient(conn, sess)
}

func (s *Server) handleList(conn net.Conn) {
	s.mu.Lock()
	infos := make([]SessionInfo, 0, len(s.sessions))
	for _, sess := range s.sessions {
		cmd := ""
		if len(sess.Command) > 0 {
			cmd = sess.Command[0]
		}
		infos = append(infos, SessionInfo{
			Name:      sess.Name,
			Command:   cmd,
			Attached:  sess.clientCount(),
			CreatedAt: sess.CreatedAt.Format(time.RFC3339),
			HubStatus: "none",
		})
	}
	s.mu.Unlock()

	resp := Response{OK: true, Sessions: infos}
	WriteFrame(conn, FrameResponse, mustMarshalJSON(resp))
}

func (s *Server) handleKill(conn net.Conn, req Request) {
	s.mu.Lock()
	sess, exists := s.sessions[req.Session]
	s.mu.Unlock()

	if !exists {
		s.sendError(conn, fmt.Sprintf("session %q not found", req.Session))
		return
	}

	sess.Kill()
	WriteFrame(conn, FrameResponse, mustMarshalJSON(Response{OK: true}))
}

func (s *Server) handleKillServer(conn net.Conn) {
	WriteFrame(conn, FrameResponse, mustMarshalJSON(Response{OK: true}))
	go s.Stop()
}

func (s *Server) runAttachedClient(conn net.Conn, sess *DaemonSession) {
	client := NewClient(conn, sess)
	sess.addClient(client)
	defer sess.removeClient(client)

	// Send screen snapshot
	if snap := sess.Screen.Snapshot(); len(snap) > 0 {
		WriteFrame(conn, FrameOutput, snap)
	}

	// Run client I/O loop (blocks until detach or disconnect)
	client.Run(s)
}

func (s *Server) watchSession(sess *DaemonSession) {
	<-sess.doneCh
	// Notify all attached clients
	notif := Notification{Event: "session_exited", Session: sess.Name, ExitCode: sess.exitCode}
	sess.mu.Lock()
	clients := append([]*Client(nil), sess.clients...)
	sess.mu.Unlock()
	for _, c := range clients {
		c.SendNotification(notif)
	}

	// Remove session
	s.mu.Lock()
	delete(s.sessions, sess.Name)
	remaining := len(s.sessions)
	s.mu.Unlock()

	// Auto-exit if no sessions remain
	if remaining == 0 {
		go s.Stop()
	}
}

func (s *Server) autoName(command []string) string {
	base := "session"
	if len(command) > 0 {
		base = command[0]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; ; i++ {
		name := fmt.Sprintf("%s-%d", base, i)
		if _, exists := s.sessions[name]; !exists {
			return name
		}
	}
}

func (s *Server) sendError(conn net.Conn, msg string) {
	WriteFrame(conn, FrameResponse, mustMarshalJSON(Response{OK: false, Error: msg}))
}

// SessionByName returns a session by name (for client session switching).
func (s *Server) SessionByName(name string) *DaemonSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[name]
}

// SessionNames returns ordered session names.
func (s *Server) SessionNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.sessions))
	for name := range s.sessions {
		names = append(names, name)
	}
	return names
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./daemon/ -v -run 'TestServer' -timeout 10s`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add daemon/
git commit -m "feat(daemon): implement server core and session management

Server listens on Unix socket, handles new_session/attach/list/kill
commands. DaemonSession wraps terminal.Session with ScreenMirror and
broadcasts PTY output to all attached clients.

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

## Task 4: Daemon Client Handler

Implement the client-side connection handler within the daemon server: I/O forwarding, resize, detach, and session switching.

**Files:**
- Create: `daemon/client.go`
- Create: `daemon/client_test.go`

- [ ] **Step 1: Write failing tests**

```go
package daemon

import (
	"bytes"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestClient_AttachAndReceiveOutput(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	// Create session running "echo hello"
	conn, _ := net.Dial("unix", sockPath)
	req := Request{Action: ActionNewSession, Session: "echo-test", Command: []string{"echo", "hello"}, Cols: 80, Rows: 24, NoHub: true}
	WriteFrame(conn, FrameRequest, mustMarshalJSON(req))

	// Read response
	ft, payload, _ := ReadFrame(conn)
	if ft != FrameResponse {
		t.Fatal("expected response")
	}
	var resp Response
	unmarshalJSON(payload, &resp)
	if !resp.OK {
		t.Fatalf("failed: %s", resp.Error)
	}

	// Should receive output containing "hello"
	deadline := time.After(3 * time.Second)
	var allOutput bytes.Buffer
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for output, got: %q", allOutput.String())
		default:
		}
		conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		ft, data, err := ReadFrame(conn)
		if err != nil {
			continue
		}
		if ft == FrameOutput {
			allOutput.Write(data)
			if bytes.Contains(allOutput.Bytes(), []byte("hello")) {
				return // success
			}
		}
		if ft == FrameNotification {
			// Session exited - check output so far
			if bytes.Contains(allOutput.Bytes(), []byte("hello")) {
				return
			}
			t.Fatalf("session exited without expected output, got: %q", allOutput.String())
		}
	}
}

func TestClient_Detach(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	conn, _ := net.Dial("unix", sockPath)
	req := Request{Action: ActionNewSession, Session: "detach-test", Command: []string{"sleep", "10"}, Cols: 80, Rows: 24, NoHub: true}
	WriteFrame(conn, FrameRequest, mustMarshalJSON(req))
	ReadFrame(conn) // response

	// Send detach
	WriteFrame(conn, FrameDetach, nil)
	conn.Close()

	// Session should still exist
	time.Sleep(100 * time.Millisecond)
	conn2, _ := net.Dial("unix", sockPath)
	defer conn2.Close()
	WriteFrame(conn2, FrameRequest, mustMarshalJSON(Request{Action: ActionList}))
	ft, payload, _ := ReadFrame(conn2)
	if ft != FrameResponse {
		t.Fatal("expected response")
	}
	var resp Response
	unmarshalJSON(payload, &resp)
	if len(resp.Sessions) != 1 || resp.Sessions[0].Name != "detach-test" {
		t.Fatalf("session should persist after detach: %+v", resp)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./daemon/ -v -run 'TestClient' -timeout 10s`
Expected: FAIL (Client type not defined)

- [ ] **Step 3: Implement `daemon/client.go`**

```go
package daemon

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"sync"
)

// Client represents an attached terminal client.
type Client struct {
	conn    net.Conn
	session *DaemonSession
	mu      sync.Mutex
	closed  bool
}

func NewClient(conn net.Conn, session *DaemonSession) *Client {
	return &Client{conn: conn, session: session}
}

// SendOutput sends terminal output to the client.
func (c *Client) SendOutput(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	WriteFrame(c.conn, FrameOutput, data)
}

// SendNotification sends a notification to the client.
func (c *Client) SendNotification(n Notification) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	WriteFrame(c.conn, FrameNotification, mustMarshalJSON(n))
}

// Run processes client frames until detach or disconnect.
func (c *Client) Run(srv *Server) {
	defer func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
	}()

	for {
		ft, payload, err := ReadFrame(c.conn)
		if err != nil {
			if err == io.EOF {
				return
			}
			return
		}

		switch ft {
		case FrameInput:
			c.session.Terminal.Write(payload)

		case FrameResize:
			var rp ResizePayload
			if json.Unmarshal(payload, &rp) == nil {
				c.session.Resize(rp.Cols, rp.Rows)
			}

		case FrameDetach:
			return

		case FrameSessionSwitch:
			var sp SwitchPayload
			if json.Unmarshal(payload, &sp) == nil {
				c.switchSession(srv, sp.Target)
			}

		default:
			log.Printf("client: unknown frame type %d", ft)
		}
	}
}

func (c *Client) switchSession(srv *Server, target string) {
	var newSess *DaemonSession

	switch target {
	case "__next", "__prev":
		names := srv.SessionNames()
		if len(names) < 2 {
			return
		}
		idx := -1
		for i, n := range names {
			if n == c.session.Name {
				idx = i
				break
			}
		}
		if idx < 0 {
			return
		}
		if target == "__next" {
			idx = (idx + 1) % len(names)
		} else {
			idx = (idx - 1 + len(names)) % len(names)
		}
		newSess = srv.SessionByName(names[idx])
	default:
		newSess = srv.SessionByName(target)
	}

	if newSess == nil || newSess == c.session {
		return
	}

	// Remove from current session
	c.session.removeClient(c)

	// Switch to new session
	c.session = newSess
	newSess.addClient(c)

	// Send new session's screen snapshot
	if snap := newSess.Screen.Snapshot(); len(snap) > 0 {
		c.SendOutput(snap)
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./daemon/ -v -run 'TestClient' -timeout 10s`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add daemon/client.go daemon/client_test.go
git commit -m "feat(daemon): implement client I/O handler with detach/switch

Client reads frames from attached terminal, forwards input to PTY,
handles resize/detach/session-switch. Broadcasts output to client.

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

## Task 5: Hub Connection per Session

Integrate the existing `connectionLoop` logic so each daemon session can maintain its own hub WebSocket connection independently.

**Files:**
- Create: `daemon/hubconn.go`
- Create: `daemon/hubconn_test.go`
- Modify: `daemon/session.go` (add hub connection field and startup)

- [ ] **Step 1: Write failing test**

```go
package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"codetty/protocol"
)

func TestHubConn_ConnectsAndForwardsOutput(t *testing.T) {
	// Setup a mock hub WebSocket server
	var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	registered := make(chan protocol.RegisterPayload, 1)
	outputReceived := make(chan []byte, 10)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			mt, payload, ok := protocol.ParseType(msg)
			if !ok {
				continue
			}
			switch mt {
			case protocol.MsgRegister:
				rp, _ := protocol.DecodeRegister(payload)
				registered <- rp
			case protocol.MsgOutput:
				outputReceived <- payload
			}
		}
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	hc := NewHubConn(HubConnConfig{
		ServerURL: wsURL,
		ClientID:  "test-session",
		Title:     "bash@test",
		Cols:      80,
		Rows:      24,
		Insecure:  true,
	})
	hc.Start()
	defer hc.Stop()

	// Wait for registration
	select {
	case rp := <-registered:
		if rp.ID != "test-session" {
			t.Errorf("registered ID = %q", rp.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for hub registration")
	}

	// Send output
	hc.SendOutput([]byte("hello hub"))

	select {
	case data := <-outputReceived:
		if string(data) != "hello hub" {
			t.Errorf("got %q", data)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for output")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./daemon/ -v -run 'TestHubConn'`
Expected: FAIL

- [ ] **Step 3: Implement `daemon/hubconn.go`**

```go
package daemon

import (
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"codetty/certs"
	"codetty/protocol"
	"codetty/terminal"
)

// HubConnConfig holds configuration for a per-session hub connection.
type HubConnConfig struct {
	ServerURL string
	ClientID  string
	Title     string
	Cols      uint16
	Rows      uint16
	Insecure  bool
	CertFile  string
	KeyFile   string
	CAFile    string
}

// HubConn manages a WebSocket connection to the hub for one session.
type HubConn struct {
	cfg      HubConnConfig
	screen   *terminal.ScreenMirror
	outputCh chan []byte
	done     chan struct{}
	stopOnce sync.Once
}

func NewHubConn(cfg HubConnConfig) *HubConn {
	return &HubConn{
		cfg:      cfg,
		screen:   terminal.NewScreenMirror(cfg.Cols, cfg.Rows),
		outputCh: make(chan []byte, 256),
		done:     make(chan struct{}),
	}
}

func (h *HubConn) Start() {
	go h.connectionLoop()
}

func (h *HubConn) Stop() {
	h.stopOnce.Do(func() { close(h.done) })
}

func (h *HubConn) SendOutput(data []byte) {
	h.screen.Write(data)
	select {
	case h.outputCh <- data:
	default: // drop if hub not keeping up
	}
}

func (h *HubConn) Resize(cols, rows uint16) {
	h.screen.Resize(cols, rows)
}

func (h *HubConn) connectionLoop() {
	delay := time.Second
	maxDelay := 30 * time.Second

	for {
		err := h.runConnection()
		if err == nil {
			return
		}
		select {
		case <-h.done:
			return
		case <-time.After(delay):
		}
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}

func (h *HubConn) runConnection() error {
	dialer := websocket.DefaultDialer
	if !h.cfg.Insecure && h.cfg.CertFile != "" {
		tlsCfg, err := certs.BuildClientTLSConfig(h.cfg.CertFile, h.cfg.KeyFile, h.cfg.CAFile)
		if err != nil {
			return err
		}
		dialer = &websocket.Dialer{
			TLSClientConfig:  tlsCfg,
			HandshakeTimeout: websocket.DefaultDialer.HandshakeTimeout,
		}
	}

	conn, _, err := dialer.Dial(h.cfg.ServerURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Register
	regMsg, err := protocol.EncodeRegister(protocol.RegisterPayload{
		ID:   h.cfg.ClientID,
		Title: h.cfg.Title,
		Cols: h.cfg.Cols,
		Rows: h.cfg.Rows,
	})
	if err != nil {
		return err
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, regMsg); err != nil {
		return err
	}

	// Send replay snapshot if available
	if snap := h.screen.Snapshot(); len(snap) > 0 {
		conn.WriteMessage(websocket.BinaryMessage, protocol.EncodeReplay(snap))
	}

	// Forward output to hub
	errCh := make(chan error, 2)
	go func() {
		for {
			select {
			case data := <-h.outputCh:
				if err := conn.WriteMessage(websocket.BinaryMessage, protocol.EncodeOutput(data)); err != nil {
					errCh <- err
					return
				}
			case <-h.done:
				errCh <- nil
				return
			}
		}
	}()

	// Read hub messages (input/resize) - for now just drain
	go func() {
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			// Hub input/resize handling will be added when needed
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-h.done:
		return nil
	}
}

// Status returns the current connection status.
func (h *HubConn) Status() string {
	select {
	case <-h.done:
		return "stopped"
	default:
		return "connected" // simplified; real implementation would track state
	}
}
```

- [ ] **Step 4: Run test**

Run: `go test ./daemon/ -v -run 'TestHubConn' -timeout 10s`
Expected: PASS

- [ ] **Step 5: Integrate hub into DaemonSession**

Modify `daemon/session.go` to optionally start a `HubConn` when creating a session (based on `Request.NoHub` and hub config fields). Also update `handleList` in `server.go` to report hub status:

```go
// In newDaemonSession, after creating terminal:
if !noHub && hubCfg.ServerURL != "" {
    s.Hub = NewHubConn(HubConnConfig{
        ServerURL: hubCfg.ServerURL,
        ClientID:  name,
        Title:     fmt.Sprintf("%s@%s", command[0], name),
        Cols:      cols,
        Rows:      rows,
        Insecure:  hubCfg.Insecure,
        CertFile:  hubCfg.CertFile,
        KeyFile:   hubCfg.KeyFile,
        CAFile:    hubCfg.CAFile,
    })
    s.Hub.Start()
}

// In readLoop, after Screen.Write and broadcastOutput:
if s.Hub != nil {
    s.Hub.SendOutput(data)
}

// In handleList, replace HubStatus: "none" with:
hubStatus := "none"
if sess.Hub != nil {
    hubStatus = sess.Hub.Status()
}
// Use hubStatus in SessionInfo
```

- [ ] **Step 6: Run all daemon tests**

Run: `go test ./daemon/ -v -timeout 30s`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add daemon/
git commit -m "feat(daemon): add per-session hub WebSocket connection

HubConn manages a reconnecting WebSocket to the hub for each session.
Output is forwarded to hub regardless of client attach state. Reuses
existing protocol package for register/output/replay framing.

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

## Task 6: Unified Binary - Subcommand Router

Create the new `cmd/codetty/` with subcommand routing. Start with the skeleton and the `__server` entry point.

**Files:**
- Create: `cmd/codetty/main.go`
- Create: `cmd/codetty/server_cmd.go`
- Create: `cmd/codetty/daemonize.go`
- Create: `cmd/codetty/daemonize_unix.go`
- Create: `cmd/codetty/daemonize_windows.go`

- [ ] **Step 1: Create `cmd/codetty/main.go`**

```go
package main

import (
	"fmt"
	"os"
)

func main() {
	subcmd := ""
	if len(os.Args) > 1 {
		subcmd = os.Args[1]
	}

	switch subcmd {
	case "hub":
		if err := runHub(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "codetty hub: %v\n", err)
			os.Exit(1)
		}
	case "new":
		if err := runNew(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "codetty new: %v\n", err)
			os.Exit(1)
		}
	case "attach":
		if err := runAttach(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "codetty attach: %v\n", err)
			os.Exit(1)
		}
	case "ls":
		if err := runList(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "codetty ls: %v\n", err)
			os.Exit(1)
		}
	case "kill":
		if err := runKill(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "codetty kill: %v\n", err)
			os.Exit(1)
		}
	case "kill-server":
		if err := runKillServer(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "codetty kill-server: %v\n", err)
			os.Exit(1)
		}
	case "run":
		if err := runOneShot(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "codetty run: %v\n", err)
			os.Exit(1)
		}
	case "__server":
		if err := runServerDaemon(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "codetty __server: %v\n", err)
			os.Exit(1)
		}
	default:
		// Default: treat as "new" (create session + attach)
		// If first arg starts with "-" or is empty, it's flags for new
		if err := runNew(os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "codetty: %v\n", err)
			os.Exit(1)
		}
	}
}
```

- [ ] **Step 2: Create `cmd/codetty/server_cmd.go`**

```go
package main

import (
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"codetty/daemon"
)

func runServerDaemon(args []string) error {
	sockPath := defaultSocketPath()

	// Ensure directory exists
	os.MkdirAll(filepath.Dir(sockPath), 0700)

	srv := daemon.NewServer(sockPath)
	if err := srv.Start(); err != nil {
		return err
	}

	log.Printf("codetty server listening on %s", sockPath)

	// Write PID file
	pidPath := defaultPIDPath()
	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
	defer os.Remove(pidPath)

	// Ignore SIGHUP (daemon must survive terminal hangup)
	signal.Ignore(syscall.SIGHUP)

	// Wait for SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh

	log.Println("codetty server shutting down")
	srv.Stop()
	return nil
}
```

- [ ] **Step 3: Create `cmd/codetty/daemonize.go`**

```go
package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

func defaultSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codetty", "server.sock")
}

func defaultPIDPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codetty", "server.pid")
}

// ensureServer ensures the daemon server is running. If not, starts it.
// Returns the socket path.
func ensureServer() (string, error) {
	sockPath := defaultSocketPath()

	// Try connecting to existing server
	conn, err := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
	if err == nil {
		conn.Close()
		return sockPath, nil
	}

	// Server not running - start it
	if err := startDaemon(); err != nil {
		return "", fmt.Errorf("start daemon: %w", err)
	}

	// Wait for server to be ready
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		conn, err := net.DialTimeout("unix", sockPath, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return sockPath, nil
		}
	}
	return "", fmt.Errorf("daemon failed to start within 5s")
}

// connectServer connects to the daemon server.
func connectServer() (net.Conn, error) {
	sockPath := defaultSocketPath()
	return net.Dial("unix", sockPath)
}
```

- [ ] **Step 4: Create `cmd/codetty/daemonize_unix.go`**

```go
//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func startDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "__server")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}
```

- [ ] **Step 5: Create `cmd/codetty/daemonize_windows.go`**

```go
//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func startDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "__server")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}
```

- [ ] **Step 6: Verify build**

Run: `go build ./cmd/codetty/`
Expected: Build succeeds (with stub implementations for unimplemented subcommands)

- [ ] **Step 7: Commit**

```bash
git add cmd/codetty/
git commit -m "feat: add unified codetty binary with subcommand routing

Entry point routes to hub/new/attach/ls/kill/kill-server/run/__server.
Daemon discovery logic ensures server is running before client operations.
Platform-specific daemonize implementations for Unix (setsid) and Windows.

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

## Task 7: CLI Subcommands (new, attach, ls, kill)

Implement the client-side subcommands that connect to the daemon server.

**Files:**
- Create: `cmd/codetty/cli.go`

- [ ] **Step 1: Implement `cmd/codetty/cli.go`**

```go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"

	"codetty/daemon"
	"codetty/terminal"

	xterm "golang.org/x/term"
)

func runNew(args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	sessionName := fs.String("s", "", "session name")
	noHub := fs.Bool("no-hub", false, "don't connect to hub")
	hubServer := fs.String("server", "", "hub WebSocket URL")
	hubCertDir := fs.String("cert-dir", "", "TLS certificate directory")
	hubInsecure := fs.Bool("insecure", false, "disable mTLS for hub")
	if err := fs.Parse(args); err != nil {
		return err
	}

	command := fs.Args()
	if len(command) == 0 {
		command = []string{terminal.DefaultShell()}
	}

	sockPath, err := ensureServer()
	if err != nil {
		return err
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("connect to server: %w", err)
	}
	defer conn.Close()

	cols, rows := terminalSize()

	req := daemon.Request{
		Action:      daemon.ActionNewSession,
		Session:     *sessionName,
		Command:     command,
		Cols:        cols,
		Rows:        rows,
		NoHub:       *noHub,
		HubServer:   *hubServer,
		HubInsecure: *hubInsecure,
	}
	if *hubCertDir != "" {
		req.HubCertFile = *hubCertDir + "/client.crt"
		req.HubKeyFile = *hubCertDir + "/client.key"
		req.HubCAFile = *hubCertDir + "/ca.crt"
	}

	daemon.WriteFrame(conn, daemon.FrameRequest, mustMarshal(req))

	ft, payload, err := daemon.ReadFrame(conn)
	if err != nil {
		return err
	}
	if ft != daemon.FrameResponse {
		return fmt.Errorf("unexpected frame type %d", ft)
	}
	var resp daemon.Response
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}

	return attachLoop(conn)
}

func runAttach(args []string) error {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	target := fs.String("t", "", "target session name")
	if err := fs.Parse(args); err != nil {
		return err
	}

	sockPath, err := ensureServer()
	if err != nil {
		return err
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("connect to server: %w", err)
	}
	defer conn.Close()

	cols, rows := terminalSize()
	req := daemon.Request{
		Action:  daemon.ActionAttach,
		Session: *target,
		Cols:    cols,
		Rows:    rows,
	}
	daemon.WriteFrame(conn, daemon.FrameRequest, mustMarshal(req))

	ft, payload, err := daemon.ReadFrame(conn)
	if err != nil {
		return err
	}
	if ft != daemon.FrameResponse {
		return fmt.Errorf("unexpected frame type %d", ft)
	}
	var resp daemon.Response
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}

	fmt.Fprintf(os.Stderr, "[attached to %s]\n", resp.Session)
	return attachLoop(conn)
}

func runList(args []string) error {
	conn, err := connectServer()
	if err != nil {
		return fmt.Errorf("no server running")
	}
	defer conn.Close()

	daemon.WriteFrame(conn, daemon.FrameRequest, mustMarshal(daemon.Request{Action: daemon.ActionList}))

	ft, payload, err := daemon.ReadFrame(conn)
	if err != nil {
		return err
	}
	if ft != daemon.FrameResponse {
		return fmt.Errorf("unexpected response")
	}
	var resp daemon.Response
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}

	if len(resp.Sessions) == 0 {
		fmt.Println("no sessions")
		return nil
	}

	fmt.Printf("%-20s %-12s %-10s %-20s %s\n", "NAME", "COMMAND", "ATTACHED", "CREATED", "HUB")
	for _, s := range resp.Sessions {
		attached := "detached"
		if s.Attached > 0 {
			attached = fmt.Sprintf("%d client(s)", s.Attached)
		}
		fmt.Printf("%-20s %-12s %-10s %-20s %s\n", s.Name, s.Command, attached, s.CreatedAt, s.HubStatus)
	}
	return nil
}

func runKill(args []string) error {
	fs := flag.NewFlagSet("kill", flag.ContinueOnError)
	target := fs.String("t", "", "target session name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *target == "" {
		return fmt.Errorf("usage: codetty kill -t <session>")
	}

	conn, err := connectServer()
	if err != nil {
		return fmt.Errorf("no server running")
	}
	defer conn.Close()

	daemon.WriteFrame(conn, daemon.FrameRequest, mustMarshal(daemon.Request{Action: daemon.ActionKill, Session: *target}))
	ft, payload, _ := daemon.ReadFrame(conn)
	if ft == daemon.FrameResponse {
		var resp daemon.Response
		json.Unmarshal(payload, &resp)
		if !resp.OK {
			return fmt.Errorf("%s", resp.Error)
		}
	}
	fmt.Printf("killed session %q\n", *target)
	return nil
}

func runKillServer(args []string) error {
	conn, err := connectServer()
	if err != nil {
		return fmt.Errorf("no server running")
	}
	defer conn.Close()

	daemon.WriteFrame(conn, daemon.FrameRequest, mustMarshal(daemon.Request{Action: daemon.ActionKillServer}))
	daemon.ReadFrame(conn)
	fmt.Println("server shutdown requested")
	return nil
}

// attachLoop enters raw mode and forwards I/O between local terminal and daemon.
func attachLoop(conn net.Conn) error {
	fd := int(os.Stdin.Fd())
	if xterm.IsTerminal(fd) {
		oldState, err := xterm.MakeRaw(fd)
		if err == nil {
			defer xterm.Restore(fd, oldState)
		}
	}

	// Handle SIGWINCH
	sigWinch := make(chan os.Signal, 1)
	signal.Notify(sigWinch, syscall.SIGWINCH)
	go func() {
		for range sigWinch {
			cols, rows := terminalSize()
			rp := daemon.ResizePayload{Cols: cols, Rows: rows}
			daemon.WriteFrame(conn, daemon.FrameResize, mustMarshal(rp))
		}
	}()

	// Local stdin → server (with prefix key handling)
	go func() {
		buf := make([]byte, 4096)
		var prefixActive bool
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				data := buf[:n]
				if prefixActive {
					prefixActive = false
					if len(data) == 1 {
						switch data[0] {
						case 'd': // detach
							daemon.WriteFrame(conn, daemon.FrameDetach, nil)
							return
						case 'n': // next session
							daemon.WriteFrame(conn, daemon.FrameSessionSwitch, mustMarshal(daemon.SwitchPayload{Target: "__next"}))
							continue
						case 'p': // prev session
							daemon.WriteFrame(conn, daemon.FrameSessionSwitch, mustMarshal(daemon.SwitchPayload{Target: "__prev"}))
							continue
						case 0x01: // literal Ctrl+A
							daemon.WriteFrame(conn, daemon.FrameInput, []byte{0x01})
							continue
						}
					}
					// Not a recognized prefix command - send Ctrl+A + the byte
					daemon.WriteFrame(conn, daemon.FrameInput, append([]byte{0x01}, data...))
					continue
				}
				if len(data) == 1 && data[0] == 0x01 { // Ctrl+A
					prefixActive = true
					continue
				}
				daemon.WriteFrame(conn, daemon.FrameInput, data)
			}
			if err != nil {
				return
			}
		}
	}()

	// Server → local stdout
	for {
		ft, payload, err := daemon.ReadFrame(conn)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		switch ft {
		case daemon.FrameOutput:
			os.Stdout.Write(payload)
		case daemon.FrameNotification:
			var notif daemon.Notification
			json.Unmarshal(payload, &notif)
			if notif.Event == "session_exited" {
				fmt.Fprintf(os.Stderr, "\r\n[session %q exited with code %d]\r\n", notif.Session, notif.ExitCode)
				return nil
			}
		}
	}
}

func terminalSize() (uint16, uint16) {
	w, h, err := xterm.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 80, 24
	}
	return uint16(w), uint16(h)
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./cmd/codetty/`
Expected: Build succeeds

- [ ] **Step 3: Manual smoke test**

Run: `./codetty ls` (should report "no server running" or "no sessions")
Run: `./codetty -s test` (should start daemon, create session, enter shell)
Then: `Ctrl+A d` to detach
Then: `./codetty ls` (should show "test" session as detached)
Then: `./codetty attach -t test` (should reattach)

- [ ] **Step 4: Commit**

```bash
git add cmd/codetty/cli.go
git commit -m "feat: implement CLI subcommands (new/attach/ls/kill)

Client connects to daemon via Unix socket, enters raw mode, and
forwards I/O. Supports Ctrl+A prefix for detach (d), next (n),
prev (p) session switching.

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

## Task 8: Hub Subcommand (Migration)

Migrate the existing `cmd/codetty-hub/` logic into `cmd/codetty/hub.go`.

**Files:**
- Create: `cmd/codetty/hub.go`
- Reference: `cmd/codetty-hub/main.go` (all existing hub logic)

- [ ] **Step 1: Create `cmd/codetty/hub.go`**

Copy the hub startup logic from `cmd/codetty-hub/main.go` into a `runHub(args []string) error` function. This includes:
- `parseHubFlags` (adapted to accept args)
- `buildHubListener`
- TLS cert reloader setup
- HTTP mux with `/ws` handler
- Bubble Tea program startup
- Signal handling (SIGHUP reload, SIGTERM shutdown)
- Init-CA / issue-cert / list-certs / revoke-cert sub-sub-commands

The function signature:
```go
func runHub(args []string) error {
    // Check for cert management sub-sub-commands
    if len(args) > 0 {
        switch args[0] {
        case "init-ca":
            return runInitCA(args[1:])
        case "issue-cert":
            return runIssueCert(args[1:])
        case "list-certs":
            out, err := runListCerts(args[1:])
            if err != nil { return err }
            fmt.Print(out)
            return nil
        case "revoke-cert":
            return runRevokeCert(args[1:])
        }
    }
    // ... rest of hub startup logic from cmd/codetty-hub/main.go
}
```

- [ ] **Step 2: Copy cert management helpers**

Copy `runInitCA`, `runIssueCert`, `runListCerts`, `runRevokeCert` from `cmd/codetty-hub/` to `cmd/codetty/` (these are likely in separate files in the hub cmd directory).

- [ ] **Step 3: Verify build and test**

Run: `go build ./cmd/codetty/ && go test ./...`
Expected: Builds and all tests pass

- [ ] **Step 4: Commit**

```bash
git add cmd/codetty/hub.go cmd/codetty/hub_certs.go
git commit -m "feat: migrate hub logic into unified codetty binary

codetty hub [...] now replaces codetty-hub with identical behavior.
Cert management subcommands preserved under codetty hub init-ca, etc.

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

## Task 9: Legacy Run Mode

Implement `codetty run` which behaves like the current `codetty-cli` (foreground, no daemon, direct hub connection). This preserves backward compatibility for scripts/CI.

**Files:**
- Create: `cmd/codetty/run.go`

- [ ] **Step 1: Implement `cmd/codetty/run.go`**

Adapt the current `cmd/codetty-cli/main.go` logic into `runOneShot(args []string) error`. This is a direct port: parse flags, start terminal, raw mode, connection loop to hub, exit when process exits.

```go
package main

import (
	// ... same imports as current cmd/codetty-cli/main.go
	"codetty/terminal"
)

func runOneShot(args []string) error {
	// Parse the same flags as current codetty-cli
	// Start terminal.Start(command, cols, rows)
	// Enter raw mode
	// Run connectionLoop to hub
	// Wait for process exit
	// This is essentially the current cmd/codetty-cli/main.go logic
	// refactored into a function that returns error
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./cmd/codetty/`
Expected: Build succeeds

- [ ] **Step 3: Smoke test**

Run: `./codetty run --insecure -- echo hello`
Expected: Prints "hello" and exits (same as current codetty-cli behavior, hub connection optional)

- [ ] **Step 4: Commit**

```bash
git add cmd/codetty/run.go
git commit -m "feat: add codetty run for legacy foreground mode

Preserves current codetty-cli behavior: direct PTY + hub connection
in foreground mode without daemon. Suitable for scripts and CI.

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

## Task 10: Integration Tests

Write integration tests that exercise the full daemon lifecycle: create, detach, reattach, session switch, kill.

**Files:**
- Create: `daemon/integration_test.go`

- [ ] **Step 1: Write integration tests**

```go
//go:build !windows

package daemon

import (
	"bytes"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestIntegration_CreateDetachReattach(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	// Create session
	conn1, _ := net.Dial("unix", sockPath)
	req := Request{Action: ActionNewSession, Session: "work", Command: []string{"sh", "-c", "echo MARKER && sleep 30"}, Cols: 80, Rows: 24, NoHub: true}
	WriteFrame(conn1, FrameRequest, mustMarshalJSON(req))
	ft, payload, _ := ReadFrame(conn1)
	if ft != FrameResponse {
		t.Fatal("expected response")
	}
	var resp Response
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		t.Fatalf("create failed: %s", resp.Error)
	}

	// Wait for MARKER output
	deadline := time.After(3 * time.Second)
	var output bytes.Buffer
	for !bytes.Contains(output.Bytes(), []byte("MARKER")) {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for MARKER, got: %q", output.String())
		default:
		}
		conn1.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		ft, data, err := ReadFrame(conn1)
		if err != nil {
			continue
		}
		if ft == FrameOutput {
			output.Write(data)
		}
	}

	// Detach
	WriteFrame(conn1, FrameDetach, nil)
	conn1.Close()

	// Verify session persists
	time.Sleep(100 * time.Millisecond)
	conn2, _ := net.Dial("unix", sockPath)
	WriteFrame(conn2, FrameRequest, mustMarshalJSON(Request{Action: ActionList}))
	ft, payload, _ = ReadFrame(conn2)
	var listResp Response
	json.Unmarshal(payload, &listResp)
	if len(listResp.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(listResp.Sessions))
	}
	if listResp.Sessions[0].Attached != 0 {
		t.Errorf("expected 0 attached, got %d", listResp.Sessions[0].Attached)
	}
	conn2.Close()

	// Reattach
	conn3, _ := net.Dial("unix", sockPath)
	WriteFrame(conn3, FrameRequest, mustMarshalJSON(Request{Action: ActionAttach, Session: "work", Cols: 80, Rows: 24}))
	ft, payload, _ = ReadFrame(conn3)
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		t.Fatalf("attach failed: %s", resp.Error)
	}
	// Should receive screen snapshot
	conn3.SetReadDeadline(time.Now().Add(time.Second))
	ft, data, err := ReadFrame(conn3)
	if err == nil && ft == FrameOutput && len(data) > 0 {
		// Got snapshot - good
	}
	conn3.Close()
}

func TestIntegration_KillSession(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	// Create
	conn, _ := net.Dial("unix", sockPath)
	WriteFrame(conn, FrameRequest, mustMarshalJSON(Request{Action: ActionNewSession, Session: "killme", Command: []string{"sleep", "60"}, Cols: 80, Rows: 24, NoHub: true}))
	ReadFrame(conn)
	WriteFrame(conn, FrameDetach, nil)
	conn.Close()

	time.Sleep(100 * time.Millisecond)

	// Kill
	conn2, _ := net.Dial("unix", sockPath)
	WriteFrame(conn2, FrameRequest, mustMarshalJSON(Request{Action: ActionKill, Session: "killme"}))
	ft, payload, _ := ReadFrame(conn2)
	var resp Response
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		t.Fatalf("kill failed: %s", resp.Error)
	}
	conn2.Close()

	// Verify removed
	time.Sleep(200 * time.Millisecond)
	conn3, _ := net.Dial("unix", sockPath)
	WriteFrame(conn3, FrameRequest, mustMarshalJSON(Request{Action: ActionList}))
	ft, payload, _ = ReadFrame(conn3)
	json.Unmarshal(payload, &resp)
	if len(resp.Sessions) != 0 {
		t.Errorf("expected 0 sessions after kill, got %d", len(resp.Sessions))
	}
	conn3.Close()
}

func TestIntegration_SessionAutoExit(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	// Create short-lived session
	conn, _ := net.Dial("unix", sockPath)
	WriteFrame(conn, FrameRequest, mustMarshalJSON(Request{Action: ActionNewSession, Session: "short", Command: []string{"true"}, Cols: 80, Rows: 24, NoHub: true}))
	ReadFrame(conn) // response

	// Wait for session_exited notification
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for session_exited")
		default:
		}
		conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		ft, payload, err := ReadFrame(conn)
		if err != nil {
			continue
		}
		if ft == FrameNotification {
			var notif Notification
			json.Unmarshal(payload, &notif)
			if notif.Event == "session_exited" && notif.Session == "short" {
				return // success
			}
		}
	}
}
```

- [ ] **Step 2: Run integration tests**

Run: `go test ./daemon/ -v -run 'TestIntegration' -timeout 30s`
Expected: PASS

- [ ] **Step 3: Run full test suite**

Run: `go test ./... -timeout 60s`
Expected: All tests pass

- [ ] **Step 4: Commit**

```bash
git add daemon/integration_test.go
git commit -m "test: add daemon integration tests

Tests full lifecycle: create/detach/reattach, kill, and session
auto-exit on PTY process completion.

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

## Task 11: Update Build Script

Update `build.sh` to build the new unified binary alongside (not replacing) the old ones during development.

**Files:**
- Modify: `build.sh`

- [ ] **Step 1: Add codetty build target**

Add to `build.sh`:
```bash
echo "Building codetty..."
go build -o codetty ./cmd/codetty
```

- [ ] **Step 2: Verify build**

Run: `./build.sh`
Expected: Builds `codetty-hub`, `codetty-cli`, and `codetty` successfully

- [ ] **Step 3: Commit**

```bash
git add build.sh
git commit -m "build: add unified codetty binary to build script

Builds codetty alongside legacy binaries during transition period.

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

## Summary

| Task | Description | Key Files |
|------|-------------|-----------|
| 1 | Extract `terminal` package | `terminal/` |
| 2 | Daemon local protocol | `daemon/protocol.go` |
| 3 | Server core + session management | `daemon/server.go`, `daemon/session.go` |
| 4 | Client handler (attach/detach/switch) | `daemon/client.go` |
| 5 | Per-session hub WebSocket | `daemon/hubconn.go` |
| 6 | Unified binary routing + daemonize | `cmd/codetty/main.go`, `daemonize*.go` |
| 7 | CLI subcommands | `cmd/codetty/cli.go` |
| 8 | Hub migration | `cmd/codetty/hub.go` |
| 9 | Legacy run mode | `cmd/codetty/run.go` |
| 10 | Integration tests | `daemon/integration_test.go` |
| 11 | Build script update | `build.sh` |
