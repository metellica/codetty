//go:build !windows

package agentdetect

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/unix"
)

// ForegroundCommand returns the name and argv of the foreground process
// group leader for the terminal owning the given PTY master fd.
// ok is false when the foreground process cannot be determined.
func ForegroundCommand(ptyFd int) (name string, argv []string, ok bool) {
	if ptyFd < 0 {
		return "", nil, false
	}
	pgid, err := unix.IoctlGetInt(ptyFd, unix.TIOCGPGRP)
	if err != nil || pgid <= 0 {
		return "", nil, false
	}
	if name, argv, ok = procfsCommand(pgid); ok {
		return name, argv, true
	}
	return psCommand(pgid)
}

// procfsCommand reads the process name and argv from /proc (Linux).
func procfsCommand(pid int) (string, []string, bool) {
	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return "", nil, false
	}
	name := strings.TrimSpace(string(comm))

	var argv []string
	if raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		for _, arg := range strings.Split(string(raw), "\x00") {
			if arg != "" {
				argv = append(argv, arg)
			}
		}
	}
	return name, argv, true
}

// psCommand shells out to ps as a portable fallback (macOS has no procfs).
func psCommand(pid int) (string, []string, bool) {
	out, err := exec.Command("ps", "-o", "args=", "-p", fmt.Sprint(pid)).Output()
	if err != nil {
		return "", nil, false
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 {
		return "", nil, false
	}
	return fields[0], fields, true
}
