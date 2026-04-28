package hub

import (
	"io"

	"codetty/protocol"

	"github.com/hinshun/vt10x"
)

// ClientSession represents one connected terminal client.
type ClientSession struct {
	ID    string
	Title string
	Cols  uint16
	Rows  uint16
	term  vt10x.Terminal
	writer *ConnWriter
}

// NewClientSession creates a session with a vt10x virtual terminal.
// Pass nil for writer if no WebSocket connection is available (testing).
func NewClientSession(id, title string, cols, rows uint16, writer *ConnWriter) *ClientSession {
	term := vt10x.New(vt10x.WithSize(int(cols), int(rows)), vt10x.WithWriter(io.Discard))
	return &ClientSession{
		ID:     id,
		Title:  title,
		Cols:   cols,
		Rows:   rows,
		term:   term,
		writer: writer,
	}
}

// WriteOutput feeds raw PTY output bytes into the vt10x terminal.
func (s *ClientSession) WriteOutput(data []byte) {
	s.term.Write(data)
}

// Screen returns the current virtual terminal screen content as a string.
// vt10x.String() handles its own locking internally.
func (s *ClientSession) Screen() string {
	return s.term.String()
}

// SendInput sends keyboard input to the remote client.
func (s *ClientSession) SendInput(data []byte) {
	if s.writer != nil {
		s.writer.Send(protocol.EncodeInput(data))
	}
}

// SendResize sends a resize command to the remote client and updates local state.
func (s *ClientSession) SendResize(cols, rows uint16) {
	s.Cols = cols
	s.Rows = rows
	s.term.Resize(int(cols), int(rows))
	if s.writer != nil {
		msg, err := protocol.EncodeResize(cols, rows)
		if err == nil {
			s.writer.Send(msg)
		}
	}
}

// Close releases resources. Safe to call multiple times.
func (s *ClientSession) Close() {
	if s.writer != nil {
		s.writer.Close()
		s.writer = nil
	}
}
