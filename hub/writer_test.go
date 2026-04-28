package hub

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var testUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// wsTestPair creates a connected server/client WebSocket pair.
func wsTestPair(t *testing.T, handler func(*websocket.Conn)) *websocket.Conn {
	t.Helper()
	var wg sync.WaitGroup
	wg.Add(1)

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverConn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		wg.Done()
		handler(serverConn)
	}))
	t.Cleanup(s.Close)

	clientConn, _, err := websocket.DefaultDialer.Dial("ws"+s.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { clientConn.Close() })

	wg.Wait()
	return clientConn
}

func TestConnWriter_SendAndReceive(t *testing.T) {
	received := make(chan []byte, 10)

	clientConn := wsTestPair(t, func(serverConn *websocket.Conn) {
		w := NewConnWriter(serverConn)

		w.Send([]byte("hello"))
		w.Send([]byte("world"))

		// Wait for writer to flush, then close everything
		time.Sleep(50 * time.Millisecond)
		w.Close()
		serverConn.Close()
	})

	go func() {
		for {
			_, msg, err := clientConn.ReadMessage()
			if err != nil {
				close(received)
				return
			}
			received <- msg
		}
	}()

	var msgs []string
	for msg := range received {
		msgs = append(msgs, string(msg))
	}

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d: %v", len(msgs), msgs)
	}
	if msgs[0] != "hello" || msgs[1] != "world" {
		t.Fatalf("unexpected messages: %v", msgs)
	}
}

func TestConnWriter_CloseStopsLoop(t *testing.T) {
	done := make(chan struct{})

	wsTestPair(t, func(serverConn *websocket.Conn) {
		w := NewConnWriter(serverConn)
		w.Close()
		close(done)
	})

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("writer did not stop after Close")
	}
}
