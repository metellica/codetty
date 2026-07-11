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
	// Fd returns the PTY file descriptor number, or -1 if not available.
	Fd() int
}

// Start creates a new PTY session running the given command.
// If dir is non-empty, the process starts in that directory.
// env entries ("KEY=value") are appended to the inherited environment.
func Start(args []string, cols, rows uint16, dir string, env []string) (Session, error) {
	if len(args) == 0 {
		return nil, ErrNoCommand
	}
	return startPlatform(args, cols, rows, dir, env)
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
