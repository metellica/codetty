//go:build !windows

package daemon

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestUpgrade_PersistsTabDir verifies tab working directories and workspace
// pinning survive a hot-upgrade round trip, so shells respawned after the
// daemon execs a new binary still start in the tab's directory instead of
// the daemon's cwd.
func TestUpgrade_PersistsTabDir(t *testing.T) {
	dir := t.TempDir()
	sess, err := newDaemonSession("", "upg", []string{"/bin/sleep", "30"}, 80, 24, dir, nil, nil)
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

	recovered := srv2.sessions[ownerKey("", "upg")]
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

// TestRecoverFromUpgrade_PendingOutputWithLaterActiveTab reproduces the
// crash that killed the daemon (and every session's children) during hot
// upgrade: recovery used to start readLoopForTab while the tab slice was
// still partially built. A recovered pty with buffered output (an agent
// spinner writing during the exec window) made the read loop index
// s.Tabs[s.ActiveTab] immediately; with ActiveTab pointing past the tabs
// appended so far this panicked. Loops must start only after all tabs are
// registered and ActiveTab is clamped.
func TestRecoverFromUpgrade_PendingOutputWithLaterActiveTab(t *testing.T) {
	dir := t.TempDir()

	var tabs []UpgradeTab
	var writers []*os.File
	var readers []*os.File
	for i := 0; i < 3; i++ {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		writers = append(writers, w)
		readers = append(readers, r)
		if i == 0 {
			// Buffered output waiting on the first tab's fd, like an
			// agent spinner that kept writing during the exec.
			if _, err := w.WriteString("pending spinner output\r\n"); err != nil {
				t.Fatal(err)
			}
		}
		// A live child stands in for the tab's shell so waitLoopForTab
		// blocks instead of treating the tab as instantly exited.
		child := exec.Command("/bin/sleep", "30")
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { child.Process.Kill(); child.Wait() })
		// Dup the fd so the recovered daemon owns it exclusively (a real
		// upgrade execs, destroying the old os.File); without this the
		// GC finalizer on r closes the fd under the recovered session.
		duped, err := syscall.Dup(int(r.Fd()))
		if err != nil {
			t.Fatal(err)
		}
		tabs = append(tabs, UpgradeTab{
			Name:    "t" + string(rune('0'+i)),
			Command: []string{"zsh"},
			Fd:      duped,
			Pid:     child.Process.Pid,
			Cols:    80,
			Rows:    24,
		})
	}

	state := UpgradeState{
		SocketPath: filepath.Join(dir, "d.sock"),
		Sessions: []UpgradeSession{{
			Name:      "dev",
			Command:   []string{"zsh"},
			Type:      "native",
			CreatedAt: "2026-07-17T14:00:00+08:00",
			ActiveTab: 2,
			Tabs:      tabs,
		}},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(dir, "state.json")
	if err := os.WriteFile(stateFile, data, 0600); err != nil {
		t.Fatal(err)
	}

	srv, err := RecoverFromUpgrade(stateFile)
	if err != nil {
		t.Fatal(err)
	}

	sess := srv.sessions[ownerKey("", "dev")]
	if sess == nil {
		t.Fatal("recovered server is missing session 'dev'")
	}

	// Give the read loop time to consume the pending output; a regression
	// panics the whole test process here.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if sess.Tabs[0].contentSeq.Load() > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pending output was never read")
		}
		time.Sleep(10 * time.Millisecond)
	}

	sess.mu.Lock()
	active := sess.ActiveTab
	sess.mu.Unlock()
	if active != 2 {
		t.Errorf("ActiveTab = %d, want 2", active)
	}

	for _, w := range writers {
		w.Close()
	}
	for _, r := range readers {
		r.Close()
	}
}
