package hub

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"codetty/protocol"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"
)

func TestModel_ForwardsCtrlQToSelectedSession(t *testing.T) {
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

	regMsg, err := protocol.EncodeRegister(protocol.RegisterPayload{
		ID:    "copilot",
		Title: "copilot@test",
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
		if len(sessions) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected registered session, got %d", len(sessions))
	}

	m := NewModel(h)
	m.sessions = sessions
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlQ})
	if cmd != nil {
		t.Fatal("Ctrl+Q should be forwarded to the selected session, not handled by the hub")
	}

	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, msg, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("expected input message for Ctrl+Q: %v", err)
	}
	msgType, payload, ok := protocol.ParseType(msg)
	if !ok {
		t.Fatalf("failed to parse message: %v", msg)
	}
	if msgType != protocol.MsgInput {
		t.Fatalf("expected input message, got type %q", msgType)
	}
	if string(payload) != "\x11" {
		t.Fatalf("expected Ctrl+Q byte 0x11, got %q", payload)
	}
}

func TestModel_QuitsOnCtrlBackslash(t *testing.T) {
	m := NewModel(New())

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlBackslash})
	if cmd == nil {
		t.Fatal("Ctrl+\\ should quit the hub")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("Ctrl+\\ should return tea.Quit")
	}
}
