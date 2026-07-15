package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"codetty/config"
)

// reloadConfig hot-reloads the .codetty.toml config file, adding/removing tabs
// to match the desired state without restarting existing processes.
func (m *tuiModel) reloadConfig() {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}

	cfg, cfgPath, err := config.LoadSessions("", cwd)
	if err != nil {
		log.Printf("reload: %v", err)
		return
	}
	if err := cfg.Validate(); err != nil {
		log.Printf("reload: invalid config: %v", err)
		return
	}

	configDir := filepath.Dir(cfgPath)
	sessions, err := cfg.Resolve(configDir)
	if err != nil {
		log.Printf("reload: resolve: %v", err)
		return
	}

	if m.conn == nil {
		return
	}
	_, routes, err := buildConfigEndpoints(cfg, sessions, defaultSocketPath())
	if err != nil {
		log.Printf("reload: endpoints: %v", err)
		return
	}

	refreshPeers := make(map[string]bool)
	for _, s := range sessions {
		sess := m.sessionByName(s.Name)
		target := routes[s.Name]
		if sess != nil {
			if current := m.conn.SessionPeer(s.Name); current != target {
				log.Printf(
					"reload: session %q target changed from %s to %s; restart codetty up to migrate it",
					s.Name,
					current,
					target,
				)
				continue
			}
		} else if !m.conn.RouteSession(s.Name, target) {
			log.Printf("reload: session %q uses a new remote target; restart codetty up to connect it", s.Name)
			continue
		} else if target != "local" {
			exists, err := m.conn.PeerHasSession(target, s.Name)
			if err != nil {
				log.Printf("reload: query session %q: %v", s.Name, err)
				continue
			}
			if exists {
				refreshPeers[target] = true
				continue
			}
		}
		if sess == nil {
			// Session doesn't exist yet — create it with first tab
			cmd := s.Command
			dir := s.Dir
			env := envMapToSlice(s.Env)
			if len(s.Tabs) > 0 {
				cmd = s.Tabs[0].Command
				dir = s.Tabs[0].Dir
				env = envMapToSlice(s.Tabs[0].Env)
			}
			firstTabName := ""
			if len(s.Tabs) > 0 {
				firstTabName = s.Tabs[0].Name
			}
			cols, rows := m.focusedContentSize()
			m.conn.CreateSessionWithEnv(s.Name, cmd, uint16(cols), uint16(rows), dir, env, firstTabName)
			// Tabs will be created after the session appears (via event)
			// Store pending tabs for deferred creation
			m.pendingReloadTabs[s.Name] = s.Tabs
			continue
		}

		// Session exists — diff tabs
		m.diffAndApplyTabs(sess, s.Tabs)
	}
	for peerID := range refreshPeers {
		m.conn.RefreshPeer(peerID)
	}
}

// diffAndApplyTabs compares current tabs with desired tabs and adds/removes as needed.
func (m *tuiModel) diffAndApplyTabs(sess *tuiSession, desired []config.ResolvedTab) {
	if m.conn == nil {
		return
	}

	// Build map of existing tab names
	existing := make(map[string]int) // name -> index
	for i, tab := range sess.Tabs {
		existing[tab.Name] = i
	}

	// Build map of desired tab names
	desiredMap := make(map[string]config.ResolvedTab)
	for _, tab := range desired {
		desiredMap[tab.Name] = tab
	}

	// Close tabs that are no longer in config (in reverse order to keep indices stable)
	for i := len(sess.Tabs) - 1; i >= 0; i-- {
		tab := sess.Tabs[i]
		if _, want := desiredMap[tab.Name]; !want {
			m.conn.CloseTab(sess.Name, i)
		}
	}

	// Add tabs that are new in config
	for _, dt := range desired {
		if _, have := existing[dt.Name]; !have {
			cmd := dt.Command
			m.conn.CreateTab(sess.Name, dt.Name, cmd, dt.Dir, envMapToSlice(dt.Env))
		}
	}
}

// envMapToSlice converts an env map to "KEY=value" slice.
func envMapToSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	result := make([]string, 0, len(env))
	for k, v := range env {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	return result
}
