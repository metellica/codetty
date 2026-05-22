package main

import (
	"encoding/json"
	"net"
	"slices"
	"testing"

	"codetty/daemon"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNewTUIModel_Defaults(t *testing.T) {
	m := newTUIModel(nil, "target", []string{"bash"})

	if m.conn != nil {
		t.Fatalf("conn = %#v, want nil", m.conn)
	}
	if m.mode != modeFocusBig {
		t.Fatalf("mode = %v, want %v", m.mode, modeFocusBig)
	}
	if m.width != 120 || m.height != 40 {
		t.Fatalf("size = %dx%d, want 120x40", m.width, m.height)
	}
	if m.focusTarget != "target" {
		t.Fatalf("focusTarget = %q, want target", m.focusTarget)
	}
	if got := m.selectedSession(); got != nil {
		t.Fatalf("selectedSession = %#v, want nil", got)
	}
}

func TestHandleSessionEvent_TracksSessionsAndFocus(t *testing.T) {
	m := newTUIModel(nil, "beta", nil)
	m.width = 100
	m.height = 30

	m.handleSessionEvent(daemon.SessionEvent{
		Type:    "created",
		Session: "alpha",
		Info: daemon.SessionInfo{
			Command:   "bash",
			CreatedAt: "2025-01-01T00:00:00Z",
		},
	})
	m.handleSessionEvent(daemon.SessionEvent{
		Type:    "snapshot",
		Session: "beta",
		Cols:    80,
		Rows:    24,
		Info: daemon.SessionInfo{
			Command:   "sh",
			CreatedAt: "2025-01-02T00:00:00Z",
		},
	})

	if len(m.sessions) != 2 || len(m.order) != 2 {
		t.Fatalf("sessions/order lens = %d/%d, want 2/2", len(m.sessions), len(m.order))
	}
	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1", m.selected)
	}
	if m.focusTarget != "" {
		t.Fatalf("focusTarget = %q, want cleared", m.focusTarget)
	}
	if got := m.selectedSession(); got == nil || got.Name != "beta" || got.Command != "sh" || got.CreatedAt != "2025-01-02T00:00:00Z" {
		t.Fatalf("selectedSession = %#v, want beta session", got)
	}

	m.handleSessionEvent(daemon.SessionEvent{Type: "exited", Session: "beta", ExitCode: 7})

	if len(m.sessions) != 1 || len(m.order) != 1 {
		t.Fatalf("sessions/order lens after exit = %d/%d, want 1/1", len(m.sessions), len(m.order))
	}
	if got := m.sessionByName("beta"); got != nil {
		t.Fatalf("beta session still present: %#v", got)
	}
}

func TestHandleKey_DoubleCtrlAForwardsLiteralCtrlA(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	m := newTUIModel(&daemonConn{conn: client}, "", nil)
	m.sessions = []*tuiSession{newTUISession("sess", "bash", 80, 24)}
	m.order = []string{"sess"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		ft, payload, err := daemon.ReadFrame(server)
		if err != nil {
			t.Errorf("ReadFrame: %v", err)
			return
		}
		if ft != daemon.FrameTaggedInput {
			t.Errorf("frame type = %v, want %v", ft, daemon.FrameTaggedInput)
			return
		}
		session, data, err := daemon.DecodeTaggedInput(payload)
		if err != nil {
			t.Errorf("DecodeTaggedInput: %v", err)
			return
		}
		if session != "sess" || len(data) != 1 || data[0] != 0x01 {
			t.Errorf("decoded input = (%q, %v), want (sess, [1])", session, data)
		}
	}()

	if cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlA}); cmd != nil {
		t.Fatal("first Ctrl+A should not return a command")
	}
	if !m.prefix {
		t.Fatal("prefix = false, want true after first Ctrl+A")
	}
	if cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlA}); cmd != nil {
		t.Fatal("second Ctrl+A should not return a command")
	}
	if m.prefix {
		t.Fatal("prefix = true, want false after double Ctrl+A")
	}

	<-done
}

func TestHandlePrefixKey_CreateSessionUsesDefaultShell(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	m := newTUIModel(&daemonConn{conn: client}, "", nil)
	m.width = 90
	m.height = 33

	done := make(chan struct{})
	go func() {
		defer close(done)
		ft, payload, err := daemon.ReadFrame(server)
		if err != nil {
			t.Errorf("ReadFrame: %v", err)
			return
		}
		if ft != daemon.FrameCreateSession {
			t.Errorf("frame type = %v, want %v", ft, daemon.FrameCreateSession)
			return
		}
		var req daemon.CreateSessionRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			t.Errorf("json.Unmarshal: %v", err)
			return
		}
		if len(req.Command) != 1 || req.Command[0] == "" {
			t.Errorf("command = %#v, want default shell", req.Command)
		}
		if req.Cols != 90 || req.Rows != 33 {
			t.Errorf("size = %dx%d, want 90x33", req.Cols, req.Rows)
		}
	}()

	if cmd := m.handlePrefixKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}}); cmd != nil {
		t.Fatal("create session should not return a command")
	}

	<-done
}

func TestUpdate_SubscribedAutoCreatesSessionWhenEmpty(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	m := newTUIModel(&daemonConn{conn: client}, "", []string{"bash", "-lc", "echo hi"})
	m.width = 120
	m.height = 40

	done := make(chan struct{})
	go func() {
		defer close(done)
		ft, payload, err := daemon.ReadFrame(server)
		if err != nil {
			t.Errorf("ReadFrame: %v", err)
			return
		}
		if ft != daemon.FrameCreateSession {
			t.Errorf("frame type = %v, want %v", ft, daemon.FrameCreateSession)
			return
		}
		var req daemon.CreateSessionRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			t.Errorf("json.Unmarshal: %v", err)
			return
		}
		if got, want := req.Command, []string{"bash", "-lc", "echo hi"}; !slices.Equal(got, want) {
			t.Errorf("command = %#v, want %#v", got, want)
		}
		if req.Cols != 120 || req.Rows != 40 {
			t.Errorf("size = %dx%d, want 120x40", req.Cols, req.Rows)
		}
	}()

	model, cmd := m.Update(daemonSubscribedMsg{})
	if model != m || cmd != nil {
		t.Fatalf("Update() = (%#v, %#v), want same model and nil cmd", model, cmd)
	}
	if m.autoCreate != nil {
		t.Fatalf("autoCreate = %#v, want nil after subscribe", m.autoCreate)
	}

	<-done
}

func TestTUIKeyToBytes_CommonMappings(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.KeyMsg
		want []byte
	}{
		{name: "enter", msg: tea.KeyMsg{Type: tea.KeyEnter}, want: []byte{'\r'}},
		{name: "tab", msg: tea.KeyMsg{Type: tea.KeyTab}, want: []byte{'\t'}},
		{name: "escape", msg: tea.KeyMsg{Type: tea.KeyEscape}, want: []byte{0x1b}},
		{name: "up", msg: tea.KeyMsg{Type: tea.KeyUp}, want: []byte{0x1b, '[', 'A'}},
		{name: "ctrl-c", msg: tea.KeyMsg{Type: tea.KeyCtrlC}, want: []byte{0x03}},
		{name: "runes", msg: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'λ'}}, want: []byte("λ")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tuiKeyToBytes(tc.msg)
			if string(got) != string(tc.want) {
				t.Fatalf("tuiKeyToBytes(%v) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}
