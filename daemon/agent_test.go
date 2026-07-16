package daemon

import (
	"testing"
	"time"

	"codetty/agentdetect"
)

// TestSessionInfo_IncludesTabAgentState verifies TabInfos carries agent
// detection results and single-tab native sessions expose Tabs in Info.
func TestSessionInfo_IncludesTabAgentState(t *testing.T) {
	sess, err := newDaemonSession("", "agent-info", []string{"cat"}, 80, 24, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Kill()

	sess.mu.Lock()
	sess.Tabs[0].agent = "copilot"
	sess.Tabs[0].agentState = agentdetect.StateBlocked
	sess.mu.Unlock()

	info := sess.Info()
	if len(info.Tabs) != 1 {
		t.Fatalf("single-tab native session should expose Tabs, got %d", len(info.Tabs))
	}
	if info.Tabs[0].Agent != "copilot" || info.Tabs[0].AgentState != "blocked" {
		t.Errorf("tab info agent = %q/%q, want copilot/blocked", info.Tabs[0].Agent, info.Tabs[0].AgentState)
	}
}

// TestPollAgentStates_ShellIsUnknown verifies a plain shell tab publishes
// no agent and polling reports no change on a steady state.
func TestPollAgentStates_ShellIsUnknown(t *testing.T) {
	sess, err := newDaemonSession("", "agent-shell", []string{"cat"}, 80, 24, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Kill()

	now := time.Now()
	// First poll commits the initial (unknown) observation; no change to
	// publish since the zero state is already unknown.
	if changed := sess.PollAgentStates(now); changed {
		t.Error("first poll of a plain shell should not report a change")
	}
	if changed := sess.PollAgentStates(now.Add(time.Second)); changed {
		t.Error("steady shell should not report changes")
	}

	sess.mu.Lock()
	agent, state := sess.Tabs[0].agent, sess.Tabs[0].agentState
	sess.mu.Unlock()
	if agent != "" || state != agentdetect.StateUnknown {
		t.Errorf("shell tab published agent=%q state=%q, want none", agent, state)
	}
}
