package terminal

import (
	"fmt"
	"strings"

	"codetty/vt10x"
)

// mouseEvent describes one synthetic mouse event: the X10 button code and
// whether it is a press or a release.
type mouseEvent struct {
	button  int
	release bool
}

// mouseEvents maps event names to the sequences of press/release events they
// generate. Button codes follow xterm: 0/1/2 = left/middle/right, +32 for
// motion, 3+32 for motion with no button, 64/65 for wheel.
var mouseEvents = map[string][]mouseEvent{
	"down1":     {{button: 0}},
	"down2":     {{button: 1}},
	"down3":     {{button: 2}},
	"up1":       {{button: 0, release: true}},
	"up2":       {{button: 1, release: true}},
	"up3":       {{button: 2, release: true}},
	"click1":    {{button: 0}, {button: 0, release: true}},
	"click2":    {{button: 1}, {button: 1, release: true}},
	"click3":    {{button: 2}, {button: 2, release: true}},
	"drag1":     {{button: 32}},
	"drag2":     {{button: 33}},
	"drag3":     {{button: 34}},
	"move":      {{button: 35}},
	"wheelup":   {{button: 64}},
	"wheeldown": {{button: 65}},
}

// MouseEventNames returns the supported send-mouse event names.
func MouseEventNames() []string {
	return []string{
		"down1", "down2", "down3", "up1", "up2", "up3",
		"click1", "click2", "click3", "drag1", "drag2", "drag3",
		"move", "wheelup", "wheeldown",
	}
}

// EncodeMouseEvent encodes a named mouse event at 1-based cell coordinates
// (x, y) using the encoding the application selected via DECSET: SGR (1006)
// when enabled, legacy X10 bytes otherwise. Returns an error if the
// application has not enabled mouse reporting or the event name is unknown.
func EncodeMouseEvent(mode vt10x.ModeFlag, event string, x, y int) ([]byte, error) {
	events, ok := mouseEvents[strings.ToLower(event)]
	if !ok {
		return nil, fmt.Errorf("unknown mouse event %q (supported: %s)",
			event, strings.Join(MouseEventNames(), " "))
	}
	if mode&vt10x.ModeMouseMask == 0 {
		return nil, fmt.Errorf("mouse reporting not enabled in this tab")
	}
	if x < 1 || y < 1 {
		return nil, fmt.Errorf("coordinates are 1-based, got %d,%d", x, y)
	}

	var b strings.Builder
	for _, ev := range events {
		if mode&vt10x.ModeMouseSgr != 0 {
			final := byte('M')
			if ev.release {
				final = 'm'
			}
			fmt.Fprintf(&b, "\x1b[<%d;%d;%d%c", ev.button, x, y, final)
			continue
		}
		// Legacy encoding: coordinates are offset by 32 and limited to
		// a single byte (223); releases report button 3.
		if x > 223 || y > 223 {
			return nil, fmt.Errorf("coordinates beyond 223 need SGR mouse mode (DECSET 1006)")
		}
		btn := ev.button
		if ev.release {
			btn = (btn &^ 3) | 3
		}
		b.WriteString("\x1b[M")
		b.WriteByte(byte(32 + btn))
		b.WriteByte(byte(32 + x))
		b.WriteByte(byte(32 + y))
	}
	return []byte(b.String()), nil
}
