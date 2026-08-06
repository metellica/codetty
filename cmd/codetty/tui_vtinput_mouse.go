package main

import "fmt"

// Windows native MOUSE_EVENT_RECORD constants (dwEventFlags and
// dwControlKeyState). Defined in a portable file so the SGR re-encoding
// below can be unit tested on any platform.
const (
	conMouseMoved    = 0x0001
	conDoubleClick   = 0x0002
	conMouseWheeled  = 0x0004
	conMouseHWheeled = 0x0008

	conRightAltPressed  = 0x0001
	conLeftAltPressed   = 0x0002
	conRightCtrlPressed = 0x0004
	conLeftCtrlPressed  = 0x0008
	conShiftPressed     = 0x0010

	conLeftButton   = 0x0001 // FROM_LEFT_1ST_BUTTON_PRESSED
	conRightButton  = 0x0002 // RIGHTMOST_BUTTON_PRESSED
	conMiddleButton = 0x0004 // FROM_LEFT_2ND_BUTTON_PRESSED
)

// sgrFromConsoleMouse re-encodes a native Windows MOUSE_EVENT record as the
// xterm SGR (1006) sequences a Unix terminal would have sent, so they can be
// fed to Bubble Tea's ANSI input parser. prev is the previously held button
// state; the updated state is returned. x, y are the record's 0-based cell
// coordinates. Returns nil bytes for events that should be dropped (motion
// with no button held — cell-motion mode reports drags only).
func sgrFromConsoleMouse(prev, buttons, flags, ctrlState uint32, x, y int16) ([]byte, uint32) {
	mod := 0
	if ctrlState&conShiftPressed != 0 {
		mod += 4
	}
	if ctrlState&(conLeftAltPressed|conRightAltPressed) != 0 {
		mod += 8
	}
	if ctrlState&(conLeftCtrlPressed|conRightCtrlPressed) != 0 {
		mod += 16
	}
	sx, sy := int(x)+1, int(y)+1
	emit := func(btn int, release bool) []byte {
		final := byte('M')
		if release {
			final = 'm'
		}
		return fmt.Appendf(nil, "\x1b[<%d;%d;%d%c", btn+mod, sx, sy, final)
	}

	switch {
	case flags&(conMouseWheeled|conMouseHWheeled) != 0:
		delta := int16(buttons >> 16)
		btn := 64 // wheel up
		if flags&conMouseHWheeled != 0 {
			btn = 66 // wheel left
			if delta > 0 {
				btn = 67 // wheel right
			}
		} else if delta < 0 {
			btn = 65 // wheel down
		}
		return emit(btn, false), prev
	case flags&conMouseMoved != 0:
		btn, held := lowestSGRButton(buttons)
		if !held {
			return nil, buttons
		}
		return emit(32+btn, false), buttons
	default:
		// Press/release (flags 0 or DOUBLE_CLICK): diff held buttons.
		var out []byte
		for _, m := range [...]struct {
			bit uint32
			sgr int
		}{{conLeftButton, 0}, {conMiddleButton, 1}, {conRightButton, 2}} {
			if buttons&m.bit != 0 && prev&m.bit == 0 {
				out = append(out, emit(m.sgr, false)...)
			}
			if buttons&m.bit == 0 && prev&m.bit != 0 {
				out = append(out, emit(m.sgr, true)...)
			}
		}
		return out, buttons
	}
}

// lowestSGRButton maps the lowest held native button bit to its SGR button
// number (left 0, middle 1, right 2).
func lowestSGRButton(buttons uint32) (int, bool) {
	switch {
	case buttons&conLeftButton != 0:
		return 0, true
	case buttons&conMiddleButton != 0:
		return 1, true
	case buttons&conRightButton != 0:
		return 2, true
	}
	return 0, false
}
