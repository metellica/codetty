package hub

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
		h.HandleConn(conn, AuthInfo{})
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
		h.HandleConn(conn, AuthInfo{})
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
		h.HandleConn(conn, AuthInfo{})
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

func TestHub_ReplayRestoresScreen(t *testing.T) {
	h := New()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		h.HandleConn(conn, AuthInfo{})
	}))
	defer s.Close()

	clientConn, _, err := websocket.DefaultDialer.Dial("ws"+s.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer clientConn.Close()

	// Register session
	regMsg, _ := protocol.EncodeRegister(protocol.RegisterPayload{
		ID: "replay-test", Title: "test", Cols: 80, Rows: 24,
	})
	clientConn.WriteMessage(websocket.BinaryMessage, regMsg)
	time.Sleep(100 * time.Millisecond)

	// Send replay data (simulates buffered PTY output)
	replayData := []byte("restored content\r\nline two")
	clientConn.WriteMessage(websocket.BinaryMessage, protocol.EncodeReplay(replayData))
	time.Sleep(100 * time.Millisecond)

	// Verify screen was restored
	sess := h.Session("replay-test")
	if sess == nil {
		t.Fatal("session not found")
	}
	screen := sess.Screen()
	if !strings.Contains(screen, "restored content") {
		t.Fatalf("screen should contain 'restored content', got:\n%s", screen)
	}
	if !strings.Contains(screen, "line two") {
		t.Fatalf("screen should contain 'line two', got:\n%s", screen)
	}
}

func TestHub_ReplayWritesSnapshotToFullscreenWriter(t *testing.T) {
	h := New()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		h.HandleConn(conn, AuthInfo{})
	}))
	defer s.Close()

	clientConn, _, err := websocket.DefaultDialer.Dial("ws"+s.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer clientConn.Close()

	regMsg, _ := protocol.EncodeRegister(protocol.RegisterPayload{
		ID: "fullscreen-replay", Title: "test", Cols: 80, Rows: 24,
	})
	clientConn.WriteMessage(websocket.BinaryMessage, regMsg)
	time.Sleep(100 * time.Millisecond)

	var fullscreen lockedBuffer
	h.SetFullscreen("fullscreen-replay", &fullscreen)
	defer h.SetFullscreen("", nil)

	replayData := []byte("\033[2J\033[Hrestored fullscreen")
	clientConn.WriteMessage(websocket.BinaryMessage, protocol.EncodeReplay(replayData))
	time.Sleep(100 * time.Millisecond)

	if got := fullscreen.String(); !strings.Contains(got, "restored fullscreen") {
		t.Fatalf("fullscreen writer should receive replay snapshot, got %q", got)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// authTestServer creates an httptest server that serves HandleConn with the given AuthInfo.
func authTestServer(t *testing.T, h *Hub, auth AuthInfo) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		h.HandleConn(conn, auth)
	}))
}

func TestHub_SecureAuthAcceptsMatchingUser(t *testing.T) {
	h := New()
	s := authTestServer(t, h, AuthInfo{Secure: true, User: "alice"})
	defer s.Close()

	clientConn, _, err := websocket.DefaultDialer.Dial("ws"+s.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer clientConn.Close()

	regMsg, _ := protocol.EncodeRegister(protocol.RegisterPayload{
		ID: "alice-session", Title: "alice@host", Cols: 80, Rows: 24, User: "alice",
	})
	clientConn.WriteMessage(websocket.BinaryMessage, regMsg)
	time.Sleep(100 * time.Millisecond)

	sessions := h.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].User != "alice" {
		t.Fatalf("expected session.User = 'alice', got %q", sessions[0].User)
	}
}

func TestHub_SecureAuthRejectsMismatchedUser(t *testing.T) {
	h := New()
	s := authTestServer(t, h, AuthInfo{Secure: true, User: "alice"})
	defer s.Close()

	clientConn, _, err := websocket.DefaultDialer.Dial("ws"+s.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer clientConn.Close()

	regMsg, _ := protocol.EncodeRegister(protocol.RegisterPayload{
		ID: "mallory-session", Title: "mallory@host", Cols: 80, Rows: 24, User: "mallory",
	})
	clientConn.WriteMessage(websocket.BinaryMessage, regMsg)
	time.Sleep(100 * time.Millisecond)

	// No session should be created
	sessions := h.Sessions()
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions (rejected), got %d", len(sessions))
	}

	// Connection should be closed by server
	clientConn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, _, readErr := clientConn.ReadMessage()
	if readErr == nil {
		t.Fatal("expected connection to be closed by server after auth rejection")
	}
}

func TestHub_InsecureAuthSetsAnonymousUser(t *testing.T) {
	h := New()
	s := authTestServer(t, h, AuthInfo{Secure: false})
	defer s.Close()

	clientConn, _, err := websocket.DefaultDialer.Dial("ws"+s.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer clientConn.Close()

	regMsg, _ := protocol.EncodeRegister(protocol.RegisterPayload{
		ID: "anon-session", Title: "anon@host", Cols: 80, Rows: 24,
	})
	clientConn.WriteMessage(websocket.BinaryMessage, regMsg)
	time.Sleep(100 * time.Millisecond)

	sessions := h.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].User != "anonymous" {
		t.Fatalf("expected session.User = 'anonymous', got %q", sessions[0].User)
	}
}
