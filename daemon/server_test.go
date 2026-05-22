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
		NoHub:   true,
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

func TestServer_ListSessions(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	// Create a session
	conn1, _ := net.Dial("unix", sockPath)
	req := Request{Action: ActionNewSession, Session: "s1", Command: []string{"sleep", "10"}, Cols: 80, Rows: 24, NoHub: true}
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
	req := Request{Action: ActionNewSession, Session: "keep", Command: []string{"sleep", "60"}, Cols: 80, Rows: 24, NoHub: true}
	WriteFrame(conn1, FrameRequest, MarshalJSON(req))
	ReadFrame(conn1)
	WriteFrame(conn1, FrameDetach, nil)
	conn1.Close()

	conn2, _ := net.Dial("unix", sockPath)
	req = Request{Action: ActionNewSession, Session: "killme", Command: []string{"sleep", "60"}, Cols: 80, Rows: 24, NoHub: true}
	WriteFrame(conn2, FrameRequest, MarshalJSON(req))
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
	req := Request{Action: ActionNewSession, Command: []string{"bash"}, Cols: 80, Rows: 24, NoHub: true}
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
