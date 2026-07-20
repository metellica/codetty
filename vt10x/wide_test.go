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

// Overwriting one half of a wide character must erase the other half so no
// orphaned lead or continuation cell is left behind (xterm/st semantics).
func TestWideCharOverwriteCleansOrphanedHalf(t *testing.T) {
	t.Run("overwrite continuation with wide char", func(t *testing.T) {
		term := New(WithSize(80, 24))
		term.Write([]byte("中")) // 中 at cols 0-1
		term.Write([]byte("\b")) // cursor 2 -> 1 (continuation cell)
		term.Write([]byte("你")) // write 你 over the continuation cell
		term.Lock()
		defer term.Unlock()
		// 中's orphaned lead must be blanked.
		if g := term.Cell(0, 0); g.Char != ' ' {
			t.Errorf("Cell(0,0) = %q, want ' ' (erased orphan lead)", g.Char)
		}
		if g := term.Cell(1, 0); g.Char != '你' {
			t.Errorf("Cell(1,0) = %q, want '你'", g.Char)
		}
		if g := term.Cell(2, 0); g.Char != 0 {
			t.Errorf("Cell(2,0) = %q, want 0 (continuation)", g.Char)
		}
	})

	t.Run("overwrite wide lead with narrow char", func(t *testing.T) {
		term := New(WithSize(80, 24))
		term.Write([]byte("中好")) // 中(0-1) 好(2-3)
		term.Write([]byte("\r"))   // cursor -> col 0
		term.Write([]byte("a"))    // overwrite 中's lead with 'a'
		term.Lock()
		defer term.Unlock()
		if g := term.Cell(0, 0); g.Char != 'a' {
			t.Errorf("Cell(0,0) = %q, want 'a'", g.Char)
		}
		// 中's orphaned continuation must be blanked, not left as NUL.
		if g := term.Cell(1, 0); g.Char != ' ' {
			t.Errorf("Cell(1,0) = %q, want ' ' (erased orphan continuation)", g.Char)
		}
		if g := term.Cell(2, 0); g.Char != '好' {
			t.Errorf("Cell(2,0) = %q, want '好'", g.Char)
		}
	})
}

// DCH (CSI P) shifting a row left must not leave an orphan half of a wide
// character at either boundary of the shift.
func TestDeleteCharsCleansSplitWidePair(t *testing.T) {
	t.Run("delete the lead, continuation shifts", func(t *testing.T) {
		term := New(WithSize(80, 24))
		term.Write([]byte("你abc"))
		term.Write([]byte("\r\x1b[1P")) // cursor col 0, delete 1 cell (你's lead)
		term.Lock()
		defer term.Unlock()
		// 你's continuation would shift to col 0; it must be blanked.
		if g := term.Cell(0, 0); g.Char != ' ' {
			t.Errorf("Cell(0,0) = %q, want ' ' (blanked orphan continuation)", g.Char)
		}
		if g := term.Cell(1, 0); g.Char != 'a' {
			t.Errorf("Cell(1,0) = %q, want 'a'", g.Char)
		}
	})

	t.Run("delete starting on a continuation cell", func(t *testing.T) {
		term := New(WithSize(80, 24))
		term.Write([]byte("你abc"))
		term.Write([]byte("\x1b[1;2H\x1b[1P")) // cursor on 你's continuation, delete it
		term.Lock()
		defer term.Unlock()
		// 你's lead loses its continuation and must be blanked.
		if g := term.Cell(0, 0); g.Char != ' ' {
			t.Errorf("Cell(0,0) = %q, want ' ' (blanked orphan lead)", g.Char)
		}
		if g := term.Cell(1, 0); g.Char != 'a' {
			t.Errorf("Cell(1,0) = %q, want 'a'", g.Char)
		}
	})
}

// ICH (CSI @) inserting blanks at a continuation cell must blank both halves
// of the split wide character.
func TestInsertBlanksCleansSplitWidePair(t *testing.T) {
	term := New(WithSize(80, 24))
	term.Write([]byte("你abc"))
	term.Write([]byte("\x1b[1;2H\x1b[1@")) // cursor on 你's continuation, insert 1 blank
	term.Lock()
	defer term.Unlock()
	if g := term.Cell(0, 0); g.Char != ' ' {
		t.Errorf("Cell(0,0) = %q, want ' ' (blanked orphan lead)", g.Char)
	}
	if g := term.Cell(1, 0); g.Char != ' ' {
		t.Errorf("Cell(1,0) = %q, want ' ' (inserted blank)", g.Char)
	}
	if g := term.Cell(2, 0); g.Char != ' ' {
		t.Errorf("Cell(2,0) = %q, want ' ' (blanked shifted continuation)", g.Char)
	}
	if g := term.Cell(3, 0); g.Char != 'a' {
		t.Errorf("Cell(3,0) = %q, want 'a'", g.Char)
	}
}

// EL/ED (clear) starting or ending inside a wide character must blank the
// surviving half outside the cleared region.
func TestClearCleansSplitWidePair(t *testing.T) {
	t.Run("erase from continuation to end of line", func(t *testing.T) {
		term := New(WithSize(80, 24))
		term.Write([]byte("你abc"))
		term.Write([]byte("\x1b[1;2H\x1b[K")) // EL from 你's continuation cell
		term.Lock()
		defer term.Unlock()
		if g := term.Cell(0, 0); g.Char != ' ' {
			t.Errorf("Cell(0,0) = %q, want ' ' (blanked orphan lead)", g.Char)
		}
	})

	t.Run("erase from start of line to wide lead", func(t *testing.T) {
		term := New(WithSize(80, 24))
		term.Write([]byte("ab你c"))
		term.Write([]byte("\x1b[1;3H\x1b[1K")) // EL to cursor, cursor on 你's lead
		term.Lock()
		defer term.Unlock()
		// 你's continuation (col 3) survives the clear; it must be blanked.
		if g := term.Cell(3, 0); g.Char != ' ' {
			t.Errorf("Cell(3,0) = %q, want ' ' (blanked orphan continuation)", g.Char)
		}
		if g := term.Cell(4, 0); g.Char != 'c' {
			t.Errorf("Cell(4,0) = %q, want 'c'", g.Char)
		}
	})
}

// RIS (ESC c) must clear the whole screen, not just a rows x rows square.
func TestResetClearsWholeScreen(t *testing.T) {
	term := New(WithSize(20, 4))
	term.Write([]byte("\x1b[1;10Hhello你好")) // content beyond column rows-1
	term.Write([]byte("\x1bc"))
	term.Lock()
	defer term.Unlock()
	for x := 0; x < 20; x++ {
		if g := term.Cell(x, 0); g.Char != ' ' {
			t.Fatalf("Cell(%d,0) = %q after RIS, want ' '", x, g.Char)
		}
	}
}

// ICH with the cursor on a valid wide lead must shift the whole pair intact,
// keeping the NUL continuation, and must not orphan a lead at the truncation
// boundary in the last column.
func TestInsertBlanksKeepsValidWidePair(t *testing.T) {
	t.Run("pair shifts intact", func(t *testing.T) {
		term := New(WithSize(80, 24))
		term.Write([]byte("你abc"))
		term.Write([]byte("\r\x1b[1@")) // cursor on 你's lead, insert 1 blank
		term.Lock()
		defer term.Unlock()
		if g := term.Cell(0, 0); g.Char != ' ' {
			t.Errorf("Cell(0,0) = %q, want ' '", g.Char)
		}
		if g := term.Cell(1, 0); g.Char != '你' {
			t.Errorf("Cell(1,0) = %q, want '你'", g.Char)
		}
		if g := term.Cell(2, 0); g.Char != 0 {
			t.Errorf("Cell(2,0) = %q, want 0 (continuation preserved)", g.Char)
		}
		if g := term.Cell(3, 0); g.Char != 'a' {
			t.Errorf("Cell(3,0) = %q, want 'a'", g.Char)
		}
	})

	t.Run("truncated pair drops both halves", func(t *testing.T) {
		term := New(WithSize(5, 4))
		term.Write([]byte("abc你")) // 你 at cols 3-4
		term.Write([]byte("\r\x1b[1@")) // insert 1 blank at col 0: 你's continuation is cut
		term.Lock()
		defer term.Unlock()
		// The lead must not be copied orphaned into the last column.
		if g := term.Cell(4, 0); g.Char != ' ' {
			t.Errorf("Cell(4,0) = %q, want ' ' (blanked truncated lead)", g.Char)
		}
		if g := term.Cell(3, 0); g.Char != 'c' {
			t.Errorf("Cell(3,0) = %q, want 'c'", g.Char)
		}
	})
}

// RIS received while on the alternate screen must swap back to the primary
// buffer so the buffer stays aligned with the cleared ModeAltScreen flag.
func TestResetLeavesAltScreen(t *testing.T) {
	term := New(WithSize(20, 4))
	term.Write([]byte("primary"))
	term.Write([]byte("\x1b[?1049h")) // enter alt screen
	term.Write([]byte("alt"))
	term.Write([]byte("\x1bc")) // RIS
	term.Lock()
	if term.Mode()&ModeAltScreen != 0 {
		t.Error("ModeAltScreen still set after RIS")
	}
	term.Unlock()
	// Writing after RIS must land on (and stay on) the primary buffer:
	// entering and leaving the alt screen must restore it.
	term.Write([]byte("after"))
	term.Write([]byte("\x1b[?1049h\x1b[?1049l"))
	term.Lock()
	defer term.Unlock()
	got := ""
	for x := 0; x < 5; x++ {
		got += string(term.Cell(x, 0).Char)
	}
	if got != "after" {
		t.Errorf("primary screen after RIS+alt roundtrip = %q, want \"after\"", got)
	}
}
