# CLI Selection Controls Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Support multiple default clients from the same directory and add direct Shift+Tab plus mouse click client selection in the hub.

**Architecture:** Make implicit CLI IDs unique at the client edge while preserving explicit `--id` replacement semantics in the hub. Add hub-local selection controls by sharing layout geometry between rendering and mouse hit testing so keyboard, mouse, input forwarding, and fullscreen entry use the same ordered session index.

**Tech Stack:** Go, Bubble Tea (`tea.KeyShiftTab`, `tea.MouseMsg`), Gorilla WebSocket tests, existing `hub/layout.go`, `hub/layout_view.go`, and `hub/tui.go` patterns.

---

## File structure

- `cmd/codetty-cli/main.go`
  - Split default identity construction from flag parsing.
  - Add helpers for implicit ID/title and explicit ID/title behavior.
- `cmd/codetty-cli/identity_test.go`
  - New tests for implicit uniqueness and explicit ID preservation.
- `hub/tui.go`
  - Add direct `Shift+Tab` handling.
  - Add mouse message handling outside fullscreen.
- `hub/layout_geometry.go`
  - New focused helper file for panel rectangles and hit testing.
  - Exposes geometry for grid and focus-big modes.
- `hub/layout_geometry_test.go`
  - New tests for grid and focus-big hit regions.
- `hub/layout_view.go`
  - Refactor grid/focus-big calculations to use geometry helpers where needed.
  - Keep visual rendering output unchanged except where geometry extraction requires shared helper calls.
- `hub/tui_test.go`
  - Add behavior tests for Shift+Tab, Tab forwarding, mouse selection, empty clicks, and fullscreen non-interference.

---

## Task 1: CLI implicit identity supports same-directory multi-clients

**Files:**
- Modify: `cmd/codetty-cli/main.go:23-53`
- Modify: `cmd/codetty-cli/main.go:233-243`
- Create: `cmd/codetty-cli/identity_test.go`

- [ ] **Step 1: Write failing tests for implicit identity and explicit ID behavior**

Create `cmd/codetty-cli/identity_test.go`:

```go
package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestImplicitClientIdentityIsUniquePerProcess(t *testing.T) {
	id1, title1 := buildClientIdentity("", false, "/root/logsim-mount/tmp", "host-a", 111, "claude")
	id2, title2 := buildClientIdentity("", false, "/root/logsim-mount/tmp", "host-a", 222, "claude")

	if id1 == id2 {
		t.Fatalf("implicit IDs should differ for same directory with different PIDs: %q", id1)
	}
	if id1 != "tmp-host-a-111" {
		t.Fatalf("id1 = %q, want %q", id1, "tmp-host-a-111")
	}
	if id2 != "tmp-host-a-222" {
		t.Fatalf("id2 = %q, want %q", id2, "tmp-host-a-222")
	}
	if title1 != "claude@tmp#111" {
		t.Fatalf("title1 = %q, want %q", title1, "claude@tmp#111")
	}
	if title2 != "claude@tmp#222" {
		t.Fatalf("title2 = %q, want %q", title2, "claude@tmp#222")
	}
}

func TestExplicitClientIdentityPreservesIDAndTitleShape(t *testing.T) {
	id, title := buildClientIdentity("shared", true, "/root/logsim-mount/tmp", "host-a", 111, "claude")

	if id != "shared" {
		t.Fatalf("explicit id = %q, want %q", id, "shared")
	}
	if title != "claude@shared" {
		t.Fatalf("explicit title = %q, want %q", title, "claude@shared")
	}
}

func TestImplicitClientIdentityFallsBackToUnknownHostname(t *testing.T) {
	id, title := buildClientIdentity("", false, filepath.Join("/", "tmp"), "", 333, "cat")

	if !strings.Contains(id, "tmp-unknown-333") {
		t.Fatalf("implicit id should include unknown hostname fallback and pid, got %q", id)
	}
	if title != "cat@tmp#333" {
		t.Fatalf("title = %q, want %q", title, "cat@tmp#333")
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./cmd/codetty-cli -run TestImplicitClientIdentity -v
```

Expected: FAIL because `buildClientIdentity` is undefined.

- [ ] **Step 3: Implement minimal identity helpers**

In `cmd/codetty-cli/main.go`, replace the current flag default and title creation with explicit tracking:

```go
func main() {
	serverURL := flag.String("server", "ws://localhost:8080/ws", "hub WebSocket URL")
	clientID := flag.String("id", "", "client identifier")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: codetty-cli --server ws://hub:8080/ws --id NAME -- <command> [args...]\n")
		os.Exit(1)
	}

	idExplicit := *clientID != ""
	wd, _ := os.Getwd()
	hostname, _ := os.Hostname()
	identityID, title := buildClientIdentity(*clientID, idExplicit, wd, hostname, os.Getpid(), args[0])
```

Then change the connection loop call:

```go
	go connectionLoop(*serverURL, identityID, title, ptmx, outputCh, doneCh, screenMirror, &replayMu)
```

Remove the old local title assignment:

```go
	title := fmt.Sprintf("%s@%s", args[0], *clientID)
```

Replace `defaultID()` with:

```go
func buildClientIdentity(id string, explicit bool, dir string, hostname string, pid int, command string) (string, string) {
	if explicit {
		return id, fmt.Sprintf("%s@%s", command, id)
	}
	base := filepath.Base(dir)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "unknown"
	}
	if hostname == "" {
		hostname = "unknown"
	}
	generatedID := fmt.Sprintf("%s-%s-%d", base, hostname, pid)
	title := fmt.Sprintf("%s@%s#%d", command, base, pid)
	return generatedID, title
}
```

- [ ] **Step 4: Run targeted CLI identity tests**

Run:

```bash
go test ./cmd/codetty-cli -run 'TestImplicitClientIdentity|TestExplicitClientIdentity' -v
```

Expected: PASS.

- [ ] **Step 5: Run full tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/codetty-cli/main.go cmd/codetty-cli/identity_test.go
git commit -m "fix(cli): make implicit client IDs unique" -m "Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

## Task 2: Shift+Tab cycles selected client and Tab still forwards

**Files:**
- Modify: `hub/tui.go:70-88`
- Modify: `hub/tui_test.go`

- [ ] **Step 1: Write failing test for direct Shift+Tab selection**

Add this test near other prefix/selection tests in `hub/tui_test.go`:

```go
func TestModel_ShiftTabSelectsNextSessionAndChangesForwardingTarget(t *testing.T) {
	h := New()
	connA, _ := modelTestClient(t, h, "a", "session-a")
	connB, _ := modelTestClient(t, h, "b", "session-b")

	m := NewModel(h)
	m.sessions = h.Sessions()
	m.layout.syncSessions(m.sessions)
	m.selected = 0

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if cmd != nil {
		t.Fatal("Shift+Tab should switch selection without returning a command")
	}
	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1", m.selected)
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil {
		t.Fatal("normal key should forward without returning a command")
	}
	msgType, payload := readTestMessage(t, connB)
	if msgType != protocol.MsgInput {
		t.Fatalf("expected input for session-b, got type %q", msgType)
	}
	if string(payload) != "x" {
		t.Fatalf("expected payload %q, got %q", "x", payload)
	}
	assertNoTestMessage(t, connA)
}
```

- [ ] **Step 2: Write failing test that plain Tab still forwards**

Add this test in `hub/tui_test.go`:

```go
func TestModel_TabStillForwardsToSelectedSession(t *testing.T) {
	h := New()
	connA, _ := modelTestClient(t, h, "a", "session-a")

	m := NewModel(h)
	m.sessions = h.Sessions()
	m.layout.syncSessions(m.sessions)
	m.selected = 0

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if cmd != nil {
		t.Fatal("Tab should forward without returning a command")
	}
	msgType, payload := readTestMessage(t, connA)
	if msgType != protocol.MsgInput {
		t.Fatalf("expected input for session-a, got type %q", msgType)
	}
	if string(payload) != "\t" {
		t.Fatalf("expected tab payload, got %q", payload)
	}
}
```

- [ ] **Step 3: Run tests and verify failure**

Run:

```bash
go test ./hub -run 'TestModel_ShiftTabSelectsNextSessionAndChangesForwardingTarget|TestModel_TabStillForwardsToSelectedSession' -v
```

Expected: Shift+Tab test FAILS because current `Update` forwards it or ignores it instead of selecting; Tab test PASSES or continues to pass.

- [ ] **Step 4: Implement direct Shift+Tab handling**

In `hub/tui.go`, inside `case tea.KeyMsg:` after fullscreen guard and before `Ctrl+A` handling, add:

```go
		if msg.Type == tea.KeyShiftTab {
			m.selectDelta(1)
			return m, nil
		}
```

The resulting order must be:

```go
	case tea.KeyMsg:
		if m.fullscreen {
			return m, nil
		}
		if msg.Type == tea.KeyShiftTab {
			m.selectDelta(1)
			return m, nil
		}
		if msg.Type == tea.KeyCtrlA {
			...
		}
```

- [ ] **Step 5: Run targeted tests**

Run:

```bash
go test ./hub -run 'TestModel_ShiftTabSelectsNextSessionAndChangesForwardingTarget|TestModel_TabStillForwardsToSelectedSession' -v
```

Expected: PASS.

- [ ] **Step 6: Run full tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add hub/tui.go hub/tui_test.go
git commit -m "feat(hub): add Shift+Tab session selection" -m "Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

## Task 3: Add layout geometry and grid hit testing

**Files:**
- Create: `hub/layout_geometry.go`
- Create: `hub/layout_geometry_test.go`
- Modify: `hub/layout_view.go:32-90`

- [ ] **Step 1: Write failing grid geometry tests**

Create `hub/layout_geometry_test.go`:

```go
package hub

import "testing"

func TestModel_GridHitRegionsMatchVisiblePanels(t *testing.T) {
	m := NewModel(New())
	m.width = 80
	m.height = 24
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
		NewClientSession("beta", "beta", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)
	m.layout.mode = layoutFixedGrid
	m.layout.fixedCols = 2

	regions := m.layoutHitRegions()
	if len(regions) != 2 {
		t.Fatalf("regions len = %d, want 2: %#v", len(regions), regions)
	}
	if regions[0].sessionIndex != 0 || regions[1].sessionIndex != 1 {
		t.Fatalf("region indexes = %d,%d want 0,1", regions[0].sessionIndex, regions[1].sessionIndex)
	}
	if got := m.hitTestSession(regions[0].x+1, regions[0].y+1); got != 0 {
		t.Fatalf("hit first panel = %d, want 0", got)
	}
	if got := m.hitTestSession(regions[1].x+1, regions[1].y+1); got != 1 {
		t.Fatalf("hit second panel = %d, want 1", got)
	}
}

func TestModel_GridHitTestIgnoresStatusAndEmptyCells(t *testing.T) {
	m := NewModel(New())
	m.width = 80
	m.height = 24
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)
	m.layout.mode = layoutFixedGrid
	m.layout.fixedCols = 2

	if got := m.hitTestSession(0, m.height-1); got != -1 {
		t.Fatalf("status hit = %d, want -1", got)
	}
	if got := m.hitTestSession(79, 0); got != -1 {
		t.Fatalf("empty filler hit = %d, want -1", got)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
go test ./hub -run 'TestModel_GridHit' -v
```

Expected: FAIL because `layoutHitRegions` and `hitTestSession` are undefined.

- [ ] **Step 3: Implement geometry helpers**

Create `hub/layout_geometry.go`:

```go
package hub

type rect struct {
	x int
	y int
	w int
	h int
}

type hitRegion struct {
	sessionIndex int
	rect
}

func (r rect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

type gridMetrics struct {
	cols        int
	rows        int
	panelW      int
	panelH      int
	visibleRows int
	startRow    int
	statusH     int
	availableH int
}

func (m *Model) gridMetrics(n int) (gridMetrics, bool) {
	status := m.statusBar(n)
	statusH := renderedLineCount(status)
	if statusH > m.height {
		return gridMetrics{statusH: statusH}, false
	}
	availableH := m.height - statusH
	if availableH <= 0 || n <= 0 {
		return gridMetrics{statusH: statusH, availableH: availableH}, false
	}

	cols := m.layout.gridCols(n, m.width, m.height)
	rows := (n + cols - 1) / cols
	panelW, panelH := m.panelSize(n, cols, availableH)
	if panelH < 1 {
		panelH = 1
	}
	visibleRows := rows
	if visibleRows*panelH > availableH {
		visibleRows = availableH / panelH
	}
	if visibleRows < 1 {
		return gridMetrics{cols: cols, rows: rows, panelW: panelW, panelH: panelH, statusH: statusH, availableH: availableH}, false
	}

	startRow := 0
	if visibleRows < rows && m.selected >= 0 && m.selected < n {
		selectedRow := m.selected / cols
		if selectedRow >= visibleRows {
			startRow = selectedRow - visibleRows + 1
		}
		if startRow+visibleRows > rows {
			startRow = rows - visibleRows
		}
	}

	return gridMetrics{
		cols:        cols,
		rows:        rows,
		panelW:      panelW,
		panelH:      panelH,
		visibleRows: visibleRows,
		startRow:    startRow,
		statusH:     statusH,
		availableH: availableH,
	}, true
}

func (m *Model) gridHitRegions(n int) []hitRegion {
	metrics, ok := m.gridMetrics(n)
	if !ok {
		return nil
	}
	regions := make([]hitRegion, 0, n)
	for displayRow := 0; displayRow < metrics.visibleRows; displayRow++ {
		r := metrics.startRow + displayRow
		for c := 0; c < metrics.cols; c++ {
			idx := r*metrics.cols + c
			if idx >= n {
				continue
			}
			regions = append(regions, hitRegion{
				sessionIndex: idx,
				rect: rect{
					x: c * metrics.panelW,
					y: displayRow * metrics.panelH,
					w: metrics.panelW,
					h: metrics.panelH,
				},
			})
		}
	}
	return regions
}

func (m *Model) layoutHitRegions() []hitRegion {
	n := len(m.orderedSessions())
	if n == 0 {
		return nil
	}
	if m.layout.mode == layoutFocusBig {
		return m.focusBigHitRegions(n)
	}
	return m.gridHitRegions(n)
}

func (m *Model) hitTestSession(x, y int) int {
	for _, region := range m.layoutHitRegions() {
		if region.contains(x, y) {
			return region.sessionIndex
		}
	}
	return -1
}
```

Also add a temporary stub for focus-big so Task 3 compiles; Task 4 replaces it:

```go
func (m *Model) focusBigHitRegions(n int) []hitRegion {
	return nil
}
```

- [ ] **Step 4: Refactor gridView to use gridMetrics**

In `hub/layout_view.go`, replace the duplicated grid status/available/cols/rows/panel/startRow calculation in `gridView` with:

```go
	sessions := m.orderedSessions()
	n := len(sessions)
	status := m.statusBar(n)
	metrics, ok := m.gridMetrics(n)
	if !ok {
		if renderedLineCount(status) > m.height {
			return constrainRenderedLineCount(status, m.height)
		}
		return status
	}

	var rowViews []string
	for displayRow := 0; displayRow < metrics.visibleRows; displayRow++ {
		r := metrics.startRow + displayRow
		var colViews []string
		for c := 0; c < metrics.cols; c++ {
			idx := r*metrics.cols + c
			if idx >= n {
				colViews = append(colViews, strings.Repeat(" ", metrics.panelW))
				continue
			}
			sess := sessions[idx]
			title := fmt.Sprintf("[%d] %s", idx+1, sess.Title)
			focused := idx == m.selected
			if focused {
				title = fmt.Sprintf("▶ [%d] %s", idx+1, sess.Title)
			}

			panel := m.renderSessionPanelWithOuterHeight(sess, title, metrics.panelW, metrics.panelH, focused)
			colViews = append(colViews, panel)
		}
		rowViews = append(rowViews, lipgloss.JoinHorizontal(lipgloss.Top, colViews...))
	}
```

Keep the existing grid/status join logic after the loop.

- [ ] **Step 5: Run targeted geometry and grid rendering tests**

Run:

```bash
go test ./hub -run 'TestModel_GridHit|TestModel_GridViewStatusBar|TestModel_GridViewFixedColsTinyHeightStaysWithinHeight' -v
```

Expected: PASS.

- [ ] **Step 6: Run full tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add hub/layout_geometry.go hub/layout_geometry_test.go hub/layout_view.go
git commit -m "feat(hub): add grid layout hit testing" -m "Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

## Task 4: Add focus-big hit regions

**Files:**
- Modify: `hub/layout_geometry.go`
- Modify: `hub/layout_geometry_test.go`

- [ ] **Step 1: Write failing focus-big hit tests**

Append to `hub/layout_geometry_test.go`:

```go
func TestModel_FocusBigWideHitRegionsSelectMainAndSecondary(t *testing.T) {
	m := NewModel(New())
	m.width = 140
	m.height = 30
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
		NewClientSession("beta", "beta", 80, 24, nil),
		NewClientSession("gamma", "gamma", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)
	m.layout.mode = layoutFocusBig
	m.selected = 1

	regions := m.layoutHitRegions()
	if len(regions) < 2 {
		t.Fatalf("focus-big regions len = %d, want at least 2: %#v", len(regions), regions)
	}
	if got := m.hitTestSession(1, 1); got != 1 {
		t.Fatalf("main panel hit = %d, want selected index 1", got)
	}
	foundSecondary := false
	for _, region := range regions {
		if region.sessionIndex == 0 {
			foundSecondary = true
			if got := m.hitTestSession(region.x+1, region.y+1); got != 0 {
				t.Fatalf("secondary hit = %d, want 0", got)
			}
		}
	}
	if !foundSecondary {
		t.Fatalf("expected a visible secondary region for session 0: %#v", regions)
	}
}

func TestModel_FocusBigHiddenSecondaryDoesNotHit(t *testing.T) {
	m := NewModel(New())
	m.width = 40
	m.height = 8
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
		NewClientSession("beta", "beta", 80, 24, nil),
		NewClientSession("gamma", "gamma", 80, 24, nil),
		NewClientSession("delta", "delta", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)
	m.layout.mode = layoutFocusBig
	m.selected = 0

	for _, region := range m.layoutHitRegions() {
		if region.sessionIndex >= len(m.sessions) {
			t.Fatalf("hidden +N more region should not be selectable: %#v", region)
		}
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
go test ./hub -run 'TestModel_FocusBig.*Hit' -v
```

Expected: FAIL because `focusBigHitRegions` currently returns nil.

- [ ] **Step 3: Implement focus-big geometry**

Replace the `focusBigHitRegions` stub in `hub/layout_geometry.go` with:

```go
func (m *Model) focusBigHitRegions(n int) []hitRegion {
	if m.width <= 0 || m.height <= 0 || n == 0 || m.selected < 0 || m.selected >= n {
		return nil
	}

	status := m.focusBigStatusBar(n)
	statusH := renderedLineCount(status)
	if statusH > m.height {
		statusH = m.height
	}
	availableH := m.height - statusH
	if availableH <= 0 {
		return nil
	}

	if n == 1 {
		return []hitRegion{{sessionIndex: m.selected, rect: rect{x: 0, y: 0, w: m.width, h: availableH}}}
	}
	if m.width >= 120 {
		return m.focusBigWideHitRegions(n, availableH)
	}
	return m.focusBigNarrowHitRegions(n, availableH)
}

func (m *Model) focusBigWideHitRegions(n, availableH int) []hitRegion {
	if availableH < minPanelH {
		return []hitRegion{{sessionIndex: m.selected, rect: rect{x: 0, y: 0, w: m.width, h: availableH}}}
	}
	mainW := m.width * 7 / 10
	sideW := m.width - mainW
	if sideW < minPanelW {
		sideW = minPanelW
		mainW = m.width - sideW
	}
	if mainW < minPanelW {
		mainW = minPanelW
	}

	regions := []hitRegion{{sessionIndex: m.selected, rect: rect{x: 0, y: 0, w: mainW, h: availableH}}}
	secondaryCount := n - 1
	visible, hidden := fitSecondaryCount(availableH, minPanelH, secondaryCount)
	renderedSlots := visible
	if hidden > 0 && availableH >= minPanelH {
		renderedSlots++
	}
	if renderedSlots <= 0 {
		return regions
	}
	slotIndex := 0
	for idx := 0; idx < n; idx++ {
		if idx == m.selected {
			continue
		}
		if slotIndex >= visible {
			break
		}
		panelH := distributedSlotSize(availableH, renderedSlots, slotIndex)
		y := 0
		for i := 0; i < slotIndex; i++ {
			y += distributedSlotSize(availableH, renderedSlots, i)
		}
		regions = append(regions, hitRegion{sessionIndex: idx, rect: rect{x: mainW, y: y, w: sideW, h: panelH}})
		slotIndex++
	}
	return regions
}

func (m *Model) focusBigNarrowHitRegions(n, availableH int) []hitRegion {
	if availableH < minPanelH*2 {
		return []hitRegion{{sessionIndex: m.selected, rect: rect{x: 0, y: 0, w: m.width, h: availableH}}}
	}
	mainPanelH := availableH * 65 / 100
	if mainPanelH < minPanelH {
		mainPanelH = minPanelH
	}
	if maxMainH := availableH - minPanelH; mainPanelH > maxMainH {
		mainPanelH = maxMainH
	}
	regions := []hitRegion{{sessionIndex: m.selected, rect: rect{x: 0, y: 0, w: m.width, h: mainPanelH}}}

	secondaryCount := n - 1
	visible, hidden := fitSecondaryCount(m.width, minPanelW, secondaryCount)
	renderedSlots := visible
	if hidden > 0 && m.width >= minPanelW {
		renderedSlots++
	}
	if renderedSlots <= 0 {
		return regions
	}
	slotIndex := 0
	for idx := 0; idx < n; idx++ {
		if idx == m.selected {
			continue
		}
		if slotIndex >= visible {
			break
		}
		panelW := distributedSlotSize(m.width, renderedSlots, slotIndex)
		x := 0
		for i := 0; i < slotIndex; i++ {
			x += distributedSlotSize(m.width, renderedSlots, i)
		}
		regions = append(regions, hitRegion{sessionIndex: idx, rect: rect{x: x, y: mainPanelH, w: panelW, h: availableH - mainPanelH}})
		slotIndex++
	}
	return regions
}
```

- [ ] **Step 4: Run targeted focus-big tests**

Run:

```bash
go test ./hub -run 'TestModel_FocusBig.*Hit|TestModel_FocusBig' -v
```

Expected: PASS.

- [ ] **Step 5: Run full tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add hub/layout_geometry.go hub/layout_geometry_test.go
git commit -m "feat(hub): add focus-big hit testing" -m "Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

## Task 5: Mouse click selects client panels

**Files:**
- Modify: `hub/tui.go`
- Modify: `hub/tui_test.go`

- [ ] **Step 1: Write failing grid mouse selection test**

Add to `hub/tui_test.go`:

```go
func TestModel_MouseClickSelectsGridPanel(t *testing.T) {
	m := NewModel(New())
	m.width = 100
	m.height = 24
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
		NewClientSession("beta", "beta", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)
	m.layout.mode = layoutFixedGrid
	m.layout.fixedCols = 2
	m.selected = 0

	regions := m.layoutHitRegions()
	if len(regions) != 2 {
		t.Fatalf("test setup regions len = %d, want 2", len(regions))
	}

	_, cmd := m.Update(tea.MouseMsg{
		X:      regions[1].x + 1,
		Y:      regions[1].y + 1,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	if cmd != nil {
		t.Fatal("mouse selection should not return a command")
	}
	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1", m.selected)
	}
}
```

- [ ] **Step 2: Write failing click ignored tests**

Add:

```go
func TestModel_MouseClickOutsidePanelDoesNotChangeSelection(t *testing.T) {
	m := NewModel(New())
	m.width = 100
	m.height = 24
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)
	m.selected = 0

	_, cmd := m.Update(tea.MouseMsg{
		X:      0,
		Y:      m.height - 1,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	if cmd != nil {
		t.Fatal("ignored mouse click should not return a command")
	}
	if m.selected != 0 {
		t.Fatalf("selected = %d, want 0 after status click", m.selected)
	}
}

func TestModel_MouseClickIgnoredInFullscreen(t *testing.T) {
	m := NewModel(New())
	m.width = 100
	m.height = 24
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
		NewClientSession("beta", "beta", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)
	m.fullscreen = true
	m.selected = 0

	_, cmd := m.Update(tea.MouseMsg{
		X:      50,
		Y:      1,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	if cmd != nil {
		t.Fatal("fullscreen mouse click should not return a command")
	}
	if m.selected != 0 {
		t.Fatalf("selected = %d, want 0 in fullscreen", m.selected)
	}
}
```

- [ ] **Step 3: Run tests and verify failure**

Run:

```bash
go test ./hub -run 'TestModel_MouseClick' -v
```

Expected: grid mouse selection test FAILS because `Update` has no mouse handling.

- [ ] **Step 4: Implement mouse handling**

In `hub/tui.go`, add a `tea.MouseMsg` case before the `tea.KeyMsg` case:

```go
	case tea.MouseMsg:
		if m.fullscreen {
			return m, nil
		}
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			if idx := m.hitTestSession(msg.X, msg.Y); idx >= 0 {
				m.selected = idx
			}
		}
```

- [ ] **Step 5: Run targeted mouse tests**

Run:

```bash
go test ./hub -run 'TestModel_MouseClick' -v
```

Expected: PASS.

- [ ] **Step 6: Run full tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add hub/tui.go hub/tui_test.go
git commit -m "feat(hub): select sessions with mouse clicks" -m "Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

---

## Task 6: End-to-end tmux verification and final review

**Files:**
- No source files expected.

- [ ] **Step 1: Run complete automated verification**

Run:

```bash
go test ./...
go test -race ./...
go build ./cmd/codetty-cli/
go build ./cmd/codetty-hub/
rm -f codetty-cli codetty-hub
```

Expected: all commands exit 0.

- [ ] **Step 2: Run tmux smoke test for duplicate default clients and Shift+Tab**

Use a temporary script from the repository root:

```bash
tmpdir=$(mktemp -d /tmp/codetty-selection.XXXXXX)
session="codetty-selection-$$"
port=$((26000 + RANDOM % 20000))
cleanup(){ tmux kill-session -t "$session" >/dev/null 2>&1 || true; rm -rf "$tmpdir"; }
trap cleanup EXIT

go build -o "$tmpdir/codetty-hub" ./cmd/codetty-hub
go build -o "$tmpdir/codetty-cli" ./cmd/codetty-cli

tmux new-session -d -s "$session" -n hub -x 120 -y 30 "$tmpdir/codetty-hub -addr 127.0.0.1:$port"
for i in $(seq 1 80); do
  code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/ws" || true)
  [ "$code" != "000" ] && break
  sleep 0.1
done

tmux new-window -d -t "$session:" -n alpha -c /root/logsim-mount/tmp "$tmpdir/codetty-cli --server ws://127.0.0.1:$port/ws -- cat"
tmux new-window -d -t "$session:" -n beta -c /root/logsim-mount/tmp "$tmpdir/codetty-cli --server ws://127.0.0.1:$port/ws -- cat"

capture(){ tmux capture-pane -p -t "$1" | tr -d '\000'; }
for i in $(seq 1 100); do
  if capture "$session:hub" | grep -Fq "clients: 2"; then
    echo "ok: duplicate default directory clients both visible"
    break
  fi
  sleep 0.1
  [ "$i" -eq 100 ] && { echo "clients: 2 not visible" >&2; capture "$session:hub" >&2; exit 1; }
done

tmux send-keys -t "$session:hub" "first-target" Enter
sleep 0.5
tmux send-keys -t "$session:hub" BTab
tmux send-keys -t "$session:hub" "second-target" Enter
sleep 0.5

if ! capture "$session:alpha" | grep -Fq "first-target"; then
  echo "first target missing from first client" >&2
  exit 1
fi
if ! capture "$session:beta" | grep -Fq "second-target"; then
  echo "second target missing from second client after Shift+Tab" >&2
  exit 1
fi
echo "ok: Shift+Tab switches input target"
```

Expected:

```text
ok: duplicate default directory clients both visible
ok: Shift+Tab switches input target
```

- [ ] **Step 3: Manually verify mouse selection in tmux**

Manual check:

1. Run the binaries in tmux with two clients from `/root/logsim-mount/tmp`.
2. Click the second panel in the hub window.
3. Type `mouse-target` and press Enter.
4. Confirm only the second client receives `mouse-target`.

Expected: clicking a panel changes the focused border and input target.

- [ ] **Step 4: Request code review**

Dispatch `superpowers:code-reviewer` with this context:

```text
Review branch implementing CLI selection controls:
- implicit codetty-cli IDs unique per process, explicit --id still replace/reconnect
- direct Shift+Tab hub selection, plain Tab still forwards
- layout geometry/hit testing for grid and focus-big
- mouse single-click panel selection outside fullscreen

Check for correctness issues around ID stability, reconnect semantics, layout/render geometry drift, mouse coordinate handling, fullscreen non-interference, and tests.
```

Expected: no Critical or Important issues before merge.

- [ ] **Step 5: Final commit or fix review feedback**

If review finds issues, fix them with TDD and commit. If no issues, proceed to branch finishing.

---

## Self-review

Spec coverage:

- Same-directory multi-client support: Task 1 and Task 6.
- Explicit duplicate `--id` replacement semantics: Task 1 tests identity behavior; existing hub replacement tests remain valid.
- Direct `Shift+Tab`: Task 2.
- Plain `Tab` forwarding: Task 2.
- Mouse click selection: Tasks 3, 4, and 5.
- Fullscreen non-interference: Task 5 and Task 6.

Placeholder scan:

- No TBD/TODO placeholders.
- All code steps include concrete code snippets.
- All verification steps include exact commands and expected outcomes.

Type consistency:

- `hitRegion`, `rect`, `gridMetrics`, `layoutHitRegions`, `hitTestSession`, and `focusBigHitRegions` are introduced before use.
- Tests use Bubble Tea names already available in v1.3.10: `tea.KeyShiftTab`, `tea.MouseMsg`, `tea.MouseButtonLeft`, `tea.MouseActionPress`.
