package hub

import (
	"fmt"
	"io"
	"log"
	"sync"
	"unicode/utf8"

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

// AuthInfo carries authentication information for a WebSocket connection.
// In secure (mTLS) mode, Secure is true and User is the CN from the client cert.
// In insecure mode, Secure is false and User is empty (stored as "anonymous").
type AuthInfo struct {
	Secure bool
	User   string
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
// auth carries identity information resolved from the HTTP/TLS layer.
// Blocks until the connection is closed.
func (h *Hub) HandleConn(conn *websocket.Conn, auth AuthInfo) {
	var sess *ClientSession

	defer func() {
		if sess != nil {
			log.Printf("disconnect: session=%s user=%s", sess.ID, sess.User)
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

			// Determine effective user and validate against auth.
			user := resolveUser(auth, p.User)
			if user == "" {
				// resolveUser returning empty means mismatch rejection.
				log.Printf("auth mismatch: authenticated=%q register=%q; closing connection", auth.User, p.User)
				reason := authRejectedCloseReason(auth.User, p.User)
				if err := conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.ClosePolicyViolation, reason)); err != nil {
					log.Printf("write close message: %v", err)
				}
				return
			}

			writer := NewConnWriter(conn)
			sess = NewClientSession(p.ID, p.Title, p.Cols, p.Rows, writer)
			sess.User = user
			h.addSession(sess)
			log.Printf("connect: session=%s user=%s", sess.ID, sess.User)

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

// authRejectedCloseReason returns a UTF-8-safe close reason of at most 123 bytes
// (the WebSocket control-frame payload limit minus the 2-byte close code) that
// always contains the stable prefix "codetty auth rejected".  When the full
// "codetty auth rejected: <authUser> != <registerUser>" string would exceed 123
// bytes the function falls back to the bare prefix so the CLI can still match it.
func authRejectedCloseReason(authUser, registerUser string) string {
	const prefix = "codetty auth rejected"
	const maxBytes = 123

	full := fmt.Sprintf("%s: %s != %s", prefix, authUser, registerUser)
	if len([]byte(full)) <= maxBytes {
		return full
	}

	// Try to fit as much detail as possible: keep the prefix and append a
	// truncated suffix, never cutting in the middle of a UTF-8 rune.
	suffix := fmt.Sprintf(": %s != %s", authUser, registerUser)
	available := maxBytes - len(prefix)
	if available <= 0 {
		return prefix
	}
	b := []byte(suffix)
	if len(b) > available {
		b = b[:available]
		// Walk back until we have a valid UTF-8 boundary.
		for !utf8.Valid(b) {
			b = b[:len(b)-1]
		}
	}
	return prefix + string(b)
}


// In secure mode, the register payload's User must match the authenticated user.
// Returns "" to signal rejection (mismatch).
// In insecure mode, returns "anonymous".
func resolveUser(auth AuthInfo, registerUser string) string {
	if auth.Secure {
		if registerUser != auth.User {
			return ""
		}
		return auth.User
	}
	return "anonymous"
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
