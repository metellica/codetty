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
	enablePersistence(srv, true)
	if err := srv.Start(); err != nil {
		log.Fatalf("daemon start: %v", err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	select {
	case <-sigCh:
		// Keep the saved layout: this is how a reboot stops the daemon.
		srv.StopPreserving()
	case <-srv.Done():
		// Stopped internally (kill-server or auto-exit): just exit.
	}
}

// cmdServerUpgrade recovers daemon state from a previous instance.
func cmdServerUpgrade(stateFile string) {
	srv, err := daemon.RecoverFromUpgrade(stateFile)
	if err != nil {
		log.Fatalf("upgrade recovery failed: %v", err)
	}

	enablePersistence(srv, false)
	if err := srv.Start(); err != nil {
		log.Fatalf("daemon start after upgrade: %v", err)
	}

	log.Printf("daemon upgraded successfully, recovered %d sessions", srv.SessionCount())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	select {
	case <-sigCh:
		srv.StopPreserving()
	case <-srv.Done():
		// Stopped internally (kill-server or auto-exit): just exit.
	}
}

// enablePersistence makes the daemon save its session/tab layout in the
// instance's state directory and, when restore is set, recreate the saved
// layout at start (a fresh daemon after reboot; a hot-upgraded daemon
// already inherited its live sessions).
func enablePersistence(srv *daemon.Server, restore bool) {
	if path := sessionStatePath(); path != "" {
		srv.EnablePersistence(path, restore)
	}
}
