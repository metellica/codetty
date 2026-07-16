package daemon

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestFrameRoundTrip_Request(t *testing.T) {
	req := Request{
		Action:  ActionNewSession,
		Session: "work",
		Command: []string{"bash"},
		Cols:    80,
		Rows:    24,
	}
	buf := &bytes.Buffer{}
	if err := WriteFrame(buf, FrameRequest, MarshalJSON(req)); err != nil {
		t.Fatal(err)
	}
	ft, payload, err := ReadFrame(buf)
	if err != nil {
		t.Fatal(err)
	}
	if ft != FrameRequest {
		t.Fatalf("got frame type %d, want %d", ft, FrameRequest)
	}
	var got Request
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if got.Action != ActionNewSession || got.Session != "work" {
		t.Errorf("request mismatch: %+v", got)
	}
}

func TestFrameRoundTrip_Output(t *testing.T) {
	data := []byte("hello terminal output")
	buf := &bytes.Buffer{}
	if err := WriteFrame(buf, FrameOutput, data); err != nil {
		t.Fatal(err)
	}
	ft, payload, err := ReadFrame(buf)
	if err != nil {
		t.Fatal(err)
	}
	if ft != FrameOutput {
		t.Fatalf("got type %d, want %d", ft, FrameOutput)
	}
	if !bytes.Equal(payload, data) {
		t.Errorf("payload mismatch")
	}
}

func TestFrameRoundTrip_MultipleFrames(t *testing.T) {
	buf := &bytes.Buffer{}
	WriteFrame(buf, FrameInput, []byte("key1"))
	WriteFrame(buf, FrameInput, []byte("key2"))
	WriteFrame(buf, FrameDetach, nil)

	ft1, p1, _ := ReadFrame(buf)
	ft2, p2, _ := ReadFrame(buf)
	ft3, _, _ := ReadFrame(buf)

	if ft1 != FrameInput || string(p1) != "key1" {
		t.Errorf("frame 1 mismatch")
	}
	if ft2 != FrameInput || string(p2) != "key2" {
		t.Errorf("frame 2 mismatch")
	}
	if ft3 != FrameDetach {
		t.Errorf("frame 3 type mismatch: got %d", ft3)
	}
}

func TestFrameRoundTrip_EmptyPayload(t *testing.T) {
	buf := &bytes.Buffer{}
	if err := WriteFrame(buf, FrameDetach, nil); err != nil {
		t.Fatal(err)
	}
	ft, payload, err := ReadFrame(buf)
	if err != nil {
		t.Fatal(err)
	}
	if ft != FrameDetach {
		t.Fatalf("got type %d, want %d", ft, FrameDetach)
	}
	if len(payload) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(payload))
	}
}

func TestFrameRoundTrip_Resize(t *testing.T) {
	rp := ResizePayload{Cols: 120, Rows: 40}
	buf := &bytes.Buffer{}
	WriteFrame(buf, FrameResize, MarshalJSON(rp))

	ft, payload, err := ReadFrame(buf)
	if err != nil {
		t.Fatal(err)
	}
	if ft != FrameResize {
		t.Fatalf("wrong type: %d", ft)
	}
	var got ResizePayload
	json.Unmarshal(payload, &got)
	if got.Cols != 120 || got.Rows != 40 {
		t.Errorf("got %+v", got)
	}
}

func TestTaggedOutputRoundTrip(t *testing.T) {
	data := []byte("hello world")
	encoded := EncodeTaggedOutput("my-session", data)
	name, decoded, err := DecodeTaggedOutput(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if name != "my-session" {
		t.Errorf("name = %q, want my-session", name)
	}
	if string(decoded) != "hello world" {
		t.Errorf("data = %q, want hello world", string(decoded))
	}
}

func TestTaggedOutputEmptyPayload(t *testing.T) {
	encoded := EncodeTaggedOutput("sess", nil)
	name, data, err := DecodeTaggedOutput(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if name != "sess" || len(data) != 0 {
		t.Errorf("got name=%q data=%v", name, data)
	}
}

func TestTaggedOutputSessionNameTooLongPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for session name longer than 255 bytes")
		}
	}()

	EncodeTaggedOutput(strings.Repeat("a", 256), []byte("x"))
}

func TestNegotiateProto(t *testing.T) {
	// Legacy peer (no hello, treated as version 1) is below our minimum
	// since owner namespaces (v2) became mandatory: rejected.
	if _, ok := NegotiateProto(0, 0); ok {
		t.Fatal("legacy v1 peer should be rejected (MinProtocolVersion = 2)")
	}
	// Same version peer.
	v, ok := NegotiateProto(ProtocolVersion, MinProtocolVersion)
	if !ok || v != ProtocolVersion {
		t.Fatalf("same version = (%d, %v), want (%d, true)", v, ok, ProtocolVersion)
	}
	// Newer peer that still speaks our version: use ours.
	v, ok = NegotiateProto(ProtocolVersion+3, ProtocolVersion)
	if !ok || v != ProtocolVersion {
		t.Fatalf("newer peer = (%d, %v), want (%d, true)", v, ok, ProtocolVersion)
	}
	// Newer peer whose minimum is above our maximum: incompatible.
	if _, ok := NegotiateProto(ProtocolVersion+3, ProtocolVersion+2); ok {
		t.Fatal("expected incompatible ranges to fail")
	}
}

func TestIntersectCaps(t *testing.T) {
	// Legacy peer without a caps list gets the full local set.
	caps := IntersectCaps(nil)
	for _, c := range ProtocolCaps() {
		if !caps[c] {
			t.Fatalf("legacy peer missing cap %q", c)
		}
	}
	// Intersection keeps only shared names.
	caps = IntersectCaps([]string{"tagged_io", "future_thing"})
	if !caps["tagged_io"] || caps["future_thing"] || caps["tab_ops"] {
		t.Fatalf("intersection = %v", caps)
	}
	// An explicit empty list means no capabilities.
	if caps := IntersectCaps([]string{}); len(caps) != 0 {
		t.Fatalf("empty list = %v, want none", caps)
	}
}
