package main

import (
	"fmt"
	"testing"
)

// fillScrollback writes enough lines to push content into the scrollback.
func fillScrollback(s *tuiSession, n int) {
	for i := 0; i < n; i++ {
		s.WriteOutput([]byte(fmt.Sprintf("line %d\r\n", i)))
	}
}

func TestTabSwitchPreservesScrollback(t *testing.T) {
	s := newTUISession("test", "sh", 40, 5)
	s.Tabs = []tuiTab{{Name: "shell"}, {Name: "vim"}}
	s.ActiveTab = 0

	fillScrollback(s, 20)
	if !s.HasScrollback() {
		t.Fatal("expected scrollback after writing lines")
	}

	// Simulate tab switch shell -> vim: stash, reset, restore (vim has none).
	s.stashScrollback("shell")
	s.resetTerminal()
	s.restoreScrollback("vim")
	if s.HasScrollback() {
		t.Fatal("vim tab should start with empty scrollback")
	}

	// Switch back vim -> shell: scrollback must come back.
	s.stashScrollback("vim")
	s.resetTerminal()
	s.restoreScrollback("shell")
	if !s.HasScrollback() {
		t.Fatal("shell scrollback lost after switching back")
	}
}

func TestPruneScrollbackStash(t *testing.T) {
	s := newTUISession("test", "sh", 40, 5)
	s.Tabs = []tuiTab{{Name: "shell"}}
	fillScrollback(s, 20)
	s.stashScrollback("gone")
	if _, ok := s.tabScrollback["gone"]; !ok {
		t.Fatal("stash missing")
	}
	s.pruneScrollbackStash()
	if _, ok := s.tabScrollback["gone"]; ok {
		t.Fatal("stash for closed tab not pruned")
	}
}
