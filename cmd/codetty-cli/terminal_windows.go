//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/UserExistsError/conpty"
	"golang.org/x/sys/windows"
)

type windowsTerminal struct {
	pty *conpty.ConPty
}

func startPlatformTerminal(args []string, cols, rows uint16) (terminalSession, error) {
	commandLine, err := windowsCommandLine(args)
	if err != nil {
		return nil, err
	}

	cpty, err := conpty.Start(
		commandLine,
		conpty.ConPtyDimensions(int(cols), int(rows)),
		conpty.ConPtyEnv(os.Environ()),
	)
	if err != nil {
		if errors.Is(err, conpty.ErrConPtyUnsupported) {
			return nil, fmt.Errorf("windows ConPTY requires Windows 10 version 1809 or later: %w", err)
		}
		return nil, err
	}
	return &windowsTerminal{pty: cpty}, nil
}

func windowsCommandLine(args []string) (string, error) {
	for _, arg := range args {
		if strings.ContainsRune(arg, '\x00') {
			return "", fmt.Errorf("command argument contains NUL byte")
		}
	}
	return windows.ComposeCommandLine(args), nil
}

func (t *windowsTerminal) Read(p []byte) (int, error) {
	return t.pty.Read(p)
}

func (t *windowsTerminal) Write(p []byte) (int, error) {
	return t.pty.Write(p)
}

func (t *windowsTerminal) Close() error {
	return t.pty.Close()
}

func (t *windowsTerminal) Resize(cols, rows uint16) error {
	return t.pty.Resize(int(cols), int(rows))
}

func (t *windowsTerminal) Wait() error {
	exitCode, err := t.pty.Wait(context.Background())
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("process exited with code %d", exitCode)
	}
	return nil
}
