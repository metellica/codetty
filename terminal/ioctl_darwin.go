//go:build darwin

package terminal

// tiocgsid is the TIOCGSID ioctl request number (get session id), as
// defined in Darwin's <sys/ttycom.h>: _IOR('t', 63, int). golang.org/x/sys/unix
// does not expose TIOCGSID for darwin, so it is hardcoded here.
const tiocgsid = 0x40047463
