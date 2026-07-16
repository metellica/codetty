package daemon

import (
	"encoding/json"
	"net"
	"sync"
)

type queuedFrame struct {
	ft      FrameType
	payload []byte
}

// Subscriber is a multi-session client that receives output from ALL sessions.
type Subscriber struct {
	conn    net.Conn
	mu      sync.Mutex
	closed  bool
	ready   bool
	pending []queuedFrame

	// Negotiated protocol state, set during the subscribe handshake before
	// Run starts and read-only afterwards.
	proto int
	caps  map[string]bool
	owner string // normalized owner namespace this subscriber belongs to
}

func newSubscriber(conn net.Conn) *Subscriber {
	return &Subscriber{conn: conn}
}

// SendTaggedOutput sends session-tagged output to the subscriber.
func (s *Subscriber) SendTaggedOutput(session string, data []byte) {
	s.sendFrame(FrameTaggedOutput, EncodeTaggedOutput(session, data))
}

// SendSessionEvent sends a session lifecycle event.
func (s *Subscriber) SendSessionEvent(evt SessionEvent) {
	s.sendFrame(FrameSessionEvent, MarshalJSON(evt))
}

func (s *Subscriber) sendFrame(ft FrameType, payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if !s.ready {
		buf := make([]byte, len(payload))
		copy(buf, payload)
		s.pending = append(s.pending, queuedFrame{ft: ft, payload: buf})
		return
	}
	WriteFrame(s.conn, ft, payload)
}

func (s *Subscriber) sendFrameDirect(ft FrameType, payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	WriteFrame(s.conn, ft, payload)
}

func (s *Subscriber) activate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ready {
		return
	}
	s.ready = true
	for _, frame := range s.pending {
		WriteFrame(s.conn, frame.ft, frame.payload)
	}
	s.pending = nil
}

// Close marks the subscriber as closed.
func (s *Subscriber) Close() {
	s.mu.Lock()
	s.closed = true
	s.pending = nil
	s.mu.Unlock()
}

// Run reads frames from the subscriber and dispatches them.
func (s *Subscriber) Run(srv *Server) {
	defer s.Close()

	for {
		ft, payload, err := ReadFrame(s.conn)
		if err != nil {
			return
		}

		switch ft {
		case FrameTaggedInput:
			session, data, err := DecodeTaggedInput(payload)
			if err != nil {
				continue
			}
			if sess := srv.SessionByName(s.owner, session); sess != nil {
				sess.Terminal.Write(data)
			}

		case FrameResize:
			var rp ResizePayload
			if json.Unmarshal(payload, &rp) == nil {
				if rp.Name != "" {
					if sess := srv.SessionByName(s.owner, rp.Name); sess != nil {
						sess.Resize(rp.Cols, rp.Rows)
					}
				} else {
					srv.resizeAllSessions(s.owner, rp.Cols, rp.Rows)
				}
			}

		case FrameCreateSession:
			var req CreateSessionRequest
			if json.Unmarshal(payload, &req) == nil {
				srv.createSessionForSubscriber(s, req)
			}

		case FrameKillSession:
			var req KillSessionRequest
			if json.Unmarshal(payload, &req) == nil {
				srv.killSessionForSubscriber(s, req)
			}

		case FrameRenameSession:
			var req RenameSessionRequest
			if json.Unmarshal(payload, &req) == nil {
				srv.renameSessionForSubscriber(s, req)
			}

		case FrameCreateTab:
			var req CreateTabRequest
			if json.Unmarshal(payload, &req) == nil {
				srv.createTabForSubscriber(s, req)
			}

		case FrameCloseTab:
			var req CloseTabRequest
			if json.Unmarshal(payload, &req) == nil {
				srv.closeTabForSubscriber(s, req)
			}

		case FrameSwitchTab:
			var req SwitchTabRequest
			if json.Unmarshal(payload, &req) == nil {
				srv.switchTabForSubscriber(s, req)
			}

		case FrameReorderSess:
			var req ReorderSessionsRequest
			if json.Unmarshal(payload, &req) == nil {
				srv.reorderSessionsForSubscriber(s, req)
			}

		case FrameSetActive:
			var req SetActiveSessionRequest
			if json.Unmarshal(payload, &req) == nil {
				srv.setActiveSessionForSubscriber(s, req)
			}

		case FrameDetach:
			return
		}
	}
}
