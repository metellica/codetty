package main

import (
	"io"

	"github.com/charmbracelet/x/ansi"
)

// runWithAutowrapDisabled disables the terminal's automatic line wrap (DECAWM,
// private mode 7) for the duration of fn, restoring it afterwards.
//
// Bubble Tea renders each frame by homing the cursor once and then writing one
// rendered line per "\r\n", relying on RELATIVE cursor motion. It truncates
// every line to the terminal width using its own (uniseg) width model. When the
// real terminal renders a glyph wider than that model expects — most commonly
// East Asian "Ambiguous" width characters (e.g. U+2022 "•", "…", some arrows)
// in a CJK-configured terminal — the physical line overflows the right margin.
// With autowrap on, the terminal wraps the overflowing cell onto a new line and
// Bubble Tea then emits its own line break, advancing an extra line and
// scrolling the whole frame upward (the bottom border and status bar scroll off
// the screen). Bubble Tea's per-line diff then preserves the misaligned state
// until a full repaint.
//
// DECAWM is a global terminal mode and is not reset when Bubble Tea switches to
// the alternate screen buffer (CSI ?1049h), so disabling it before the program
// runs keeps it disabled for the whole session. With autowrap off the terminal
// clips the overflowing cell instead of wrapping, keeping every frame aligned.
// The restore is deferred so it runs on normal exit, error, and panic unwind.
func runWithAutowrapDisabled(out io.Writer, fn func() error) error {
	_, _ = io.WriteString(out, ansi.ResetAutoWrapMode)
	defer func() { _, _ = io.WriteString(out, ansi.SetAutoWrapMode) }()
	return fn()
}
