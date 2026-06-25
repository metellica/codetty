package main

import (
	"bytes"
	"io"
	"os"
	"sync/atomic"
)

// imeWriter wraps the terminal output writer and appends a cursor-positioning
// sequence after each write.  This overrides the position that Bubble Tea
// parks the cursor at after every rendered frame (always the bottom-left
// corner with altscreen), moving the real OS cursor to the focused session's
// cursor position instead.
//
// Windows Terminal (and other terminals) uses the real cursor position to
// anchor the IME composition window.  Without this, Chinese/Japanese/Korean
// preedit text appears at the bottom-left of the screen rather than at the
// focused session's cursor.
//
// imeWriter implements term.File (Fd, Read, Write, Close) so that Bubble Tea
// can still query terminal dimensions and configure raw mode through the
// wrapped file descriptor.
type imeWriter struct {
	file    *os.File
	seq     atomic.Pointer[string] // desired ANSI CUP sequence, e.g. "\x1b[5;10H"
	enabled atomic.Bool            // false during shutdown to avoid corrupting the normal-screen cursor
}

var imeWriterEmptySeq = ""

func newImeWriter(f *os.File) *imeWriter {
	iw := &imeWriter{file: f}
	iw.seq.Store(&imeWriterEmptySeq)
	iw.enabled.Store(true)
	return iw
}

// setCursorSeq sets the ANSI CUP sequence to append after every write.
// seq should be "\x1b[row;colH" (1-indexed) or "" to disable.
// Safe to call concurrently with Write.
func (w *imeWriter) setCursorSeq(seq string) {
	w.seq.Store(&seq)
}

// disable stops appending the cursor sequence.  Call before Bubble Tea exits
// alt-screen so the normal-screen shell prompt cursor is not displaced.
func (w *imeWriter) disable() {
	w.enabled.Store(false)
}

// Write forwards p to the underlying file, then immediately appends the
// stored cursor-position sequence so the terminal cursor lands on the
// session's cursor cell rather than wherever Bubble Tea left it.
//
// When the alt-screen exit sequence (\x1b[?1049l) is detected in the write
// payload, appending is permanently disabled so the normal-buffer shell
// cursor is not displaced after CodeTTY exits.
func (w *imeWriter) Write(p []byte) (n int, err error) {
	// Detect alt-screen exit before writing so we don't append a CUP into
	// the normal screen buffer.
	if bytes.Contains(p, altScreenExitSeq) {
		w.disable()
	}
	n, err = w.file.Write(p)
	if err != nil {
		return
	}
	if w.enabled.Load() {
		if seq := w.seq.Load(); seq != nil && *seq != "" {
			// Best-effort: ignore errors; a missed frame is harmless.
			_, _ = io.WriteString(w.file, *seq)
		}
	}
	return
}

// altScreenExitSeq is the VT sequence Bubble Tea writes when leaving the
// alternate screen buffer (ansi.ResetAltScreenSaveCursorMode = \x1b[?1049l).
var altScreenExitSeq = []byte("\x1b[?1049l")

// Fd returns the file descriptor of the underlying file.
// Required by charmbracelet/x/term.File so Bubble Tea can query terminal
// dimensions and manage raw mode via the wrapped stdout fd.
func (w *imeWriter) Fd() uintptr { return w.file.Fd() }

// Read and Close forward to the underlying file to satisfy term.File.
func (w *imeWriter) Read(p []byte) (int, error)  { return w.file.Read(p) }
func (w *imeWriter) Close() error                 { return w.file.Close() }

