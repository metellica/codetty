//go:build !windows

package main

import (
	"strings"
	"testing"
	"time"
)

func TestStartTerminalRunsCommandThroughPTY(t *testing.T) {
	term, err := startTerminal([]string{"sh", "-c", "printf codetty"}, 80, 24)
	if err != nil {
		t.Fatalf("startTerminal: %v", err)
	}
	defer term.Close()

	type readResult struct {
		data string
		err  error
	}
	readCh := make(chan readResult, 1)
	go func() {
		buf := make([]byte, 128)
		n, err := term.Read(buf)
		readCh <- readResult{data: string(buf[:n]), err: err}
	}()

	select {
	case got := <-readCh:
		if got.err != nil && got.data == "" {
			t.Fatalf("read terminal output: %v", got.err)
		}
		if !strings.Contains(got.data, "codetty") {
			t.Fatalf("terminal output = %q, want substring %q", got.data, "codetty")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out reading terminal output")
	}

	if err := term.Wait(); err != nil {
		t.Fatalf("wait terminal command: %v", err)
	}
}
