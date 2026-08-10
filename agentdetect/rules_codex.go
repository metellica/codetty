package agentdetect

// codexSpec detects OpenAI's Codex CLI (the `codex` command).
//
// The primary run-state signal is the OSC window title (matching herdr's
// codex.toml manifest):
//   - blocked: title contains "Action Required"
//   - working: title contains a Braille spinner frame
//   - idle:    any other non-empty title
//
// Screen-text rules cover approval prompts and act as a working fallback:
//   - blocked: "Allow command?", "press Enter to confirm or Esc to
//     cancel", grant-permissions menus, generic [y/n] prompts
//   - working: the status line "• Working (… esc to interrupt)" rendered
//     near the bottom during model processing
//   - idle:   default fallback when nothing matches
func codexSpec() *AgentSpec {
	return &AgentSpec{
		Name:        "codex",
		BinaryNames: []string{"codex", "openai-codex"},
		Rules: []Rule{
			{
				ID:       "title_blocked",
				State:    StateBlocked,
				Priority: 1100,
				Region:   RegionTitle,
				Match:    Contains("action required"),
			},
			{
				ID:       "title_working",
				State:    StateWorking,
				Priority: 1050,
				Region:   RegionTitle,
				// Braille spinner frame surrounded by spaces/edges.
				Match: Regex(`(?:^| )[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏](?: |$)`),
			},
			{
				ID:       "strong_blocker",
				State:    StateBlocked,
				Priority: 900,
				Match: Any(
					// Tool/command approval prompt.
					Contains("allow command?"),
					Contains("press enter to confirm or esc to cancel"),
					Contains("enter to submit answer"),
					Contains("enter to submit all"),
					// Permission escalation prompt.
					Contains("grant these permissions"),
				),
			},
			{
				ID:       "weak_blocker",
				State:    StateBlocked,
				Priority: 600,
				Match: Any(
					Contains("[y/n]"),
					Contains("yes (y)"),
					All(Contains("do you want to"), Any(Contains("yes"), Contains("❯"))),
					All(Contains("would you like to"), Any(Contains("yes"), Contains("❯"))),
				),
			},
			{
				ID:       "working_status_line",
				State:    StateWorking,
				Priority: 500,
				// Status line rendered during active processing, e.g.
				// "• Working (2m 03s · Esc to interrupt)". The
				// interrupted marker guards against reading a stale
				// status line after Ctrl+C.
				Match: All(
					Regex(`(?m)^\s*[•◦⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]?\s*Working\s*\([^)]*[Ee]sc to interrupt\)`),
					Not(Contains("■ conversation interrupted")),
				),
			},
			{
				ID:       "title_idle",
				State:    StateIdle,
				Priority: 100,
				Region:   RegionTitle,
				// Any non-empty title without spinner/Action Required
				// (those are caught by higher-priority rules) is idle.
				// Note the title may be a stale shell-set one, so no
				// screen working fallback may live below this rule;
				// the precise working_status_line above covers builds
				// regardless of title support.
				Match: Regex(`\S`),
			},
		},
	}
}
