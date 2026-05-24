package daemon

import (
	"os/exec"
	"testing"
)

func TestIsTmuxAvailable(t *testing.T) {
	// On this system tmux should be available; just verify no panic.
	avail := IsTmuxAvailable()
	_, err := exec.LookPath("tmux")
	expected := err == nil
	if avail != expected {
		t.Fatalf("IsTmuxAvailable() = %v, want %v", avail, expected)
	}
}

func TestDiscoverTmuxSessions(t *testing.T) {
	if !IsTmuxAvailable() {
		t.Skip("tmux not available")
	}

	sessions, err := DiscoverTmuxSessions()
	if err != nil {
		t.Fatalf("DiscoverTmuxSessions() error: %v", err)
	}
	// We can't predict exact sessions, but should not error
	t.Logf("discovered %d tmux sessions: %v", len(sessions), sessions)
}

func TestDiscoverAndAttachTmux_NoDoubleAttach(t *testing.T) {
	if !IsTmuxAvailable() {
		t.Skip("tmux not available")
	}

	sessions, _ := DiscoverTmuxSessions()
	if len(sessions) == 0 {
		t.Skip("no tmux sessions to test with")
	}

	dir := t.TempDir()
	sockPath := dir + "/test.sock"
	srv := NewServer(sockPath)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// First discovery
	n1 := srv.DiscoverAndAttachTmux()
	if n1 == 0 {
		t.Fatal("expected at least 1 tmux session to be attached")
	}

	// Second discovery should attach 0 new sessions (already managed)
	n2 := srv.DiscoverAndAttachTmux()
	if n2 != 0 {
		t.Fatalf("second DiscoverAndAttachTmux() = %d, want 0 (all already managed)", n2)
	}

	// Verify sessions are in the server
	srv.mu.Lock()
	count := 0
	for _, sess := range srv.sessions {
		if sess.Type == SessionTypeTmux {
			count++
		}
	}
	srv.mu.Unlock()

	if count != n1 {
		t.Fatalf("server has %d tmux sessions, want %d", count, n1)
	}
}

func TestDiscoverAndAttachTmux_SessionInfo(t *testing.T) {
	if !IsTmuxAvailable() {
		t.Skip("tmux not available")
	}

	sessions, _ := DiscoverTmuxSessions()
	if len(sessions) == 0 {
		t.Skip("no tmux sessions to test with")
	}

	dir := t.TempDir()
	sockPath := dir + "/test.sock"
	srv := NewServer(sockPath)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	srv.DiscoverAndAttachTmux()

	srv.mu.Lock()
	defer srv.mu.Unlock()

	for _, sess := range srv.sessions {
		if sess.Type != SessionTypeTmux {
			continue
		}
		if sess.TmuxTarget == "" {
			t.Error("tmux session has empty TmuxTarget")
		}
		info := sess.Info()
		if info.Type != SessionTypeTmux {
			t.Errorf("Info().Type = %q, want %q", info.Type, SessionTypeTmux)
		}
		if info.Command != "tmux" {
			t.Errorf("Info().Command = %q, want %q", info.Command, "tmux")
		}
	}
}
