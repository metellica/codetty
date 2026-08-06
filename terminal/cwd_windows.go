//go:build windows

package terminal

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// ProcessCwd returns the current working directory of a live process by
// reading CurrentDirectory.DosPath out of its PEB process parameters
// (the same approach tmux-like Windows multiplexers use for
// pane_current_path). Returns "" when the directory cannot be determined,
// in which case callers fall back to the tab's recorded directory.
//
// Note: cmd.exe keeps the Win32 process cwd in sync with `cd`, but
// PowerShell's Set-Location does not call SetCurrentDirectory, so for
// PowerShell this usually reports the spawn directory.
func ProcessCwd(pid int) string {
	h, err := windows.OpenProcess(
		windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)

	var pbi windows.PROCESS_BASIC_INFORMATION
	var retLen uint32
	err = windows.NtQueryInformationProcess(h, windows.ProcessBasicInformation,
		unsafe.Pointer(&pbi), uint32(unsafe.Sizeof(pbi)), &retLen)
	if err != nil || pbi.PebBaseAddress == nil {
		return ""
	}

	// Read the ProcessParameters pointer out of the child's PEB.
	var peb windows.PEB
	pebAddr := uintptr(unsafe.Pointer(pbi.PebBaseAddress))
	if err := readRemote(h, pebAddr, unsafe.Pointer(&peb), unsafe.Sizeof(peb)); err != nil {
		return ""
	}
	if peb.ProcessParameters == nil {
		return ""
	}

	var params windows.RTL_USER_PROCESS_PARAMETERS
	paramsAddr := uintptr(unsafe.Pointer(peb.ProcessParameters))
	if err := readRemote(h, paramsAddr, unsafe.Pointer(&params), unsafe.Sizeof(params)); err != nil {
		return ""
	}

	cur := params.CurrentDirectory.DosPath
	if cur.Length == 0 || cur.Buffer == nil || cur.Length > 4096 {
		return ""
	}
	buf := make([]uint16, cur.Length/2)
	if err := readRemote(h, uintptr(unsafe.Pointer(cur.Buffer)),
		unsafe.Pointer(&buf[0]), uintptr(cur.Length)); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf)
}

func readRemote(h windows.Handle, addr uintptr, dst unsafe.Pointer, size uintptr) error {
	var read uintptr
	return windows.ReadProcessMemory(h, addr, (*byte)(dst), size, &read)
}
