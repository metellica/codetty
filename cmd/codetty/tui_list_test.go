package main

import (
	"codetty/daemon"

	"net"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
)

func newListTestModel() *tuiModel {
	m := newTUIModel(nil, "", nil)
	m.width = 120
	m.height = 40
	m.sessions = []*tuiSession{
		newTUISession("alpha", "bash", 80, 24),
		newTUISession("beta", "bash", 80, 24),
		newTUISession("gamma", "bash", 80, 24),
	}
	m.order = []string{"alpha", "beta", "gamma"}
	m.mode = modeList
	m.selected = 0
	return m
}

func TestListMode_ShiftTabCyclesListThenBigPanel(t *testing.T) {
	m := newListTestModel()
	m.selected = 1

	pressPrefixTab(m)
	if !m.listFocus {
		t.Fatal("listFocus should be true after Shift+Tab")
	}
	if m.listCursor != 0 {
		t.Fatalf("listCursor = %d, want 0 (starts at first entry)", m.listCursor)
	}

	// Continues through the list…
	pressPrefixTab(m)
	if !m.listFocus || m.listCursor != 1 {
		t.Fatalf("listFocus/cursor = %v/%d, want true/1", m.listFocus, m.listCursor)
	}
	pressPrefixTab(m)
	if !m.listFocus || m.listCursor != 2 {
		t.Fatalf("listFocus/cursor = %v/%d, want true/2", m.listFocus, m.listCursor)
	}

	// …and returns to the big panel after the last entry.
	pressPrefixTab(m)
	if m.listFocus {
		t.Fatal("listFocus should return to big panel after last entry")
	}
	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1 (cycling must not switch)", m.selected)
	}
}

func TestListKey_UpDownEnterSwitchesSelection(t *testing.T) {
	m := newListTestModel()
	pressPrefixTab(m)

	m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.listCursor != 2 {
		t.Fatalf("listCursor = %d, want 2", m.listCursor)
	}
	// Wraps around.
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.listCursor != 0 {
		t.Fatalf("listCursor = %d, want 0 (wrap)", m.listCursor)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	if m.listCursor != 2 {
		t.Fatalf("listCursor = %d, want 2 (wrap up)", m.listCursor)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.selected != 2 {
		t.Fatalf("selected = %d, want 2 after Enter", m.selected)
	}
	if m.listFocus {
		t.Fatal("listFocus should return to big panel after Enter")
	}
	if m.mode != modeList {
		t.Fatalf("mode = %v, want modeList", m.mode)
	}
}

func TestListKey_EscapeAndOtherKeysReturnFocus(t *testing.T) {
	m := newListTestModel()
	pressPrefixTab(m)
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown})

	m.handleKey(tea.KeyMsg{Type: tea.KeyEscape})
	if m.listFocus {
		t.Fatal("Escape should drop list focus")
	}
	if m.selected != 0 {
		t.Fatalf("selected = %d, want 0 (Escape must not switch)", m.selected)
	}

	pressPrefixTab(m)
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.listFocus {
		t.Fatal("printable key should drop list focus")
	}
}

func TestListKey_UpDownTraversesAgentRows(t *testing.T) {
	m := newListTestModel()
	m.sessions[1].Tabs = []tuiTab{
		{Name: "sh"},
		{Name: "cp", Agent: "copilot", AgentState: "working"},
		{Name: "cl", Agent: "claude", AgentState: "blocked"},
	}
	// Focus the list at the first header directly.
	m.listFocus = true
	m.listCursor = 0
	m.listCursorTab = -1

	// Agent rows are not selectable. Down: alpha → beta → gamma → wrap alpha.
	steps := []struct{ sess, tab int }{
		{1, -1}, {2, -1}, {0, -1},
	}
	for i, want := range steps {
		m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
		if m.listCursor != want.sess || m.listCursorTab != want.tab {
			t.Fatalf("step %d: cursor = (%d, %d), want (%d, %d)",
				i, m.listCursor, m.listCursorTab, want.sess, want.tab)
		}
	}

	// Up wraps back to gamma.
	m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	if m.listCursor != 2 || m.listCursorTab != -1 {
		t.Fatalf("cursor = (%d, %d), want (2, -1)", m.listCursor, m.listCursorTab)
	}
}

func TestListKey_EnterOnSessionUsePriorityTab(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	defer client.Close()
	go func() {
		buf := make([]byte, 1024)
		for {
			if _, err := server.Read(buf); err != nil {
				return
			}
		}
	}()

	m := newListTestModel()
	m.conn = &daemonConn{conn: client}
	m.sessions[1].Tabs = []tuiTab{
		{Name: "sh"},
		{Name: "cp", Agent: "copilot", AgentState: "blocked"},
	}
	m.sessions[1].ActiveTab = 0

	// Shift+Tab lands on session with noticed agent (beta has blocked).
	pressPrefixTab(m)
	if m.listCursor != 1 || m.listCursorTab != -1 {
		t.Fatalf("cursor = (%d, %d), want (1, -1)", m.listCursor, m.listCursorTab)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})

	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1", m.selected)
	}
	if m.listFocus {
		t.Fatal("Enter should drop list focus")
	}
	// Priority tab should be the blocked agent (tab 1), not active tab 0.
	if !m.pendingTabAck["beta"] {
		t.Fatal("Enter on session with blocked agent should request a tab switch")
	}
}

func TestListKey_EnterOnHeaderDoesNotSwitchTab(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	defer client.Close()
	go func() {
		buf := make([]byte, 1024)
		for {
			if _, err := server.Read(buf); err != nil {
				return
			}
		}
	}()

	m := newListTestModel()
	m.conn = &daemonConn{conn: client}
	m.sessions[1].Tabs = []tuiTab{
		{Name: "sh"},
		{Name: "cp", Agent: "copilot", AgentState: "working"},
	}
	m.sessions[1].ActiveTab = 0

	pressPrefixTab(m)
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown}) // beta header
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})

	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1", m.selected)
	}
	if m.pendingTabAck["beta"] {
		t.Fatal("Enter on a header row must not request a tab switch")
	}
}

func TestListMode_ShiftTabPrioritizesAttentionAgents(t *testing.T) {
	m := newListTestModel()
	m.sessions[0].Tabs = []tuiTab{
		{Name: "sh"},
		{Name: "cp", Agent: "copilot", AgentState: "idle", Done: true}, // done
	}
	m.sessions[2].Tabs = []tuiTab{
		{Name: "cl", Agent: "claude", AgentState: "blocked"},
	}

	// Noticed agents are at session level only. Priority: blocked (sess 2)
	// first, then done (sess 0), then remaining session 1, then big panel.
	steps := []struct{ sess, tab int }{
		{2, -1}, {0, -1}, {1, -1},
	}
	for i, want := range steps {
		pressPrefixTab(m)
		if !m.listFocus || m.listCursor != want.sess || m.listCursorTab != want.tab {
			t.Fatalf("step %d: focus/cursor = %v/(%d, %d), want true/(%d, %d)",
				i, m.listFocus, m.listCursor, m.listCursorTab, want.sess, want.tab)
		}
	}
	pressPrefixTab(m)
	if m.listFocus {
		t.Fatal("listFocus should return to big panel after last entry")
	}
}

func TestListView_ShowsSessionsAndAgents(t *testing.T) {
	m := newListTestModel()
	m.sessions[1].Tabs = []tuiTab{
		{Name: "sh", Command: "bash"},
		{Name: "cp", Command: "copilot", Agent: "copilot", AgentState: "working"},
		{Name: "cl", Command: "claude", Agent: "claude", AgentState: "blocked"},
	}

	view := xansi.Strip(m.listView())
	for _, want := range []string{"Sessions", "1 alpha", "2 beta", "3 gamma", "copilot working", "claude blocked"} {
		if !strings.Contains(view, want) {
			t.Fatalf("listView() missing %q in:\n%s", want, view)
		}
	}
}

func TestListView_DoneMarkerShown(t *testing.T) {
	m := newListTestModel()
	m.sessions[0].Tabs = []tuiTab{
		{Name: "cp", Agent: "copilot", AgentState: "idle", Done: true},
	}

	view := xansi.Strip(m.listView())
	if !strings.Contains(view, "copilot done") {
		t.Fatalf("listView() missing done marker in:\n%s", view)
	}
}

func TestListMode_MouseClickSidebarSwitches(t *testing.T) {
	m := newListTestModel()

	// Click the third session's header row: sidebar is on the left, rows
	// start at y=2 (border + title), each session is boxed (top border,
	// header, bottom border): gamma's header is at y=9.
	m.handleMouse(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      2,
		Y:      9,
	})
	if m.selected != 2 {
		t.Fatalf("selected = %d, want 2 after sidebar click", m.selected)
	}
	if m.floatingIdx != -1 {
		t.Fatalf("floatingIdx = %d, want -1 (no floating from sidebar)", m.floatingIdx)
	}
}

func TestListMode_PanelRegionsOnlyBigPanel(t *testing.T) {
	m := newListTestModel()
	regions := m.panelRegions()
	if len(regions) != 1 {
		t.Fatalf("len(regions) = %d, want 1", len(regions))
	}
	if regions[0].sessionIdx != m.selected {
		t.Fatalf("region sessionIdx = %d, want %d", regions[0].sessionIdx, m.selected)
	}
	mainW, _ := m.listLayoutWidths()
	if regions[0].contentW != mainW-2 {
		t.Fatalf("contentW = %d, want %d", regions[0].contentW, mainW-2)
	}
}

func TestListMode_PrefixDigitKeepsListMode(t *testing.T) {
	m := newListTestModel()
	m.prefix = true
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1", m.selected)
	}
	if m.mode != modeList {
		t.Fatalf("mode = %v, want modeList (digit must not leave list mode)", m.mode)
	}
}

func TestListMode_PrefixSSwitchesToListMode(t *testing.T) {
	m := newListTestModel()
	m.mode = modeFullscreen
	m.prefix = true
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if m.mode != modeList {
		t.Fatalf("mode = %v, want modeList", m.mode)
	}
}

func TestListMode_PrefixFEntersAndExitsFullscreen(t *testing.T) {
	m := newListTestModel()
	m.prefix = true
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if m.mode != modeFullscreen {
		t.Fatalf("mode = %v, want fullscreen", m.mode)
	}
	m.prefix = true
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if m.mode != modeList {
		t.Fatalf("mode = %v, want modeList (pop back)", m.mode)
	}
}

func TestListHitTest_ScrolledList(t *testing.T) {
	m := newListTestModel()
	m.listScroll = 4
	_, sideW := m.listLayoutWidths()

	// Row y=2 is the first visible line; with scroll offset 4 it maps to
	// beta's header row (each session box is 3 lines: border, header,
	// border). Sidebar is on the left.
	if idx, tabIdx := m.listHitTest(2, 2); idx != 1 || tabIdx != -1 {
		t.Fatalf("listHitTest = (%d, %d), want (1, -1)", idx, tabIdx)
	}
	// Outside the sidebar content area (big panel area on the right).
	if idx, _ := m.listHitTest(sideW+1, 2); idx != -1 {
		t.Fatalf("listHitTest = %d, want -1 for big panel area", idx)
	}
}

func TestListHitTest_AgentRowReturnsTabIndex(t *testing.T) {
	m := newListTestModel()
	m.sessions[1].Tabs = []tuiTab{
		{Name: "sh"},
		{Name: "cp", Agent: "copilot", AgentState: "working"},
		{Name: "cl", Agent: "claude", AgentState: "blocked"},
	}

	// Sidebar is on left; each session is boxed. Rows: y=2..4 alpha box,
	// y=5 beta top border, y=6 beta header, y=7 copilot(tab 1),
	// y=8 claude(tab 2), y=9 beta bottom border, y=10..12 gamma box.
	if idx, tabIdx := m.listHitTest(2, 6); idx != 1 || tabIdx != -1 {
		t.Fatalf("header row = (%d, %d), want (1, -1)", idx, tabIdx)
	}
	if idx, tabIdx := m.listHitTest(2, 7); idx != 1 || tabIdx != 1 {
		t.Fatalf("copilot row = (%d, %d), want (1, 1)", idx, tabIdx)
	}
	if idx, tabIdx := m.listHitTest(2, 8); idx != 1 || tabIdx != 2 {
		t.Fatalf("claude row = (%d, %d), want (1, 2)", idx, tabIdx)
	}
}

func TestListMode_MouseClickAgentRowSelectsSession(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	defer client.Close()
	// Drain frames written by SwitchTab so the pipe write doesn't block.
	go func() {
		buf := make([]byte, 1024)
		for {
			if _, err := server.Read(buf); err != nil {
				return
			}
		}
	}()

	m := newListTestModel()
	m.conn = &daemonConn{conn: client}
	m.sessions[1].Tabs = []tuiTab{
		{Name: "sh"},
		{Name: "cp", Agent: "copilot", AgentState: "blocked"},
	}
	m.sessions[1].ActiveTab = 0

	// Click the copilot agent row. Boxed sessions: y=2..4 alpha box,
	// y=5 beta top border, y=6 beta header, y=7 copilot(tab 1),
	// y=8 beta bottom border, y=9..11 gamma box.
	m.handleMouse(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      2,
		Y:      7,
	})

	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1", m.selected)
	}
	// listCursorTab should be -1 (agents not selectable).
	if m.listCursorTab != -1 {
		t.Fatalf("listCursorTab = %d, want -1", m.listCursorTab)
	}
	// Priority tab is the blocked agent (tab 1), so a switch is requested.
	if !m.pendingTabAck["beta"] {
		t.Fatal("clicking an agent row should select session and switch to priority tab")
	}
}

func TestListMode_MouseClickHeaderDoesNotSwitchTab(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	defer client.Close()
	go func() {
		buf := make([]byte, 1024)
		for {
			if _, err := server.Read(buf); err != nil {
				return
			}
		}
	}()

	m := newListTestModel()
	m.conn = &daemonConn{conn: client}
	m.sessions[1].Tabs = []tuiTab{
		{Name: "sh"},
		{Name: "cp", Agent: "copilot", AgentState: "working"},
	}

	// Click beta's header row (y=6: after alpha's 3-line box and beta's
	// top border). Sidebar on left.
	m.handleMouse(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      2,
		Y:      6,
	})

	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1", m.selected)
	}
	if m.pendingTabAck["beta"] {
		t.Fatal("header click must not request a tab switch")
	}
}

// pipeCapture drains a net.Pipe end and accumulates the bytes for assertions.
type pipeCapture struct {
	mu  sync.Mutex
	buf []byte
}

func (c *pipeCapture) drain(conn net.Conn) {
	buf := make([]byte, 1024)
	for {
		n, err := conn.Read(buf)
		c.mu.Lock()
		c.buf = append(c.buf, buf[:n]...)
		c.mu.Unlock()
		if err != nil {
			return
		}
	}
}

func (c *pipeCapture) contains(sub string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Contains(string(c.buf), sub)
}

func (c *pipeCapture) waitFor(t *testing.T, sub string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if c.contains(sub) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	t.Fatalf("pipe output %q does not contain %q", c.buf, sub)
}

func TestListMode_BigPanelPressForwardedToMouseApp(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	defer client.Close()
	cap := &pipeCapture{}
	go cap.drain(server)

	m := newListTestModel()
	m.conn = &daemonConn{conn: client}
	_, sideW := m.listLayoutWidths()
	// Enable mouse reporting on the big-panel session (like a multiplexer/copilot).
	m.sessions[0].WriteOutput([]byte("\x1b[?1003h\x1b[?1006h"))

	press := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: sideW + 5, Y: 4}
	m.handleMouse(press)

	// Same as the floating window: the press starts a pending local
	// selection and nothing reaches the PTY yet.
	if !m.sel.pending {
		t.Fatal("press must start a pending selection even for mouse-enabled apps")
	}
	if m.mouseFwdDown {
		t.Fatal("mouseFwdDown must not be set on a left press")
	}

	release := tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, X: sideW + 5, Y: 4}
	m.handleMouse(release)

	// Click without drag: press+release forwarded to the app.
	// contentX=sideW+1, contentY=1 → local (4,3) → SGR 1-based (5,4).
	cap.waitFor(t, "\x1b[<0;5;4M")
	cap.waitFor(t, "\x1b[<0;5;4m")
}

func TestListMode_BigPanelPressStartsSelectionWithoutMouseApp(t *testing.T) {
	m := newListTestModel()
	_, sideW := m.listLayoutWidths()

	m.handleMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: sideW + 5, Y: 4})

	if !m.sel.pending {
		t.Fatal("press on a plain shell should start a pending text selection")
	}
	if m.mouseFwdDown {
		t.Fatal("mouseFwdDown must not be set for plain shells")
	}
}

func TestListMode_SidebarClickDoesNotForwardRelease(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	defer client.Close()
	cap := &pipeCapture{}
	go cap.drain(server)

	m := newListTestModel()
	m.conn = &daemonConn{conn: client}
	m.sessions[0].WriteOutput([]byte("\x1b[?1003h\x1b[?1006h"))

	// Click a sidebar row, then release: nothing should reach the PTY.
	// Sidebar is on the left.
	m.handleMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 2, Y: 3})
	m.handleMouse(tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, X: 2, Y: 3})

	time.Sleep(20 * time.Millisecond)
	if cap.contains("\x1b[<") {
		t.Fatal("sidebar click must not forward mouse events to the PTY")
	}
}

func TestResizeSessionPTYs_ReservesTabBarRow(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	defer client.Close()
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := server.Read(buf); err != nil {
				return
			}
		}
	}()

	m := newListTestModel()
	m.conn = &daemonConn{conn: client}
	m.sessions[1].Tabs = []tuiTab{{Name: "sh"}, {Name: "cp"}}

	m.resizeSessionPTYs()

	cols, rows := m.focusedContentSize()
	if got := m.sessions[0].Rows; int(got) != rows {
		t.Fatalf("single-tab session rows = %d, want %d", got, rows)
	}
	// In list mode the tab bar is part of the bottom info line (already
	// accounted for by focusedContentSize), so no extra row is deducted.
	if got := m.sessions[1].Rows; int(got) != rows {
		t.Fatalf("tab-bar session rows = %d, want %d (info line includes tab bar)", got, rows)
	}
	if got := m.sessions[0].Cols; int(got) != cols {
		t.Fatalf("cols = %d, want %d", got, cols)
	}
}

func TestSyncTabInfo_RefitsPTYWhenTabBarAppears(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	defer client.Close()
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := server.Read(buf); err != nil {
				return
			}
		}
	}()

	m := newListTestModel()
	m.conn = &daemonConn{conn: client}
	m.resizeSessionPTYs()
	cols, rows := m.focusedContentSize()
	_ = cols

	// Second tab appears: in list mode the tab bar is on the info line,
	// so PTY size doesn't change.
	m.syncTabInfo(m.sessions[0], daemon.SessionInfo{
		Tabs:      []daemon.TabInfo{{Name: "sh"}, {Name: "cp"}},
		ActiveTab: 1,
	})
	if got := m.sessions[0].Rows; int(got) != rows {
		t.Fatalf("rows after tab bar appears = %d, want %d", got, rows)
	}

	// Back to one tab: same size.
	m.syncTabInfo(m.sessions[0], daemon.SessionInfo{
		Tabs:      []daemon.TabInfo{{Name: "sh"}},
		ActiveTab: 0,
	})
	if got := m.sessions[0].Rows; int(got) != rows {
		t.Fatalf("rows after tab bar disappears = %d, want %d", got, rows)
	}
}

func TestListMode_BigPanelRightClickPastesLastCopy(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	defer client.Close()
	cap := &pipeCapture{}
	go cap.drain(server)

	m := newListTestModel()
	m.conn = &daemonConn{conn: client}
	_, sideW := m.listLayoutWidths()
	m.clipText = "hello copy"
	m.sessions[0].WriteOutput([]byte("\x1b[?1003h\x1b[?1006h\x1b[?2004h"))

	press := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonRight, X: sideW + 5, Y: 4}
	m.handleMouse(press)
	if m.mouseFwdDown {
		t.Fatal("right-click paste must not set mouseFwdDown")
	}
	cap.waitFor(t, "\x1b[200~hello copy\x1b[201~")
	if cap.contains("\x1b[<") {
		t.Fatal("right click must not be forwarded as a mouse event")
	}
}

func TestListMode_BigPanelRightClickNoCopyIsNoop(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	defer client.Close()
	cap := &pipeCapture{}
	go cap.drain(server)

	m := newListTestModel()
	m.conn = &daemonConn{conn: client}
	_, sideW := m.listLayoutWidths()

	m.handleMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonRight, X: sideW + 5, Y: 4})
	m.handleMouse(tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonRight, X: sideW + 5, Y: 4})

	time.Sleep(20 * time.Millisecond)
	if cap.contains("\x1b[<") {
		t.Fatal("right click must not reach the PTY as a mouse event")
	}
}

func TestListMode_DragOverMouseAppSelectsLocally(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	defer client.Close()
	cap := &pipeCapture{}
	go cap.drain(server)

	m := newListTestModel()
	m.conn = &daemonConn{conn: client}
	_, sideW := m.listLayoutWidths()
	m.sessions[0].WriteOutput([]byte("hello world"))
	m.sessions[0].WriteOutput([]byte("\x1b[?1003h\x1b[?1006h"))

	m.handleMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: sideW + 1, Y: 1})
	m.handleMouse(tea.MouseMsg{Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft, X: sideW + 8, Y: 1})
	if !m.sel.active {
		t.Fatal("drag over a mouse-enabled app must run the local selection")
	}
	cmd := m.handleMouse(tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, X: sideW + 8, Y: 1})
	if cmd == nil {
		t.Fatal("release after drag should copy the selection to the clipboard")
	}

	time.Sleep(20 * time.Millisecond)
	if cap.contains("\x1b[<") {
		t.Fatal("drag-selection must not forward mouse events to the PTY")
	}
}
