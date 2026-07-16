package terminal

import (
	"testing"

	"codetty/vt10x"
)

func TestEncodeMouseEvent(t *testing.T) {
	sgr := vt10x.ModeMouseButton | vt10x.ModeMouseSgr
	legacy := vt10x.ModeMouseButton

	cases := []struct {
		name  string
		mode  vt10x.ModeFlag
		event string
		x, y  int
		want  string
		err   bool
	}{
		{"sgr press", sgr, "down1", 10, 5, "\x1b[<0;10;5M", false},
		{"sgr release", sgr, "up1", 10, 5, "\x1b[<0;10;5m", false},
		{"sgr click = press+release", sgr, "click3", 2, 3, "\x1b[<2;2;3M\x1b[<2;2;3m", false},
		{"sgr wheel", sgr, "WheelUp", 1, 1, "\x1b[<64;1;1M", false},
		{"legacy press", legacy, "down1", 10, 5, "\x1b[M\x20\x2a\x25", false},
		{"legacy release reports button 3", legacy, "up1", 10, 5, "\x1b[M\x23\x2a\x25", false},
		{"legacy big coords rejected", legacy, "down1", 300, 5, "", true},
		{"sgr big coords ok", sgr, "down1", 300, 5, "\x1b[<0;300;5M", false},
		{"mouse off rejected", 0, "click1", 1, 1, "", true},
		{"unknown event", sgr, "tripleclick", 1, 1, "", true},
		{"zero coord rejected", sgr, "click1", 0, 1, "", true},
	}
	for _, c := range cases {
		got, err := EncodeMouseEvent(c.mode, c.event, c.x, c.y)
		if c.err {
			if err == nil {
				t.Errorf("%s: want error, got %q", c.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
		} else if string(got) != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
