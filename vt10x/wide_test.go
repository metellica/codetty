package vt10x

import (
	"testing"
)

func TestWideCharacterAdvancesCursorBy2(t *testing.T) {
	term := New(WithSize(80, 24))
	// Write a CJK character (你, width 2)
	term.Write([]byte("你"))
	term.Lock()
	defer term.Unlock()

	// Cell 0 should contain '你'
	g := term.Cell(0, 0)
	if g.Char != '你' {
		t.Errorf("Cell(0,0) = %q, want '你'", g.Char)
	}

	// Cell 1 should be a continuation cell (NUL)
	g1 := term.Cell(1, 0)
	if g1.Char != 0 {
		t.Errorf("Cell(1,0) = %q, want 0 (continuation)", g1.Char)
	}

	// Cursor should be at column 2
	cur := term.Cursor()
	if cur.X != 2 {
		t.Errorf("cursor.X = %d, want 2", cur.X)
	}
}

func TestTwoWideCharsRenderCorrectly(t *testing.T) {
	term := New(WithSize(80, 24))
	// Write "你好" — two CJK characters, each width 2
	term.Write([]byte("你好"))
	term.Lock()
	defer term.Unlock()

	// Check cells
	g0 := term.Cell(0, 0)
	if g0.Char != '你' {
		t.Errorf("Cell(0,0) = %q, want '你'", g0.Char)
	}
	g1 := term.Cell(1, 0)
	if g1.Char != 0 {
		t.Errorf("Cell(1,0) = %q, want 0 (continuation)", g1.Char)
	}
	g2 := term.Cell(2, 0)
	if g2.Char != '好' {
		t.Errorf("Cell(2,0) = %q, want '好'", g2.Char)
	}
	g3 := term.Cell(3, 0)
	if g3.Char != 0 {
		t.Errorf("Cell(3,0) = %q, want 0 (continuation)", g3.Char)
	}

	// Cursor at column 4
	cur := term.Cursor()
	if cur.X != 4 {
		t.Errorf("cursor.X = %d, want 4", cur.X)
	}
}

func TestMixedASCIIAndWideChars(t *testing.T) {
	term := New(WithSize(80, 24))
	// "a你b" — 1 + 2 + 1 = 4 columns
	term.Write([]byte("a你b"))
	term.Lock()
	defer term.Unlock()

	if g := term.Cell(0, 0); g.Char != 'a' {
		t.Errorf("Cell(0,0) = %q, want 'a'", g.Char)
	}
	if g := term.Cell(1, 0); g.Char != '你' {
		t.Errorf("Cell(1,0) = %q, want '你'", g.Char)
	}
	if g := term.Cell(2, 0); g.Char != 0 {
		t.Errorf("Cell(2,0) = %q, want 0 (continuation)", g.Char)
	}
	if g := term.Cell(3, 0); g.Char != 'b' {
		t.Errorf("Cell(3,0) = %q, want 'b'", g.Char)
	}

	cur := term.Cursor()
	if cur.X != 4 {
		t.Errorf("cursor.X = %d, want 4", cur.X)
	}
}
