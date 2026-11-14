package vt10x

import "testing"

// Kitty keyboard protocol queries/pushes and other prefixed CSI sequences
// share final bytes with cursor restore/SGR and must not move the cursor.
func TestPrefixedCSIDoesNotMoveCursor(t *testing.T) {
	for _, seq := range []string{"\x1b[?u", "\x1b[>1u", "\x1b[<u", "\x1b[=5;1u", "\x1b[?s", "\x1b[>0q", "\x1b[>c"} {
		term := New(WithSize(80, 24))
		term.Write([]byte("\x1b[10;20H" + seq))
		term.Lock()
		cur := term.Cursor()
		term.Unlock()
		if cur.X != 19 || cur.Y != 9 {
			t.Errorf("%q moved cursor to (%d,%d), want (19,9)", seq, cur.X, cur.Y)
		}
	}
}

func TestPrefixedSGRDoesNotResetAttrs(t *testing.T) {
	term := New(WithSize(80, 24))
	term.Write([]byte("\x1b[1m\x1b[>4;2mA"))
	term.Lock()
	defer term.Unlock()
	if term.Cell(0, 0).Mode&attrBold == 0 {
		t.Errorf("CSI > 4;2 m reset bold attribute")
	}
}

// XTQMODKEYS query (CSI ? 4 m, sent by lazygit) must not enable underline.
func TestPrivateSGRQueryDoesNotUnderline(t *testing.T) {
	term := New(WithSize(80, 24))
	term.Write([]byte("\x1b[?4mA"))
	term.Lock()
	defer term.Unlock()
	if term.Cell(0, 0).Mode&attrUnderline != 0 {
		t.Errorf("CSI ? 4 m enabled underline")
	}
}
