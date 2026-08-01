//go:build windows

// Package-internal ConPTY implementation. This replaces the former
// github.com/UserExistsError/conpty dependency so codetty can apply the
// ConPTY quirk workarounds that native Windows multiplexers (psmux,
// Windows Terminal) rely on:
//
//   - PSEUDOCONSOLE_RESIZE_QUIRK suppresses conhost's full-screen repaint
//     on every ResizePseudoConsole (flicker on pane layout changes).
//   - PSEUDOCONSOLE_WIN32_INPUT_MODE lets conhost accept high-fidelity
//     win32-input-mode sequences alongside plain VT input.
//   - PSEUDOCONSOLE_PASSTHROUGH_MODE (Win11 22621+) stops conhost from
//     re-emulating the child's output, so sequences like DECSCUSR and
//     DECTCEM survive to our vt10x mirror. Some builds reject spawning
//     with it (ERROR_INVALID_PARAMETER); we retry without it.
//   - PSEUDOCONSOLE_INHERIT_CURSOR is deliberately NOT used: it makes the
//     new conhost emit ESC[6n and block until someone answers, which
//     nobody does in a daemon, hanging the child inside process init.
//   - 64 KiB pipe buffers (default anonymous pipes are ~4 KiB) to avoid
//     kernel round-trips on high-throughput output.
//   - STARTF_USESTDHANDLES is NOT set: newer Win11 builds require it to
//     be paired with bInheritHandles=TRUE, and ConPTY routes stdio via
//     PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE anyway.
//   - Process exit is detected with a blocking WaitForSingleObject
//     instead of a 1s poll loop.
package terminal

import (
	"fmt"
	"io"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	pseudoConsoleInheritCursor  = 0x1
	pseudoConsoleResizeQuirk    = 0x2
	pseudoConsoleWin32InputMode = 0x4
	pseudoConsolePassthrough    = 0x8

	conptyPipeSize = 64 * 1024
)

// consoleStateMu serializes operations that mutate process-global console
// state: CreateProcess std-handle nulling and the FreeConsole/AttachConsole
// dance used for input injection. Without it a concurrent AttachConsole can
// stamp recycled handle values into a child's process parameters.
var consoleStateMu sync.Mutex

// windowsBuildNumber returns the OS build (e.g. 19045, 22631). RtlGetVersion
// is used because GetVersionEx lies without an application manifest.
var windowsBuildNumber = sync.OnceValue(func() uint32 {
	return windows.RtlGetVersion().BuildNumber
})

// conptyHasPassthrough reports whether this Windows build supports
// PSEUDOCONSOLE_PASSTHROUGH_MODE (and translates VT mouse input properly).
func conptyHasPassthrough() bool { return windowsBuildNumber() >= 22621 }

type conPty struct {
	hpc    windows.Handle
	pi     windows.ProcessInformation
	ptyIn  windows.Handle // conhost reads child input from here
	ptyOut windows.Handle // conhost writes child output here
	cmdIn  windows.Handle // we write child input here
	cmdOut windows.Handle // we read child output here

	closeOnce   sync.Once
	cmdOutOnce  sync.Once
	waitOnce    sync.Once
	procHandles sync.Once
	waitErr     error
}

func conptyBaseFlags() uint32 {
	flags := uint32(pseudoConsoleResizeQuirk | pseudoConsoleWin32InputMode)
	if conptyHasPassthrough() {
		flags |= pseudoConsolePassthrough
	}
	return flags
}

// startConPty creates a pseudo console with the quirk flags and spawns
// commandLine attached to it.
func startConPty(commandLine string, cols, rows uint16, dir string, env []string) (*conPty, error) {
	cpty, err := startConPtyFlags(commandLine, cols, rows, dir, env, conptyBaseFlags())
	if err != nil && conptyHasPassthrough() && err == windows.ERROR_INVALID_PARAMETER {
		// Some Win11 builds reject CreateProcess against a passthrough-mode
		// ConPTY handle. Retry without passthrough.
		cpty, err = startConPtyFlags(commandLine, cols, rows, dir, env,
			conptyBaseFlags()&^uint32(pseudoConsolePassthrough))
	}
	return cpty, err
}

func startConPtyFlags(commandLine string, cols, rows uint16, dir string, env []string, flags uint32) (*conPty, error) {
	if cols < 2 {
		cols = 2 // conhost crashes the child on degenerate sizes
	}
	if rows < 2 {
		rows = 2
	}

	var ptyIn, ptyOut, cmdIn, cmdOut windows.Handle
	if err := windows.CreatePipe(&ptyIn, &cmdIn, nil, conptyPipeSize); err != nil {
		return nil, fmt.Errorf("create input pipe: %w", err)
	}
	if err := windows.CreatePipe(&cmdOut, &ptyOut, nil, conptyPipeSize); err != nil {
		closeAll(ptyIn, cmdIn)
		return nil, fmt.Errorf("create output pipe: %w", err)
	}

	var hpc windows.Handle
	size := windows.Coord{X: int16(cols), Y: int16(rows)}
	if err := windows.CreatePseudoConsole(size, ptyIn, ptyOut, flags, &hpc); err != nil {
		closeAll(ptyIn, ptyOut, cmdIn, cmdOut)
		return nil, fmt.Errorf("CreatePseudoConsole: %w", err)
	}

	pi, err := spawnWithConPty(hpc, commandLine, dir, env)
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		closeAll(ptyIn, ptyOut, cmdIn, cmdOut)
		return nil, err
	}

	return &conPty{
		hpc:    hpc,
		pi:     *pi,
		ptyIn:  ptyIn,
		ptyOut: ptyOut,
		cmdIn:  cmdIn,
		cmdOut: cmdOut,
	}, nil
}

func spawnWithConPty(hpc windows.Handle, commandLine, dir string, env []string) (*windows.ProcessInformation, error) {
	cmdLine, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		return nil, err
	}
	var dirPtr *uint16
	if dir != "" {
		dirPtr, err = windows.UTF16PtrFromString(dir)
		if err != nil {
			return nil, err
		}
	}

	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, fmt.Errorf("alloc attribute list: %w", err)
	}
	defer attrs.Delete()
	// The HPCON handle itself is the attribute value (passed in the
	// pointer slot, per the documented ConPTY sample). Reinterpret via a
	// pointer round-trip to keep `go vet` happy.
	hpcValue := *(*unsafe.Pointer)(unsafe.Pointer(&hpc))
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		hpcValue, unsafe.Sizeof(hpc)); err != nil {
		return nil, fmt.Errorf("set pseudoconsole attribute: %w", err)
	}

	siEx := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	siEx.Cb = uint32(unsafe.Sizeof(siEx))
	// No STARTF_USESTDHANDLES: ConPTY provides the child's stdio.

	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT)

	var pi windows.ProcessInformation

	// Null this process's std handle slots for the duration of
	// CreateProcess so no stale/recycled handles leak into the child's
	// process parameters (the console-injection dance in this package can
	// recycle console handles concurrently).
	consoleStateMu.Lock()
	savedIn, _ := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	savedOut, _ := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	savedErr, _ := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	windows.SetStdHandle(windows.STD_INPUT_HANDLE, 0)
	windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, 0)
	windows.SetStdHandle(windows.STD_ERROR_HANDLE, 0)
	err = windows.CreateProcess(nil, cmdLine, nil, nil, false, flags,
		envBlockUTF16(env), dirPtr, &siEx.StartupInfo, &pi)
	windows.SetStdHandle(windows.STD_INPUT_HANDLE, savedIn)
	windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, savedOut)
	windows.SetStdHandle(windows.STD_ERROR_HANDLE, savedErr)
	consoleStateMu.Unlock()

	if err != nil {
		return nil, err
	}
	return &pi, nil
}

// envBlockUTF16 builds a CreateProcess CREATE_UNICODE_ENVIRONMENT block.
func envBlockUTF16(env []string) *uint16 {
	if len(env) == 0 {
		return nil // inherit parent environment
	}
	var block []uint16
	for _, kv := range env {
		block = append(block, utf16.Encode([]rune(kv))...)
		block = append(block, 0)
	}
	block = append(block, 0)
	return &block[0]
}

func (c *conPty) Read(p []byte) (int, error) {
	var n uint32
	err := windows.ReadFile(c.cmdOut, p, &n, nil)
	if err != nil {
		// Once the pipe breaks the reader owns closing cmdOut; Close()
		// must not race a blocked ReadFile on the same handle.
		c.closeCmdOut()
		if err == windows.ERROR_BROKEN_PIPE || err == windows.ERROR_INVALID_HANDLE {
			return int(n), io.EOF
		}
		return int(n), err
	}
	return int(n), nil
}

func (c *conPty) Write(p []byte) (int, error) {
	var n uint32
	err := windows.WriteFile(c.cmdIn, p, &n, nil)
	return int(n), err
}

func (c *conPty) Pid() int { return int(c.pi.ProcessId) }

func (c *conPty) Resize(cols, rows uint16) error {
	if cols < 2 {
		cols = 2
	}
	if rows < 2 {
		rows = 2
	}
	return windows.ResizePseudoConsole(c.hpc, windows.Coord{X: int16(cols), Y: int16(rows)})
}

// Wait blocks until the child exits and returns an error for a non-zero
// exit code. Event-driven (no polling), so tab exit handling is immediate.
func (c *conPty) Wait() error {
	c.waitOnce.Do(func() {
		windows.WaitForSingleObject(c.pi.Process, windows.INFINITE)
		var code uint32
		if err := windows.GetExitCodeProcess(c.pi.Process, &code); err != nil {
			c.waitErr = err
		} else if code != 0 {
			c.waitErr = fmt.Errorf("process exited with code %d", code)
		}
	})
	return c.waitErr
}

// Close terminates the child (ClosePseudoConsole kills everything attached
// to the console) and releases handles. The output pipe read end is closed
// by the reader goroutine when its ReadFile fails, avoiding the classic
// CloseHandle-during-ReadFile race; a timer backstops handle leaks when no
// reader is active.
func (c *conPty) Close() error {
	c.closeOnce.Do(func() {
		windows.ClosePseudoConsole(c.hpc)
		// Closing our copies of conhost's pipe ends breaks the pipes and
		// unblocks any reader once conhost is gone.
		closeAll(c.ptyIn, c.ptyOut, c.cmdIn)
		c.closeProcHandles()
		time.AfterFunc(2*time.Second, c.closeCmdOut)
	})
	return nil
}

// Kill terminates the whole process tree, then tears down the console.
// ClosePseudoConsole alone only kills direct console attachments; children
// that detached (e.g. via their own CREATE_NEW_CONSOLE) would leak.
func (c *conPty) Kill() error {
	killProcessTree(c.pi.ProcessId)
	return c.Close()
}

func (c *conPty) closeCmdOut() {
	c.cmdOutOnce.Do(func() { windows.CloseHandle(c.cmdOut) })
}

func (c *conPty) closeProcHandles() {
	c.procHandles.Do(func() {
		// Wait() may be blocked on the process handle; give it a moment
		// to observe the kill before the handle goes away.
		windows.WaitForSingleObject(c.pi.Process, 3000)
		closeAll(c.pi.Thread)
		// Leave the process handle to Wait(); if nobody waits it is
		// reclaimed at daemon exit. Closing it here would race
		// WaitForSingleObject/GetExitCodeProcess in Wait().
	})
}

func closeAll(handles ...windows.Handle) {
	for _, h := range handles {
		if h != 0 && h != windows.InvalidHandle {
			windows.CloseHandle(h)
		}
	}
}

// killProcessTree terminates pid and all its descendants, leaf-first, using
// a Toolhelp snapshot. Two sweeps catch children spawned between snapshot
// and termination.
func killProcessTree(pid uint32) {
	for sweep := 0; sweep < 2; sweep++ {
		procs := snapshotChildren()
		var kill func(p uint32)
		kill = func(p uint32) {
			for child, parent := range procs {
				if parent == p && child != p {
					kill(child)
				}
			}
			terminatePid(p)
		}
		kill(pid)
	}
}

// snapshotChildren returns a map of pid -> parent pid.
func snapshotChildren() map[uint32]uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	procs := make(map[uint32]uint32)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return procs
	}
	for {
		procs[pe.ProcessID] = pe.ParentProcessID
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return procs
}

func terminatePid(pid uint32) {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return
	}
	windows.TerminateProcess(h, 1)
	windows.CloseHandle(h)
}
