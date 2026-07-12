package daemon

import (
	"sync"
	"testing"
	"time"
)

func TestIsShellCommand(t *testing.T) {
	cases := []struct {
		command []string
		want    bool
	}{
		{nil, true},
		{[]string{"/bin/bash"}, true},
		{[]string{"zsh", "-l"}, true},
		{[]string{"C:\\Windows\\System32\\cmd.exe"}, true},
		{[]string{"PowerShell.EXE"}, true},
		{[]string{"htop"}, false},
		{[]string{"/usr/bin/vim", "file.txt"}, false},
		{[]string{"python3"}, false},
	}
	for _, c := range cases {
		if got := isShellCommand(c.command); got != c.want {
			t.Errorf("isShellCommand(%v) = %v, want %v", c.command, got, c.want)
		}
	}
}

// tabEventRecorder collects onTabsChanged callbacks for assertions.
type tabEventRecorder struct {
	mu     sync.Mutex
	events []string
	ch     chan string
}

func newTabEventRecorder() *tabEventRecorder {
	return &tabEventRecorder{ch: make(chan string, 16)}
}

func (r *tabEventRecorder) callback(_ *DaemonSession, event string) {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
	r.ch <- event
}

func (r *tabEventRecorder) wait(t *testing.T, want string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-r.ch:
			if ev == want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q event", want)
		}
	}
}

// TestTabExit_NonShellCommandRespawnsShell verifies a tab whose non-shell
// command exits gets the default shell respawned in place.
func TestTabExit_NonShellCommandRespawnsShell(t *testing.T) {
	rec := newTabEventRecorder()
	sess, err := newDaemonSession("respawn", []string{"/bin/sleep", "0.2"}, 80, 24, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Kill()
	sess.setTabsChangedCallback(rec.callback)

	rec.wait(t, "tab_created")

	sess.mu.Lock()
	defer sess.mu.Unlock()
	if len(sess.Tabs) != 1 {
		t.Fatalf("expected 1 tab after respawn, got %d", len(sess.Tabs))
	}
	tab := sess.Tabs[0]
	if tab.exited {
		t.Error("respawned tab should not be marked exited")
	}
	if isShell := isShellCommand(tab.Command); !isShell {
		t.Errorf("respawned tab command = %v, want a shell", tab.Command)
	}
	if sess.Terminal != tab.Terminal {
		t.Error("session Terminal should point at the respawned terminal")
	}
	select {
	case <-sess.doneCh:
		t.Error("session should stay alive after respawn")
	default:
	}
}

// TestTabExit_ShellTabAutoCloses verifies an exiting shell tab is removed
// from a multi-tab session and the remaining tab becomes active.
func TestTabExit_ShellTabAutoCloses(t *testing.T) {
	rec := newTabEventRecorder()
	sess, err := newDaemonSession("autoclose", []string{"cat"}, 80, 24, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Kill()
	sess.setTabsChangedCallback(rec.callback)

	if _, err := sess.AddTab("shell", []string{"sh", "-c", "exit 0"}, 80, 24, "", nil); err != nil {
		t.Fatal(err)
	}

	rec.wait(t, "tab_closed")

	sess.mu.Lock()
	defer sess.mu.Unlock()
	if len(sess.Tabs) != 1 {
		t.Fatalf("expected 1 tab after auto-close, got %d", len(sess.Tabs))
	}
	if sess.ActiveTab != 0 {
		t.Errorf("ActiveTab = %d, want 0", sess.ActiveTab)
	}
	if sess.Terminal != sess.Tabs[0].Terminal || sess.Screen != sess.Tabs[0].Screen {
		t.Error("session Terminal/Screen should track the surviving tab")
	}
	select {
	case <-sess.doneCh:
		t.Error("session should stay alive while a tab remains")
	default:
	}
}

// TestTabExit_LastShellTabEndsSession verifies the last shell tab exiting
// ends the whole session instead of auto-closing or respawning.
func TestTabExit_LastShellTabEndsSession(t *testing.T) {
	rec := newTabEventRecorder()
	sess, err := newDaemonSession("last-shell", []string{"sh", "-c", "exit 0"}, 80, 24, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess.setTabsChangedCallback(rec.callback)

	select {
	case <-sess.doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("session did not end after last shell tab exited")
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.events) != 0 {
		t.Errorf("no tab events expected, got %v", rec.events)
	}
}

// TestTabExit_KilledSessionDoesNotRespawn verifies Kill suppresses the
// respawn logic even for non-shell tabs.
func TestTabExit_KilledSessionDoesNotRespawn(t *testing.T) {
	rec := newTabEventRecorder()
	sess, err := newDaemonSession("killed", []string{"/bin/sleep", "30"}, 80, 24, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess.setTabsChangedCallback(rec.callback)

	sess.Kill()

	select {
	case <-sess.doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("killed session did not end")
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.events) != 0 {
		t.Errorf("no tab events expected after Kill, got %v", rec.events)
	}
}

// TestCloseTab_DoesNotRespawn verifies an explicit CloseTab on a non-shell
// tab removes it without triggering a respawn.
func TestCloseTab_DoesNotRespawn(t *testing.T) {
	rec := newTabEventRecorder()
	sess, err := newDaemonSession("closetab", []string{"cat"}, 80, 24, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Kill()
	sess.setTabsChangedCallback(rec.callback)

	if _, err := sess.AddTab("slp", []string{"/bin/sleep", "30"}, 80, 24, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := sess.CloseTab(1); err != nil {
		t.Fatal(err)
	}

	// Give the wait loop a moment to (incorrectly) respawn if suppression
	// failed, then assert nothing happened.
	time.Sleep(300 * time.Millisecond)
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if len(sess.Tabs) != 1 {
		t.Fatalf("expected 1 tab after CloseTab, got %d", len(sess.Tabs))
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.events) != 0 {
		t.Errorf("no auto tab events expected after CloseTab, got %v", rec.events)
	}
}

// TestTabExit_ShellWithDirRespawns verifies that a shell tab with a workspace
// dir respawns in that directory instead of auto-closing.
func TestTabExit_ShellWithDirRespawns(t *testing.T) {
	rec := newTabEventRecorder()
	sess, err := newDaemonSession("shell-dir", []string{"cat"}, 80, 24, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Kill()
	sess.setTabsChangedCallback(rec.callback)

	// Add a shell tab with a workspace directory.
	if _, err := sess.AddTab("work", []string{"sh", "-c", "exit 0"}, 80, 24, "/tmp", nil); err != nil {
		t.Fatal(err)
	}

	// The shell exits immediately; because it has a Dir, it should respawn.
	rec.wait(t, "tab_created")

	sess.mu.Lock()
	defer sess.mu.Unlock()
	if len(sess.Tabs) != 2 {
		t.Fatalf("expected 2 tabs (original + respawned), got %d", len(sess.Tabs))
	}
	tab := sess.Tabs[1]
	if tab.exited {
		t.Error("respawned tab should not be marked exited")
	}
	if !isShellCommand(tab.Command) {
		t.Errorf("respawned tab command = %v, want a shell", tab.Command)
	}
	if tab.Dir != "/tmp" {
		t.Errorf("respawned tab Dir = %q, want /tmp", tab.Dir)
	}
}
