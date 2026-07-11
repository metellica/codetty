package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"codetty/terminal"
)

// Server is the daemon that manages PTY sessions.
type Server struct {
	sockPath    string
	listener    net.Listener
	sessions    map[string]*DaemonSession
	subscribers []*Subscriber
	mu          sync.Mutex
	done        chan struct{}
	wg          sync.WaitGroup
}

// NewServer creates a new daemon server listening on the given Unix socket path.
func NewServer(sockPath string) *Server {
	return &Server{
		sockPath: sockPath,
		sessions: make(map[string]*DaemonSession),
		done:     make(chan struct{}),
	}
}

// Start begins listening for client connections.
func (s *Server) Start() error {
	os.Remove(s.sockPath)

	ln, err := net.Listen("unix", s.sockPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.sockPath, err)
	}
	s.listener = ln

	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

// Stop shuts down the server, killing all sessions and cleaning up.
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
	case ActionRefreshTmux:
		s.handleRefreshTmux(conn)
	case ActionCreateTab:
		s.handleCreateTab(conn, req)
	default:
		s.sendError(conn, fmt.Sprintf("unknown action: %s", req.Action))
	}
}

func (s *Server) handleNewSession(conn net.Conn, req Request) {
	name := req.Session
	if name == "" {
		s.mu.Lock()
		name = autoSessionName(req.Command, s.sessions)
		s.mu.Unlock()
	}

	s.mu.Lock()
	if _, exists := s.sessions[name]; exists {
		s.mu.Unlock()
		s.sendError(conn, fmt.Sprintf("session %q already exists", name))
		return
	}
	// A tmux session must be managed by at most one daemon session:
	// double-attaching would mirror the same tmux session in two panels.
	if req.Type == SessionTypeTmux {
		if existing := s.sessionByTmuxTargetLocked(req.TmuxTarget); existing != nil {
			s.mu.Unlock()
			s.sendError(conn, fmt.Sprintf("tmux session %q already managed by session %q", req.TmuxTarget, existing.Name))
			return
		}
	}
	s.mu.Unlock()

	sess, err := newDaemonSession(name, req.Command, req.Cols, req.Rows, req.Dir, req.Env, s.broadcastToSubscribers)
	if err != nil {
		s.sendError(conn, fmt.Sprintf("start session: %v", err))
		return
	}

	// Set the first tab's name if provided
	if req.TabName != "" && len(sess.Tabs) > 0 {
		sess.Tabs[0].Name = req.TabName
	}

	// Apply session type metadata (e.g. from codetty up).
	if req.Type == SessionTypeTmux {
		sess.Type = SessionTypeTmux
		sess.TmuxTarget = req.TmuxTarget
	}

	s.mu.Lock()
	if _, exists := s.sessions[name]; exists {
		s.mu.Unlock()
		sess.Kill()
		s.sendError(conn, fmt.Sprintf("session %q already exists", name))
		return
	}
	if sess.Type == SessionTypeTmux {
		if existing := s.sessionByTmuxTargetLocked(sess.TmuxTarget); existing != nil {
			s.mu.Unlock()
			sess.Kill()
			s.sendError(conn, fmt.Sprintf("tmux session %q already managed by session %q", sess.TmuxTarget, existing.Name))
			return
		}
	}
	s.sessions[name] = sess
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
	for _, sess := range s.sessions {
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

	sess.KillWithTmux()
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
	if req.TmuxAttach {
		s.attachTmuxSessionForSubscriber(sub, req)
		return
	}
	if req.Tmux {
		s.createTmuxSessionForSubscriber(sub, req)
		return
	}
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

	sess, err := newDaemonSession(name, cmd, req.Cols, req.Rows, "", nil, s.broadcastToSubscribers)
	if err != nil {
		sub.SendSessionEvent(SessionEvent{Type: "error", Session: name})
		return
	}
	s.mu.Lock()
	if _, exists := s.sessions[name]; exists {
		s.mu.Unlock()
		sess.Kill()
		sub.SendSessionEvent(SessionEvent{Type: "error", Session: name})
		return
	}
	s.sessions[name] = sess
	s.mu.Unlock()
	go s.watchSession(sess)
	s.notifySubscribers(SessionEvent{Type: "created", Session: name, Info: sess.Info()})
}

func (s *Server) createTmuxSessionForSubscriber(sub *Subscriber, req CreateSessionRequest) {
	tmuxName := req.Name
	if tmuxName == "" {
		tmuxName = "codetty"
	}
	// Create the tmux session (detached)
	if err := CreateTmuxSession(tmuxName); err != nil {
		sub.SendSessionEvent(SessionEvent{Type: "error", Session: tmuxName})
		return
	}
	// Attach it as a daemon session
	displayName := "[tmux] " + tmuxName
	cmd := []string{"tmux", "attach-session", "-t", tmuxName}

	s.mu.Lock()
	if _, exists := s.sessions[displayName]; exists {
		s.mu.Unlock()
		sub.SendSessionEvent(SessionEvent{Type: "error", Session: displayName})
		return
	}
	s.mu.Unlock()

	sess, err := newDaemonSession(displayName, cmd, req.Cols, req.Rows, "", nil, s.broadcastToSubscribers)
	if err != nil {
		sub.SendSessionEvent(SessionEvent{Type: "error", Session: displayName})
		return
	}
	sess.Type = SessionTypeTmux
	sess.TmuxTarget = tmuxName

	s.mu.Lock()
	if _, exists := s.sessions[displayName]; exists {
		s.mu.Unlock()
		sess.Kill()
		sub.SendSessionEvent(SessionEvent{Type: "error", Session: displayName})
		return
	}
	s.sessions[displayName] = sess
	s.mu.Unlock()
	go s.watchSession(sess)
	s.notifySubscribers(SessionEvent{Type: "created", Session: displayName, Info: sess.Info()})
}

func (s *Server) attachTmuxSessionForSubscriber(sub *Subscriber, req CreateSessionRequest) {
	tmuxName := req.Name
	if tmuxName == "" {
		return
	}
	displayName := "[tmux] " + tmuxName
	cmd := []string{"tmux", "attach-session", "-t", tmuxName}

	s.mu.Lock()
	if _, exists := s.sessions[displayName]; exists {
		s.mu.Unlock()
		// Already managed, nothing to do
		return
	}
	if s.sessionByTmuxTargetLocked(tmuxName) != nil {
		s.mu.Unlock()
		// Already managed under another name (e.g. by codetty up)
		return
	}
	s.mu.Unlock()

	sess, err := newDaemonSession(displayName, cmd, req.Cols, req.Rows, "", nil, s.broadcastToSubscribers)
	if err != nil {
		sub.SendSessionEvent(SessionEvent{Type: "error", Session: displayName})
		return
	}
	sess.Type = SessionTypeTmux
	sess.TmuxTarget = tmuxName

	s.mu.Lock()
	if _, exists := s.sessions[displayName]; exists {
		s.mu.Unlock()
		sess.Kill()
		return
	}
	if s.sessionByTmuxTargetLocked(tmuxName) != nil {
		s.mu.Unlock()
		sess.Kill()
		return
	}
	s.sessions[displayName] = sess
	s.mu.Unlock()
	go s.watchSession(sess)
	s.notifySubscribers(SessionEvent{Type: "created", Session: displayName, Info: sess.Info()})
}

func (s *Server) killSessionForSubscriber(req KillSessionRequest) {
	s.mu.Lock()
	sess, exists := s.sessions[req.Name]
	s.mu.Unlock()
	if exists {
		if req.KillTmux {
			sess.KillWithTmux()
		} else {
			sess.Kill()
		}
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

	// For tmux sessions, also rename in tmux server
	if sess.Type == SessionTypeTmux && sess.TmuxTarget != "" {
		newTmuxName := req.NewName
		// Strip "[tmux] " prefix if present to get clean tmux name
		if len(newTmuxName) > 7 && newTmuxName[:7] == "[tmux] " {
			newTmuxName = newTmuxName[7:]
		}
		if err := RenameTmuxSession(sess.TmuxTarget, newTmuxName); err != nil {
			s.mu.Unlock()
			return
		}
		sess.TmuxTarget = newTmuxName
	}

	delete(s.sessions, req.OldName)
	sess.Name = req.NewName
	s.sessions[req.NewName] = sess
	s.mu.Unlock()

	s.notifySubscribers(SessionEvent{
		Type:    "renamed",
		Session: req.OldName,
		Info:    SessionInfo{Name: req.NewName},
	})
}

func (s *Server) handleSubscribeAll(conn net.Conn, req Request) {
	sub := newSubscriber(conn)
	s.addSubscriber(sub)
	defer s.removeSubscriber(sub)

	s.mu.Lock()
	sessions := make([]*DaemonSession, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.mu.Unlock()

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

	sub.sendFrameDirect(FrameResponse, MarshalJSON(Response{OK: true}))
	sub.activate()
	sub.Run(s)
}

func (s *Server) watchSession(sess *DaemonSession) {
	<-sess.doneCh

	notif := Notification{Event: "session_exited", Session: sess.Name, ExitCode: sess.exitCode}
	sess.mu.Lock()
	clients := append([]*Client(nil), sess.clients...)
	sess.mu.Unlock()
	for _, c := range clients {
		c.SendNotification(notif)
	}

	s.mu.Lock()
	delete(s.sessions, sess.Name)
	remaining := len(s.sessions)
	s.mu.Unlock()

	s.notifySubscribers(SessionEvent{Type: "exited", Session: sess.Name, ExitCode: sess.exitCode})

	if remaining == 0 {
		go s.Stop()
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

// sessionByTmuxTargetLocked returns the live session managing the given tmux
// session, or nil. Caller must hold s.mu.
func (s *Server) sessionByTmuxTargetLocked(tmuxTarget string) *DaemonSession {
	if tmuxTarget == "" {
		return nil
	}
	for _, sess := range s.sessions {
		if sess.Type == SessionTypeTmux && sess.TmuxTarget == tmuxTarget && !sess.isExited() {
			return sess
		}
	}
	return nil
}

// SessionNames returns session names in iteration order.
func (s *Server) SessionNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.sessions))
	for name := range s.sessions {
		names = append(names, name)
	}
	return names
}

// DiscoverAndAttachTmux discovers tmux sessions and creates DaemonSessions for them.
// It skips sessions that are already managed. Returns the number of newly attached sessions.
func (s *Server) DiscoverAndAttachTmux() int {
	tmuxSessions, err := DiscoverTmuxSessions()
	if err != nil || len(tmuxSessions) == 0 {
		return 0
	}

	s.mu.Lock()
	// Build set of already-managed tmux targets
	managed := make(map[string]bool)
	for _, sess := range s.sessions {
		if sess.Type == SessionTypeTmux {
			managed[sess.TmuxTarget] = true
		}
	}
	s.mu.Unlock()

	count := 0
	for _, tmuxName := range tmuxSessions {
		if managed[tmuxName] {
			continue
		}

		name := "[tmux] " + tmuxName
		cmd := []string{"tmux", "attach-session", "-t", tmuxName}

		sess, err := newDaemonSession(name, cmd, 80, 24, "", nil, s.broadcastToSubscribers)
		if err != nil {
			log.Printf("tmux attach %q failed: %v", tmuxName, err)
			continue
		}
		sess.Type = SessionTypeTmux
		sess.TmuxTarget = tmuxName

		s.mu.Lock()
		if _, exists := s.sessions[name]; exists || s.sessionByTmuxTargetLocked(tmuxName) != nil {
			s.mu.Unlock()
			sess.Kill()
			continue
		}
		s.sessions[name] = sess
		s.mu.Unlock()

		s.notifySubscribers(SessionEvent{Type: "created", Session: name, Info: sess.Info()})
		go s.watchSession(sess)
		count++
	}
	return count
}

func (s *Server) handleRefreshTmux(conn net.Conn) {
	n := s.DiscoverAndAttachTmux()
	WriteFrame(conn, FrameResponse, MarshalJSON(Response{OK: true, Session: fmt.Sprintf("%d new tmux sessions", n)}))
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

	// Also rename the first tab if it's still named "default" and this is the first extra tab
	sess.mu.Lock()
	if len(sess.Tabs) == 2 && sess.Tabs[0].Name == "default" {
		sess.Tabs[0].Name = sess.Tabs[0].Name // keep as-is unless renamed
	}
	sess.mu.Unlock()

	// Notify subscribers
	s.notifySubscribers(SessionEvent{
		Type:    "tab_created",
		Session: sess.Name,
		Info:    sess.Info(),
	})

	WriteFrame(conn, FrameResponse, MarshalJSON(Response{OK: true}))
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
