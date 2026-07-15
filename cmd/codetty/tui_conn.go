package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"codetty/daemon"

	tea "github.com/charmbracelet/bubbletea"
)

// daemonConn manages the connection between TUI and daemon.
type daemonConn struct {
	conn       io.ReadWriteCloser
	sockPath   string
	program    *tea.Program
	subscribed bool
	endpoint   daemonEndpoint

	peers       map[string]*daemonPeer
	sessionPeer map[string]string
	// pendingCreates counts this client's unnamed create requests per peer,
	// so the daemon-generated name can be routed back when the "created"
	// event arrives (guarded by routeMu).
	pendingCreates map[string]int
	routeMu        sync.RWMutex
	defaultPeer    string
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

func newMultiDaemonConn(endpoints map[string]daemonEndpoint, sessionPeer map[string]string) (*daemonConn, error) {
	ids := make([]string, 0, len(endpoints))
	for id := range endpoints {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	dc := &daemonConn{
		peers:          make(map[string]*daemonPeer, len(endpoints)),
		sessionPeer:    make(map[string]string, len(sessionPeer)),
		pendingCreates: make(map[string]int),
		closed:         make(chan struct{}),
	}
	for name, id := range sessionPeer {
		dc.sessionPeer[name] = id
	}
	if _, ok := endpoints["local"]; ok {
		dc.defaultPeer = "local"
	} else if len(ids) > 0 {
		dc.defaultPeer = ids[0]
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

// Subscribe sends subscribe_all and starts the read loop.
func (dc *daemonConn) Subscribe(p *tea.Program) error {
	dc.program = p

	if len(dc.peers) > 0 {
		dc.subMu.Lock()
		dc.pendingSubs = len(dc.peers)
		dc.subMu.Unlock()
		for _, peer := range dc.peers {
			if err := dc.subscribePeer(peer); err != nil {
				return err
			}
			go dc.readPeerLoop(peer)
		}
		return nil
	}

	if err := daemon.WriteFrame(dc.conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{
		Action: daemon.ActionSubscribeAll,
	})); err != nil {
		return fmt.Errorf("send subscribe_all request: %w", err)
	}

	go dc.readLoop()
	return nil
}

func (dc *daemonConn) subscribePeer(peer *daemonPeer) error {
	peer.writeMu.Lock()
	defer peer.writeMu.Unlock()
	if err := daemon.WriteFrame(peer.conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{
		Action: daemon.ActionSubscribeAll,
	})); err != nil {
		return fmt.Errorf("subscribe %s: %w", peer.id, err)
	}
	return nil
}

func (dc *daemonConn) readPeerLoop(peer *daemonPeer) {
	for {
		_ = dc.readPeer(peer)
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
		if err == nil && dc.acceptSession(peer.id, session) {
			dc.bufferOutput(session, data)
		}
	case daemon.FrameSessionEvent:
		var evt daemon.SessionEvent
		if json.Unmarshal(payload, &evt) != nil {
			return
		}
		if evt.Type == "created" {
			dc.claimPendingCreate(peer.id, evt.Session)
		}
		if dc.acceptEvent(peer.id, evt) {
			if evt.Type == "renamed" &&
				evt.Info.Name != "" &&
				dc.sessionKnownOnOtherPeer(peer.id, evt.Info.Name) {
				dc.dropConflictingRename(peer, evt.Session)
				dc.program.Send(daemonSessionEventMsg{Event: daemon.SessionEvent{
					Type:    "exited",
					Session: evt.Session,
				}})
				return
			}
			dc.trackPeerEvent(peer, evt)
			dc.program.Send(daemonSessionEventMsg{Event: evt})
		}
	case daemon.FrameResponse:
		for _, session := range dc.finishPeerReconcile(peer) {
			dc.program.Send(daemonSessionEventMsg{Event: daemon.SessionEvent{
				Type:    "exited",
				Session: session,
			}})
		}
		peer.stateMu.Lock()
		firstAck := !peer.initialAck
		peer.initialAck = true
		peer.stateMu.Unlock()
		if firstAck {
			dc.subMu.Lock()
			if dc.pendingSubs > 0 {
				dc.pendingSubs--
				if dc.pendingSubs == 0 {
					dc.program.Send(daemonSubscribedMsg{})
				}
			}
			dc.subMu.Unlock()
		}
	}
}

func (dc *daemonConn) sessionKnownOnOtherPeer(peerID, name string) bool {
	dc.routeMu.RLock()
	routedID, routed := dc.sessionPeer[name]
	dc.routeMu.RUnlock()
	if routed && routedID != peerID {
		return true
	}
	for id, peer := range dc.peers {
		if id == peerID {
			continue
		}
		peer.stateMu.Lock()
		known := peer.known[name]
		peer.stateMu.Unlock()
		if known {
			return true
		}
	}
	return false
}

func (dc *daemonConn) dropConflictingRename(peer *daemonPeer, oldName string) {
	peer.stateMu.Lock()
	delete(peer.known, oldName)
	peer.stateMu.Unlock()

	dc.routeMu.Lock()
	if id, ok := dc.sessionPeer[oldName]; ok && id == peer.id {
		delete(dc.sessionPeer, oldName)
	}
	dc.routeMu.Unlock()
}

func (dc *daemonConn) trackPeerEvent(peer *daemonPeer, evt daemon.SessionEvent) {
	peer.stateMu.Lock()
	switch evt.Type {
	case "snapshot", "created":
		peer.known[evt.Session] = true
		if peer.reconcile {
			peer.seen[evt.Session] = true
		}
	case "renamed":
		delete(peer.known, evt.Session)
		if evt.Info.Name != "" {
			peer.known[evt.Info.Name] = true
			if peer.reconcile {
				peer.seen[evt.Info.Name] = true
			}
		}
	case "exited":
		delete(peer.known, evt.Session)
	}
	peer.stateMu.Unlock()

	if evt.Type == "renamed" && evt.Info.Name != "" {
		dc.routeMu.Lock()
		if id, ok := dc.sessionPeer[evt.Session]; ok && id == peer.id {
			delete(dc.sessionPeer, evt.Session)
			dc.sessionPeer[evt.Info.Name] = id
		}
		dc.routeMu.Unlock()
	}
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

func (dc *daemonConn) acceptSession(peerID, session string) bool {
	dc.routeMu.RLock()
	defer dc.routeMu.RUnlock()
	if expected, ok := dc.sessionPeer[session]; ok {
		return expected == peerID
	}
	return peerID == "local"
}

// claimPendingCreate routes a daemon-named session back to the peer that
// created it in response to this client's own unnamed create request, so
// its events are not filtered out when the default peer is remote.
func (dc *daemonConn) claimPendingCreate(peerID, session string) {
	dc.routeMu.Lock()
	defer dc.routeMu.Unlock()
	if dc.pendingCreates[peerID] <= 0 {
		return
	}
	if _, routed := dc.sessionPeer[session]; routed {
		return
	}
	dc.pendingCreates[peerID]--
	dc.sessionPeer[session] = peerID
}

func (dc *daemonConn) acceptEvent(peerID string, evt daemon.SessionEvent) bool {
	if evt.Type == "active" {
		return dc.acceptSession(peerID, evt.Session)
	}
	if evt.Type == "reordered" {
		return peerID == dc.defaultPeer
	}
	return dc.acceptSession(peerID, evt.Session)
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
		if err := daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{
			Action: daemon.ActionSubscribeAll,
		})); err != nil {
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

// SendInput sends tagged input to a session.
func (dc *daemonConn) SendInput(session string, data []byte) {
	dc.writeSessionFrame(session, daemon.FrameTaggedInput, daemon.EncodeTaggedInput(session, data))
}

// SendResize sends resize to all sessions.
func (dc *daemonConn) SendResize(cols, rows uint16) {
	if len(dc.peers) > 0 {
		for _, peer := range dc.peers {
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

// SendResizeSession sends resize to a specific session by name.
func (dc *daemonConn) SendResizeSession(name string, cols, rows uint16) {
	dc.writeSessionFrame(name, daemon.FrameResize, daemon.MarshalJSON(daemon.ResizePayload{
		Cols: cols, Rows: rows, Name: name,
	}))
}

// CreateSession requests the daemon to create a new session.
// dir sets the first tab's working directory ("" = daemon default).
func (dc *daemonConn) CreateSession(name string, command []string, cols, rows uint16, dir string) {
	dc.CreateSessionWithEnv(name, command, cols, rows, dir, nil, "")
}

func (dc *daemonConn) CreateSessionWithEnv(
	name string,
	command []string,
	cols, rows uint16,
	dir string,
	env []string,
	tabName string,
) {
	if len(dc.peers) > 0 {
		if name != "" {
			_ = dc.RouteSession(name, dc.defaultPeer)
		} else if dc.defaultPeer != "local" {
			// The daemon will pick a name; remember to claim the resulting
			// "created" event so the session isn't filtered as unrouted.
			dc.routeMu.Lock()
			dc.pendingCreates[dc.defaultPeer]++
			dc.routeMu.Unlock()
		}
	}
	dc.writeSessionFrame(name, daemon.FrameCreateSession, daemon.MarshalJSON(daemon.CreateSessionRequest{
		Name: name, Command: command, Cols: cols, Rows: rows, Dir: dir, Env: env, TabName: tabName,
	}))
}

// KillSession requests the daemon to kill a session.
func (dc *daemonConn) KillSession(name string) {
	dc.writeSessionFrame(name, daemon.FrameKillSession, daemon.MarshalJSON(daemon.KillSessionRequest{
		Name: name,
	}))
}

// RenameSession requests the daemon to rename a session.
func (dc *daemonConn) RenameSession(oldName, newName string) {
	if len(dc.peers) > 0 {
		peer := dc.peerForSession(oldName)
		if peer == nil || dc.sessionKnownOnOtherPeer(peer.id, newName) {
			return
		}
	}
	dc.writeSessionFrame(oldName, daemon.FrameRenameSession, daemon.MarshalJSON(daemon.RenameSessionRequest{
		OldName: oldName,
		NewName: newName,
	}))
}

// Detach sends detach and closes the connection.
func (dc *daemonConn) Detach() {
	if len(dc.peers) > 0 {
		for _, peer := range dc.peers {
			dc.writePeerFrame(peer, daemon.FrameDetach, nil)
		}
		dc.closePeers()
		return
	}
	daemon.WriteFrame(dc.conn, daemon.FrameDetach, nil)
	dc.conn.Close()
}

// CreateTab requests the daemon to create a new tab within a session.
func (dc *daemonConn) CreateTab(session, name string, command []string, dir string, env []string) {
	dc.writeSessionFrame(session, daemon.FrameCreateTab, daemon.MarshalJSON(daemon.CreateTabRequest{
		Session: session,
		Name:    name,
		Command: command,
		Dir:     dir,
		Env:     env,
	}))
}

// CloseTab requests the daemon to close a tab in a session.
func (dc *daemonConn) CloseTab(session string, index int) {
	dc.writeSessionFrame(session, daemon.FrameCloseTab, daemon.MarshalJSON(daemon.CloseTabRequest{
		Session: session,
		Index:   index,
	}))
}

// SwitchTab requests the daemon to switch the active tab in a session.
func (dc *daemonConn) SwitchTab(session string, index int) {
	dc.writeSessionFrame(session, daemon.FrameSwitchTab, daemon.MarshalJSON(daemon.SwitchTabRequest{
		Session: session,
		Index:   index,
	}))
}

// ReorderSessions pushes the full session display order to the daemon so it
// persists across detach/attach and hot upgrades.
func (dc *daemonConn) ReorderSessions(order []string) {
	if len(dc.peers) > 0 {
		grouped := make(map[string][]string)
		for _, name := range order {
			peer := dc.peerForSession(name)
			if peer != nil {
				grouped[peer.id] = append(grouped[peer.id], name)
			}
		}
		for id, names := range grouped {
			dc.writePeerFrame(dc.peers[id], daemon.FrameReorderSess, daemon.MarshalJSON(daemon.ReorderSessionsRequest{
				Order: names,
			}))
		}
		return
	}
	daemon.WriteFrame(dc.conn, daemon.FrameReorderSess, daemon.MarshalJSON(daemon.ReorderSessionsRequest{
		Order: order,
	}))
}

// SetActiveSession tells the daemon which session occupies the big panel so
// the next attach restores it.
func (dc *daemonConn) SetActiveSession(name string) {
	dc.writeSessionFrame(name, daemon.FrameSetActive, daemon.MarshalJSON(daemon.SetActiveSessionRequest{
		Session: name,
	}))
}

func (dc *daemonConn) RouteSession(name, peerID string) bool {
	if len(dc.peers) == 0 {
		return peerID == "" || peerID == "local"
	}
	if _, ok := dc.peers[peerID]; !ok {
		return false
	}
	dc.routeMu.Lock()
	if current, ok := dc.sessionPeer[name]; ok && current != peerID {
		dc.routeMu.Unlock()
		return false
	}
	dc.sessionPeer[name] = peerID
	dc.routeMu.Unlock()
	return true
}

func (dc *daemonConn) SessionPeer(name string) string {
	if len(dc.peers) == 0 {
		return "local"
	}
	dc.routeMu.RLock()
	defer dc.routeMu.RUnlock()
	if id, ok := dc.sessionPeer[name]; ok {
		return id
	}
	return dc.defaultPeer
}

func (dc *daemonConn) PeerHasSession(peerID, name string) (bool, error) {
	if len(dc.peers) == 0 {
		return false, nil
	}
	peer := dc.peers[peerID]
	if peer == nil {
		return false, fmt.Errorf("daemon peer %q is not connected", peerID)
	}
	sessions, err := querySessionsEndpoint(peer.endpoint)
	if err != nil {
		return false, err
	}
	for _, session := range sessions {
		if session.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func (dc *daemonConn) RefreshPeer(peerID string) {
	peer := dc.peers[peerID]
	if peer == nil {
		return
	}
	peer.writeMu.Lock()
	_ = peer.conn.Close()
	peer.writeMu.Unlock()
}

func (dc *daemonConn) writeSessionFrame(session string, ft daemon.FrameType, payload []byte) {
	if len(dc.peers) == 0 {
		_ = daemon.WriteFrame(dc.conn, ft, payload)
		return
	}
	if peer := dc.peerForSession(session); peer != nil {
		dc.writePeerFrame(peer, ft, payload)
	}
}

func (dc *daemonConn) peerForSession(session string) *daemonPeer {
	id := dc.defaultPeer
	dc.routeMu.RLock()
	if routed, ok := dc.sessionPeer[session]; ok {
		id = routed
	}
	dc.routeMu.RUnlock()
	return dc.peers[id]
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
		for _, peer := range dc.peers {
			_ = peer.conn.Close()
		}
	})
}
