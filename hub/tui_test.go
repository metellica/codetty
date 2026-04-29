package hub

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"codetty/protocol"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"
)

func modelTestClient(t *testing.T, h *Hub, id, title string) (*websocket.Conn, []*ClientSession) {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		h.HandleConn(conn)
	}))
	t.Cleanup(s.Close)

	clientConn, _, err := websocket.DefaultDialer.Dial("ws"+s.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() {
		_ = clientConn.Close()
	})

	regMsg, err := protocol.EncodeRegister(protocol.RegisterPayload{
		ID:    id,
		Title: title,
		Cols:  80,
		Rows:  24,
	})
	if err != nil {
		t.Fatalf("encode register: %v", err)
	}
	if err := clientConn.WriteMessage(websocket.BinaryMessage, regMsg); err != nil {
		t.Fatalf("send register: %v", err)
	}

	var sessions []*ClientSession
	for i := 0; i < 20; i++ {
		sessions = h.Sessions()
		for _, session := range sessions {
			if session.ID == id {
				return clientConn, sessions
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected registered session %q, got %d sessions", id, len(sessions))
	return nil, nil
}

func readTestMessage(t *testing.T, conn *websocket.Conn) (protocol.MsgType, []byte) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("expected websocket message: %v", err)
	}
	msgType, payload, ok := protocol.ParseType(msg)
	if !ok {
		t.Fatalf("failed to parse message: %v", msg)
	}
	return msgType, payload
}

func assertNoTestMessage(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, msg, err := conn.ReadMessage()
	if err == nil {
		t.Fatalf("expected no websocket message, got %v", msg)
	}
	if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("expected read timeout waiting for no message, got %v", err)
	}
}

func TestModel_UsesCtrlAPrefixForHubCommands(t *testing.T) {
	h := New()
	m := NewModel(h)
	m.sessions = []*ClientSession{
		NewClientSession("one", "one@test", 80, 24, nil),
		NewClientSession("two", "two@test", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if cmd != nil {
		t.Fatal("Ctrl+A should enter prefix mode without returning a command")
	}
	if !m.layout.prefix {
		t.Fatal("Ctrl+A should enter prefix mode")
	}
	if m.selected != 0 {
		t.Fatalf("Ctrl+A should not change selection, got %d", m.selected)
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd != nil {
		t.Fatal("Ctrl+A n should select next session without returning a command")
	}
	if m.layout.prefix {
		t.Fatal("Ctrl+A n should clear prefix mode")
	}
	if m.selected != 1 {
		t.Fatalf("Ctrl+A n should select next session, got %d", m.selected)
	}
}

func TestModel_PrefixGridCommandPreservesGridPreference(t *testing.T) {
	tests := []struct {
		name          string
		keys          []rune
		wantMode      layoutMode
		wantFixedCols int
	}{
		{
			name:          "fixed grid remains fixed grid",
			keys:          []rune{'2', 'g'},
			wantMode:      layoutFixedGrid,
			wantFixedCols: 2,
		},
		{
			name:          "focus big returns to fixed grid preference",
			keys:          []rune{'2', 'b', 'g'},
			wantMode:      layoutFixedGrid,
			wantFixedCols: 2,
		},
		{
			name:          "focus big returns to auto grid preference",
			keys:          []rune{'a', 'b', 'g'},
			wantMode:      layoutAutoGrid,
			wantFixedCols: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewModel(New())

			for _, key := range tt.keys {
				_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
				if cmd != nil {
					t.Fatal("Ctrl+A should enter prefix mode without returning a command")
				}
				if !m.layout.prefix {
					t.Fatal("Ctrl+A should enter prefix mode")
				}

				_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
				if cmd != nil {
					t.Fatalf("Ctrl+A %c should not return a command", key)
				}
				if m.layout.prefix {
					t.Fatalf("Ctrl+A %c should clear prefix mode", key)
				}
			}

			if m.layout.mode != tt.wantMode {
				t.Fatalf("layout mode = %s, want %s", m.layout.modeName(), (&layoutState{mode: tt.wantMode}).modeName())
			}
			if m.layout.fixedCols != tt.wantFixedCols {
				t.Fatalf("fixedCols = %d, want %d", m.layout.fixedCols, tt.wantFixedCols)
			}
		})
	}
}

func TestModel_LegacyShortcutsForwardWithoutPrefix(t *testing.T) {
	h := New()
	clientConn, sessions := modelTestClient(t, h, "copilot", "copilot@test")

	m := NewModel(h)
	m.sessions = sessions
	m.layout.syncSessions(m.sessions)

	tests := []struct {
		name string
		key  tea.KeyType
		want string
	}{
		{name: "ctrl close bracket", key: tea.KeyCtrlCloseBracket, want: "\x1d"},
		{name: "ctrl f", key: tea.KeyCtrlF, want: "\x06"},
		{name: "ctrl backslash", key: tea.KeyCtrlBackslash, want: "\x1c"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, cmd := m.Update(tea.KeyMsg{Type: tt.key})
			if cmd != nil {
				t.Fatalf("%s should be forwarded, not handled by the hub", tt.name)
			}

			msgType, payload := readTestMessage(t, clientConn)
			if msgType != protocol.MsgInput {
				t.Fatalf("expected input message, got type %q", msgType)
			}
			if string(payload) != tt.want {
				t.Fatalf("expected payload %q, got %q", tt.want, payload)
			}
		})
	}
}

func TestModel_QuitsOnPrefixBackslash(t *testing.T) {
	m := NewModel(New())

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if cmd != nil {
		t.Fatal("Ctrl+A should enter prefix mode without returning a command")
	}
	if !m.layout.prefix {
		t.Fatal("Ctrl+A should enter prefix mode")
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'\\'}})
	if cmd == nil {
		t.Fatal("Ctrl+A \\ should quit the hub")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("Ctrl+A \\ should return tea.Quit")
	}
	if m.layout.prefix {
		t.Fatal("Ctrl+A \\ should clear prefix mode")
	}
}

func TestModel_UnknownPrefixCommandClearsAndDoesNotForward(t *testing.T) {
	h := New()
	clientConn, sessions := modelTestClient(t, h, "copilot", "copilot@test")

	m := NewModel(h)
	m.sessions = sessions
	m.layout.syncSessions(m.sessions)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if cmd != nil {
		t.Fatal("Ctrl+A should enter prefix mode without returning a command")
	}
	if !m.layout.prefix {
		t.Fatal("Ctrl+A should enter prefix mode")
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlBackslash})
	if cmd != nil {
		t.Fatal("unknown Ctrl+A Ctrl+\\ should not return a command")
	}
	if m.layout.prefix {
		t.Fatal("unknown Ctrl+A Ctrl+\\ should clear prefix mode")
	}
	assertNoTestMessage(t, clientConn)
}

func TestModel_PrefixCtrlAForwardsLiteral(t *testing.T) {
	h := New()
	clientConn, sessions := modelTestClient(t, h, "copilot", "copilot@test")

	m := NewModel(h)
	m.sessions = sessions
	m.layout.syncSessions(m.sessions)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if cmd != nil {
		t.Fatal("first Ctrl+A should enter prefix mode without returning a command")
	}
	if !m.layout.prefix {
		t.Fatal("first Ctrl+A should enter prefix mode")
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if cmd != nil {
		t.Fatal("second Ctrl+A should be forwarded without returning a command")
	}
	if m.layout.prefix {
		t.Fatal("second Ctrl+A should clear prefix mode")
	}

	msgType, payload := readTestMessage(t, clientConn)
	if msgType != protocol.MsgInput {
		t.Fatalf("expected input message, got type %q", msgType)
	}
	if string(payload) != "\x01" {
		t.Fatalf("expected literal Ctrl+A byte 0x01, got %q", payload)
	}
}

func TestModel_ForwardsCtrlQToSelectedSession(t *testing.T) {
	h := New()
	clientConn, sessions := modelTestClient(t, h, "copilot", "copilot@test")

	m := NewModel(h)
	m.sessions = sessions
	m.layout.syncSessions(m.sessions)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlQ})
	if cmd != nil {
		t.Fatal("Ctrl+Q should be forwarded to the selected session, not handled by the hub")
	}

	msgType, payload := readTestMessage(t, clientConn)
	if msgType != protocol.MsgInput {
		t.Fatalf("expected input message, got type %q", msgType)
	}
	if string(payload) != "\x11" {
		t.Fatalf("expected Ctrl+Q byte 0x11, got %q", payload)
	}
}
