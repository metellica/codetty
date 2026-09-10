package agentdetect

// copilotSpec detects the GitHub Copilot CLI. The heuristics mirror the
// hint strings Copilot renders at the bottom of its TUI:
//   - idle/done state shows "/ commands" help bar
//   - approval/selection prompts show both an "esc …" and an "enter …" hint
//   - plain working states show only the "esc …" interrupt hint
func copilotSpec() *AgentSpec {
	return &AgentSpec{
		Name:        "copilot",
		BinaryNames: []string{"copilot", "github-copilot", "ghcs"},
		Rules: []Rule{
			{
				ID:       "selection_blocker",
				State:    StateBlocked,
				Priority: 300,
				Match: All(
					Any(
						Contains("esc to cancel"),
						Contains("esc cancel"),
					),
					Any(
						Contains("enter to select"),
						Contains("enter to confirm"),
						Contains("enter to submit"),
						Contains("enter accept"),
					),
				),
			},
			{
				ID:       "idle_prompt",
				State:    StateIdle,
				Priority: 200,
				Match: Any(
					Contains("/ commands"),
					Contains("? help"),
				),
			},
			{
				ID:       "background_agents_working",
				State:    StateWorking,
				Priority: 210,
				// Copilot renders "<icon> Waiting for background agents"
				// while a background agent task is running (matches
				// herdr's background_agents_working rule, added after
				// #3291/#3403). The icon is cli-spinners' "toggle9"
				// spinner, which alternates between ◉ and ◎ — herdr's
				// manifest only checked ◎, so we match both frames to
				// avoid missing every other poll.
				Match: Regex(`(?m)^\s*[◉◎]\s+Waiting for background agents(?:\s|·|$)`),
			},
			{
				ID:       "working_cancel_hint",
				State:    StateWorking,
				Priority: 100,
				Match: Any(
					Contains("esc to cancel"),
					Contains("esc cancel"),
					Contains("esc again to cancel"),
					Contains("esc interrupt"),
				),
			},
		},
	}
}
