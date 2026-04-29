package hub

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"codetty/protocol"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/gorilla/websocket"
)

var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

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

func stripANSI(s string) string {
	return ansiEscapeRE.ReplaceAllString(s, "")
}

func renderedHeight(s string) int {
	trimmed := strings.TrimRight(s, "\n")
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "\n"))
}

func renderedMaxWidth(s string) int {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	maxWidth := 0
	for _, line := range lines {
		if width := lipgloss.Width(line); width > maxWidth {
			maxWidth = width
		}
	}
	return maxWidth
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

func TestModel_PrefixFixedColsAndAuto(t *testing.T) {
	m := NewModel(New())

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if cmd != nil {
		t.Fatal("Ctrl+A should enter prefix mode without returning a command")
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if cmd != nil {
		t.Fatal("Ctrl+A 2 should not return a command")
	}
	if m.layout.mode != layoutFixedGrid {
		t.Fatalf("Ctrl+A 2 mode = %s, want fixed", m.layout.modeName())
	}
	if m.layout.fixedCols != 2 {
		t.Fatalf("Ctrl+A 2 fixedCols = %d, want 2", m.layout.fixedCols)
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if cmd != nil {
		t.Fatal("Ctrl+A should enter prefix mode without returning a command")
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd != nil {
		t.Fatal("Ctrl+A a should not return a command")
	}
	if m.layout.mode != layoutAutoGrid {
		t.Fatalf("Ctrl+A a mode = %s, want auto", m.layout.modeName())
	}
	if m.layout.fixedCols != 0 {
		t.Fatalf("Ctrl+A a fixedCols = %d, want 0", m.layout.fixedCols)
	}
}

func TestModel_PrefixFocusBigMode(t *testing.T) {
	m := NewModel(New())
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
		NewClientSession("beta", "beta", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if cmd != nil {
		t.Fatal("Ctrl+A should enter prefix mode without returning a command")
	}
	if !m.layout.prefix {
		t.Fatal("Ctrl+A should enter prefix mode")
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if cmd != nil {
		t.Fatal("Ctrl+A b should set focus-big mode without returning a command")
	}
	if m.layout.prefix {
		t.Fatal("Ctrl+A b should clear prefix mode")
	}
	if m.layout.mode != layoutFocusBig {
		t.Fatalf("Ctrl+A b mode = %s, want focus-big", m.layout.modeName())
	}
}

func TestModel_FocusBigViewUsesSelectedAsPrimary(t *testing.T) {
	m := NewModel(New())
	m.width = 120
	m.height = 24
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
		NewClientSession("beta", "beta", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)
	m.layout.mode = layoutFocusBig
	m.selected = 1

	view := stripANSI(m.View())
	if !strings.Contains(view, "MAIN [2] beta") {
		t.Fatalf("focus-big view should render selected session as primary, got:\n%s", view)
	}
	if !strings.Contains(view, "focus-big | clients: 2") {
		t.Fatalf("focus-big view should include status bar mode, got:\n%s", view)
	}
}

func TestModel_FocusBigNarrowViewUsesSelectedAsPrimary(t *testing.T) {
	m := NewModel(New())
	m.width = 100
	m.height = 24
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
		NewClientSession("beta", "beta", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)
	m.layout.mode = layoutFocusBig
	m.selected = 0

	view := stripANSI(m.View())
	if !strings.Contains(view, "MAIN [1] alpha") {
		t.Fatalf("narrow focus-big view should render selected session as primary, got:\n%s", view)
	}
	if !strings.Contains(view, "focus-big | clients: 2") {
		t.Fatalf("narrow focus-big view should include status bar mode, got:\n%s", view)
	}
}

func TestModel_FocusBigNarrowTinyHeightStaysWithinHeight(t *testing.T) {
	m := NewModel(New())
	m.width = 80
	m.height = 5
	m.layout.mode = layoutFocusBig
	m.selected = 0
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
		NewClientSession("beta", "beta", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)

	view := stripANSI(m.View())
	if !strings.Contains(view, "MAIN [1] alpha") {
		t.Fatalf("narrow focus-big view should render selected session as primary, got:\n%s", view)
	}
	if strings.Contains(view, "[2] beta") || strings.Contains(view, "more") {
		t.Fatalf("narrow focus-big tiny-height view should not render secondaries, got:\n%s", view)
	}
	if got := renderedHeight(view); got > m.height {
		t.Fatalf("narrow focus-big tiny-height view height = %d, want <= %d\n%s", got, m.height, view)
	}
	if got := renderedMaxWidth(view); got > m.width {
		t.Fatalf("narrow focus-big tiny-height view width = %d, want <= %d\n%s", got, m.width, view)
	}
}

func TestModel_FocusBigNarrowOneLineBodyStaysWithinHeight(t *testing.T) {
	m := NewModel(New())
	m.width = 80
	m.height = 3
	m.layout.mode = layoutFocusBig
	m.selected = 0
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
		NewClientSession("beta", "beta", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)

	view := stripANSI(m.View())
	if !strings.Contains(view, "MAIN [1] alpha") {
		t.Fatalf("narrow focus-big view should render selected session as primary, got:\n%s", view)
	}
	if strings.Contains(view, "[2] beta") || strings.Contains(view, "more") {
		t.Fatalf("narrow focus-big one-line-body view should not render secondaries, got:\n%s", view)
	}
	if got := renderedHeight(view); got > m.height {
		t.Fatalf("narrow focus-big one-line-body view height = %d, want <= %d\n%s", got, m.height, view)
	}
	if got := renderedMaxWidth(view); got > m.width {
		t.Fatalf("narrow focus-big one-line-body view width = %d, want <= %d\n%s", got, m.width, view)
	}
}

func TestModel_FocusBigNarrowNoBodyStaysWithinHeight(t *testing.T) {
	m := NewModel(New())
	m.width = 80
	m.height = 2
	m.layout.mode = layoutFocusBig
	m.selected = 0
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
		NewClientSession("beta", "beta", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)

	view := stripANSI(m.View())
	if strings.Contains(view, "[2] beta") || strings.Contains(view, "more") {
		t.Fatalf("narrow focus-big no-body view should not render secondaries, got:\n%s", view)
	}
	if got := renderedHeight(view); got > m.height {
		t.Fatalf("narrow focus-big no-body view height = %d, want <= %d\n%s", got, m.height, view)
	}
	if got := renderedMaxWidth(view); got > m.width {
		t.Fatalf("narrow focus-big no-body view width = %d, want <= %d\n%s", got, m.width, view)
	}
}

func TestModel_FocusBigViewUsesLayoutOrder(t *testing.T) {
	a := NewClientSession("a", "alpha", 80, 24, nil)
	b := NewClientSession("b", "beta", 80, 24, nil)
	m := NewModel(New())
	m.width = 120
	m.height = 24
	m.sessions = []*ClientSession{a, b}
	m.layout.order = []string{"b", "a"}
	m.layout.mode = layoutFocusBig
	m.selected = 0

	view := stripANSI(m.View())
	if !strings.Contains(view, "MAIN [1] beta") {
		t.Fatalf("focus-big view should use ordered sessions for primary, got:\n%s", view)
	}
}

func TestModel_FocusBigWideThresholdStaysWithinHeight(t *testing.T) {
	m := NewModel(New())
	m.width = 120
	m.height = 24
	m.layout.mode = layoutFocusBig
	m.selected = 0
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
		NewClientSession("beta", "beta", 80, 24, nil),
		NewClientSession("gamma", "gamma", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)

	view := stripANSI(m.View())
	if !strings.Contains(view, "MAIN [1]") {
		t.Fatalf("wide focus-big threshold view should render selected session as primary, got:\n%s", view)
	}
	if got := renderedHeight(view); got > m.height {
		t.Fatalf("wide focus-big threshold view height = %d, want <= %d\n%s", got, m.height, view)
	}
	if got := renderedMaxWidth(view); got > m.width {
		t.Fatalf("wide focus-big threshold view width = %d, want <= %d\n%s", got, m.width, view)
	}
}

func TestModel_FocusBigWideTinyHeightStaysWithinHeight(t *testing.T) {
	for _, height := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("height_%d", height), func(t *testing.T) {
			m := NewModel(New())
			m.width = 140
			m.height = height
			m.layout.mode = layoutFocusBig
			m.selected = 0
			m.sessions = []*ClientSession{
				NewClientSession("alpha", "alpha", 80, 24, nil),
				NewClientSession("beta", "beta", 80, 24, nil),
			}
			m.layout.syncSessions(m.sessions)

			view := stripANSI(m.View())
			if got := renderedHeight(view); got > m.height {
				t.Fatalf("wide focus-big tiny-height view height = %d, want <= %d\n%s", got, m.height, view)
			}
			if got := renderedMaxWidth(view); got > m.width {
				t.Fatalf("wide focus-big tiny-height view width = %d, want <= %d\n%s", got, m.width, view)
			}
		})
	}
}

func TestModel_FocusBigWideThresholdStatusOnlyStaysWithinHeight(t *testing.T) {
	m := NewModel(New())
	m.width = 120
	m.height = 1
	m.layout.mode = layoutFocusBig
	m.selected = 0
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
		NewClientSession("beta", "beta", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)

	view := stripANSI(m.View())
	if strings.Contains(view, "MAIN [1]") || strings.Contains(view, "[2] beta") {
		t.Fatalf("wide focus-big status-only view should not render body panels, got:\n%s", view)
	}
	if got := renderedHeight(view); got > m.height {
		t.Fatalf("wide focus-big status-only view height = %d, want <= %d\n%s", got, m.height, view)
	}
	if got := renderedMaxWidth(view); got > m.width {
		t.Fatalf("wide focus-big status-only view width = %d, want <= %d\n%s", got, m.width, view)
	}
}

func TestModel_FocusBigWideManySessionsStaysWithinHeight(t *testing.T) {
	m := NewModel(New())
	m.width = 140
	m.height = 24
	m.layout.mode = layoutFocusBig
	m.selected = 0
	for i := 0; i < 30; i++ {
		m.sessions = append(m.sessions, NewClientSession(
			fmt.Sprintf("session-%02d", i+1),
			fmt.Sprintf("s%02d", i+1),
			80,
			24,
			nil,
		))
	}
	m.layout.syncSessions(m.sessions)

	view := stripANSI(m.View())
	if !strings.Contains(view, "MAIN [1]") {
		t.Fatalf("wide focus-big view should render selected session as primary, got:\n%s", view)
	}
	if !strings.Contains(view, "+") || !strings.Contains(view, "more") {
		t.Fatalf("wide focus-big view should show a more indicator when secondaries are hidden, got:\n%s", view)
	}
	if got := renderedHeight(view); got > m.height {
		t.Fatalf("wide focus-big view height = %d, want <= %d\n%s", got, m.height, view)
	}
	if got := renderedMaxWidth(view); got > m.width {
		t.Fatalf("wide focus-big view width = %d, want <= %d\n%s", got, m.width, view)
	}
}

func TestModel_FocusBigNarrowManySessionsStaysWithinWidth(t *testing.T) {
	m := NewModel(New())
	m.width = 80
	m.height = 24
	m.layout.mode = layoutFocusBig
	m.selected = 0
	for i := 0; i < 20; i++ {
		m.sessions = append(m.sessions, NewClientSession(
			fmt.Sprintf("session-%02d", i+1),
			fmt.Sprintf("s%02d", i+1),
			80,
			24,
			nil,
		))
	}
	m.layout.syncSessions(m.sessions)

	view := stripANSI(m.View())
	if !strings.Contains(view, "MAIN [1]") {
		t.Fatalf("narrow focus-big view should render selected session as primary, got:\n%s", view)
	}
	if !strings.Contains(view, "+") || !strings.Contains(view, "more") {
		t.Fatalf("narrow focus-big view should show a more indicator when secondaries are hidden, got:\n%s", view)
	}
	if got := renderedMaxWidth(view); got > m.width {
		t.Fatalf("narrow focus-big view width = %d, want <= %d\n%s", got, m.width, view)
	}
	if got := renderedHeight(view); got > m.height {
		t.Fatalf("narrow focus-big view height = %d, want <= %d\n%s", got, m.height, view)
	}
}

func TestModel_FocusBigNarrowLongTitlesStayWithinWidth(t *testing.T) {
	m := NewModel(New())
	m.width = 80
	m.height = 24
	m.layout.mode = layoutFocusBig
	m.selected = 0
	for i := 0; i < 20; i++ {
		m.sessions = append(m.sessions, NewClientSession(
			fmt.Sprintf("session-%02d", i+1),
			fmt.Sprintf("session-%02d-with-a-very-long-title-that-must-not-expand-the-focus-big-panel", i+1),
			80,
			24,
			nil,
		))
	}
	m.layout.syncSessions(m.sessions)

	view := stripANSI(m.View())
	if !strings.Contains(view, "MAIN [1]") {
		t.Fatalf("narrow focus-big view should render selected session as primary, got:\n%s", view)
	}
	if !strings.Contains(view, "+") || !strings.Contains(view, "more") {
		t.Fatalf("narrow focus-big view should show a more indicator when secondaries are hidden, got:\n%s", view)
	}
	if got := renderedMaxWidth(view); got > m.width {
		t.Fatalf("narrow focus-big view with long titles width = %d, want <= %d\n%s", got, m.width, view)
	}
}

func TestModel_FocusBigViewFallsBackToGridForZeroOrOneSession(t *testing.T) {
	m := NewModel(New())
	m.width = 120
	m.height = 24
	m.layout.mode = layoutFocusBig

	got := stripANSI(m.focusBigView())
	want := stripANSI(m.gridView())
	if got != want {
		t.Fatalf("focusBigView should fall back to gridView for zero sessions, got:\n%s\nwant:\n%s", got, want)
	}
}

func TestModel_FocusBigSingleSessionUsesBudgetedMainPanel(t *testing.T) {
	m := NewModel(New())
	m.width = 120
	m.height = 5
	m.layout.mode = layoutFocusBig
	m.selected = 0
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha-with-a-very-long-title-that-must-not-expand-the-panel", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)

	view := stripANSI(m.View())
	if !strings.Contains(view, "MAIN [1]") {
		t.Fatalf("single-session focus-big view should render selected session as MAIN, got:\n%s", view)
	}
	if got := renderedHeight(view); got > m.height {
		t.Fatalf("single-session focus-big view height = %d, want <= %d\n%s", got, m.height, view)
	}
	if got := renderedMaxWidth(view); got > m.width {
		t.Fatalf("single-session focus-big view width = %d, want <= %d\n%s", got, m.width, view)
	}
	if grid := stripANSI(m.gridView()); view == grid {
		t.Fatalf("single-session focus-big view should not fall back to gridView, got:\n%s", view)
	}
}

func TestModel_FocusBigTinyWidthsStayWithinWidth(t *testing.T) {
	for _, width := range []int{1, 2, 3, 4} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			m := NewModel(New())
			m.width = width
			m.height = 5
			m.layout.mode = layoutFocusBig
			m.selected = 0
			m.sessions = []*ClientSession{
				NewClientSession("alpha", "alpha-with-a-very-long-title-that-must-not-expand-the-panel", 80, 24, nil),
			}
			m.layout.syncSessions(m.sessions)

			view := stripANSI(m.View())
			if got := renderedMaxWidth(view); got > m.width {
				t.Fatalf("single-session focus-big tiny-width view width = %d, want <= %d\n%s", got, m.width, view)
			}
			if got := renderedHeight(view); got > m.height {
				t.Fatalf("single-session focus-big tiny-width view height = %d, want <= %d\n%s", got, m.height, view)
			}
		})
	}
}

func TestModel_FocusBigNarrowTinyWidthsStayWithinWidth(t *testing.T) {
	for _, width := range []int{1, 2, 3, 4} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			m := NewModel(New())
			m.width = width
			m.height = 8
			m.layout.mode = layoutFocusBig
			m.selected = 0
			m.sessions = []*ClientSession{
				NewClientSession("alpha", "alpha", 80, 24, nil),
				NewClientSession("beta", "beta", 80, 24, nil),
			}
			m.layout.syncSessions(m.sessions)

			view := stripANSI(m.View())
			if got := renderedMaxWidth(view); got > m.width {
				t.Fatalf("narrow focus-big tiny-width view width = %d, want <= %d\n%s", got, m.width, view)
			}
			if got := renderedHeight(view); got > m.height {
				t.Fatalf("narrow focus-big tiny-width view height = %d, want <= %d\n%s", got, m.height, view)
			}
		})
	}
}

func TestModel_FocusBigTinyWidthStatusOnlyStaysWithinWidth(t *testing.T) {
	for _, width := range []int{1, 2, 3, 4} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			m := NewModel(New())
			m.width = width
			m.height = 1
			m.layout.mode = layoutFocusBig
			m.selected = 0
			m.sessions = []*ClientSession{
				NewClientSession("alpha", "alpha", 80, 24, nil),
				NewClientSession("beta", "beta", 80, 24, nil),
			}
			m.layout.syncSessions(m.sessions)

			view := stripANSI(m.View())
			if got := renderedMaxWidth(view); got > m.width {
				t.Fatalf("focus-big tiny-width status-only view width = %d, want <= %d\n%s", got, m.width, view)
			}
			if got := renderedHeight(view); got > m.height {
				t.Fatalf("focus-big tiny-width status-only view height = %d, want <= %d\n%s", got, m.height, view)
			}
		})
	}
}

func TestModel_FocusBigStatusBarWidthConstraintPreservesANSI(t *testing.T) {
	m := NewModel(New())
	m.width = 5
	m.height = 1
	m.layout.mode = layoutFocusBig

	status := m.focusBigStatusBar(2)
	for _, line := range strings.Split(status, "\n") {
		if got := ansi.StringWidth(line); got > m.width {
			t.Fatalf("focus-big status line width = %d, want <= %d\n%q", got, m.width, status)
		}
	}
	if strings.Contains(ansi.Strip(status), "\x1b") {
		t.Fatalf("focus-big status should not expose partial ANSI escapes, got %q", status)
	}
	if strings.Contains(status, "\x1b[") && !strings.Contains(status, "\x1b[0m") {
		t.Fatalf("focus-big styled status should preserve ANSI reset, got %q", status)
	}
}

func TestModel_FocusBigTinyWidthPanelFallbackPreservesANSI(t *testing.T) {
	m := NewModel(New())
	m.width = 1
	m.height = 5
	m.layout.mode = layoutFocusBig
	m.selected = 0
	session := NewClientSession("alpha", "alpha", 80, 24, nil)
	session.WriteOutput([]byte("\x1b[31m🙂"))
	m.sessions = []*ClientSession{session}
	m.layout.syncSessions(m.sessions)

	view := m.View()
	if strings.Contains(view, "\x1b[ ") || (strings.Contains(view, "\x1b[") && !strings.Contains(view, "\x1b[0m")) {
		t.Fatalf("focus-big tiny-width panel fallback should not expose partial ANSI escapes, got %q", view)
	}
}

func TestModel_FocusBigViewFallsBackToGridForInvalidSelection(t *testing.T) {
	tests := []struct {
		name     string
		selected int
	}{
		{name: "negative selected", selected: -1},
		{name: "selected past end", selected: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewModel(New())
			m.width = 120
			m.height = 24
			m.sessions = []*ClientSession{
				NewClientSession("alpha", "alpha", 80, 24, nil),
				NewClientSession("beta", "beta", 80, 24, nil),
			}
			m.layout.syncSessions(m.sessions)
			m.layout.mode = layoutFocusBig
			m.selected = tt.selected

			got := stripANSI(m.focusBigView())
			want := stripANSI(m.gridView())
			if got != want {
				t.Fatalf("focusBigView should fall back to gridView, got:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestModel_GridViewUsesLayoutOrder(t *testing.T) {
	a := NewClientSession("a", "first", 80, 24, nil)
	b := NewClientSession("b", "second", 80, 24, nil)
	m := NewModel(New())
	m.width = 120
	m.height = 20
	m.sessions = []*ClientSession{a, b}
	m.layout.order = []string{"b", "a"}

	view := stripANSI(m.gridView())
	secondIndex := strings.Index(view, "second")
	firstIndex := strings.Index(view, "first")
	if secondIndex < 0 {
		t.Fatalf("gridView missing ordered first title %q in:\n%s", "second", view)
	}
	if firstIndex < 0 {
		t.Fatalf("gridView missing ordered second title %q in:\n%s", "first", view)
	}
	if secondIndex > firstIndex {
		t.Fatalf("gridView rendered raw order; second title index %d after first title index %d in:\n%s", secondIndex, firstIndex, view)
	}
}

func TestModel_GridViewStatusBarStaysWithinWidth(t *testing.T) {
	m := NewModel(New())
	m.width = 80
	m.height = 24
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
		NewClientSession("beta", "beta", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)

	view := stripANSI(m.gridView())
	if got := renderedMaxWidth(view); got > m.width {
		t.Fatalf("grid view width = %d, want <= %d\n%s", got, m.width, view)
	}
	if got := renderedHeight(view); got > m.height {
		t.Fatalf("grid view height = %d, want <= %d\n%s", got, m.height, view)
	}
}

func TestModel_ReorderedGridFocusMatchesForwardingTarget(t *testing.T) {
	h := New()
	connA, _ := modelTestClient(t, h, "a", "session-a")
	connB, _ := modelTestClient(t, h, "b", "session-b")

	m := NewModel(h)
	m.width = 120
	m.height = 20
	m.sessions = h.Sessions()
	m.layout.syncSessions(m.sessions)
	m.selected = 0

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if cmd != nil {
		t.Fatal("Ctrl+A should enter prefix mode without returning a command")
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	if cmd != nil {
		t.Fatal("Ctrl+A l should not return a command")
	}
	assertLayoutOrder(t, m.layout.order, []string{"b", "a"})
	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1 after moving session-a later", m.selected)
	}

	view := stripANSI(m.gridView())
	if !strings.Contains(view, "session-b") || !strings.Contains(view, "session-a") {
		t.Fatalf("gridView missing session titles after reorder:\n%s", view)
	}
	if !strings.Contains(view, "▶ [2] session-a") {
		t.Fatalf("gridView should visibly focus session-a after reorder, got:\n%s", view)
	}
	if strings.Contains(view, "▶ [2] session-b") || strings.Contains(view, "▶ [1] session-b") {
		t.Fatalf("gridView focused session-b, but input target should be session-a:\n%s", view)
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil {
		t.Fatal("normal key should be forwarded without returning a command")
	}
	msgType, payload := readTestMessage(t, connA)
	if msgType != protocol.MsgInput {
		t.Fatalf("expected input message for session-a, got type %q", msgType)
	}
	if string(payload) != "x" {
		t.Fatalf("expected session-a payload %q, got %q", "x", payload)
	}
	assertNoTestMessage(t, connB)
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
