package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const usage = `codetty — terminal multiplexer with hub integration

Usage:
  codetty [new] [-s name] [--] [command ...]   Create a new session
  codetty attach [-s name]                     Reattach to a session (alias: a)
  codetty ls                                   List active sessions
  codetty kill [-s name]                       Kill a session
  codetty kill-server                          Stop the daemon and all sessions
  codetty hub [flags]                          Start the hub TUI
  codetty run [flags] [--] [command ...]       Legacy one-shot CLI mode

Options:
  -s, --session NAME   Target session name (default: auto-generated or latest)
  --no-hub             Do not connect to the hub
  --hub-url URL        Hub WebSocket URL
  --insecure           Skip TLS verification
  --help, -h           Show this help
`

func main() {
	if len(os.Args) < 2 {
		// Default: new session
		cmdNew(os.Args[1:])
		return
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "new":
		cmdNew(args)
	case "attach", "a":
		cmdAttach(args)
	case "ls":
		cmdList(args)
	case "kill":
		cmdKill(args)
	case "kill-server":
		cmdKillServer(args)
	case "hub":
		cmdHub(args)
	case "run":
		cmdRun(args)
	case "__server":
		cmdServer(args)
	case "--help", "-h", "help":
		fmt.Print(usage)
	case "--version", "-v":
		fmt.Println("codetty dev")
	default:
		// Treat unknown args as command to run in a new session
		cmdNew(os.Args[1:])
	}
}

// defaultSocketPath returns the Unix socket path for the daemon.
func defaultSocketPath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), fmt.Sprintf("codetty-%d", os.Getuid()))
	}
	return filepath.Join(dir, "codetty.sock")
}
