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
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	c1, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	WriteFrame(c1, FrameRequest, MarshalJSON(Request{
		Action:  ActionNewSession,
		Session: "s1",
		Command: []string{"sh", "-c", "echo hello && sleep 30"},
		Cols:    80,
		Rows:    24,
	}))
	if _, _, err := ReadFrame(c1); err != nil {
		t.Fatal(err)
	}
	c1.SetReadDeadline(time.Now().Add(2 * time.Second))
	gotInitialOutput := false
	for i := 0; i < 10; i++ {
		ft, payload, err := ReadFrame(c1)
		if err != nil {
			break
		}
		if ft == FrameOutput && contains(payload, "hello") {
			gotInitialOutput = true
			break
		}
	}
	if !gotInitialOutput {
		t.Fatal("did not receive initial output from source session")
	}
	if err := WriteFrame(c1, FrameDetach, nil); err != nil {
		t.Fatal(err)
	}
	c1.Close()

	time.Sleep(100 * time.Millisecond)

	c2, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	WriteFrame(c2, FrameRequest, MarshalJSON(Request{Action: ActionSubscribeAll}))

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
			if err := json.Unmarshal(payload, &evt); err != nil {
				t.Fatal(err)
			}
			if evt.Type == "snapshot" && evt.Session == "s1" {
				gotSnapshot = true
			}
		case FrameResponse:
			var resp Response
			if err := json.Unmarshal(payload, &resp); err != nil {
				t.Fatal(err)
			}
			if resp.OK {
				gotResponse = true
			}
		case FrameTaggedOutput:
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
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	sub, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	WriteFrame(sub, FrameRequest, MarshalJSON(Request{Action: ActionSubscribeAll}))
	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := ReadFrame(sub); err != nil {
		t.Fatal(err)
	}

	c1, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	WriteFrame(c1, FrameRequest, MarshalJSON(Request{
		Action:  ActionNewSession,
		Session: "live",
		Command: []string{"echo", "world"},
		Cols:    80,
		Rows:    24,
	}))
	if _, _, err := ReadFrame(c1); err != nil {
		t.Fatal(err)
	}
	c1.Close()

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
			if err := json.Unmarshal(payload, &evt); err != nil {
				t.Fatal(err)
			}
			if evt.Type == "created" && evt.Session == "live" {
				gotCreated = true
			}
		case FrameTaggedOutput:
			name, _, err := DecodeTaggedOutput(payload)
			if err != nil {
				t.Fatal(err)
			}
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
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	sub, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	WriteFrame(sub, FrameRequest, MarshalJSON(Request{Action: ActionSubscribeAll}))
	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := ReadFrame(sub); err != nil {
		t.Fatal(err)
	}

	WriteFrame(sub, FrameCreateSession, MarshalJSON(CreateSessionRequest{
		Name:    "from-sub",
		Command: []string{"echo", "hi"},
		Cols:    80,
		Rows:    24,
	}))

	gotCreated := false
	for i := 0; i < 10; i++ {
		ft, payload, err := ReadFrame(sub)
		if err != nil {
			break
		}
		if ft == FrameSessionEvent {
			var evt SessionEvent
			if err := json.Unmarshal(payload, &evt); err != nil {
				t.Fatal(err)
			}
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

func TestSubscriber_CreateSessionRejectsDuplicateName(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	creator, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	WriteFrame(creator, FrameRequest, MarshalJSON(Request{
		Action:  ActionNewSession,
		Session: "dup",
		Command: []string{"sh", "-c", "sleep 30"},
		Cols:    80,
		Rows:    24,
	}))
	if _, _, err := ReadFrame(creator); err != nil {
		t.Fatal(err)
	}
	WriteFrame(creator, FrameDetach, nil)
	creator.Close()

	sub, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	WriteFrame(sub, FrameRequest, MarshalJSON(Request{Action: ActionSubscribeAll}))
	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		ft, _, err := ReadFrame(sub)
		if err != nil {
			t.Fatal(err)
		}
		if ft == FrameResponse {
			break
		}
	}

	WriteFrame(sub, FrameCreateSession, MarshalJSON(CreateSessionRequest{
		Name:    "dup",
		Command: []string{"echo", "again"},
		Cols:    80,
		Rows:    24,
	}))

	gotError := false
	for i := 0; i < 10; i++ {
		ft, payload, err := ReadFrame(sub)
		if err != nil {
			break
		}
		if ft != FrameSessionEvent {
			continue
		}
		var evt SessionEvent
		if err := json.Unmarshal(payload, &evt); err != nil {
			t.Fatal(err)
		}
		if evt.Type == "error" && evt.Session == "dup" {
			gotError = true
			break
		}
	}
	if !gotError {
		t.Fatal("expected duplicate session create to emit error event")
	}

	time.Sleep(100 * time.Millisecond)
	srv.mu.Lock()
	sess := srv.sessions["dup"]
	srv.mu.Unlock()
	if sess == nil || len(sess.Command) < 3 || sess.Command[2] != "sleep 30" {
		t.Fatal("duplicate create replaced existing session")
	}
}

func TestSubscriber_Detach(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	c1, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	WriteFrame(c1, FrameRequest, MarshalJSON(Request{
		Action:  ActionNewSession,
		Session: "bg",
		Command: []string{"sleep", "60"},
		Cols:    80,
		Rows:    24,
	}))
	if _, _, err := ReadFrame(c1); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(c1, FrameDetach, nil); err != nil {
		t.Fatal(err)
	}
	c1.Close()

	time.Sleep(50 * time.Millisecond)

	sub, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	WriteFrame(sub, FrameRequest, MarshalJSON(Request{Action: ActionSubscribeAll}))
	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		ft, _, err := ReadFrame(sub)
		if err != nil {
			t.Fatal(err)
		}
		if ft == FrameResponse {
			break
		}
	}

	if err := WriteFrame(sub, FrameDetach, nil); err != nil {
		t.Fatal(err)
	}
	sub.Close()

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
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	c1, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	WriteFrame(c1, FrameRequest, MarshalJSON(Request{
		Action:  ActionNewSession,
		Session: "keep",
		Command: []string{"sleep", "60"},
		Cols:    80,
		Rows:    24,
	}))
	if _, _, err := ReadFrame(c1); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(c1, FrameDetach, nil); err != nil {
		t.Fatal(err)
	}
	c1.Close()

	sub, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	WriteFrame(sub, FrameRequest, MarshalJSON(Request{Action: ActionSubscribeAll}))
	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		ft, _, err := ReadFrame(sub)
		if err != nil {
			t.Fatal(err)
		}
		if ft == FrameResponse {
			break
		}
	}

	WriteFrame(sub, FrameCreateSession, MarshalJSON(CreateSessionRequest{
		Name:    "tokill",
		Command: []string{"sleep", "60"},
		Cols:    80,
		Rows:    24,
	}))
	time.Sleep(100 * time.Millisecond)

	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i := 0; i < 5; i++ {
		ft, payload, err := ReadFrame(sub)
		if err != nil {
			break
		}
		if ft != FrameSessionEvent {
			continue
		}
		var evt SessionEvent
		if err := json.Unmarshal(payload, &evt); err != nil {
			t.Fatal(err)
		}
		if evt.Type == "created" && evt.Session == "tokill" {
			break
		}
	}

	if err := WriteFrame(sub, FrameKillSession, MarshalJSON(KillSessionRequest{Name: "tokill"})); err != nil {
		t.Fatal(err)
	}

	sub.SetReadDeadline(time.Now().Add(3 * time.Second))
	gotExited := false
	for i := 0; i < 10; i++ {
		ft, payload, err := ReadFrame(sub)
		if err != nil {
			break
		}
		if ft != FrameSessionEvent {
			continue
		}
		var evt SessionEvent
		if err := json.Unmarshal(payload, &evt); err != nil {
			t.Fatal(err)
		}
		if evt.Type == "exited" && evt.Session == "tokill" {
			gotExited = true
			break
		}
	}
	if !gotExited {
		t.Error("did not receive exited event after kill")
	}
}

func TestSubscriber_DisconnectsOnServerStop(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}

	sub, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	WriteFrame(sub, FrameRequest, MarshalJSON(Request{Action: ActionSubscribeAll}))
	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		ft, _, err := ReadFrame(sub)
		if err != nil {
			t.Fatal(err)
		}
		if ft == FrameResponse {
			break
		}
	}

	ctl, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	WriteFrame(ctl, FrameRequest, MarshalJSON(Request{Action: ActionKillServer}))
	if _, _, err := ReadFrame(ctl); err != nil {
		t.Fatal(err)
	}
	ctl.Close()

	sub.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if _, _, err := ReadFrame(sub); err == nil {
		t.Fatal("expected subscriber connection to close after server stop")
	}
}
