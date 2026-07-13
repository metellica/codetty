//go:build windows

package terminal

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/UserExistsError/conpty"
	"golang.org/x/sys/windows"
)

type windowsSession struct {
	pty *conpty.ConPty
}

func startPlatform(args []string, cols, rows uint16, dir string, env []string) (Session, error) {
	commandLine, err := windowsCommandLine(args)
	if err != nil {
		return nil, err
	}

	opts := []conpty.ConPtyOption{
		conpty.ConPtyDimensions(int(cols), int(rows)),
		conpty.ConPtyEnv(append(os.Environ(), env...)),
	}
	if dir != "" {
		opts = append(opts, conpty.ConPtyWorkDir(dir))
	}

	cpty, err := conpty.Start(commandLine, opts...)
	if err != nil {
		return nil, fmt.Errorf("start conpty: %w", err)
	}
	return &windowsSession{pty: cpty}, nil
}

// ProcessCwd is not supported on Windows; callers fall back to the
// tab's recorded directory.
func ProcessCwd(pid int) string { return "" }

func windowsCommandLine(args []string) (string, error) {
	for _, arg := range args {
		if strings.ContainsRune(arg, '\x00') {
			return "", fmt.Errorf("command argument contains NUL byte")
		}
	}
	return windows.ComposeCommandLine(args), nil
}

func (t *windowsSession) Read(p []byte) (int, error)  { return t.pty.Read(p) }
func (t *windowsSession) Write(p []byte) (int, error) { return t.pty.Write(p) }
func (t *windowsSession) Close() error                { return t.pty.Close() }
func (t *windowsSession) Kill() error                 { return t.pty.Close() }
func (t *windowsSession) Fd() int                     { return -1 }
func (t *windowsSession) Pid() int                    { return t.pty.Pid() }

func (t *windowsSession) Resize(cols, rows uint16) error {
	return t.pty.Resize(int(cols), int(rows))
}

func (t *windowsSession) Wait() error {
	exitCode, err := t.pty.Wait(context.Background())
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("process exited with code %d", exitCode)
	}
	return nil
}
