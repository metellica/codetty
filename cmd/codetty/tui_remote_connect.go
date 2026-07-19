package main

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Remote daemon connections are established in the background: the model
// marks the endpoint as "connecting" (shown in the status bar) and stays
// responsive while the deploy check (Ensure) and the subscribe handshake
// run in a goroutine. Every path that attaches a remote daemon — config
// startup, config reload, the prefix+t target picker and the recorded
// remote registry — funnels through these messages.

// remoteConnectingMsg marks a remote endpoint as connecting in the UI.
type remoteConnectingMsg struct {
	id    string
	label string
}

// remoteConnectDoneMsg reports the outcome of a background remote
// connection attempt; a non-nil err becomes a status-bar notice.
type remoteConnectDoneMsg struct {
	id    string
	label string
	err   error
}

// remoteContainerSpec is one container to reconcile on a remote once its
// daemon connection is up.
type remoteContainerSpec struct {
	display string
	cp      containerPlan
}

// remoteConnectJob connects one remote endpoint and creates the config
// containers (with their tabs) that should live there. Containers of every
// session using the endpoint share the job, so one handshake serves all.
type remoteConnectJob struct {
	id         string
	label      string
	endpoint   daemonEndpoint
	containers []remoteContainerSpec
}

// endpointLabel names an endpoint for UI messages.
func endpointLabel(e daemonEndpoint) string {
	if se, ok := e.(*sshDaemonEndpoint); ok {
		return se.name
	}
	return e.ID()
}

// connectingRemoteState reference-counts concurrent background connection
// flows for one endpoint, keeping the status-bar indicator up until the
// last flow's done message arrives.
type connectingRemoteState struct {
	label string
	refs  int
}

// markRemoteConnecting records a pending background connection so the
// status bar shows it until the matching remoteConnectDoneMsg arrives.
// Each mark must be paired with exactly one done message.
func (m *tuiModel) markRemoteConnecting(id, label string) {
	if m.connectingRemotes == nil {
		m.connectingRemotes = make(map[string]*connectingRemoteState)
	}
	if st := m.connectingRemotes[id]; st != nil {
		st.refs++
		return
	}
	m.connectingRemotes[id] = &connectingRemoteState{label: label, refs: 1}
}

// connectingLabel summarizes pending remote connections for the status
// bar ("" when none), with sorted labels for a stable display.
func (m *tuiModel) connectingLabel() string {
	if len(m.connectingRemotes) == 0 {
		return ""
	}
	labels := make([]string, 0, len(m.connectingRemotes))
	for _, st := range m.connectingRemotes {
		labels = append(labels, st.label)
	}
	sort.Strings(labels)
	return strings.Join(labels, ", ")
}

// connectRemoteJobCmd runs a remote connection job off the Update
// goroutine: Ensure (deploy/reachability), AddPeerSynced (dial + subscribe
// handshake, consuming the initial snapshots), then container creation.
// Containers that already exist on the remote are adopted as-is — their
// state streams in via the subscription. Only touches daemonConn, which is
// safe from any goroutine.
func connectRemoteJobCmd(conn *daemonConn, job remoteConnectJob, cols, rows uint16) tea.Cmd {
	return func() tea.Msg {
		if !conn.HasPeer(job.id) {
			if err := job.endpoint.Ensure(); err != nil {
				return remoteConnectDoneMsg{id: job.id, label: job.label, err: err}
			}
		}
		if err := conn.AddPeerSynced(job.id, job.endpoint); err != nil {
			return remoteConnectDoneMsg{id: job.id, label: job.label, err: err}
		}
		// The endpoint may have turned out to be an alias of an already
		// connected daemon; address its parts by the canonical peer ID.
		peerID := conn.resolvePeerID(job.id)
		for _, spec := range job.containers {
			if _, exists := conn.ContainerTabs(spec.display, peerID); exists {
				continue
			}
			for _, tab := range spec.cp.Tabs {
				conn.CreateTabOn(
					spec.display, peerID, tab.Name,
					tab.Command, tab.Dir, envMapToSlice(tab.Env),
					cols, rows,
				)
			}
		}
		return remoteConnectDoneMsg{id: job.id, label: job.label}
	}
}

// startupRemoteJobCmds launches the deferred remote jobs from the startup
// config (codetty up with remote tabs) once the local subscription is up.
// Jobs run even when the peer is already connecting or connected (e.g. via
// the recorded-remote registry): AddPeerSynced is idempotent and waits for
// the sync, and containers existing there are adopted rather than re-made.
func (m *tuiModel) startupRemoteJobCmds() tea.Cmd {
	if len(m.startupRemoteJobs) == 0 || m.conn == nil {
		return nil
	}
	jobs := m.startupRemoteJobs
	m.startupRemoteJobs = nil
	cols, rows := m.focusedContentSize()
	var cmds []tea.Cmd
	for _, job := range jobs {
		m.markRemoteConnecting(job.id, job.label)
		cmds = append(cmds, connectRemoteJobCmd(m.conn, job, uint16(cols), uint16(rows)))
	}
	return tea.Batch(cmds...)
}
