package hub

import (
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
// Blocks until the user presses ESC-ESC or Ctrl+A, or the session disconnects.
func (f *fullscreenHandler) Run() error {
	stdout := f.stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stdin := f.stdin
	if stdin == nil {
		stdin = os.Stdin
	}

	replaySeq := f.session.ReplaySeq()

	done := make(chan struct{})
	f.hub.fsMu.Lock()
	f.hub.fullscreenID = f.session.ID
	f.hub.fsWriter = stdout
	f.hub.fsAwaitReplay = true
	f.hub.fsDoneCh = done

	// Raw mode so individual keystrokes are forwarded immediately and are not
	// locally echoed before the remote PTY echoes them back.
	if fd, ok := inputFD(stdin); ok && xterm.IsTerminal(fd) {
		oldState, err := xterm.MakeRaw(fd)
		if err == nil {
			defer xterm.Restore(fd, oldState)
		}
	}

	f.writeSnapshot(stdout)
	f.hub.fsMu.Unlock()
	defer f.hub.SetFullscreen("", nil)

	// Wait briefly for the resize-triggered replay to arrive before we start
	// forwarding user input. Without this, a command like "ls" can be echoed
	// against a stale prompt/cursor position and appears garbled.
	replayed := f.session.WaitForReplayAfter(replaySeq, 300*time.Millisecond)
	if !replayed {
		f.hub.ClearFullscreenReplayGate(f.session.ID)
	}

	// Read stdin in a goroutine so we can also select on the done channel
	// (closed when the fullscreen session disconnects).
	type readResult struct {
		data []byte
		err  error
	}
	readCh := make(chan readResult, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdin.Read(buf)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				select {
				case readCh <- readResult{data: cp}:
				case <-done:
					return
				}
			}
			if err != nil {
				select {
				case readCh <- readResult{err: err}:
				case <-done:
				}
				return
			}
		}
	}()

	var lastEsc time.Time

	for {
		select {
		case <-done:
			return nil
		case r := <-readCh:
			if r.err != nil {
				return r.err
			}
			data := r.data

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
}

func (f *fullscreenHandler) writeSnapshot(stdout io.Writer) {
	// Clear screen and reset terminal state.
	// We intentionally do NOT paint a snapshot of the old vt10x buffer because
	// the session was just resized from panel dimensions to fullscreen dimensions.
	// The old content was formatted for the panel width and would appear garbled.
	// Instead, let the remote shell redraw naturally at the new size.
	//
	// Explicitly disable newline mode (LNM, ESC[20l). Normal terminals leave
	// LNM off; enabling it makes LF imply CR and can add blank lines after
	// commands in and after fullscreen.
	stdout.Write([]byte("\033[0m\033[r\033[?6l\033[20l\033[2J\033[H\033[?7h\033[?25h"))
}

func inputFD(r io.Reader) (int, bool) {
	if f, ok := r.(*os.File); ok {
		return int(f.Fd()), true
	}
	if os.Stdin != nil {
		return int(os.Stdin.Fd()), true
	}
	return 0, false
}
