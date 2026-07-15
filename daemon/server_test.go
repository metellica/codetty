//go:build !windows

package daemon

import (
	"encoding/json"
	"net"
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
