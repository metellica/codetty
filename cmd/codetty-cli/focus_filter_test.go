package main

import (
	"bytes"
	"io"
	"testing"
)

func TestCopyLocalInputFiltersFocusEvents(t *testing.T) {
	var dst bytes.Buffer
	src := bytes.NewBufferString("before\033[O-middle\033[I-after")

	if err := copyLocalInput(&dst, src); err != nil {
		t.Fatalf("copyLocalInput: %v", err)
	}

	if got, want := dst.String(), "before-middle-after"; got != want {
		t.Fatalf("expected focus events to be filtered, got %q want %q", got, want)
	}
}

func TestCopyLocalInputPreservesNonFocusEscapeSequences(t *testing.T) {
	var dst bytes.Buffer
	src := bytes.NewBufferString("left\033[Dright\033[A")

	if err := copyLocalInput(&dst, src); err != nil {
		t.Fatalf("copyLocalInput: %v", err)
	}

	if got, want := dst.String(), "left\033[Dright\033[A"; got != want {
		t.Fatalf("expected non-focus escape sequences to be preserved, got %q want %q", got, want)
	}
}

func TestCopyLocalInputFiltersFocusEventsAcrossReads(t *testing.T) {
	var dst bytes.Buffer
	src := chunkReader{chunks: [][]byte{
		[]byte("before"),
		[]byte{0x1b},
		[]byte("["),
		[]byte("O"),
		[]byte("-after"),
	}}

	if err := copyLocalInput(&dst, &src); err != nil {
		t.Fatalf("copyLocalInput: %v", err)
	}

	if got, want := dst.String(), "before-after"; got != want {
		t.Fatalf("expected split focus event to be filtered, got %q want %q", got, want)
	}
}

type chunkReader struct {
	chunks [][]byte
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[0])
	r.chunks = r.chunks[1:]
	return n, nil
}
