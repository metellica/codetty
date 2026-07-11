//go:build !windows

package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
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
func (t *unixSession) Pid() int                    { return t.cmd.Process.Pid }

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
// pid is the child process PID (used for Wait). When pid is 0 (state files
// written by older binaries), the child PID is recovered from the pty
// itself via TIOCGSID.
func RecoverSession(fd int, pid int) Session {
	// Restore close-on-exec (cleared before the upgrade exec) so the pty
	// master does not leak into child processes spawned after recovery.
	syscall.CloseOnExec(fd)
	if pid <= 0 {
		pid = ptyChildPid(fd)
	}
	file := os.NewFile(uintptr(fd), "pty-recovered")
	return &recoveredSession{file: file, pid: pid}
}

// ptyChildPid finds the direct child owning the pty via the master fd.
// TIOCGSID returns the pty's session leader — the process started on the
// pty (creack/pty uses Setsid+Setctty). Only PIDs that are direct children
// of this process are returned, so Wait4 on the result is valid.
func ptyChildPid(fd int) int {
	sid, err := unix.IoctlGetInt(fd, unix.TIOCGSID)
	if err != nil || sid <= 0 {
		return 0
	}
	if !isDirectChild(sid) {
		return 0
	}
	return sid
}

// isDirectChild reports whether pid is a direct child of this process.
func isDirectChild(pid int) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		// /proc unavailable (e.g. macOS): trust the pty session id.
		return true
	}
	// stat format: pid (comm) state ppid ... — comm may contain spaces
	// and parentheses, so parse after the last ')'.
	s := string(data)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return false
	}
	fields := strings.Fields(s[i+2:])
	if len(fields) < 2 {
		return false
	}
	ppid, err := strconv.Atoi(fields[1])
	return err == nil && ppid == os.Getpid()
}

func (r *recoveredSession) Read(p []byte) (int, error)  { return r.file.Read(p) }
func (r *recoveredSession) Write(p []byte) (int, error) { return r.file.Write(p) }
func (r *recoveredSession) Close() error                { return r.file.Close() }
func (r *recoveredSession) Fd() int                     { return int(r.file.Fd()) }
func (r *recoveredSession) Pid() int                    { return r.pid }

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
		for {
			_, err := syscall.Wait4(r.pid, &ws, 0, nil)
			if err != syscall.EINTR {
				return err
			}
		}
	}
	// No child could be identified: the pty has no live session leader,
	// so the original child is already gone. Report exit immediately so
	// the daemon respawns or closes the tab instead of hanging forever.
	return nil
}

// ClearCloseOnExec clears the close-on-exec flag for the given fd,
// so it survives across syscall.Exec.
func ClearCloseOnExec(fd int) {
	flags, _, _ := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
	syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFD, flags&^syscall.FD_CLOEXEC)
}
