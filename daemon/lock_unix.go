//go:build !windows

package daemon

import (
	"fmt"
	"syscall"

	"codetty/terminal"
)

// sockLock is an exclusive advisory lock guarding a daemon socket path.
// Concurrent clients (e.g. ssh bridges racing at startup) may all spawn a
// __server process; only the lock holder may unlink and bind the socket,
// the rest exit. The lock lives as long as the fd is open, so it also
// survives hot upgrade via exec when passed through prepareExec.
//
// The fd is held raw (not wrapped in os.File): an os.File finalizer would
// silently close a lock fd shared across an upgrade handoff, and the
// recycled fd number would later be double-closed under someone else.
type sockLock struct {
	fd int
}

// acquireSockLock takes an exclusive non-blocking lock on sockPath+".lock".
// Fails if another daemon (or one starting up) already holds it. The lock
// file itself is never removed: unlinking it would allow two processes to
// lock different inodes of the same path.
func acquireSockLock(sockPath string) (*sockLock, error) {
	path := sockPath + ".lock"
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("daemon already running (lock %s held)", path)
	}
	return &sockLock{fd: fd}, nil
}

// recoverSockLock wraps a lock fd inherited across a hot-upgrade exec.
func recoverSockLock(fd int) *sockLock {
	return &sockLock{fd: fd}
}

// prepareExec makes the lock fd survive exec and returns it, so the
// upgraded daemon keeps holding the lock with no window for a racing
// bridge to start a second daemon.
func (l *sockLock) prepareExec() int {
	terminal.ClearCloseOnExec(l.fd)
	return l.fd
}

// release drops the lock. The lock file stays behind (harmless, empty).
func (l *sockLock) release() {
	if l.fd >= 0 {
		syscall.Close(l.fd)
		l.fd = -1
	}
}
