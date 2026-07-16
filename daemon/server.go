package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"codetty/terminal"
)

// Server is the daemon that manages PTY sessions.
type Server struct {
	sockPath    string
	daemonID    string    // unique daemon identity, echoed on subscribe ACKs
	lock        *sockLock // exclusive daemon-instance lock; nil until Start
	listener    net.Listener
	sessions    map[string]*DaemonSession
	order       []string // session names in display order (authoritative)
	active      string   // last big-panel session; restored on next attach
	subscribers []*Subscriber
	mu          sync.Mutex
	done        chan struct{}
	stopped     chan struct{}
	wg          sync.WaitGroup
}

// NewServer creates a new daemon server listening on the given Unix socket path.
func NewServer(sockPath string) *Server {
	idBytes := make([]byte, 8)
	rand.Read(idBytes)
	return &Server{
		sockPath: sockPath,
		daemonID: hex.EncodeToString(idBytes),
		sessions: make(map[string]*DaemonSession),
		done:     make(chan struct{}),
		stopped:  make(chan struct{}),
	}
}

// Start begins listening for client connections. The instance lock is
// taken first: clients racing to auto-start the daemon may spawn several
// server processes, and only the lock holder may unlink and (re)bind the
// socket — otherwise a losing daemon's shutdown would remove the socket
// out from under the winner.
func (s *Server) Start() error {
	if s.lock == nil {
		lock, err := acquireSockLock(s.sockPath)
		if err != nil {
			return err
		}
		s.lock = lock
	}
	os.Remove(s.sockPath)

	ln, err := net.Listen("unix", s.sockPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.sockPath, err)
	}
	s.listener = ln

	s.wg.Add(1)
	go s.acceptLoop()

	s.wg.Add(1)
	go s.agentPollLoop()
	return nil
}

// agentPollInterval is the detection tick. The Detector throttles normal
// rescans internally (500ms); the fast tick exists so pending Working→Idle
// holds are re-checked at herdr-equivalent cadence (100ms).
const agentPollInterval = 100 * time.Millisecond

// agentPollLoop periodically runs agent detection on all native sessions
// and broadcasts an "agent_state" session event when anything changed.
func (s *Server) agentPollLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(agentPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case now := <-ticker.C:
			s.mu.Lock()
			sessions := make([]*DaemonSession, 0, len(s.sessions))
			for _, sess := range s.sessions {
				sessions = append(sessions, sess)
			}
			s.mu.Unlock()

			for _, sess := range sessions {
				if sess.PollAgentStates(now) {
					s.notifySubscribers(SessionEvent{
						Type:    "agent_state",
						Session: sess.Name,
						Info:    sess.Info(),
					})
				}
			}
		}
	}
}

// Stop shuts down the server, killing all sessions and cleaning up.
// Done returns a channel closed once the server has fully shut down (via
// kill-server, auto-exit, or Stop). The daemon process waits on it to exit.
func (s *Server) Done() <-chan struct{} {
	return s.stopped
}

// Stop shuts down the server, killing all sessions.
func (s *Server) Stop() {
	select {
	case <-s.done:
		return // already stopped
	default:
		close(s.done)
	}

	if s.listener != nil {
		s.listener.Close()
	}
	s.wg.Wait()

	s.mu.Lock()
	for _, sess := range s.sessions {
		sess.Kill()
	}
	s.mu.Unlock()

	os.Remove(s.sockPath)
	if s.lock != nil {
		s.lock.release()
		s.lock = nil
	}
	close(s.stopped)
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				log.Printf("accept error: %v", err)
				continue
			}
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	ft, payload, err := ReadFrame(conn)
	if err != nil {
		return
	}
	if ft != FrameRequest {
		return
	}

	var req Request
	if err := json.Unmarshal(payload, &req); err != nil {
		s.sendError(conn, "invalid request")
		return
	}

	switch req.Action {
	case ActionNewSession:
		s.handleNewSession(conn, req)
	case ActionAttach:
		s.handleAttach(conn, req)
	case ActionList:
		s.handleList(conn)
	case ActionKill:
		s.handleKill(conn, req)
	case ActionKillServer:
		s.handleKillServer(conn)
	case ActionSubscribeAll:
		s.handleSubscribeAll(conn, req)
	case ActionCreateTab:
		s.handleCreateTab(conn, req)
	case ActionCloseTab:
		s.handleCloseTab(conn, req)
	case ActionUpgrade:
		s.handleUpgrade(conn, req)
	case ActionSendKeys:
		s.handleSendKeys(conn, req)
	case ActionCapture:
		s.handleCapture(conn, req)
	default:
		s.sendError(conn, fmt.Sprintf("unknown action: %s", req.Action))
	}
}

func (s *Server) handleNewSession(conn net.Conn, req Request) {
	cmd := req.Command
	if len(cmd) == 0 {
		cmd = []string{terminal.DefaultShell()}
	}
	name := req.Session
	if name == "" {
		s.mu.Lock()
		name = autoSessionName(cmd, s.sessions)
		s.mu.Unlock()
	}

	s.mu.Lock()
	if _, exists := s.sessions[name]; exists {
		s.mu.Unlock()
		s.sendError(conn, fmt.Sprintf("session %q already exists", name))
		return
	}
	s.mu.Unlock()

	sess, err := newDaemonSession(name, cmd, req.Cols, req.Rows, req.Dir, req.Env, s.broadcastToSubscribers)
	if err != nil {
		s.sendError(conn, fmt.Sprintf("start session: %v", err))
		return
	}

	// Set the first tab's name if provided
	if req.TabName != "" && len(sess.Tabs) > 0 {
		sess.Tabs[0].Name = req.TabName
	}

	s.mu.Lock()
	if _, exists := s.sessions[name]; exists {
		s.mu.Unlock()
		sess.Kill()
		s.sendError(conn, fmt.Sprintf("session %q already exists", name))
		return
	}
	s.addSessionLocked(sess)
	s.mu.Unlock()

	s.notifySubscribers(SessionEvent{Type: "created", Session: name, Info: sess.Info()})
	go s.watchSession(sess)

	resp := Response{OK: true, Session: name}
	WriteFrame(conn, FrameResponse, MarshalJSON(resp))

	if !req.NoAttach {
		s.runAttachedClient(conn, sess)
	}
}

func (s *Server) handleAttach(conn net.Conn, req Request) {
	s.mu.Lock()
	var sess *DaemonSession
	if req.Session != "" {
		sess = s.sessions[req.Session]
	} else {
		var latest time.Time
		for _, ss := range s.sessions {
			if !ss.isExited() && ss.CreatedAt.After(latest) {
				sess = ss
				latest = ss.CreatedAt
			}
		}
	}
	s.mu.Unlock()

	if sess == nil {
		s.sendError(conn, "no session found")
		return
	}
	if sess.isExited() {
		s.sendError(conn, fmt.Sprintf("session %q has exited", sess.Name))
		return
	}

	resp := Response{OK: true, Session: sess.Name}
	WriteFrame(conn, FrameResponse, MarshalJSON(resp))
	s.runAttachedClient(conn, sess)
}

func (s *Server) handleList(conn net.Conn) {
	s.mu.Lock()
	infos := make([]SessionInfo, 0, len(s.sessions))
	for _, sess := range s.orderedSessionsLocked() {
		infos = append(infos, sess.Info())
	}
	s.mu.Unlock()

	resp := Response{OK: true, Sessions: infos}
	WriteFrame(conn, FrameResponse, MarshalJSON(resp))
}

func (s *Server) handleKill(conn net.Conn, req Request) {
	s.mu.Lock()
	sess, exists := s.sessions[req.Session]
	s.mu.Unlock()

	if !exists {
		s.sendError(conn, fmt.Sprintf("session %q not found", req.Session))
		return
	}

	sess.Kill()
	WriteFrame(conn, FrameResponse, MarshalJSON(Response{OK: true}))
}

func (s *Server) handleKillServer(conn net.Conn) {
	WriteFrame(conn, FrameResponse, MarshalJSON(Response{OK: true}))
	go s.Stop()
}

func (s *Server) runAttachedClient(conn net.Conn, sess *DaemonSession) {
	client := newClient(conn, sess)
	sess.addClient(client)
	defer sess.removeClient(client)

	// Send screen snapshot for immediate restore
	if snap := sess.Screen.Snapshot(); len(snap) > 0 {
		WriteFrame(conn, FrameOutput, snap)
	}

	client.Run(s)
}

func (s *Server) addSubscriber(sub *Subscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscribers = append(s.subscribers, sub)
}

func (s *Server) removeSubscriber(sub *Subscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, ss := range s.subscribers {
		if ss == sub {
			s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
			return
		}
	}
}

func (s *Server) broadcastToSubscribers(session string, data []byte) {
	s.mu.Lock()
	subs := append([]*Subscriber(nil), s.subscribers...)
	s.mu.Unlock()
	for _, sub := range subs {
		sub.SendTaggedOutput(session, data)
	}
}

func (s *Server) notifySubscribers(evt SessionEvent) {
	s.mu.Lock()
	subs := append([]*Subscriber(nil), s.subscribers...)
	s.mu.Unlock()
	for _, sub := range subs {
		sub.SendSessionEvent(evt)
	}
}

func (s *Server) resizeAllSessions(cols, rows uint16) {
	s.mu.Lock()
	sessions := make([]*DaemonSession, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.mu.Unlock()
	for _, sess := range sessions {
		sess.Resize(cols, rows)
	}
}

func (s *Server) createSessionForSubscriber(sub *Subscriber, req CreateSessionRequest) {
	name := req.Name
	if name == "" {
		s.mu.Lock()
		name = autoSessionName(req.Command, s.sessions)
		s.mu.Unlock()
	}
	cmd := req.Command
	if len(cmd) == 0 {
		cmd = []string{terminal.DefaultShell()}
	}
	s.mu.Lock()
	if _, exists := s.sessions[name]; exists {
		s.mu.Unlock()
		sub.SendSessionEvent(SessionEvent{Type: "error", Session: name})
		return
	}
	s.mu.Unlock()

	sess, err := newDaemonSession(name, cmd, req.Cols, req.Rows, req.Dir, req.Env, s.broadcastToSubscribers)
	if err != nil {
		sub.SendSessionEvent(SessionEvent{Type: "error", Session: name})
		return
	}
	if req.TabName != "" && len(sess.Tabs) > 0 {
		sess.Tabs[0].Name = req.TabName
	}
	s.mu.Lock()
	if _, exists := s.sessions[name]; exists {
		s.mu.Unlock()
		sess.Kill()
		sub.SendSessionEvent(SessionEvent{Type: "error", Session: name})
		return
	}
	s.addSessionLocked(sess)
	s.mu.Unlock()
	go s.watchSession(sess)
	s.notifySubscribers(SessionEvent{Type: "created", Session: name, Info: sess.Info()})
}

func (s *Server) killSessionForSubscriber(req KillSessionRequest) {
	s.mu.Lock()
	sess, exists := s.sessions[req.Name]
	s.mu.Unlock()
	if exists {
		sess.Kill()
	}
}

func (s *Server) renameSessionForSubscriber(req RenameSessionRequest) {
	if req.OldName == "" || req.NewName == "" || req.OldName == req.NewName {
		return
	}
	s.mu.Lock()
	sess, exists := s.sessions[req.OldName]
	if !exists {
		s.mu.Unlock()
		return
	}
	if _, conflict := s.sessions[req.NewName]; conflict {
		s.mu.Unlock()
		return
	}

	delete(s.sessions, req.OldName)
	sess.Name = req.NewName
	s.sessions[req.NewName] = sess
	if s.active == req.OldName {
		s.active = req.NewName
	}
	for i, n := range s.order {
		if n == req.OldName {
			s.order[i] = req.NewName
			break
		}
	}
	s.mu.Unlock()

	s.notifySubscribers(SessionEvent{
		Type:    "renamed",
		Session: req.OldName,
		Info:    SessionInfo{Name: req.NewName},
	})
}

func (s *Server) handleSubscribeAll(conn net.Conn, req Request) {
	// SSH-style negotiation: reject before sending any state when the
	// version ranges don't overlap; otherwise remember the negotiated
	// version and capability intersection for this subscriber.
	proto, ok := NegotiateProto(req.Proto, req.ProtoMin)
	if !ok {
		WriteFrame(conn, FrameResponse, MarshalJSON(Response{
			Error: fmt.Sprintf(
				"incompatible protocol: daemon speaks %d..%d, client speaks %d..%d (upgrade the older side)",
				MinProtocolVersion, ProtocolVersion, req.ProtoMin, req.Proto),
			Proto:    ProtocolVersion,
			ProtoMin: MinProtocolVersion,
			Caps:     ProtocolCaps(),
		}))
		return
	}

	sub := newSubscriber(conn)
	sub.proto = proto
	sub.caps = IntersectCaps(req.Caps)
	s.addSubscriber(sub)
	defer s.removeSubscriber(sub)

	s.mu.Lock()
	sessions := s.orderedSessionsLocked()
	activeSess := s.sessions[s.active]
	s.mu.Unlock()

	// Announce the remembered big-panel session before the snapshots so
	// the client can adopt it as its focus target (an explicit attach
	// target set by the client takes precedence on its side). With no
	// remembered session, fall back to the most recently created one,
	// preserving the historical `attach` default.
	if activeSess == nil || activeSess.isExited() {
		activeSess = nil
		var latest time.Time
		for _, sess := range sessions {
			if !sess.isExited() && sess.CreatedAt.After(latest) {
				activeSess = sess
				latest = sess.CreatedAt
			}
		}
	}
	if activeSess != nil {
		sub.sendFrameDirect(FrameSessionEvent, MarshalJSON(SessionEvent{
			Type:    "active",
			Session: activeSess.Name,
		}))
	}

	for _, sess := range sessions {
		if !sess.isExited() {
			snap := sess.Screen.Snapshot()
			cols, rows := sess.Screen.Size()
			sub.sendFrameDirect(FrameSessionEvent, MarshalJSON(SessionEvent{
				Type:    "snapshot",
				Session: sess.Name,
				Info:    sess.Info(),
				Cols:    cols,
				Rows:    rows,
			}))
			if len(snap) > 0 {
				sub.sendFrameDirect(FrameTaggedOutput, EncodeTaggedOutput(sess.Name, snap))
			}
		}
	}

	sub.sendFrameDirect(FrameResponse, MarshalJSON(Response{
		OK:       true,
		Proto:    ProtocolVersion,
		ProtoMin: MinProtocolVersion,
		Caps:     ProtocolCaps(),
		DaemonID: s.daemonID,
	}))
	sub.activate()
	sub.Run(s)
}

func (s *Server) watchSession(sess *DaemonSession) {
	sess.setTabsChangedCallback(s.handleTabsAutoChanged)
	<-sess.doneCh

	notif := Notification{Event: "session_exited", Session: sess.Name, ExitCode: sess.exitCode}
	sess.mu.Lock()
	clients := append([]*Client(nil), sess.clients...)
	sess.mu.Unlock()
	for _, c := range clients {
		c.SendNotification(notif)
	}

	s.mu.Lock()
	s.removeSessionLocked(sess.Name)
	remaining := len(s.sessions)
	s.mu.Unlock()

	s.notifySubscribers(SessionEvent{Type: "exited", Session: sess.Name, ExitCode: sess.exitCode})

	if remaining == 0 {
		go s.Stop()
	}
}

// handleTabsAutoChanged broadcasts tab list changes that originate inside
// the daemon (auto-close of an exited shell tab, shell respawn in a tab
// whose command exited) rather than from a client request.
func (s *Server) handleTabsAutoChanged(sess *DaemonSession, event string) {
	s.notifySubscribers(SessionEvent{
		Type:    event,
		Session: sess.Name,
		Info:    sess.Info(),
	})

	// The active tab may have changed (auto-close): send a fresh snapshot
	// so clients re-render the newly active tab.
	if event == "tab_closed" {
		snap := sess.Screen.Snapshot()
		if len(snap) > 0 {
			s.mu.Lock()
			subs := append([]*Subscriber(nil), s.subscribers...)
			s.mu.Unlock()
			for _, sub := range subs {
				sub.SendTaggedOutput(sess.Name, snap)
			}
		}
	}
}

func (s *Server) sendError(conn net.Conn, msg string) {
	WriteFrame(conn, FrameResponse, MarshalJSON(Response{OK: false, Error: msg}))
}

// SessionByName returns a session by name (for client session switching).
func (s *Server) SessionByName(name string) *DaemonSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[name]
}

// addSessionLocked registers a session and appends it to the display order.
// Caller must hold s.mu.
func (s *Server) addSessionLocked(sess *DaemonSession) {
	s.sessions[sess.Name] = sess
	s.order = append(s.order, sess.Name)
}

// removeSessionLocked unregisters a session and drops it from the display
// order. Caller must hold s.mu.
func (s *Server) removeSessionLocked(name string) {
	delete(s.sessions, name)
	if s.active == name {
		s.active = ""
	}
	for i, n := range s.order {
		if n == name {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
}

// orderedSessionsLocked returns sessions in display order. Caller must hold s.mu.
func (s *Server) orderedSessionsLocked() []*DaemonSession {
	result := make([]*DaemonSession, 0, len(s.order))
	for _, name := range s.order {
		if sess, ok := s.sessions[name]; ok {
			result = append(result, sess)
		}
	}
	return result
}

// reorderSessionsForSubscriber applies a client-requested display order and
// broadcasts the new order to the other subscribers (the sender already has
// it locally; echoing it back could transiently undo rapid follow-up moves).
// Unknown names are ignored; sessions missing from the request keep their
// relative order at the end.
func (s *Server) reorderSessionsForSubscriber(from *Subscriber, req ReorderSessionsRequest) {
	s.mu.Lock()
	newOrder := make([]string, 0, len(s.order))
	seen := make(map[string]bool, len(s.order))
	for _, name := range req.Order {
		if _, ok := s.sessions[name]; ok && !seen[name] {
			newOrder = append(newOrder, name)
			seen[name] = true
		}
	}
	for _, name := range s.order {
		if !seen[name] {
			newOrder = append(newOrder, name)
		}
	}
	s.order = newOrder
	order := append([]string(nil), newOrder...)
	subs := append([]*Subscriber(nil), s.subscribers...)
	s.mu.Unlock()

	evt := SessionEvent{Type: "reordered", Order: order}
	for _, sub := range subs {
		if sub != from {
			sub.SendSessionEvent(evt)
		}
	}
}

// setActiveSessionForSubscriber remembers the client's big-panel session so
// the next attach restores it. Unknown names are ignored.
func (s *Server) setActiveSessionForSubscriber(req SetActiveSessionRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[req.Session]; ok {
		s.active = req.Session
	}
}

// SessionNames returns session names in display order.
func (s *Server) SessionNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}

// createTabForSubscriber creates a new tab within an existing session.
func (s *Server) createTabForSubscriber(sub *Subscriber, req CreateTabRequest) {
	sess := s.SessionByName(req.Session)
	if sess == nil {
		return
	}

	cmd := req.Command
	if len(cmd) == 0 {
		cmd = []string{terminal.DefaultShell()}
	}

	name := req.Name
	if name == "" {
		name = fmt.Sprintf("tab-%d", sess.TabCount())
	}

	// Use the active tab's screen size for the new tab
	s.mu.Lock()
	var cols, rows uint16
	if len(sess.Tabs) > 0 {
		cols, rows = sess.Tabs[sess.ActiveTab].Screen.Size()
	} else {
		cols, rows = 80, 24
	}
	s.mu.Unlock()

	idx, err := sess.AddTab(name, cmd, cols, rows, req.Dir, req.Env)
	if err != nil {
		return
	}

	// Notify all subscribers about the new tab
	s.notifySubscribers(SessionEvent{
		Type:    "tab_created",
		Session: req.Session,
		Info:    sess.Info(),
	})

	// Auto-switch to the new tab
	sess.SwitchTab(idx)
	s.notifyTabSwitch(sub, sess, idx)
}

// handleCreateTab handles a create_tab request from a non-subscriber connection.
func (s *Server) handleCreateTab(conn net.Conn, req Request) {
	sess := s.SessionByName(req.Session)
	if sess == nil {
		s.sendError(conn, fmt.Sprintf("session %q not found", req.Session))
		return
	}

	cmd := req.Command
	if len(cmd) == 0 {
		cmd = []string{terminal.DefaultShell()}
	}

	name := req.TabName
	if name == "" {
		name = fmt.Sprintf("tab-%d", sess.TabCount())
	}

	cols, rows := req.Cols, req.Rows
	if cols == 0 || rows == 0 {
		sess.mu.Lock()
		if len(sess.Tabs) > 0 {
			cols, rows = sess.Tabs[sess.ActiveTab].Screen.Size()
		} else {
			cols, rows = 80, 24
		}
		sess.mu.Unlock()
	}

	_, err := sess.AddTab(name, cmd, cols, rows, req.Dir, req.Env)
	if err != nil {
		s.sendError(conn, fmt.Sprintf("create tab: %v", err))
		return
	}

	// Notify subscribers
	s.notifySubscribers(SessionEvent{
		Type:    "tab_created",
		Session: sess.Name,
		Info:    sess.Info(),
	})

	WriteFrame(conn, FrameResponse, MarshalJSON(Response{OK: true}))
}

// handleCloseTab handles a close_tab request from a non-subscriber connection.
func (s *Server) handleCloseTab(conn net.Conn, req Request) {
	sess := s.SessionByName(req.Session)
	if sess == nil {
		s.sendError(conn, fmt.Sprintf("session %q not found", req.Session))
		return
	}

	if err := sess.CloseTab(req.TabIndex); err != nil {
		s.sendError(conn, fmt.Sprintf("close tab: %v", err))
		return
	}

	s.notifySubscribers(SessionEvent{
		Type:    "tab_closed",
		Session: sess.Name,
		Info:    sess.Info(),
	})

	WriteFrame(conn, FrameResponse, MarshalJSON(Response{OK: true}))
}

// tabByRef resolves a tab reference within a session: "" = active tab, a
// number = tab index, anything else = tab name.
func (s *DaemonSession) tabByRef(ref string) (*SessionTab, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.Tabs) == 0 {
		return nil, fmt.Errorf("session has no tabs")
	}
	if ref == "" {
		return s.Tabs[s.ActiveTab], nil
	}
	if idx, err := strconv.Atoi(ref); err == nil {
		if idx < 0 || idx >= len(s.Tabs) {
			return nil, fmt.Errorf("tab index %d out of range (0..%d)", idx, len(s.Tabs)-1)
		}
		return s.Tabs[idx], nil
	}
	for _, tab := range s.Tabs {
		if tab.Name == ref {
			return tab, nil
		}
	}
	return nil, fmt.Errorf("tab %q not found", ref)
}

// handleSendKeys writes raw bytes to a tab's PTY (tmux send-keys analog;
// the CLI translates key names to bytes).
func (s *Server) handleSendKeys(conn net.Conn, req Request) {
	sess := s.SessionByName(req.Session)
	if sess == nil {
		s.sendError(conn, fmt.Sprintf("session %q not found", req.Session))
		return
	}
	tab, err := sess.tabByRef(req.Tab)
	if err != nil {
		s.sendError(conn, err.Error())
		return
	}
	if _, err := tab.Terminal.Write(req.Data); err != nil {
		s.sendError(conn, fmt.Sprintf("write: %v", err))
		return
	}
	WriteFrame(conn, FrameResponse, MarshalJSON(Response{OK: true}))
}

// handleCapture returns a tab's screen as plain text (tmux capture-pane
// analog). Rows limits output to the bottom N rows; 0 = whole screen.
func (s *Server) handleCapture(conn net.Conn, req Request) {
	sess := s.SessionByName(req.Session)
	if sess == nil {
		s.sendError(conn, fmt.Sprintf("session %q not found", req.Session))
		return
	}
	tab, err := sess.tabByRef(req.Tab)
	if err != nil {
		s.sendError(conn, err.Error())
		return
	}
	WriteFrame(conn, FrameResponse, MarshalJSON(Response{
		OK:   true,
		Text: tab.Screen.TailText(int(req.Rows)),
	}))
}

// closeTabForSubscriber closes a tab in a session.
func (s *Server) closeTabForSubscriber(sub *Subscriber, req CloseTabRequest) {
	sess := s.SessionByName(req.Session)
	if sess == nil {
		return
	}

	if err := sess.CloseTab(req.Index); err != nil {
		return
	}

	s.notifySubscribers(SessionEvent{
		Type:    "tab_closed",
		Session: req.Session,
		Info:    sess.Info(),
	})

	// Send snapshot of the new active tab
	s.sendTabSnapshot(sub, sess)
}

// switchTabForSubscriber switches the active tab in a session.
func (s *Server) switchTabForSubscriber(sub *Subscriber, req SwitchTabRequest) {
	sess := s.SessionByName(req.Session)
	if sess == nil {
		return
	}

	if err := sess.SwitchTab(req.Index); err != nil {
		return
	}

	s.notifyTabSwitch(sub, sess, req.Index)
}

// notifyTabSwitch notifies subscribers about a tab switch and sends a snapshot.
func (s *Server) notifyTabSwitch(sub *Subscriber, sess *DaemonSession, tabIdx int) {
	s.notifySubscribers(SessionEvent{
		Type:    "tab_switched",
		Session: sess.Name,
		Info:    sess.Info(),
	})

	// Send a snapshot of the new active tab's screen
	s.sendTabSnapshot(sub, sess)
}

// sendTabSnapshot sends the active tab's screen content to all subscribers.
func (s *Server) sendTabSnapshot(sub *Subscriber, sess *DaemonSession) {
	snap := sess.Screen.Snapshot()
	if len(snap) > 0 {
		s.mu.Lock()
		subs := append([]*Subscriber(nil), s.subscribers...)
		s.mu.Unlock()
		for _, s := range subs {
			s.SendTaggedOutput(sess.Name, snap)
		}
	}
}
