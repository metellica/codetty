package daemon

import (
	"reflect"
	"testing"
)

func newOrderTestServer(names ...string) *Server {
	srv := NewServer("unused.sock")
	for _, n := range names {
		srv.addSessionLocked(&DaemonSession{Name: n})
	}
	return srv
}

// testSub returns a subscriber in the given owner namespace without a
// connection (sufficient for registry-level operations).
func testSub(owner string) *Subscriber {
	return &Subscriber{owner: owner}
}

func TestServer_OrderFollowsCreation(t *testing.T) {
	srv := newOrderTestServer("a", "b", "c")

	if got := srv.SessionNames(""); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("SessionNames = %v, want [a b c]", got)
	}

	srv.mu.Lock()
	srv.removeSessionLocked("", "b")
	srv.addSessionLocked(&DaemonSession{Name: "d"})
	srv.mu.Unlock()

	if got := srv.SessionNames(""); !reflect.DeepEqual(got, []string{"a", "c", "d"}) {
		t.Fatalf("SessionNames after remove+add = %v, want [a c d]", got)
	}
}

func TestServer_ReorderSessions(t *testing.T) {
	srv := newOrderTestServer("a", "b", "c")

	// Unknown names are ignored; unlisted sessions keep relative order at end.
	srv.reorderSessionsForSubscriber(testSub(""), ReorderSessionsRequest{Order: []string{"c", "ghost", "a"}})

	if got := srv.SessionNames(""); !reflect.DeepEqual(got, []string{"c", "a", "b"}) {
		t.Fatalf("SessionNames after reorder = %v, want [c a b]", got)
	}
}

func TestServer_RenameKeepsOrderPosition(t *testing.T) {
	srv := newOrderTestServer("a", "b", "c")

	srv.renameSessionForSubscriber(testSub(""), RenameSessionRequest{OldName: "b", NewName: "bee"})

	if got := srv.SessionNames(""); !reflect.DeepEqual(got, []string{"a", "bee", "c"}) {
		t.Fatalf("SessionNames after rename = %v, want [a bee c]", got)
	}
}

func TestServer_ActiveSessionLifecycle(t *testing.T) {
	srv := newOrderTestServer("a", "b")
	sub := testSub("")

	srv.setActiveSessionForSubscriber(sub, SetActiveSessionRequest{Session: "ghost"})
	if srv.actives[""] != "" {
		t.Fatalf("active = %q after unknown name, want empty", srv.actives[""])
	}

	srv.setActiveSessionForSubscriber(sub, SetActiveSessionRequest{Session: "b"})
	if srv.actives[""] != "b" {
		t.Fatalf("active = %q, want b", srv.actives[""])
	}

	srv.renameSessionForSubscriber(sub, RenameSessionRequest{OldName: "b", NewName: "bee"})
	if srv.actives[""] != "bee" {
		t.Fatalf("active after rename = %q, want bee", srv.actives[""])
	}

	srv.mu.Lock()
	srv.removeSessionLocked("", "bee")
	srv.mu.Unlock()
	if srv.actives[""] != "" {
		t.Fatalf("active after remove = %q, want empty", srv.actives[""])
	}
}

func TestServer_OrderedSessionsLocked(t *testing.T) {
	srv := newOrderTestServer("x", "y")

	srv.mu.Lock()
	sessions := srv.orderedSessionsLocked("")
	srv.mu.Unlock()

	if len(sessions) != 2 || sessions[0].Name != "x" || sessions[1].Name != "y" {
		t.Fatalf("orderedSessionsLocked = %v, want [x y]", sessions)
	}
}
