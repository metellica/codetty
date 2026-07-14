package main

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"codetty/daemon"

	tea "github.com/charmbracelet/bubbletea"
)

// daemonConn manages the connection between TUI and daemon.
type daemonConn struct {
	conn       net.Conn
	sockPath   string
	program    *tea.Program
	subscribed bool

	// Output coalescing: buffer per-session output during burst,
	// flush at most every 16ms to reduce View() calls.
	outMu      sync.Mutex
	outBuf     map[string][]byte
	outPending bool // true while a flush timer is running
}

// Tea messages from daemon connection
type daemonOutputMsg struct {
	Session string
	Data    []byte
}

type daemonSessionEventMsg struct {
	Event daemon.SessionEvent
}

type daemonDisconnectedMsg struct {
	Err error
}

// daemonReconnectedMsg carries a fresh connection after the daemon dropped
// us (e.g. during a hot upgrade) and we successfully dialed again.
type daemonReconnectedMsg struct {
	conn *daemonConn
}

// daemonReconnectFailedMsg signals that reconnection attempts were exhausted.
type daemonReconnectFailedMsg struct{}

type daemonSubscribedMsg struct{}

func newDaemonConn(sockPath string) (*daemonConn, error) {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, err
	}
	return &daemonConn{conn: conn, sockPath: sockPath}, nil
}

// reconnectCmd retries dialing the daemon socket for a few seconds. The
// daemon closes subscriber connections during a hot upgrade and rebinds
// the socket moments later, so a short retry window bridges the gap.
func reconnectCmd(sockPath string) tea.Cmd {
	return func() tea.Msg {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if dc, err := newDaemonConn(sockPath); err == nil {
				return daemonReconnectedMsg{conn: dc}
			}
			time.Sleep(200 * time.Millisecond)
		}
		return daemonReconnectFailedMsg{}
	}
}

// Subscribe sends subscribe_all and starts the read loop.
func (dc *daemonConn) Subscribe(p *tea.Program) error {
	dc.program = p

	if err := daemon.WriteFrame(dc.conn, daemon.FrameRequest, daemon.MarshalJSON(daemon.Request{
		Action: daemon.ActionSubscribeAll,
	})); err != nil {
		return fmt.Errorf("send subscribe_all request: %w", err)
	}

	go dc.readLoop()
	return nil
}

func (dc *daemonConn) readLoop() {
	for {
		ft, payload, err := daemon.ReadFrame(dc.conn)
		if err != nil {
			dc.program.Send(daemonDisconnectedMsg{Err: err})
			return
		}

		switch ft {
		case daemon.FrameTaggedOutput:
			session, data, err := daemon.DecodeTaggedOutput(payload)
			if err != nil {
				continue
			}
			dc.bufferOutput(session, data)

		case daemon.FrameSessionEvent:
			var evt daemon.SessionEvent
			if json.Unmarshal(payload, &evt) == nil {
				dc.program.Send(daemonSessionEventMsg{Event: evt})
			}

		case daemon.FrameResponse:
			if !dc.subscribed {
				dc.subscribed = true
				dc.program.Send(daemonSubscribedMsg{})
			}
		}
	}
}

// bufferOutput accumulates output for coalesced delivery.
// First call in a window sends immediately (zero latency for interactive use),
// then starts a 16ms timer. Subsequent calls within the window buffer data
// until the timer fires and flushes.
func (dc *daemonConn) bufferOutput(session string, data []byte) {
	dc.outMu.Lock()
	if dc.outBuf == nil {
		dc.outBuf = make(map[string][]byte)
	}
	dc.outBuf[session] = append(dc.outBuf[session], data...)

	if !dc.outPending {
		// Leading edge: flush immediately, then start coalescing window
		dc.outPending = true
		buf := dc.outBuf
		dc.outBuf = make(map[string][]byte)
		dc.outMu.Unlock()

		for s, d := range buf {
			dc.program.Send(daemonOutputMsg{Session: s, Data: d})
		}
		time.AfterFunc(16*time.Millisecond, dc.flushOutput)
		return
	}
	dc.outMu.Unlock()
}

// flushOutput sends all buffered output and resets the coalescing window.
func (dc *daemonConn) flushOutput() {
	dc.outMu.Lock()
	buf := dc.outBuf
	dc.outBuf = make(map[string][]byte)
	dc.outPending = false
	dc.outMu.Unlock()

	for s, d := range buf {
		dc.program.Send(daemonOutputMsg{Session: s, Data: d})
	}
}

// SendInput sends tagged input to a session.
func (dc *daemonConn) SendInput(session string, data []byte) {
	daemon.WriteFrame(dc.conn, daemon.FrameTaggedInput, daemon.EncodeTaggedInput(session, data))
}

// SendResize sends resize to all sessions.
func (dc *daemonConn) SendResize(cols, rows uint16) {
	daemon.WriteFrame(dc.conn, daemon.FrameResize, daemon.MarshalJSON(daemon.ResizePayload{
		Cols: cols, Rows: rows,
	}))
}

// SendResizeSession sends resize to a specific session by name.
func (dc *daemonConn) SendResizeSession(name string, cols, rows uint16) {
	daemon.WriteFrame(dc.conn, daemon.FrameResize, daemon.MarshalJSON(daemon.ResizePayload{
		Cols: cols, Rows: rows, Name: name,
	}))
}

// CreateSession requests the daemon to create a new session.
// dir sets the first tab's working directory ("" = daemon default).
func (dc *daemonConn) CreateSession(name string, command []string, cols, rows uint16, dir string) {
	daemon.WriteFrame(dc.conn, daemon.FrameCreateSession, daemon.MarshalJSON(daemon.CreateSessionRequest{
		Name: name, Command: command, Cols: cols, Rows: rows, Dir: dir,
	}))
}

// KillSession requests the daemon to kill a session.
func (dc *daemonConn) KillSession(name string) {
	daemon.WriteFrame(dc.conn, daemon.FrameKillSession, daemon.MarshalJSON(daemon.KillSessionRequest{
		Name: name,
	}))
}

// RenameSession requests the daemon to rename a session.
func (dc *daemonConn) RenameSession(oldName, newName string) {
	daemon.WriteFrame(dc.conn, daemon.FrameRenameSession, daemon.MarshalJSON(daemon.RenameSessionRequest{
		OldName: oldName,
		NewName: newName,
	}))
}

// Detach sends detach and closes the connection.
func (dc *daemonConn) Detach() {
	daemon.WriteFrame(dc.conn, daemon.FrameDetach, nil)
	dc.conn.Close()
}

// CreateTab requests the daemon to create a new tab within a session.
func (dc *daemonConn) CreateTab(session, name string, command []string, dir string, env []string) {
	daemon.WriteFrame(dc.conn, daemon.FrameCreateTab, daemon.MarshalJSON(daemon.CreateTabRequest{
		Session: session,
		Name:    name,
		Command: command,
		Dir:     dir,
		Env:     env,
	}))
}

// CloseTab requests the daemon to close a tab in a session.
func (dc *daemonConn) CloseTab(session string, index int) {
	daemon.WriteFrame(dc.conn, daemon.FrameCloseTab, daemon.MarshalJSON(daemon.CloseTabRequest{
		Session: session,
		Index:   index,
	}))
}

// SwitchTab requests the daemon to switch the active tab in a session.
func (dc *daemonConn) SwitchTab(session string, index int) {
	daemon.WriteFrame(dc.conn, daemon.FrameSwitchTab, daemon.MarshalJSON(daemon.SwitchTabRequest{
		Session: session,
		Index:   index,
	}))
}

// ReorderSessions pushes the full session display order to the daemon so it
// persists across detach/attach and hot upgrades.
func (dc *daemonConn) ReorderSessions(order []string) {
	daemon.WriteFrame(dc.conn, daemon.FrameReorderSess, daemon.MarshalJSON(daemon.ReorderSessionsRequest{
		Order: order,
	}))
}

// SetActiveSession tells the daemon which session occupies the big panel so
// the next attach restores it.
func (dc *daemonConn) SetActiveSession(name string) {
	daemon.WriteFrame(dc.conn, daemon.FrameSetActive, daemon.MarshalJSON(daemon.SetActiveSessionRequest{
		Session: name,
	}))
}
