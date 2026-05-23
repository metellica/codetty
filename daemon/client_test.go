//go:build !windows

package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestClient_ReceiveOutput(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	// Create a session that produces output
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	req := Request{
		Action:  ActionNewSession,
		Session: "echo-sess",
		Command: []string{"echo", "hello world"},
		Cols:    80,
		Rows:    24,
	}
	WriteFrame(conn, FrameRequest, MarshalJSON(req))

	// Read response
	ft, _, err := ReadFrame(conn)
	if err != nil {
		t.Fatal(err)
	}
	if ft != FrameResponse {
		t.Fatalf("expected response, got %d", ft)
	}

	// Should receive output frames
	gotOutput := false
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		ft, payload, err := ReadFrame(conn)
		if err != nil {
			break
		}
		if ft == FrameOutput && len(payload) > 0 {
			gotOutput = true
			break
		}
		if ft == FrameNotification {
			// session exited notification
			break
		}
	}
	if !gotOutput {
		t.Error("did not receive output from session")
	}
}

func TestClient_Detach(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	conn, _ := net.Dial("unix", sockPath)
	defer conn.Close()

	// Create session
	req := Request{Action: ActionNewSession, Session: "detach-sess", Command: []string{"cat"}, Cols: 80, Rows: 24}
	WriteFrame(conn, FrameRequest, MarshalJSON(req))
	ReadFrame(conn) // consume response

	// Send detach
	WriteFrame(conn, FrameDetach, nil)

	// After detach, the server should close our client loop
	// We can verify by trying to list sessions from a new connection
	time.Sleep(50 * time.Millisecond)

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
		t.Fatalf("expected 1 session, got %+v", resp)
	}
	if resp.Sessions[0].Attached != 0 {
		t.Errorf("expected 0 clients after detach, got %d", resp.Sessions[0].Attached)
	}
}

func TestClient_Input(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	conn, _ := net.Dial("unix", sockPath)
	defer conn.Close()

	// Use cat which echoes input back
	req := Request{Action: ActionNewSession, Session: "input-sess", Command: []string{"cat"}, Cols: 80, Rows: 24}
	WriteFrame(conn, FrameRequest, MarshalJSON(req))
	ReadFrame(conn) // response

	// Send input
	WriteFrame(conn, FrameInput, []byte("test-input\n"))

	// Should get echoed back
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	gotEcho := false
	for i := 0; i < 10; i++ {
		ft, payload, err := ReadFrame(conn)
		if err != nil {
			break
		}
		if ft == FrameOutput && contains(payload, "test-input") {
			gotEcho = true
			break
		}
	}
	if !gotEcho {
		t.Error("did not receive echo of input")
	}
}

func TestClient_Resize(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	conn, _ := net.Dial("unix", sockPath)
	defer conn.Close()

	req := Request{Action: ActionNewSession, Session: "resize-sess", Command: []string{"cat"}, Cols: 80, Rows: 24}
	WriteFrame(conn, FrameRequest, MarshalJSON(req))
	ReadFrame(conn)

	// Send resize
	rp := ResizePayload{Cols: 120, Rows: 40}
	WriteFrame(conn, FrameResize, MarshalJSON(rp))
	time.Sleep(50 * time.Millisecond)

	// Verify via list that session shows new size
	conn2, _ := net.Dial("unix", sockPath)
	defer conn2.Close()
	WriteFrame(conn2, FrameRequest, MarshalJSON(Request{Action: ActionList}))
	_, payload, _ := ReadFrame(conn2)
	var resp Response
	json.Unmarshal(payload, &resp)
	if len(resp.Sessions) != 1 {
		t.Fatal("no sessions")
	}
	// The screen mirror should have been resized
	srv.mu.Lock()
	sess := srv.sessions["resize-sess"]
	srv.mu.Unlock()
	if sess == nil {
		t.Fatal("session not found")
	}
	cols, rows := sess.Screen.Size()
	if cols != 120 || rows != 40 {
		t.Errorf("screen size = %dx%d, want 120x40", cols, rows)
	}
}

func TestClient_SessionSwitch(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	srv.Start()
	defer srv.Stop()

	// Create two sessions
	c1, _ := net.Dial("unix", sockPath)
	WriteFrame(c1, FrameRequest, MarshalJSON(Request{Action: ActionNewSession, Session: "s1", Command: []string{"cat"}, Cols: 80, Rows: 24}))
	ReadFrame(c1)
	WriteFrame(c1, FrameDetach, nil)
	c1.Close()

	c2, _ := net.Dial("unix", sockPath)
	WriteFrame(c2, FrameRequest, MarshalJSON(Request{Action: ActionNewSession, Session: "s2", Command: []string{"cat"}, Cols: 80, Rows: 24}))
	ReadFrame(c2)
	WriteFrame(c2, FrameDetach, nil)
	c2.Close()

	time.Sleep(50 * time.Millisecond)

	// Connect to s1
	c3, _ := net.Dial("unix", sockPath)
	defer c3.Close()
	WriteFrame(c3, FrameRequest, MarshalJSON(Request{Action: ActionAttach, Session: "s1", Cols: 80, Rows: 24}))
	ReadFrame(c3) // response

	// Switch to next
	WriteFrame(c3, FrameSessionSwitch, MarshalJSON(SwitchPayload{Target: "__next"}))
	time.Sleep(50 * time.Millisecond)

	// Verify we're on s2 by checking client counts
	c4, _ := net.Dial("unix", sockPath)
	defer c4.Close()
	WriteFrame(c4, FrameRequest, MarshalJSON(Request{Action: ActionList}))
	_, payload, _ := ReadFrame(c4)
	var resp Response
	json.Unmarshal(payload, &resp)
	for _, si := range resp.Sessions {
		if si.Name == "s2" && si.Attached != 1 {
			t.Errorf("s2 attached = %d, want 1", si.Attached)
		}
		if si.Name == "s1" && si.Attached != 0 {
			t.Errorf("s1 attached = %d, want 0", si.Attached)
		}
	}
}

func contains(data []byte, substr string) bool {
	return len(data) > 0 && len(substr) > 0 &&
		bytesContains(data, []byte(substr))
}

func bytesContains(data, sub []byte) bool {
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
