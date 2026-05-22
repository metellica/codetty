package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// cmdHub starts the hub TUI by exec'ing codetty-hub.
// In the future, this may be embedded directly.
func cmdHub(args []string) {
	hubBin, err := exec.LookPath("codetty-hub")
	if err != nil {
		fmt.Fprintf(os.Stderr, "codetty-hub not found in PATH\n")
		os.Exit(1)
	}
	argv := append([]string{hubBin}, args...)
	if err := syscall.Exec(hubBin, argv, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "exec codetty-hub: %v\n", err)
		os.Exit(1)
	}
}
