package hub

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"codetty/protocol"

	"github.com/gorilla/websocket"
)

func TestHub_ClientConnectAndDisconnect(t *testing.T) {
	h := New()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		h.HandleConn(conn)
	}))
	defer s.Close()

	clientConn, _, err := websocket.DefaultDialer.Dial("ws"+s.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	regMsg, _ := protocol.EncodeRegister(protocol.RegisterPayload{
		ID:    "test-machine",
		Title: "claude@test",
		Cols:  80,
		Rows:  24,
	})
	clientConn.WriteMessage(websocket.BinaryMessage, regMsg)
	time.Sleep(100 * time.Millisecond)

	sessions := h.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].ID != "test-machine" {
		t.Fatalf("expected ID 'test-machine', got %q", sessions[0].ID)
	}

	clientConn.Close()
	time.Sleep(100 * time.Millisecond)

	sessions = h.Sessions()
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions after disconnect, got %d", len(sessions))
	}
}

func TestHub_OutputUpdatesVT(t *testing.T) {
	h := New()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		h.HandleConn(conn)
	}))
	defer s.Close()

	clientConn, _, err := websocket.DefaultDialer.Dial("ws"+s.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer clientConn.Close()

	regMsg, _ := protocol.EncodeRegister(protocol.RegisterPayload{
		ID: "vt-test", Title: "test", Cols: 80, Rows: 24,
	})
	clientConn.WriteMessage(websocket.BinaryMessage, regMsg)
	time.Sleep(100 * time.Millisecond)

	clientConn.WriteMessage(websocket.BinaryMessage, protocol.EncodeOutput([]byte("hello vt")))
	time.Sleep(100 * time.Millisecond)

	sessions := h.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	screen := sessions[0].Screen()
	if len(screen) == 0 {
		t.Fatal("screen should not be empty after output")
	}
}

func TestHub_ReconnectReplaces(t *testing.T) {
	h := New()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		h.HandleConn(conn)
	}))
	defer s.Close()

	c1, _, _ := websocket.DefaultDialer.Dial("ws"+s.URL[4:], nil)
	regMsg, _ := protocol.EncodeRegister(protocol.RegisterPayload{
		ID: "same-id", Title: "first", Cols: 80, Rows: 24,
	})
	c1.WriteMessage(websocket.BinaryMessage, regMsg)
	time.Sleep(100 * time.Millisecond)

	c2, _, _ := websocket.DefaultDialer.Dial("ws"+s.URL[4:], nil)
	regMsg2, _ := protocol.EncodeRegister(protocol.RegisterPayload{
		ID: "same-id", Title: "second", Cols: 80, Rows: 24,
	})
	c2.WriteMessage(websocket.BinaryMessage, regMsg2)
	time.Sleep(100 * time.Millisecond)

	sessions := h.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session (replaced), got %d", len(sessions))
	}
	if sessions[0].Title != "second" {
		t.Fatalf("expected title 'second', got %q", sessions[0].Title)
	}

	c1.Close()
	c2.Close()
}
