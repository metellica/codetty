package hub

import (
	"io"
	"log"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"

	"codetty/protocol"
)

// Tea messages sent from Hub to TUI
type ClientConnectedMsg struct{ Session *ClientSession }
type ClientDisconnectedMsg struct{ ID string }
type ScreenUpdateMsg struct {
	ID   string
	Data []byte
}

// Hub manages all connected client sessions.
type Hub struct {
	mu       sync.RWMutex
	sessions map[string]*ClientSession
	order    []string // ordered list of session IDs
	program  *tea.Program

	fsMu         sync.RWMutex
	fullscreenID string
	fsWriter     io.Writer
}

func New() *Hub {
	return &Hub{
		sessions: make(map[string]*ClientSession),
	}
}

func (h *Hub) SetProgram(p *tea.Program) {
	h.program = p
}

// SetFullscreen sets which session is currently displayed in fullscreen mode.
// Pass empty string and nil writer to exit fullscreen.
func (h *Hub) SetFullscreen(id string, w io.Writer) {
	h.fsMu.Lock()
	h.fullscreenID = id
	h.fsWriter = w
	h.fsMu.Unlock()
}

func (h *Hub) GetFullscreenID() string {
	h.fsMu.RLock()
	defer h.fsMu.RUnlock()
	return h.fullscreenID
}

// HandleConn handles a new WebSocket connection from a client.
// Blocks until the connection is closed.
func (h *Hub) HandleConn(conn *websocket.Conn) {
	var sess *ClientSession

	defer func() {
		if sess != nil {
			h.removeSession(sess.ID)
		}
		conn.Close()
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}

		msgType, payload, ok := protocol.ParseType(msg)
		if !ok {
			return
		}

		switch msgType {
		case protocol.MsgRegister:
			p, err := protocol.DecodeRegister(payload)
			if err != nil {
				log.Printf("bad register payload: %v", err)
				return
			}
			writer := NewConnWriter(conn)
			sess = NewClientSession(p.ID, p.Title, p.Cols, p.Rows, writer)
			h.addSession(sess)

		case protocol.MsgReplay:
			if sess == nil {
				return
			}
			inFullscreen := h.writeSessionOutput(sess, payload)
			if h.program != nil && !inFullscreen {
				h.program.Send(ScreenUpdateMsg{ID: sess.ID, Data: nil})
			}

		case protocol.MsgOutput:
			if sess == nil {
				return
			}
			inFullscreen := h.writeSessionOutput(sess, payload)
			if h.program != nil && !inFullscreen {
				h.program.Send(ScreenUpdateMsg{ID: sess.ID, Data: payload})
			}
		}
	}
}

func (h *Hub) writeSessionOutput(sess *ClientSession, payload []byte) bool {
	h.fsMu.RLock()
	defer h.fsMu.RUnlock()

	sess.WriteOutput(payload)

	// In fullscreen mode, forward raw output to the fullscreen writer.
	// Skip program.Send entirely — bubbletea's event loop is blocked
	// inside tea.Exec and its msgs channel is unbuffered, so Send
	// would block forever.
	inFullscreen := h.fullscreenID != ""
	if h.fullscreenID == sess.ID && h.fsWriter != nil {
		h.fsWriter.Write(payload)
	}
	return inFullscreen
}

func (h *Hub) addSession(s *ClientSession) {
	h.mu.Lock()
	// If a session with the same ID exists, close the old one
	if old, exists := h.sessions[s.ID]; exists {
		old.Close()
		for i, id := range h.order {
			if id == s.ID {
				h.order = append(h.order[:i], h.order[i+1:]...)
				break
			}
		}
	}
	h.sessions[s.ID] = s
	h.order = append(h.order, s.ID)
	h.mu.Unlock()

	if h.program != nil {
		h.program.Send(ClientConnectedMsg{Session: s})
	}
}

func (h *Hub) removeSession(id string) {
	h.mu.Lock()
	if _, exists := h.sessions[id]; exists {
		delete(h.sessions, id)
		for i, oid := range h.order {
			if oid == id {
				h.order = append(h.order[:i], h.order[i+1:]...)
				break
			}
		}
	}
	h.mu.Unlock()

	if h.program != nil {
		h.program.Send(ClientDisconnectedMsg{ID: id})
	}
}

// Sessions returns an ordered copy of all active sessions.
func (h *Hub) Sessions() []*ClientSession {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*ClientSession, 0, len(h.order))
	for _, id := range h.order {
		if s, ok := h.sessions[id]; ok {
			out = append(out, s)
		}
	}
	return out
}

// Session returns a session by ID, or nil if not found.
func (h *Hub) Session(id string) *ClientSession {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sessions[id]
}
