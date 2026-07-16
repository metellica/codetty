//go:build windows

package daemon

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// sockLock is an exclusive lock guarding a daemon socket path, so racing
// clients cannot start two daemons for the same instance.
type sockLock struct {
	f *os.File
}

// acquireSockLock takes an exclusive non-blocking LockFileEx lock on
// sockPath+".lock". Fails if another daemon already holds it.
func acquireSockLock(sockPath string) (*sockLock, error) {
	f, err := os.OpenFile(sockPath+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	ol := new(windows.Overlapped)
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("daemon already running (lock %s held)", f.Name())
	}
	return &sockLock{f: f}, nil
}

// release drops the lock. The lock file stays behind (harmless, empty).
func (l *sockLock) release() {
	l.f.Close()
}
