package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// cmdRun runs the legacy one-shot CLI mode (direct PTY + hub WebSocket, no daemon).
// This mode is equivalent to the old codetty-cli behavior for backwards compatibility.
func cmdRun(args []string) {
	// Try to find codetty-cli alongside the current binary
	self, _ := os.Executable()
	candidates := []string{
		filepath.Join(filepath.Dir(self), "codetty-cli"),
		"codetty-cli",
	}
	for _, bin := range candidates {
		if p, err := exec.LookPath(bin); err == nil {
			cmd := exec.Command(p, args...)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				os.Exit(1)
			}
			return
		}
	}
	fmt.Fprintf(os.Stderr, "codetty run: legacy mode requires codetty-cli binary\n")
	fmt.Fprintf(os.Stderr, "  build it with: go build -o codetty-cli ./cmd/codetty-cli\n")
	fmt.Fprintf(os.Stderr, "  or use daemon mode: codetty new %s\n", strings.Join(args, " "))
	os.Exit(1)
}
