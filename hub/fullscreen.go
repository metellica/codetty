package hub

import (
	"fmt"
	"io"
	"os"
	"time"

	xterm "golang.org/x/term"
)

// fullscreenHandler implements tea.ExecCommand. When bubbletea runs it
// via tea.Exec, the terminal is released and we get direct access to
// stdin/stdout for raw terminal proxying.
type fullscreenHandler struct {
	session *ClientSession
	hub     *Hub
	stdin   io.Reader
	stdout  io.Writer
}

func (f *fullscreenHandler) SetStdin(r io.Reader)  { f.stdin = r }
func (f *fullscreenHandler) SetStdout(w io.Writer) { f.stdout = w }
func (f *fullscreenHandler) SetStderr(w io.Writer) {}

// Run proxies raw input from the hub's terminal to the remote session.
// Output is forwarded by Hub.HandleConn to the fullscreen writer.
// Blocks until the user presses ESC-ESC or Ctrl+A.
func (f *fullscreenHandler) Run() error {
	stdout := f.stdout
	if stdout == nil {
		stdout = os.Stdout
	}

	f.hub.fsMu.Lock()
	f.hub.fullscreenID = f.session.ID
	f.hub.fsWriter = stdout

	// Raw mode so individual keystrokes are forwarded immediately
	fd := int(os.Stdin.Fd())
	if xterm.IsTerminal(fd) {
		oldState, err := xterm.MakeRaw(fd)
		if err == nil {
			defer xterm.Restore(fd, oldState)
		}
	}

	f.writeSnapshot(stdout)
	f.hub.fsMu.Unlock()
	defer f.hub.SetFullscreen("", nil)

	stdin := f.stdin
	if stdin == nil {
		stdin = os.Stdin
	}

	buf := make([]byte, 4096)
	var lastEsc time.Time

	for {
		n, err := stdin.Read(buf)
		if err != nil {
			return err
		}
		data := buf[:n]

		if len(data) > 0 {
			// Ctrl+A (byte 0x01) exits immediately
			if data[0] == 0x01 {
				return nil
			}
			// Standalone ESC (single byte 0x1b) — double ESC within 300ms exits
			if data[0] == 0x1b && len(data) == 1 {
				if time.Since(lastEsc) < 300*time.Millisecond {
					return nil
				}
				lastEsc = time.Now()
			}
		}

		f.session.SendInput(data)
	}
}

func (f *fullscreenHandler) writeSnapshot(stdout io.Writer) {
	stdout.Write([]byte("\033[2J\033[H"))

	cols, rows := int(f.session.Cols), int(f.session.Rows)
	if cols <= 0 || rows <= 0 {
		return
	}

	snapshot := f.session.SnapshotANSI(cols, rows)
	stdout.Write([]byte("\033[?7l"))
	for y, line := range snapshot.lines {
		fmt.Fprintf(stdout, "\033[%d;1H%s", y+1, line)
	}
	if snapshot.wrapEnabled {
		stdout.Write([]byte("\033[?7h"))
	} else {
		stdout.Write([]byte("\033[?7l"))
	}
	curX, curY := snapshot.cursorX, snapshot.cursorY
	if curX < 0 {
		curX = 0
	}
	if curY < 0 {
		curY = 0
	}
	if curX >= cols {
		curX = cols - 1
	}
	if curY >= rows {
		curY = rows - 1
	}
	fmt.Fprintf(stdout, "\033[%d;%dH", curY+1, curX+1)
	if snapshot.cursorVisible {
		stdout.Write([]byte("\033[?25h"))
	} else {
		stdout.Write([]byte("\033[?25l"))
	}
}
