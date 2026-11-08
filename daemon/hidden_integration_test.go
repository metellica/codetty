//go:build !windows

package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// subscribeTestClient connects and subscribes to the local owner namespace,
// returning the connection and the session events received before the ACK.
func subscribeTestClient(t *testing.T, sockPath string) (net.Conn, []SessionEvent) {
	t.Helper()
	c, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := WriteFrame(c, FrameRequest, MarshalJSON(Request{
		Action:   ActionSubscribeAll,
		Proto:    ProtocolVersion,
		ProtoMin: MinProtocolVersion,
		Caps:     ProtocolCaps(),
	})); err != nil {
		t.Fatal(err)
	}
	return c, readEventsUntilAck(t, c)
}

func readEventsUntilAck(t *testing.T, c net.Conn) []SessionEvent {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	var events []SessionEvent
	for {
		ft, payload, err := ReadFrame(c)
		if err != nil {
			t.Fatalf("read frame before ACK: %v", err)
		}
		if ft == FrameSessionEvent {
			var evt SessionEvent
			if err := json.Unmarshal(payload, &evt); err != nil {
				t.Fatalf("decode session event: %v", err)
			}
			events = append(events, evt)
			continue
		}
		if ft == FrameResponse {
			var resp Response
			if err := json.Unmarshal(payload, &resp); err != nil || !resp.OK {
				t.Fatalf("subscribe failed: %s", string(payload))
			}
			return events
		}
	}
}

// readEventOfType reads events until one with the given type arrives.
func readEventOfType(t *testing.T, c net.Conn, typ string) SessionEvent {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		ft, payload, err := ReadFrame(c)
		if err != nil {
			t.Fatalf("waiting for %q event: %v", typ, err)
		}
		if ft != FrameSessionEvent {
			continue
		}
		var evt SessionEvent
		if err := json.Unmarshal(payload, &evt); err != nil {
			t.Fatalf("decode session event: %v", err)
		}
		if evt.Type == typ {
			return evt
		}
	}
}

func findEvent(events []SessionEvent, typ string) *SessionEvent {
	for i := range events {
		if events[i].Type == typ {
			return &events[i]
		}
	}
	return nil
}

func TestSubscribeReplaysHiddenSet(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	addTestSession(t, srv, "", "alpha", "")
	addTestSession(t, srv, "", "beta", "")
	srv.setHiddenForSubscriber(testSub(""), SetHiddenRequest{Session: "beta", Hidden: true})

	c, events := subscribeTestClient(t, sockPath)

	// The visibility announcement comes first and carries the full set.
	vis := findEvent(events, "visibility")
	if vis == nil {
		t.Fatal("subscribe did not announce visibility")
	}
	if !reflect.DeepEqual(vis.Hidden, []string{"beta"}) {
		t.Fatalf("visibility hidden = %v, want [beta]", vis.Hidden)
	}
	if len(events) == 0 || events[0].Type != "visibility" {
		t.Fatalf("visibility must precede snapshots, got first event %q", events[0].Type)
	}

	// Hidden sessions are still alive and still snapshot to the client.
	if snap := findEvent(events, "snapshot"); snap == nil || snap.Session != "alpha" {
		t.Fatalf("missing alpha snapshot: %+v", events)
	}
	for _, evt := range events {
		if evt.Type == "active" && evt.Session == "beta" {
			t.Fatal("hidden session announced as active")
		}
	}

	// A later subscriber sees the same state.
	c2, events2 := subscribeTestClient(t, sockPath)
	if vis := findEvent(events2, "visibility"); vis == nil || !reflect.DeepEqual(vis.Hidden, []string{"beta"}) {
		t.Fatalf("second subscriber visibility = %+v, want [beta]", vis)
	}
	_ = c
	_ = c2
}

func TestHiddenChangeBroadcastsToOtherClients(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(sockPath)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	addTestSession(t, srv, "", "alpha", "")

	c1, _ := subscribeTestClient(t, sockPath)
	c2, _ := subscribeTestClient(t, sockPath)

	// Client 1 hides alpha; client 2 must learn about it, and client 1 gets
	// the authoritative echo confirming the change was accepted.
	if err := WriteFrame(c1, FrameSetHidden, MarshalJSON(SetHiddenRequest{Session: "alpha", Hidden: true})); err != nil {
		t.Fatal(err)
	}
	if vis := readEventOfType(t, c1, "visibility"); !reflect.DeepEqual(vis.Hidden, []string{"alpha"}) {
		t.Fatalf("sender echo hidden = %v, want [alpha]", vis.Hidden)
	}
	vis := readEventOfType(t, c2, "visibility")
	if !reflect.DeepEqual(vis.Hidden, []string{"alpha"}) {
		t.Fatalf("broadcast hidden = %v, want [alpha]", vis.Hidden)
	}

	// Showing it broadcasts the empty authoritative set.
	if err := WriteFrame(c1, FrameSetHidden, MarshalJSON(SetHiddenRequest{Session: "alpha", Hidden: false})); err != nil {
		t.Fatal(err)
	}
	vis = readEventOfType(t, c2, "visibility")
	if len(vis.Hidden) != 0 {
		t.Fatalf("broadcast hidden after show = %v, want empty", vis.Hidden)
	}

	// A brand new subscriber sees it visible again.
	_, events := subscribeTestClient(t, sockPath)
	if vis := findEvent(events, "visibility"); vis == nil || len(vis.Hidden) != 0 {
		t.Fatalf("new subscriber visibility = %+v, want empty", vis)
	}
}
