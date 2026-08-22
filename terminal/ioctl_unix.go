//go:build !windows && !darwin

package terminal

import "golang.org/x/sys/unix"

// tiocgsid is the TIOCGSID ioctl request number (get session id).
// golang.org/x/sys/unix defines this constant on Linux and the BSDs.
const tiocgsid = unix.TIOCGSID
