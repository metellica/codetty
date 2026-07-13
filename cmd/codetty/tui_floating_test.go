package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
)

func newFloatingTestModel() *tuiModel {
	m := newTUIModel(nil, "", nil)
	m.width = 100
	m.height = 30
	m.sessions = []*tuiSession{
		newTUISession("alpha", "bash", 80, 24),
		newTUISession("beta", "bash", 80, 24),
	}
	m.order = []string{"alpha", "beta"}
	m.mode = modeList
	m.selected = 0
	return m
}

// floatingInfoRow returns the row of the floating panel's bottom info line
// for the test model: panel top + panel height - 1 bottom border - 1.
func floatingInfoRow(m *tuiModel) int {
	fh := m.height * 8 / 10
	return (m.height-fh)/2 + fh - 2
}

func TestOverlayFloating_SessionInfoBottomRightNoTitle(t *testing.T) {
	m := newFloatingTestModel()
	m.floatingIdx = 0

	lines := strings.Split(xansi.Strip(m.overlayFloating("")), "\n")
	infoRow := floatingInfoRow(m)
	if infoRow >= len(lines) {
		t.Fatalf("view has %d lines, info row %d out of range", len(lines), infoRow)
	}
	if !strings.Contains(lines[infoRow], "[1] alpha  (^A q close)") {
		t.Fatalf("info line %d = %q, want session info at bottom", infoRow, lines[infoRow])
	}

	// No title header: the first row inside the panel is terminal content.
	fh := m.height * 8 / 10
	topRow := (m.height-fh)/2 + 1
	if strings.Contains(lines[topRow], "alpha") {
		t.Fatalf("top row %q must not contain a title", lines[topRow])
	}
}

func TestOverlayFloating_TabBarSharesInfoLine(t *testing.T) {
	m := newFloatingTestModel()
	m.sessions[0].Tabs = []tuiTab{{Name: "one"}, {Name: "two"}}
	m.sessions[0].ActiveTab = 0
	m.floatingIdx = 0

	lines := strings.Split(xansi.Strip(m.overlayFloating("")), "\n")
	line := lines[floatingInfoRow(m)]
	if !strings.Contains(line, "one") || !strings.Contains(line, "two") {
		t.Fatalf("info line %q must contain the tab bar", line)
	}
	if !strings.Contains(line, "[1] alpha") {
		t.Fatalf("info line %q must contain right-aligned session info", line)
	}
}

func TestFloatingContentRect_NoTitleRow(t *testing.T) {
	m := newFloatingTestModel()
	x, y, cw, ch := m.floatingContentRect()
	// fw=80, fh=24 centered in 100x30: content starts just inside the border.
	if x != 11 || y != 4 {
		t.Fatalf("content origin = (%d,%d), want (11,4)", x, y)
	}
	// Width minus borders; height minus borders and bottom info line.
	if cw != 78 || ch != 21 {
		t.Fatalf("content size = %dx%d, want 78x21", cw, ch)
	}
}

func TestFloatingMouse_InfoLineClickConsumedNotClosed(t *testing.T) {
	m := newFloatingTestModel()
	m.sessions[0].Tabs = []tuiTab{{Name: "one"}, {Name: "two"}}
	m.floatingIdx = 0

	cx, cy, _, ch := m.floatingContentRect()
	m.handleFloatingMouse(tea.MouseMsg{
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		X: cx + 1, Y: cy + ch,
	})
	if m.floatingIdx != 0 {
		t.Fatalf("floatingIdx = %d, want 0 (info-line click must not close)", m.floatingIdx)
	}

	// A click outside the panel still closes the floating window.
	m.handleFloatingMouse(tea.MouseMsg{
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		X: 0, Y: 0,
	})
	if m.floatingIdx != -1 {
		t.Fatalf("floatingIdx = %d, want -1 (outside click closes)", m.floatingIdx)
	}
}
