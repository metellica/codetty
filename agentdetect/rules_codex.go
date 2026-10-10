package agentdetect

import "strings"

// codexSpec detects OpenAI's Codex CLI (the `codex` command).
//
// The primary run-state signal is the OSC window title (matching herdr's
// codex.toml manifest):
//   - blocked: title contains "Action Required"
//   - working: title contains a Braille spinner frame
//   - idle:    any other non-empty title
//
// Screen-text rules cover approval prompts and act as a working fallback:
//   - blocked: workspace trust prompt (both the legacy "Do you trust the
//     contents" and the "Trust this folder?" dialogs), "Update available!"
//     startup dialog, "Allow command?", "press Enter to confirm or Esc to
//     cancel", plugin/mention search popups, grant-permissions menus,
//     generic [y/n] prompts
//   - working: the live status line above the composer, e.g.
//     "• Working (12s • esc to interrupt)" or a dynamic activity label such
//     as "Reading files (5s)"; the interrupt hint may be remapped or
//     hidden, so only the elapsed-timer suffix is required
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
				ID:       "trust_directory",
				State:    StateBlocked,
				Priority: 950,
				// Startup workspace-trust prompt (matches herdr's
				// trust_directory rule): the legacy question, or the
				// newer "Trust this folder?" dialog under a
				// "Folder access" header.
				Match: Any(
					Regex(`(?is)do\s+you\s+trust\s+the\s+contents\s+of\s+this\s+directory\?`),
					All(
						Any(
							Regex(`(?m)^> You are in [^\r\n]+$`),
							Contains("folder access"),
						),
						Contains("trust this folder?", "codex can read, edit, and run files here"),
						Any(
							Contains("trust and continue"),
							Contains("enter continue"),
						),
					),
				),
			},
			{
				ID:       "startup_update",
				State:    StateBlocked,
				Priority: 950,
				// "Update available!" startup dialog blocks until the
				// user skips or updates (matches herdr's startup_update
				// rule; regression from #3301/#3632/#3517 where quoted
				// confirmation text upstream could mask this as idle).
				Match: All(
					Contains("update available!", "update now"),
					Any(
						Regex(`(?i)skip\s+until\s+next\s+version`),
						Regex(`(?i)press enter to continue\s*$`),
					),
				),
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
					// Plugin/mention search popup.
					Contains("all results", "filesystem only", "plugins"),
				),
			},
			{
				ID:       "weak_blocker",
				State:    StateBlocked,
				Priority: 600,
				// Suppressed while a live composer prompt is on screen
				// (herdr: whole_recent_without_current_prompt_marker),
				// so transcript text above it can't read as a prompt.
				Match: withoutCurrentPrompt(All(
					// Sparkles can replace the space after ›; a later
					// response marker makes that prompt stale.
					Not(Regex(`(?m)^›[⠁⠂⠄⠈⠐⠠⡀⢀][^\n]*(?:\n(?:[^•■✗✓\n][^\n]*)?)*\z`)),
					Any(
						Contains("[y/n]"),
						Contains("yes (y)"),
						All(Contains("do you want to"), Any(Contains("yes"), Contains("❯"))),
						All(Contains("would you like to"), Any(Contains("yes"), Contains("❯"))),
					),
				)),
			},
			{
				ID:       "working_status_line",
				State:    StateWorking,
				Priority: 500,
				// Live status line above the composer, e.g.
				// "• Working (2m 03s • esc to interrupt)" or a dynamic
				// label such as "Reading files (5s)". The interrupt
				// hint can be remapped or hidden and queued inputs can
				// sit below the status, so require the elapsed-timer
				// suffix with no later response/transcript block
				// (matches herdr's screen_working_fallback; "·" is
				// accepted alongside "•" as the hint separator). A
				// failed reconnect keeps its final timer but is not
				// working.
				Match: beforeCurrentPrompt(All(
					Regex(`(?m)^(?:[•◦][ \t]+)?[^\s›•◦■✗✓─][^\r\n]* \((?:[0-9]+[hm] )*[0-9]+s(?: [•·] [^\r\n]+? to interrupt)?\)(?: · [^\r\n]*)?(?:\r?\n(?:[^•◦›■✗✓─\r\n][^\r\n]*|•[ \t]+(?:Queued\s+follow-up\s+inputs|Messages\s+to\s+be\s+submitted\s+after\s+next\s+tool\s+call(?:\s+\(press\s+[^\r\n]+?\s+to\s+interrupt\s+and\s+send\s+immediately\))?|Messages\s+to\s+be\s+submitted\s+at\s+end\s+of\s+turn)|›[⠁⠂⠄⠈⠐⠠⡀⢀][^\r\n]*)?)*\s*\z`),
					Not(Regex(`(?m)^(?:[•◦][ \t]+)?Reconnect failed — check the endpoint, then relaunch \([0-9hms ]+\)$`)),
				)),
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

// codexPromptIndex returns the index of the live composer prompt line ("›"
// or "› …") in lines, or -1 when none is current. A prompt followed by a
// transcript block marker (•/■/✗/✓) is stale (herdr's
// current_codex_prompt_index).
func codexPromptIndex(lines []string) int {
	idx := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i] == "›" || strings.HasPrefix(lines[i], "› ") {
			idx = i
			break
		}
	}
	if idx < 0 {
		return -1
	}
	for _, l := range lines[idx+1:] {
		if strings.HasPrefix(l, "•") || strings.HasPrefix(l, "■") ||
			strings.HasPrefix(l, "✗") || strings.HasPrefix(l, "✓") {
			return -1
		}
	}
	return idx
}

// beforeCurrentPrompt runs m on the screen text above the live composer
// prompt (the whole text when no prompt is current).
func beforeCurrentPrompt(m Matcher) Matcher {
	return func(text, lower string) bool {
		lines := strings.Split(text, "\n")
		idx := codexPromptIndex(lines)
		if idx < 0 {
			return m(text, lower)
		}
		before := strings.Join(lines[:idx], "\n")
		if idx > 0 {
			before += "\n"
		}
		return m(before, strings.ToLower(before))
	}
}

// withoutCurrentPrompt runs m on the screen text only when no live composer
// prompt is current; otherwise it never matches.
func withoutCurrentPrompt(m Matcher) Matcher {
	return func(text, lower string) bool {
		if codexPromptIndex(strings.Split(text, "\n")) >= 0 {
			return false
		}
		return m(text, lower)
	}
}
