package agentdetect

import (
	"testing"
	"time"
)

func copilot() *AgentSpec {
	spec := copilotSpec()
	spec.sortRules()
	return spec
}

func TestCopilotBlockedOnApprovalPrompt(t *testing.T) {
	screens := []string{
		// tool approval selection
		"Run this command?\n" +
			"  1. Yes\n  2. No\n" +
			"Enter to confirm · Esc to cancel",
		// ask_user style select
		"? Pick an option\n> option A\n  option B\n" +
			"enter to select · esc cancel",
	}
	for _, screen := range screens {
		if got := copilot().Evaluate(screen); got != StateBlocked {
			t.Errorf("Evaluate(%q) = %q, want blocked", screen, got)
		}
	}
}

func TestCopilotWorkingOnCancelHint(t *testing.T) {
	screens := []string{
		"⠧ Thinking...\nEsc to cancel",
		"Working on it\npress esc again to cancel",
		"Generating\nEsc interrupt",
	}
	for _, screen := range screens {
		if got := copilot().Evaluate(screen); got != StateWorking {
			t.Errorf("Evaluate(%q) = %q, want working", screen, got)
		}
	}
}

func TestCopilotIdleWithoutSignals(t *testing.T) {
	screen := "How can I help you today?\n> \n"
	if got := copilot().Evaluate(screen); got != StateIdle {
		t.Errorf("Evaluate(%q) = %q, want idle", screen, got)
	}
}

func TestBlockedOutranksWorking(t *testing.T) {
	// Both hints on screen: blocker rule has higher priority.
	screen := "Esc to cancel · Enter to confirm"
	if got := copilot().Evaluate(screen); got != StateBlocked {
		t.Errorf("Evaluate(%q) = %q, want blocked", screen, got)
	}
}

func TestIdentifyCopilotBinaries(t *testing.T) {
	for _, name := range []string{"copilot", "github-copilot", "ghcs", "/usr/local/bin/copilot", "COPILOT.exe"} {
		if spec := Identify(name, nil); spec == nil || spec.Name != "copilot" {
			t.Errorf("Identify(%q) failed", name)
		}
	}
}

func TestIdentifyUnwrapsNodeRuntime(t *testing.T) {
	spec := Identify("node", []string{"node", "--max-old-space-size=4096", "/opt/copilot/index.js"})
	if spec != nil {
		t.Fatalf("index.js should not identify copilot, got %v", spec)
	}
	spec = Identify("node", []string{"node", "/usr/local/bin/copilot"})
	if spec == nil || spec.Name != "copilot" {
		t.Fatalf("node-wrapped copilot not identified")
	}
}

func TestIdentifyRejectsPlainShell(t *testing.T) {
	if spec := Identify("bash", []string{"bash"}); spec != nil {
		t.Errorf("bash should not identify an agent")
	}
	if spec := Identify("vim", []string{"vim", "main.go"}); spec != nil {
		t.Errorf("vim should not identify an agent")
	}
}

func TestDetectorHoldsWorkingToIdle(t *testing.T) {
	var d Detector
	now := time.Now()

	state, changed := d.Commit("copilot", StateWorking, 1, now)
	if state != StateWorking || !changed {
		t.Fatalf("initial working: state=%q changed=%v", state, changed)
	}

	// First two idle readings are held.
	for i := 1; i <= 2; i++ {
		state, changed = d.Commit("copilot", StateIdle, uint64(1+i), now.Add(time.Duration(i)*500*time.Millisecond))
		if state != StateWorking || changed {
			t.Fatalf("idle reading %d: state=%q changed=%v, want held working", i, state, changed)
		}
	}

	// Third consecutive idle commits.
	state, changed = d.Commit("copilot", StateIdle, 4, now.Add(1500*time.Millisecond))
	if state != StateIdle || !changed {
		t.Fatalf("confirmed idle: state=%q changed=%v", state, changed)
	}
}

func TestDetectorWorkingInterruptsIdleHold(t *testing.T) {
	var d Detector
	now := time.Now()

	d.Commit("copilot", StateWorking, 1, now)
	d.Commit("copilot", StateIdle, 2, now.Add(500*time.Millisecond)) // held

	state, changed := d.Commit("copilot", StateWorking, 3, now.Add(time.Second))
	if state != StateWorking || changed {
		t.Fatalf("resumed working: state=%q changed=%v (state unchanged, no publish)", state, changed)
	}
}

func TestDetectorBlockedBypassesHold(t *testing.T) {
	var d Detector
	now := time.Now()

	d.Commit("copilot", StateWorking, 1, now)
	state, changed := d.Commit("copilot", StateBlocked, 2, now.Add(500*time.Millisecond))
	if state != StateBlocked || !changed {
		t.Fatalf("blocked: state=%q changed=%v, want immediate publish", state, changed)
	}
}

func TestDetectorAgentExitBypassesHold(t *testing.T) {
	var d Detector
	now := time.Now()

	d.Commit("copilot", StateWorking, 1, now)
	state, changed := d.Commit("", StateIdle, 2, now.Add(500*time.Millisecond))
	if state != StateUnknown || !changed {
		t.Fatalf("agent gone: state=%q changed=%v, want unknown published", state, changed)
	}
}

func TestDetectorNeedsScan(t *testing.T) {
	var d Detector
	now := time.Now()

	if !d.NeedsScan(1, now) {
		t.Fatal("fresh detector must scan")
	}
	d.Commit("copilot", StateWorking, 1, now)

	if d.NeedsScan(1, now.Add(time.Second)) {
		t.Fatal("unchanged seq should skip scan")
	}
	if !d.NeedsScan(2, now.Add(time.Second)) {
		t.Fatal("changed seq must scan")
	}
	if !d.NeedsScan(1, now.Add(maxStaleness)) {
		t.Fatal("stale commit must rescan")
	}

	// Pending idle hold keeps scanning even with unchanged seq.
	d.Commit("copilot", StateIdle, 2, now.Add(500*time.Millisecond))
	if !d.NeedsScan(2, now.Add(600*time.Millisecond)) {
		t.Fatal("pending idle hold must keep scanning")
	}
}
