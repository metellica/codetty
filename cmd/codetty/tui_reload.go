package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"codetty/config"
)

// reloadConfig hot-reloads the .codetty.toml config file, adding/removing
// tabs (local and remote) to match the desired state without restarting
// existing processes.
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
	endpoints, plans, err := buildConfigEndpoints(cfg, sessions, defaultSocketPath())
	if err != nil {
		log.Printf("reload: endpoints: %v", err)
		return
	}

	if !m.conn.multi() {
		m.reloadLocalOnly(plans)
		return
	}

	cols, rows := m.focusedContentSize()
	for _, plan := range plans {
		display := plan.Session.Name
		desiredPeers := make(map[string]bool, len(plan.Containers))
		for _, cp := range plan.Containers {
			desiredPeers[cp.EndpointID] = true
			if cp.EndpointID != localPeerID && !m.conn.HasPeer(cp.EndpointID) {
				endpoint := endpoints[cp.EndpointID]
				if err := endpoint.Ensure(); err != nil {
					log.Printf("reload: prepare %s: %v", cp.EndpointID, err)
					continue
				}
				// Check for an existing container before subscribing: if it
				// already exists, adopt it as-is instead of re-creating
				// its tabs (its state streams in via the subscription).
				current, err := querySessionsEndpoint(endpoint)
				if err != nil {
					log.Printf("reload: query %s: %v", cp.EndpointID, err)
					continue
				}
				exists := endpointSessionNames(current)[cp.Container]
				if err := m.conn.AddPeer(cp.EndpointID, endpoint); err != nil {
					log.Printf("reload: connect %s: %v", cp.EndpointID, err)
					continue
				}
				if !exists {
					m.createContainerTabs(display, cp, uint16(cols), uint16(rows))
				}
				continue
			}

			tabs, exists := m.conn.ContainerTabs(display, cp.EndpointID)
			if !exists {
				m.createContainerTabs(display, cp, uint16(cols), uint16(rows))
				continue
			}

			// Diff this container's tabs against the desired set.
			// Create before close so a close never targets a container's
			// last remaining tab: per-peer frames are FIFO, so the daemon
			// appends the new tabs (keeping existing indices stable)
			// before the closes execute.
			desired := make(map[string]bool, len(cp.Tabs))
			for _, tab := range cp.Tabs {
				desired[tab.Name] = true
			}
			existing := make(map[string]bool, len(tabs))
			for _, tab := range tabs {
				existing[tab.Name] = true
			}
			for _, tab := range cp.Tabs {
				if !existing[tab.Name] {
					m.conn.CreateTabOn(
						display, cp.EndpointID, tab.Name,
						tab.Command, tab.Dir, envMapToSlice(tab.Env),
						uint16(cols), uint16(rows),
					)
				}
			}
			// Close in reverse order to keep part-local indices stable.
			for i := len(tabs) - 1; i >= 0; i-- {
				if !desired[tabs[i].Name] {
					m.conn.CloseTabOn(display, cp.EndpointID, i)
				}
			}
		}

		// Kill containers whose tabs all moved elsewhere or were removed
		// from the config. Desired containers were processed above, so any
		// replacement creates are already in flight.
		for _, peerID := range m.conn.ContainerPeers(display) {
			if !desiredPeers[peerID] {
				m.conn.KillContainerOn(display, peerID)
			}
		}
	}
}

// createContainerTabs creates every desired tab of a container; the first
// CreateTabOn creates the container session itself and the rest are queued
// until it appears.
func (m *tuiModel) createContainerTabs(display string, cp containerPlan, cols, rows uint16) {
	for _, tab := range cp.Tabs {
		m.conn.CreateTabOn(
			display, cp.EndpointID, tab.Name,
			tab.Command, tab.Dir, envMapToSlice(tab.Env),
			cols, rows,
		)
	}
}

// reloadLocalOnly applies a reload over a legacy single-daemon connection:
// only local containers are diffed, remote tabs are reported.
func (m *tuiModel) reloadLocalOnly(plans []sessionPlan) {
	for _, plan := range plans {
		var localTabs []config.ResolvedTab
		for _, cp := range plan.Containers {
			if cp.EndpointID == localPeerID {
				localTabs = cp.Tabs
			} else {
				log.Printf("reload: session %q has remote tabs; restart codetty to connect them", plan.Session.Name)
			}
		}
		if len(localTabs) == 0 {
			continue
		}
		sess := m.sessionByName(plan.Session.Name)
		if sess == nil {
			first := localTabs[0]
			cols, rows := m.focusedContentSize()
			m.conn.CreateSessionWithEnv(
				plan.Session.Name, first.Command,
				uint16(cols), uint16(rows),
				first.Dir, envMapToSlice(first.Env), first.Name,
			)
			// Remaining tabs are created once the session appears.
			m.pendingReloadTabs[plan.Session.Name] = localTabs
			continue
		}
		m.diffAndApplyTabs(sess, localTabs)
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
