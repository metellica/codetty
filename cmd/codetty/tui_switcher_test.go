package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func newSwitcherTestModel() *tuiModel {
	m := newTUIModel(nil, "", nil)
	m.width = 120
	m.height = 40
	m.sessions = []*tuiSession{
		newTUISession("a", "bash", 80, 24),
		newTUISession("b", "bash", 80, 24),
		newTUISession("c", "bash", 80, 24),
	}
	m.order = []string{"a", "b", "c"}
	m.mode = modeFullscreen
	m.selected = 0
	return m
}

func TestShiftTab_FullscreenOpensSwitcher(t *testing.T) {
	m := newSwitcherTestModel()

	m.handleKey(tea.KeyMsg{Type: tea.KeyShiftTab})

	if !m.switcher.active {
		t.Fatal("switcher should be active after Shift+Tab in fullscreen")
	}
	if m.selected != 0 {
		t.Fatalf("selected = %d, want 0 (unchanged)", m.selected)
	}
	if m.mode != modeFullscreen {
		t.Fatalf("mode = %v, want %v", m.mode, modeFullscreen)
	}
}

func TestShiftTab_FullscreenSingleSessionNoSwitcher(t *testing.T) {
	m := newSwitcherTestModel()
	m.sessions = m.sessions[:1]
	m.order = m.order[:1]

	m.handleKey(tea.KeyMsg{Type: tea.KeyShiftTab})

	if m.switcher.active {
		t.Fatal("switcher should not open with a single session")
	}
}

func TestSwitcher_CycleAndEnterSwitches(t *testing.T) {
	m := newSwitcherTestModel()
	m.openSwitcher()

	// Candidates are [b, c]; cursor starts on b.
	m.handleKey(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.switcher.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.switcher.cursor)
	}
	// Wraps around.
	m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	if m.switcher.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 (wrapped)", m.switcher.cursor)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	if m.switcher.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 (backward wrap)", m.switcher.cursor)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})

	if m.switcher.active {
		t.Fatal("switcher should close after Enter")
	}
	if m.selected != 2 {
		t.Fatalf("selected = %d, want 2 (session c)", m.selected)
	}
	if m.mode != modeFullscreen {
		t.Fatalf("mode = %v, want fullscreen", m.mode)
	}
}

func TestSwitcher_EscClosesWithoutSwitching(t *testing.T) {
	m := newSwitcherTestModel()
	m.openSwitcher()

	m.handleKey(tea.KeyMsg{Type: tea.KeyEscape})

	if m.switcher.active {
		t.Fatal("switcher should close after Esc")
	}
	if m.selected != 0 {
		t.Fatalf("selected = %d, want 0 (unchanged)", m.selected)
	}
}

func TestSwitcher_DigitJump(t *testing.T) {
	m := newSwitcherTestModel()
	m.openSwitcher()

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})

	if m.switcher.active {
		t.Fatal("switcher should close after digit jump")
	}
	if m.selected != 2 {
		t.Fatalf("selected = %d, want 2", m.selected)
	}
}

func TestSwitcher_GridNavigation(t *testing.T) {
	// 6 sessions → 5 candidates; at 120x40 the grid is 3 cols × 2 rows:
	//   0 1 2
	//   3 4
	m := newSwitcherTestModel()
	m.sessions = append(m.sessions,
		newTUISession("d", "bash", 80, 24),
		newTUISession("e", "bash", 80, 24),
		newTUISession("f", "bash", 80, 24),
	)
	m.order = []string{"a", "b", "c", "d", "e", "f"}
	m.openSwitcher()

	if cols, rows, _, _ := m.switcherGrid(5); cols != 3 || rows != 2 {
		t.Fatalf("grid = %dx%d, want 3x2", cols, rows)
	}

	steps := []struct {
		key  tea.KeyMsg
		want int
		desc string
	}{
		{tea.KeyMsg{Type: tea.KeyDown}, 3, "down keeps column"},
		{tea.KeyMsg{Type: tea.KeyDown}, 0, "down past last row wraps to first row, same column"},
		{tea.KeyMsg{Type: tea.KeyUp}, 3, "up past first row wraps to last row with this column"},
		{tea.KeyMsg{Type: tea.KeyRight}, 4, "right keeps row"},
		{tea.KeyMsg{Type: tea.KeyRight}, 3, "right past row end wraps to row start"},
		{tea.KeyMsg{Type: tea.KeyLeft}, 4, "left past row start wraps to row end"},
		{tea.KeyMsg{Type: tea.KeyUp}, 1, "up keeps column"},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}}, 2, "l moves right"},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}, 2, "j in a column with a single cell stays"},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}}, 1, "h moves left"},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}}, 4, "k wraps to last row of column"},
	}
	for _, s := range steps {
		m.handleKey(s.key)
		if m.switcher.cursor != s.want {
			t.Fatalf("%s: cursor = %d, want %d", s.desc, m.switcher.cursor, s.want)
		}
	}
}

func TestSwitcher_ExcludesCurrentSession(t *testing.T) {
	m := newSwitcherTestModel()
	m.selected = 1

	cands := m.switcherCandidates()
	if len(cands) != 2 || cands[0] != 0 || cands[1] != 2 {
		t.Fatalf("candidates = %v, want [0 2]", cands)
	}
}

func TestSwitcher_ViewRendersPreviews(t *testing.T) {
	m := newSwitcherTestModel()
	m.openSwitcher()

	view := m.View()

	if !strings.Contains(view, "Switch Session") {
		t.Fatal("view should contain switcher header")
	}
	// Candidate panels are titled with visible index+1 and name.
	if !strings.Contains(view, "[2] b") || !strings.Contains(view, "[3] c") {
		t.Fatalf("view should list candidate sessions b and c")
	}
	// Current fullscreen session must not appear as a panel.
	if strings.Contains(view, "[1] a") {
		t.Fatal("view should not list the current session")
	}
}

func TestSwitcher_MouseClickSwitches(t *testing.T) {
	m := newSwitcherTestModel()
	m.openSwitcher()
	m.View() // populate hit-test geometry

	sw := m.switcher
	if sw.boxW <= 0 || sw.panelW <= 0 || sw.panelH <= 0 {
		t.Fatalf("geometry not recorded: %+v", sw)
	}

	// Candidates are [b, c] in a single column; click inside the second panel.
	m.Update(tea.MouseMsg{
		X:      sw.gridX + 1,
		Y:      sw.gridY + sw.panelH + 1,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})

	if m.switcher.active {
		t.Fatal("switcher should close after clicking a panel")
	}
	if m.selected != 2 {
		t.Fatalf("selected = %d, want 2 (session c)", m.selected)
	}
	if m.mode != modeFullscreen {
		t.Fatalf("mode = %v, want fullscreen", m.mode)
	}
}

func TestSwitcher_MouseClickOutsideCloses(t *testing.T) {
	m := newSwitcherTestModel()
	m.openSwitcher()
	m.View()

	m.Update(tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})

	if m.switcher.active {
		t.Fatal("switcher should close after clicking outside the box")
	}
	if m.selected != 0 {
		t.Fatalf("selected = %d, want 0 (unchanged)", m.selected)
	}
}

func TestSwitcher_MouseWheelCyclesHighlight(t *testing.T) {
	m := newSwitcherTestModel()
	m.openSwitcher()
	m.View()

	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if m.switcher.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 after wheel down", m.switcher.cursor)
	}
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if m.switcher.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 (wrapped)", m.switcher.cursor)
	}
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	if m.switcher.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 after wheel up wrap", m.switcher.cursor)
	}
	if !m.switcher.active {
		t.Fatal("wheel should not close the switcher")
	}
}
