//go:build windows

package terminal

import (
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Native console input injection. conhost builds before 22621 drop VT mouse
// sequences written to the ConPTY input pipe, so mouse events must be
// delivered as MOUSE_EVENT input records straight into the child's console
// input buffer. Reaching that buffer from the daemon requires the
// FreeConsole → AttachConsole(childPid) → CreateFile("CONIN$") dance, which
// mutates process-global console state and therefore holds consoleStateMu
// (shared with ConPTY spawning).

var (
	modkernel32           = windows.NewLazySystemDLL("kernel32.dll")
	procAttachConsole     = modkernel32.NewProc("AttachConsole")
	procFreeConsole       = modkernel32.NewProc("FreeConsole")
	procWriteConsoleInput = modkernel32.NewProc("WriteConsoleInputW")
)

const (
	mouseEventRecordType = 0x0002

	fromLeft1stButtonPressed = 0x0001
	rightmostButtonPressed   = 0x0002
	fromLeft2ndButtonPressed = 0x0004

	mouseMoved   = 0x0001
	mouseWheeled = 0x0004

	shiftPressed    = 0x0010
	leftCtrlPressed = 0x0008
	leftAltPressed  = 0x0002
)

// inputRecord mirrors INPUT_RECORD for MOUSE_EVENT: a 4-byte type header
// (uint16 + alignment padding) followed by MOUSE_EVENT_RECORD.
type inputRecord struct {
	eventType uint16
	_         uint16
	x, y      int16  // MousePosition (0-based cells)
	button    uint32 // dwButtonState
	control   uint32 // dwControlKeyState
	flags     uint32 // dwEventFlags
}

// withChildConsole attaches to pid's console, opens CONIN$, runs fn, and
// restores this process to a console-less state. Serialized against ConPTY
// spawning via consoleStateMu.
func withChildConsole(pid uint32, fn func(conin windows.Handle) error) error {
	consoleStateMu.Lock()
	defer consoleStateMu.Unlock()

	procFreeConsole.Call()
	r, _, err := procAttachConsole.Call(uintptr(pid))
	if r == 0 {
		return err
	}
	defer procFreeConsole.Call()

	name, _ := windows.UTF16PtrFromString("CONIN$")
	h, cerr := windows.CreateFile(name,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, 0, 0)
	if cerr != nil {
		return cerr
	}
	defer windows.CloseHandle(h)
	return fn(h)
}

// childConsoleVTI reports whether the child console has
// ENABLE_VIRTUAL_TERMINAL_INPUT set (a VT-aware TUI is reading input, so
// raw VT mouse bytes on the pipe reach it verbatim).
func childConsoleVTI(pid uint32) (vti bool, ok bool) {
	err := withChildConsole(pid, func(conin windows.Handle) error {
		var mode uint32
		if err := windows.GetConsoleMode(conin, &mode); err != nil {
			return err
		}
		vti = mode&windows.ENABLE_VIRTUAL_TERMINAL_INPUT != 0
		return nil
	})
	return vti, err == nil
}

// injectMouseRecords writes MOUSE_EVENT input records into pid's console.
func injectMouseRecords(pid uint32, recs []inputRecord) bool {
	if len(recs) == 0 {
		return true
	}
	err := withChildConsole(pid, func(conin windows.Handle) error {
		var written uint32
		r, _, callErr := procWriteConsoleInput.Call(
			uintptr(conin),
			uintptr(unsafe.Pointer(&recs[0])),
			uintptr(len(recs)),
			uintptr(unsafe.Pointer(&written)))
		if r == 0 {
			return callErr
		}
		return nil
	})
	return err == nil
}

// mouseInjector converts decoded VT mouse events into native input records,
// tracking held-button state and throttling motion events.
type mouseInjector struct {
	mu       sync.Mutex
	buttons  uint32
	lastMove time.Time

	vtiMu      sync.Mutex
	vtiVal     bool
	vtiChecked time.Time
}

const (
	dragThrottle = 16 * time.Millisecond
	vtiCacheTTL  = 500 * time.Millisecond
)

// childVTI returns the cached VTI mode of the child console.
func (m *mouseInjector) childVTI(pid uint32) bool {
	m.vtiMu.Lock()
	defer m.vtiMu.Unlock()
	if time.Since(m.vtiChecked) < vtiCacheTTL {
		return m.vtiVal
	}
	vti, ok := childConsoleVTI(pid)
	if ok {
		m.vtiVal = vti
		m.vtiChecked = time.Now()
	}
	return m.vtiVal
}

// record converts ev to an INPUT_RECORD. Returns ok=false for events that
// should be dropped (throttled motion).
func (m *mouseInjector) record(ev *mouseSeqEvent) (inputRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec := inputRecord{
		eventType: mouseEventRecordType,
		x:         int16(ev.x - 1),
		y:         int16(ev.y - 1),
	}
	if ev.btn&4 != 0 {
		rec.control |= shiftPressed
	}
	if ev.btn&8 != 0 {
		rec.control |= leftAltPressed
	}
	if ev.btn&16 != 0 {
		rec.control |= leftCtrlPressed
	}

	switch {
	case ev.btn&64 != 0: // wheel
		rec.flags = mouseWheeled
		delta := int32(120)
		if ev.btn&1 != 0 { // 65 = wheel down
			delta = -120
		}
		rec.button = m.buttons | uint32(delta)<<16
	case ev.btn&32 != 0: // motion
		now := time.Now()
		if now.Sub(m.lastMove) < dragThrottle {
			return rec, false
		}
		m.lastMove = now
		rec.flags = mouseMoved
		rec.button = m.buttons
	default:
		var bit uint32
		switch ev.btn & 3 {
		case 0:
			bit = fromLeft1stButtonPressed
		case 1:
			bit = fromLeft2ndButtonPressed
		case 2:
			bit = rightmostButtonPressed
		}
		if ev.release {
			m.buttons &^= bit
		} else {
			m.buttons |= bit
		}
		rec.button = m.buttons
	}
	return rec, true
}
