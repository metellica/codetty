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
				// Claude Code >= 2.1.228 switched its busy spinner to
				// half-circle glyphs (U+25D0-25D3); match both ranges
				// (matches herdr's osc_title_working).
				Match: Regex(`^[\x{2800}-\x{28FF}\x{25D0}-\x{25D3}] `),
			},
			{
				ID:       "background_agents_working",
				State:    StateWorking,
				Priority: 900,
				// "* Waiting for N background agents to finish" (matches
				// herdr's background_agents_working rule). Uses the same
				// activity-marker glyph set as working_spinner below.
				Match: Regex(`(?m)^\s*[*·✢✶✻✽]\s+Waiting for [1-9]\d* background agents? to finish\s*$`),
			},
			{
				ID:       "background_mcp_task_working",
				State:    StateWorking,
				Priority: 895,
				// "<marker> ... · N MCP tasks still running" activity
				// summary (matches herdr's background_mcp_task_working).
				// Guarded against overlapping live blocker text so a
				// stale summary line above an active prompt can't mask
				// it as merely "working".
				Match: All(
					Regex(`(?m)^[*·✢✶✻✽][ \t]+\S.*·\s*[1-9]\d*\s+MCP\s+tasks?\s+still\s+running\s*$`),
					Not(Contains("do you want to proceed?")),
					Not(Contains("esc to cancel")),
					Not(Contains("waiting for permission")),
					Not(Contains("do you want to allow this connection?")),
					Not(Contains("tab to amend")),
					Not(Contains("ctrl+e to explain")),
				),
			},
			{
				ID:       "btw_overlay_working",
				State:    StateWorking,
				Priority: 890,
				// The "/btw" side-conversation overlay shows its own
				// "Esc to close" footer instead of the usual working
				// hints (matches herdr's btw_overlay_working).
				Match: All(
					Regex(`(?m)^\s*/btw(?:\s|$)`),
					Contains("esc to close"),
				),
			},
			{
				ID:       "dynamic_workflow_prompt",
				State:    StateBlocked,
				Priority: 980,
				// "Run a dynamic workflow?" confirmation (matches
				// herdr's dynamic_workflow_prompt).
				Match: Contains("run a dynamic workflow?", "esc to cancel"),
			},
			{
				ID:       "mcp_elicitation_prompt",
				State:    StateBlocked,
				Priority: 980,
				// MCP elicitation dialogs (elicitation/create) show
				// Accept/Decline controls with an "Esc to cancel"
				// footer but no Enter hint, so form_blocker can't see
				// them (matches herdr's mcp_elicitation_prompt).
				Match: All(
					Contains("esc to cancel"),
					Regex(`(?im)^\s*MCP server ["“].+["”] requests your input\s*$`),
					Any(
						Regex(`(?m)^\s*❯?\s*Accept\b`),
						Regex(`(?m)^\s*❯?\s*Decline\b`),
					),
				),
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
						// Plan-mode review/interview prompts (matches
						// herdr's legacy_no_prompt_blocker phrases).
						Contains("review your answers"),
						Contains("skip interview and plan immediately"),
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
