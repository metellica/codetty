package daemon

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// tmuxCmdTimeout is the maximum time to wait for a single tmux command.
const tmuxCmdTimeout = 10 * time.Second

// tmuxWaitDelay bounds how long we wait for I/O pipes after the timeout
// fires. Without this, a killed tmux client whose pipe fd was inherited by
// the tmux server would make CombinedOutput/Output block forever.
const tmuxWaitDelay = 3 * time.Second

// tmuxCommand runs a tmux command with a timeout.
// Returns an error that includes stderr output for debugging.
func tmuxCommand(args ...string) error {
	_, err := tmuxOutput(args...)
	return err
}

// tmuxOutput runs a tmux command with a timeout and returns trimmed stdout.
func tmuxOutput(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", args...)
	cmd.WaitDelay = tmuxWaitDelay
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("tmux %s: timed out after %s", args[0], tmuxCmdTimeout)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// IsTmuxAvailable checks if the tmux binary exists in PATH.
func IsTmuxAvailable() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// TmuxSessionExists checks if a tmux session with the given name exists.
// The "=" prefix forces exact matching (tmux otherwise falls back to prefix
// matching, which would treat "dev" as existing when only "devbox" does).
func TmuxSessionExists(name string) bool {
	return tmuxCommand("has-session", "-t", "="+name) == nil
}

// CreateTmuxSession creates a new detached tmux session with the given name.
func CreateTmuxSession(name string) error {
	return tmuxCommand("new-session", "-d", "-s", name)
}

// KillTmuxSession kills a tmux session by name (exact match).
func KillTmuxSession(name string) error {
	return tmuxCommand("kill-session", "-t", "="+name)
}

// RenameTmuxSession renames a tmux session from oldName to newName.
func RenameTmuxSession(oldName, newName string) error {
	return tmuxCommand("rename-session", "-t", "="+oldName, newName)
}

// DiscoverTmuxSessions returns the list of active tmux session names.
// Returns nil and no error if tmux is not available or has no server running.
func DiscoverTmuxSessions() ([]string, error) {
	if !IsTmuxAvailable() {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#{session_name}").Output()
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

// TmuxWindowSetup describes a window to create in a tmux session.
type TmuxWindowSetup struct {
	Name    string
	Command string
	Dir     string
}

// SetupTmuxSessionWithWindows creates a tmux session with the given windows.
// Each window has its own name, command, and working directory. env entries
// ("KEY=value") are set in the tmux session environment, inherited by every
// window.
//
// Returns (false, nil) if the session already exists (no modification),
// (true, nil) on success. On error the partially created session is killed
// so a later retry starts clean, and (false, err) is returned.
//
// The setup runs in two phases: first all windows are created, then commands
// are sent to each window by its unique window ID. Sending commands only
// after all windows exist avoids racing against heavy TUI programs (e.g.
// copilot, nvim) whose startup can stall subsequent new-window commands.
// Window IDs (@n) guarantee keys never land in the caller's current window.
func SetupTmuxSessionWithWindows(tmuxSession string, windows []TmuxWindowSetup, env []string) (created bool, err error) {
	if TmuxSessionExists(tmuxSession) {
		return false, nil
	}

	// On any failure after session creation, kill the partial session so the
	// next attempt does not silently attach to an incomplete one.
	defer func() {
		if err != nil {
			KillTmuxSession(tmuxSession)
		}
	}()

	args := []string{"new-session", "-d", "-s", tmuxSession, "-P", "-F", "#{window_id}"}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	if len(windows) > 0 {
		if windows[0].Dir != "" {
			args = append(args, "-c", windows[0].Dir)
		}
		if windows[0].Name != "" {
			args = append(args, "-n", windows[0].Name)
		}
	}
	id, err := tmuxOutput(args...)
	if err != nil {
		return false, fmt.Errorf("create tmux session %q: %w", tmuxSession, err)
	}
	if len(windows) == 0 {
		return true, nil
	}

	// Phase 1: create all windows, collecting their unique IDs.
	windowIDs := make([]string, len(windows))
	windowIDs[0] = id

	// Shadow any user-configured after-new-window hook (e.g. an interactive
	// rename-window prompt) at session scope. Such hooks target our CLI
	// client, which can never answer a prompt, hanging new-window forever.
	// The session-scope empty hook leaves the user's global hook untouched;
	// it is removed again after setup. Note: set-hook's -t does not accept
	// the "=" exact-match prefix; the plain name is unambiguous here because
	// the session was just created.
	hookShadowed := tmuxCommand("set-hook", "-t", tmuxSession, "after-new-window", "") == nil
	defer func() {
		if hookShadowed && err == nil {
			tmuxCommand("set-hook", "-u", "-t", tmuxSession, "after-new-window")
		}
	}()

	for i, w := range windows[1:] {
		// -d keeps the first window current; "=" forces exact session match
		// and the trailing ":" pins the target to the session so the window
		// is never created elsewhere.
		newArgs := []string{"new-window", "-d", "-t", "=" + tmuxSession + ":", "-P", "-F", "#{window_id}"}
		if w.Name != "" {
			newArgs = append(newArgs, "-n", w.Name)
		}
		if w.Dir != "" {
			newArgs = append(newArgs, "-c", w.Dir)
		}
		id, werr := tmuxOutput(newArgs...)
		if werr != nil {
			return false, fmt.Errorf("new window %q: %w", w.Name, werr)
		}
		windowIDs[i+1] = id
	}

	// Phase 2: send each window its command, addressed by window ID.
	for i, w := range windows {
		if w.Command == "" {
			continue
		}
		if serr := tmuxCommand("send-keys", "-t", windowIDs[i], w.Command, "Enter"); serr != nil {
			return false, fmt.Errorf("send command to window %q: %w", w.Name, serr)
		}
	}

	return true, nil
}
