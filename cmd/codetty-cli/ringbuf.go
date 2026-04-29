package main

import "sync"

// RingBuffer is a fixed-size circular buffer for raw PTY output.
// Thread-safe: Write and Bytes can be called from different goroutines.
type RingBuffer struct {
	mu   sync.Mutex
	buf  []byte
	size int
	pos  int
	full bool
}

// NewRingBuffer creates a ring buffer with the given capacity in bytes.
func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		buf:  make([]byte, size),
		size: size,
	}
}

// Write appends data to the ring buffer, overwriting oldest data if full.
func (r *RingBuffer) Write(data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// If data is larger than buffer, only keep the tail
	if len(data) >= r.size {
		copy(r.buf, data[len(data)-r.size:])
		r.pos = 0
		r.full = true
		return
	}

	n := copy(r.buf[r.pos:], data)
	if n < len(data) {
		// Wrapped around
		copy(r.buf, data[n:])
		r.pos = len(data) - n
		r.full = true
	} else {
		r.pos += n
		if r.pos == r.size {
			r.pos = 0
			r.full = true
		}
	}
}

// Bytes returns the buffer contents in chronological order.
// Returns a new slice (safe to use without locking).
func (r *RingBuffer) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.full {
		if r.pos == 0 {
			return nil
		}
		out := make([]byte, r.pos)
		copy(out, r.buf[:r.pos])
		return out
	}

	out := make([]byte, r.size)
	n := copy(out, r.buf[r.pos:])
	copy(out[n:], r.buf[:r.pos])
	return out
}
