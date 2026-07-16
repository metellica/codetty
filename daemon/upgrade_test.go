//go:build !windows

package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestUpgrade_PersistsTabDir verifies tab working directories and workspace
// pinning survive a hot-upgrade round trip, so shells respawned after the
// daemon execs a new binary still start in the tab's directory instead of
// the daemon's cwd.
func TestUpgrade_PersistsTabDir(t *testing.T) {
	dir := t.TempDir()
	sess, err := newDaemonSession("upg", []string{"/bin/sleep", "30"}, 80, 24, dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	srv := NewServer(filepath.Join(dir, "d.sock"))
	srv.addSessionLocked(sess)

	stateFile, err := srv.PrepareUpgrade()
	if err != nil {
		t.Fatal(err)
	}

	// A real upgrade execs: the old process image (and its os.File over
	// the pty fd) disappears, so the recovered daemon exclusively owns
	// the fd. In-process here, sess would keep a second os.File over the
	// SAME fd — one explicit Close plus one GC finalizer close means the
	// (recycled) fd number gets closed twice, corrupting unrelated fds in
	// later tests. Dup the fds in the state file to mirror exec's
	// exclusive ownership.
	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	var state UpgradeState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	for si := range state.Sessions {
		for ti := range state.Sessions[si].Tabs {
			duped, err := syscall.Dup(state.Sessions[si].Tabs[ti].Fd)
			if err != nil {
				t.Fatal(err)
			}
			state.Sessions[si].Tabs[ti].Fd = duped
		}
	}
	if data, err = json.Marshal(state); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile, data, 0600); err != nil {
		t.Fatal(err)
	}

	srv2, err := RecoverFromUpgrade(stateFile)
	if err != nil {
		t.Fatal(err)
	}

	recovered := srv2.sessions["upg"]
	if recovered == nil {
		t.Fatal("recovered server is missing session 'upg'")
	}
	recovered.mu.Lock()
	tab := recovered.Tabs[0]
	gotDir, workspace := tab.Dir, tab.workspace
	recovered.mu.Unlock()

	if gotDir != dir {
		t.Errorf("recovered tab Dir = %q, want %q", gotDir, dir)
	}
	if !workspace {
		t.Error("recovered tab workspace = false, want true")
	}

	// Suppress respawn on both session objects (they share one child
	// process) before killing it, so no stray shell outlives the test.
	sess.mu.Lock()
	sess.killed = true
	sess.mu.Unlock()
	recovered.Kill()
	sess.Kill()
}
