package terminal

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
)

// ErrNoCommand is returned when Start is called with an empty args slice.
var ErrNoCommand = errors.New("no command specified")

// Session abstracts a PTY-backed process.
type Session interface {
	io.ReadWriteCloser
	Resize(cols, rows uint16) error
	Wait() error
	// Kill sends SIGKILL (or equivalent) to the process.
	Kill() error
}

// Start creates a new PTY session running the given command.
func Start(args []string, cols, rows uint16) (Session, error) {
	if len(args) == 0 {
		return nil, ErrNoCommand
	}
	return startPlatform(args, cols, rows)
}

// DefaultShell returns the user's default shell command.
func DefaultShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	if runtime.GOOS == "windows" {
		if ps, err := exec.LookPath("powershell.exe"); err == nil {
			return ps
		}
		return "cmd.exe"
	}
	return "/bin/sh"
}
