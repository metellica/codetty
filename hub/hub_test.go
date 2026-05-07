package hub

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

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

func TestHub_ReplayOpensFullscreenGate(t *testing.T) {
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

	clientConn.WriteMessage(websocket.BinaryMessage, protocol.EncodeOutput([]byte("visible after gate")))
	time.Sleep(100 * time.Millisecond)

	if got := fullscreen.String(); !strings.Contains(got, "visible after gate") {
		t.Fatalf("fullscreen writer should forward output after replay gate opens, got %q", got)
	}
}

func TestHub_FullscreenSuppressesOutputUntilReplay(t *testing.T) {
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
		ID: "fullscreen-gated", Title: "test", Cols: 80, Rows: 24,
	})
	clientConn.WriteMessage(websocket.BinaryMessage, regMsg)
	time.Sleep(100 * time.Millisecond)

	var fullscreen lockedBuffer
	h.SetFullscreen("fullscreen-gated", &fullscreen)
	defer h.SetFullscreen("", nil)

	clientConn.WriteMessage(websocket.BinaryMessage, protocol.EncodeOutput([]byte("partial redraw")))
	time.Sleep(100 * time.Millisecond)
	if got := fullscreen.String(); got != "" {
		t.Fatalf("fullscreen writer should suppress output before replay, got %q", got)
	}

	replayData := []byte("\033[2J\033[Hstable fullscreen")
	clientConn.WriteMessage(websocket.BinaryMessage, protocol.EncodeReplay(replayData))
	time.Sleep(100 * time.Millisecond)

	if got := fullscreen.String(); !strings.Contains(got, "stable fullscreen") {
		t.Fatalf("fullscreen writer should start with replay snapshot, got %q", got)
	}

	clientConn.WriteMessage(websocket.BinaryMessage, protocol.EncodeOutput([]byte("\r\nnext line")))
	time.Sleep(100 * time.Millisecond)
	if got := fullscreen.String(); !strings.Contains(got, "next line") {
		t.Fatalf("fullscreen writer should forward output after replay, got %q", got)
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

// TestHub_SecureAuthRejectsMismatchedUser_SendsPolicyViolation verifies that a
// register with a mismatched user causes the hub to send a close frame with
// code ClosePolicyViolation and a reason containing "codetty auth rejected",
// and that no session is created.
func TestHub_SecureAuthRejectsMismatchedUser_SendsPolicyViolation(t *testing.T) {
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

	// Read with a short deadline so we don't hang forever.
	clientConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, readErr := clientConn.ReadMessage()
	if readErr == nil {
		t.Fatal("expected connection to be closed with policy violation, but ReadMessage succeeded")
	}

	closeErr, ok := readErr.(*websocket.CloseError)
	if !ok {
		t.Fatalf("expected *websocket.CloseError, got %T: %v", readErr, readErr)
	}
	if closeErr.Code != websocket.ClosePolicyViolation {
		t.Errorf("close code: want %d (ClosePolicyViolation), got %d", websocket.ClosePolicyViolation, closeErr.Code)
	}
	if !strings.Contains(closeErr.Text, "codetty auth rejected") {
		t.Errorf("close reason should contain %q, got %q", "codetty auth rejected", closeErr.Text)
	}

	// No session should be created.
	time.Sleep(50 * time.Millisecond)
	if sessions := h.Sessions(); len(sessions) != 0 {
		t.Fatalf("expected 0 sessions (rejected), got %d", len(sessions))
	}
}

// TestAuthRejectedCloseReason_ShortNames verifies normal case fits and contains prefix.
func TestAuthRejectedCloseReason_ShortNames(t *testing.T) {
	reason := authRejectedCloseReason("alice", "mallory")
	if !strings.HasPrefix(reason, "codetty auth rejected") {
		t.Errorf("reason must start with stable prefix, got %q", reason)
	}
	if len([]byte(reason)) > 123 {
		t.Errorf("reason byte length %d exceeds 123", len([]byte(reason)))
	}
	if !utf8.ValidString(reason) {
		t.Error("reason must be valid UTF-8")
	}
}

// TestAuthRejectedCloseReason_LongNames verifies that very long auth/register names
// are still bounded to <=123 bytes and contain the stable prefix.
func TestAuthRejectedCloseReason_LongNames(t *testing.T) {
	longAuth := strings.Repeat("a", 100)
	longRegister := strings.Repeat("b", 100)
	reason := authRejectedCloseReason(longAuth, longRegister)
	if !strings.Contains(reason, "codetty auth rejected") {
		t.Errorf("reason must contain stable prefix, got %q", reason)
	}
	if len([]byte(reason)) > 123 {
		t.Errorf("reason byte length %d exceeds 123", len([]byte(reason)))
	}
	if !utf8.ValidString(reason) {
		t.Error("reason must be valid UTF-8")
	}
}

// TestAuthRejectedCloseReason_MultibyteNames verifies that multi-byte UTF-8 names
// are handled safely (no mid-rune truncation) and result is valid UTF-8 within 123 bytes.
func TestAuthRejectedCloseReason_MultibyteNames(t *testing.T) {
	// Each "á" is 2 bytes in UTF-8; 70 × 2 = 140 bytes just for authUser.
	longAuth := strings.Repeat("á", 70)
	longRegister := strings.Repeat("ñ", 70)
	reason := authRejectedCloseReason(longAuth, longRegister)
	if !strings.Contains(reason, "codetty auth rejected") {
		t.Errorf("reason must contain stable prefix, got %q", reason)
	}
	if len([]byte(reason)) > 123 {
		t.Errorf("reason byte length %d exceeds 123", len([]byte(reason)))
	}
	if !utf8.ValidString(reason) {
		t.Error("reason must be valid UTF-8")
	}
}

// TestHub_AuthRejectionWithLongNames_ClosesWithPolicyViolation verifies that
// when auth and register user names are very long, the server still sends a
// well-formed close frame (ClosePolicyViolation) containing "codetty auth rejected"
// and creates no session.
func TestHub_AuthRejectionWithLongNames_ClosesWithPolicyViolation(t *testing.T) {
	longAuth := strings.Repeat("x", 100)
	h := New()
	s := authTestServer(t, h, AuthInfo{Secure: true, User: longAuth})
	defer s.Close()

	clientConn, _, err := websocket.DefaultDialer.Dial("ws"+s.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer clientConn.Close()

	longRegister := strings.Repeat("y", 100)
	regMsg, _ := protocol.EncodeRegister(protocol.RegisterPayload{
		ID: "long-session", Title: "long@host", Cols: 80, Rows: 24, User: longRegister,
	})
	clientConn.WriteMessage(websocket.BinaryMessage, regMsg)

	clientConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, readErr := clientConn.ReadMessage()
	if readErr == nil {
		t.Fatal("expected connection to be closed with policy violation, but ReadMessage succeeded")
	}

	closeErr, ok := readErr.(*websocket.CloseError)
	if !ok {
		t.Fatalf("expected *websocket.CloseError, got %T: %v", readErr, readErr)
	}
	if closeErr.Code != websocket.ClosePolicyViolation {
		t.Errorf("close code: want %d (ClosePolicyViolation), got %d", websocket.ClosePolicyViolation, closeErr.Code)
	}
	if !strings.Contains(closeErr.Text, "codetty auth rejected") {
		t.Errorf("close reason should contain %q, got %q", "codetty auth rejected", closeErr.Text)
	}

	time.Sleep(50 * time.Millisecond)
	if sessions := h.Sessions(); len(sessions) != 0 {
		t.Fatalf("expected 0 sessions (rejected), got %d", len(sessions))
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
