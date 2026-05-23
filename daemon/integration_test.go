//go:build !windows

package daemon_test

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"codetty/daemon"
)

// TestIntegration_FullLifecycle tests:
// daemon start → new session → receive output → detach → reattach → kill → stop
func TestIntegration_FullLifecycle(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := daemon.NewServer(sockPath)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// 1. Create a new session running "echo hello && sleep 60"
	conn1, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}

	req := daemon.Request{
		Action:  daemon.ActionNewSession,
		Session: "integ-1",
		Command: []string{"sh", "-c", "echo hello && sleep 60"},
		Cols:    80,
		Rows:    24,
	}
	daemon.WriteFrame(conn1, daemon.FrameRequest, daemon.MarshalJSON(req))

	ft, payload, err := daemon.ReadFrame(conn1)
	if err != nil {
		t.Fatal(err)
	}
	if ft != daemon.FrameResponse {
		t.Fatalf("expected response, got %d", ft)
	}
	var resp daemon.Response
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		t.Fatalf("new session failed: %s", resp.Error)
	}
	if resp.Session != "integ-1" {
		t.Fatalf("session = %q, want integ-1", resp.Session)
	}

	// 2. Receive output
	gotHello := false
	conn1.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i := 0; i < 20; i++ {
		ft, payload, err = daemon.ReadFrame(conn1)
		if err != nil {
			break
		}
		if ft == daemon.FrameOutput && containsStr(payload, "hello") {
			gotHello = true
			break
		}
	}
	if !gotHello {
		t.Fatal("did not receive 'hello' output")
	}

	// 3. Detach
	daemon.WriteFrame(conn1, daemon.FrameDetach, nil)
	conn1.Close()
	time.Sleep(50 * time.Millisecond)

	// 4. Verify session still running via list
	conn2, _ := net.Dial("unix", sockPath)
	daemon.WriteFrame(conn2, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{Action: daemon.ActionList}))
	ft, payload, _ = daemon.ReadFrame(conn2)
	json.Unmarshal(payload, &resp)
	if len(resp.Sessions) != 1 || resp.Sessions[0].Name != "integ-1" {
		t.Fatalf("expected 1 session after detach, got: %+v", resp.Sessions)
	}
	if resp.Sessions[0].Attached != 0 {
		t.Errorf("expected 0 clients after detach, got %d", resp.Sessions[0].Attached)
	}
	conn2.Close()

	// 5. Reattach
	conn3, _ := net.Dial("unix", sockPath)
	daemon.WriteFrame(conn3, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{
		Action:  daemon.ActionAttach,
		Session: "integ-1",
		Cols:    80,
		Rows:    24,
	}))
	ft, payload, _ = daemon.ReadFrame(conn3)
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		t.Fatalf("reattach failed: %s", resp.Error)
	}

	// Should get snapshot replay (contains "hello" from earlier)
	conn3.SetReadDeadline(time.Now().Add(time.Second))
	gotReplay := false
	for i := 0; i < 5; i++ {
		ft, payload, err = daemon.ReadFrame(conn3)
		if err != nil {
			break
		}
		if ft == daemon.FrameOutput && containsStr(payload, "hello") {
			gotReplay = true
			break
		}
	}
	if !gotReplay {
		t.Error("did not receive replay snapshot on reattach")
	}

	// Detach again
	daemon.WriteFrame(conn3, daemon.FrameDetach, nil)
	conn3.Close()
	time.Sleep(50 * time.Millisecond)

	// 6. Kill the session
	conn4, _ := net.Dial("unix", sockPath)
	daemon.WriteFrame(conn4, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{
		Action:  daemon.ActionKill,
		Session: "integ-1",
	}))
	ft, payload, _ = daemon.ReadFrame(conn4)
	json.Unmarshal(payload, &resp)
	if !resp.OK {
		t.Fatalf("kill failed: %s", resp.Error)
	}
	conn4.Close()
}

// TestIntegration_SessionSwitch tests switching between sessions.
func TestIntegration_SessionSwitch(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := daemon.NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	// Create two sessions
	createSession := func(name string) {
		conn, _ := net.Dial("unix", sockPath)
		daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{
			Action:  daemon.ActionNewSession,
			Session: name,
			Command: []string{"sleep", "60"},
			Cols:    80,
			Rows:    24,
		}))
		daemon.ReadFrame(conn) // response
		daemon.WriteFrame(conn, daemon.FrameDetach, nil)
		conn.Close()
	}
	createSession("sw-a")
	createSession("sw-b")
	time.Sleep(50 * time.Millisecond)

	// Connect to sw-a
	conn, _ := net.Dial("unix", sockPath)
	defer conn.Close()
	daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{
		Action:  daemon.ActionAttach,
		Session: "sw-a",
		Cols:    80,
		Rows:    24,
	}))
	daemon.ReadFrame(conn) // response

	// Switch to next (should be sw-b)
	daemon.WriteFrame(conn, daemon.FrameSessionSwitch, daemon.MarshalJSON(daemon.SwitchPayload{Target: "__next"}))
	time.Sleep(100 * time.Millisecond)

	// Verify via list
	conn2, _ := net.Dial("unix", sockPath)
	defer conn2.Close()
	daemon.WriteFrame(conn2, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{Action: daemon.ActionList}))
	_, payload, _ := daemon.ReadFrame(conn2)
	var resp daemon.Response
	json.Unmarshal(payload, &resp)
	for _, s := range resp.Sessions {
		if s.Name == "sw-b" && s.Attached != 1 {
			t.Errorf("sw-b should have 1 client, got %d", s.Attached)
		}
		if s.Name == "sw-a" && s.Attached != 0 {
			t.Errorf("sw-a should have 0 clients, got %d", s.Attached)
		}
	}
}

// TestIntegration_SessionExitNotification tests that clients get notified
// when a session's process exits.
func TestIntegration_SessionExitNotification(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := daemon.NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	conn, _ := net.Dial("unix", sockPath)
	defer conn.Close()
	daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{
		Action:  daemon.ActionNewSession,
		Session: "short",
		Command: []string{"echo", "bye"},
		Cols:    80,
		Rows:    24,
	}))
	daemon.ReadFrame(conn) // response

	// Wait for exit notification
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	gotNotif := false
	for i := 0; i < 20; i++ {
		ft, payload, err := daemon.ReadFrame(conn)
		if err != nil {
			break
		}
		if ft == daemon.FrameNotification {
			var n daemon.Notification
			json.Unmarshal(payload, &n)
			if n.Event == "session_exited" && n.Session == "short" {
				gotNotif = true
				break
			}
		}
	}
	if !gotNotif {
		t.Error("did not receive session_exited notification")
	}
}

// TestIntegration_KillServer tests that kill-server stops the daemon.
func TestIntegration_KillServer(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := daemon.NewServer(sockPath)
	srv.Start()

	conn, _ := net.Dial("unix", sockPath)
	daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{Action: daemon.ActionKillServer}))
	daemon.ReadFrame(conn)
	conn.Close()

	time.Sleep(200 * time.Millisecond)

	// Should not be able to connect
	_, err := net.DialTimeout("unix", sockPath, 100*time.Millisecond)
	if err == nil {
		t.Error("server should be stopped after kill-server")
	}
}

func containsStr(data []byte, s string) bool {
	return len(data) > 0 && len(s) > 0 && bytesContainsStr(data, []byte(s))
}

func bytesContainsStr(data, sub []byte) bool {
	for i := range data {
		if i+len(sub) > len(data) {
			return false
		}
		match := true
		for j := range sub {
			if data[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
