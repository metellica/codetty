package daemon

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestNormalizeOwner(t *testing.T) {
	srv := NewServer(filepath.Join(t.TempDir(), "d.sock"))
	if got := srv.normalizeOwner(srv.hostOwner); got != "" {
		t.Errorf("normalizeOwner(hostOwner) = %q, want \"\"", got)
	}
	if got := srv.normalizeOwner("cafe0123"); got != "cafe0123" {
		t.Errorf("normalizeOwner(foreign) = %q, want unchanged", got)
	}
	if got := srv.normalizeOwner(""); got != "" {
		t.Errorf("normalizeOwner(\"\") = %q, want \"\"", got)
	}
}

// TestOwnerNamespaceIsolation verifies that two owners can hold sessions
// with the same name without colliding, and that per-owner registries
// (names, order, lookup) stay separate.
func TestOwnerNamespaceIsolation(t *testing.T) {
	srv := NewServer(filepath.Join(t.TempDir(), "d.sock"))

	mk := func(owner, name string) *DaemonSession {
		sess, err := newDaemonSession(owner, name, []string{"/bin/sleep", "30"}, 80, 24, "", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return sess
	}
	local := mk("", "dev")
	remoteB := mk("beefcafe", "dev")
	remoteC := mk("c0ffee00", "dev")
	srv.mu.Lock()
	srv.addSessionLocked(local)
	srv.addSessionLocked(remoteB)
	srv.addSessionLocked(remoteC)
	srv.mu.Unlock()
	t.Cleanup(func() {
		for _, s := range []*DaemonSession{local, remoteB, remoteC} {
			s.Kill()
		}
	})

	if got := srv.SessionCount(); got != 3 {
		t.Fatalf("SessionCount = %d, want 3", got)
	}
	if got := srv.SessionByName("", "dev"); got != local {
		t.Errorf("SessionByName(\"\", dev) = %v, want local session", got)
	}
	if got := srv.SessionByName("beefcafe", "dev"); got != remoteB {
		t.Errorf("SessionByName(beefcafe, dev) = %v, want B's session", got)
	}
	if got := srv.SessionByName("beefcafe", "nope"); got != nil {
		t.Errorf("SessionByName(beefcafe, nope) = %v, want nil", got)
	}
	if got := srv.SessionNames("c0ffee00"); !reflect.DeepEqual(got, []string{"dev"}) {
		t.Errorf("SessionNames(c0ffee00) = %v, want [dev]", got)
	}

	// Removing one owner's session must not touch the others.
	srv.mu.Lock()
	srv.removeSessionLocked("beefcafe", "dev")
	owners := srv.ownersLocked()
	srv.mu.Unlock()
	if srv.SessionByName("beefcafe", "dev") != nil {
		t.Error("B's session still present after remove")
	}
	if srv.SessionByName("", "dev") != local || srv.SessionByName("c0ffee00", "dev") != remoteC {
		t.Error("removing B's session disturbed other namespaces")
	}
	if !reflect.DeepEqual(owners, []string{"", "c0ffee00"}) {
		t.Errorf("ownersLocked = %v, want [\"\" c0ffee00]", owners)
	}
}
