package main

import (
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"codetty/config"
	"codetty/daemon"
)

type failingEnsureEndpoint struct {
	id  string
	err error
}

func (e *failingEnsureEndpoint) ID() string                        { return e.id }
func (e *failingEnsureEndpoint) Ensure() error                     { return e.err }
func (e *failingEnsureEndpoint) Dial() (io.ReadWriteCloser, error) { return nil, e.err }

// The connecting/done message pair must drive the status-bar indicator and
// surface failures as a notice, keeping the Update loop free of any
// handshake work.
func TestRemoteConnectMessagesDriveStatusBar(t *testing.T) {
	m := newTUIModel(nil, "", nil)

	m.update(remoteConnectingMsg{id: "remote:aa", label: "gpu2"})
	m.update(remoteConnectingMsg{id: "remote:bb", label: "jenkins"})
	if got := m.connectingLabel(); got != "gpu2, jenkins" {
		t.Fatalf("connectingLabel = %q", got)
	}
	if bar := m.statusBar(); !strings.Contains(bar, "connecting: gpu2, jenkins") {
		t.Fatalf("status bar missing connecting indicator: %q", bar)
	}

	m.update(remoteConnectDoneMsg{id: "remote:aa", label: "gpu2"})
	if got := m.connectingLabel(); got != "jenkins" {
		t.Fatalf("connectingLabel after done = %q", got)
	}
	if m.notice != "" {
		t.Fatalf("successful connect set notice %q", m.notice)
	}

	m.update(remoteConnectDoneMsg{id: "remote:bb", label: "jenkins", err: errors.New("boom")})
	if got := m.connectingLabel(); got != "" {
		t.Fatalf("connectingLabel after failure = %q", got)
	}
	if !strings.Contains(m.notice, "jenkins") || !strings.Contains(m.notice, "boom") {
		t.Fatalf("failure notice = %q", m.notice)
	}
}

// Several flows (startup job, recorded registry, reload, picker) may
// connect the same endpoint concurrently; the indicator must stay up until
// the last flow's done message.
func TestRemoteConnectRefCountsConcurrentFlows(t *testing.T) {
	m := newTUIModel(nil, "", nil)

	m.update(remoteConnectingMsg{id: "remote:aa", label: "gpu2"})
	m.update(remoteConnectingMsg{id: "remote:aa", label: "gpu2"})
	if got := m.connectingLabel(); got != "gpu2" {
		t.Fatalf("connectingLabel = %q", got)
	}

	m.update(remoteConnectDoneMsg{id: "remote:aa", label: "gpu2"})
	if got := m.connectingLabel(); got != "gpu2" {
		t.Fatalf("connectingLabel after first done = %q, want still connecting", got)
	}
	m.update(remoteConnectDoneMsg{id: "remote:aa", label: "gpu2"})
	if got := m.connectingLabel(); got != "" {
		t.Fatalf("connectingLabel after last done = %q", got)
	}
}

// The config order must survive until every named session actually exists:
// sessions created by a remote job arrive as created events trailing the
// job's done message (CreateTabOn only sends the frames).
func TestDesiredOrderAppliedToLateRemoteSessions(t *testing.T) {
	m := newTUIModel(nil, "", nil)
	m.subscribed = true
	m.desiredOrder = []string{"remote-b", "local-a"}
	m.handleSessionEvent(daemon.SessionEvent{Type: "created", Session: "local-a"})

	// Job connects, syncs (no remote-b there yet), sends the create
	// frames and reports done before the created event round-trips.
	m.update(remoteConnectingMsg{id: "remote:bb", label: "gpu2"})
	m.update(remoteConnectDoneMsg{id: "remote:bb", label: "gpu2"})
	if len(m.desiredOrder) == 0 {
		t.Fatal("desiredOrder dropped while remote-b does not exist yet")
	}

	m.update(daemonSessionEventMsg{Event: daemon.SessionEvent{Type: "created", Session: "remote-b"}})
	if got := strings.Join(m.order, ","); got != "remote-b,local-a" {
		t.Fatalf("order = %q, want remote-b,local-a", got)
	}
	if len(m.desiredOrder) != 0 {
		t.Fatalf("desiredOrder not cleared once complete: %v", m.desiredOrder)
	}

	// A manual reorder must win over a still-pending config order.
	m.desiredOrder = []string{"remote-b", "local-a"}
	m.moveOrder(1)
	if len(m.desiredOrder) != 0 {
		t.Fatal("manual reorder did not clear pending config order")
	}
}

// A failing Ensure must be reported through remoteConnectDoneMsg instead of
// blocking or logging silently.
func TestConnectRemoteJobCmdReportsEnsureFailure(t *testing.T) {
	dc := newTestCompositeConn("local")
	endpoint := &failingEnsureEndpoint{id: "remote:gpu", err: errors.New("ssh unreachable")}
	cmd := connectRemoteJobCmd(dc, remoteConnectJob{
		id: "remote:gpu", label: "gpu2", endpoint: endpoint,
	}, 100, 30)

	msg, ok := cmd().(remoteConnectDoneMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want remoteConnectDoneMsg", msg)
	}
	if msg.id != "remote:gpu" || msg.label != "gpu2" || msg.err == nil {
		t.Fatalf("done msg = %+v", msg)
	}
}

// connectRemoteJobCmd must adopt containers that already exist on the
// remote (their state streams in via the subscription) and only create the
// missing ones, after the handshake completed in the background.
func TestConnectRemoteJobCmdAdoptsExistingAndCreatesMissing(t *testing.T) {
	prog, _ := startTestProgram(t)
	dc := newTestCompositeConn("local")
	dc.program = prog

	client, server := net.Pipe()
	endpoint := &singleConnEndpoint{id: "remote:gpu", conn: client}

	serverFrames := make(chan recordedFrame, 8)
	go func() {
		if _, _, err := daemon.ReadFrame(server); err != nil {
			return // hello request
		}
		daemon.WriteFrame(server, daemon.FrameSessionEvent, daemon.MarshalJSON(daemon.SessionEvent{
			Type: "snapshot", Session: "dev",
			Info: daemon.SessionInfo{Name: "dev", Tabs: []daemon.TabInfo{{Name: "old"}}},
			Cols: 100, Rows: 30,
		}))
		daemon.WriteFrame(server, daemon.FrameResponse, daemon.MarshalJSON(daemon.Response{OK: true}))
		for {
			ft, payload, err := daemon.ReadFrame(server)
			if err != nil {
				return
			}
			serverFrames <- recordedFrame{ft, payload}
		}
	}()

	job := remoteConnectJob{
		id: "remote:gpu", label: "gpu2", endpoint: endpoint,
		containers: []remoteContainerSpec{
			{display: "dev", cp: containerPlan{EndpointID: "remote:gpu", Container: "dev",
				Tabs: []config.ResolvedTab{{Name: "old"}}}},
			{display: "ops", cp: containerPlan{EndpointID: "remote:gpu", Container: "ops",
				Tabs: []config.ResolvedTab{{Name: "agent"}}}},
		},
	}
	msg, ok := connectRemoteJobCmd(dc, job, 100, 30)().(remoteConnectDoneMsg)
	if !ok || msg.err != nil {
		t.Fatalf("done msg = %+v", msg)
	}
	t.Cleanup(func() {
		close(dc.closed)
		server.Close()
	})

	select {
	case f := <-serverFrames:
		if f.ft != daemon.FrameCreateSession {
			t.Fatalf("frame type = %d, want create_session", f.ft)
		}
		var cs daemon.CreateSessionRequest
		mustUnmarshal(t, f.payload, &cs)
		if cs.Name != "ops" || cs.TabName != "agent" {
			t.Fatalf("create session = %+v", cs)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no create_session frame reached the peer")
	}

	// The existing "dev" container must be adopted, not re-created.
	select {
	case f := <-serverFrames:
		t.Fatalf("unexpected extra frame type %d for adopted container", f.ft)
	case <-time.After(100 * time.Millisecond):
	}
}
