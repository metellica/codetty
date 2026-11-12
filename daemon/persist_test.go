//go:build !windows

package daemon

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"codetty/terminal"
)

func addTestSession(t *testing.T, srv *Server, owner, name, dir string, extraTabs ...string) {
	t.Helper()
	sess, err := newDaemonSession(owner, name, []string{"cat"}, 80, 24, dir, nil, srv.ownerBroadcast(owner))
	if err != nil {
		t.Fatal(err)
	}
	for _, tab := range extraTabs {
		if _, err := sess.AddTab(tab, []string{"cat"}, 80, 24, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	srv.mu.Lock()
	srv.addSessionLocked(sess)
	srv.mu.Unlock()
	go srv.watchSession(sess)
}

func TestServer_PersistRestoresLayoutAfterRestart(t *testing.T) {
	dir := t.TempDir()
	workDir := t.TempDir()
	statePath := filepath.Join(dir, "state", "sessions-state.json")

	srv1 := NewServer(filepath.Join(dir, "a.sock"))
	srv1.EnablePersistence(statePath, true)
	if err := srv1.Start(); err != nil {
		t.Fatal(err)
	}
	addTestSession(t, srv1, "", "alpha", workDir, "logs", "build")
	addTestSession(t, srv1, "", "beta", "")
	addTestSession(t, srv1, "remote01", "gamma", "")
	srv1.SessionByName("", "alpha").RenameTab(1, "renamed")
	srv1.SessionByName("", "alpha").SwitchTab(2)
	srv1.reorderSessionsForSubscriber(testSub(""), ReorderSessionsRequest{Order: []string{"beta", "alpha"}})
	srv1.setActiveSessionForSubscriber(testSub(""), SetActiveSessionRequest{Session: "alpha"})
	srv1.setHiddenForSubscriber(testSub(""), SetHiddenRequest{Session: "beta", Hidden: true})

	srv1.saveState()
	// An OS-initiated shutdown must keep the file even though stopping
	// kills every session.
	srv1.StopPreserving()
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state file missing after preserving stop: %v", err)
	}

	srv2 := NewServer(filepath.Join(dir, "b.sock"))
	srv2.EnablePersistence(statePath, true)
	if err := srv2.Start(); err != nil {
		t.Fatal(err)
	}

	if got := srv2.SessionNames(""); !reflect.DeepEqual(got, []string{"beta", "alpha"}) {
		t.Fatalf("restored order = %v, want [beta alpha]", got)
	}
	if got := srv2.SessionNames("remote01"); !reflect.DeepEqual(got, []string{"gamma"}) {
		t.Fatalf("restored remote01 sessions = %v, want [gamma]", got)
	}
	if srv2.actives[""] != "alpha" {
		t.Fatalf("restored active = %q, want alpha", srv2.actives[""])
	}
	if got := hiddenNames(srv2, ""); !reflect.DeepEqual(got, []string{"beta"}) {
		t.Fatalf("restored hidden = %v, want [beta]", got)
	}

	alpha := srv2.SessionByName("", "alpha")
	var names []string
	for _, tab := range alpha.Tabs {
		names = append(names, tab.Name)
		// Only the default shell comes back, not the saved command.
		if !reflect.DeepEqual(tab.Command, []string{terminal.DefaultShell()}) {
			t.Fatalf("tab %q command = %v, want default shell", tab.Name, tab.Command)
		}
	}
	if want := []string{"default", "renamed", "build"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("restored tabs = %v, want %v", names, want)
	}
	if alpha.ActiveTab != 2 {
		t.Fatalf("restored active tab = %d, want 2", alpha.ActiveTab)
	}
	if alpha.Tabs[1].origName != "logs" {
		t.Fatalf("restored origName = %q, want logs", alpha.Tabs[1].origName)
	}

	// Shells start in the tab's last working directory.
	deadline := time.Now().Add(2 * time.Second)
	for terminal.ProcessCwd(alpha.Tabs[0].Terminal.Pid()) != workDir {
		if time.Now().After(deadline) {
			t.Fatalf("restored tab cwd = %q, want %q", terminal.ProcessCwd(alpha.Tabs[0].Terminal.Pid()), workDir)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// A deliberate stop (kill-server, last session exit) discards the layout.
	srv2.Stop()
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("state file still present after plain stop: %v", err)
	}
}

func TestServer_PersistDropsKilledSessions(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "sessions-state.json")

	srv := NewServer(filepath.Join(dir, "a.sock"))
	srv.EnablePersistence(statePath, true)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	addTestSession(t, srv, "", "keep", "")
	addTestSession(t, srv, "", "drop", "")

	srv.SessionByName("", "drop").Kill()
	deadline := time.Now().Add(2 * time.Second)
	for srv.SessionCount() != 1 {
		if time.Now().After(deadline) {
			t.Fatal("killed session was not removed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	srv.saveState()

	srv2 := NewServer(filepath.Join(dir, "b.sock"))
	srv2.EnablePersistence(statePath, true)
	srv2.restoreSessions()
	defer srv2.Stop()
	if got := srv2.SessionNames(""); !reflect.DeepEqual(got, []string{"keep"}) {
		t.Fatalf("restored sessions = %v, want [keep]", got)
	}
}

func TestServer_KillServerKeepsStateFile(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "sessions-state.json")
	sockPath := filepath.Join(dir, "a.sock")

	srv := NewServer(sockPath)
	srv.EnablePersistence(statePath, true)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	addTestSession(t, srv, "", "alpha", "")
	addTestSession(t, srv, "", "beta", "")

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	WriteFrame(conn, FrameRequest, MarshalJSON(Request{Action: ActionKillServer}))
	ReadFrame(conn)
	conn.Close()
	select {
	case <-srv.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop after kill-server")
	}

	srv2 := NewServer(filepath.Join(dir, "b.sock"))
	srv2.EnablePersistence(statePath, true)
	srv2.restoreSessions()
	defer srv2.Stop()
	if got := srv2.SessionNames(""); !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Fatalf("restored sessions = %v, want [alpha beta]", got)
	}
}

func TestBackupStateFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions-state.json")
	days := []string{"2026-11-01", "2026-11-02", "2026-11-03", "2026-11-04", "2026-11-05", "2026-11-06", "2026-11-07", "2026-11-08", "2026-11-09"}
	for _, day := range days {
		if err := os.WriteFile(path, []byte(day), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := backupStateFile(path, day, 7); err != nil {
			t.Fatal(err)
		}
	}
	// A second backup on the same day must not overwrite the first.
	os.WriteFile(path, []byte("later"), 0o600)
	backupStateFile(path, "2026-11-09", 7)

	got, _ := filepath.Glob(filepath.Join(dir, "sessions-state.*-*-*.json"))
	if len(got) != 7 {
		t.Fatalf("kept %d backups, want 7: %v", len(got), got)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions-state.2026-11-02.json")); !os.IsNotExist(err) {
		t.Fatalf("oldest backup should be pruned: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "sessions-state.2026-11-09.json"))
	if string(data) != "2026-11-09" {
		t.Fatalf("today's backup = %q, want first-of-day content", data)
	}

	// A missing state file creates no (empty) backup but still prunes.
	os.Remove(path)
	if err := backupStateFile(path, "2026-11-10", 7); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions-state.2026-11-10.json")); !os.IsNotExist(err) {
		t.Fatalf("backup created without a state file: %v", err)
	}
}
