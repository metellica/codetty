package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"codetty/daemon"
)

// cmdServer is the internal daemon entry point (__server subcommand).
// It runs the daemon server in the foreground, listening on the Unix socket.
func cmdServer(args []string) {
	sockPath := defaultSocketPath()
	if len(args) > 0 {
		sockPath = args[0]
	}

	srv := daemon.NewServer(sockPath)
	if err := srv.Start(); err != nil {
		log.Fatalf("daemon start: %v", err)
	}

	// Auto-discover tmux sessions on startup
	srv.DiscoverAndAttachTmux()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh

	srv.Stop()
}
