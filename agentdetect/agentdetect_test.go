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
		if got := copilot().Evaluate("", screen); got != StateBlocked {
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
		if got := copilot().Evaluate("", screen); got != StateWorking {
			t.Errorf("Evaluate(%q) = %q, want working", screen, got)
		}
	}
}

func TestCopilotIdleWithoutSignals(t *testing.T) {
	screen := "How can I help you today?\n> \n"
	if got := copilot().Evaluate("", screen); got != StateIdle {
		t.Errorf("Evaluate(%q) = %q, want idle", screen, got)
	}
}

func TestCopilotIdlePromptOverridesStaleWorking(t *testing.T) {
	// After copilot finishes, "esc interrupt" may linger on screen
	// above the idle prompt. The idle rule (higher priority) should win.
	screens := []string{
		"esc interrupt\n\n/ commands · ? help · → next tab",
		"Esc interrupt\nResult: done\n/ commands",
		"some output\nesc interrupt\n? help",
	}
	for _, screen := range screens {
		if got := copilot().Evaluate("", screen); got != StateIdle {
			t.Errorf("Evaluate(%q) = %q, want idle", screen, got)
		}
	}
}

func TestCopilotBackgroundAgentsWorking(t *testing.T) {
	// Both toggle9 spinner frames (◉/◎) must be recognized (issue: the
	// status bar was seen showing ◉, but the upstream herdr manifest
	// this rule mirrors only checked ◎).
	screens := []string{
		"◉ Waiting for background agents · 94.8 KiB esc stop agents",
		"◎ Waiting for background agents · 12.0 KiB esc stop agents",
		"◎ Waiting for background agents\n",
	}
	for _, screen := range screens {
		if got := copilot().Evaluate("", screen); got != StateWorking {
			t.Errorf("Evaluate(%q) = %q, want working", screen, got)
		}
	}
}

func TestBlockedOutranksWorking(t *testing.T) {
	// Both hints on screen: blocker rule has higher priority.
	screen := "Esc to cancel · Enter to confirm"
	if got := copilot().Evaluate("", screen); got != StateBlocked {
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

	// First two idle readings are held (herdr: 100ms recheck cadence).
	for i := 1; i <= 2; i++ {
		state, changed = d.Commit("copilot", StateIdle, uint64(1+i), now.Add(time.Duration(i)*100*time.Millisecond))
		if state != StateWorking || changed {
			t.Fatalf("idle reading %d: state=%q changed=%v, want held working", i, state, changed)
		}
	}

	// Third consecutive idle commits.
	state, changed = d.Commit("copilot", StateIdle, 4, now.Add(300*time.Millisecond))
	if state != StateIdle || !changed {
		t.Fatalf("confirmed idle: state=%q changed=%v", state, changed)
	}
}

func TestDetectorIdleHoldCapExpires(t *testing.T) {
	var d Detector
	now := time.Now()

	d.Commit("copilot", StateWorking, 1, now)
	d.Commit("copilot", StateIdle, 2, now.Add(100*time.Millisecond)) // hold starts

	// Past the 700ms cap the idle reading commits regardless of count.
	state, changed := d.Commit("copilot", StateIdle, 3, now.Add(100*time.Millisecond+idleHoldCap))
	if state != StateIdle || !changed {
		t.Fatalf("cap expiry: state=%q changed=%v, want committed idle", state, changed)
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

// ---- claude helpers ----

func claude() *AgentSpec {
	spec := claudeSpec()
	spec.sortRules()
	return spec
}

func TestClaudeBlockedOnPermissionPrompt(t *testing.T) {
	screens := []string{
		// Full permission prompt.
		"Do you want to proceed?\n\n" +
			"❯ 1. Yes\n" +
			"  2. Yes, and don't ask again for Bash commands in /root/project\n" +
			"  3. No, and tell Claude what to do differently",
		// "This command requires approval" variant.
		"This command requires approval\n\n" +
			"Do you want to proceed?\n" +
			"❯ 1. Yes\n  2. Yes, and don't ask again\n  3. No",
		// Numbered menu with Yes/No.
		"Choose an option\n" +
			"❯ 1. Yes, allow\n" +
			"  2. No, deny",
	}
	for _, screen := range screens {
		if got := claude().Evaluate("", screen); got != StateBlocked {
			t.Errorf("Evaluate(%q) = %q, want blocked", screen, got)
		}
	}
}

func TestClaudeWorkingOnSpinner(t *testing.T) {
	screens := []string{
		// Canonical spinner line.
		"✻ Thinking… (3s · ↓ 174 tokens · thinking)",
		// Different spinner frame and verb.
		"✽ Herding… (8m 39s · ↓ 834 tokens)",
		// Generic spinner char + verb + ellipsis.
		"✶ Cooking… (12s · ↓ 2.1k tokens)",
		// Reduced-motion static indicator.
		"● Thinking… (5s · ↑ 300 tokens)",
		// Braille spinner (older/mobile builds).
		"⠧ Processing…",
		// Esc interrupt hint during processing.
		"✻ Thinking…\n\nEsc to interrupt",
		// Ctrl+C interrupt hint.
		"⠇ Working…\nCtrl+C to interrupt",
	}
	for _, screen := range screens {
		if got := claude().Evaluate("", screen); got != StateWorking {
			t.Errorf("Evaluate(%q) = %q, want working", screen, got)
		}
	}
}

func TestClaudeTitleWorking(t *testing.T) {
	// The OSC title's Braille spinner prefix is the primary working
	// signal (herdr: osc_title_working); it wins even when the screen
	// shows only stale idle-looking text.
	titles := []string{"⠐ Claude Code", "⠧ Count from 1 to 5", "⣿ Claude"}
	for _, title := range titles {
		if got := claude().Evaluate(title, "❯ \n"); got != StateWorking {
			t.Errorf("Evaluate(title=%q) = %q, want working", title, got)
		}
	}
}

func TestClaudeTitleIdleOverridesStaleScreenWorking(t *testing.T) {
	// Regression: after a turn finishes, the past-tense completion line
	// ("✻ Cooked for 7s") and other transcript text persist on screen.
	// The "✳ " idle title must win so the state doesn't stick at working.
	screens := []string{
		"● 1\n  2\n  3\n\n✻ Cooked for 7s\n\n❯ ",
		"✻ Worked for 1m 52s\n\n❯ ",
		"● Done… almost everything is wired up.\n\n❯ ",
	}
	for _, screen := range screens {
		if got := claude().Evaluate("✳ Claude Code", screen); got != StateIdle {
			t.Errorf("Evaluate(%q) = %q, want idle", screen, got)
		}
	}
}

func TestClaudePastTenseCompletionIsNotWorking(t *testing.T) {
	// Past-tense completion lines persist after the turn ends; without
	// any title they must fall through to idle, not working.
	screens := []string{
		"✻ Worked for 1m 52s\n\n❯ ",
		"✽ Baked for 45s\n\n❯ ",
		"✶ Cooked for 2m 10s\n\n❯ ",
	}
	for _, screen := range screens {
		if got := claude().Evaluate("", screen); got != StateIdle {
			t.Errorf("Evaluate(%q) = %q, want idle", screen, got)
		}
	}
}

func TestClaudeTitleWorkingHalfCircleSpinner(t *testing.T) {
	// Claude Code >= 2.1.228 switched its title spinner to half-circle
	// glyphs (U+25D0-25D3) instead of Braille frames.
	if got := claude().Evaluate("◑ Claude Code", "❯ \n"); got != StateWorking {
		t.Errorf("Evaluate(title=half-circle) = %q, want working", got)
	}
}

func TestClaudeBackgroundAgentsWorking(t *testing.T) {
	screens := []string{
		"✻ Waiting for 3 background agents to finish",
		"* Waiting for 1 background agent to finish",
	}
	for _, screen := range screens {
		if got := claude().Evaluate("", screen); got != StateWorking {
			t.Errorf("Evaluate(%q) = %q, want working", screen, got)
		}
	}
}

func TestClaudeBackgroundMCPTaskWorking(t *testing.T) {
	screen := "✻ Running tests · 2 MCP tasks still running"
	if got := claude().Evaluate("", screen); got != StateWorking {
		t.Errorf("Evaluate(%q) = %q, want working", screen, got)
	}
}

func TestClaudeBtwOverlayWorking(t *testing.T) {
	screen := "/btw ask a quick question\n\nEsc to close"
	if got := claude().Evaluate("", screen); got != StateWorking {
		t.Errorf("Evaluate(%q) = %q, want working", screen, got)
	}
}

func TestClaudeDynamicWorkflowPromptBlocked(t *testing.T) {
	screen := "Run a dynamic workflow?\n\nEsc to cancel"
	if got := claude().Evaluate("", screen); got != StateBlocked {
		t.Errorf("Evaluate(%q) = %q, want blocked", screen, got)
	}
}

func TestClaudeMCPElicitationPromptBlocked(t *testing.T) {
	screen := "MCP server \"linear\" requests your input\n\n" +
		"❯ Accept\n  Decline\n\n" +
		"Esc to cancel"
	if got := claude().Evaluate("", screen); got != StateBlocked {
		t.Errorf("Evaluate(%q) = %q, want blocked", screen, got)
	}
}

func TestClaudeLegacyReviewPromptBlocked(t *testing.T) {
	screens := []string{
		"Please review your answers\n\n❯ 1. Looks good\n  2. Edit",
		"Skip interview and plan immediately\n\n❯ 1. Yes\n  2. No",
	}
	for _, screen := range screens {
		if got := claude().Evaluate("", screen); got != StateBlocked {
			t.Errorf("Evaluate(%q) = %q, want blocked", screen, got)
		}
	}
}

func TestClaudeTrustFolderPromptBlocked(t *testing.T) {
	// Captured live from Claude Code v2.1.223 startup in a new dir.
	screen := "Accessing workspace:\n\n" +
		" /tmp/agenttest\n\n" +
		" Claude Code'll be able to read, edit, and execute files here.\n\n" +
		" ❯ 1. Yes, I trust this folder\n" +
		"   2. No, exit\n\n" +
		" Enter to confirm · Esc to cancel"
	if got := claude().Evaluate("", screen); got != StateBlocked {
		t.Errorf("Evaluate(trust prompt) = %q, want blocked", got)
	}
}

func TestClaudeIdleWithoutSignals(t *testing.T) {
	// Plain prompt — no spinner, no permission prompt.
	screen := "❯ \n"
	if got := claude().Evaluate("", screen); got != StateIdle {
		t.Errorf("Evaluate(%q) = %q, want idle", screen, got)
	}
}

func TestClaudeBlockedOutranksWorking(t *testing.T) {
	// Both patterns on screen: blocked must win.
	screen := "✻ Thinking… (3s · ↓ 174 tokens)\n\nDo you want to proceed?\n❯ 1. Yes\n  2. No"
	if got := claude().Evaluate("", screen); got != StateBlocked {
		t.Errorf("Evaluate(%q) = %q, want blocked", screen, got)
	}
}

func TestClaudeFinishedIsNotBlocked(t *testing.T) {
	// Regression: after Claude finishes a turn and returns to idle,
	// the screen tail must NOT be misread as blocked. Incidental
	// "yes"/"no" words in Claude's output text, next to a bare ❯
	// prompt or numbered list, should not trigger the blocked rules.
	screens := []string{
		// Claude finished, showing idle prompt with output above.
		"I've completed the refactoring. Here is a summary:\n" +
			"\n" +
			"  1. Extracted the auth module — yes, this includes the middleware\n" +
			"  2. Added tests for edge cases\n" +
			"  3. Updated the config schema\n" +
			"\n" +
			"❯ ",
		// Idle prompt after a turn that mentioned approval in prose.
		"The fix is applied. No further changes are needed.\n" +
			"\n" +
			"❯ ",
		// Output contains "yes" in a code block, idle prompt below.
		"```python\n" +
			"answer = 'yes' if confirmed else 'no'\n" +
			"```\n" +
			"❯ ",
		// Past-tense completion spinner becomes idle.
		"✻ Worked for 1m 52s\n" +
			"\n" +
			"❯ ",
		// Transcript quoting the approval phrase (e.g. Claude viewing
		// this repo's own test file) above a bare idle prompt.
		"    Contains(\"do you want to proceed\"),\n" +
			"    \"Do you want to proceed?\\n❯ 1. Yes\\n  2. No\",\n" +
			"\n" +
			"❯ ",
	}
	for _, screen := range screens {
		if got := claude().Evaluate("", screen); got == StateBlocked {
			t.Errorf("Evaluate(%q) = blocked, want idle or working (false blocked)", screen)
		}
	}
}

func TestIdentifyClaudeBinaries(t *testing.T) {
	for _, name := range []string{"claude", "claude-code", "/usr/local/bin/claude", "CLAUDE.exe"} {
		if spec := Identify(name, nil); spec == nil || spec.Name != "claude" {
			t.Errorf("Identify(%q) failed", name)
		}
	}
}

func TestIdentifyClaudeUnwrapsNodeRuntime(t *testing.T) {
	// node launching claude via global bin symlink.
	spec := Identify("node", []string{"node", "/usr/local/bin/claude"})
	if spec == nil || spec.Name != "claude" {
		t.Fatalf("node-wrapped claude not identified")
	}
	// bun launching claude.
	spec = Identify("bun", []string{"bun", "/home/user/.npm/claude"})
	if spec == nil || spec.Name != "claude" {
		t.Fatalf("bun-wrapped claude not identified")
	}
}

// ---- codex helpers ----

func codex() *AgentSpec {
	spec := codexSpec()
	spec.sortRules()
	return spec
}

func TestCodexTrustDirectoryPromptBlocked(t *testing.T) {
	screen := "> You are in /home/user/project\n\n" +
		"Do you trust the contents of this directory?\n\n" +
		"1. Yes, proceed\n2. No, exit"
	if got := codex().Evaluate("", screen); got != StateBlocked {
		t.Errorf("Evaluate(%q) = %q, want blocked", screen, got)
	}
}

func TestCodexStartupUpdatePromptBlocked(t *testing.T) {
	screen := "Update available! v1.2.3 -> v1.3.0\n\n" +
		"Update now\nSkip until next version\n\n" +
		"Press enter to continue"
	if got := codex().Evaluate("", screen); got != StateBlocked {
		t.Errorf("Evaluate(%q) = %q, want blocked", screen, got)
	}
}

func TestCodexBlockedOnPermissionPrompt(t *testing.T) {
	screens := []string{
		// Primary permission escalation.
		"Would you like to grant these permissions?\n\n" +
			"Reason: codex wants to run: npm install\n\n" +
			"1. Yes, grant these permissions (y)\n" +
			"2. Yes, grant these permissions for this session (a)\n" +
			"3. No, continue without permissions (n)",
		// Short variant.
		"grant these permissions\n1. Yes  2. No",
		// Command approval prompt.
		"Allow command?\n\n" +
			"› 1. Yes (y)\n" +
			"  2. No, provide feedback (n)",
		// Confirm/cancel form.
		"Press Enter to confirm or Esc to cancel",
		// Generic y/n prompt.
		"Overwrite existing file? [y/n]",
	}
	for _, screen := range screens {
		if got := codex().Evaluate("", screen); got != StateBlocked {
			t.Errorf("Evaluate(%q) = %q, want blocked", screen, got)
		}
	}
}

func TestCodexTitleBlocked(t *testing.T) {
	// Codex publishes "Action Required" in the OSC title when it needs
	// human input (herdr: osc_title_blocked) — even if the screen text
	// looks unremarkable.
	if got := codex().Evaluate("Action Required · codex", "some output"); got != StateBlocked {
		t.Errorf("Evaluate(title=Action Required) = %q, want blocked", got)
	}
}

func TestCodexWorkingOnIndicator(t *testing.T) {
	screens := []string{
		// Status line during processing.
		"• Working (12s · Esc to interrupt)",
		"◦ Working (2m 03s · Esc to interrupt) · 1.2k tokens",
	}
	for _, screen := range screens {
		if got := codex().Evaluate("", screen); got != StateWorking {
			t.Errorf("Evaluate(%q) = %q, want working", screen, got)
		}
	}
	// Braille spinner in the OSC title is the primary working signal.
	if got := codex().Evaluate("⠋ codex", "anything"); got != StateWorking {
		t.Errorf("Evaluate(title=⠋ codex) = %q, want working", got)
	}
}

func TestCodexInterruptedIsNotWorking(t *testing.T) {
	// A stale status line after Ctrl+C must not read as working.
	screen := "• Working (12s · Esc to interrupt)\n■ Conversation interrupted"
	if got := codex().Evaluate("", screen); got == StateWorking {
		t.Errorf("Evaluate(interrupted) = working, want not working")
	}
}

func TestCodexTitleIdleOverridesStaleInterruptHint(t *testing.T) {
	// A lingering "Esc to interrupt" in the transcript must not hold
	// working once the title reports a normal (idle) state.
	screen := "old output mentioning Esc to interrupt\n\n› "
	if got := codex().Evaluate("codex — my-project", screen); got != StateIdle {
		t.Errorf("Evaluate(idle title + stale hint) = %q, want idle", got)
	}
}

func TestCodexIdleWithoutSignals(t *testing.T) {
	screen := "Ready\n> \n"
	if got := codex().Evaluate("", screen); got != StateIdle {
		t.Errorf("Evaluate(%q) = %q, want idle", screen, got)
	}
}

func TestCodexBlockedOutranksWorking(t *testing.T) {
	screen := "⠙ Working…\n\nWould you like to grant these permissions?\n1. Yes\n2. No"
	if got := codex().Evaluate("", screen); got != StateBlocked {
		t.Errorf("Evaluate(%q) = %q, want blocked", screen, got)
	}
}

func TestIdentifyCodexBinaries(t *testing.T) {
	for _, name := range []string{"codex", "openai-codex", "/usr/local/bin/codex", "CODEX.exe"} {
		if spec := Identify(name, nil); spec == nil || spec.Name != "codex" {
			t.Errorf("Identify(%q) failed", name)
		}
	}
}

func TestIdentifyCodexUnwrapsNodeRuntime(t *testing.T) {
	// node launching codex.
	spec := Identify("node", []string{"node", "/usr/local/bin/codex"})
	if spec == nil || spec.Name != "codex" {
		t.Fatalf("node-wrapped codex not identified")
	}
}

// ---- registry coverage ----

func TestAllSpecsRegistered(t *testing.T) {
	names := make(map[string]bool)
	for _, spec := range registry {
		if names[spec.Name] {
			t.Errorf("duplicate spec name: %s", spec.Name)
		}
		names[spec.Name] = true
	}
	for _, want := range []string{"copilot", "claude", "codex"} {
		if !names[want] {
			t.Errorf("spec %q not found in registry", want)
		}
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
	if d.NeedsScan(2, now.Add(100*time.Millisecond)) {
		t.Fatal("changed seq within the scan-interval throttle should wait")
	}
	if !d.NeedsScan(2, now.Add(time.Second)) {
		t.Fatal("changed seq must scan after the throttle window")
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

func TestCodexTrustFolderDialogBlocked(t *testing.T) {
	screen := "Folder access\n\nTrust this folder?\n" +
		"Codex can read, edit, and run files here\n\n" +
		"› 1. Trust and continue\n  2. Exit\n\nenter continue"
	if got := codex().Evaluate("", screen); got != StateBlocked {
		t.Errorf("Evaluate(trust folder) = %q, want blocked", got)
	}
}

func TestCodexPluginSearchPopupBlocked(t *testing.T) {
	screen := "› @pl\n\nAll Results   Filesystem Only   Plugins\n"
	if got := codex().Evaluate("", screen); got != StateBlocked {
		t.Errorf("Evaluate(mention popup) = %q, want blocked", got)
	}
}

func TestCodexWorkingStatusLineVariants(t *testing.T) {
	screens := []string{
		// Real codex separator and a dynamic activity label.
		"• Working (12s • esc to interrupt)\n\n› Ask Codex\n\n  gpt-5 · 90% left",
		"• Reading files (1m 05s • esc to interrupt)\n\n› ",
		// Reduced-motion/remapped hint: only the timer suffix remains.
		"Reading files (5s)\n\n› ",
		// Queued inputs may sit below the status.
		"• Working (3s • esc to interrupt)\n  • Queued follow-up inputs\n    ↳ next\n\n› ",
	}
	for _, screen := range screens {
		if got := codex().Evaluate("", screen); got != StateWorking {
			t.Errorf("Evaluate(%q) = %q, want working", screen, got)
		}
	}
}

func TestCodexStaleStatusLineNotWorking(t *testing.T) {
	screens := []string{
		// A response block after the status makes it stale.
		"• Working (12s • esc to interrupt)\n• Done.\n\n› ",
		"• Reconnect failed — check the endpoint, then relaunch (12s)\n\n› ",
	}
	for _, screen := range screens {
		if got := codex().Evaluate("", screen); got == StateWorking {
			t.Errorf("Evaluate(%q) = working, want not working", screen)
		}
	}
}

func TestCodexWeakBlockerSuppressedByLivePrompt(t *testing.T) {
	// Transcript text above a live composer must not read as a prompt.
	screen := "• Overwrite file? [y/n]\n\n› Ask Codex"
	if got := codex().Evaluate("", screen); got == StateBlocked {
		t.Errorf("Evaluate(%q) = blocked, want not blocked", screen)
	}
}
