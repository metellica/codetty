//go:build !windows

package daemon

import (
	"path/filepath"
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
