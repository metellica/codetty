package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
)

func newFocusBigTestModel() *tuiModel {
	m := newTUIModel(nil, "", nil)
	m.width = 120
	m.height = 40
	m.sessions = []*tuiSession{
		newTUISession("alpha", "bash", 80, 24),
		newTUISession("beta", "bash", 80, 24),
		newTUISession("gamma", "bash", 80, 24),
	}
	m.order = []string{"alpha", "beta", "gamma"}
	m.mode = modeFocusBig
	m.selected = 0
	return m
}

func TestPrefixB_SwitchesToFocusBigAndBack(t *testing.T) {
	m := newFocusBigTestModel()
	m.mode = modeList

	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlA})
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if m.mode != modeFocusBig {
		t.Fatalf("mode = %v, want modeFocusBig", m.mode)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlA})
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if m.mode != modeList {
		t.Fatalf("mode = %v, want modeList", m.mode)
	}
}

func TestPrefixF_FocusBigToFullscreenAndBack(t *testing.T) {
	m := newFocusBigTestModel()

	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlA})
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if m.mode != modeFullscreen {
		t.Fatalf("mode = %v, want modeFullscreen", m.mode)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlA})
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if m.mode != modeFocusBig {
		t.Fatalf("mode = %v, want modeFocusBig (pop back)", m.mode)
	}
}

func TestFocusBig_ShiftTabCyclesHighlightThenBigPanel(t *testing.T) {
	m := newFocusBigTestModel()

	// Highlight cycles through side panels (selected excluded)…
	for i, want := range []int{1, 2, -1} {
		pressPrefixTab(m)
		if m.focusCursor != want {
			t.Fatalf("press %d: focusCursor = %d, want %d", i+1, m.focusCursor, want)
		}
		if m.selected != 0 {
			t.Fatalf("selected = %d, want 0 (cycling must not switch)", m.selected)
		}
		if m.mode != modeFocusBig {
			t.Fatalf("mode = %v, want modeFocusBig", m.mode)
		}
	}
}

func TestFocusBig_EnterOpensFloating_SpacePromotes(t *testing.T) {
	m := newFocusBigTestModel()

	// Enter on the highlighted side panel opens it in a floating window.
	pressPrefixTab(m) // highlight beta
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.floatingIdx != 1 {
		t.Fatalf("floatingIdx = %d, want 1 (beta floating)", m.floatingIdx)
	}
	if m.selected != 0 {
		t.Fatalf("selected = %d, want 0 (floating must not switch big panel)", m.selected)
	}
	if m.focusCursor != -1 {
		t.Fatalf("focusCursor = %d, want -1 after Enter", m.focusCursor)
	}
	m.closeFloating()
	if m.mode != modeFocusBig || m.floatingIdx != -1 {
		t.Fatalf("mode/floatingIdx = %v/%d, want modeFocusBig/-1 after close", m.mode, m.floatingIdx)
	}

	// Space on the highlighted side panel promotes it to the big panel.
	pressPrefixTab(m) // highlight beta
	m.handleKey(tea.KeyMsg{Type: tea.KeySpace})
	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1 (beta promoted)", m.selected)
	}
	if m.floatingIdx != -1 {
		t.Fatalf("floatingIdx = %d, want -1 (Space must not float)", m.floatingIdx)
	}
	if m.focusCursor != -1 {
		t.Fatalf("focusCursor = %d, want -1 after Space", m.focusCursor)
	}
}

func TestFocusBig_HighlightKeysDoNotLeakToPTY(t *testing.T) {
	m := newFocusBigTestModel()

	pressPrefixTab(m) // highlight beta
	// ↑/↓ move the highlight among side panels (wrap).
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.focusCursor != 2 {
		t.Fatalf("focusCursor = %d, want 2 after Down", m.focusCursor)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.focusCursor != 1 {
		t.Fatalf("focusCursor = %d, want 1 (wrap)", m.focusCursor)
	}
	// Any other key returns focus to the big panel without forwarding.
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.focusCursor != -1 {
		t.Fatalf("focusCursor = %d, want -1 after other key", m.focusCursor)
	}
	if m.selected != 0 {
		t.Fatalf("selected = %d, want 0", m.selected)
	}
}

func TestFocusBig_ClickSidePanelOpensFloating(t *testing.T) {
	m := newFocusBigTestModel()

	mainW, _ := m.focusBigLayoutWidths()
	// Second side panel: availH = 39, 2 secondaries → slotH 19; gamma
	// occupies rows 19+ in the side column.
	m.handleMouse(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      mainW + 2,
		Y:      25,
	})
	if m.floatingIdx != 2 {
		t.Fatalf("floatingIdx = %d, want 2 (gamma floating)", m.floatingIdx)
	}
	if m.selected != 0 {
		t.Fatalf("selected = %d, want 0 (plain click must not switch big panel)", m.selected)
	}
}

func TestFocusBig_CtrlClickSidePanelPromotes(t *testing.T) {
	m := newFocusBigTestModel()

	mainW, _ := m.focusBigLayoutWidths()
	m.handleMouse(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		Ctrl:   true,
		X:      mainW + 2,
		Y:      25,
	})
	if m.selected != 2 {
		t.Fatalf("selected = %d, want 2 (gamma promoted)", m.selected)
	}
	if m.floatingIdx != -1 {
		t.Fatalf("floatingIdx = %d, want -1 (Ctrl+click must not float)", m.floatingIdx)
	}

	// First side panel now shows alpha (selected is excluded).
	m.handleMouse(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		Ctrl:   true,
		X:      mainW + 2,
		Y:      3,
	})
	if m.selected != 0 {
		t.Fatalf("selected = %d, want 0 (alpha promoted)", m.selected)
	}
}

func TestFocusBig_ClickBigPanelDoesNotSwitch(t *testing.T) {
	m := newFocusBigTestModel()
	m.selected = 1

	m.handleMouse(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      10,
		Y:      5,
	})
	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1 (big panel click must not switch)", m.selected)
	}
	// A plain-shell big panel starts a pending text selection instead of
	// forwarding the click.
	if !m.sel.pending {
		t.Fatal("expected pending selection after big panel click")
	}
	if m.sel.session != "beta" {
		t.Fatalf("sel.session = %q, want beta", m.sel.session)
	}
}

func TestFocusBig_HitTest(t *testing.T) {
	m := newFocusBigTestModel()
	mainW, _ := m.focusBigLayoutWidths()

	if idx := m.hitTest(mainW-1, 5); idx != 0 {
		t.Fatalf("big panel hit = %d, want 0 (selected)", idx)
	}
	if idx := m.hitTest(mainW, 5); idx != 1 {
		t.Fatalf("first side panel hit = %d, want 1", idx)
	}
	if idx := m.hitTest(mainW, 25); idx != 2 {
		t.Fatalf("second side panel hit = %d, want 2", idx)
	}
	if idx := m.hitTest(mainW, m.height-1); idx != -1 {
		t.Fatalf("status bar hit = %d, want -1", idx)
	}
}

func TestFocusBig_PanelRegionsCoverOnlyBigPanel(t *testing.T) {
	m := newFocusBigTestModel()

	regions := m.panelRegions()
	if len(regions) != 1 {
		t.Fatalf("regions = %d, want 1 (side panels never forward input)", len(regions))
	}
	r := regions[0]
	if r.sessionIdx != m.selected {
		t.Fatalf("region sessionIdx = %d, want %d", r.sessionIdx, m.selected)
	}
	mainW, _ := m.focusBigLayoutWidths()
	if r.contentX != 1 || r.contentW != mainW-2 {
		t.Fatalf("region x/w = %d/%d, want 1/%d", r.contentX, r.contentW, mainW-2)
	}
	// Same content geometry as the list-mode big panel: borders + bottom
	// info line.
	if r.contentY != 1 || r.contentH != m.height-1-3 {
		t.Fatalf("region y/h = %d/%d, want 1/%d", r.contentY, r.contentH, m.height-1-3)
	}
}

func TestFocusBig_ContentSizeMatchesBigPanel(t *testing.T) {
	m := newFocusBigTestModel()

	mainW, _ := m.focusBigLayoutWidths()
	cols, rows := m.focusedContentSize()
	if cols != mainW-2 || rows != m.height-1-3 {
		t.Fatalf("content = %dx%d, want %dx%d", cols, rows, mainW-2, m.height-1-3)
	}

	// Single session: big panel takes the full width.
	m.sessions = m.sessions[:1]
	m.order = m.order[:1]
	cols, rows = m.focusedContentSize()
	if cols != m.width-2 || rows != m.height-1-3 {
		t.Fatalf("single-session content = %dx%d, want %dx%d", cols, rows, m.width-2, m.height-1-3)
	}
}

func TestSessionAgentBadge(t *testing.T) {
	agentBlinkSolid = true
	cases := []struct {
		name string
		tabs []tuiTab
		want string
	}{
		{"no agent", []tuiTab{{Name: "shell"}}, ""},
		{"idle agent", []tuiTab{{Name: "cp", Agent: "copilot", AgentState: "idle"}}, "○"},
		{"working agent", []tuiTab{{Name: "cp", Agent: "copilot", AgentState: "working"}}, "●"},
		{"blocked agent", []tuiTab{{Name: "cp", Agent: "copilot", AgentState: "blocked"}}, "■"},
		{"done agent", []tuiTab{{Name: "cp", Agent: "copilot", AgentState: "idle", Done: true}}, "●"},
		{"blocked wins", []tuiTab{
			{Name: "a", Agent: "copilot", AgentState: "working"},
			{Name: "b", Agent: "copilot", AgentState: "blocked"},
		}, "■"},
	}
	for _, tc := range cases {
		sess := &tuiSession{Name: "s", Tabs: tc.tabs}
		if got := sessionAgentBadge(sess); got != tc.want {
			t.Errorf("%s: badge = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSessionAgentBadge_WorkingBlinksHollow(t *testing.T) {
	sess := &tuiSession{Name: "s", Tabs: []tuiTab{
		{Name: "cp", Agent: "copilot", AgentState: "working"},
	}}
	agentBlinkSolid = false
	defer func() { agentBlinkSolid = true }()
	if got := sessionAgentBadge(sess); got != "○" {
		t.Fatalf("badge = %q, want ○ (hollow blink phase)", got)
	}
}

func TestFocusSideTitleColors(t *testing.T) {
	if _, bg := focusSideTitleColors("blocked"); bg != agentBlockedColor {
		t.Fatalf("blocked bg = %v, want %v", bg, agentBlockedColor)
	}
	if _, bg := focusSideTitleColors("working"); bg != agentWorkingColor {
		t.Fatalf("working bg = %v, want %v", bg, agentWorkingColor)
	}
	if _, bg := focusSideTitleColors("done"); bg != agentDoneColor {
		t.Fatalf("done bg = %v, want %v", bg, agentDoneColor)
	}
	if _, bg := focusSideTitleColors("idle"); bg != sidePanelTitleColor {
		t.Fatalf("idle bg = %v, want %v (no agent event keeps default)", bg, sidePanelTitleColor)
	}
	if _, bg := focusSideTitleColors(""); bg != sidePanelTitleColor {
		t.Fatalf("empty bg = %v, want %v", bg, sidePanelTitleColor)
	}
}

func TestRenderFocusSidePanel_TitleBadgeOnlyWithAgent(t *testing.T) {
	agentBlinkSolid = true
	m := newFocusBigTestModel()

	plain := m.sessions[1]
	view := xansi.Strip(m.renderFocusSidePanel(plain, 2, 30, 10, false))
	titleLine := strings.SplitN(view, "\n", 3)[1]
	if !strings.Contains(titleLine, "[2] beta") {
		t.Fatalf("title missing session name: %q", titleLine)
	}
	for _, badge := range []string{"●", "○", "■"} {
		if strings.Contains(titleLine, badge) {
			t.Fatalf("no-agent title must not show %q: %q", badge, titleLine)
		}
	}

	agent := m.sessions[2]
	agent.Tabs = []tuiTab{{Name: "cp", Agent: "copilot", AgentState: "working"}}
	view = xansi.Strip(m.renderFocusSidePanel(agent, 3, 30, 10, false))
	titleLine = strings.SplitN(view, "\n", 3)[1]
	if !strings.Contains(titleLine, "●") {
		t.Fatalf("working-agent title must show badge: %q", titleLine)
	}
	if strings.Contains(titleLine, "copilot") || strings.Contains(titleLine, "working") {
		t.Fatalf("title must not leak agent details: %q", titleLine)
	}
}

func TestFocusBigView_RendersSidePanels(t *testing.T) {
	m := newFocusBigTestModel()
	m.sessions[1].Tabs = []tuiTab{{Name: "cp", Agent: "copilot", AgentState: "blocked"}}

	view := xansi.Strip(m.focusBigView())
	if !strings.Contains(view, "[2] beta ■") {
		t.Fatalf("view missing blocked side panel title, got:\n%s", view)
	}
	if !strings.Contains(view, "[3] gamma") {
		t.Fatalf("view missing gamma side panel title")
	}
	// Big panel bottom info line shows the selected session (list-mode style).
	if !strings.Contains(view, "[1] alpha") {
		t.Fatalf("view missing big panel info line")
	}
}

func TestFocusBig_ShiftTabPrioritizesAgentState(t *testing.T) {
	m := newFocusBigTestModel()
	// alpha (selected=0) is the big panel.
	// beta (idx 1) has a "done" agent.
	// gamma (idx 2) has a "blocked" agent.
	m.sessions[1].Tabs = []tuiTab{{Name: "shell", Agent: "copilot", AgentState: "idle", Done: true}}
	m.sessions[2].Tabs = []tuiTab{{Name: "shell", Agent: "copilot", AgentState: "blocked"}}

	// First Shift+Tab should land on the blocked session (gamma=2).
	pressPrefixTab(m)
	if m.focusCursor != 2 {
		t.Fatalf("press 1: focusCursor = %d, want 2 (blocked)", m.focusCursor)
	}
	// Second should land on the done session (beta=1).
	pressPrefixTab(m)
	if m.focusCursor != 1 {
		t.Fatalf("press 2: focusCursor = %d, want 1 (done)", m.focusCursor)
	}
	// Third returns to big panel.
	pressPrefixTab(m)
	if m.focusCursor != -1 {
		t.Fatalf("press 3: focusCursor = %d, want -1 (big panel)", m.focusCursor)
	}
}
