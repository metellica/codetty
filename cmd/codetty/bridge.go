package main

import (
	"io"
	"log"
	"net"
	"os"
)

// cmdBridge connects stdio to the local daemon socket. It is invoked through
// OpenSSH so the local TUI can speak the native CodeTTY protocol remotely.
func cmdBridge() {
	sockPath := defaultSocketPath()
	if err := ensureDaemon(sockPath); err != nil {
		log.Fatalf("start daemon: %v", err)
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		log.Fatalf("connect daemon: %v", err)
	}
	defer conn.Close()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(conn, os.Stdin)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(os.Stdout, conn)
		done <- struct{}{}
	}()
	<-done
}
