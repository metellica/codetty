//go:build !windows

package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
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

	// Verify we can connect
	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatalf("cannot connect to server: %v", err)
	}
	conn.Close()

	srv.Stop()

	// Verify socket cleaned up
	_, err = net.DialTimeout("unix", sockPath, 100*time.Millisecond)
	if err == nil {
		t.Fatal("should not be able to connect after stop")
	}
}

// Clients racing to auto-start the daemon can spawn several __server
// processes; the instance lock must let exactly one bind the socket, and
// losers must not disturb the winner's socket (previously a losing
// daemon's shutdown unlinked it).
func TestServer_InstanceLockSingleDaemon(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	srv1 := NewServer(sockPath)
	if err := srv1.Start(); err != nil {
		t.Fatal(err)
	}

	srv2 := NewServer(sockPath)
	if err := srv2.Start(); err == nil {
		srv2.Stop()
		t.Fatal("second daemon on the same socket must be refused")
	}

	// The winner's socket must still be intact.
	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatalf("winner socket broken after refused start: %v", err)
	}
	conn.Close()

	// Stop releases the lock so a fresh daemon can start.
	srv1.Stop()
	srv3 := NewServer(sockPath)
	if err := srv3.Start(); err != nil {
		t.Fatalf("start after stop: %v", err)
	}
	srv3.Stop()
}

// The instance lock must be handed across a hot upgrade: the recovered
// server keeps holding it (no window for a racing client to start a
// second daemon) and a concurrent start is still refused.
func TestServer_InstanceLockSurvivesUpgradeState(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	srv1 := NewServer(sockPath)
	if err := srv1.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv1.Stop()

	stateFile, err := srv1.PrepareUpgrade()
	if err != nil {
		t.Fatal(err)
	}
	var state UpgradeState
	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.LockFd <= 0 {
		t.Fatalf("LockFd = %d, want the held lock fd", state.LockFd)
	}

	srv2, err := RecoverFromUpgrade(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if srv2.lock == nil {
		t.Fatal("recovered server does not hold the instance lock")
	}
	// srv1 and srv2 share the same lock fd (in-process stand-in for
	// exec); drop srv2's reference so srv1.Stop doesn't double-close.
	srv2.lock = nil

	other := NewServer(sockPath)
	if err := other.Start(); err == nil {
		other.Stop()
		t.Fatal("lock must still be held after upgrade recovery")
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

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	req := Request{
		Action:  ActionNewSession,
		Session: "test-session",
		Command: []string{"echo", "hello"},
		Cols:    80,
		Rows:    24,
	}
	WriteFrame(conn, FrameRequest, MarshalJSON(req))

	ft, payload, err := ReadFrame(conn)
	if err != nil {
		t.Fatal(err)
	}
	if ft != FrameResponse {
		t.Fatalf("expected response frame, got %d", ft)
	}
	var resp Response
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		t.Fatalf("new_session failed: %s", resp.Error)
	}
	if resp.Session != "test-session" {
		t.Errorf("got session %q, want %q", resp.Session, "test-session")
	}
}

func TestServer_NewSessionUsesDaemonDefaultShell(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	WriteFrame(conn, FrameRequest, MarshalJSON(Request{
		Action:   ActionNewSession,
		Session:  "shell",
		NoAttach: true,
		Cols:     80,
		Rows:     24,
	}))
	_, payload, err := ReadFrame(conn)
	if err != nil {
		t.Fatal(err)
	}
	var resp Response
	if err := json.Unmarshal(payload, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("new_session failed: %s", resp.Error)
	}
	session := srv.SessionByName("shell")
	if session == nil || len(session.Command) == 0 {
		t.Fatalf("session command = %v, want daemon default shell", session)
	}
}

func TestCreateSessionForSubscriberSetsFirstTabName(t *testing.T) {
	srv := NewServer(filepath.Join(t.TempDir(), "test.sock"))
	sub := newSubscriber(nil)
	srv.createSessionForSubscriber(sub, CreateSessionRequest{
		Name:    "configured",
		Command: []string{"cat"},
		TabName: "agent",
		Cols:    80,
		Rows:    24,
	})
	session := srv.SessionByName("configured")
	if session == nil {
		t.Fatal("session was not created")
	}
	defer session.Kill()
	if len(session.Tabs) != 1 || session.Tabs[0].Name != "agent" {
		t.Fatalf("tabs = %+v, want first tab named agent", session.Tabs)
	}
}

func TestServer_ListSessions(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	// Create a session
	conn1, _ := net.Dial("unix", sockPath)
	req := Request{Action: ActionNewSession, Session: "s1", Command: []string{"cat"}, Cols: 80, Rows: 24}
	WriteFrame(conn1, FrameRequest, MarshalJSON(req))
	ReadFrame(conn1) // consume response

	// Detach
	WriteFrame(conn1, FrameDetach, nil)
	conn1.Close()

	time.Sleep(50 * time.Millisecond)

	// List sessions
	conn2, _ := net.Dial("unix", sockPath)
	defer conn2.Close()
	WriteFrame(conn2, FrameRequest, MarshalJSON(Request{Action: ActionList}))
	ft, payload, _ := ReadFrame(conn2)
	if ft != FrameResponse {
		t.Fatal("expected response")
	}
	var resp Response
	json.Unmarshal(payload, &resp)
	if !resp.OK || len(resp.Sessions) != 1 {
		t.Fatalf("expected 1 session, got: %+v", resp)
	}
	if resp.Sessions[0].Name != "s1" {
		t.Errorf("session name = %q, want s1", resp.Sessions[0].Name)
	}
}

func TestServer_KillSession(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	// Create two sessions so server doesn't auto-exit when we kill one
	conn1, _ := net.Dial("unix", sockPath)
	req := Request{Action: ActionNewSession, Session: "killme", Command: []string{"cat"}, Cols: 80, Rows: 24}
	WriteFrame(conn1, FrameRequest, MarshalJSON(req))
	ReadFrame(conn1)
	WriteFrame(conn1, FrameDetach, nil)
	conn1.Close()

	conn2, _ := net.Dial("unix", sockPath)
	req2 := Request{Action: ActionNewSession, Session: "keep", Command: []string{"cat"}, Cols: 80, Rows: 24}
	WriteFrame(conn2, FrameRequest, MarshalJSON(req2))
	ReadFrame(conn2)
	WriteFrame(conn2, FrameDetach, nil)
	conn2.Close()

	time.Sleep(50 * time.Millisecond)

	// Kill one session
	conn3, _ := net.Dial("unix", sockPath)
	WriteFrame(conn3, FrameRequest, MarshalJSON(Request{Action: ActionKill, Session: "killme"}))
	ft, payload, _ := ReadFrame(conn3)
	var resp Response
	json.Unmarshal(payload, &resp)
	if ft != FrameResponse || !resp.OK {
		t.Fatalf("kill failed: %+v", resp)
	}
	conn3.Close()

	time.Sleep(300 * time.Millisecond)

	// Verify only 'keep' remains
	conn4, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("cannot connect after kill: %v", err)
	}
	defer conn4.Close()
	WriteFrame(conn4, FrameRequest, MarshalJSON(Request{Action: ActionList}))
	ft, payload, _ = ReadFrame(conn4)
	json.Unmarshal(payload, &resp)
	if len(resp.Sessions) != 1 {
		t.Fatalf("expected 1 session after kill, got %d", len(resp.Sessions))
	}
	if resp.Sessions[0].Name != "keep" {
		t.Errorf("remaining session = %q, want 'keep'", resp.Sessions[0].Name)
	}
}

func TestServer_AutoSessionName(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	// Create without explicit name
	conn, _ := net.Dial("unix", sockPath)
	defer conn.Close()
	req := Request{Action: ActionNewSession, Command: []string{"bash"}, Cols: 80, Rows: 24}
	WriteFrame(conn, FrameRequest, MarshalJSON(req))
	ft, payload, _ := ReadFrame(conn)
	if ft != FrameResponse {
		t.Fatal("expected response")
	}
	var resp Response
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		t.Fatalf("failed: %s", resp.Error)
	}
	if resp.Session != "bash-0" {
		t.Errorf("auto name = %q, want bash-0", resp.Session)
	}
}

func TestServer_CreateTab(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// Create a session first
	conn1, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	req := Request{
		Action:  ActionNewSession,
		Session: "tabtest",
		Command: []string{"cat"},
		Cols:    80,
		Rows:    24,
		TabName: "main",
	}
	WriteFrame(conn1, FrameRequest, MarshalJSON(req))
	ft, payload, err := ReadFrame(conn1)
	if err != nil {
		t.Fatal(err)
	}
	if ft != FrameResponse {
		t.Fatalf("expected response frame, got %d", ft)
	}
	var resp Response
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		t.Fatalf("new_session failed: %s", resp.Error)
	}
	WriteFrame(conn1, FrameDetach, nil)
	conn1.Close()
	time.Sleep(50 * time.Millisecond)

	// Create a tab
	conn2, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	tabReq := Request{
		Action:  ActionCreateTab,
		Session: "tabtest",
		TabName: "secondary",
		Command: []string{"cat"},
	}
	WriteFrame(conn2, FrameRequest, MarshalJSON(tabReq))
	ft2, payload2, err := ReadFrame(conn2)
	if err != nil {
		t.Fatal(err)
	}
	if ft2 != FrameResponse {
		t.Fatalf("expected response frame, got %d", ft2)
	}
	var resp2 Response
	json.Unmarshal(payload2, &resp2)
	if !resp2.OK {
		t.Fatalf("create_tab failed: %s", resp2.Error)
	}
	conn2.Close()

	// Verify via list
	conn3, _ := net.Dial("unix", sockPath)
	defer conn3.Close()
	WriteFrame(conn3, FrameRequest, MarshalJSON(Request{Action: ActionList}))
	_, payload3, _ := ReadFrame(conn3)
	var resp3 Response
	json.Unmarshal(payload3, &resp3)
	if len(resp3.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(resp3.Sessions))
	}
	sess := resp3.Sessions[0]
	if len(sess.Tabs) != 2 {
		t.Fatalf("expected 2 tabs, got %d", len(sess.Tabs))
	}
	if sess.Tabs[0].Name != "main" {
		t.Errorf("tab[0].Name = %q, want main", sess.Tabs[0].Name)
	}
	if sess.Tabs[1].Name != "secondary" {
		t.Errorf("tab[1].Name = %q, want secondary", sess.Tabs[1].Name)
	}
}

// send_keys writes bytes to the targeted tab's PTY; capture returns the
// tab's screen text — together the tmux send-keys/capture-pane analog.
func TestServer_SendKeysAndCapture(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	conn, _ := net.Dial("unix", sockPath)
	WriteFrame(conn, FrameRequest, MarshalJSON(Request{
		Action: ActionNewSession, Session: "sk", Command: []string{"cat"},
		Cols: 80, Rows: 24, NoAttach: true,
	}))
	ReadFrame(conn)
	conn.Close()

	send := func(req Request) Response {
		t.Helper()
		c, err := net.Dial("unix", sockPath)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		WriteFrame(c, FrameRequest, MarshalJSON(req))
		ft, payload, err := ReadFrame(c)
		if err != nil || ft != FrameResponse {
			t.Fatalf("response: ft=%d err=%v", ft, err)
		}
		var resp Response
		json.Unmarshal(payload, &resp)
		return resp
	}

	if resp := send(Request{Action: ActionSendKeys, Session: "sk", Data: []byte("hello\r")}); !resp.OK {
		t.Fatalf("send_keys: %s", resp.Error)
	}

	// cat echoes the line; poll the screen until it shows up.
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp := send(Request{Action: ActionCapture, Session: "sk"})
		if !resp.OK {
			t.Fatalf("capture: %s", resp.Error)
		}
		if strings.Contains(resp.Text, "hello") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("screen never showed sent keys; text=%q", resp.Text)
		}
		time.Sleep(30 * time.Millisecond)
	}

	// Errors: bad session, bad tab ref.
	if resp := send(Request{Action: ActionSendKeys, Session: "nope", Data: []byte("x")}); resp.OK {
		t.Fatal("send_keys to missing session must fail")
	}
	if resp := send(Request{Action: ActionCapture, Session: "sk", Tab: "9"}); resp.OK {
		t.Fatal("capture of out-of-range tab must fail")
	}
}

// The subscribe ACK must carry a stable daemon identity so clients can
// deduplicate multiple endpoints reaching the same daemon; it must also
// survive hot upgrade (clients would otherwise treat the upgraded daemon
// as a new one).
func TestServer_DaemonIDOnSubscribeAndUpgrade(t *testing.T) {
	dir := t.TempDir()
	srv := NewServer(filepath.Join(dir, "test.sock"))
	if srv.daemonID == "" {
		t.Fatal("daemonID must be set at construction")
	}
	srv.Start()
	defer srv.Stop()

	subscribe := func() string {
		t.Helper()
		conn, err := net.Dial("unix", srv.sockPath)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		WriteFrame(conn, FrameRequest, MarshalJSON(Request{Action: ActionSubscribeAll}))
		for {
			ft, payload, err := ReadFrame(conn)
			if err != nil {
				t.Fatal(err)
			}
			if ft != FrameResponse {
				continue
			}
			var resp Response
			json.Unmarshal(payload, &resp)
			if !resp.OK {
				t.Fatalf("subscribe failed: %s", resp.Error)
			}
			return resp.DaemonID
		}
	}

	if got := subscribe(); got != srv.daemonID {
		t.Fatalf("ACK daemon_id = %q, want %q", got, srv.daemonID)
	}

	stateFile, err := srv.PrepareUpgrade()
	if err != nil {
		t.Fatal(err)
	}
	srv2, err := RecoverFromUpgrade(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	srv2.lock = nil // shared with srv in-process; avoid double release
	if srv2.daemonID != srv.daemonID {
		t.Fatalf("daemonID after upgrade = %q, want %q", srv2.daemonID, srv.daemonID)
	}
}

// send_mouse must synthesize an event only when the tab's application has
// enabled mouse reporting, encoded with the protocol it selected (SGR here);
// otherwise the request must fail rather than write garbage bytes.
func TestServer_SendMouse(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	send := func(req Request) Response {
		t.Helper()
		c, err := net.Dial("unix", sockPath)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		WriteFrame(c, FrameRequest, MarshalJSON(req))
		ft, payload, err := ReadFrame(c)
		if err != nil || ft != FrameResponse {
			t.Fatalf("response: ft=%d err=%v", ft, err)
		}
		var resp Response
		json.Unmarshal(payload, &resp)
		return resp
	}

	// The session enables SGR mouse reporting, then echoes its input with
	// control bytes made visible so the capture can assert on them.
	send(Request{
		Action: ActionNewSession, Session: "sm",
		Command: []string{"sh", "-c", `printf '\033[?1000h\033[?1006hREADY\n'; cat -v`},
		Cols:    80, Rows: 24, NoAttach: true,
	})

	// Wait until the DECSET reached the screen mirror.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if resp := send(Request{Action: ActionCapture, Session: "sm"}); strings.Contains(resp.Text, "READY") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("session never became ready")
		}
		time.Sleep(30 * time.Millisecond)
	}

	if resp := send(Request{Action: ActionSendMouse, Session: "sm", Mouse: "click1", MouseX: 5, MouseY: 3}); !resp.OK {
		t.Fatalf("send_mouse: %s", resp.Error)
	}

	// cat -v renders ESC as ^[ ; expect the SGR press+release sequences.
	deadline = time.Now().Add(3 * time.Second)
	for {
		resp := send(Request{Action: ActionCapture, Session: "sm"})
		if strings.Contains(resp.Text, "^[[<0;5;3M^[[<0;5;3m") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("mouse event never echoed; text=%q", resp.Text)
		}
		time.Sleep(30 * time.Millisecond)
	}

	// A tab without mouse reporting refuses synthetic events.
	send(Request{
		Action: ActionNewSession, Session: "plain", Command: []string{"cat"},
		Cols: 80, Rows: 24, NoAttach: true,
	})
	resp := send(Request{Action: ActionSendMouse, Session: "plain", Mouse: "click1", MouseX: 1, MouseY: 1})
	if resp.OK || !strings.Contains(resp.Error, "mouse reporting not enabled") {
		t.Fatalf("send_mouse without mouse mode: ok=%v err=%q", resp.OK, resp.Error)
	}
	if resp := send(Request{Action: ActionSendMouse, Session: "sm", Mouse: "bogus", MouseX: 1, MouseY: 1}); resp.OK {
		t.Fatal("unknown mouse event must fail")
	}
}
