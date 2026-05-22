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
			if sess := srv.SessionByName(session); sess != nil {
				sess.Terminal.Write(data)
			}

		case FrameResize:
			var rp ResizePayload
			if json.Unmarshal(payload, &rp) == nil {
				srv.resizeAllSessions(rp.Cols, rp.Rows)
			}

		case FrameCreateSession:
			var req CreateSessionRequest
			if json.Unmarshal(payload, &req) == nil {
				srv.createSessionForSubscriber(s, req)
			}

		case FrameKillSession:
			var req KillSessionRequest
			if json.Unmarshal(payload, &req) == nil {
				srv.killSessionForSubscriber(req)
			}

		case FrameDetach:
			return
		}
	}
}
