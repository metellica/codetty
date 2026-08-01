package terminal

import (
	"bytes"
	"testing"
)

func collectSegments(t *testing.T, f *mouseFilter, chunks ...[]byte) (plain []byte, events []*mouseSeqEvent) {
	t.Helper()
	for _, c := range chunks {
		for _, seg := range f.feed(c) {
			if seg.ev != nil {
				events = append(events, seg.ev)
			} else {
				plain = append(plain, seg.data...)
			}
		}
	}
	return plain, events
}

func TestMouseFilter_PlainPassthrough(t *testing.T) {
	var f mouseFilter
	in := []byte("hello \x1b[A\x1b[1;5C world \x1bOP")
	plain, events := collectSegments(t, &f, in)
	if len(events) != 0 {
		t.Fatalf("expected no events, got %d", len(events))
	}
	if !bytes.Equal(plain, in) {
		t.Fatalf("plain = %q, want %q", plain, in)
	}
}

func TestMouseFilter_SGRPress(t *testing.T) {
	var f mouseFilter
	plain, events := collectSegments(t, &f, []byte("\x1b[<0;10;5M"))
	if len(plain) != 0 {
		t.Fatalf("unexpected plain bytes %q", plain)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.btn != 0 || ev.x != 10 || ev.y != 5 || ev.release {
		t.Fatalf("bad event: %+v", ev)
	}
	if string(ev.raw) != "\x1b[<0;10;5M" {
		t.Fatalf("raw = %q", ev.raw)
	}
}

func TestMouseFilter_SGRRelease(t *testing.T) {
	var f mouseFilter
	_, events := collectSegments(t, &f, []byte("\x1b[<0;3;4m"))
	if len(events) != 1 || !events[0].release {
		t.Fatalf("expected release event, got %+v", events)
	}
}

func TestMouseFilter_MixedKeysAndMouse(t *testing.T) {
	var f mouseFilter
	plain, events := collectSegments(t, &f, []byte("ab\x1b[<65;7;8Mcd\x1b[<64;1;1Mef"))
	if string(plain) != "abcdef" {
		t.Fatalf("plain = %q", plain)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].btn != 65 || events[0].x != 7 || events[0].y != 8 {
		t.Fatalf("bad wheel event: %+v", events[0])
	}
}

func TestMouseFilter_SGRSplitAcrossWrites(t *testing.T) {
	var f mouseFilter
	plain, events := collectSegments(t, &f,
		[]byte("x\x1b[<0;1"), []byte("2;34M y"))
	if string(plain) != "x y" {
		t.Fatalf("plain = %q", plain)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].x != 12 || events[0].y != 34 {
		t.Fatalf("bad event: %+v", events[0])
	}
}

func TestMouseFilter_SGRSplitAtIntroducer(t *testing.T) {
	var f mouseFilter
	// "\x1b[<" is unambiguous mouse, must be buffered.
	plain, events := collectSegments(t, &f,
		[]byte("\x1b[<"), []byte("0;2;3M"))
	if len(plain) != 0 {
		t.Fatalf("plain = %q", plain)
	}
	if len(events) != 1 || events[0].x != 2 || events[0].y != 3 {
		t.Fatalf("bad events: %+v", events)
	}
}

func TestMouseFilter_AmbiguousEscTailNotBuffered(t *testing.T) {
	var f mouseFilter
	// Lone ESC / ESC-[ at chunk end are keys (e.g. Alt-, arrow prefix):
	// they must pass through immediately, never be delayed.
	plain, events := collectSegments(t, &f, []byte("q\x1b"))
	if string(plain) != "q\x1b" || len(events) != 0 {
		t.Fatalf("plain = %q events = %v", plain, events)
	}
	plain, events = collectSegments(t, &f, []byte("w\x1b["))
	if string(plain) != "w\x1b[" || len(events) != 0 {
		t.Fatalf("plain = %q events = %v", plain, events)
	}
}

func TestMouseFilter_X10(t *testing.T) {
	var f mouseFilter
	plain, events := collectSegments(t, &f, []byte("\x1b[M\x20\x25\x2a"))
	if len(plain) != 0 || len(events) != 1 {
		t.Fatalf("plain=%q events=%d", plain, len(events))
	}
	ev := events[0]
	if ev.btn != 0 || ev.x != 5 || ev.y != 10 || ev.release {
		t.Fatalf("bad event: %+v", ev)
	}
}

func TestMouseFilter_X10Release(t *testing.T) {
	var f mouseFilter
	_, events := collectSegments(t, &f, []byte("\x1b[M\x23\x21\x21"))
	if len(events) != 1 || !events[0].release {
		t.Fatalf("expected release, got %+v", events)
	}
}

func TestMouseFilter_X10SplitAcrossWrites(t *testing.T) {
	var f mouseFilter
	plain, events := collectSegments(t, &f, []byte("\x1b[M\x20"), []byte("\x21\x22z"))
	if string(plain) != "z" {
		t.Fatalf("plain = %q", plain)
	}
	if len(events) != 1 || events[0].x != 1 || events[0].y != 2 {
		t.Fatalf("bad events: %+v", events)
	}
}

func TestMouseFilter_InvalidSGRNotConsumed(t *testing.T) {
	var f mouseFilter
	in := []byte("\x1b[<abcM")
	plain, events := collectSegments(t, &f, in)
	if len(events) != 0 {
		t.Fatalf("unexpected events: %+v", events)
	}
	if !bytes.Equal(plain, in) {
		t.Fatalf("plain = %q, want %q", plain, in)
	}
}

func TestMouseFilter_OversizedPendingDropped(t *testing.T) {
	var f mouseFilter
	// A "mouse" prefix that keeps growing past maxPendingMouseSeq must not
	// buffer forever.
	long := append([]byte("\x1b[<"), bytes.Repeat([]byte("1"), maxPendingMouseSeq+8)...)
	plain, events := collectSegments(t, &f, long)
	if len(events) != 0 {
		t.Fatalf("unexpected events: %+v", events)
	}
	if !bytes.Equal(plain, long) {
		t.Fatalf("plain = %q, want %q", plain, long)
	}
	if len(f.pending) != 0 {
		t.Fatalf("pending should be empty, got %q", f.pending)
	}
}

func TestMouseFilter_CtrlCUnaffected(t *testing.T) {
	var f mouseFilter
	plain, events := collectSegments(t, &f, []byte{0x03})
	if len(events) != 0 || !bytes.Equal(plain, []byte{0x03}) {
		t.Fatalf("plain = %q events = %v", plain, events)
	}
}
