package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"codetty/config"

	tea "github.com/charmbracelet/bubbletea"
)

// Remote tabs created ad-hoc from the TUI picker are not described by any
// config file, so a restarted TUI would not know to reconnect their remote
// daemons (the shadow containers keep running there). The registry persists
// which remote endpoints currently hold shadow parts; startup reconnects
// them and part changes rewrite the file, so dead or emptied remotes drop
// off automatically.

// recordedRemotesPath returns the registry path inside the per-instance
// state directory (~/.config/codetty/<instance>/).
func recordedRemotesPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	name := instanceName
	if name == "" {
		name = "default"
	}
	return filepath.Join(home, ".config", "codetty", name, "remotes.json")
}

// loadRecordedRemotes reads the registry; missing or invalid files mean no
// recorded remotes.
func loadRecordedRemotes() map[string]config.RemoteDef {
	path := recordedRemotesPath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var remotes map[string]config.RemoteDef
	if json.Unmarshal(data, &remotes) != nil {
		return nil
	}
	return remotes
}

// saveRecordedRemotes atomically rewrites the registry to exactly the given
// set; an empty set removes the file.
func saveRecordedRemotes(remotes map[string]config.RemoteDef) {
	path := recordedRemotesPath()
	if path == "" {
		return
	}
	if len(remotes) == 0 {
		_ = os.Remove(path)
		// Best effort: drop the instance dir if it became empty.
		_ = os.Remove(filepath.Dir(path))
		return
	}
	data, err := json.MarshalIndent(remotes, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// reconnectRecordedRemotes dials recorded remotes not already connected
// (e.g. not referenced by the config), so shadow tabs created ad-hoc in
// earlier runs reappear. Runs in the background: unreachable remotes must
// not block TUI startup, and their registry entries are preserved.
func reconnectRecordedRemotes(conn *daemonConn, p *tea.Program, recorded map[string]config.RemoteDef) {
	for name, def := range recorded {
		endpoint, err := newSSHDaemonEndpoint(name, def)
		if err != nil || conn.HasPeer(endpoint.ID()) {
			continue
		}
		go func(name string, endpoint *sshDaemonEndpoint) {
			if err := endpoint.Ensure(); err != nil {
				p.Send(tuiNoticeMsg{text: fmt.Sprintf("remote %s: %v", name, err)})
				return
			}
			if err := conn.AddPeer(endpoint.ID(), endpoint); err != nil {
				p.Send(tuiNoticeMsg{text: fmt.Sprintf("remote %s: %v", name, err)})
			}
		}(name, endpoint)
	}
}
