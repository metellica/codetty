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

	// Press 'c' in prefix mode
	if cmd := m.handlePrefixKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}}); cmd != nil {
		t.Fatal("create session should not return a command")
	}

	// If tmux is available, we enter chooser mode first; select 'c' for codetty
	if daemon.IsTmuxAvailable() {
		if !m.createChooser {
			t.Fatal("createChooser = false, want true after prefix+c (tmux available)")
		}
		m.handleCreateChooserKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	}

	if !m.creating {
		t.Fatal("creating = false, want true after type selection")
	}

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
		if req.Tmux {
			t.Errorf("tmux = true, want false for codetty session")
		}
	}()

	// Press Enter — should send CreateSession frame
	m.handleCreateKey(tea.KeyMsg{Type: tea.KeyEnter})

	<-done
}

func TestHandlePrefixKey_CreateTmuxSession(t *testing.T) {
	if !daemon.IsTmuxAvailable() {
		t.Skip("tmux not available")
	}

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	m := newTUIModel(&daemonConn{conn: client}, "", nil)
	m.width = 90
	m.height = 33

	// Press 'c' in prefix mode — enters chooser
	m.handlePrefixKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if !m.createChooser {
		t.Fatal("createChooser = false, want true")
	}

	// Select 't' for tmux
	m.handleCreateChooserKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if !m.creating || !m.createTmux {
		t.Fatalf("creating=%v createTmux=%v, want both true", m.creating, m.createTmux)
	}

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
		if !req.Tmux {
			t.Errorf("tmux = false, want true")
		}
		if req.Name != "mysess" {
			t.Errorf("name = %q, want %q", req.Name, "mysess")
		}
	}()

	// Type name and press Enter
	m.handleCreateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("mysess")})
	m.handleCreateKey(tea.KeyMsg{Type: tea.KeyEnter})

	<-done
}

func TestHandleCreateChooserKey_EscCancels(t *testing.T) {
	m := newTUIModel(nil, "", nil)
	m.createChooser = true

	m.handleCreateChooserKey(tea.KeyMsg{Type: tea.KeyEscape})
	if m.createChooser {
		t.Fatal("createChooser should be false after Esc")
	}
	if m.creating {
		t.Fatal("creating should be false after Esc")
	}
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

func TestHandlePrefixKey_SessionManager(t *testing.T) {
	m := newTUIModel(nil, "", nil)
	m.width = 100
	m.height = 30
	m.sessions = []*tuiSession{newTUISession("sess", "bash", 80, 24)}
	m.order = []string{"sess"}

	// Press Ctrl+A t — should open session manager
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlA})
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})

	if !m.sessMgr.active {
		t.Fatal("session manager should be active after prefix+t")
	}
	items := m.sessionManagerItems()
	if len(items) != 1 {
		t.Fatalf("expected 1 session manager item, got %d", len(items))
	}
	if items[0].Name != "sess" {
		t.Fatalf("expected session name 'sess', got '%s'", items[0].Name)
	}
}

func TestTmuxPicker_KeyHandling(t *testing.T) {
	m := newTUIModel(nil, "", nil)
	m.width = 100
	m.height = 30
	m.tmuxPicker = tmuxPicker{
		items: []tmuxPickerItem{
			{Name: "sess1", Selected: true},
			{Name: "sess2", Selected: true},
			{Name: "sess3", Selected: false},
		},
		cursor: 0,
		active: true,
	}

	// Move down
	m.handleTmuxPickerKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.tmuxPicker.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.tmuxPicker.cursor)
	}

	// Toggle with space
	m.handleTmuxPickerKey(tea.KeyMsg{Type: tea.KeySpace})
	if m.tmuxPicker.items[1].Selected {
		t.Fatal("item 1 should be deselected after space")
	}

	// Select all with 'a'
	m.handleTmuxPickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	for i, item := range m.tmuxPicker.items {
		if !item.Selected {
			t.Fatalf("item %d should be selected after 'a'", i)
		}
	}

	// Deselect all with 'n'
	m.handleTmuxPickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	for i, item := range m.tmuxPicker.items {
		if item.Selected {
			t.Fatalf("item %d should be deselected after 'n'", i)
		}
	}

	// Esc closes picker
	m.handleTmuxPickerKey(tea.KeyMsg{Type: tea.KeyEscape})
	if m.tmuxPicker.active {
		t.Fatal("picker should be inactive after Esc")
	}
}


func TestHandleSessionEvent_TmuxTypePreserved(t *testing.T) {
	m := newTUIModel(nil, "", nil)
	m.width = 100
	m.height = 30

	m.handleSessionEvent(daemon.SessionEvent{
		Type:    "created",
		Session: "[tmux] myserver",
		Cols:    80,
		Rows:    24,
		Info: daemon.SessionInfo{
			Command:   "tmux",
			Type:      "tmux",
			CreatedAt: "2025-01-01T00:00:00Z",
		},
	})

	sess := m.sessionByName("[tmux] myserver")
	if sess == nil {
		t.Fatal("tmux session not found")
	}
	if sess.Type != "tmux" {
		t.Fatalf("session Type = %q, want %q", sess.Type, "tmux")
	}
	if sess.Command != "tmux" {
		t.Fatalf("session Command = %q, want %q", sess.Command, "tmux")
	}
}

func TestHideShowSession(t *testing.T) {
	m := newTUIModel(nil, "", nil)
	m.width = 160
	m.height = 40
	m.sessions = []*tuiSession{
		newTUISession("alpha", "bash", 80, 24),
		newTUISession("beta", "bash", 80, 24),
		newTUISession("gamma", "bash", 80, 24),
	}
	m.order = []string{"alpha", "beta", "gamma"}

	if got := len(m.visibleSessions()); got != 3 {
		t.Fatalf("expected 3 visible, got %d", got)
	}

	m.hideSession("beta")
	visible := m.visibleSessions()
	if len(visible) != 2 {
		t.Fatalf("expected 2 visible after hide, got %d", len(visible))
	}
	if visible[0].Name != "alpha" || visible[1].Name != "gamma" {
		t.Fatalf("unexpected visible sessions: %v, %v", visible[0].Name, visible[1].Name)
	}

	m.showSession("beta")
	if got := len(m.visibleSessions()); got != 3 {
		t.Fatalf("expected 3 visible after show, got %d", got)
	}
}

func TestHideSession_AdjustsSelected(t *testing.T) {
	m := newTUIModel(nil, "", nil)
	m.width = 160
	m.height = 40
	m.sessions = []*tuiSession{
		newTUISession("a", "bash", 80, 24),
		newTUISession("b", "bash", 80, 24),
	}
	m.order = []string{"a", "b"}
	m.selected = 1

	m.hideSession("b")
	visible := m.visibleSessions()
	if len(visible) != 1 {
		t.Fatalf("expected 1 visible, got %d", len(visible))
	}
	if m.selected != 0 {
		t.Fatalf("expected selected=0 after hiding, got %d", m.selected)
	}
}

func TestReorderByVisibleIdx(t *testing.T) {
	m := newTUIModel(nil, "", nil)
	m.width = 160
	m.height = 40
	m.sessions = []*tuiSession{
		newTUISession("a", "bash", 80, 24),
		newTUISession("b", "bash", 80, 24),
		newTUISession("c", "bash", 80, 24),
	}
	m.order = []string{"a", "b", "c"}

	m.reorderByVisibleIdx(0, 2)

	if m.order[0] != "b" || m.order[1] != "a" || m.order[2] != "c" {
		t.Fatalf("unexpected order after reorder: %v", m.order)
	}
}

func TestReorderByVisibleIdx_WithHidden(t *testing.T) {
	m := newTUIModel(nil, "", nil)
	m.width = 160
	m.height = 40
	m.sessions = []*tuiSession{
		newTUISession("a", "bash", 80, 24),
		newTUISession("b", "bash", 80, 24),
		newTUISession("c", "bash", 80, 24),
	}
	m.order = []string{"a", "b", "c"}
	m.hideSession("b")

	m.reorderByVisibleIdx(1, 0)

	visible := m.visibleSessions()
	if len(visible) != 2 {
		t.Fatalf("expected 2 visible, got %d", len(visible))
	}
	if visible[0].Name != "c" || visible[1].Name != "a" {
		t.Fatalf("unexpected visible order: %v, %v", visible[0].Name, visible[1].Name)
	}
}

func TestSessionManagerItems(t *testing.T) {
	m := newTUIModel(nil, "", nil)
	m.sessions = []*tuiSession{
		newTUISession("shell1", "bash", 80, 24),
		newTUISession("shell2", "bash", 80, 24),
	}
	m.sessions[1].Type = "tmux"
	m.order = []string{"shell1", "shell2"}
	m.hideSession("shell1")

	items := m.sessionManagerItems()
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if !items[0].Hidden {
		t.Fatal("shell1 should be hidden")
	}
	if items[1].Hidden {
		t.Fatal("shell2 should not be hidden")
	}
	if items[1].Type != "tmux" {
		t.Fatal("shell2 should be tmux type")
	}
}

func TestSessionManager_ToggleVisibility(t *testing.T) {
	m := newTUIModel(nil, "", nil)
	m.width = 100
	m.height = 30
	m.sessions = []*tuiSession{
		newTUISession("s1", "bash", 80, 24),
		newTUISession("s2", "bash", 80, 24),
	}
	m.order = []string{"s1", "s2"}

	m.openSessionManager()
	if !m.sessMgr.active {
		t.Fatal("session manager should be active")
	}

	m.handleSessionManagerKey(tea.KeyMsg{Type: tea.KeySpace})
	if !m.hidden["s1"] {
		t.Fatal("s1 should be hidden after toggle")
	}

	m.handleSessionManagerKey(tea.KeyMsg{Type: tea.KeySpace})
	if m.hidden["s1"] {
		t.Fatal("s1 should be visible after second toggle")
	}
}

func TestRemoveSession_CleansHidden(t *testing.T) {
	m := newTUIModel(nil, "", nil)
	m.width = 160
	m.height = 40
	m.sessions = []*tuiSession{
		newTUISession("a", "bash", 80, 24),
		newTUISession("b", "bash", 80, 24),
	}
	m.order = []string{"a", "b"}
	m.hideSession("a")

	if !m.hidden["a"] {
		t.Fatal("a should be hidden")
	}

	m.removeSession("a")
	if m.hidden["a"] {
		t.Fatal("hidden state should be cleaned up after remove")
	}
}

func TestUpdate_PrefixKeyViaUpdate(t *testing.T) {
	// Test that prefix key works through the full Update() path
	// (not just handleKey), verifying confirmKill and sessMgr checks don't interfere
	m := newTUIModel(nil, "", nil)
	m.width = 100
	m.height = 30
	m.sessions = []*tuiSession{newTUISession("sess", "bash", 80, 24)}
	m.order = []string{"sess"}

	// Send Ctrl+A through Update
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if cmd != nil {
		t.Fatal("Ctrl+A should not return a command")
	}
	if !m.prefix {
		t.Fatal("prefix should be true after Ctrl+A via Update")
	}

	// Send 'c' through Update (should enter create mode)
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd != nil {
		t.Fatal("prefix+c should not return a command")
	}
	if m.prefix {
		t.Fatal("prefix should be cleared after second key")
	}
	if !m.creating && !m.createChooser {
		t.Fatal("should be in creating or createChooser mode after prefix+c")
	}
}

func TestUpdate_PrefixKeyNotInterceptedBySessionManager(t *testing.T) {
	// Verify that sessMgr.active = false doesn't intercept keys
	m := newTUIModel(nil, "", nil)
	m.width = 100
	m.height = 30
	m.sessions = []*tuiSession{newTUISession("sess", "bash", 80, 24)}
	m.order = []string{"sess"}

	// Ensure sessMgr is inactive
	if m.sessMgr.active {
		t.Fatal("sessMgr should be inactive by default")
	}
	if m.confirmKill {
		t.Fatal("confirmKill should be false by default")
	}

	// Ctrl+A should still work
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if !m.prefix {
		t.Fatal("prefix should be true after Ctrl+A")
	}

	// 'g' should toggle grid mode
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if m.mode != modeGrid {
		t.Fatalf("mode should be grid after prefix+g, got %d", m.mode)
	}
}
