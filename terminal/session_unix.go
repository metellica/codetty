//go:build !windows

package terminal

import (
	"os"
	"os/exec"
	"syscall"

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
func (t *unixSession) Fd() int                     { return int(t.file.Fd()) }

func (t *unixSession) Resize(cols, rows uint16) error {
	return pty.Setsize(t.file, &pty.Winsize{Cols: cols, Rows: rows})
}

func (t *unixSession) Wait() error { return t.cmd.Wait() }

// recoveredSession wraps a PTY fd inherited from a previous daemon process.
// It supports I/O and resize, but cannot Kill/Wait (no *exec.Cmd).
type recoveredSession struct {
	file *os.File
	pid  int // original child PID, for re-adoption via Wait
}

// RecoverSession wraps an inherited file descriptor as a Session.
// pid is the child process PID (used for Wait).
func RecoverSession(fd int, pid int) Session {
	file := os.NewFile(uintptr(fd), "pty-recovered")
	return &recoveredSession{file: file, pid: pid}
}

func (r *recoveredSession) Read(p []byte) (int, error)  { return r.file.Read(p) }
func (r *recoveredSession) Write(p []byte) (int, error) { return r.file.Write(p) }
func (r *recoveredSession) Close() error                { return r.file.Close() }
func (r *recoveredSession) Fd() int                     { return int(r.file.Fd()) }

func (r *recoveredSession) Resize(cols, rows uint16) error {
	return pty.Setsize(r.file, &pty.Winsize{Cols: cols, Rows: rows})
}

func (r *recoveredSession) Kill() error {
	if r.pid > 0 {
		return syscall.Kill(r.pid, syscall.SIGKILL)
	}
	return nil
}

func (r *recoveredSession) Wait() error {
	if r.pid > 0 {
		var ws syscall.WaitStatus
		_, err := syscall.Wait4(r.pid, &ws, 0, nil)
		return err
	}
	// Block forever (the child is still running, we just don't have a handle)
	select {}
}

// ClearCloseOnExec clears the close-on-exec flag for the given fd,
// so it survives across syscall.Exec.
func ClearCloseOnExec(fd int) {
	syscall.CloseOnExec(fd) // this actually SETS it, we need to clear
	// Use raw fcntl to clear FD_CLOEXEC
	flags, _, _ := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
	syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFD, flags&^syscall.FD_CLOEXEC)
}
