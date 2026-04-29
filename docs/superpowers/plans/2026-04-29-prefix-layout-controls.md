# Prefix Layout Controls Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Ctrl+A prefix-only hub controls for manual panel ordering, fixed grid columns, and focus-big layout while forwarding non-prefix keys to the selected remote PTY.

**Architecture:** Add a small layout state object to `hub` that owns display order, layout mode, fixed column count, and prefix-pending state. Refactor grid rendering to consume ordered sessions and add a separate focus-big renderer. Update TUI key handling so `Ctrl+A` starts prefix mode and hub commands run only after the prefix.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, existing codetty hub/session abstractions.

---

## File Structure

| File | Responsibility |
|------|----------------|
| `hub/layout.go` | layout mode/state, session ordering, movement, fixed column clamping |
| `hub/layout_test.go` | direct unit tests for layout state |
| `hub/layout_view.go` | grid view and focus-big view rendering helpers |
| `hub/tui.go` | Model state, prefix key handling, top-level View |
| `hub/tui_test.go` | integration-style key handling and view behavior tests |

---

### Task 1: Layout State and Ordering

**Files:**
- Create: `hub/layout.go`
- Create: `hub/layout_test.go`
- Modify: `hub/tui.go`

- [ ] **Step 1: Write failing layout ordering tests**

Create `hub/layout_test.go`:

```go
package hub

import "testing"

func layoutTestSession(id string) *ClientSession {
	return NewClientSession(id, id, 80, 24, nil)
}

func TestLayoutSyncSessionsAppendsNewAndRemovesMissing(t *testing.T) {
	l := layoutState{}
	sessions := []*ClientSession{layoutTestSession("a"), layoutTestSession("b")}
	l.syncSessions(sessions)
	if got := l.order; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("initial order = %v, want [a b]", got)
	}

	l.order = []string{"b", "a"}
	sessions = []*ClientSession{layoutTestSession("a"), layoutTestSession("c")}
	l.syncSessions(sessions)
	if got := l.order; len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Fatalf("synced order = %v, want [a c]", got)
	}
}

func TestLayoutOrderedSessionsUsesUserOrder(t *testing.T) {
	l := layoutState{order: []string{"b", "a"}}
	sessions := []*ClientSession{layoutTestSession("a"), layoutTestSession("b")}
	got := l.orderedSessions(sessions)
	if got[0].ID != "b" || got[1].ID != "a" {
		t.Fatalf("ordered sessions = [%s %s], want [b a]", got[0].ID, got[1].ID)
	}
}

func TestLayoutMoveSelected(t *testing.T) {
	l := layoutState{order: []string{"a", "b", "c"}}
	selected := l.moveSelected(1, -1)
	if selected != 0 || l.order[0] != "b" || l.order[1] != "a" {
		t.Fatalf("move left selected=%d order=%v, want selected=0 order=[b a c]", selected, l.order)
	}

	selected = l.moveSelected(0, 1)
	if selected != 1 || l.order[0] != "a" || l.order[1] != "b" {
		t.Fatalf("move right selected=%d order=%v, want selected=1 order=[a b c]", selected, l.order)
	}
}

func TestLayoutGridColsFixedClamps(t *testing.T) {
	l := layoutState{mode: layoutFixedGrid, fixedCols: 4}
	if got := l.gridCols(2, 200, 30); got != 2 {
		t.Fatalf("gridCols sessions clamp = %d, want 2", got)
	}
	if got := l.gridCols(4, 60, 30); got != 3 {
		t.Fatalf("gridCols width clamp = %d, want 3", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/logsim-mount/codetty && go test ./hub/ -run TestLayout -v`

Expected: compile failure because `layoutState`, `layoutFixedGrid`, and methods do not exist.

- [ ] **Step 3: Implement layout state**

Create `hub/layout.go`:

```go
package hub

type layoutMode int

const (
	layoutAutoGrid layoutMode = iota
	layoutFixedGrid
	layoutFocusBig
)

type layoutState struct {
	mode      layoutMode
	fixedCols int
	order     []string
	prefix    bool
}

func (l *layoutState) syncSessions(sessions []*ClientSession) {
	exists := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		exists[s.ID] = true
	}

	next := make([]string, 0, len(sessions))
	seen := make(map[string]bool, len(sessions))
	for _, id := range l.order {
		if exists[id] && !seen[id] {
			next = append(next, id)
			seen[id] = true
		}
	}
	for _, s := range sessions {
		if !seen[s.ID] {
			next = append(next, s.ID)
			seen[s.ID] = true
		}
	}
	l.order = next
}

func (l *layoutState) orderedSessions(sessions []*ClientSession) []*ClientSession {
	byID := make(map[string]*ClientSession, len(sessions))
	for _, s := range sessions {
		byID[s.ID] = s
	}
	out := make([]*ClientSession, 0, len(sessions))
	seen := make(map[string]bool, len(sessions))
	for _, id := range l.order {
		if s, ok := byID[id]; ok {
			out = append(out, s)
			seen[id] = true
		}
	}
	for _, s := range sessions {
		if !seen[s.ID] {
			out = append(out, s)
		}
	}
	return out
}

func (l *layoutState) moveSelected(selected, delta int) int {
	target := selected + delta
	if selected < 0 || selected >= len(l.order) || target < 0 || target >= len(l.order) {
		return selected
	}
	l.order[selected], l.order[target] = l.order[target], l.order[selected]
	return target
}

func (l *layoutState) gridCols(n, width, height int) int {
	if n <= 1 {
		return 1
	}
	if l.mode == layoutFixedGrid && l.fixedCols > 0 {
		cols := clamp(l.fixedCols, 1, n)
		for cols > 1 && width/cols < 20 {
			cols--
		}
		return cols
	}
	return autoGridCols(n, width, height)
}

func (l *layoutState) modeName() string {
	switch l.mode {
	case layoutFixedGrid:
		return "fixed"
	case layoutFocusBig:
		return "focus-big"
	default:
		return "auto"
	}
}

func autoGridCols(n, width, height int) int {
	if n <= 1 {
		return 1
	}
	usableH := height - 2
	if usableH < 1 {
		usableH = 1
	}
	bestCols := 1
	bestDist := 999.0
	for c := 1; c <= n && c <= 4; c++ {
		r := (n + c - 1) / c
		pw := width / c
		ph := usableH / r
		if ph < 4 || pw < 20 {
			continue
		}
		ratio := float64(pw) / float64(ph)
		dist := ratio - 3.0
		if dist < 0 {
			dist = -dist
		}
		if dist < bestDist {
			bestDist = dist
			bestCols = c
		}
	}
	return bestCols
}
```

- [ ] **Step 4: Add layout to Model and sync on connect/disconnect**

Modify `hub/tui.go`:

```go
type Model struct {
	hub        *Hub
	sessions   []*ClientSession
	selected   int
	fullscreen bool
	width      int
	height     int
	layout     layoutState
}
```

In `ClientConnectedMsg` and `ClientDisconnectedMsg`, after refreshing sessions:

```go
m.layout.syncSessions(m.sessions)
ordered := m.orderedSessions()
if m.selected >= len(ordered) {
	m.selected = max(0, len(ordered)-1)
}
```

Add:

```go
func (m *Model) orderedSessions() []*ClientSession {
	return m.layout.orderedSessions(m.sessions)
}
```

- [ ] **Step 5: Run layout tests**

Run: `cd /root/logsim-mount/codetty && go test ./hub/ -run TestLayout -v`

Expected: all layout tests pass.

- [ ] **Step 6: Commit**

```bash
cd /root/logsim-mount/codetty
git add hub/layout.go hub/layout_test.go hub/tui.go
git commit -m "feat(hub): add layout ordering state

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

### Task 2: Prefix Key Handling

**Files:**
- Modify: `hub/tui.go`
- Modify: `hub/tui_test.go`

- [ ] **Step 1: Write failing prefix key tests**

Append to `hub/tui_test.go`:

```go
func TestModel_UsesCtrlAPrefixForHubCommands(t *testing.T) {
	m := NewModel(New())
	m.sessions = []*ClientSession{NewClientSession("a", "a", 80, 24, nil), NewClientSession("b", "b", 80, 24, nil)}
	m.layout.syncSessions(m.sessions)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if cmd != nil {
		t.Fatal("Ctrl+A prefix should not return a command")
	}
	if !m.layout.prefix {
		t.Fatal("Ctrl+A should enter prefix mode")
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd != nil {
		t.Fatal("prefix n should not return a command")
	}
	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1", m.selected)
	}
	if m.layout.prefix {
		t.Fatal("prefix should clear after command")
	}
}

func TestModel_LegacyShortcutsForwardWithoutPrefix(t *testing.T) {
	h := New()
	clientConn, sessions := modelTestClient(t, h, "legacy", "legacy@test")
	m := NewModel(h)
	m.sessions = sessions
	m.layout.syncSessions(m.sessions)

	for _, key := range []tea.KeyType{tea.KeyCtrlCloseBracket, tea.KeyCtrlF, tea.KeyCtrlBackslash} {
		_, cmd := m.Update(tea.KeyMsg{Type: key})
		if cmd != nil {
			t.Fatalf("key %v should be forwarded, not handled by hub", key)
		}
	}

	for i := 0; i < 3; i++ {
		if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		msgType, _, ok := readTestMessage(t, clientConn)
		if !ok || msgType != protocol.MsgInput {
			t.Fatalf("expected forwarded input message, got ok=%v type=%v", ok, msgType)
		}
	}
}
```

Also add helpers:

```go
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
	t.Cleanup(func() { clientConn.Close() })

	regMsg, err := protocol.EncodeRegister(protocol.RegisterPayload{ID: id, Title: title, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("encode register: %v", err)
	}
	if err := clientConn.WriteMessage(websocket.BinaryMessage, regMsg); err != nil {
		t.Fatalf("send register: %v", err)
	}
	for i := 0; i < 20; i++ {
		sessions := h.Sessions()
		if len(sessions) == 1 {
			return clientConn, sessions
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected registered session")
	return nil, nil
}

func readTestMessage(t *testing.T, conn *websocket.Conn) (protocol.MsgType, []byte, bool) {
	t.Helper()
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read message: %v", err)
	}
	return protocol.ParseType(msg)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/logsim-mount/codetty && go test ./hub/ -run 'TestModel_UsesCtrlAPrefixForHubCommands|TestModel_LegacyShortcutsForwardWithoutPrefix' -v`

Expected: failures because Ctrl+A is forwarded and legacy shortcuts are still handled directly.

- [ ] **Step 3: Implement prefix handling**

In `hub/tui.go`, replace direct hub handling in `tea.KeyMsg` with:

```go
case tea.KeyMsg:
	if m.fullscreen {
		return m, nil
	}
	if msg.Type == tea.KeyCtrlA {
		if m.layout.prefix {
			m.layout.prefix = false
			m.forwardKey(msg)
			return m, nil
		}
		m.layout.prefix = true
		return m, nil
	}
	if m.layout.prefix {
		m.layout.prefix = false
		return m, m.handlePrefixKey(msg)
	}
	m.forwardKey(msg)
```

Add helpers:

```go
func (m *Model) handlePrefixKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case 'n':
				m.selectDelta(1)
			case 'p':
				m.selectDelta(-1)
			case 'f':
				return m.enterFullscreen()
			case '\\':
				return tea.Quit
			case 'h':
				m.selected = m.layout.moveSelected(m.selected, -1)
			case 'l':
				m.selected = m.layout.moveSelected(m.selected, 1)
			case '1', '2', '3', '4':
				m.layout.mode = layoutFixedGrid
				m.layout.fixedCols = int(msg.Runes[0] - '0')
			case 'a':
				m.layout.mode = layoutAutoGrid
			case 'b':
				m.layout.mode = layoutFocusBig
			case 'g':
				if m.layout.mode == layoutFocusBig {
					m.layout.mode = layoutAutoGrid
				}
			}
		}
	case tea.KeyLeft:
		m.selected = m.layout.moveSelected(m.selected, -1)
	case tea.KeyRight:
		m.selected = m.layout.moveSelected(m.selected, 1)
	}
	return nil
}
```

Add:

```go
func (m *Model) forwardKey(msg tea.KeyMsg) {
	ordered := m.orderedSessions()
	if m.selected < len(ordered) {
		raw := keyToBytes(msg)
		if len(raw) > 0 {
			ordered[m.selected].SendInput(raw)
		}
	}
}

func (m *Model) selectDelta(delta int) {
	ordered := m.orderedSessions()
	if len(ordered) > 0 {
		m.selected = (m.selected + delta + len(ordered)) % len(ordered)
	}
}

func (m *Model) enterFullscreen() tea.Cmd {
	ordered := m.orderedSessions()
	if len(ordered) == 0 || m.selected >= len(ordered) {
		return nil
	}
	m.fullscreen = true
	sess := ordered[m.selected]
	sess.SendResize(uint16(m.width), uint16(m.height))
	handler := &fullscreenHandler{session: sess, hub: m.hub}
	return tea.Exec(handler, func(err error) tea.Msg { return exitFullscreenMsg{} })
}
```

- [ ] **Step 4: Run prefix tests**

Run: `cd /root/logsim-mount/codetty && go test ./hub/ -run 'TestModel_UsesCtrlAPrefixForHubCommands|TestModel_LegacyShortcutsForwardWithoutPrefix|TestModel_ForwardsCtrlQToSelectedSession' -v`

Expected: tests pass.

- [ ] **Step 5: Commit**

```bash
cd /root/logsim-mount/codetty
git add hub/tui.go hub/tui_test.go
git commit -m "feat(hub): add Ctrl+A prefix command handling

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

### Task 3: Fixed Grid and Ordered Grid Rendering

**Files:**
- Create: `hub/layout_view.go`
- Modify: `hub/tui.go`
- Modify: `hub/tui_test.go`

- [ ] **Step 1: Write failing grid tests**

Add to `hub/tui_test.go`:

```go
func TestModel_PrefixFixedColsAndAuto(t *testing.T) {
	m := NewModel(New())
	m.sessions = []*ClientSession{NewClientSession("a", "a", 80, 24, nil), NewClientSession("b", "b", 80, 24, nil)}
	m.layout.syncSessions(m.sessions)

	m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if m.layout.mode != layoutFixedGrid || m.layout.fixedCols != 2 {
		t.Fatalf("layout = (%v,%d), want fixed 2", m.layout.mode, m.layout.fixedCols)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if m.layout.mode != layoutAutoGrid {
		t.Fatalf("layout mode = %v, want auto", m.layout.mode)
	}
}

func TestModel_GridViewUsesLayoutOrder(t *testing.T) {
	m := NewModel(New())
	m.width = 120
	m.height = 40
	m.sessions = []*ClientSession{
		NewClientSession("a", "first", 80, 24, nil),
		NewClientSession("b", "second", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)
	m.layout.order = []string{"b", "a"}

	view := m.gridView()
	if strings.Index(view, "second") < 0 || strings.Index(view, "first") < 0 {
		t.Fatalf("view should contain both sessions:\n%s", view)
	}
	if strings.Index(view, "second") > strings.Index(view, "first") {
		t.Fatalf("layout order not reflected in view:\n%s", view)
	}
}
```

Add `strings` to imports.

- [ ] **Step 2: Run grid tests to verify failure**

Run: `cd /root/logsim-mount/codetty && go test ./hub/ -run 'TestModel_PrefixFixedColsAndAuto|TestModel_GridViewUsesLayoutOrder' -v`

Expected: fixed mode may pass after Task 2, grid order test fails until rendering uses ordered sessions.

- [ ] **Step 3: Move grid rendering into layout_view.go and use ordered sessions**

Create `hub/layout_view.go` by moving `panelSize`, `gridView`, and `renderPanel` from `hub/tui.go`, then update `gridView`:

```go
func (m *Model) gridView() string {
	sessions := m.orderedSessions()
	n := len(sessions)
	cols := m.layout.gridCols(n, m.width, m.height)
	rows := (n + cols - 1) / cols
	panelW, panelH := m.panelSize(n, cols)
	...
	sess := sessions[idx]
	...
}
```

Update `panelSize` signature:

```go
func (m *Model) panelSize(n, cols int) (int, int) {
	if n == 0 {
		return m.width, m.height - 2
	}
	rows := (n + cols - 1) / cols
	...
}
```

Remove old `gridCols()` from `tui.go`; `layout.gridCols` replaces it.

- [ ] **Step 4: Update status bar**

In `gridView`, status should use prefix instructions and layout mode:

```go
helpText := fmt.Sprintf("Prefix Ctrl+A | n/p switch | f fullscreen | h/l move | 1-4 cols | a auto | b focus-big | g grid | \\\\ quit | layout: %s | clients: %d", m.layout.modeName(), n)
if m.layout.prefix {
	helpText = "PREFIX Ctrl+A: n/p switch  f fullscreen  h/l move  1-4 cols  a auto  b focus-big  g grid  \\ quit  Ctrl+A send"
}
```

- [ ] **Step 5: Run hub tests**

Run: `cd /root/logsim-mount/codetty && go test ./hub/ -v`

Expected: hub tests pass.

- [ ] **Step 6: Commit**

```bash
cd /root/logsim-mount/codetty
git add hub/tui.go hub/layout_view.go hub/tui_test.go
git commit -m "feat(hub): render ordered fixed-grid layouts

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

### Task 4: Focus Big Rendering

**Files:**
- Modify: `hub/layout_view.go`
- Modify: `hub/tui.go`
- Modify: `hub/tui_test.go`

- [ ] **Step 1: Write failing focus-big tests**

Add to `hub/tui_test.go`:

```go
func TestModel_PrefixFocusBigMode(t *testing.T) {
	m := NewModel(New())
	m.sessions = []*ClientSession{NewClientSession("a", "alpha", 80, 24, nil), NewClientSession("b", "beta", 80, 24, nil)}
	m.layout.syncSessions(m.sessions)

	m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if m.layout.mode != layoutFocusBig {
		t.Fatalf("layout mode = %v, want focus-big", m.layout.mode)
	}
}

func TestModel_FocusBigViewUsesSelectedAsPrimary(t *testing.T) {
	m := NewModel(New())
	m.width = 140
	m.height = 40
	m.sessions = []*ClientSession{
		NewClientSession("a", "alpha", 80, 24, nil),
		NewClientSession("b", "beta", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)
	m.selected = 1
	m.layout.mode = layoutFocusBig

	view := m.View()
	if !strings.Contains(view, "MAIN [2] beta") {
		t.Fatalf("focus-big primary title missing selected session:\n%s", view)
	}
}
```

- [ ] **Step 2: Run focus tests to verify failure**

Run: `cd /root/logsim-mount/codetty && go test ./hub/ -run 'TestModel_PrefixFocusBigMode|TestModel_FocusBigViewUsesSelectedAsPrimary' -v`

Expected: mode test passes after Task 2; view test fails until focus-big renderer exists.

- [ ] **Step 3: Implement focus-big view**

In `hub/tui.go`, change View:

```go
if m.layout.mode == layoutFocusBig {
	return m.focusBigView()
}
return m.gridView()
```

In `hub/layout_view.go`, add:

```go
func (m *Model) focusBigView() string {
	sessions := m.orderedSessions()
	if len(sessions) <= 1 || m.selected >= len(sessions) {
		return m.gridView()
	}
	if m.width >= 120 {
		return m.focusBigWideView(sessions)
	}
	return m.focusBigNarrowView(sessions)
}
```

Implement wide:

```go
func (m *Model) focusBigWideView(sessions []*ClientSession) string {
	mainW := (m.width * 70) / 100
	sideW := m.width - mainW
	contentH := m.height - 2
	main := m.renderSessionPanel(sessions[m.selected], fmt.Sprintf("MAIN [%d] %s", m.selected+1, sessions[m.selected].Title), mainW, contentH-2, true)
	var sidePanels []string
	others := append([]*ClientSession{}, sessions[:m.selected]...)
	others = append(others, sessions[m.selected+1:]...)
	sideH := contentH / len(others)
	if sideH < 5 {
		sideH = 5
	}
	for _, s := range others {
		sidePanels = append(sidePanels, m.renderSessionPanel(s, s.Title, sideW, sideH-2, false))
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, main, lipgloss.JoinVertical(lipgloss.Left, sidePanels...))
	return lipgloss.JoinVertical(lipgloss.Left, body, m.statusBar(len(sessions)))
}
```

Implement narrow with main on top and secondary panels horizontally below.

Extract:

```go
func (m *Model) renderSessionPanel(sess *ClientSession, title string, panelW, contentH int, focused bool) string
func (m *Model) statusBar(n int) string
```

- [ ] **Step 4: Run focus tests and all tests**

Run: `cd /root/logsim-mount/codetty && go test ./hub/ -run 'TestModel_PrefixFocusBigMode|TestModel_FocusBigViewUsesSelectedAsPrimary' -v && go test ./...`

Expected: tests pass.

- [ ] **Step 5: Commit**

```bash
cd /root/logsim-mount/codetty
git add hub/tui.go hub/layout_view.go hub/tui_test.go
git commit -m "feat(hub): add focus-big layout mode

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

### Task 5: Final Verification

**Files:**
- Modify as needed from earlier tasks.

- [ ] **Step 1: Run full test suite**

Run: `cd /root/logsim-mount/codetty && go test ./...`

Expected: all packages pass.

- [ ] **Step 2: Run race tests**

Run: `cd /root/logsim-mount/codetty && go test -race ./...`

Expected: all packages pass with no races.

- [ ] **Step 3: Build binaries**

Run: `cd /root/logsim-mount/codetty && go build ./cmd/codetty-cli/ && go build ./cmd/codetty-hub/ && rm -f codetty-cli codetty-hub`

Expected: both builds succeed.

- [ ] **Step 4: Manual tmux smoke test**

Run hub and two clients in tmux, then confirm:

- non-prefix text goes to selected client;
- `Ctrl+A n` switches selection;
- `Ctrl+A h/l` reorders panels;
- `Ctrl+A 2` fixes two columns;
- `Ctrl+A b` enters focus-big;
- `Ctrl+A g` returns to grid;
- `Ctrl+A \` quits hub.

- [ ] **Step 5: Final commit if any verification-only fixes were needed**

```bash
cd /root/logsim-mount/codetty
git status --short
```

If there are changes:

```bash
git add -A
git commit -m "fix(hub): polish prefix layout controls

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```
