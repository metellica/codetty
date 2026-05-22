package main

import (
	"fmt"
	"os"
)

// cmdHub starts the hub TUI.
// TODO: embed hub logic directly once cmd/codetty-hub is refactored into a library.
func cmdHub(args []string) {
	fmt.Fprintf(os.Stderr, "hub subcommand not yet embedded — build and use codetty-hub directly:\n")
	fmt.Fprintf(os.Stderr, "  go build -o codetty-hub ./cmd/codetty-hub && ./codetty-hub %s\n", formatArgs(args))
	os.Exit(1)
}

func formatArgs(args []string) string {
	s := ""
	for _, a := range args {
		s += " " + a
	}
	return s
}
