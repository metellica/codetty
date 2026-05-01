package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"codetty/protocol"

	"github.com/gorilla/websocket"
)

type fakeTerminal struct {
	resizeCh chan [2]uint16
}

func newFakeTerminal() *fakeTerminal {
	return &fakeTerminal{resizeCh: make(chan [2]uint16, 1)}
}

func (f *fakeTerminal) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (f *fakeTerminal) Write(p []byte) (int, error) {
	return len(p), nil
}

func (f *fakeTerminal) Close() error {
	return nil
}

func (f *fakeTerminal) Resize(cols, rows uint16) error {
	f.resizeCh <- [2]uint16{cols, rows}
	return nil
}

func (f *fakeTerminal) Wait() error {
	return nil
}

func TestStartTerminalRejectsMissingCommand(t *testing.T) {
	if _, err := startTerminal(nil, 80, 24); err == nil {
		t.Fatal("startTerminal should reject an empty command")
	}
}

func TestRunConnectionResizeUsesTerminalBackend(t *testing.T) {
	resizeSent := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Upgrade(w, r, nil, 1024, 1024)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read register: %v", err)
			return
		}
		msgType, _, ok := protocol.ParseType(msg)
		if !ok || msgType != protocol.MsgRegister {
			t.Errorf("first message type = %q, ok=%v; want register", msgType, ok)
			return
		}

		resizeMsg, err := protocol.EncodeResize(100, 40)
		if err != nil {
			t.Errorf("encode resize: %v", err)
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, resizeMsg); err != nil {
			t.Errorf("send resize: %v", err)
			return
		}
		close(resizeSent)
		time.Sleep(20 * time.Millisecond)
	}))
	defer server.Close()

	term := newFakeTerminal()
	screen := NewScreenMirror(80, 24)
	outputCh := make(chan []byte)
	doneCh := make(chan error)
	errCh := make(chan error, 1)
	var replayMu sync.Mutex

	go func() {
		errCh <- runConnection(connectionConfig{
			ServerURL: "ws" + strings.TrimPrefix(server.URL, "http"),
			Dialer:    websocket.DefaultDialer,
		}, "id", "title", term, outputCh, doneCh, screen, &replayMu)
	}()

	select {
	case <-resizeSent:
	case <-time.After(time.Second):
		t.Fatal("server did not send resize")
	}

	select {
	case got := <-term.resizeCh:
		if got != [2]uint16{100, 40} {
			t.Fatalf("resize = %v, want [100 40]", got)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal backend did not receive resize")
	}

	cols, rows := screen.Size()
	if cols != 100 || rows != 40 {
		t.Fatalf("screen size = %dx%d, want 100x40", cols, rows)
	}

	select {
	case <-errCh:
	case <-time.After(time.Second):
		t.Fatal("runConnection did not return after server close")
	}
}
