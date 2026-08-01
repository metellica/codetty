//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// startDaemonProcess spawns the daemon detached from this terminal.
//
// The daemon gets its own hidden console (CREATE_NEW_CONSOLE + SW_HIDE)
// rather than DETACHED_PROCESS: a console-less parent breaks ConPTY child
// spawning and console APIs the daemon relies on (native mouse injection,
// Ctrl event delivery). CREATE_BREAKAWAY_FROM_JOB escapes kill-on-close job
// objects (e.g. sshd's), so sessions survive the launching terminal; some
// job objects forbid breakaway, in which case we retry without it.
func startDaemonProcess(sockPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	flags := uint32(windows.CREATE_NEW_CONSOLE |
		windows.CREATE_NEW_PROCESS_GROUP |
		windows.CREATE_BREAKAWAY_FROM_JOB)
	if err := spawnDaemon(exe, sockPath, flags); err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return spawnDaemon(exe, sockPath, flags&^uint32(windows.CREATE_BREAKAWAY_FROM_JOB))
		}
		return err
	}
	return nil
}

func spawnDaemon(exe, sockPath string, flags uint32) error {
	cmd := exec.Command(exe, "__server", sockPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: flags,
		HideWindow:    true,
	}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}
