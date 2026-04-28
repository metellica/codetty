package hub

import (
	"io"
	"os"
	"strings"
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

	// Tell the hub we're in fullscreen so HandleConn forwards output here
	f.hub.SetFullscreen(f.session.ID, stdout)
	defer f.hub.SetFullscreen("", nil)

	// Raw mode so individual keystrokes are forwarded immediately
	fd := int(os.Stdin.Fd())
	if xterm.IsTerminal(fd) {
		oldState, err := xterm.MakeRaw(fd)
		if err == nil {
			defer xterm.Restore(fd, oldState)
		}
	}

	// Clear screen and show current terminal content.
	// MakeRaw disables OPOST so \n won't translate to \r\n;
	// replace manually for correct display.
	stdout.Write([]byte("\033[2J\033[H"))
	screen := f.session.Screen()
	if screen != "" {
		stdout.Write([]byte(strings.ReplaceAll(screen, "\n", "\r\n")))
	}

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
