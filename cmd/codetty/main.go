package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"codetty/terminal"
)

const usage = `codetty — terminal multiplexer

Usage:
  codetty                                      Open TUI (auto-create shell if no sessions)
  codetty [new] [-s name] [--] [command ...]   Create session and open TUI
  codetty attach [-s name]                     Open TUI focused on session (alias: a)
  codetty ls                                   List active sessions
  codetty kill [-s name]                       Kill a session
  codetty kill-server                          Stop the daemon and all sessions

TUI Keys (Ctrl+A prefix):
  d       Detach (exit TUI, sessions keep running)
  x       Kill focused session
  c       Create new session
  r       Rename focused session
  t       Discover/attach tmux sessions
  w       Session manager (hide/show/kill)
  n/p     Next/previous session
  f       Toggle fullscreen (no borders/status)
  g       Toggle grid view
  b       Focus-big mode
  a       Auto grid mode
  h/l     Move session order left/right
  1-9     Jump to session by number

Other keys:
  Shift+Tab           Switch to next session
  Mouse click         Select session panel

Options:
  -s, --session NAME   Target session name
  --help, -h           Show this help
`

func main() {
	if len(os.Args) < 2 {
		cmdDefault()
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

func cmdDefault() {
	sockPath := defaultSocketPath()
	if err := ensureDaemon(sockPath); err != nil {
		log.Fatalf("cannot start daemon: %v", err)
	}
	startTUI(sockPath, "", []string{terminal.DefaultShell()})
}

// defaultSocketPath returns the Unix socket path for the daemon.
func defaultSocketPath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), fmt.Sprintf("codetty-%d", os.Getuid()))
	}
	return filepath.Join(dir, "codetty.sock")
}
