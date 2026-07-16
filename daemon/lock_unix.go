//go:build !windows

package daemon

import (
	"fmt"
	"os"
	"syscall"

	"codetty/terminal"
)

// sockLock is an exclusive advisory lock guarding a daemon socket path.
// Concurrent clients (e.g. ssh bridges racing at startup) may all spawn a
// __server process; only the lock holder may unlink and bind the socket,
// the rest exit. The lock lives as long as the fd is open, so it also
// survives hot upgrade via exec when passed through prepareExec.
type sockLock struct {
	f *os.File
}

// acquireSockLock takes an exclusive non-blocking lock on sockPath+".lock".
// Fails if another daemon (or one starting up) already holds it. The lock
// file itself is never removed: unlinking it would allow two processes to
// lock different inodes of the same path.
func acquireSockLock(sockPath string) (*sockLock, error) {
	f, err := os.OpenFile(sockPath+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("daemon already running (lock %s held)", f.Name())
	}
	return &sockLock{f: f}, nil
}

// recoverSockLock wraps a lock fd inherited across a hot-upgrade exec.
func recoverSockLock(fd int) *sockLock {
	return &sockLock{f: os.NewFile(uintptr(fd), "")}
}

// prepareExec makes the lock fd survive exec and returns it, so the
// upgraded daemon keeps holding the lock with no window for a racing
// bridge to start a second daemon.
func (l *sockLock) prepareExec() int {
	fd := int(l.f.Fd())
	terminal.ClearCloseOnExec(fd)
	return fd
}

// release drops the lock. The lock file stays behind (harmless, empty).
func (l *sockLock) release() {
	l.f.Close()
}
