package main

import (
	"encoding/json"
	"fmt"
	"sort"

	"codetty/config"
	"codetty/daemon"
)

func buildConfigEndpoints(
	cfg *config.SessionsConfig,
	sessions []config.ResolvedSession,
	sockPath string,
) (map[string]daemonEndpoint, map[string]string, error) {
	endpoints := make(map[string]daemonEndpoint)
	routes := make(map[string]string, len(sessions))
	remoteByKey := make(map[string]string)

	for _, session := range sessions {
		if session.Remote == "" {
			if _, ok := endpoints["local"]; !ok {
				endpoints["local"] = localDaemonEndpoint{sockPath: sockPath}
			}
			routes[session.Name] = "local"
			continue
		}

		def, ok := cfg.Remote[session.Remote]
		if !ok {
			return nil, nil, fmt.Errorf("session %q: remote %q not found", session.Name, session.Remote)
		}
		instance := def.Instance
		if instance == "" {
			instance = "default"
		}
		key := def.Host + "\x00" + instance
		endpoint, err := newSSHDaemonEndpoint(session.Remote, def)
		if err != nil {
			return nil, nil, err
		}
		id, ok := remoteByKey[key]
		if !ok {
			id = endpoint.ID()
			remoteByKey[key] = id
			endpoints[id] = endpoint
		} else if existing, ok := endpoints[id].(*sshDaemonEndpoint); ok &&
			(existing.deploy != endpoint.deploy || existing.binary != endpoint.binary) {
			return nil, nil, fmt.Errorf(
				"remote %q conflicts with another definition for %s instance %q",
				session.Remote,
				def.Host,
				instance,
			)
		}
		routes[session.Name] = id
	}

	return endpoints, routes, nil
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
