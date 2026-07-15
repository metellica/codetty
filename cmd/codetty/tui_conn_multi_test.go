package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"strings"
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

type closedReadWriteCloser struct{}

func (*closedReadWriteCloser) Read([]byte) (int, error)    { return 0, io.EOF }
func (*closedReadWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (*closedReadWriteCloser) Close() error                { return nil }

// recordingConn records frames written to a peer for later inspection.
type recordingConn struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *recordingConn) Read([]byte) (int, error) { return 0, io.EOF }
func (c *recordingConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}
func (c *recordingConn) Close() error { return nil }

type recordedFrame struct {
	ft      daemon.FrameType
	payload []byte
}

func (c *recordingConn) frames(t *testing.T) []recordedFrame {
	t.Helper()
	c.mu.Lock()
	data := append([]byte(nil), c.buf.Bytes()...)
	c.mu.Unlock()
	var frames []recordedFrame
	r := bytes.NewReader(data)
	for r.Len() > 0 {
		ft, payload, err := daemon.ReadFrame(r)
		if err != nil {
			t.Fatalf("decode recorded frame: %v", err)
		}
		frames = append(frames, recordedFrame{ft, payload})
	}
	return frames
}

// newTestCompositeConn builds a multi-peer conn without dialing anything.
func newTestCompositeConn(peerIDs ...string) *daemonConn {
	dc := &daemonConn{
		peers:      make(map[string]*daemonPeer),
		localID:    "cafe0123",
		composites: make(map[string]*compositeSession),
		partOwner:  make(map[string]*compositeSession),
		queuedTabs: make(map[string][]queuedTab),
		closed:     make(chan struct{}),
	}
	for _, id := range peerIDs {
		dc.peers[id] = &daemonPeer{
			id:    id,
			conn:  &recordingConn{},
			known: make(map[string]bool),
		}
	}
	return dc
}

func (dc *daemonConn) testShadow(display string) string {
	return shadowSessionName(display, dc.localID)
}

// mergeAndRun feeds an event through mergeEvent and runs the deferred writes.
func (dc *daemonConn) mergeAndRun(peer *daemonPeer, evt daemon.SessionEvent) []daemon.SessionEvent {
	out, post := dc.mergeEvent(peer, evt)
	for _, fn := range post {
		fn()
	}
	return out
}

func TestShadowSessionNameRoundTrip(t *testing.T) {
	name := shadowSessionName("dev", "cafe0123")
	display, id, ok := parseShadowSessionName(name)
	if !ok || display != "dev" || id != "cafe0123" {
		t.Fatalf("parse(%q) = %q %q %v", name, display, id, ok)
	}
	if _, _, ok := parseShadowSessionName("plain"); ok {
		t.Fatal("plain name parsed as shadow")
	}
	if _, _, ok := parseShadowSessionName("@@id"); ok {
		t.Fatal("empty display parsed as shadow")
	}
}

func TestCompositeMergesShadowIntoLocalSession(t *testing.T) {
	dc := newTestCompositeConn("local", "remote:gpu")
	local := dc.peers["local"]
	remote := dc.peers["remote:gpu"]

	out := dc.mergeAndRun(local, daemon.SessionEvent{
		Type: "snapshot", Session: "dev",
		Info: daemon.SessionInfo{Name: "dev", Tabs: []daemon.TabInfo{{Name: "shell"}, {Name: "edit"}}, ActiveTab: 1},
	})
	if len(out) != 1 || out[0].Type != "snapshot" || out[0].Session != "dev" {
		t.Fatalf("local snapshot out = %+v", out)
	}
	if len(out[0].Info.Tabs) != 2 || out[0].Info.ActiveTab != 1 {
		t.Fatalf("merged info = %+v", out[0].Info)
	}

	out = dc.mergeAndRun(remote, daemon.SessionEvent{
		Type: "snapshot", Session: dc.testShadow("dev"),
		Info: daemon.SessionInfo{Name: dc.testShadow("dev"), Tabs: []daemon.TabInfo{{Name: "agent"}}, ActiveTab: 0},
	})
	if len(out) != 1 || out[0].Type != "agent_state" || out[0].Session != "dev" {
		t.Fatalf("shadow snapshot out = %+v", out)
	}
	info := out[0].Info
	if len(info.Tabs) != 3 || info.Tabs[2].Name != "agent" {
		t.Fatalf("merged tabs = %+v", info.Tabs)
	}
	if info.ActiveTab != 1 {
		t.Fatalf("merged active tab = %d, want local part's tab 1", info.ActiveTab)
	}
}

func TestCompositeFiltersForeignRemoteSessions(t *testing.T) {
	dc := newTestCompositeConn("local", "remote:gpu")
	remote := dc.peers["remote:gpu"]

	for _, name := range []string{"plain", "dev@@deadbeef"} {
		out := dc.mergeAndRun(remote, daemon.SessionEvent{
			Type: "snapshot", Session: name,
			Info: daemon.SessionInfo{Name: name},
		})
		if len(out) != 0 {
			t.Fatalf("foreign session %q leaked: %+v", name, out)
		}
	}
}

func TestCompositeOutputOnlyFromActivePart(t *testing.T) {
	dc := newTestCompositeConn("local", "remote:gpu")
	local := dc.peers["local"]
	remote := dc.peers["remote:gpu"]
	shadow := dc.testShadow("dev")

	dc.mergeAndRun(local, daemon.SessionEvent{
		Type: "snapshot", Session: "dev",
		Info: daemon.SessionInfo{Name: "dev", Tabs: []daemon.TabInfo{{Name: "shell"}}},
	})
	dc.mergeAndRun(remote, daemon.SessionEvent{
		Type: "snapshot", Session: shadow,
		Info: daemon.SessionInfo{Name: shadow, Tabs: []daemon.TabInfo{{Name: "agent"}}},
	})

	if _, ok := dc.mapOutput("remote:gpu", shadow); ok {
		t.Fatal("inactive part output was forwarded")
	}
	if display, ok := dc.mapOutput("local", "dev"); !ok || display != "dev" {
		t.Fatalf("active part output = %q %v", display, ok)
	}

	// Switching to the remote tab adopts the remote part as active.
	out := dc.mergeAndRun(remote, daemon.SessionEvent{
		Type: "tab_switched", Session: shadow,
		Info: daemon.SessionInfo{Name: shadow, Tabs: []daemon.TabInfo{{Name: "agent"}}, ActiveTab: 0},
	})
	if len(out) != 1 || out[0].Type != "tab_switched" || out[0].Session != "dev" {
		t.Fatalf("tab_switched out = %+v", out)
	}
	if out[0].Info.ActiveTab != 1 {
		t.Fatalf("merged active tab = %d, want remote tab offset 1", out[0].Info.ActiveTab)
	}
	if _, ok := dc.mapOutput("local", "dev"); ok {
		t.Fatal("stale local output forwarded after remote became active")
	}
	if display, ok := dc.mapOutput("remote:gpu", shadow); !ok || display != "dev" {
		t.Fatalf("remote output = %q %v", display, ok)
	}
}

func TestCompositeTabChangeOnInactivePartDemotedToAgentState(t *testing.T) {
	dc := newTestCompositeConn("local", "remote:gpu")
	local := dc.peers["local"]
	remote := dc.peers["remote:gpu"]
	shadow := dc.testShadow("dev")

	dc.mergeAndRun(local, daemon.SessionEvent{
		Type: "snapshot", Session: "dev",
		Info: daemon.SessionInfo{Name: "dev", Tabs: []daemon.TabInfo{{Name: "shell"}}},
	})
	dc.mergeAndRun(remote, daemon.SessionEvent{
		Type: "snapshot", Session: shadow,
		Info: daemon.SessionInfo{Name: shadow, Tabs: []daemon.TabInfo{{Name: "agent"}}},
	})

	out := dc.mergeAndRun(remote, daemon.SessionEvent{
		Type: "tab_created", Session: shadow,
		Info: daemon.SessionInfo{Name: shadow, Tabs: []daemon.TabInfo{{Name: "agent"}, {Name: "logs"}}},
	})
	if len(out) != 1 || out[0].Type != "agent_state" {
		t.Fatalf("inactive tab_created out = %+v, want agent_state demotion", out)
	}
	if len(out[0].Info.Tabs) != 3 {
		t.Fatalf("merged tabs = %+v", out[0].Info.Tabs)
	}
}

func TestCompositeRoutesTabOpsByMergedIndex(t *testing.T) {
	dc := newTestCompositeConn("local", "remote:gpu")
	local := dc.peers["local"]
	remote := dc.peers["remote:gpu"]
	shadow := dc.testShadow("dev")

	dc.mergeAndRun(local, daemon.SessionEvent{
		Type: "snapshot", Session: "dev",
		Info: daemon.SessionInfo{Name: "dev", Tabs: []daemon.TabInfo{{Name: "shell"}, {Name: "edit"}}},
	})
	dc.mergeAndRun(remote, daemon.SessionEvent{
		Type: "snapshot", Session: shadow,
		Info: daemon.SessionInfo{Name: shadow, Tabs: []daemon.TabInfo{{Name: "agent"}}},
	})

	dc.SwitchTab("dev", 2)
	frames := dc.peers["remote:gpu"].conn.(*recordingConn).frames(t)
	if len(frames) != 1 || frames[0].ft != daemon.FrameSwitchTab {
		t.Fatalf("remote frames = %+v", frames)
	}
	var sw daemon.SwitchTabRequest
	mustUnmarshal(t, frames[0].payload, &sw)
	if sw.Session != shadow || sw.Index != 0 {
		t.Fatalf("switch = %+v, want shadow tab 0", sw)
	}

	// Closing the remote part's only tab kills the shadow container.
	dc.CloseTab("dev", 2)
	frames = dc.peers["remote:gpu"].conn.(*recordingConn).frames(t)
	if len(frames) != 2 || frames[1].ft != daemon.FrameKillSession {
		t.Fatalf("remote frames after close = %+v", frames)
	}
	var kill daemon.KillSessionRequest
	mustUnmarshal(t, frames[1].payload, &kill)
	if kill.Name != shadow {
		t.Fatalf("killed %q, want %q", kill.Name, shadow)
	}

	// Closing a local tab maps to a plain close on the local daemon.
	dc.CloseTab("dev", 1)
	lframes := dc.peers["local"].conn.(*recordingConn).frames(t)
	last := lframes[len(lframes)-1]
	if last.ft != daemon.FrameCloseTab {
		t.Fatalf("local frames = %+v", lframes)
	}
	var cl daemon.CloseTabRequest
	mustUnmarshal(t, last.payload, &cl)
	if cl.Session != "dev" || cl.Index != 1 {
		t.Fatalf("close = %+v", cl)
	}

	if got := dc.TabTarget("dev", 2); got != "remote:gpu" {
		t.Fatalf("TabTarget(2) = %q", got)
	}
	if got := dc.TabTarget("dev", 0); got != "local" {
		t.Fatalf("TabTarget(0) = %q", got)
	}
}

// TestReloadContainerOps covers the composite primitives config reload uses:
// CloseTabOn always sends a plain close (creates are issued first, so a
// close never removes a container's last tab), ContainerPeers enumerates a
// display session's parts, and KillContainerOn kills a stale container.
func TestReloadContainerOps(t *testing.T) {
	dc := newTestCompositeConn("local", "remote:gpu")
	local := dc.peers["local"]
	remote := dc.peers["remote:gpu"]
	shadow := dc.testShadow("dev")

	dc.mergeAndRun(local, daemon.SessionEvent{
		Type: "snapshot", Session: "dev",
		Info: daemon.SessionInfo{Name: "dev", Tabs: []daemon.TabInfo{{Name: "shell"}}},
	})
	dc.mergeAndRun(remote, daemon.SessionEvent{
		Type: "snapshot", Session: shadow,
		Info: daemon.SessionInfo{Name: shadow, Tabs: []daemon.TabInfo{{Name: "agent"}}},
	})

	peers := dc.ContainerPeers("dev")
	if len(peers) != 2 || peers[0] != "local" || peers[1] != "remote:gpu" {
		t.Fatalf("ContainerPeers = %v", peers)
	}

	// Even for a part's only tab, CloseTabOn sends a plain close, never a
	// kill: reload's create-before-close ordering guarantees the daemon
	// already appended replacement tabs when this executes.
	dc.CloseTabOn("dev", "remote:gpu", 0)
	frames := remote.conn.(*recordingConn).frames(t)
	if len(frames) != 1 || frames[0].ft != daemon.FrameCloseTab {
		t.Fatalf("remote frames = %+v", frames)
	}
	var cl daemon.CloseTabRequest
	mustUnmarshal(t, frames[0].payload, &cl)
	if cl.Session != shadow || cl.Index != 0 {
		t.Fatalf("close = %+v", cl)
	}

	dc.KillContainerOn("dev", "remote:gpu")
	frames = remote.conn.(*recordingConn).frames(t)
	if len(frames) != 2 || frames[1].ft != daemon.FrameKillSession {
		t.Fatalf("remote frames after kill = %+v", frames)
	}
	var kill daemon.KillSessionRequest
	mustUnmarshal(t, frames[1].payload, &kill)
	if kill.Name != shadow {
		t.Fatalf("killed %q, want %q", kill.Name, shadow)
	}

	// Unknown peers and displays are no-ops.
	dc.KillContainerOn("dev", "remote:other")
	dc.KillContainerOn("nope", "remote:gpu")
	if got := len(remote.conn.(*recordingConn).frames(t)); got != 2 {
		t.Fatalf("unexpected extra frames: %d", got)
	}
}

func TestCompositeRenameRenamesAllParts(t *testing.T) {
	dc := newTestCompositeConn("local", "remote:gpu")
	local := dc.peers["local"]
	remote := dc.peers["remote:gpu"]
	shadow := dc.testShadow("dev")

	dc.mergeAndRun(local, daemon.SessionEvent{
		Type: "snapshot", Session: "dev",
		Info: daemon.SessionInfo{Name: "dev", Tabs: []daemon.TabInfo{{Name: "shell"}}},
	})
	dc.mergeAndRun(remote, daemon.SessionEvent{
		Type: "snapshot", Session: shadow,
		Info: daemon.SessionInfo{Name: shadow, Tabs: []daemon.TabInfo{{Name: "agent"}}},
	})

	dc.RenameSession("dev", "bad@@name")
	if frames := dc.peers["local"].conn.(*recordingConn).frames(t); len(frames) != 0 {
		t.Fatalf("rename with @@ was sent: %+v", frames)
	}

	dc.RenameSession("dev", "prod")
	lframes := dc.peers["local"].conn.(*recordingConn).frames(t)
	rframes := dc.peers["remote:gpu"].conn.(*recordingConn).frames(t)
	if len(lframes) != 1 || lframes[0].ft != daemon.FrameRenameSession {
		t.Fatalf("local frames = %+v", lframes)
	}
	if len(rframes) != 1 || rframes[0].ft != daemon.FrameRenameSession {
		t.Fatalf("remote frames = %+v", rframes)
	}
	var lr, rr daemon.RenameSessionRequest
	mustUnmarshal(t, lframes[0].payload, &lr)
	mustUnmarshal(t, rframes[0].payload, &rr)
	if lr.OldName != "dev" || lr.NewName != "prod" {
		t.Fatalf("local rename = %+v", lr)
	}
	if rr.OldName != shadow || rr.NewName != dc.testShadow("prod") {
		t.Fatalf("remote rename = %+v", rr)
	}

	// The composite renames once the first part event arrives.
	out := dc.mergeAndRun(local, daemon.SessionEvent{
		Type: "renamed", Session: "dev",
		Info: daemon.SessionInfo{Name: "prod"},
	})
	if len(out) != 1 || out[0].Type != "renamed" || out[0].Session != "dev" || out[0].Info.Name != "prod" {
		t.Fatalf("renamed out = %+v", out)
	}
	// The shadow rename event arrives later and is a no-op for the TUI.
	out = dc.mergeAndRun(remote, daemon.SessionEvent{
		Type: "renamed", Session: shadow,
		Info: daemon.SessionInfo{Name: dc.testShadow("prod")},
	})
	if len(out) != 0 {
		t.Fatalf("follow-up rename out = %+v", out)
	}
	if dc.composites["prod"] == nil || dc.composites["dev"] != nil {
		t.Fatal("composite was not re-keyed")
	}
}

func TestCompositePartExitPromotesNextPart(t *testing.T) {
	dc := newTestCompositeConn("local", "remote:gpu")
	local := dc.peers["local"]
	remote := dc.peers["remote:gpu"]
	shadow := dc.testShadow("dev")

	dc.mergeAndRun(local, daemon.SessionEvent{
		Type: "snapshot", Session: "dev",
		Info: daemon.SessionInfo{Name: "dev", Tabs: []daemon.TabInfo{{Name: "shell"}}},
	})
	dc.mergeAndRun(remote, daemon.SessionEvent{
		Type: "snapshot", Session: shadow,
		Info: daemon.SessionInfo{Name: shadow, Tabs: []daemon.TabInfo{{Name: "agent"}}, ActiveTab: 0},
	})

	// Active (local) part dies: the remote part takes over and its daemon
	// is asked to replay the screen via a same-index tab switch.
	out := dc.mergeAndRun(local, daemon.SessionEvent{Type: "exited", Session: "dev"})
	if len(out) != 1 || out[0].Type != "tab_switched" || out[0].Session != "dev" {
		t.Fatalf("exit out = %+v", out)
	}
	if len(out[0].Info.Tabs) != 1 || out[0].Info.Tabs[0].Name != "agent" {
		t.Fatalf("merged info after exit = %+v", out[0].Info)
	}
	rframes := dc.peers["remote:gpu"].conn.(*recordingConn).frames(t)
	if len(rframes) != 1 || rframes[0].ft != daemon.FrameSwitchTab {
		t.Fatalf("remote frames = %+v", rframes)
	}

	// Last part dies: the composite exits.
	out = dc.mergeAndRun(remote, daemon.SessionEvent{Type: "exited", Session: shadow, ExitCode: 3})
	if len(out) != 1 || out[0].Type != "exited" || out[0].Session != "dev" || out[0].ExitCode != 3 {
		t.Fatalf("final exit out = %+v", out)
	}
	if len(dc.composites) != 0 {
		t.Fatalf("composites not cleaned up: %+v", dc.composites)
	}
}

func TestCreateTabOnQueuesUntilContainerAppears(t *testing.T) {
	dc := newTestCompositeConn("local", "remote:gpu")
	remote := dc.peers["remote:gpu"]
	shadow := dc.testShadow("dev")

	dc.CreateTabOn("dev", "remote:gpu", "agent", []string{"copilot"}, "/w", nil, 100, 30)
	dc.CreateTabOn("dev", "remote:gpu", "logs", nil, "", nil, 100, 30)

	frames := remote.conn.(*recordingConn).frames(t)
	if len(frames) != 1 || frames[0].ft != daemon.FrameCreateSession {
		t.Fatalf("frames = %+v, want single create_session", frames)
	}
	var cs daemon.CreateSessionRequest
	mustUnmarshal(t, frames[0].payload, &cs)
	if cs.Name != shadow || cs.TabName != "agent" || cs.Cols != 100 {
		t.Fatalf("create session = %+v", cs)
	}

	// The container snapshot flushes the queued tab.
	dc.mergeAndRun(remote, daemon.SessionEvent{
		Type: "created", Session: shadow,
		Info: daemon.SessionInfo{Name: shadow, Tabs: []daemon.TabInfo{{Name: "agent"}}},
	})
	frames = remote.conn.(*recordingConn).frames(t)
	if len(frames) != 2 || frames[1].ft != daemon.FrameCreateTab {
		t.Fatalf("frames after snapshot = %+v", frames)
	}
	var ct daemon.CreateTabRequest
	mustUnmarshal(t, frames[1].payload, &ct)
	if ct.Session != shadow || ct.Name != "logs" {
		t.Fatalf("queued tab = %+v", ct)
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

func TestMultiDaemonConnMergesPeersOverPipes(t *testing.T) {
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
	dc, err := newMultiDaemonConn(endpoints)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dc.closePeers)
	shadow := shadowSessionName("dev", dc.localID)

	serve := func(conn net.Conn, events []daemon.SessionEvent, input chan<- string) {
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
		{Type: "snapshot", Session: "work", Info: daemon.SessionInfo{Name: "work", Tabs: []daemon.TabInfo{{Name: "shell"}}}},
	}, localInput)
	go serve(remoteServer, []daemon.SessionEvent{
		{Type: "snapshot", Session: shadow, Info: daemon.SessionInfo{Name: shadow, Tabs: []daemon.TabInfo{{Name: "agent"}}}},
		{Type: "snapshot", Session: "foreign", Info: daemon.SessionInfo{Name: "foreign"}},
	}, remoteInput)

	program, messages := startTestProgram(t)
	if err := dc.Subscribe(program); err != nil {
		t.Fatal(err)
	}

	seen := make(map[string]bool)
	deadline := time.After(2 * time.Second)
	for len(seen) < 2 {
		select {
		case msg := <-messages:
			if event, ok := msg.(daemonSessionEventMsg); ok {
				seen[event.Event.Session] = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for snapshots: %v", seen)
		}
	}
	if seen["foreign"] || seen[shadow] {
		t.Fatalf("foreign or raw shadow name leaked: %v", seen)
	}
	if !seen["work"] || !seen["dev"] {
		t.Fatalf("unexpected snapshots: %v", seen)
	}

	// Input follows each composite's active part with the daemon-side name.
	dc.SendInput("work", []byte("a"))
	dc.SendInput("dev", []byte("b"))
	if got := <-localInput; got != "work" {
		t.Fatalf("local input routed to %q", got)
	}
	if got := <-remoteInput; got != shadow {
		t.Fatalf("remote input routed to %q, want %q", got, shadow)
	}
}

func mustUnmarshal(t *testing.T, payload []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(payload, v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

// endpointFor wires a pre-connected pipe as a reconnectable endpoint: the
// first Dial returns the pipe, later Dials fail (so rejection tests would
// notice an unwanted reconnect attempt via dial counting).
type countingEndpoint struct {
	id    string
	conn  io.ReadWriteCloser
	dials atomic.Int32
	once  sync.Once
}

func (e *countingEndpoint) ID() string    { return e.id }
func (e *countingEndpoint) Ensure() error { return nil }
func (e *countingEndpoint) Dial() (io.ReadWriteCloser, error) {
	e.dials.Add(1)
	var conn io.ReadWriteCloser
	e.once.Do(func() { conn = e.conn })
	if conn == nil {
		return nil, io.EOF
	}
	return conn, nil
}

func TestPeerHandshakeStoresNegotiationAndRejectionStopsPeer(t *testing.T) {
	okClient, okServer := net.Pipe()
	rejClient, rejServer := net.Pipe()
	t.Cleanup(func() {
		_ = okServer.Close()
		_ = rejServer.Close()
	})

	rejEndpoint := &countingEndpoint{id: "remote:old", conn: rejClient}
	endpoints := map[string]daemonEndpoint{
		"local":      &singleConnEndpoint{id: "local", conn: okClient},
		"remote:old": rejEndpoint,
	}
	dc, err := newMultiDaemonConn(endpoints)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dc.closePeers)

	// Healthy daemon: ACK with a hello (subset of caps), keep serving.
	go func() {
		_, _, _ = daemon.ReadFrame(okServer)
		_ = daemon.WriteFrame(okServer, daemon.FrameResponse, daemon.MarshalJSON(daemon.Response{
			OK:       true,
			Proto:    daemon.ProtocolVersion,
			ProtoMin: daemon.MinProtocolVersion,
			Caps:     []string{"tagged_io"},
		}))
	}()
	// Incompatible daemon: reject the handshake and close, like the real
	// server does.
	go func() {
		_, _, _ = daemon.ReadFrame(rejServer)
		_ = daemon.WriteFrame(rejServer, daemon.FrameResponse, daemon.MarshalJSON(daemon.Response{
			Error: "incompatible protocol: daemon speaks 1..1, client speaks 4..5 (upgrade the older side)",
		}))
		_ = rejServer.Close()
	}()

	program, messages := startTestProgram(t)
	if err := dc.Subscribe(program); err != nil {
		t.Fatal(err)
	}

	var gotNotice, gotSubscribed bool
	deadline := time.After(2 * time.Second)
	for !gotNotice || !gotSubscribed {
		select {
		case msg := <-messages:
			switch m := msg.(type) {
			case tuiNoticeMsg:
				if !strings.Contains(m.text, "incompatible protocol") {
					t.Fatalf("notice = %q", m.text)
				}
				gotNotice = true
			case daemonSubscribedMsg:
				// A rejected peer must still count towards startup so the
				// TUI does not hang waiting for it.
				gotSubscribed = true
			}
		case <-deadline:
			t.Fatalf("timed out: notice=%v subscribed=%v", gotNotice, gotSubscribed)
		}
	}

	local := dc.peer("local")
	waitFor(t, func() bool {
		local.stateMu.Lock()
		defer local.stateMu.Unlock()
		return local.initialAck
	})
	if local.proto != daemon.ProtocolVersion {
		t.Fatalf("local proto = %d, want %d", local.proto, daemon.ProtocolVersion)
	}
	if !local.hasCap("tagged_io") || local.hasCap("tab_ops") {
		t.Fatalf("caps = %v, want only the intersection", local.caps)
	}

	rejected := dc.peer("remote:old")
	waitFor(t, func() bool {
		rejected.stateMu.Lock()
		defer rejected.stateMu.Unlock()
		return rejected.rejected
	})
	// Give a would-be reconnect loop time to fire, then verify none did.
	time.Sleep(400 * time.Millisecond)
	if got := rejEndpoint.dials.Load(); got != 1 {
		t.Fatalf("rejected peer dialed %d times, want 1 (no reconnects)", got)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
