//go:build windows

package main

import (
	"os"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/sys/windows"
)

// VT stdin reader for SSH sessions into Windows.
//
// Bubble Tea's Windows input path reads native INPUT_RECORDs with
// ENABLE_VIRTUAL_TERMINAL_INPUT cleared and only reports mouse events from
// native MOUSE_EVENT records. When codetty runs under Windows OpenSSH, the
// remote terminal's mouse arrives as VT (SGR) byte sequences through sshd's
// ConPTY — there is no real mouse to generate native records, so mouse never
// works. The psmux approach (ssh_input.rs): enable ENABLE_VIRTUAL_TERMINAL
// _INPUT and read the raw VT byte stream instead, parsing it like a Unix
// terminal. conhost passes VT mouse through to VTI clients on builds >=
// 22621 (microsoft/terminal#376); older builds drop it before any client
// can see it.
//
// vtStdinReader does that: it reads INPUT_RECORDs via ReadConsoleInputW
// (needed to still observe WINDOW_BUFFER_SIZE resize records, which the
// raw byte stream loses) and re-emits KEY_EVENT chars as UTF-8 bytes for
// Bubble Tea's ANSI parser. Native MOUSE_EVENT records (synthesized by
// newer conhosts, or a real conhost window) are re-encoded as SGR.

var procReadConsoleInputW = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReadConsoleInputW")

const (
	conKeyEvent          = 0x0001
	conMouseEvent        = 0x0002
	conWindowBufferEvent = 0x0004
)

// conRecord mirrors INPUT_RECORD: a 4-byte type header followed by a
// 16-byte event union, decoded manually per event type.
type conRecord struct {
	eventType uint16
	_         uint16
	data      [4]uint32
}

type vtStdinReader struct {
	conin       windows.Handle
	orig        uint32
	pend        []byte   // decoded bytes not yet consumed by Read
	u16         []uint16 // dangling UTF-16 high surrogate across batches
	prevButtons uint32
	resizes     chan tea.WindowSizeMsg
}

// newVTStdinReader returns a raw VT input reader when codetty should bypass
// Bubble Tea's native console input path, or nil to use the default. Enabled
// under SSH sessions (SSH_CONNECTION/SSH_CLIENT/SSH_TTY); CODETTY_VT_INPUT=1
// forces it on for local testing, =0 disables it.
func newVTStdinReader() *vtStdinReader {
	switch os.Getenv("CODETTY_VT_INPUT") {
	case "0", "false", "off":
		return nil
	case "1", "true", "on":
	default:
		if os.Getenv("SSH_CONNECTION") == "" && os.Getenv("SSH_CLIENT") == "" &&
			os.Getenv("SSH_TTY") == "" {
			return nil
		}
	}

	h := windows.Handle(os.Stdin.Fd())
	var orig uint32
	if windows.GetConsoleMode(h, &orig) != nil {
		return nil // stdin is not a console
	}
	mode := orig &^ uint32(windows.ENABLE_ECHO_INPUT|windows.ENABLE_LINE_INPUT|
		windows.ENABLE_PROCESSED_INPUT|windows.ENABLE_QUICK_EDIT_MODE)
	mode |= windows.ENABLE_EXTENDED_FLAGS | windows.ENABLE_WINDOW_INPUT |
		windows.ENABLE_MOUSE_INPUT | windows.ENABLE_VIRTUAL_TERMINAL_INPUT
	if windows.SetConsoleMode(h, mode) != nil {
		return nil
	}
	return &vtStdinReader{
		conin:   h,
		orig:    orig,
		resizes: make(chan tea.WindowSizeMsg, 8),
	}
}

func (r *vtStdinReader) Read(p []byte) (int, error) {
	for len(r.pend) == 0 {
		var recs [64]conRecord
		var n uint32
		ret, _, err := procReadConsoleInputW.Call(uintptr(r.conin),
			uintptr(unsafe.Pointer(&recs[0])), uintptr(len(recs)),
			uintptr(unsafe.Pointer(&n)))
		if ret == 0 {
			return 0, err
		}
		r.translate(recs[:n])
	}
	n := copy(p, r.pend)
	r.pend = r.pend[n:]
	return n, nil
}

// translate converts a batch of INPUT_RECORDs to bytes in r.pend.
func (r *vtStdinReader) translate(recs []conRecord) {
	u := r.u16
	for _, rec := range recs {
		switch rec.eventType {
		case conKeyEvent:
			// KEY_EVENT_RECORD: data[0]=bKeyDown, data[1]=repeat|vk<<16,
			// data[2]=scan|char<<16, data[3]=controlKeyState.
			if rec.data[0] == 0 {
				continue // key-up
			}
			ch := uint16(rec.data[2] >> 16)
			if ch == 0 {
				continue // bare modifier / VK-only event
			}
			repeat := int(uint16(rec.data[1]))
			if repeat < 1 {
				repeat = 1
			}
			for range repeat {
				u = append(u, ch)
			}
		case conMouseEvent:
			// MOUSE_EVENT_RECORD: data[0]=COORD{x,y}, data[1]=buttonState,
			// data[2]=controlKeyState, data[3]=eventFlags.
			u = r.flushU16(u)
			x := int16(rec.data[0])
			y := int16(rec.data[0] >> 16)
			seq, next := sgrFromConsoleMouse(
				r.prevButtons, rec.data[1], rec.data[3], rec.data[2], x, y)
			r.prevButtons = next
			r.pend = append(r.pend, seq...)
		case conWindowBufferEvent:
			w := int(int16(rec.data[0]))
			h := int(int16(rec.data[0] >> 16))
			select {
			case r.resizes <- tea.WindowSizeMsg{Width: w, Height: h}:
			default:
			}
		}
	}
	r.u16 = r.flushU16(u)
}

// flushU16 decodes buffered UTF-16 units into r.pend, keeping a trailing
// unpaired high surrogate for the next batch.
func (r *vtStdinReader) flushU16(u []uint16) []uint16 {
	if len(u) == 0 {
		return u[:0]
	}
	keep := 0
	if last := u[len(u)-1]; last >= 0xD800 && last < 0xDC00 {
		keep = 1
	}
	for _, rn := range utf16.Decode(u[: len(u)-keep : len(u)-keep]) {
		r.pend = utf8.AppendRune(r.pend, rn)
	}
	if keep == 1 {
		u[0] = u[len(u)-1]
	}
	return u[:keep]
}

// forwardResizes delivers WINDOW_BUFFER_SIZE events to the program. The raw
// byte stream carries no resize information, so without this the TUI would
// never learn about terminal size changes.
func (r *vtStdinReader) forwardResizes(p *tea.Program) {
	go func() {
		for msg := range r.resizes {
			p.Send(msg)
		}
	}()
}

// restore resets the console input mode changed by newVTStdinReader.
func (r *vtStdinReader) restore() {
	windows.SetConsoleMode(r.conin, r.orig)
}
