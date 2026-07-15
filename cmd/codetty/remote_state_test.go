package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codetty/config"
	"codetty/daemon"
)

func TestRecordedRemotesRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if got := loadRecordedRemotes(); len(got) != 0 {
		t.Fatalf("load with no file = %v", got)
	}

	remotes := map[string]config.RemoteDef{
		"gpu2":    {Host: "gpu2", Instance: "default", Deploy: "auto"},
		"jenkins": {Host: "10.0.0.5", Instance: "ci", Deploy: "never", Binary: "/opt/codetty"},
	}
	saveRecordedRemotes(remotes)

	got := loadRecordedRemotes()
	if len(got) != 2 {
		t.Fatalf("load = %v", got)
	}
	if got["gpu2"] != remotes["gpu2"] || got["jenkins"] != remotes["jenkins"] {
		t.Fatalf("round trip mismatch: %v", got)
	}

	if info, err := os.Stat(recordedRemotesPath()); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("registry file stat = %v %v", info, err)
	}

	// Empty set removes the file.
	saveRecordedRemotes(nil)
	if _, err := os.Stat(recordedRemotesPath()); !os.IsNotExist(err) {
		t.Fatalf("file not removed: %v", err)
	}
}

func TestRecordedRemotesPathPerInstance(t *testing.T) {
	t.Setenv("HOME", "/home/x")
	old := instanceName
	defer func() { instanceName = old }()

	instanceName = ""
	if got := recordedRemotesPath(); !strings.HasSuffix(got, filepath.Join("codetty", "default", "remotes.json")) {
		t.Fatalf("default path = %q", got)
	}
	instanceName = "work"
	if got := recordedRemotesPath(); !strings.HasSuffix(got, filepath.Join("codetty", "work", "remotes.json")) {
		t.Fatalf("named path = %q", got)
	}
}

func TestPersistRemotePartsTracksShadowOwnership(t *testing.T) {
	dc := newTestCompositeConn("local")
	gpu, err := newSSHDaemonEndpoint("gpu2", config.RemoteDef{Host: "gpu2"})
	if err != nil {
		t.Fatal(err)
	}
	remote := &daemonPeer{
		id:         gpu.ID(),
		endpoint:   gpu,
		conn:       &recordingConn{},
		known:      make(map[string]bool),
		initialAck: true,
	}
	dc.peers[gpu.ID()] = remote

	var writes []map[string]config.RemoteDef
	dc.persistRemotes = func(m map[string]config.RemoteDef) { writes = append(writes, m) }

	dc.mergeAndRun(dc.peers["local"], daemon.SessionEvent{
		Type: "snapshot", Session: "dev",
		Info: daemon.SessionInfo{Name: "dev", Tabs: []daemon.TabInfo{{Name: "shell"}}},
	})
	dc.persistRemoteParts()
	if len(writes) != 1 || len(writes[0]) != 0 {
		t.Fatalf("writes after local-only = %v", writes)
	}

	// Shadow part appears: the endpoint is recorded (auto deploy omits
	// the normalized binary path).
	dc.mergeAndRun(remote, daemon.SessionEvent{
		Type: "snapshot", Session: dc.testShadow("dev"),
		Info: daemon.SessionInfo{Name: dc.testShadow("dev"), Tabs: []daemon.TabInfo{{Name: "agent"}}},
	})
	dc.persistRemoteParts()
	want := config.RemoteDef{Host: "gpu2", Instance: "default", Deploy: "auto"}
	if len(writes) != 2 || writes[1]["gpu2"] != want {
		t.Fatalf("writes after shadow = %v", writes)
	}

	// Unchanged state does not rewrite the file.
	dc.persistRemoteParts()
	if len(writes) != 2 {
		t.Fatalf("dedupe failed: %d writes", len(writes))
	}

	// Shadow exits: entry drops off.
	dc.mergeAndRun(remote, daemon.SessionEvent{Type: "exited", Session: dc.testShadow("dev")})
	dc.persistRemoteParts()
	if len(writes) != 3 || len(writes[2]) != 0 {
		t.Fatalf("writes after exit = %v", writes)
	}
}

func TestPersistRemotePartsPreservesUnreachableSeeds(t *testing.T) {
	dc := newTestCompositeConn("local")
	def := config.RemoteDef{Host: "down", Instance: "default", Deploy: "auto"}
	dc.remoteSeeds = map[string]recordedRemote{
		"remote:absent":   {name: "down", def: def},
		"remote:rejected": {name: "old", def: config.RemoteDef{Host: "old"}},
		"remote:empty":    {name: "empty", def: config.RemoteDef{Host: "empty"}},
	}
	dc.peers["remote:rejected"] = &daemonPeer{
		id: "remote:rejected", conn: &recordingConn{},
		known: make(map[string]bool), initialAck: true, rejected: true,
	}
	dc.peers["remote:empty"] = &daemonPeer{
		id: "remote:empty", conn: &recordingConn{},
		known: make(map[string]bool), initialAck: true,
	}

	var got map[string]config.RemoteDef
	dc.persistRemotes = func(m map[string]config.RemoteDef) { got = m }
	dc.persistRemoteParts()

	// Never-connected and rejected seeds survive; a reconciled peer
	// without shadow parts is dropped.
	if len(got) != 2 || got["down"] != def || got["old"].Host != "old" {
		t.Fatalf("persisted = %v", got)
	}
}
