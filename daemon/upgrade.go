//go:build !windows

package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"codetty/terminal"
)

// UpgradeState holds the serialized daemon state for hot restart.
type UpgradeState struct {
	SocketPath string              `json:"socket_path"`
	Sessions   []UpgradeSession    `json:"sessions"`
}

// UpgradeSession holds the state of a single session for hot restart.
type UpgradeSession struct {
	Name       string            `json:"name"`
	Command    []string          `json:"command"`
	Type       string            `json:"type"`
	TmuxTarget string            `json:"tmux_target,omitempty"`
	CreatedAt  string            `json:"created_at"`
	Tabs       []UpgradeTab      `json:"tabs"`
	ActiveTab  int               `json:"active_tab"`
}

// UpgradeTab holds the state of a single tab for hot restart.
type UpgradeTab struct {
	Name    string `json:"name"`
	Command []string `json:"command"`
	Fd      int    `json:"fd"`
	Pid     int    `json:"pid"`
	Cols    uint16 `json:"cols"`
	Rows    uint16 `json:"rows"`
}

// PrepareUpgrade serializes the current daemon state and prepares fds for exec.
// Returns the path to the state file.
func (s *Server) PrepareUpgrade() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := UpgradeState{
		SocketPath: s.sockPath,
	}

	for _, sess := range s.sessions {
		us := UpgradeSession{
			Name:       sess.Name,
			Command:    sess.Command,
			Type:       sess.Type,
			TmuxTarget: sess.TmuxTarget,
			CreatedAt:  sess.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			ActiveTab:  sess.ActiveTab,
		}

		for _, tab := range sess.Tabs {
			fd := tab.Terminal.Fd()
			if fd < 0 {
				return "", fmt.Errorf("session %q tab %q: cannot get fd", sess.Name, tab.Name)
			}

			// Get the child PID for recovery
			pid := 0
			if tab.Terminal.Fd() >= 0 {
				// Try to get PID from /proc/self/fd -> pty -> child
				// For exec.Cmd-based sessions, we can get it from the process
				pid = getPidFromFd(fd)
			}

			cols, rows := tab.Screen.Size()

			// Clear close-on-exec so fd survives exec
			terminal.ClearCloseOnExec(fd)

			us.Tabs = append(us.Tabs, UpgradeTab{
				Name:    tab.Name,
				Command: tab.Command,
				Fd:      fd,
				Pid:     pid,
				Cols:    cols,
				Rows:    rows,
			})
		}

		state.Sessions = append(state.Sessions, us)
	}

	// Write state to temp file
	stateFile := filepath.Join(os.TempDir(), fmt.Sprintf("codetty-upgrade-%d.json", os.Getpid()))
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal state: %w", err)
	}
	if err := os.WriteFile(stateFile, data, 0600); err != nil {
		return "", fmt.Errorf("write state file: %w", err)
	}

	return stateFile, nil
}

// Exec replaces the current process with the new binary.
func (s *Server) Exec(newBinary, stateFile string) error {
	// Close the listener so the new process can bind
	if s.listener != nil {
		s.listener.Close()
	}

	// Close all subscriber connections (TUI clients will reconnect)
	s.mu.Lock()
	for _, sub := range s.subscribers {
		sub.Close()
	}
	s.subscribers = nil
	s.mu.Unlock()

	// Exec the new binary with the upgrade state file
	args := []string{newBinary, "__server", "--upgrade", stateFile}
	env := os.Environ()

	return syscall.Exec(newBinary, args, env)
}

// RecoverFromUpgrade restores daemon state from an upgrade state file.
func RecoverFromUpgrade(stateFile string) (*Server, error) {
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("read state file: %w", err)
	}

	var state UpgradeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}

	srv := NewServer(state.SocketPath)

	for _, us := range state.Sessions {
		sess := &DaemonSession{
			Name:       us.Name,
			Command:    us.Command,
			Type:       us.Type,
			TmuxTarget: us.TmuxTarget,
			ActiveTab:  us.ActiveTab,
			onOutput:   srv.broadcastToSubscribers,
			doneCh:     make(chan struct{}),
		}

		// Parse creation time
		if t, err := parseTime(us.CreatedAt); err == nil {
			sess.CreatedAt = t
		}

		for _, ut := range us.Tabs {
			term := terminal.RecoverSession(ut.Fd, ut.Pid)
			screen := terminal.NewScreenMirror(ut.Cols, ut.Rows)

			tab := &SessionTab{
				Name:     ut.Name,
				Command:  ut.Command,
				Terminal: term,
				Screen:   screen,
				doneCh:   make(chan struct{}),
			}

			sess.Tabs = append(sess.Tabs, tab)

			// Start read/wait loops for recovered tabs
			go sess.readLoopForTab(tab, term)
			go sess.waitLoopForTab(tab, term)
		}

		if len(sess.Tabs) > 0 {
			if sess.ActiveTab >= len(sess.Tabs) {
				sess.ActiveTab = 0
			}
			sess.Terminal = sess.Tabs[sess.ActiveTab].Terminal
			sess.Screen = sess.Tabs[sess.ActiveTab].Screen
		}

		srv.sessions[us.Name] = sess
		go srv.watchSession(sess)
	}

	// Clean up state file
	os.Remove(stateFile)

	return srv, nil
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

// handleUpgrade performs a hot upgrade of the daemon binary.
func (s *Server) handleUpgrade(conn net.Conn, req Request) {
	newBinary := req.Session // reuse Session field for binary path
	if newBinary == "" {
		// Default: use the current executable path
		exe, err := os.Executable()
		if err != nil {
			s.sendError(conn, fmt.Sprintf("cannot determine executable: %v", err))
			return
		}
		newBinary = exe
	}

	// Verify new binary exists and is executable
	if _, err := os.Stat(newBinary); err != nil {
		s.sendError(conn, fmt.Sprintf("binary not found: %v", err))
		return
	}

	// Prepare upgrade state
	stateFile, err := s.PrepareUpgrade()
	if err != nil {
		s.sendError(conn, fmt.Sprintf("prepare upgrade: %v", err))
		return
	}

	// Send success response before exec (client will reconnect)
	WriteFrame(conn, FrameResponse, MarshalJSON(Response{OK: true, Session: "upgrading"}))
	conn.Close()

	// Exec the new binary
	log.Printf("upgrading daemon: %s -> %s (state: %s)", os.Args[0], newBinary, stateFile)
	if err := s.Exec(newBinary, stateFile); err != nil {
		log.Fatalf("exec failed: %v", err)
	}
}

// getPidFromFd attempts to find the child process PID for a given pty fd.
// This is best-effort — uses /proc on Linux.
func getPidFromFd(fd int) int {
	// Read the pts number from the fd's symlink in /proc
	link, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", fd))
	if err != nil {
		return 0
	}

	// Find processes whose controlling terminal matches
	// This is a simplified approach — scan /proc/*/stat
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Check if it's a PID
		pid := 0
		if _, err := fmt.Sscanf(entry.Name(), "%d", &pid); err != nil {
			continue
		}
		if pid == os.Getpid() {
			continue
		}
		// Check fd 0 of that process
		childFd0, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/0", pid))
		if err != nil {
			continue
		}
		if childFd0 == link {
			return pid
		}
	}
	return 0
}
