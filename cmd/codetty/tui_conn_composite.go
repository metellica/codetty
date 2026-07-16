package main

import (
	"sort"
	"strings"

	"codetty/daemon"
)

// Composite sessions: the TUI always shows sessions as a local concept, but
// individual tabs may run on remote daemons. Each remote daemon holds those
// tabs in a container session that shares the display name; the daemon's
// owner namespace (keyed by this host's owner ID) keeps it isolated from
// the remote host's own sessions and from other clients'. The connection
// layer merges the local session and its remote parts into a single
// composite session, rewriting events, output and tab indices so the TUI
// only ever sees the display name and one concatenated tab list.

// compositePart is one daemon-side container of a composite session: either
// the local session itself or a container session on a remote daemon.
type compositePart struct {
	peerID string
	name   string             // session name on the owning daemon
	label  string             // remote name for tab markers ("" for local)
	info   daemon.SessionInfo // last-known info from that daemon
}

// compositeSession merges a local session and its remote parts into
// the single session the TUI displays.
type compositeSession struct {
	display string
	parts   []*compositePart // local part first, remote parts sorted by peer ID
	active  *compositePart   // the part whose active tab is displayed
}

func partLess(a, b *compositePart) bool {
	if (a.peerID == localPeerID) != (b.peerID == localPeerID) {
		return a.peerID == localPeerID
	}
	return a.peerID < b.peerID
}

func (c *compositeSession) part(peerID string) *compositePart {
	for _, p := range c.parts {
		if p.peerID == peerID {
			return p
		}
	}
	return nil
}

func (c *compositeSession) insertPart(p *compositePart) {
	i := sort.Search(len(c.parts), func(i int) bool { return !partLess(c.parts[i], p) })
	c.parts = append(c.parts, nil)
	copy(c.parts[i+1:], c.parts[i:])
	c.parts[i] = p
	if c.active == nil {
		c.active = p
	}
}

func (c *compositeSession) removePart(p *compositePart) {
	for i, q := range c.parts {
		if q == p {
			c.parts = append(c.parts[:i], c.parts[i+1:]...)
			break
		}
	}
	if c.active == p {
		c.active = nil
		if len(c.parts) > 0 {
			c.active = c.parts[0]
		}
	}
}

// partTabCount returns how many tabs a part contributes to the merged bar.
// A daemon session always has at least one tab even when Info omits them.
func partTabCount(p *compositePart) int {
	if n := len(p.info.Tabs); n > 0 {
		return n
	}
	return 1
}

// mapTab resolves a merged tab index to its owning part and local index.
func (c *compositeSession) mapTab(idx int) (*compositePart, int) {
	if idx < 0 {
		return nil, 0
	}
	for _, p := range c.parts {
		n := partTabCount(p)
		if idx < n {
			return p, idx
		}
		idx -= n
	}
	return nil, 0
}

// mergedInfo builds the SessionInfo the TUI sees: the display name, the
// concatenation of all parts' tabs, and the active tab pointing into the
// active part.
func (c *compositeSession) mergedInfo() daemon.SessionInfo {
	if len(c.parts) == 1 {
		info := c.parts[0].info
		info.Name = c.display
		info.Tabs = stampRemoteTabs(info.Tabs, c.parts[0].label)
		return info
	}
	if len(c.parts) == 0 {
		return daemon.SessionInfo{Name: c.display}
	}
	base := c.parts[0].info
	info := daemon.SessionInfo{
		Name:      c.display,
		Command:   base.Command,
		Type:      base.Type,
		Attached:  base.Attached,
		CreatedAt: base.CreatedAt,
	}
	offset := 0
	for _, p := range c.parts {
		tabs := p.info.Tabs
		if len(tabs) == 0 {
			tabs = []daemon.TabInfo{{Name: "default", Command: p.info.Command}}
		}
		info.Tabs = append(info.Tabs, stampRemoteTabs(tabs, p.label)...)
		if p == c.active {
			at := p.info.ActiveTab
			if at < 0 || at >= len(tabs) {
				at = 0
			}
			info.ActiveTab = offset + at
		}
		offset += len(tabs)
	}
	return info
}

func partKey(peerID, name string) string {
	return peerID + "\x00" + name
}

// stampRemoteTabs marks tabs as living on the named remote. Copies before
// stamping so the part's own slice is never mutated.
func stampRemoteTabs(tabs []daemon.TabInfo, label string) []daemon.TabInfo {
	if label == "" || len(tabs) == 0 {
		return tabs
	}
	out := make([]daemon.TabInfo, len(tabs))
	copy(out, tabs)
	for i := range out {
		out[i].Remote = label
	}
	return out
}

// peerRemoteLabel names the remote a peer's tabs run on for UI markers;
// local tabs get no marker.
func peerRemoteLabel(peer *daemonPeer) string {
	if peer.id == localPeerID {
		return ""
	}
	if e, ok := peer.endpoint.(*sshDaemonEndpoint); ok {
		return e.name
	}
	return strings.TrimPrefix(peer.id, "remote:")
}

// displayForName maps a peer-side session name to a composite display name.
// Session names map to themselves: the daemon scopes each subscription to
// its owner namespace, so every session a peer delivers belongs to this
// TUI host already.
func (dc *daemonConn) displayForName(peerID, session string) (string, bool) {
	return session, true
}

// mapOutput resolves tagged output from a peer to the display session it
// belongs to. Output is forwarded only from the composite's active part so
// inactive parts' broadcasts don't corrupt the displayed terminal.
func (dc *daemonConn) mapOutput(peerID, session string) (string, bool) {
	dc.compMu.Lock()
	defer dc.compMu.Unlock()
	c := dc.partOwner[partKey(peerID, session)]
	if c == nil || c.active == nil || c.active.peerID != peerID {
		return "", false
	}
	return c.display, true
}

// mergeEvent folds a peer's session event into the composite state and
// returns the rewritten events to forward to the TUI, plus deferred frame
// writes to run after the composite lock is released.
func (dc *daemonConn) mergeEvent(peer *daemonPeer, evt daemon.SessionEvent) (out []daemon.SessionEvent, post []func()) {
	local := peer.id == localPeerID
	switch evt.Type {
	case "reordered", "active":
		// Session order and big-panel selection are owned by the local
		// daemon; remote daemons only ever host tab containers.
		if local {
			return []daemon.SessionEvent{evt}, nil
		}
		return nil, nil
	}

	dc.compMu.Lock()
	defer dc.compMu.Unlock()

	switch evt.Type {
	case "snapshot", "created":
		display, ok := dc.displayForName(peer.id, evt.Session)
		if !ok {
			return nil, nil
		}
		peer.track(evt.Type, evt.Session, "")
		c := dc.composites[display]
		if c == nil {
			c = &compositeSession{display: display}
			dc.composites[display] = c
		}
		isNewComposite := len(c.parts) == 0
		p := c.part(peer.id)
		if p == nil {
			p = &compositePart{peerID: peer.id, name: evt.Session, label: peerRemoteLabel(peer)}
			c.insertPart(p)
			dc.partOwner[partKey(peer.id, evt.Session)] = c
		}
		p.info = evt.Info
		// Remote containers keep the PTY size of a previous run across
		// TUI restarts and reconnects; re-fit them to the panel size the
		// TUI last pushed so their content renders (and hit-tests) at
		// the width actually displayed.
		if peer.id != localPeerID {
			if cols, rows, ok := dc.desiredPanelSizeLocked(display); ok &&
				(evt.Cols != cols || evt.Rows != rows) {
				peerID, container := peer.id, evt.Session
				post = append(post, func() {
					dc.writeToPeer(peerID, daemon.FrameResize, daemon.MarshalJSON(daemon.ResizePayload{
						Cols: cols, Rows: rows, Name: container,
					}))
				})
			}
		}
		post = append(post, dc.flushQueuedTabsLocked(peer.id, evt.Session)...)
		if isNewComposite {
			outEvt := evt
			outEvt.Session = display
			outEvt.Info = c.mergedInfo()
			return []daemon.SessionEvent{outEvt}, post
		}
		// A part joined (or refreshed) an existing composite: update the
		// tab bar without resetting the displayed terminal.
		return []daemon.SessionEvent{{
			Type: "agent_state", Session: display, Info: c.mergedInfo(),
		}}, post

	case "renamed":
		return dc.mergeRenamed(peer, evt)

	case "tab_created", "tab_closed", "tab_switched", "agent_state":
		c := dc.partOwner[partKey(peer.id, evt.Session)]
		if c == nil {
			return nil, nil
		}
		p := c.part(peer.id)
		if p == nil {
			return nil, nil
		}
		p.info = evt.Info
		typ := evt.Type
		switch {
		case typ == "tab_switched":
			// The daemon follows a switch with a screen snapshot; adopting
			// the part as active lets that snapshot through.
			c.active = p
		case typ != "agent_state" && c.active != p:
			// Tab changes on inactive parts must not reset the displayed
			// terminal (their snapshot would be filtered out); demote to a
			// pure tab-bar update.
			typ = "agent_state"
		}
		return []daemon.SessionEvent{{
			Type: typ, Session: c.display, Info: c.mergedInfo(),
		}}, nil

	case "exited":
		peer.track(evt.Type, evt.Session, "")
		c := dc.partOwner[partKey(peer.id, evt.Session)]
		if c == nil {
			return nil, nil
		}
		delete(dc.partOwner, partKey(peer.id, evt.Session))
		delete(dc.queuedTabs, partKey(peer.id, evt.Session))
		p := c.part(peer.id)
		if p == nil {
			return nil, nil
		}
		return dc.removePartLocked(c, p, evt.ExitCode)

	case "error":
		display, ok := dc.displayForName(peer.id, evt.Session)
		if !ok {
			return nil, nil
		}
		delete(dc.queuedTabs, partKey(peer.id, evt.Session))
		evt.Session = display
		return []daemon.SessionEvent{evt}, nil
	}

	return nil, nil
}

// mergeRenamed handles a rename on one part: it re-keys the part, renames
// the composite when the display name changed, and asks the other parts to
// follow so every container keeps the display name.
func (dc *daemonConn) mergeRenamed(peer *daemonPeer, evt daemon.SessionEvent) (out []daemon.SessionEvent, post []func()) {
	newName := evt.Info.Name
	if newName == "" {
		return nil, nil
	}
	c := dc.partOwner[partKey(peer.id, evt.Session)]
	if c == nil {
		return nil, nil
	}
	p := c.part(peer.id)
	if p == nil {
		return nil, nil
	}
	peer.track("renamed", evt.Session, newName)
	delete(dc.partOwner, partKey(peer.id, evt.Session))
	dc.partOwner[partKey(peer.id, newName)] = c
	p.name = newName
	p.info.Name = newName

	newDisplay := newName
	if newDisplay == c.display {
		return nil, nil
	}

	if target := dc.composites[newDisplay]; target != nil && target != c {
		// The new display name is already taken: fold the part into the
		// existing composite instead of clobbering it.
		out, post = dc.removePartLocked(c, p, 0)
		target.insertPart(p)
		dc.partOwner[partKey(peer.id, newName)] = target
		out = append(out, daemon.SessionEvent{
			Type: "agent_state", Session: target.display, Info: target.mergedInfo(),
		})
		return out, post
	}

	oldDisplay := c.display
	delete(dc.composites, oldDisplay)
	c.display = newDisplay
	dc.composites[newDisplay] = c
	out = append(out, daemon.SessionEvent{
		Type:    "renamed",
		Session: oldDisplay,
		Info:    daemon.SessionInfo{Name: newDisplay},
	})

	// Ask the other parts to follow the new display name (no-ops when the
	// rename was initiated by RenameSession, which already renamed all
	// parts; the daemon ignores renames whose old name no longer exists).
	for _, q := range c.parts {
		if q == p {
			continue
		}
		want := newDisplay
		if q.name == want {
			continue
		}
		peerID, oldName := q.peerID, q.name
		post = append(post, func() {
			dc.writeToPeer(peerID, daemon.FrameRenameSession, daemon.MarshalJSON(daemon.RenameSessionRequest{
				OldName: oldName,
				NewName: want,
			}))
		})
	}
	return out, post
}

// removePartLocked removes a part from its composite and returns the events
// describing the result: the composite exits when its last part is gone;
// otherwise the tab bar shrinks, and if the removed part was active the
// first remaining part takes over (its daemon is asked to replay a screen
// snapshot via a same-index tab switch).
func (dc *daemonConn) removePartLocked(c *compositeSession, p *compositePart, exitCode int) (out []daemon.SessionEvent, post []func()) {
	wasActive := c.active == p
	c.removePart(p)
	if len(c.parts) == 0 {
		delete(dc.composites, c.display)
		return []daemon.SessionEvent{{
			Type: "exited", Session: c.display, ExitCode: exitCode,
		}}, nil
	}
	if wasActive {
		np := c.active
		peerID, name, idx := np.peerID, np.name, np.info.ActiveTab
		post = append(post, func() {
			dc.writeToPeer(peerID, daemon.FrameSwitchTab, daemon.MarshalJSON(daemon.SwitchTabRequest{
				Session: name,
				Index:   idx,
			}))
		})
		return []daemon.SessionEvent{{
			Type: "tab_switched", Session: c.display, Info: c.mergedInfo(),
		}}, post
	}
	return []daemon.SessionEvent{{
		Type: "agent_state", Session: c.display, Info: c.mergedInfo(),
	}}, nil
}

// queuedTab is a tab creation waiting for its container session to appear.
type queuedTab struct {
	name    string
	command []string
	dir     string
	env     []string
}

// flushQueuedTabsLocked emits deferred create_tab requests for tabs that
// were queued while their container session was still being created.
func (dc *daemonConn) flushQueuedTabsLocked(peerID, container string) []func() {
	key := partKey(peerID, container)
	queued, ok := dc.queuedTabs[key]
	if !ok {
		return nil
	}
	delete(dc.queuedTabs, key)
	if len(queued) == 0 {
		return nil
	}
	tabs := append([]queuedTab(nil), queued...)
	return []func(){func() {
		for _, tab := range tabs {
			dc.writeToPeer(peerID, daemon.FrameCreateTab, daemon.MarshalJSON(daemon.CreateTabRequest{
				Session: container,
				Name:    tab.name,
				Command: tab.command,
				Dir:     tab.dir,
				Env:     tab.env,
			}))
		}
	}}
}

// CreateTabOn ensures a tab exists in the display session on the given peer,
// creating the container session (the local session or the remote container)
// when the target has no part there yet. Tabs requested while the container
// is in flight are queued and created once it appears.
func (dc *daemonConn) CreateTabOn(
	display, peerID, name string,
	command []string,
	dir string,
	env []string,
	cols, rows uint16,
) {
	if !dc.multi() {
		dc.CreateTab(display, name, command, dir, env)
		return
	}

	// The target may be an alias of a peer we already know under another
	// name (same daemon reachable via two endpoints).
	peerID = dc.resolvePeerID(peerID)

	container := display

	dc.compMu.Lock()
	var target *compositePart
	if c := dc.composites[display]; c != nil {
		target = c.part(peerID)
	}
	if target == nil {
		key := partKey(peerID, container)
		if queued, pending := dc.queuedTabs[key]; pending {
			dc.queuedTabs[key] = append(queued, queuedTab{
				name: name, command: command, dir: dir, env: env,
			})
			dc.compMu.Unlock()
			return
		}
		dc.queuedTabs[key] = nil
	}
	dc.compMu.Unlock()

	if target != nil {
		dc.writeToPeer(peerID, daemon.FrameCreateTab, daemon.MarshalJSON(daemon.CreateTabRequest{
			Session: target.name,
			Name:    name,
			Command: command,
			Dir:     dir,
			Env:     env,
		}))
		return
	}
	if cols == 0 || rows == 0 {
		cols, rows = 80, 24
	}
	dc.writeToPeer(peerID, daemon.FrameCreateSession, daemon.MarshalJSON(daemon.CreateSessionRequest{
		Name:    container,
		Command: command,
		Cols:    cols,
		Rows:    rows,
		Dir:     dir,
		Env:     env,
		TabName: name,
	}))
}

// TabTarget returns the peer ID owning the merged tab index of a display
// session ("local" for purely local sessions or unknown indices).
func (dc *daemonConn) TabTarget(display string, idx int) string {
	if !dc.multi() {
		return localPeerID
	}
	dc.compMu.Lock()
	defer dc.compMu.Unlock()
	c := dc.composites[display]
	if c == nil {
		return localPeerID
	}
	p, _ := c.mapTab(idx)
	if p == nil {
		return localPeerID
	}
	return p.peerID
}

// ContainerTabs returns the last-known tabs of the display session's part
// on the given peer, and whether that part exists.
func (dc *daemonConn) ContainerTabs(display, peerID string) ([]daemon.TabInfo, bool) {
	if !dc.multi() {
		return nil, false
	}
	dc.compMu.Lock()
	defer dc.compMu.Unlock()
	c := dc.composites[display]
	if c == nil {
		return nil, false
	}
	p := c.part(peerID)
	if p == nil {
		return nil, false
	}
	return append([]daemon.TabInfo(nil), p.info.Tabs...), true
}

// CloseTabOn closes a tab addressed by part-local index on the given peer.
// It always sends a plain close: reload issues creates before closes, so a
// close never has to remove a container's last remaining tab (containers
// that lose all their tabs are killed via KillContainerOn instead).
func (dc *daemonConn) CloseTabOn(display, peerID string, localIdx int) {
	if !dc.multi() {
		dc.CloseTab(display, localIdx)
		return
	}
	dc.compMu.Lock()
	c := dc.composites[display]
	if c == nil {
		dc.compMu.Unlock()
		return
	}
	p := c.part(peerID)
	if p == nil {
		dc.compMu.Unlock()
		return
	}
	name := p.name
	dc.compMu.Unlock()

	dc.writeToPeer(peerID, daemon.FrameCloseTab, daemon.MarshalJSON(daemon.CloseTabRequest{
		Session: name,
		Index:   localIdx,
	}))
}

// ContainerPeers returns the peer IDs that currently hold a part of the
// display session.
func (dc *daemonConn) ContainerPeers(display string) []string {
	if !dc.multi() {
		return nil
	}
	dc.compMu.Lock()
	defer dc.compMu.Unlock()
	c := dc.composites[display]
	if c == nil {
		return nil
	}
	peers := make([]string, 0, len(c.parts))
	for _, p := range c.parts {
		peers = append(peers, p.peerID)
	}
	return peers
}

// KillContainerOn kills the display session's container on the given peer
// (the local session itself or a remote container session).
func (dc *daemonConn) KillContainerOn(display, peerID string) {
	if !dc.multi() {
		return
	}
	dc.compMu.Lock()
	var name string
	if c := dc.composites[display]; c != nil {
		if p := c.part(peerID); p != nil {
			name = p.name
		}
	}
	dc.compMu.Unlock()
	if name == "" {
		return
	}
	dc.writeToPeer(peerID, daemon.FrameKillSession, daemon.MarshalJSON(daemon.KillSessionRequest{
		Name: name,
	}))
}
