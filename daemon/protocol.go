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
	FrameCreateTab     FrameType = 0x17 // C→S: JSON create tab request
	FrameCloseTab      FrameType = 0x18 // C→S: JSON close tab request
	FrameSwitchTab     FrameType = 0x19 // C→S: JSON switch tab request
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
	ActionCreateTab    Action = "create_tab"
	ActionUpgrade      Action = "upgrade"
)

// Request is the JSON payload for FrameRequest.
type Request struct {
	Action     Action   `json:"action"`
	Session    string   `json:"session,omitempty"`
	Command    []string `json:"command,omitempty"`
	Env        []string `json:"env,omitempty"`
	Cols       uint16   `json:"cols,omitempty"`
	Rows       uint16   `json:"rows,omitempty"`
	Dir        string   `json:"dir,omitempty"`          // working directory for new session
	Type       string   `json:"type,omitempty"`         // "native" or "tmux"
	TmuxTarget string   `json:"tmux_target,omitempty"`  // tmux session name (when Type=="tmux")
	NoAttach   bool     `json:"no_attach,omitempty"`    // create session but don't start attached client
	TabName    string   `json:"tab_name,omitempty"`     // tab name for create_tab action
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
	Name       string `json:"name"`
	Command    string `json:"command"`
	Type       string `json:"type,omitempty"`
	TmuxTarget string `json:"tmux_target,omitempty"` // tmux session name (when Type=="tmux")
	Tabs       []TabInfo `json:"tabs,omitempty"`      // tabs within this session (native only)
	ActiveTab  int    `json:"active_tab,omitempty"`   // index of the active tab
	Attached   int    `json:"attached"`
	CreatedAt  string `json:"created_at"`
}

// TabInfo describes a single tab within a session.
type TabInfo struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

// ResizePayload for FrameResize.
type ResizePayload struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
	Name string `json:"name,omitempty"` // if set, resize only this session
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
	Name       string   `json:"name,omitempty"`
	Command    []string `json:"command,omitempty"`
	Cols       uint16   `json:"cols"`
	Rows       uint16   `json:"rows"`
	Tmux       bool     `json:"tmux,omitempty"`        // create a new tmux session
	TmuxAttach bool     `json:"tmux_attach,omitempty"` // attach to an existing tmux session (Name = tmux target)
}

// KillSessionRequest is the payload for FrameKillSession.
type KillSessionRequest struct {
	Name     string `json:"name"`
	KillTmux bool   `json:"kill_tmux,omitempty"` // also kill underlying tmux session
}

// RenameSessionRequest is the payload for FrameRenameSession.
type RenameSessionRequest struct {
	OldName string `json:"old_name"`
	NewName string `json:"new_name"`
}

// CreateTabRequest is the payload for FrameCreateTab.
type CreateTabRequest struct {
	Session string   `json:"session"`          // session to add the tab to
	Name    string   `json:"name,omitempty"`   // tab name (auto-generated if empty)
	Command []string `json:"command,omitempty"` // command to run (default shell if empty)
	Dir     string   `json:"dir,omitempty"`    // working directory
	Env     []string `json:"env,omitempty"`    // environment variables
}

// CloseTabRequest is the payload for FrameCloseTab.
type CloseTabRequest struct {
	Session string `json:"session"` // session name
	Index   int    `json:"index"`   // tab index to close
}

// SwitchTabRequest is the payload for FrameSwitchTab.
type SwitchTabRequest struct {
	Session string `json:"session"` // session name
	Index   int    `json:"index"`   // tab index to switch to
}

// TabEvent describes a tab lifecycle change (sent via FrameSessionEvent).
type TabEvent struct {
	Type      string  `json:"type"`               // "tab_created", "tab_closed", "tab_switched"
	Session   string  `json:"session"`            // session name
	TabIndex  int     `json:"tab_index"`          // affected tab index
	TabName   string  `json:"tab_name,omitempty"` // tab name
	ActiveTab int     `json:"active_tab"`         // new active tab index
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
