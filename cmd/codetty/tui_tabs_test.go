package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
)

func newTabsTestModel() *tuiModel {
	m := newTUIModel(nil, "", nil)
	m.width = 120
	m.height = 40
	m.sessions = []*tuiSession{
		newTUISession("alpha", "bash", 80, 24),
		newTUISession("beta", "bash", 80, 24),
		newTUISession("gamma", "bash", 80, 24),
	}
	m.order = []string{"alpha", "beta", "gamma"}
	m.mode = modeTabs
	m.selected = 0
	return m
}

func TestPrefixT_SwitchesToTabsModeAndBack(t *testing.T) {
	m := newTabsTestModel()
	m.mode = modeList

	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlA})
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'T'}})
	if m.mode != modeTabs {
		t.Fatalf("mode = %v, want modeTabs", m.mode)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlA})
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if m.mode != modeList {
		t.Fatalf("mode = %v, want modeList", m.mode)
	}
}

func TestPrefixF_TabsToFullscreenAndBack(t *testing.T) {
	m := newTabsTestModel()

	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlA})
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if m.mode != modeFullscreen {
		t.Fatalf("mode = %v, want modeFullscreen", m.mode)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlA})
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if m.mode != modeTabs {
		t.Fatalf("mode = %v, want modeTabs (pop back)", m.mode)
	}
}

func TestTabs_ShiftTabCyclesSessions(t *testing.T) {
	m := newTabsTestModel()

	for i, want := range []int{1, 2, 0} {
		m.handleKey(tea.KeyMsg{Type: tea.KeyShiftTab})
		if m.selected != want {
			t.Fatalf("press %d: selected = %d, want %d", i+1, m.selected, want)
		}
		if m.mode != modeTabs {
			t.Fatalf("mode = %v, want modeTabs", m.mode)
		}
	}
}

func TestSessionTabItems_CarryAggregateAgentState(t *testing.T) {
	m := newTabsTestModel()
	m.sessions[1].Tabs = []tuiTab{
		{Name: "sh"},
		{Name: "cp", Agent: "copilot", AgentState: "blocked"},
	}
	m.sessions[2].Exited = true

	items := sessionTabItems(m.visibleSessions())
	if len(items) != 3 {
		t.Fatalf("len(items) = %d, want 3", len(items))
	}
	if items[0].AgentState != "" || items[0].Name != "alpha" {
		t.Fatalf("items[0] = %+v, want alpha with no agent state", items[0])
	}
	if items[1].AgentState != "blocked" {
		t.Fatalf("items[1].AgentState = %q, want blocked", items[1].AgentState)
	}
	if items[2].Name != "gamma (exited)" {
		t.Fatalf("items[2].Name = %q, want exited suffix", items[2].Name)
	}

	// Same style classes as the focus-big tab bar: active highlight for
	// the selected session, agent state colors for the rest.
	if kind := tabStyleKind(items[0], true, false); kind != "active" {
		t.Fatalf("selected session tab style = %q, want active", kind)
	}
	if kind := tabStyleKind(items[1], false, false); kind != "blocked" {
		t.Fatalf("blocked session tab style = %q, want blocked", kind)
	}
	if kind := tabStyleKind(items[2], false, false); kind != "inactive" {
		t.Fatalf("exited session tab style = %q, want inactive", kind)
	}
}

func TestTabsView_ShowsSessionTabBarAndContent(t *testing.T) {
	m := newTabsTestModel()
	m.sessions[0].WriteOutput([]byte("hello-tabs"))

	view := m.tabsView()
	lines := strings.Split(view, "\n")
	if len(lines) != m.height {
		t.Fatalf("view has %d lines, want %d", len(lines), m.height)
	}

	topBar := xansi.Strip(lines[0])
	for _, want := range []string{"1:alpha", "2:beta", "3:gamma"} {
		if !strings.Contains(topBar, want) {
			t.Fatalf("top bar %q missing %q", topBar, want)
		}
	}
	if !strings.Contains(xansi.Strip(lines[1]), "hello-tabs") {
		t.Fatalf("content line %q missing session output", xansi.Strip(lines[1]))
	}
	if !strings.Contains(xansi.Strip(lines[len(lines)-1]), "[1] alpha") {
		t.Fatalf("bottom line %q missing session info", xansi.Strip(lines[len(lines)-1]))
	}
}

func TestTabs_ClickTopBarSwitchesSession(t *testing.T) {
	m := newTabsTestModel()

	// Locate beta's tab on the top bar via the shared hit-test.
	items := sessionTabItems(m.visibleSessions())
	x := -1
	for probe := 0; probe < m.width; probe++ {
		if hitTestTabBar(items, m.selected, m.width, probe) == 1 {
			x = probe
			break
		}
	}
	if x < 0 {
		t.Fatal("beta tab not found on top bar")
	}

	m.handleTabsMouse(tea.MouseMsg{
		X: x, Y: 0,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1 after top bar click", m.selected)
	}
}

func TestTabs_ContentClickStartsLocalSelection(t *testing.T) {
	m := newTabsTestModel()

	// Click inside the content area: row 5 on screen is content row 4.
	m.handleTabsMouse(tea.MouseMsg{
		X: 10, Y: 5,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	if !m.sel.pending || m.sel.session != "alpha" {
		t.Fatalf("sel = %+v, want pending selection in alpha", m.sel)
	}
	if m.sel.startY != 4 {
		t.Fatalf("sel.startY = %d, want 4 (screen row minus tab bar)", m.sel.startY)
	}
}

func TestTabs_FocusedContentSizeReservesTopAndBottom(t *testing.T) {
	m := newTabsTestModel()
	cols, rows := m.focusedContentSize()
	if cols != m.width || rows != m.height-2 {
		t.Fatalf("focusedContentSize = %dx%d, want %dx%d", cols, rows, m.width, m.height-2)
	}
}
