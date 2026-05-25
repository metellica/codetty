package main

import (
	"encoding/json"
	"fmt"
	"net"

	"codetty/daemon"

	tea "github.com/charmbracelet/bubbletea"
)

// daemonConn manages the connection between TUI and daemon.
type daemonConn struct {
	conn       net.Conn
	program    *tea.Program
	subscribed bool
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

type daemonSubscribedMsg struct{}

func newDaemonConn(sockPath string) (*daemonConn, error) {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, err
	}
	return &daemonConn{conn: conn}, nil
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
			dc.program.Send(daemonOutputMsg{Session: session, Data: data})

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
func (dc *daemonConn) CreateSession(name string, command []string, cols, rows uint16) {
	daemon.WriteFrame(dc.conn, daemon.FrameCreateSession, daemon.MarshalJSON(daemon.CreateSessionRequest{
		Name: name, Command: command, Cols: cols, Rows: rows,
	}))
}

// CreateTmuxSession requests the daemon to create a new tmux session.
func (dc *daemonConn) CreateTmuxSession(name string, cols, rows uint16) {
	daemon.WriteFrame(dc.conn, daemon.FrameCreateSession, daemon.MarshalJSON(daemon.CreateSessionRequest{
		Name: name, Cols: cols, Rows: rows, Tmux: true,
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

// RefreshTmux requests the daemon to discover and attach new tmux sessions.
func (dc *daemonConn) RefreshTmux() {
	daemon.WriteFrame(dc.conn, daemon.FrameRefreshTmux, nil)
}
