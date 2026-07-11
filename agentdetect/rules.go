package agentdetect

import (
	"regexp"
	"sort"
	"strings"
)

// Matcher tests screen-tail text. It receives both the original text and
// a pre-lowercased copy: substring matchers use the lowercased text while
// regex matchers run against the original (matching herdr's manifest
// engine semantics).
type Matcher func(text, lower string) bool

// Contains matches when every substring is present (substrings must be
// lowercase; matching is done against the lowercased text).
func Contains(subs ...string) Matcher {
	return func(_, lower string) bool {
		for _, s := range subs {
			if !strings.Contains(lower, s) {
				return false
			}
		}
		return true
	}
}

// Any matches when at least one sub-matcher matches.
func Any(ms ...Matcher) Matcher {
	return func(text, lower string) bool {
		for _, m := range ms {
			if m(text, lower) {
				return true
			}
		}
		return false
	}
}

// All matches when every sub-matcher matches.
func All(ms ...Matcher) Matcher {
	return func(text, lower string) bool {
		for _, m := range ms {
			if !m(text, lower) {
				return false
			}
		}
		return true
	}
}

// Not inverts a matcher.
func Not(m Matcher) Matcher {
	return func(text, lower string) bool { return !m(text, lower) }
}

// Regex matches the original (non-lowercased) text against a pattern
// compiled at spec-registration time.
func Regex(pattern string) Matcher {
	re := regexp.MustCompile(pattern)
	return func(text, _ string) bool { return re.MatchString(text) }
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
	lower := strings.ToLower(tail)
	for _, r := range spec.Rules {
		if r.Match(tail, lower) {
			return r.State
		}
	}
	return StateIdle
}
