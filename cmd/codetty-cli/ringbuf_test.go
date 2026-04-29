package main

import (
	"bytes"
	"testing"
)

func TestRingBuffer_EmptyBytes(t *testing.T) {
	rb := NewRingBuffer(64)
	got := rb.Bytes()
	if len(got) != 0 {
		t.Fatalf("expected empty, got %d bytes", len(got))
	}
}

func TestRingBuffer_WriteWithinCapacity(t *testing.T) {
	rb := NewRingBuffer(64)
	rb.Write([]byte("hello"))
	rb.Write([]byte(" world"))

	got := rb.Bytes()
	if !bytes.Equal(got, []byte("hello world")) {
		t.Fatalf("got %q, want %q", got, "hello world")
	}
}

func TestRingBuffer_WrapAround(t *testing.T) {
	rb := NewRingBuffer(8)
	rb.Write([]byte("ABCDEFGH")) // fills exactly
	rb.Write([]byte("IJ"))       // overwrites A,B

	got := rb.Bytes()
	want := []byte("CDEFGHIJ")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRingBuffer_WriteLargerThanCapacity(t *testing.T) {
	rb := NewRingBuffer(4)
	rb.Write([]byte("ABCDEFGH"))

	got := rb.Bytes()
	want := []byte("EFGH")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRingBuffer_MultipleWraps(t *testing.T) {
	rb := NewRingBuffer(4)
	rb.Write([]byte("AB"))
	rb.Write([]byte("CD"))
	rb.Write([]byte("EF"))
	rb.Write([]byte("GH"))

	got := rb.Bytes()
	want := []byte("EFGH")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
