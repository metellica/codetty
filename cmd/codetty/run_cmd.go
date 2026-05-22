package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// cmdRun runs the legacy one-shot CLI mode (codetty-cli behavior).
// It exec's codetty-cli for backwards compatibility.
func cmdRun(args []string) {
	cliBin, err := exec.LookPath("codetty-cli")
	if err != nil {
		fmt.Fprintf(os.Stderr, "codetty-cli not found in PATH (legacy run mode requires codetty-cli)\n")
		os.Exit(1)
	}
	argv := append([]string{cliBin}, args...)
	if err := syscall.Exec(cliBin, argv, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "exec codetty-cli: %v\n", err)
		os.Exit(1)
	}
}
