package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
)

// newScrollTestModel returns a list-mode model whose focused session has
// content in the scrollback buffer.
func newScrollTestModel(t *testing.T) *tuiModel {
	t.Helper()
	m := newTUIModel(nil, "", nil)
	m.width = 120
	m.height = 40
	sess := newScrollbackSession(t)
	m.sessions = []*tuiSession{sess}
	m.order = []string{sess.Name}
	m.mode = modeList
	m.selected = 0
	return m
}

func TestScrollMode_PrefixBracketEntersAndPagesUp(t *testing.T) {
	m := newScrollTestModel(t)
	sess := m.sessions[0]

	m.prefix = true
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	if !m.scrolling {
		t.Fatal("scrolling should be true after prefix+[")
	}
	if !sess.IsScrolledBack() {
		t.Fatal("entering scroll mode should page up into scrollback")
	}
}

func TestScrollMode_PrefixPgUpEnters(t *testing.T) {
	m := newScrollTestModel(t)

	m.prefix = true
	m.handleKey(tea.KeyMsg{Type: tea.KeyPgUp})
	if !m.scrolling {
		t.Fatal("scrolling should be true after prefix+PgUp")
	}
}

func TestScrollMode_NoScrollbackShowsNotice(t *testing.T) {
	m := newScrollTestModel(t)
	m.sessions[0] = newTUISession("empty", "bash", 20, 4)
	m.order = []string{"empty"}

	m.prefix = true
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	if m.scrolling {
		t.Fatal("scroll mode must not start without scrollback")
	}
	if m.notice == "" {
		t.Fatal("expected a status notice when there is no scrollback")
	}
}

func TestScrollMode_NavigationKeys(t *testing.T) {
	m := newScrollTestModel(t)
	sess := m.sessions[0]
	m.prefix = true
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})

	offset, _ := sess.ScrollPos()
	m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	if got, _ := sess.ScrollPos(); got != offset+1 {
		t.Fatalf("offset after Up = %d, want %d", got, offset+1)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if got, _ := sess.ScrollPos(); got != offset {
		t.Fatalf("offset after Down = %d, want %d", got, offset)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if got, total := sess.ScrollPos(); got != total {
		t.Fatalf("offset after g = %d, want top %d", got, total)
	}

	// q exits and snaps back to the live view.
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if m.scrolling {
		t.Fatal("q should leave scroll mode")
	}
	if sess.IsScrolledBack() {
		t.Fatal("q should snap back to the live view")
	}
}

func TestScrollMode_ScrollingToLiveExits(t *testing.T) {
	m := newScrollTestModel(t)
	sess := m.sessions[0]
	m.prefix = true
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})

	// Page down repeatedly until the live view is reached.
	for i := 0; i < 50 && m.scrolling; i++ {
		m.handleKey(tea.KeyMsg{Type: tea.KeyPgDown})
	}
	if m.scrolling {
		t.Fatal("reaching the live view should leave scroll mode")
	}
	if sess.IsScrolledBack() {
		t.Fatal("session should be at the live view")
	}
}

func TestScrollMode_OtherKeyExitsAndSnapsLive(t *testing.T) {
	m := newScrollTestModel(t)
	sess := m.sessions[0]
	m.prefix = true
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})

	// A non-scroll key (typing) leaves scroll mode and snaps to live.
	// With no daemon connection the key itself is dropped, but the mode
	// transition must still happen.
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.scrolling {
		t.Fatal("non-scroll key should leave scroll mode")
	}
	if sess.IsScrolledBack() {
		t.Fatal("non-scroll key should snap back to the live view")
	}
}

func TestScrollMode_StatusBarShowsIndicator(t *testing.T) {
	m := newScrollTestModel(t)
	m.prefix = true
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})

	bar := m.statusBar()
	if !strings.Contains(xansi.Strip(bar), "[scroll ") {
		t.Fatalf("status bar missing scroll indicator: %q", bar)
	}
}
