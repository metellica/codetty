package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"codetty/daemon"

	tea "github.com/charmbracelet/bubbletea"
)

// localPeerID is the peer ID of the local daemon in multi-peer mode.
const localPeerID = "local"

// daemonConn manages the connection between TUI and daemon.
type daemonConn struct {
	conn       io.ReadWriteCloser
	sockPath   string
	program    *tea.Program
	subscribed bool
	endpoint   daemonEndpoint

	// Multi-peer mode: the local daemon plus remote daemons hosting
	// shadow container sessions, merged into composite sessions.
	peers   map[string]*daemonPeer
	peersMu sync.RWMutex

	// localID identifies this TUI host inside shadow session names.
	localID string

	// Composite session state (guarded by compMu).
	compMu     sync.Mutex
	composites map[string]*compositeSession // display name -> composite
	partOwner  map[string]*compositeSession // partKey(peer, name) -> composite
	queuedTabs map[string][]queuedTab       // partKey -> tabs awaiting container

	pendingSubs int
	subMu       sync.Mutex
	closeOnce   sync.Once
	closed      chan struct{}

	// Output coalescing: buffer per-session output during burst,
	// flush at most every 16ms to reduce View() calls.
	outMu      sync.Mutex
	outBuf     map[string][]byte
	outPending bool // true while a flush timer is running
}

type daemonPeer struct {
	id         string
	endpoint   daemonEndpoint
	conn       io.ReadWriteCloser
	writeMu    sync.Mutex
	stateMu    sync.Mutex
	known      map[string]bool
	seen       map[string]bool
	reconcile  bool
	initialAck bool

	// Negotiated protocol state from the subscribe ACK (stateMu). A peer
	// that rejected the handshake is marked rejected and never reconnected.
	proto    int
	caps     map[string]bool
	rejected bool
}

// hasCap reports whether the peer advertised a capability; future features
// must check this before sending frames the peer may not understand.
func (peer *daemonPeer) hasCap(name string) bool {
	peer.stateMu.Lock()
	defer peer.stateMu.Unlock()
	return peer.caps[name]
}

// track records session lifecycle transitions for reconnect reconciliation.
func (peer *daemonPeer) track(evtType, session, newName string) {
	peer.stateMu.Lock()
	defer peer.stateMu.Unlock()
	switch evtType {
	case "snapshot", "created":
		peer.known[session] = true
		if peer.reconcile {
			peer.seen[session] = true
		}
	case "renamed":
		delete(peer.known, session)
		if newName != "" {
			peer.known[newName] = true
			if peer.reconcile {
				peer.seen[newName] = true
			}
		}
	case "exited":
		delete(peer.known, session)
	}
}

// Tea messages from daemon connection
type daemonOutputMsg struct {
	Session string
	Data    []byte
}

type daemonSessionEventMsg struct {
	Event daemon.SessionEvent
}

type daemonDisconnectedMsg struct {
	Err error
}

// daemonReconnectedMsg carries a fresh connection after the daemon dropped
// us (e.g. during a hot upgrade) and we successfully dialed again.
type daemonReconnectedMsg struct {
	conn *daemonConn
}

// daemonReconnectFailedMsg signals that reconnection attempts were exhausted.
type daemonReconnectFailedMsg struct{}

type daemonSubscribedMsg struct{}

func newDaemonConn(sockPath string) (*daemonConn, error) {
	endpoint := localDaemonEndpoint{sockPath: sockPath}
	conn, err := endpoint.Dial()
	if err != nil {
		return nil, err
	}
	return &daemonConn{conn: conn, sockPath: sockPath, endpoint: endpoint}, nil
}

func newMultiDaemonConn(endpoints map[string]daemonEndpoint) (*daemonConn, error) {
	ids := make([]string, 0, len(endpoints))
	for id := range endpoints {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	dc := &daemonConn{
		peers:      make(map[string]*daemonPeer, len(endpoints)),
		localID:    localShadowID(),
		composites: make(map[string]*compositeSession),
		partOwner:  make(map[string]*compositeSession),
		queuedTabs: make(map[string][]queuedTab),
		closed:     make(chan struct{}),
	}

	for _, id := range ids {
		endpoint := endpoints[id]
		conn, err := endpoint.Dial()
		if err != nil {
			dc.closePeers()
			return nil, fmt.Errorf("connect %s: %w", id, err)
		}
		dc.peers[id] = &daemonPeer{
			id:       id,
			endpoint: endpoint,
			conn:     conn,
			known:    make(map[string]bool),
		}
	}
	return dc, nil
}

// multi reports whether this connection runs in multi-peer (composite) mode.
func (dc *daemonConn) multi() bool {
	return dc.peers != nil
}

func (dc *daemonConn) peer(id string) *daemonPeer {
	dc.peersMu.RLock()
	defer dc.peersMu.RUnlock()
	return dc.peers[id]
}

func (dc *daemonConn) peerList() []*daemonPeer {
	dc.peersMu.RLock()
	defer dc.peersMu.RUnlock()
	list := make([]*daemonPeer, 0, len(dc.peers))
	for _, peer := range dc.peers {
		list = append(list, peer)
	}
	return list
}

// HasPeer reports whether the peer is already connected.
func (dc *daemonConn) HasPeer(id string) bool {
	return dc.peer(id) != nil
}

// AddPeer connects to a new daemon endpoint at runtime and subscribes to
// it, so shadow sessions on it merge into the composite view.
func (dc *daemonConn) AddPeer(id string, endpoint daemonEndpoint) error {
	if !dc.multi() {
		return fmt.Errorf("not in multi-daemon mode")
	}
	if dc.peer(id) != nil {
		return nil
	}
	conn, err := endpoint.Dial()
	if err != nil {
		return err
	}
	peer := &daemonPeer{
		id:       id,
		endpoint: endpoint,
		conn:     conn,
		known:    make(map[string]bool),
	}
	dc.peersMu.Lock()
	if _, ok := dc.peers[id]; ok {
		dc.peersMu.Unlock()
		_ = conn.Close()
		return nil
	}
	dc.peers[id] = peer
	dc.peersMu.Unlock()

	if err := dc.subscribePeer(peer); err != nil {
		dc.peersMu.Lock()
		delete(dc.peers, id)
		dc.peersMu.Unlock()
		_ = conn.Close()
		return err
	}
	go dc.readPeerLoop(peer)
	return nil
}

// reconnectCmd retries dialing the daemon socket for a few seconds. The
// daemon closes subscriber connections during a hot upgrade and rebinds
// the socket moments later, so a short retry window bridges the gap.
func reconnectCmd(sockPath string) tea.Cmd {
	return func() tea.Msg {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if dc, err := newDaemonConn(sockPath); err == nil {
				return daemonReconnectedMsg{conn: dc}
			}
			time.Sleep(200 * time.Millisecond)
		}
		return daemonReconnectFailedMsg{}
	}
}

// helloRequest returns the subscribe_all request carrying our protocol hello.
func helloRequest() daemon.Request {
	return daemon.Request{
		Action:   daemon.ActionSubscribeAll,
		Proto:    daemon.ProtocolVersion,
		ProtoMin: daemon.MinProtocolVersion,
		Caps:     daemon.ProtocolCaps(),
	}
}

// Subscribe sends subscribe_all and starts the read loop.
func (dc *daemonConn) Subscribe(p *tea.Program) error {
	dc.program = p

	if dc.multi() {
		peers := dc.peerList()
		dc.subMu.Lock()
		dc.pendingSubs = len(peers)
		dc.subMu.Unlock()
		for _, peer := range peers {
			if err := dc.subscribePeer(peer); err != nil {
				return err
			}
			go dc.readPeerLoop(peer)
		}
		return nil
	}

	if err := daemon.WriteFrame(dc.conn, daemon.FrameRequest, daemon.MarshalJSON(helloRequest())); err != nil {
		return fmt.Errorf("send subscribe_all request: %w", err)
	}

	go dc.readLoop()
	return nil
}

func (dc *daemonConn) subscribePeer(peer *daemonPeer) error {
	peer.writeMu.Lock()
	defer peer.writeMu.Unlock()
	if err := daemon.WriteFrame(peer.conn, daemon.FrameRequest, daemon.MarshalJSON(helloRequest())); err != nil {
		return fmt.Errorf("subscribe %s: %w", peer.id, err)
	}
	return nil
}

func (dc *daemonConn) readPeerLoop(peer *daemonPeer) {
	for {
		_ = dc.readPeer(peer)
		peer.stateMu.Lock()
		rejected := peer.rejected
		peer.stateMu.Unlock()
		if rejected {
			_ = peer.conn.Close()
			return
		}
		select {
		case <-dc.closed:
			return
		default:
		}
		if err := dc.reconnectPeer(peer); err != nil {
			return
		}
	}
}

func (dc *daemonConn) readPeer(peer *daemonPeer) error {
	for {
		ft, payload, err := daemon.ReadFrame(peer.conn)
		if err != nil {
			return err
		}
		dc.handlePeerFrame(peer, ft, payload)
	}
}

func (dc *daemonConn) readPeerUntilSubscribed(peer *daemonPeer) error {
	for {
		ft, payload, err := daemon.ReadFrame(peer.conn)
		if err != nil {
			return err
		}
		dc.handlePeerFrame(peer, ft, payload)
		if ft == daemon.FrameResponse {
			return nil
		}
	}
}

func (dc *daemonConn) handlePeerFrame(peer *daemonPeer, ft daemon.FrameType, payload []byte) {
	switch ft {
	case daemon.FrameTaggedOutput:
		session, data, err := daemon.DecodeTaggedOutput(payload)
		if err != nil {
			return
		}
		if display, ok := dc.mapOutput(peer.id, session); ok {
			dc.bufferOutput(display, data)
		}
	case daemon.FrameSessionEvent:
		var evt daemon.SessionEvent
		if json.Unmarshal(payload, &evt) != nil {
			return
		}
		out, post := dc.mergeEvent(peer, evt)
		for _, e := range out {
			dc.program.Send(daemonSessionEventMsg{Event: e})
		}
		for _, fn := range post {
			fn()
		}
	case daemon.FrameResponse:
		var resp daemon.Response
		respOK := json.Unmarshal(payload, &resp) == nil
		if respOK && !resp.OK && resp.Error != "" {
			// Handshake rejected (e.g. incompatible protocol): give up on
			// this peer permanently — reconnecting would be rejected again.
			peer.stateMu.Lock()
			peer.rejected = true
			peer.initialAck = true
			peer.stateMu.Unlock()
			dc.ackPending(peer)
			if dc.program != nil {
				dc.program.Send(tuiNoticeMsg{text: fmt.Sprintf("%s: %s", peer.id, resp.Error)})
			}
			return
		}
		for _, session := range dc.finishPeerReconcile(peer) {
			out, post := dc.removeStalePart(peer.id, session)
			for _, e := range out {
				dc.program.Send(daemonSessionEventMsg{Event: e})
			}
			for _, fn := range post {
				fn()
			}
		}
		peer.stateMu.Lock()
		firstAck := !peer.initialAck
		peer.initialAck = true
		if respOK {
			// Same computation as the daemon side: highest common
			// version, capability intersection (legacy ACKs without a
			// hello negotiate to version 1 with all current caps).
			peer.proto, _ = daemon.NegotiateProto(resp.Proto, resp.ProtoMin)
			peer.caps = daemon.IntersectCaps(resp.Caps)
		}
		peer.stateMu.Unlock()
		if firstAck {
			dc.ackPending(peer)
		}
	}
}

// ackPending counts a peer's initial subscribe answer towards the "all
// peers subscribed" notification the TUI waits for at startup.
func (dc *daemonConn) ackPending(peer *daemonPeer) {
	dc.subMu.Lock()
	if dc.pendingSubs > 0 {
		dc.pendingSubs--
		if dc.pendingSubs == 0 {
			dc.program.Send(daemonSubscribedMsg{})
		}
	}
	dc.subMu.Unlock()
}

// removeStalePart drops a part whose session disappeared while the peer
// connection was down, and reports the composite fallout.
func (dc *daemonConn) removeStalePart(peerID, session string) (out []daemon.SessionEvent, post []func()) {
	dc.compMu.Lock()
	defer dc.compMu.Unlock()
	key := partKey(peerID, session)
	c := dc.partOwner[key]
	if c == nil {
		return nil, nil
	}
	delete(dc.partOwner, key)
	delete(dc.queuedTabs, key)
	p := c.part(peerID)
	if p == nil {
		return nil, nil
	}
	return dc.removePartLocked(c, p, 0)
}

func (dc *daemonConn) finishPeerReconcile(peer *daemonPeer) []string {
	peer.stateMu.Lock()
	defer peer.stateMu.Unlock()
	if !peer.reconcile {
		return nil
	}
	var stale []string
	for session := range peer.known {
		if !peer.seen[session] {
			stale = append(stale, session)
			delete(peer.known, session)
		}
	}
	peer.reconcile = false
	peer.seen = nil
	sort.Strings(stale)
	return stale
}

func (dc *daemonConn) reconnectPeer(peer *daemonPeer) error {
	delay := 200 * time.Millisecond
	for {
		select {
		case <-dc.closed:
			return io.EOF
		case <-time.After(delay):
		}

		conn, err := peer.endpoint.Dial()
		if err != nil {
			if delay < 10*time.Second {
				delay *= 2
			}
			continue
		}
		peer.stateMu.Lock()
		peer.reconcile = true
		peer.seen = make(map[string]bool)
		peer.stateMu.Unlock()

		peer.writeMu.Lock()
		old := peer.conn
		if err := daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(helloRequest())); err != nil {
			peer.writeMu.Unlock()
			_ = conn.Close()
			if delay < 10*time.Second {
				delay *= 2
			}
			continue
		}
		peer.conn = conn
		peer.writeMu.Unlock()
		_ = old.Close()
		if err := dc.readPeerUntilSubscribed(peer); err != nil {
			_ = conn.Close()
			if delay < 10*time.Second {
				delay *= 2
			}
			continue
		}
		return nil
	}
}

func (dc *daemonConn) readLoop() {
	for {
		ft, payload, err := daemon.ReadFrame(dc.conn)
		if err != nil {
			dc.program.Send(daemonDisconnectedMsg{Err: err})
			return
		}

		switch ft {
		case daemon.FrameTaggedOutput:
			session, data, err := daemon.DecodeTaggedOutput(payload)
			if err != nil {
				continue
			}
			dc.bufferOutput(session, data)

		case daemon.FrameSessionEvent:
			var evt daemon.SessionEvent
			if json.Unmarshal(payload, &evt) == nil {
				dc.program.Send(daemonSessionEventMsg{Event: evt})
			}

		case daemon.FrameResponse:
			var resp daemon.Response
			if json.Unmarshal(payload, &resp) == nil && !resp.OK && resp.Error != "" {
				dc.program.Send(daemonDisconnectedMsg{Err: fmt.Errorf("%s", resp.Error)})
				return
			}
			if !dc.subscribed {
				dc.subscribed = true
				dc.program.Send(daemonSubscribedMsg{})
			}
		}
	}
}

// bufferOutput accumulates output for coalesced delivery.
// First call in a window sends immediately (zero latency for interactive use),
// then starts a 16ms timer. Subsequent calls within the window buffer data
// until the timer fires and flushes.
func (dc *daemonConn) bufferOutput(session string, data []byte) {
	dc.outMu.Lock()
	if dc.outBuf == nil {
		dc.outBuf = make(map[string][]byte)
	}
	dc.outBuf[session] = append(dc.outBuf[session], data...)

	if !dc.outPending {
		// Leading edge: flush immediately, then start coalescing window
		dc.outPending = true
		buf := dc.outBuf
		dc.outBuf = make(map[string][]byte)
		dc.outMu.Unlock()

		for s, d := range buf {
			dc.program.Send(daemonOutputMsg{Session: s, Data: d})
		}
		time.AfterFunc(16*time.Millisecond, dc.flushOutput)
		return
	}
	dc.outMu.Unlock()
}

// flushOutput sends all buffered output and resets the coalescing window.
func (dc *daemonConn) flushOutput() {
	dc.outMu.Lock()
	buf := dc.outBuf
	dc.outBuf = make(map[string][]byte)
	dc.outPending = false
	dc.outMu.Unlock()

	for s, d := range buf {
		dc.program.Send(daemonOutputMsg{Session: s, Data: d})
	}
}

// partRef identifies one daemon-side container of a composite session.
type partRef struct {
	peerID string
	name   string
}

// activePart resolves the display session's active part (falling back to a
// same-named local session when the composite is unknown yet).
func (dc *daemonConn) activePart(display string) partRef {
	dc.compMu.Lock()
	defer dc.compMu.Unlock()
	if c := dc.composites[display]; c != nil && c.active != nil {
		return partRef{c.active.peerID, c.active.name}
	}
	return partRef{localPeerID, display}
}

// partsOf lists all containers of a display session (falling back to a
// same-named local session when the composite is unknown yet).
func (dc *daemonConn) partsOf(display string) []partRef {
	dc.compMu.Lock()
	defer dc.compMu.Unlock()
	c := dc.composites[display]
	if c == nil || len(c.parts) == 0 {
		return []partRef{{localPeerID, display}}
	}
	refs := make([]partRef, 0, len(c.parts))
	for _, p := range c.parts {
		refs = append(refs, partRef{p.peerID, p.name})
	}
	return refs
}

func (dc *daemonConn) writeToPeer(peerID string, ft daemon.FrameType, payload []byte) {
	dc.writePeerFrame(dc.peer(peerID), ft, payload)
}

// SendInput sends tagged input to a session (its active part in multi mode).
func (dc *daemonConn) SendInput(session string, data []byte) {
	if !dc.multi() {
		_ = daemon.WriteFrame(dc.conn, daemon.FrameTaggedInput, daemon.EncodeTaggedInput(session, data))
		return
	}
	ref := dc.activePart(session)
	dc.writeToPeer(ref.peerID, daemon.FrameTaggedInput, daemon.EncodeTaggedInput(ref.name, data))
}

// SendResize sends resize to all sessions.
func (dc *daemonConn) SendResize(cols, rows uint16) {
	if dc.multi() {
		for _, peer := range dc.peerList() {
			dc.writePeerFrame(peer, daemon.FrameResize, daemon.MarshalJSON(daemon.ResizePayload{
				Cols: cols, Rows: rows,
			}))
		}
		return
	}
	daemon.WriteFrame(dc.conn, daemon.FrameResize, daemon.MarshalJSON(daemon.ResizePayload{
		Cols: cols, Rows: rows,
	}))
}

// SendResizeSession sends resize to a specific session by name (all of its
// parts in multi mode, so every container tracks the panel size).
func (dc *daemonConn) SendResizeSession(name string, cols, rows uint16) {
	if !dc.multi() {
		_ = daemon.WriteFrame(dc.conn, daemon.FrameResize, daemon.MarshalJSON(daemon.ResizePayload{
			Cols: cols, Rows: rows, Name: name,
		}))
		return
	}
	for _, ref := range dc.partsOf(name) {
		dc.writeToPeer(ref.peerID, daemon.FrameResize, daemon.MarshalJSON(daemon.ResizePayload{
			Cols: cols, Rows: rows, Name: ref.name,
		}))
	}
}

// CreateSession requests the daemon to create a new session.
// dir sets the first tab's working directory ("" = daemon default).
func (dc *daemonConn) CreateSession(name string, command []string, cols, rows uint16, dir string) {
	dc.CreateSessionWithEnv(name, command, cols, rows, dir, nil, "")
}

// CreateSessionWithEnv creates a session on the local daemon: sessions are
// a local concept, remote daemons only ever host shadow tab containers.
func (dc *daemonConn) CreateSessionWithEnv(
	name string,
	command []string,
	cols, rows uint16,
	dir string,
	env []string,
	tabName string,
) {
	payload := daemon.MarshalJSON(daemon.CreateSessionRequest{
		Name: name, Command: command, Cols: cols, Rows: rows, Dir: dir, Env: env, TabName: tabName,
	})
	if dc.multi() {
		dc.writeToPeer(localPeerID, daemon.FrameCreateSession, payload)
		return
	}
	_ = daemon.WriteFrame(dc.conn, daemon.FrameCreateSession, payload)
}

// KillSession requests the daemon to kill a session (all parts in multi mode).
func (dc *daemonConn) KillSession(name string) {
	if !dc.multi() {
		_ = daemon.WriteFrame(dc.conn, daemon.FrameKillSession, daemon.MarshalJSON(daemon.KillSessionRequest{
			Name: name,
		}))
		return
	}
	for _, ref := range dc.partsOf(name) {
		dc.writeToPeer(ref.peerID, daemon.FrameKillSession, daemon.MarshalJSON(daemon.KillSessionRequest{
			Name: ref.name,
		}))
	}
}

// RenameSession requests the daemon to rename a session. In multi mode all
// parts are renamed so shadow names stay derived from the display name.
func (dc *daemonConn) RenameSession(oldName, newName string) {
	if !dc.multi() {
		_ = daemon.WriteFrame(dc.conn, daemon.FrameRenameSession, daemon.MarshalJSON(daemon.RenameSessionRequest{
			OldName: oldName,
			NewName: newName,
		}))
		return
	}
	if newName == "" || strings.Contains(newName, shadowSep) {
		return
	}
	dc.compMu.Lock()
	c := dc.composites[oldName]
	if c == nil || dc.composites[newName] != nil {
		dc.compMu.Unlock()
		return
	}
	type renameOp struct {
		ref partRef
		to  string
	}
	ops := make([]renameOp, 0, len(c.parts))
	for _, p := range c.parts {
		want := newName
		if p.peerID != localPeerID {
			want = shadowSessionName(newName, dc.localID)
		}
		ops = append(ops, renameOp{partRef{p.peerID, p.name}, want})
	}
	dc.compMu.Unlock()
	for _, op := range ops {
		dc.writeToPeer(op.ref.peerID, daemon.FrameRenameSession, daemon.MarshalJSON(daemon.RenameSessionRequest{
			OldName: op.ref.name,
			NewName: op.to,
		}))
	}
}

// Detach sends detach and closes the connection.
func (dc *daemonConn) Detach() {
	if dc.multi() {
		for _, peer := range dc.peerList() {
			dc.writePeerFrame(peer, daemon.FrameDetach, nil)
		}
		dc.closePeers()
		return
	}
	daemon.WriteFrame(dc.conn, daemon.FrameDetach, nil)
	dc.conn.Close()
}

// CreateTab requests a new tab within a session. In multi mode the tab is
// created next to the session's active part (use CreateTabOn to target a
// specific peer).
func (dc *daemonConn) CreateTab(session, name string, command []string, dir string, env []string) {
	if !dc.multi() {
		_ = daemon.WriteFrame(dc.conn, daemon.FrameCreateTab, daemon.MarshalJSON(daemon.CreateTabRequest{
			Session: session,
			Name:    name,
			Command: command,
			Dir:     dir,
			Env:     env,
		}))
		return
	}
	ref := dc.activePart(session)
	dc.writeToPeer(ref.peerID, daemon.FrameCreateTab, daemon.MarshalJSON(daemon.CreateTabRequest{
		Session: ref.name,
		Name:    name,
		Command: command,
		Dir:     dir,
		Env:     env,
	}))
}

// CloseTab requests the daemon to close a tab in a session. In multi mode
// the merged index is mapped to the owning part; closing a part's last tab
// kills that container instead (daemons refuse closing the last tab).
func (dc *daemonConn) CloseTab(session string, index int) {
	if !dc.multi() {
		_ = daemon.WriteFrame(dc.conn, daemon.FrameCloseTab, daemon.MarshalJSON(daemon.CloseTabRequest{
			Session: session,
			Index:   index,
		}))
		return
	}
	dc.compMu.Lock()
	c := dc.composites[session]
	if c == nil {
		dc.compMu.Unlock()
		return
	}
	p, localIdx := c.mapTab(index)
	if p == nil {
		dc.compMu.Unlock()
		return
	}
	ref := partRef{p.peerID, p.name}
	killPart := len(c.parts) > 1 && partTabCount(p) == 1
	dc.compMu.Unlock()

	if killPart {
		dc.writeToPeer(ref.peerID, daemon.FrameKillSession, daemon.MarshalJSON(daemon.KillSessionRequest{
			Name: ref.name,
		}))
		return
	}
	dc.writeToPeer(ref.peerID, daemon.FrameCloseTab, daemon.MarshalJSON(daemon.CloseTabRequest{
		Session: ref.name,
		Index:   localIdx,
	}))
}

// SwitchTab requests the daemon to switch the active tab in a session,
// mapping the merged index to the owning part in multi mode.
func (dc *daemonConn) SwitchTab(session string, index int) {
	if !dc.multi() {
		_ = daemon.WriteFrame(dc.conn, daemon.FrameSwitchTab, daemon.MarshalJSON(daemon.SwitchTabRequest{
			Session: session,
			Index:   index,
		}))
		return
	}
	dc.compMu.Lock()
	c := dc.composites[session]
	if c == nil {
		dc.compMu.Unlock()
		return
	}
	p, localIdx := c.mapTab(index)
	dc.compMu.Unlock()
	if p == nil {
		return
	}
	dc.writeToPeer(p.peerID, daemon.FrameSwitchTab, daemon.MarshalJSON(daemon.SwitchTabRequest{
		Session: p.name,
		Index:   localIdx,
	}))
}

// ReorderSessions pushes the full session display order to the daemon so it
// persists across detach/attach and hot upgrades. Display order is owned by
// the local daemon.
func (dc *daemonConn) ReorderSessions(order []string) {
	payload := daemon.MarshalJSON(daemon.ReorderSessionsRequest{Order: order})
	if dc.multi() {
		dc.writeToPeer(localPeerID, daemon.FrameReorderSess, payload)
		return
	}
	daemon.WriteFrame(dc.conn, daemon.FrameReorderSess, payload)
}

// SetActiveSession tells the daemon which session occupies the big panel so
// the next attach restores it. The big-panel selection is owned by the
// local daemon.
func (dc *daemonConn) SetActiveSession(name string) {
	payload := daemon.MarshalJSON(daemon.SetActiveSessionRequest{Session: name})
	if dc.multi() {
		dc.writeToPeer(localPeerID, daemon.FrameSetActive, payload)
		return
	}
	daemon.WriteFrame(dc.conn, daemon.FrameSetActive, payload)
}

func (dc *daemonConn) writePeerFrame(peer *daemonPeer, ft daemon.FrameType, payload []byte) {
	if peer == nil {
		return
	}
	peer.writeMu.Lock()
	defer peer.writeMu.Unlock()
	_ = daemon.WriteFrame(peer.conn, ft, payload)
}

func (dc *daemonConn) closePeers() {
	dc.closeOnce.Do(func() {
		if dc.closed != nil {
			close(dc.closed)
		}
		for _, peer := range dc.peerList() {
			_ = peer.conn.Close()
		}
	})
}
