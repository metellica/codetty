package daemon

import (
	"reflect"
	"testing"
)

// hiddenNames is a locked wrapper for assertions.
func hiddenNames(srv *Server, owner string) []string {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return srv.hiddenNamesLocked(owner)
}

func TestServer_HiddenLifecycle(t *testing.T) {
	srv := newOrderTestServer("a", "b", "c")
	sub := testSub("")

	srv.setHiddenForSubscriber(sub, SetHiddenRequest{Session: "b", Hidden: true})
	if got := hiddenNames(srv, ""); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("hidden = %v, want [b]", got)
	}

	// Unknown and empty names are ignored.
	srv.setHiddenForSubscriber(sub, SetHiddenRequest{Session: "ghost", Hidden: true})
	srv.setHiddenForSubscriber(sub, SetHiddenRequest{Session: "", Hidden: true})
	if got := hiddenNames(srv, ""); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("hidden after unknown = %v, want [b]", got)
	}

	// Re-hiding is idempotent.
	srv.setHiddenForSubscriber(sub, SetHiddenRequest{Session: "b", Hidden: true})
	if got := hiddenNames(srv, ""); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("hidden after re-hide = %v, want [b]", got)
	}

	srv.setHiddenForSubscriber(sub, SetHiddenRequest{Session: "b", Hidden: false})
	if got := hiddenNames(srv, ""); len(got) != 0 {
		t.Fatalf("hidden after show = %v, want none", got)
	}
	if _, ok := srv.hidden[""]; ok {
		t.Fatal("empty hidden set should be removed from the registry")
	}
}

func TestServer_HiddenFollowsRename(t *testing.T) {
	srv := newOrderTestServer("a", "b")
	sub := testSub("")

	srv.setHiddenForSubscriber(sub, SetHiddenRequest{Session: "b", Hidden: true})
	srv.renameSessionForSubscriber(sub, RenameSessionRequest{OldName: "b", NewName: "bee"})

	if got := hiddenNames(srv, ""); !reflect.DeepEqual(got, []string{"bee"}) {
		t.Fatalf("hidden after rename = %v, want [bee]", got)
	}
}

func TestServer_RemoveSessionClearsHidden(t *testing.T) {
	srv := newOrderTestServer("a", "b")
	sub := testSub("")
	srv.setHiddenForSubscriber(sub, SetHiddenRequest{Session: "b", Hidden: true})

	srv.mu.Lock()
	srv.removeSessionLocked("", "b")
	srv.mu.Unlock()

	if got := hiddenNames(srv, ""); len(got) != 0 {
		t.Fatalf("hidden after remove = %v, want none", got)
	}
	if _, ok := srv.hidden[""]; ok {
		t.Fatal("empty hidden set should be removed from the registry")
	}
}

func TestServer_HiddenClearsActiveSession(t *testing.T) {
	srv := newOrderTestServer("a", "b")
	sub := testSub("")

	srv.setActiveSessionForSubscriber(sub, SetActiveSessionRequest{Session: "b"})
	srv.setHiddenForSubscriber(sub, SetHiddenRequest{Session: "b", Hidden: true})

	if srv.actives[""] != "" {
		t.Fatalf("active = %q after hiding it, want empty", srv.actives[""])
	}

	// Showing the session again must not restore the stale selection.
	srv.setHiddenForSubscriber(sub, SetHiddenRequest{Session: "b", Hidden: false})
	if srv.actives[""] != "" {
		t.Fatalf("active = %q after show, want still empty", srv.actives[""])
	}
}

func TestServer_HiddenIsPerOwner(t *testing.T) {
	srv := newOrderTestServer("local")
	srv.mu.Lock()
	srv.addSessionLocked(&DaemonSession{Name: "remote", Owner: "beef"})
	srv.mu.Unlock()

	srv.setHiddenForSubscriber(testSub(""), SetHiddenRequest{Session: "local", Hidden: true})
	srv.setHiddenForSubscriber(testSub("beef"), SetHiddenRequest{Session: "remote", Hidden: true})

	if got := hiddenNames(srv, ""); !reflect.DeepEqual(got, []string{"local"}) {
		t.Fatalf("local hidden = %v, want [local]", got)
	}
	if got := hiddenNames(srv, "beef"); !reflect.DeepEqual(got, []string{"remote"}) {
		t.Fatalf("beef hidden = %v, want [remote]", got)
	}
}
