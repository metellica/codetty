package daemon

import (
	"fmt"
	"sync"
	"time"

	"codetty/terminal"
)

// SessionType identifies the kind of session.
const (
	SessionTypeNative = "native"
	SessionTypeTmux   = "tmux"
)

// DaemonSession represents a managed PTY session within the daemon.
type DaemonSession struct {
	Name       string
	Command    []string
	Type       string // SessionTypeNative or SessionTypeTmux
	TmuxTarget string // original tmux session name (only when Type==SessionTypeTmux)
	Terminal   terminal.Session
	Screen     *terminal.ScreenMirror
	CreatedAt  time.Time
	onOutput   func(session string, data []byte)

	mu       sync.Mutex
	clients  []*Client
	exited   bool
	exitCode int
	doneCh   chan struct{}
}

func newDaemonSession(name string, command []string, cols, rows uint16, onOutput func(session string, data []byte)) (*DaemonSession, error) {
	term, err := terminal.Start(command, cols, rows)
	if err != nil {
		return nil, err
	}

	s := &DaemonSession{
		Name:      name,
		Command:   command,
		Type:      SessionTypeNative,
		Terminal:  term,
		Screen:    terminal.NewScreenMirror(cols, rows),
		CreatedAt: time.Now(),
		onOutput:  onOutput,
		doneCh:    make(chan struct{}),
	}

	go s.readLoop()
	go s.waitLoop()

	return s, nil
}

func (s *DaemonSession) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := s.Terminal.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			s.Screen.Write(data)
			s.broadcastOutput(data)
			if s.onOutput != nil {
				broadcast := make([]byte, len(data))
				copy(broadcast, data)
				s.onOutput(s.Name, broadcast)
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *DaemonSession) waitLoop() {
	err := s.Terminal.Wait()
	s.mu.Lock()
	s.exited = true
	if err != nil {
		s.exitCode = 1
	}
	s.mu.Unlock()
	close(s.doneCh)
}

func (s *DaemonSession) broadcastOutput(data []byte) {
	s.mu.Lock()
	clients := append([]*Client(nil), s.clients...)
	s.mu.Unlock()

	for _, c := range clients {
		c.SendOutput(data)
	}
}

func (s *DaemonSession) addClient(c *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients = append(s.clients, c)
}

func (s *DaemonSession) removeClient(c *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, cl := range s.clients {
		if cl == c {
			s.clients = append(s.clients[:i], s.clients[i+1:]...)
			return
		}
	}
}

func (s *DaemonSession) clientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

func (s *DaemonSession) isExited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited
}

// Resize resizes the PTY and screen mirror.
func (s *DaemonSession) Resize(cols, rows uint16) {
	s.Terminal.Resize(cols, rows)
	s.Screen.Resize(cols, rows)
}

// Kill terminates the session's PTY process.
func (s *DaemonSession) Kill() {
	s.Terminal.Kill()
	s.Terminal.Close()
}

// Info returns a SessionInfo snapshot for listing.
func (s *DaemonSession) Info() SessionInfo {
	cmd := ""
	if len(s.Command) > 0 {
		cmd = s.Command[0]
	}
	return SessionInfo{
		Name:      s.Name,
		Command:   cmd,
		Type:      s.Type,
		Attached:  s.clientCount(),
		CreatedAt: s.CreatedAt.Format(time.RFC3339),
	}
}

// autoSessionName generates a unique session name for the given command.
func autoSessionName(command []string, existing map[string]*DaemonSession) string {
	base := "session"
	if len(command) > 0 {
		base = command[0]
	}
	for i := 0; ; i++ {
		name := fmt.Sprintf("%s-%d", base, i)
		if _, exists := existing[name]; !exists {
			return name
		}
	}
}
