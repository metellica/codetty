//go:build windows

package agentdetect

// ForegroundCommand is not implemented on Windows yet; agent detection
// reports Unknown for all ConPTY sessions.
func ForegroundCommand(ptyFd int) (name string, argv []string, ok bool) {
	return "", nil, false
}
