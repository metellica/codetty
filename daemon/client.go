package daemon

import (
	"encoding/json"
	"io"
	"net"
	"sync"
)

// Client represents an attached terminal client connection.
type Client struct {
	conn    net.Conn
	session *DaemonSession
	mu      sync.Mutex
	closed  bool
}

func newClient(conn net.Conn, session *DaemonSession) *Client {
	return &Client{conn: conn, session: session}
}

// SendOutput sends terminal output to the client.
func (c *Client) SendOutput(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	WriteFrame(c.conn, FrameOutput, data)
}

// SendNotification sends a notification to the client.
func (c *Client) SendNotification(n Notification) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	WriteFrame(c.conn, FrameNotification, MarshalJSON(n))
}

// Run processes client frames until detach or disconnect.
func (c *Client) Run(srv *Server) {
	defer func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
	}()

	for {
		ft, payload, err := ReadFrame(c.conn)
		if err != nil {
			if err == io.EOF {
				return
			}
			return
		}

		switch ft {
		case FrameInput:
			c.session.Terminal.Write(payload)

		case FrameResize:
			var rp ResizePayload
			if json.Unmarshal(payload, &rp) == nil {
				c.session.Resize(rp.Cols, rp.Rows)
			}

		case FrameDetach:
			return

		case FrameSessionSwitch:
			var sp SwitchPayload
			if json.Unmarshal(payload, &sp) == nil {
				c.switchSession(srv, sp.Target)
			}
		}
	}
}

func (c *Client) switchSession(srv *Server, target string) {
	var newSess *DaemonSession

	switch target {
	case "__next", "__prev":
		names := srv.SessionNames()
		if len(names) < 2 {
			return
		}
		idx := -1
		for i, n := range names {
			if n == c.session.Name {
				idx = i
				break
			}
		}
		if idx < 0 {
			return
		}
		if target == "__next" {
			idx = (idx + 1) % len(names)
		} else {
			idx = (idx - 1 + len(names)) % len(names)
		}
		newSess = srv.SessionByName(names[idx])
	default:
		newSess = srv.SessionByName(target)
	}

	if newSess == nil || newSess == c.session {
		return
	}

	c.session.removeClient(c)
	c.session = newSess
	newSess.addClient(c)

	if snap := newSess.Screen.Snapshot(); len(snap) > 0 {
		c.SendOutput(snap)
	}
}
