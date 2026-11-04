package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"codetty/terminal"
)

// Persistence: the daemon periodically snapshots its session/tab layout to a
// state file owned by one isolation unit (a daemon instance; the owner
// namespaces served by that daemon are recorded inside it). After a machine
// reboot, a freshly started daemon of the same instance recreates every
// session and tab from the file. Only the layout comes back: each tab gets
// the default shell in its last known working directory, never the command
// it was running (nor its screen contents or environment).
//
// Writes come solely from the periodic loop. A shutdown triggered by the OS
// (StopPreserving) freezes the file so that the shells dying with the daemon
// are not recorded as deliberately closed; every other Stop (kill-server,
// last session exiting) means the user wants nothing back and removes it.

const (
	stateVersion = 1
	// persistInterval is how often the layout is snapshotted; it bounds how
	// much recent change a crash or power loss can lose.
	persistInterval = time.Second
)

type persistedState struct {
	Version  int                `json:"version"`
	Actives  map[string]string  `json:"actives,omitempty"`
	Sessions []persistedSession `json:"sessions"` // grouped by owner, in display order
}

type persistedSession struct {
	Name      string         `json:"name"`
	Owner     string         `json:"owner,omitempty"`
	CreatedAt string         `json:"created_at,omitempty"`
	ActiveTab int            `json:"active_tab"`
	Tabs      []persistedTab `json:"tabs"`
}

type persistedTab struct {
	Name      string `json:"name"`
	OrigName  string `json:"orig_name,omitempty"`
	Dir       string `json:"dir,omitempty"` // tab's configured/inherited dir
	Cwd       string `json:"cwd,omitempty"` // live working directory when saved
	Workspace bool   `json:"workspace,omitempty"`
	Cols      uint16 `json:"cols"`
	Rows      uint16 `json:"rows"`
}

// EnablePersistence turns on state persistence to path. With restore set,
// Start recreates the saved sessions before accepting clients. Must be
// called before Start.
func (s *Server) EnablePersistence(path string, restore bool) {
	s.statePath = path
	s.restoreState = restore
}

// StopPreserving shuts the daemon down like Stop but keeps the state file
// as last saved, so the layout is restored on the next start. Use for
// OS-initiated shutdowns (SIGTERM/SIGINT).
func (s *Server) StopPreserving() {
	s.persistMu.Lock()
	s.preserveState = true
	s.persistMu.Unlock()
	s.Stop()
}

func (s *Server) persistLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(persistInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.saveState()
		}
	}
}

// saveState writes the current layout when it changed since the last write.
func (s *Server) saveState() {
	state := s.snapshotState()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}

	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	if s.preserveState || bytes.Equal(data, s.lastState) {
		return
	}
	if len(state.Sessions) == 0 {
		// Nothing left to restore.
		os.Remove(s.statePath)
		s.lastState = data
		return
	}
	if err := writeStateFile(s.statePath, data); err != nil {
		log.Printf("persist state: %v", err)
		return
	}
	s.lastState = data
}

// snapshotState captures the layout of every owner namespace.
func (s *Server) snapshotState() persistedState {
	type tabRef struct {
		tab persistedTab
		pid int
	}
	state := persistedState{Version: stateVersion, Actives: map[string]string{}}
	var refs [][]tabRef

	s.mu.Lock()
	for owner, name := range s.actives {
		if _, ok := s.sessions[ownerKey(owner, name)]; ok {
			state.Actives[owner] = name
		}
	}
	for _, owner := range s.ownersLocked() {
		for _, sess := range s.orderedSessionsLocked(owner) {
			sess.mu.Lock()
			ps := persistedSession{
				Name:      sess.Name,
				Owner:     sess.Owner,
				CreatedAt: sess.CreatedAt.Format(time.RFC3339),
				ActiveTab: sess.ActiveTab,
			}
			var tabs []tabRef
			for _, tab := range sess.Tabs {
				cols, rows := tab.Screen.Size()
				pid := 0
				if tab.Terminal != nil {
					pid = tab.Terminal.Pid()
				}
				tabs = append(tabs, tabRef{
					tab: persistedTab{
						Name:      tab.Name,
						OrigName:  tab.origName,
						Dir:       tab.Dir,
						Workspace: tab.workspace,
						Cols:      cols,
						Rows:      rows,
					},
					pid: pid,
				})
			}
			sess.mu.Unlock()
			if len(tabs) == 0 {
				continue
			}
			state.Sessions = append(state.Sessions, ps)
			refs = append(refs, tabs)
		}
	}
	s.mu.Unlock()

	// Resolve live working directories outside the locks.
	for i, tabs := range refs {
		for _, r := range tabs {
			r.tab.Cwd = terminal.ProcessCwd(r.pid)
			state.Sessions[i].Tabs = append(state.Sessions[i].Tabs, r.tab)
		}
	}
	if len(state.Actives) == 0 {
		state.Actives = nil
	}
	return state
}

// writeStateFile atomically replaces the state file.
func writeStateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// restoreSessions recreates the sessions recorded in the state file. It
// runs from Start, before the listener is bound.
func (s *Server) restoreSessions() {
	data, err := os.ReadFile(s.statePath)
	if err != nil {
		return
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil || state.Version != stateVersion {
		log.Printf("restore state: ignoring unreadable %s", s.statePath)
		return
	}

	restored := 0
	for _, ps := range state.Sessions {
		owner := s.normalizeOwner(ps.Owner)
		if _, exists := s.sessions[ownerKey(owner, ps.Name)]; exists {
			continue
		}
		sess, err := newRestoredSession(owner, ps, s.ownerBroadcast(owner))
		if err != nil {
			log.Printf("restore session %q: %v", ps.Name, err)
			continue
		}
		s.mu.Lock()
		s.addSessionLocked(sess)
		s.mu.Unlock()
		go s.watchSession(sess)
		restored++
	}
	for owner, name := range state.Actives {
		owner = s.normalizeOwner(owner)
		if _, ok := s.sessions[ownerKey(owner, name)]; ok {
			s.actives[owner] = name
		}
	}
	if restored > 0 {
		log.Printf("restored %d sessions from %s", restored, s.statePath)
	}
}

// newRestoredSession rebuilds a session from its saved layout, starting the
// default shell in every tab.
func newRestoredSession(owner string, ps persistedSession, onOutput func(session string, data []byte)) (*DaemonSession, error) {
	shell := []string{terminal.DefaultShell()}
	sess := &DaemonSession{
		Name:      ps.Name,
		Owner:     owner,
		Command:   shell,
		Type:      SessionTypeNative,
		CreatedAt: time.Now(),
		onOutput:  onOutput,
		doneCh:    make(chan struct{}),
	}
	if t, err := time.Parse(time.RFC3339, ps.CreatedAt); err == nil {
		sess.CreatedAt = t
	}

	for _, pt := range ps.Tabs {
		cols, rows := pt.Cols, pt.Rows
		if cols == 0 || rows == 0 {
			cols, rows = 80, 24
		}
		term, err := startInFirstUsableDir(shell, cols, rows, pt.Cwd, pt.Dir)
		if err != nil {
			sess.Kill()
			return nil, fmt.Errorf("tab %q: %w", pt.Name, err)
		}
		sess.Tabs = append(sess.Tabs, &SessionTab{
			Name:      pt.Name,
			origName:  pt.OrigName,
			Command:   shell,
			Dir:       pt.Dir,
			workspace: pt.Workspace,
			Terminal:  term,
			Screen:    terminal.NewScreenMirror(cols, rows),
			doneCh:    make(chan struct{}),
		})
	}
	if len(sess.Tabs) == 0 {
		return nil, fmt.Errorf("no tabs")
	}
	if ps.ActiveTab >= 0 && ps.ActiveTab < len(sess.Tabs) {
		sess.ActiveTab = ps.ActiveTab
	}
	sess.Terminal = sess.Tabs[sess.ActiveTab].Terminal
	sess.Screen = sess.Tabs[sess.ActiveTab].Screen

	// Start the loops only after every tab is registered (see
	// RecoverFromUpgrade).
	for _, tab := range sess.Tabs {
		go sess.readLoopForTab(tab, tab.Terminal)
		go sess.waitLoopForTab(tab, tab.Terminal)
	}
	return sess, nil
}

// startInFirstUsableDir starts command in the first directory that exists,
// falling back to the daemon's own cwd so a deleted directory never loses
// the tab.
func startInFirstUsableDir(command []string, cols, rows uint16, dirs ...string) (terminal.Session, error) {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			continue
		}
		if term, err := terminal.Start(command, cols, rows, dir, nil); err == nil {
			return term, nil
		}
	}
	return terminal.Start(command, cols, rows, "", nil)
}
