//go:build windows

package terminal

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

type windowsSession struct {
	pty *conPty

	// Mouse workaround for conhost builds < 22621, which drop VT mouse
	// sequences written to the ConPTY input pipe: intercept them and
	// inject native MOUSE_EVENT records instead (unless the child console
	// runs in VT-input mode, where raw bytes reach the app verbatim).
	filterMouse bool
	filter      mouseFilter
	injector    mouseInjector
}

func startPlatform(args []string, cols, rows uint16, dir string, env []string) (Session, error) {
	commandLine, err := windowsCommandLine(args)
	if err != nil {
		return nil, err
	}
	cpty, err := startConPty(commandLine, cols, rows, dir, ptyEnviron(env))
	if err != nil {
		return nil, fmt.Errorf("start conpty: %w", err)
	}
	return &windowsSession{
		pty:         cpty,
		filterMouse: !conptyHasPassthrough(),
	}, nil
}

func windowsCommandLine(args []string) (string, error) {
	for _, arg := range args {
		if strings.ContainsRune(arg, '\x00') {
			return "", fmt.Errorf("command argument contains NUL byte")
		}
	}
	return windows.ComposeCommandLine(args), nil
}

func (t *windowsSession) Read(p []byte) (int, error) { return t.pty.Read(p) }

func (t *windowsSession) Write(p []byte) (int, error) {
	if !t.filterMouse {
		return t.pty.Write(p)
	}
	for _, seg := range t.filter.feed(p) {
		if seg.ev == nil {
			if _, err := t.pty.Write(seg.data); err != nil {
				return len(p), err
			}
			continue
		}
		if t.injector.childVTI(t.pty.pi.ProcessId) {
			// VT-input child (crossterm/ratatui style TUI): the raw
			// sequence reaches the app through the pipe.
			if _, err := t.pty.Write(seg.ev.raw); err != nil {
				return len(p), err
			}
			continue
		}
		if rec, ok := t.injector.record(seg.ev); ok {
			injectMouseRecords(t.pty.pi.ProcessId, []inputRecord{rec})
		}
	}
	return len(p), nil
}

func (t *windowsSession) Close() error { return t.pty.Close() }
func (t *windowsSession) Kill() error  { return t.pty.Kill() }
func (t *windowsSession) Fd() int      { return -1 }
func (t *windowsSession) Pid() int     { return t.pty.Pid() }

func (t *windowsSession) Resize(cols, rows uint16) error {
	return t.pty.Resize(cols, rows)
}

func (t *windowsSession) Wait() error { return t.pty.Wait() }
