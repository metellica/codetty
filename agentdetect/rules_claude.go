package agentdetect

// claudeSpec detects Anthropic's Claude Code CLI (the `claude` command).
//
// The primary run-state signal is the OSC window title, which Claude Code
// updates continuously (matching herdr's claude.toml manifest):
//   - working: title starts with a Braille spinner frame ("⠐ Claude Code")
//   - idle:    title starts with "✳ " ("✳ Claude Code")
//
// The title never lingers in the transcript, unlike screen text such as
// the past-tense completion line ("✻ Cooked for 7s") which stays visible
// after a turn finishes and must NOT be treated as working.
//
// Screen-text rules cover blockers (permission/trust prompts) and act as
// a working fallback when no title is available:
//   - blocked: "Do you want to proceed?", "Esc to cancel" + "Enter to
//     confirm/select" forms, numbered ❯ 1. Yes / N. No menus
//   - working: live spinner line with verb + ellipsis ("✻ Thinking…"),
//     or the "esc to interrupt" hint shown only during a running turn
//
// Screen spinner frames vary by platform:
//
//	macOS:  · ✢ ✳ ✶ ✻ ✽
//	Ghostty: · ✢ ✳ ✶ ✻ *
//	Other:  · ✢ * ✶ ✻ ✽
//	Reduced motion: ● (blinks dim/bright)
func claudeSpec() *AgentSpec {
	return &AgentSpec{
		Name:        "claude",
		BinaryNames: []string{"claude", "claude-code"},
		Rules: []Rule{
			{
				ID:       "title_working",
				State:    StateWorking,
				Priority: 1100,
				Region:   RegionTitle,
				// Braille spinner frame prefix, e.g. "⠐ Claude Code".
				Match: Regex(`^[\x{2800}-\x{28FF}] `),
			},
			{
				ID:       "form_blocker",
				State:    StateBlocked,
				Priority: 980,
				// Trust-folder / selection forms: "Enter to confirm ·
				// Esc to cancel" (verified live against the workspace
				// trust prompt).
				Match: All(
					Contains("esc to cancel"),
					Any(
						Contains("enter to confirm"),
						Contains("enter to select"),
					),
				),
			},
			{
				ID:       "permission_blocker",
				State:    StateBlocked,
				Priority: 850,
				Match: All(
					Any(
						// Canonical Claude Code approval screen,
						// corroborated by its prompt chrome (menu,
						// "Esc to cancel · Tab to amend" hints) so
						// the bare phrase quoted in transcript output
						// cannot false-trigger.
						All(
							Contains("do you want to proceed"),
							Any(
								Contains("esc to cancel"),
								Contains("tab to amend"),
								Regex(`(?m)^\s*❯?\s*\d+\. Yes\b`),
							),
						),
						// Numbered menu with BOTH a Yes option AND a No
						// option starting their own lines (optionally
						// behind the ❯ selector). Line-anchored to avoid
						// false positives from incidental "Yes"/"No"
						// words in prose or code output.
						All(
							Regex(`(?m)^\s*❯?\s*\d+\. Yes\b`),
							Regex(`(?m)^\s*❯?\s*\d+\. No\b`),
						),
					),
					// A visible empty ❯ input box means Claude is idle
					// at its prompt; blocker-looking text above it is
					// transcript content, not a live prompt (herdr's
					// legacy_no_prompt_blocker guard). Live permission
					// prompts replace the input box, so no bare ❯
					// line is on screen while one is showing.
					Not(Regex(`(?m)^\s*❯\s*$`)),
				),
			},
			{
				ID:       "title_idle",
				State:    StateIdle,
				Priority: 250,
				Region:   RegionTitle,
				// "✳ " prefix (U+2733) marks the ready/idle title.
				// Outranks the screen working fallback so stale
				// transcript text can never hold the working state
				// once the title reports idle.
				Match: Regex(`^✳ `),
			},
			{
				ID:       "working_spinner",
				State:    StateWorking,
				Priority: 100,
				Match: Any(
					// Live spinner frame + verb + ellipsis (e.g.
					// "✻ Thinking…"). Requires the ellipsis so the
					// past-tense completion line ("✻ Cooked for 7s"),
					// which persists after the turn ends, does not
					// match. · and * are excluded from the frame set
					// because they are too common in ordinary output.
					Regex(`[✻✽✶✳✢●⠧⠇⠏⠋⠙⠹⠸⠼⠴⠦] \w+…`),
					// Interrupt hints shown only while a turn runs.
					Contains("esc to interrupt"),
					Contains("ctrl+c to interrupt"),
				),
			},
		},
	}
}
