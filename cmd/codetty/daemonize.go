package main

import (
	"fmt"
	"net"
	"os"
	"time"
)

// ensureDaemon ensures the daemon server is running. If not, starts it.
// Returns when the daemon is ready to accept connections.
func ensureDaemon(sockPath string) error {
	// Quick check: can we connect?
	if isReachable(sockPath) {
		return nil
	}

	// Ensure socket directory exists
	dir := sockPath[:len(sockPath)-len("/codetty.sock")]
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	// Start daemon
	if err := startDaemonProcess(sockPath); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}

	// Wait for daemon to be ready
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if isReachable(sockPath) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not start within 5s")
}

func isReachable(sockPath string) bool {
	conn, err := net.DialTimeout("unix", sockPath, 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
