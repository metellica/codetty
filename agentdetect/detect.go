// Package agentdetect detects AI coding agents running inside PTY sessions
// and infers their state (idle/working/blocked) from the OSC window title
// and the visible screen tail.
//
// Detection has two parts:
//   - identification: map the PTY's foreground process to a registered agent
//     (see registry.go and proc_*.go)
//   - state inference: match the OSC title and screen tail against the
//     agent's declarative rules (see rules.go), debounced by a per-tab
//     Detector state machine.
package agentdetect

import "time"

// AgentState is the detected state of an agent in a terminal.
type AgentState string

const (
	// StateUnknown means no recognized agent is running (plain shell etc.).
	StateUnknown AgentState = ""
	// StateIdle means the agent is waiting at its prompt with nothing to do.
	StateIdle AgentState = "idle"
	// StateWorking means the agent is actively processing.
	StateWorking AgentState = "working"
	// StateBlocked means the agent needs human input (approval, selection).
	StateBlocked AgentState = "blocked"
)

const (
	// idleConfirmations is how many consecutive idle scans are required
	// before a Working state is allowed to fall back to Idle. Agents pause
	// briefly between tool calls; without this hold the state flickers.
	// Matches herdr's AGENT_PENDING_IDLE_CONFIRMATIONS.
	idleConfirmations = 3
	// idleHoldCap bounds the Working→Idle hold regardless of poll cadence.
	// Matches herdr's AGENT_PENDING_IDLE_CAP (700ms).
	idleHoldCap = 700 * time.Millisecond
	// scanInterval throttles normal rescans; pending-idle rechecks run at
	// the caller's tick rate instead (herdr: 100ms rechecks).
	scanInterval = 500 * time.Millisecond
	// maxStaleness forces a rescan even without new PTY output, so agent
	// launches/exits that produce no output are still picked up.
	maxStaleness = 4 * time.Second
)

// Detector is a per-terminal state machine that debounces raw rule-engine
// results into stable published states. It is not safe for concurrent use.
type Detector struct {
	agent   string
	state   AgentState
	lastSeq uint64
	hasSeq  bool

	idleConfirms  int
	idleHoldStart time.Time
	lastCommit    time.Time
}

// Agent returns the currently committed agent name ("" if none).
func (d *Detector) Agent() string { return d.agent }

// State returns the currently committed state.
func (d *Detector) State() AgentState { return d.state }

// NeedsScan reports whether the caller should run identification and a
// screen scan this poll. While a Working→Idle hold is pending, every tick
// rescans (fast confirmation); otherwise scans are throttled to
// scanInterval and skipped entirely while the PTY content sequence is
// unchanged, with a staleness bound as fallback.
func (d *Detector) NeedsScan(seq uint64, now time.Time) bool {
	if !d.hasSeq {
		return true
	}
	if d.idleConfirms > 0 {
		return true
	}
	if seq != d.lastSeq {
		return now.Sub(d.lastCommit) >= scanInterval
	}
	return now.Sub(d.lastCommit) >= maxStaleness
}

// Commit consumes a scan result and returns the published state plus
// whether it (or the agent) changed since the last publication.
// Pass agent=="" (with any raw value) when no recognized agent is running.
func (d *Detector) Commit(agent string, raw AgentState, seq uint64, now time.Time) (AgentState, bool) {
	agentChanged := agent != d.agent
	d.lastSeq = seq
	d.hasSeq = true
	d.lastCommit = now

	if agent == "" {
		raw = StateUnknown
	}

	// Hold Working→Idle until the idle reading is confirmed.
	if !agentChanged && d.state == StateWorking && raw == StateIdle {
		if d.idleConfirms == 0 {
			d.idleHoldStart = now
		}
		d.idleConfirms++
		if d.idleConfirms < idleConfirmations && now.Sub(d.idleHoldStart) < idleHoldCap {
			return d.state, false
		}
	}
	d.idleConfirms = 0

	changed := agentChanged || raw != d.state
	d.agent = agent
	d.state = raw
	return raw, changed
}
