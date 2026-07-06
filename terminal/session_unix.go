//go:build !windows

package terminal

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type unixSession struct {
	file *os.File
	cmd  *exec.Cmd
}

func startPlatform(args []string, cols, rows uint16, dir string, env []string) (Session, error) {
	cmd := exec.Command(args[0], args[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	return &unixSession{file: file, cmd: cmd}, nil
}

func (t *unixSession) Read(p []byte) (int, error)  { return t.file.Read(p) }
func (t *unixSession) Write(p []byte) (int, error) { return t.file.Write(p) }
func (t *unixSession) Close() error                { return t.file.Close() }
func (t *unixSession) Kill() error                 { return t.cmd.Process.Kill() }

func (t *unixSession) Resize(cols, rows uint16) error {
	return pty.Setsize(t.file, &pty.Winsize{Cols: cols, Rows: rows})
}

func (t *unixSession) Wait() error { return t.cmd.Wait() }
