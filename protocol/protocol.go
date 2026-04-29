package protocol

import "encoding/json"

type MsgType byte

const (
	// cli → hub
	MsgRegister MsgType = 'r'
	MsgOutput   MsgType = 'o'
	MsgReplay   MsgType = 'R' // ANSI screen snapshot for screen recovery

	// hub → cli
	MsgInput  MsgType = 'i'
	MsgResize MsgType = 's'
)

type RegisterPayload struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Cols  uint16 `json:"cols"`
	Rows  uint16 `json:"rows"`
}

type ResizePayload struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func EncodeRegister(p RegisterPayload) ([]byte, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return append([]byte{byte(MsgRegister)}, b...), nil
}

func DecodeRegister(data []byte) (RegisterPayload, error) {
	var p RegisterPayload
	err := json.Unmarshal(data, &p)
	return p, err
}

func EncodeOutput(data []byte) []byte {
	return append([]byte{byte(MsgOutput)}, data...)
}

func EncodeReplay(data []byte) []byte {
	return append([]byte{byte(MsgReplay)}, data...)
}

func EncodeInput(data []byte) []byte {
	return append([]byte{byte(MsgInput)}, data...)
}

func EncodeResize(cols, rows uint16) ([]byte, error) {
	b, err := json.Marshal(ResizePayload{Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	return append([]byte{byte(MsgResize)}, b...), nil
}

func DecodeResize(data []byte) (ResizePayload, error) {
	var p ResizePayload
	err := json.Unmarshal(data, &p)
	return p, err
}

// ParseType extracts the message type from a raw WebSocket message.
func ParseType(msg []byte) (MsgType, []byte, bool) {
	if len(msg) < 1 {
		return 0, nil, false
	}
	return MsgType(msg[0]), msg[1:], true
}
