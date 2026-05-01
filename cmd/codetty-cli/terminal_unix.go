//go:build !windows

package main

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type unixTerminal struct {
	file *os.File
	cmd  *exec.Cmd
}

func startPlatformTerminal(args []string, cols, rows uint16) (terminalSession, error) {
	cmd := exec.Command(args[0], args[1:]...)
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	return &unixTerminal{file: file, cmd: cmd}, nil
}

func (t *unixTerminal) Read(p []byte) (int, error) {
	return t.file.Read(p)
}

func (t *unixTerminal) Write(p []byte) (int, error) {
	return t.file.Write(p)
}

func (t *unixTerminal) Close() error {
	return t.file.Close()
}

func (t *unixTerminal) Resize(cols, rows uint16) error {
	return pty.Setsize(t.file, &pty.Winsize{Cols: cols, Rows: rows})
}

func (t *unixTerminal) Wait() error {
	return t.cmd.Wait()
}
