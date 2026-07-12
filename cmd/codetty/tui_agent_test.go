package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"codetty/daemon"
)

func TestAgentBadge(t *testing.T) {
	cases := map[string]string{
		"blocked": "■",
		"working": "●",
		"idle":    "",
		"":        "",
	}
	for state, want := range cases {
		if got := agentBadge(state); got != want {
			t.Errorf("agentBadge(%q) = %q, want %q", state, got, want)
		}
	}
}

func TestAgentBlink_TogglesWhileWorking(t *testing.T) {
	defer func() { agentBlinkSolid = true }()

	m := newTUIModel(nil, "", nil)
	m.width, m.height = 120, 40
	m.sessions = []*tuiSession{newTUISession("alpha", "bash", 80, 24)}
	m.order = []string{"alpha"}
	m.sessions[0].Tabs = []tuiTab{{Name: "cp", Agent: "copilot", AgentState: "working"}}

	// Any update while an agent is working schedules a blink tick.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	if !m.blinkPending || cmd == nil {
		t.Fatalf("blinkPending/cmd = %v/%v, want true/non-nil", m.blinkPending, cmd)
	}

	// The tick toggles the phase and re-arms while still working.
	m.Update(agentBlinkMsg{})
	if agentBlinkSolid {
		t.Fatal("agentBlinkSolid should be false after first tick")
	}
	if !m.blinkPending {
		t.Fatal("tick should re-arm while an agent is working")
	}
	if got := agentBadge("working"); got != "○" {
		t.Fatalf("working badge = %q, want ○ in hollow phase", got)
	}
	m.Update(agentBlinkMsg{})
	if !agentBlinkSolid {
		t.Fatal("agentBlinkSolid should toggle back to true")
	}

	// When no agent works anymore, the phase resets solid and the tick stops.
	m.sessions[0].Tabs[0].AgentState = "idle"
	agentBlinkSolid = false
	m.blinkPending = false
	m.Update(agentBlinkMsg{})
	if !agentBlinkSolid {
		t.Fatal("phase should reset to solid when nothing is working")
	}
	if m.blinkPending {
		t.Fatal("tick should stop when nothing is working")
	}
}

func TestSessionAgentStateAggregation(t *testing.T) {
	sess := &tuiSession{Tabs: []tuiTab{
		{Name: "shell"},
		{Name: "copilot", Agent: "copilot", AgentState: "working"},
		{Name: "copilot2", Agent: "copilot", AgentState: "blocked"},
	}}
	if got := sessionAgentState(sess); got != "blocked" {
		t.Errorf("aggregate = %q, want blocked (most urgent wins)", got)
	}

	sess.Tabs[2].AgentState = "idle"
	if got := sessionAgentState(sess); got != "working" {
		t.Errorf("aggregate = %q, want working", got)
	}

	// A finished-unseen tab outranks working.
	sess.Tabs[2].Done = true
	if got := sessionAgentState(sess); got != "done" {
		t.Errorf("aggregate = %q, want done (outranks working)", got)
	}
	sess.Tabs[2].Done = false

	sess.Tabs[1].AgentState = "idle"
	if got := sessionAgentState(sess); got != "idle" {
		t.Errorf("aggregate = %q, want idle", got)
	}

	sess.Tabs = sess.Tabs[:1]
	if got := sessionAgentState(sess); got != "" {
		t.Errorf("aggregate = %q, want none for plain tabs", got)
	}
}

func TestPanelTitleIsPlain(t *testing.T) {
	sess := &tuiSession{Name: "dev", Tabs: []tuiTab{
		{Name: "copilot", Agent: "copilot", AgentState: "blocked"},
	}}
	// Agent state moved to border/header/status layers; title stays clean.
	if title := panelTitle(1, sess); title != "[1] dev" {
		t.Errorf("panelTitle = %q, want plain [1] dev", title)
	}
}

func TestAgentStateLabel(t *testing.T) {
	cases := map[string]string{
		"blocked": "■ blocked",
		"working": "● working",
		"idle":    "",
		"":        "",
	}
	for state, want := range cases {
		if got := agentStateLabel(state); got != want {
			t.Errorf("agentStateLabel(%q) = %q, want %q", state, got, want)
		}
	}
}

func TestAgentStatusSegment(t *testing.T) {
	m := &tuiModel{}
	if seg := m.agentStatusSegment(); seg != "" {
		t.Fatalf("no agents should yield empty segment, got %q", seg)
	}

	m.sessions = []*tuiSession{
		{Name: "a", Tabs: []tuiTab{
			{Name: "t1", Agent: "copilot", AgentState: "blocked"},
			{Name: "t2", Agent: "copilot", AgentState: "working"},
		}},
		{Name: "b", Tabs: []tuiTab{
			{Name: "t1", Agent: "copilot", AgentState: "working"},
		}},
	}
	seg := m.agentStatusSegment()
	if !strings.Contains(seg, "1 blocked") || !strings.Contains(seg, "2 working") {
		t.Errorf("segment %q missing counts", seg)
	}
}

func TestRenderPanelBorderEncodesAgentState(t *testing.T) {
	m := &tuiModel{width: 80, height: 24}
	blockedSess := newTUISession("dev", "zsh", 40, 10)
	blockedSess.Tabs = []tuiTab{{Name: "main", Agent: "copilot", AgentState: "blocked"}}
	idleSess := newTUISession("idle", "zsh", 40, 10)

	blockedPanel := m.renderPanelWithColor(blockedSess, "[1] dev", 40, 10, false, -1, true)
	idlePanel := m.renderPanelWithColor(idleSess, "[2] idle", 40, 10, false, -1, true)

	// The blocked panel must render differently (red border + header label).
	if blockedPanel == idlePanel {
		t.Fatal("blocked panel renders identically to idle panel")
	}
	if !strings.Contains(blockedPanel, "■ blocked") {
		t.Errorf("blocked panel missing header label")
	}
}

func TestTabBarLabelIncludesBadgeAndKeepsHitTestInSync(t *testing.T) {
	tabs := []tuiTab{
		{Name: "shell"},
		{Name: "copilot", Agent: "copilot", AgentState: "blocked"},
	}

	label := tabBarLabel(1, tabs[1])
	if !strings.Contains(label, "■") {
		t.Fatalf("label %q missing badge", label)
	}

	// Hit test must resolve positions across the badge-widened first tab.
	bar := renderTabBar(tabs, 0, 80, false)
	if !strings.Contains(bar, "copilot") {
		t.Fatalf("tab bar missing tab name: %q", bar)
	}

	w0 := lipgloss.Width(tabBarLabel(0, tabs[0]))
	// First cell of second tab: after tab 0 and the 1-cell separator.
	if got := hitTestTabBar(tabs, 0, 80, w0+1); got != 1 {
		t.Errorf("hit test at second tab start = %d, want 1", got)
	}
	if got := hitTestTabBar(tabs, 0, 80, 0); got != 0 {
		t.Errorf("hit test at origin = %d, want 0", got)
	}
}

func TestRenderTabBarMutedDropsActiveHighlight(t *testing.T) {
	plain := tuiTab{Name: "shell"}
	blocked := tuiTab{Name: "agent", Agent: "copilot", AgentState: "blocked"}
	working := tuiTab{Name: "agent", Agent: "copilot", AgentState: "working"}

	cases := []struct {
		name   string
		tab    tuiTab
		active bool
		muted  bool
		want   string
	}{
		{"big panel active keeps highlight", plain, true, false, "active"},
		{"big panel active wins over blocked", blocked, true, false, "active"},
		{"side panel active gets no emphasis", plain, true, true, "inactive"},
		{"side panel blocked outranks active", blocked, true, true, "blocked"},
		{"side panel working outranks active", working, true, true, "working"},
		{"inactive blocked always colored", blocked, false, false, "blocked"},
		{"plain inactive", plain, false, true, "inactive"},
	}
	for _, c := range cases {
		if got := tabStyleKind(c.tab, c.active, c.muted); got != c.want {
			t.Errorf("%s: tabStyleKind = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAgentDoneAfterUpdate(t *testing.T) {
	working := tuiTab{Agent: "copilot", AgentState: "working"}
	idle := tuiTab{Agent: "copilot", AgentState: "idle"}
	exited := tuiTab{}
	blocked := tuiTab{Agent: "copilot", AgentState: "blocked"}

	cases := []struct {
		name     string
		old, new tuiTab
		want     bool
	}{
		{"working to idle marks done", working, idle, true},
		{"working to exited marks done", working, exited, true},
		{"working stays working", working, working, false},
		{"working to blocked is not done", working, blocked, false},
		{"idle stays idle without prior done", idle, idle, false},
		{"done persists across idle updates", tuiTab{AgentState: "idle", Done: true}, idle, true},
		{"new working clears done", tuiTab{AgentState: "idle", Done: true}, working, false},
		{"new blocked clears done", tuiTab{AgentState: "idle", Done: true}, blocked, false},
	}
	for _, c := range cases {
		if got := agentDoneAfterUpdate(c.old, c.new); got != c.want {
			t.Errorf("%s: agentDoneAfterUpdate = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestEffectiveAgentState(t *testing.T) {
	if got := effectiveAgentState(tuiTab{AgentState: "idle", Done: true}); got != "done" {
		t.Errorf("done idle tab = %q, want done", got)
	}
	if got := effectiveAgentState(tuiTab{AgentState: "", Done: true}); got != "done" {
		t.Errorf("done exited tab = %q, want done", got)
	}
	if got := effectiveAgentState(tuiTab{AgentState: "working", Done: true}); got != "working" {
		t.Errorf("working tab must ignore stale done flag, got %q", got)
	}
	if got := effectiveAgentState(tuiTab{AgentState: "idle"}); got != "idle" {
		t.Errorf("plain idle = %q", got)
	}
}

func TestAckAgentDoneSessionClearsOnlyActiveTab(t *testing.T) {
	m := &tuiModel{}
	front := newTUISession("front", "zsh", 80, 24)
	front.Tabs = []tuiTab{
		{Name: "t1", AgentState: "idle", Done: true},
		{Name: "t2", AgentState: "idle", Done: true},
	}
	front.ActiveTab = 0
	back := newTUISession("back", "zsh", 80, 24)
	back.Tabs = []tuiTab{{Name: "t1", AgentState: "idle", Done: true}}
	m.sessions = []*tuiSession{front, back}
	m.order = []string{"front", "back"}

	m.ackAgentDoneSession("front")

	if front.Tabs[0].Done {
		t.Error("active tab of acked session must be cleared")
	}
	if !front.Tabs[1].Done {
		t.Error("background tab of acked session must stay done")
	}
	if !back.Tabs[0].Done {
		t.Error("other session must stay done")
	}
}

// TestDoneRequiresUserInteraction verifies the done marker survives daemon
// events and clears when the user navigates to the done panel.
func TestDoneRequiresUserInteraction(t *testing.T) {
	m := &tuiModel{floatingIdx: -1, selected: 0, width: 80, height: 24}
	dev := newTUISession("dev", "zsh", 80, 24)
	dev.Tabs = []tuiTab{{Name: "main", Agent: "copilot", AgentState: "working"}}
	other := newTUISession("other", "zsh", 80, 24)
	m.sessions = []*tuiSession{other, dev}
	m.order = []string{"other", "dev"}

	// Agent finishes in the background panel: daemon event sets done…
	m.Update(daemonSessionEventMsg{Event: daemon.SessionEvent{
		Type:    "agent_state",
		Session: "dev",
		Info: daemon.SessionInfo{
			Name: "dev",
			Tabs: []daemon.TabInfo{{Name: "main", Command: "copilot", Agent: "copilot", AgentState: "idle"}},
		},
	}})
	if !dev.Tabs[0].Done {
		t.Fatal("working→idle must set done")
	}

	// …more daemon traffic must not acknowledge it…
	m.Update(daemonOutputMsg{Session: "dev", Data: []byte("x")})
	if !dev.Tabs[0].Done {
		t.Fatal("daemon events must not acknowledge done")
	}

	// …a key that doesn't navigate must not acknowledge it either
	// (nil conn: nothing is forwarded, attention target unchanged)…
	m.prefix = true
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if !dev.Tabs[0].Done {
		t.Fatal("non-navigating interaction must not acknowledge a background panel")
	}

	// …but navigating to the done panel does (prefix + n → select next).
	m.prefix = true
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.selected != 1 {
		t.Fatalf("expected navigation to select session 1, got %d", m.selected)
	}
	if dev.Tabs[0].Done {
		t.Fatal("navigating to the done panel must acknowledge it")
	}
	if other.Tabs != nil && len(other.Tabs) > 0 && other.Tabs[0].Done {
		t.Fatal("other session must be unaffected")
	}
}

func TestSyncTabInfoMarksDoneOnWorkingToIdle(t *testing.T) {
	m := &tuiModel{}
	sess := &tuiSession{Name: "dev", Tabs: []tuiTab{
		{Name: "main", Agent: "copilot", AgentState: "working"},
	}}
	m.syncTabInfo(sess, daemon.SessionInfo{
		Tabs: []daemon.TabInfo{
			{Name: "main", Command: "copilot", Agent: "copilot", AgentState: "idle"},
		},
	})
	if !sess.Tabs[0].Done {
		t.Fatal("working→idle must set the done marker")
	}
	if got := effectiveAgentState(sess.Tabs[0]); got != "done" {
		t.Fatalf("effective state = %q, want done", got)
	}
}

func TestSyncTabInfoCopiesAgentState(t *testing.T) {
	m := &tuiModel{}
	sess := &tuiSession{Name: "dev"}
	m.syncTabInfo(sess, daemon.SessionInfo{
		Tabs: []daemon.TabInfo{
			{Name: "main", Command: "copilot", Agent: "copilot", AgentState: "working"},
		},
	})
	if len(sess.Tabs) != 1 || sess.Tabs[0].Agent != "copilot" || sess.Tabs[0].AgentState != "working" {
		t.Fatalf("syncTabInfo did not copy agent state: %+v", sess.Tabs)
	}
}

func TestHandleSessionEventAgentState(t *testing.T) {
	m := &tuiModel{}
	sess := newTUISession("dev", "zsh", 80, 24)
	m.sessions = append(m.sessions, sess)
	m.order = append(m.order, "dev")

	m.handleSessionEvent(daemon.SessionEvent{
		Type:    "agent_state",
		Session: "dev",
		Info: daemon.SessionInfo{
			Name: "dev",
			Tabs: []daemon.TabInfo{
				{Name: "main", Command: "copilot", Agent: "copilot", AgentState: "blocked"},
			},
		},
	})

	if len(sess.Tabs) != 1 || sess.Tabs[0].AgentState != "blocked" {
		t.Fatalf("agent_state event not applied: %+v", sess.Tabs)
	}
}
