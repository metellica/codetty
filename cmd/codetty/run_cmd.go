package main

import (
	"fmt"
	"os"
)

// cmdRun runs the legacy one-shot CLI mode (codetty-cli behavior).
// TODO: embed CLI logic directly once cmd/codetty-cli is refactored into a library.
func cmdRun(args []string) {
	fmt.Fprintf(os.Stderr, "run subcommand not yet embedded — build and use codetty-cli directly:\n")
	fmt.Fprintf(os.Stderr, "  go build -o codetty-cli ./cmd/codetty-cli && ./codetty-cli %s\n", formatArgs(args))
	os.Exit(1)
}
