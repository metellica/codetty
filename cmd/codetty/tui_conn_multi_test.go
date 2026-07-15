package main

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codetty/daemon"
)

type singleConnEndpoint struct {
	id   string
	conn io.ReadWriteCloser
	once sync.Once
}

type failedHandshakeEndpoint struct {
	dials atomic.Int32
}

func (e *failedHandshakeEndpoint) ID() string    { return "remote:failed" }
func (e *failedHandshakeEndpoint) Ensure() error { return nil }
func (e *failedHandshakeEndpoint) Dial() (io.ReadWriteCloser, error) {
	e.dials.Add(1)
	client, server := net.Pipe()
	go func() {
		_, _, _ = daemon.ReadFrame(server)
		_ = server.Close()
	}()
	return client, nil
}

func (e *singleConnEndpoint) ID() string    { return e.id }
func (e *singleConnEndpoint) Ensure() error { return nil }
func (e *singleConnEndpoint) Dial() (io.ReadWriteCloser, error) {
	var conn io.ReadWriteCloser
	e.once.Do(func() { conn = e.conn })
	if conn == nil {
		return nil, io.EOF
	}
	return conn, nil
}

func TestMultiDaemonConnRoutesSessionsAndFiltersRemoteSnapshots(t *testing.T) {
	localClient, localServer := net.Pipe()
	remoteClient, remoteServer := net.Pipe()
	t.Cleanup(func() {
		_ = localServer.Close()
		_ = remoteServer.Close()
	})

	endpoints := map[string]daemonEndpoint{
		"local":      &singleConnEndpoint{id: "local", conn: localClient},
		"remote:gpu": &singleConnEndpoint{id: "remote:gpu", conn: remoteClient},
	}
	dc, err := newMultiDaemonConn(endpoints, map[string]string{
		"local-session":  "local",
		"remote-session": "remote:gpu",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dc.closePeers)

	serve := func(
		conn net.Conn,
		events []daemon.SessionEvent,
		input chan<- string,
	) {
		ft, _, err := daemon.ReadFrame(conn)
		if err != nil || ft != daemon.FrameRequest {
			return
		}
		for _, event := range events {
			_ = daemon.WriteFrame(conn, daemon.FrameSessionEvent, daemon.MarshalJSON(event))
		}
		_ = daemon.WriteFrame(conn, daemon.FrameResponse, daemon.MarshalJSON(daemon.Response{OK: true}))

		ft, payload, err := daemon.ReadFrame(conn)
		if err == nil && ft == daemon.FrameTaggedInput {
			session, _, decodeErr := daemon.DecodeTaggedInput(payload)
			if decodeErr == nil {
				input <- session
			}
		}
	}

	localInput := make(chan string, 1)
	remoteInput := make(chan string, 1)
	go serve(localServer, []daemon.SessionEvent{
		{Type: "snapshot", Session: "local-session"},
		{Type: "snapshot", Session: "unconfigured-local"},
	}, localInput)
	go serve(remoteServer, []daemon.SessionEvent{
		{Type: "snapshot", Session: "remote-session"},
		{Type: "snapshot", Session: "unconfigured-remote"},
	}, remoteInput)

	program, messages := startTestProgram(t)
	if err := dc.Subscribe(program); err != nil {
		t.Fatal(err)
	}

	seen := make(map[string]bool)
	deadline := time.After(2 * time.Second)
	for len(seen) < 3 {
		select {
		case msg := <-messages:
			if event, ok := msg.(daemonSessionEventMsg); ok {
				seen[event.Event.Session] = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for snapshots: %v", seen)
		}
	}
	if seen["unconfigured-remote"] {
		t.Fatal("unconfigured remote session leaked into the local TUI")
	}
	if !seen["local-session"] || !seen["unconfigured-local"] || !seen["remote-session"] {
		t.Fatalf("unexpected snapshots: %v", seen)
	}

	dc.SendInput("local-session", []byte("a"))
	dc.SendInput("remote-session", []byte("b"))
	if got := <-localInput; got != "local-session" {
		t.Fatalf("local input routed to %q", got)
	}
	if got := <-remoteInput; got != "remote-session" {
		t.Fatalf("remote input routed to %q", got)
	}
}

func TestDaemonConnRenameMovesRemoteRouteAfterEvent(t *testing.T) {
	dc := &daemonConn{
		peers: map[string]*daemonPeer{
			"remote:gpu": {
				id:    "remote:gpu",
				known: map[string]bool{"old": true},
			},
		},
		sessionPeer: map[string]string{"old": "remote:gpu"},
		defaultPeer: "local",
	}
	peer := dc.peers["remote:gpu"]
	dc.trackPeerEvent(peer, daemon.SessionEvent{
		Type:    "renamed",
		Session: "old",
		Info:    daemon.SessionInfo{Name: "new"},
	})

	if got := dc.SessionPeer("new"); got != "remote:gpu" {
		t.Fatalf("new route = %q, want remote:gpu", got)
	}
	if got := dc.SessionPeer("old"); got != "local" {
		t.Fatalf("old route = %q, want default local", got)
	}
}

func TestFinishPeerReconcileReturnsMissingSessions(t *testing.T) {
	peer := &daemonPeer{
		known:     map[string]bool{"alive": true, "gone": true},
		seen:      map[string]bool{"alive": true},
		reconcile: true,
	}
	dc := &daemonConn{}
	stale := dc.finishPeerReconcile(peer)
	if len(stale) != 1 || stale[0] != "gone" {
		t.Fatalf("stale = %v, want [gone]", stale)
	}
	if peer.known["gone"] || !peer.known["alive"] || peer.reconcile {
		t.Fatalf("peer state after reconcile = %+v", peer)
	}
}

func TestRouteSessionRejectsTargetChange(t *testing.T) {
	dc := &daemonConn{
		peers: map[string]*daemonPeer{
			"local":      {id: "local"},
			"remote:gpu": {id: "remote:gpu"},
		},
		sessionPeer: map[string]string{"work": "local"},
		defaultPeer: "local",
	}
	if dc.RouteSession("work", "remote:gpu") {
		t.Fatal("RouteSession allowed a live route to change")
	}
	if got := dc.SessionPeer("work"); got != "local" {
		t.Fatalf("route changed to %q", got)
	}
}

func TestDaemonConnRejectsCrossPeerRenameCollision(t *testing.T) {
	dc := &daemonConn{
		peers: map[string]*daemonPeer{
			"local": {
				id:    "local",
				known: map[string]bool{"shared": true},
			},
			"remote:gpu": {
				id:    "remote:gpu",
				known: map[string]bool{"remote": true},
			},
		},
		sessionPeer: map[string]string{
			"shared": "local",
			"remote": "remote:gpu",
		},
		defaultPeer: "local",
	}
	if !dc.sessionKnownOnOtherPeer("remote:gpu", "shared") {
		t.Fatal("expected cross-peer name collision")
	}
}

func TestReconnectWaitsForSubscriptionAndBacksOff(t *testing.T) {
	endpoint := &failedHandshakeEndpoint{}
	dc := &daemonConn{closed: make(chan struct{})}
	peer := &daemonPeer{
		id:       endpoint.ID(),
		endpoint: endpoint,
		conn:     &closedReadWriteCloser{},
		known:    make(map[string]bool),
	}

	done := make(chan error, 1)
	go func() { done <- dc.reconnectPeer(peer) }()
	time.Sleep(850 * time.Millisecond)

	select {
	case err := <-done:
		t.Fatalf("reconnect returned before subscription acknowledgement: %v", err)
	default:
	}
	if got := endpoint.dials.Load(); got > 2 {
		t.Fatalf("SSH reconnect attempts = %d in 850ms, want exponential backoff", got)
	}
	close(dc.closed)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reconnect did not stop after close")
	}
}

type closedReadWriteCloser struct{}

func (*closedReadWriteCloser) Read([]byte) (int, error)    { return 0, io.EOF }
func (*closedReadWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (*closedReadWriteCloser) Close() error                { return nil }

func TestPeerInitialSubscriptionAckCountsOnce(t *testing.T) {
	dc := &daemonConn{pendingSubs: 2}
	peer := &daemonPeer{known: make(map[string]bool)}
	payload := daemon.MarshalJSON(daemon.Response{OK: true})

	dc.handlePeerFrame(peer, daemon.FrameResponse, payload)
	dc.handlePeerFrame(peer, daemon.FrameResponse, payload)

	if dc.pendingSubs != 1 {
		t.Fatalf("pending subscriptions = %d, want 1 after duplicate peer ACK", dc.pendingSubs)
	}
}

func TestRemoteOnlyConnectionFiltersUnconfiguredSessions(t *testing.T) {
	dc := &daemonConn{
		peers: map[string]*daemonPeer{
			"remote:gpu": {id: "remote:gpu"},
		},
		sessionPeer: map[string]string{"configured": "remote:gpu"},
		defaultPeer: "remote:gpu",
	}
	if !dc.acceptSession("remote:gpu", "configured") {
		t.Fatal("configured remote session was rejected")
	}
	if dc.acceptSession("remote:gpu", "unconfigured") {
		t.Fatal("unconfigured remote session was accepted")
	}
}

func TestUnnamedCreateOnRemoteDefaultPeerClaimsCreatedSession(t *testing.T) {
	peer := &daemonPeer{
		id:    "remote:gpu",
		conn:  &closedReadWriteCloser{},
		known: make(map[string]bool),
	}
	dc := &daemonConn{
		peers:          map[string]*daemonPeer{"remote:gpu": peer},
		sessionPeer:    map[string]string{},
		pendingCreates: map[string]int{},
		defaultPeer:    "remote:gpu",
	}

	dc.CreateSession("", nil, 80, 24, "")
	if dc.pendingCreates["remote:gpu"] != 1 {
		t.Fatalf("pendingCreates = %d, want 1 after unnamed create", dc.pendingCreates["remote:gpu"])
	}

	dc.claimPendingCreate("remote:gpu", "s1")
	if !dc.acceptSession("remote:gpu", "s1") {
		t.Fatal("daemon-named session from own create was rejected")
	}
	if dc.pendingCreates["remote:gpu"] != 0 {
		t.Fatalf("pendingCreates = %d, want 0 after claim", dc.pendingCreates["remote:gpu"])
	}

	// A second created event with no pending create must stay filtered.
	dc.claimPendingCreate("remote:gpu", "foreign")
	if dc.acceptSession("remote:gpu", "foreign") {
		t.Fatal("foreign session was claimed without a pending create")
	}
}
