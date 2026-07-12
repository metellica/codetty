package main

import (
	"net"
	"path/filepath"
	"testing"

	"codetty/daemon"

	tea "github.com/charmbracelet/bubbletea"
)

func TestUpdate_DisconnectWithoutSockPathQuits(t *testing.T) {
	m := newTUIModel(nil, "", nil)

	_, cmd := m.Update(daemonDisconnectedMsg{})
	if cmd == nil {
		t.Fatal("cmd = nil, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("cmd() = %T, want tea.QuitMsg", cmd())
	}
}

func TestUpdate_DisconnectTriggersReconnect(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "d.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()

	m := newTUIModel(&daemonConn{sockPath: sockPath}, "", nil)

	_, cmd := m.Update(daemonDisconnectedMsg{})
	if cmd == nil {
		t.Fatal("cmd = nil, want reconnect command")
	}
	msg := cmd()
	rec, ok := msg.(daemonReconnectedMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want daemonReconnectedMsg", msg)
	}
	if rec.conn == nil || rec.conn.sockPath != sockPath {
		t.Fatalf("reconnected conn = %#v, want conn with sockPath %q", rec.conn, sockPath)
	}
	rec.conn.conn.Close()
}

func TestUpdate_ReconnectedResetsStateAndSubscribes(t *testing.T) {
	prog, _ := startTestProgram(t)

	m := newTUIModel(&daemonConn{program: prog}, "", nil)
	m.width = 100
	m.height = 30
	m.handleSessionEvent(daemon.SessionEvent{
		Type:    "created",
		Session: "alpha",
		Info:    daemon.SessionInfo{Command: "bash"},
	})
	m.selected = 0
	m.listFocus = true

	client, server := net.Pipe()
	defer server.Close()
	// Drain the subscribe_all request Subscribe writes to the pipe.
	go daemon.ReadFrame(server)

	newConn := &daemonConn{conn: client, sockPath: "/tmp/x.sock"}
	_, cmd := m.Update(daemonReconnectedMsg{conn: newConn})

	if m.conn != newConn {
		t.Fatal("conn was not swapped to the reconnected one")
	}
	if len(m.sessions) != 0 || len(m.order) != 0 {
		t.Fatalf("sessions/order not cleared: %d/%d", len(m.sessions), len(m.order))
	}
	if m.listFocus {
		t.Fatalf("focus state not reset: listFocus=%v", m.listFocus)
	}
	if newConn.program != prog {
		t.Fatal("program was not carried over to the new connection")
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want ClearScreen")
	}
	if _, ok := cmd().(tea.QuitMsg); ok {
		t.Fatal("cmd() = tea.QuitMsg, want ClearScreen")
	}
}

func TestUpdate_ReconnectFailedQuits(t *testing.T) {
	m := newTUIModel(nil, "", nil)

	_, cmd := m.Update(daemonReconnectFailedMsg{})
	if cmd == nil {
		t.Fatal("cmd = nil, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("cmd() = %T, want tea.QuitMsg", cmd())
	}
}
