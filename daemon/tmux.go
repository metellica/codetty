package daemon

import (
	"os/exec"
	"strings"
)

// IsTmuxAvailable checks if the tmux binary exists in PATH.
func IsTmuxAvailable() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// CreateTmuxSession creates a new detached tmux session with the given name.
func CreateTmuxSession(name string) error {
	return exec.Command("tmux", "new-session", "-d", "-s", name).Run()
}

// KillTmuxSession kills a tmux session by name.
func KillTmuxSession(name string) error {
	return exec.Command("tmux", "kill-session", "-t", name).Run()
}

// RenameTmuxSession renames a tmux session from oldName to newName.
func RenameTmuxSession(oldName, newName string) error {
	return exec.Command("tmux", "rename-session", "-t", oldName, newName).Run()
}

// DiscoverTmuxSessions returns the list of active tmux session names.
// Returns nil and no error if tmux is not available or has no server running.
func DiscoverTmuxSessions() ([]string, error) {
	if !IsTmuxAvailable() {
		return nil, nil
	}

	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		// tmux returns error when no server is running — not a real error for us
		return nil, nil
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var sessions []string
	for _, line := range lines {
		name := strings.TrimSpace(line)
		if name != "" {
			sessions = append(sessions, name)
		}
	}
	return sessions, nil
}
