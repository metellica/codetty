//go:build windows

package daemon

import (
	"fmt"
	"net"
)

// PrepareUpgrade is not supported on Windows.
func (s *Server) PrepareUpgrade() (string, error) {
	return "", fmt.Errorf("hot upgrade is not supported on Windows")
}

// Exec is not supported on Windows.
func (s *Server) Exec(newBinary, stateFile string) error {
	return fmt.Errorf("hot upgrade is not supported on Windows")
}

// RecoverFromUpgrade is not supported on Windows.
func RecoverFromUpgrade(stateFile string) (*Server, error) {
	return nil, fmt.Errorf("hot upgrade is not supported on Windows")
}

// handleUpgrade sends an error on Windows.
func (s *Server) handleUpgrade(conn net.Conn, req Request) {
	s.sendError(conn, "hot upgrade is not supported on Windows")
}
