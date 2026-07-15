package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"codetty/config"
	"codetty/daemon"
)

// containerPlan is one daemon-side container session to create for a config
// session: either the local part (named after the session) or a remote
// shadow session (<display>@@<localID>).
type containerPlan struct {
	EndpointID string
	Container  string
	Tabs       []config.ResolvedTab
}

// sessionPlan groups one resolved session's tabs by target daemon: the
// local container first, then remote shadows sorted by endpoint ID.
type sessionPlan struct {
	Session    config.ResolvedSession
	Containers []containerPlan
}

// buildConfigEndpoints resolves the daemon endpoints a config needs (the
// local daemon is always included) and the per-session container plans
// describing which tabs live on which daemon.
func buildConfigEndpoints(
	cfg *config.SessionsConfig,
	sessions []config.ResolvedSession,
	sockPath string,
) (map[string]daemonEndpoint, []sessionPlan, error) {
	endpoints := map[string]daemonEndpoint{
		"local": localDaemonEndpoint{sockPath: sockPath},
	}
	remoteByKey := make(map[string]string)
	shadowID := localShadowID()

	endpointFor := func(sessionName, remoteName string) (string, error) {
		def, ok := cfg.Remote[remoteName]
		if !ok {
			return "", fmt.Errorf("session %q: remote %q not found", sessionName, remoteName)
		}
		instance := def.Instance
		if instance == "" {
			instance = "default"
		}
		key := def.Host + "\x00" + instance
		endpoint, err := newSSHDaemonEndpoint(remoteName, def)
		if err != nil {
			return "", err
		}
		id, ok := remoteByKey[key]
		if !ok {
			id = endpoint.ID()
			remoteByKey[key] = id
			endpoints[id] = endpoint
			return id, nil
		}
		if existing, ok := endpoints[id].(*sshDaemonEndpoint); ok &&
			(existing.deploy != endpoint.deploy || existing.binary != endpoint.binary) {
			return "", fmt.Errorf(
				"remote %q conflicts with another definition for %s instance %q",
				remoteName,
				def.Host,
				instance,
			)
		}
		return id, nil
	}

	plans := make([]sessionPlan, 0, len(sessions))
	for _, session := range sessions {
		byEndpoint := make(map[string]*containerPlan)
		var order []string
		for _, tab := range session.Tabs {
			id := "local"
			container := session.Name
			if tab.Remote != "" {
				var err error
				id, err = endpointFor(session.Name, tab.Remote)
				if err != nil {
					return nil, nil, err
				}
				container = shadowSessionName(session.Name, shadowID)
			}
			cp, ok := byEndpoint[id]
			if !ok {
				cp = &containerPlan{EndpointID: id, Container: container}
				byEndpoint[id] = cp
				order = append(order, id)
			}
			cp.Tabs = append(cp.Tabs, tab)
		}
		// Local container first, then remotes sorted by endpoint ID, to
		// match the composite merge order in the TUI.
		sort.Slice(order, func(i, j int) bool {
			if (order[i] == "local") != (order[j] == "local") {
				return order[i] == "local"
			}
			return order[i] < order[j]
		})
		plan := sessionPlan{Session: session}
		for _, id := range order {
			plan.Containers = append(plan.Containers, *byEndpoint[id])
		}
		plans = append(plans, plan)
	}

	return endpoints, plans, nil
}

// remoteTarget is a selectable execution target for a new tab: the local
// daemon or a remote defined in the sessions config.
type remoteTarget struct {
	label    string
	id       string
	endpoint daemonEndpoint // nil for the local daemon
}

// loadRemoteTargets lists the tab execution targets available to the TUI:
// always the local daemon, plus every [remote.*] in the sessions config.
func loadRemoteTargets() []remoteTarget {
	targets := []remoteTarget{{label: "local", id: "local"}}
	cwd, err := os.Getwd()
	if err != nil {
		return targets
	}
	cfg, _, err := config.LoadSessions("", cwd)
	if err != nil {
		return targets
	}
	names := make([]string, 0, len(cfg.Remote))
	for name := range cfg.Remote {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		endpoint, err := newSSHDaemonEndpoint(name, cfg.Remote[name])
		if err != nil {
			continue
		}
		targets = append(targets, remoteTarget{label: name, id: endpoint.ID(), endpoint: endpoint})
	}
	return targets
}

func ensureEndpoints(endpoints map[string]daemonEndpoint) error {
	ids := make([]string, 0, len(endpoints))
	for id := range endpoints {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := endpoints[id].Ensure(); err != nil {
			return err
		}
	}
	return nil
}

func querySessionsEndpoint(endpoint daemonEndpoint) ([]daemon.SessionInfo, error) {
	conn, err := endpoint.Dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{
		Action: daemon.ActionList,
	})); err != nil {
		return nil, err
	}
	ft, payload, err := daemon.ReadFrame(conn)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if ft != daemon.FrameResponse {
		return nil, fmt.Errorf("unexpected response")
	}
	var resp daemon.Response
	if err := json.Unmarshal(payload, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("list failed: %s", resp.Error)
	}
	return resp.Sessions, nil
}

func createDaemonSessionEndpoint(
	endpoint daemonEndpoint,
	name string,
	cmd []string,
	cols, rows uint16,
	dir string,
	env []string,
	tabName string,
) error {
	return requestEndpoint(endpoint, daemon.Request{
		Action:   daemon.ActionNewSession,
		Session:  name,
		Command:  cmd,
		Cols:     cols,
		Rows:     rows,
		Dir:      dir,
		Env:      env,
		NoAttach: true,
		TabName:  tabName,
	})
}

func createDaemonTabEndpoint(
	endpoint daemonEndpoint,
	session, tabName string,
	cmd []string,
	dir string,
	env []string,
) error {
	return requestEndpoint(endpoint, daemon.Request{
		Action:  daemon.ActionCreateTab,
		Session: session,
		TabName: tabName,
		Command: cmd,
		Dir:     dir,
		Env:     env,
	})
}

func requestEndpoint(endpoint daemonEndpoint, req daemon.Request) error {
	conn, err := endpoint.Dial()
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	if err := daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(req)); err != nil {
		return err
	}
	ft, payload, err := daemon.ReadFrame(conn)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if ft != daemon.FrameResponse {
		return fmt.Errorf("unexpected frame type: %d", ft)
	}
	var resp daemon.Response
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

func endpointSessionNames(sessions []daemon.SessionInfo) map[string]bool {
	names := make(map[string]bool, len(sessions))
	for _, session := range sessions {
		names[session.Name] = true
	}
	return names
}

func endpointSessionsByName(sessions []daemon.SessionInfo) map[string]daemon.SessionInfo {
	result := make(map[string]daemon.SessionInfo, len(sessions))
	for _, session := range sessions {
		result[session.Name] = session
	}
	return result
}

func closeDaemonTabEndpoint(endpoint daemonEndpoint, session string, index int) error {
	conn, err := endpoint.Dial()
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	if err := daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{
		Action:   daemon.ActionCloseTab,
		Session:  session,
		TabIndex: index,
	})); err != nil {
		return err
	}
	ft, payload, err := daemon.ReadFrame(conn)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if ft != daemon.FrameResponse {
		return fmt.Errorf("unexpected frame type: %d", ft)
	}
	var resp daemon.Response
	if err := json.Unmarshal(payload, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}
