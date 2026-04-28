package protocol

import (
	"testing"
)

func TestEncodeDecodeRegister(t *testing.T) {
	orig := RegisterPayload{
		ID:    "machine1",
		Title: "claude@machine1",
		Cols:  220,
		Rows:  50,
	}
	encoded, err := EncodeRegister(orig)
	if err != nil {
		t.Fatalf("EncodeRegister: %v", err)
	}
	if MsgType(encoded[0]) != MsgRegister {
		t.Fatalf("expected type %c, got %c", MsgRegister, encoded[0])
	}
	decoded, err := DecodeRegister(encoded[1:])
	if err != nil {
		t.Fatalf("DecodeRegister: %v", err)
	}
	if decoded != orig {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", decoded, orig)
	}
}

func TestEncodeDecodeResize(t *testing.T) {
	encoded, err := EncodeResize(120, 40)
	if err != nil {
		t.Fatalf("EncodeResize: %v", err)
	}
	if MsgType(encoded[0]) != MsgResize {
		t.Fatalf("expected type %c, got %c", MsgResize, encoded[0])
	}
	decoded, err := DecodeResize(encoded[1:])
	if err != nil {
		t.Fatalf("DecodeResize: %v", err)
	}
	if decoded.Cols != 120 || decoded.Rows != 40 {
		t.Fatalf("round-trip mismatch: got %+v", decoded)
	}
}

func TestEncodeOutput(t *testing.T) {
	data := []byte("hello world")
	encoded := EncodeOutput(data)
	if MsgType(encoded[0]) != MsgOutput {
		t.Fatalf("expected type %c, got %c", MsgOutput, encoded[0])
	}
	if string(encoded[1:]) != "hello world" {
		t.Fatalf("payload mismatch: got %q", encoded[1:])
	}
}

func TestEncodeInput(t *testing.T) {
	data := []byte("keystrokes")
	encoded := EncodeInput(data)
	if MsgType(encoded[0]) != MsgInput {
		t.Fatalf("expected type %c, got %c", MsgInput, encoded[0])
	}
	if string(encoded[1:]) != "keystrokes" {
		t.Fatalf("payload mismatch: got %q", encoded[1:])
	}
}

func TestParseType(t *testing.T) {
	tests := []struct {
		name    string
		msg     []byte
		wantTyp MsgType
		wantOK  bool
	}{
		{"register", []byte("r{\"id\":\"x\"}"), MsgRegister, true},
		{"output", []byte("ohello"), MsgOutput, true},
		{"input", []byte("ikey"), MsgInput, true},
		{"resize", []byte("s{\"cols\":80}"), MsgResize, true},
		{"empty", []byte{}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typ, _, ok := ParseType(tt.msg)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && typ != tt.wantTyp {
				t.Fatalf("type = %c, want %c", typ, tt.wantTyp)
			}
		})
	}
}
