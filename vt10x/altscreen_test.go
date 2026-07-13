package vt10x

import (
	"fmt"
	"testing"
)

// Regression: a redundant alt-screen reset (?1049l while already on the
// primary screen) must NOT swap to the alt screen. Previously `!set || !alt`
// swapped on every reset, so stray resets (e.g. Bubble Tea exit sequences,
// defensive app resets) corrupted the mode flag.
func TestAltScreenRedundantResetIsNoop(t *testing.T) {
	term := New(WithSize(80, 24))

	term.Write([]byte("\x1b[?1049l"))
	term.Lock()
	if term.Mode()&ModeAltScreen != 0 {
		t.Fatal("redundant ?1049l flipped terminal INTO alt screen")
	}
	term.Unlock()

	// Normal enter/exit still works.
	term.Write([]byte("\x1b[?1049h"))
	term.Lock()
	if term.Mode()&ModeAltScreen == 0 {
		t.Fatal("?1049h did not enter alt screen")
	}
	term.Unlock()

	term.Write([]byte("\x1b[?1049l"))
	term.Lock()
	if term.Mode()&ModeAltScreen != 0 {
		t.Fatal("?1049l did not leave alt screen")
	}
	term.Unlock()

	// Redundant set while already in alt screen must also be a no-op.
	term.Write([]byte("\x1b[?1049h\x1b[?1049h"))
	term.Lock()
	if term.Mode()&ModeAltScreen == 0 {
		t.Fatal("redundant ?1049h flipped terminal OUT of alt screen")
	}
	term.Unlock()
}

func TestScrollbackCaptureAndClear(t *testing.T) {
	term := New(WithSize(80, 5))
	for i := 0; i < 10; i++ {
		term.Write([]byte(fmt.Sprintf("line %d\r\n", i)))
	}

	term.Lock()
	if n := term.ScrollbackLen(); n == 0 {
		t.Fatal("expected scrollback lines after scrolling output")
	}
	term.ClearScrollback()
	if n := term.ScrollbackLen(); n != 0 {
		t.Fatalf("ScrollbackLen after clear = %d, want 0", n)
	}
	term.Unlock()
}

func TestScrollbackNotCapturedInAltScreen(t *testing.T) {
	term := New(WithSize(80, 5))
	term.Write([]byte("\x1b[?1049h"))
	for i := 0; i < 10; i++ {
		term.Write([]byte(fmt.Sprintf("alt line %d\r\n", i)))
	}
	term.Lock()
	defer term.Unlock()
	if n := term.ScrollbackLen(); n != 0 {
		t.Fatalf("alt screen output leaked into scrollback: %d lines", n)
	}
}

func TestScrollbackDisabled(t *testing.T) {
	term := New(WithSize(80, 5))
	term.DisableScrollback()
	for i := 0; i < 10; i++ {
		term.Write([]byte(fmt.Sprintf("line %d\r\n", i)))
	}
	term.Lock()
	defer term.Unlock()
	if n := term.ScrollbackLen(); n != 0 {
		t.Fatalf("scrollback captured despite being disabled: %d lines", n)
	}
}
