package daemon

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// FrameType identifies the type of local protocol frame.
type FrameType byte

const (
	FrameRequest       FrameType = 0x01 // C→S: JSON request
	FrameResponse      FrameType = 0x02 // S→C: JSON response
	FrameOutput        FrameType = 0x03 // S→C: raw terminal output
	FrameInput         FrameType = 0x04 // C→S: raw keyboard input
	FrameResize        FrameType = 0x05 // C→S: JSON resize
	FrameDetach        FrameType = 0x06 // C→S: detach (no payload)
	FrameSessionSwitch FrameType = 0x07 // C→S: JSON switch target
	FrameNotification  FrameType = 0x08 // S→C: JSON notification
	FrameTaggedOutput  FrameType = 0x10 // S→C: session-tagged output
	FrameTaggedInput   FrameType = 0x11 // C→S: session-tagged input
	FrameSessionEvent  FrameType = 0x12 // S→C: JSON session lifecycle event
	FrameCreateSession FrameType = 0x13 // C→S: JSON create session request
	FrameKillSession   FrameType = 0x14 // C→S: JSON kill session request
	FrameRenameSession FrameType = 0x15 // C→S: JSON rename session request
	FrameRefreshTmux   FrameType = 0x16 // C→S: request tmux session refresh (no payload)
)

// Action identifies the requested operation in a Request frame.
type Action string

const (
	ActionNewSession   Action = "new_session"
	ActionAttach       Action = "attach"
	ActionList         Action = "list"
	ActionKill         Action = "kill"
	ActionKillServer   Action = "kill_server"
	ActionSubscribeAll Action = "subscribe_all"
	ActionRefreshTmux  Action = "refresh_tmux"
)

// Request is the JSON payload for FrameRequest.
type Request struct {
	Action  Action   `json:"action"`
	Session string   `json:"session,omitempty"`
	Command []string `json:"command,omitempty"`
	Env     []string `json:"env,omitempty"`
	Cols    uint16   `json:"cols,omitempty"`
	Rows    uint16   `json:"rows,omitempty"`
}

// Response is the JSON payload for FrameResponse.
type Response struct {
	OK       bool          `json:"ok"`
	Error    string        `json:"error,omitempty"`
	Session  string        `json:"session,omitempty"`
	Sessions []SessionInfo `json:"sessions,omitempty"`
}

// SessionInfo describes a session in list responses.
type SessionInfo struct {
	Name      string `json:"name"`
	Command   string `json:"command"`
	Type      string `json:"type,omitempty"`
	Attached  int    `json:"attached"`
	CreatedAt string `json:"created_at"`
}

// ResizePayload for FrameResize.
type ResizePayload struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// SwitchPayload for FrameSessionSwitch.
type SwitchPayload struct {
	Target string `json:"target"`
}

// Notification for FrameNotification.
type Notification struct {
	Event    string `json:"event"`
	Session  string `json:"session"`
	ExitCode int    `json:"exit_code,omitempty"`
}

// SessionEvent describes a session lifecycle change (for FrameSessionEvent).
type SessionEvent struct {
	Type     string      `json:"type"`
	Session  string      `json:"session"`
	Info     SessionInfo `json:"info,omitempty"`
	ExitCode int         `json:"exit_code,omitempty"`
	Cols     uint16      `json:"cols,omitempty"`
	Rows     uint16      `json:"rows,omitempty"`
}

// CreateSessionRequest is the payload for FrameCreateSession.
type CreateSessionRequest struct {
	Name    string   `json:"name,omitempty"`
	Command []string `json:"command,omitempty"`
	Cols    uint16   `json:"cols"`
	Rows    uint16   `json:"rows"`
}

// KillSessionRequest is the payload for FrameKillSession.
type KillSessionRequest struct {
	Name string `json:"name"`
}

// RenameSessionRequest is the payload for FrameRenameSession.
type RenameSessionRequest struct {
	OldName string `json:"old_name"`
	NewName string `json:"new_name"`
}

// EncodeTaggedOutput prepends session name length + name to output data.
func EncodeTaggedOutput(session string, data []byte) []byte {
	nameBytes := []byte(session)
	if len(nameBytes) > 255 {
		panic("tagged frame session name exceeds 255 bytes")
	}
	buf := make([]byte, 1+len(nameBytes)+len(data))
	buf[0] = byte(len(nameBytes))
	copy(buf[1:], nameBytes)
	copy(buf[1+len(nameBytes):], data)
	return buf
}

// DecodeTaggedOutput extracts session name and payload from tagged frame.
func DecodeTaggedOutput(payload []byte) (session string, data []byte, err error) {
	if len(payload) < 1 {
		return "", nil, fmt.Errorf("tagged frame too short")
	}
	nameLen := int(payload[0])
	if len(payload) < 1+nameLen {
		return "", nil, fmt.Errorf("tagged frame name truncated")
	}
	session = string(payload[1 : 1+nameLen])
	data = payload[1+nameLen:]
	return session, data, nil
}

// EncodeTaggedInput prepends session name to input data (same format as output).
func EncodeTaggedInput(session string, data []byte) []byte {
	return EncodeTaggedOutput(session, data)
}

// DecodeTaggedInput extracts session name and input from tagged frame.
func DecodeTaggedInput(payload []byte) (session string, data []byte, err error) {
	return DecodeTaggedOutput(payload)
}

// WriteFrame writes a length-prefixed frame to w.
// Format: [4 bytes big-endian length of type+payload][1 byte type][payload]
func WriteFrame(w io.Writer, ft FrameType, payload []byte) error {
	length := uint32(1 + len(payload))
	if err := binary.Write(w, binary.BigEndian, length); err != nil {
		return fmt.Errorf("write frame length: %w", err)
	}
	if _, err := w.Write([]byte{byte(ft)}); err != nil {
		return fmt.Errorf("write frame type: %w", err)
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return fmt.Errorf("write frame payload: %w", err)
		}
	}
	return nil
}

// ReadFrame reads a single frame from r.
func ReadFrame(r io.Reader) (FrameType, []byte, error) {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return 0, nil, err
	}
	if length == 0 {
		return 0, nil, fmt.Errorf("empty frame")
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, fmt.Errorf("read frame body: %w", err)
	}
	return FrameType(buf[0]), buf[1:], nil
}

// MarshalJSON is a helper to marshal any value to JSON bytes.
func MarshalJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
