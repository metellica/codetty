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
	// Check for upgrade mode: __server --upgrade <state-file>
	if len(args) >= 2 && args[0] == "--upgrade" {
		cmdServerUpgrade(args[1])
		return
	}

	sockPath := defaultSocketPath()
	if len(args) > 0 {
		sockPath = args[0]
	}

	srv := daemon.NewServer(sockPath)
	if err := srv.Start(); err != nil {
		log.Fatalf("daemon start: %v", err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh

	srv.Stop()
}

// cmdServerUpgrade recovers daemon state from a previous instance.
func cmdServerUpgrade(stateFile string) {
	srv, err := daemon.RecoverFromUpgrade(stateFile)
	if err != nil {
		log.Fatalf("upgrade recovery failed: %v", err)
	}

	if err := srv.Start(); err != nil {
		log.Fatalf("daemon start after upgrade: %v", err)
	}

	log.Printf("daemon upgraded successfully, recovered %d sessions", len(srv.SessionNames()))

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh

	srv.Stop()
}
