package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"codetty/daemon"

	tea "github.com/charmbracelet/bubbletea"
)

type recordingModel struct {
	msgs chan tea.Msg
}

func (m recordingModel) Init() tea.Cmd { return nil }

func (m recordingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	select {
	case m.msgs <- msg:
	default:
	}
	return m, nil
}

func (m recordingModel) View() string { return "" }

func startTestProgram(t *testing.T) (*tea.Program, <-chan tea.Msg) {
	t.Helper()

	msgs := make(chan tea.Msg, 16)
	p := tea.NewProgram(
		recordingModel{msgs: msgs},
		tea.WithInput(nil),
		tea.WithOutput(io.Discard),
		tea.WithoutRenderer(),
	)

	errCh := make(chan error, 1)
	go func() {
		_, err := p.Run()
		errCh <- err
	}()

	time.Sleep(20 * time.Millisecond)
	t.Cleanup(func() {
		p.Quit()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("program run: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for Bubble Tea program to stop")
		}
	})

	return p, msgs
}

func waitForMsg[T any](t *testing.T, msgs <-chan tea.Msg) T {
	t.Helper()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-msgs:
			if typed, ok := msg.(T); ok {
				return typed
			}
		case <-deadline:
			var zero T
			t.Fatalf("timed out waiting for %T", zero)
		}
	}
}

func TestNewDaemonConn_DialsUnixSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "daemon.sock")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	dc, err := newDaemonConn(sockPath)
	if err != nil {
		t.Fatalf("newDaemonConn: %v", err)
	}
	defer dc.conn.Close()

	select {
	case conn := <-accepted:
		conn.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for accept")
	}
}

func TestDaemonConn_SubscribeReturnsErrorWhenRequestWriteFails(t *testing.T) {
	client, server := net.Pipe()
	server.Close()
	defer client.Close()

	p, _ := startTestProgram(t)
	dc := &daemonConn{conn: client}

	if err := dc.Subscribe(p); err == nil {
		t.Fatal("Subscribe() error = nil, want write error")
	}
}

func TestDaemonConn_SubscribeTranslatesFramesToTeaMessages(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	p, msgs := startTestProgram(t)
	dc := &daemonConn{conn: client}
	t.Cleanup(func() { client.Close() })

	done := make(chan struct{})
	go func() {
		defer close(done)

		ft, payload, err := daemon.ReadFrame(server)
		if err != nil {
			t.Errorf("read subscribe frame: %v", err)
			return
		}
		if ft != daemon.FrameRequest {
			t.Errorf("subscribe frame type = %v, want %v", ft, daemon.FrameRequest)
			return
		}

		var req daemon.Request
		if err := json.Unmarshal(payload, &req); err != nil {
			t.Errorf("unmarshal subscribe request: %v", err)
			return
		}
		if req.Action != daemon.ActionSubscribeAll {
			t.Errorf("subscribe action = %q, want %q", req.Action, daemon.ActionSubscribeAll)
		}

		if err := daemon.WriteFrame(server, daemon.FrameTaggedOutput, daemon.EncodeTaggedOutput("sess", []byte("hello"))); err != nil {
			t.Errorf("write tagged output: %v", err)
			return
		}
		if err := daemon.WriteFrame(server, daemon.FrameSessionEvent, daemon.MarshalJSON(daemon.SessionEvent{Type: "created", Session: "sess"})); err != nil {
			t.Errorf("write session event: %v", err)
			return
		}
		if err := daemon.WriteFrame(server, daemon.FrameResponse, daemon.MarshalJSON(daemon.Response{OK: true})); err != nil {
			t.Errorf("write subscribe response: %v", err)
			return
		}
		server.Close()
	}()

	if err := dc.Subscribe(p); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	output := waitForMsg[daemonOutputMsg](t, msgs)
	if output.Session != "sess" || string(output.Data) != "hello" {
		t.Fatalf("output = %#v, want session sess with hello", output)
	}

	event := waitForMsg[daemonSessionEventMsg](t, msgs)
	if event.Event.Type != "created" || event.Event.Session != "sess" {
		t.Fatalf("event = %#v, want created sess", event)
	}

	_ = waitForMsg[daemonSubscribedMsg](t, msgs)

	disconnected := waitForMsg[daemonDisconnectedMsg](t, msgs)
	if !errors.Is(disconnected.Err, io.EOF) {
		t.Fatalf("disconnect err = %v, want EOF", disconnected.Err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server goroutine")
	}
}

func TestDaemonConn_SendMethodsWriteExpectedFrames(t *testing.T) {
	client, server := net.Pipe()
	dc := &daemonConn{conn: client}
	t.Cleanup(func() { client.Close() })

	type expectedFrame struct {
		ft      daemon.FrameType
		session string
		data    string
		cols    uint16
		rows    uint16
		name    string
		command []string
	}

	expected := []expectedFrame{
		{ft: daemon.FrameTaggedInput, session: "sess", data: "abc"},
		{ft: daemon.FrameResize, cols: 120, rows: 40},
		{ft: daemon.FrameCreateSession, name: "new", command: []string{"bash", "-lc", "echo hi"}, cols: 90, rows: 30},
		{ft: daemon.FrameKillSession, name: "old"},
		{ft: daemon.FrameDetach},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer server.Close()

		for i, want := range expected {
			ft, payload, err := daemon.ReadFrame(server)
			if err != nil {
				t.Errorf("read frame %d: %v", i, err)
				return
			}
			if ft != want.ft {
				t.Errorf("frame %d type = %v, want %v", i, ft, want.ft)
				return
			}

			switch want.ft {
			case daemon.FrameTaggedInput:
				session, data, err := daemon.DecodeTaggedInput(payload)
				if err != nil {
					t.Errorf("decode tagged input: %v", err)
					return
				}
				if session != want.session || string(data) != want.data {
					t.Errorf("tagged input = (%q, %q), want (%q, %q)", session, data, want.session, want.data)
					return
				}
			case daemon.FrameResize:
				var resize daemon.ResizePayload
				if err := json.Unmarshal(payload, &resize); err != nil {
					t.Errorf("unmarshal resize: %v", err)
					return
				}
				if resize.Cols != want.cols || resize.Rows != want.rows {
					t.Errorf("resize = %#v, want cols=%d rows=%d", resize, want.cols, want.rows)
					return
				}
			case daemon.FrameCreateSession:
				var req daemon.CreateSessionRequest
				if err := json.Unmarshal(payload, &req); err != nil {
					t.Errorf("unmarshal create: %v", err)
					return
				}
				if req.Name != want.name || req.Cols != want.cols || req.Rows != want.rows || len(req.Command) != len(want.command) {
					t.Errorf("create request = %#v, want name=%q cols=%d rows=%d command=%v", req, want.name, want.cols, want.rows, want.command)
					return
				}
				for i := range req.Command {
					if req.Command[i] != want.command[i] {
						t.Errorf("create command[%d] = %q, want %q", i, req.Command[i], want.command[i])
						return
					}
				}
			case daemon.FrameKillSession:
				var req daemon.KillSessionRequest
				if err := json.Unmarshal(payload, &req); err != nil {
					t.Errorf("unmarshal kill: %v", err)
					return
				}
				if req.Name != want.name {
					t.Errorf("kill request = %#v, want name=%q", req, want.name)
					return
				}
			case daemon.FrameDetach:
				if len(payload) != 0 {
					t.Errorf("detach payload length = %d, want 0", len(payload))
					return
				}
			}
		}
	}()

	dc.SendInput("sess", []byte("abc"))
	dc.SendResize(120, 40)
	dc.CreateSession("new", []string{"bash", "-lc", "echo hi"}, 90, 30)
	dc.KillSession("old", true)
	dc.Detach()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for sent frames")
	}
}
