package agentdetect

import (
	"regexp"
	"sort"
	"strings"
)

// Matcher tests lowercased screen-tail text.
type Matcher func(text string) bool

// Contains matches when every substring is present (substrings must be
// lowercase; input text is lowercased before evaluation).
func Contains(subs ...string) Matcher {
	return func(text string) bool {
		for _, s := range subs {
			if !strings.Contains(text, s) {
				return false
			}
		}
		return true
	}
}

// Any matches when at least one sub-matcher matches.
func Any(ms ...Matcher) Matcher {
	return func(text string) bool {
		for _, m := range ms {
			if m(text) {
				return true
			}
		}
		return false
	}
}

// All matches when every sub-matcher matches.
func All(ms ...Matcher) Matcher {
	return func(text string) bool {
		for _, m := range ms {
			if !m(text) {
				return false
			}
		}
		return true
	}
}

// Not inverts a matcher.
func Not(m Matcher) Matcher {
	return func(text string) bool { return !m(text) }
}

// Regex matches against a pattern compiled at spec-registration time.
func Regex(pattern string) Matcher {
	re := regexp.MustCompile(pattern)
	return func(text string) bool { return re.MatchString(text) }
}

// Rule maps a screen-tail pattern to an agent state. Higher priority rules
// win; the first matching rule (in descending priority order) decides.
type Rule struct {
	ID       string
	State    AgentState
	Priority int
	Match    Matcher
}

// AgentSpec declares how to identify one agent and infer its state.
// Adding support for a new agent means registering one of these.
type AgentSpec struct {
	// Name is the canonical agent name published in states/events.
	Name string
	// BinaryNames are process names that identify this agent.
	BinaryNames []string
	// Rules are evaluated by descending priority; first match wins.
	// When no rule matches, the state defaults to StateIdle.
	Rules []Rule
}

// sortRules orders rules by descending priority (stable for equal values).
func (spec *AgentSpec) sortRules() {
	sort.SliceStable(spec.Rules, func(i, j int) bool {
		return spec.Rules[i].Priority > spec.Rules[j].Priority
	})
}

// Evaluate returns the raw state for the given screen tail text.
func (spec *AgentSpec) Evaluate(tail string) AgentState {
	text := strings.ToLower(tail)
	for _, r := range spec.Rules {
		if r.Match(text) {
			return r.State
		}
	}
	return StateIdle
}
