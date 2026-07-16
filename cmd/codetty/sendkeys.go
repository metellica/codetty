package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"

	"codetty/daemon"
	"codetty/terminal"
)

// namedKeys maps tmux-style key names (lower-cased for lookup) to the byte
// sequences written to the PTY.
var namedKeys = map[string]string{
	"enter":  "\r",
	"escape": "\x1b",
	"esc":    "\x1b",
	"tab":    "\t",
	"btab":   "\x1b[Z",
	"space":  " ",
	"bspace": "\x7f",
	"up":     "\x1b[A",
	"down":   "\x1b[B",
	"right":  "\x1b[C",
	"left":   "\x1b[D",
	"home":   "\x1b[H",
	"end":    "\x1b[F",
	"ppage":  "\x1b[5~",
	"pgup":   "\x1b[5~",
	"npage":  "\x1b[6~",
	"pgdn":   "\x1b[6~",
	"ic":     "\x1b[2~",
	"insert": "\x1b[2~",
	"dc":     "\x1b[3~",
	"delete": "\x1b[3~",
	"f1":     "\x1bOP",
	"f2":     "\x1bOQ",
	"f3":     "\x1bOR",
	"f4":     "\x1bOS",
	"f5":     "\x1b[15~",
	"f6":     "\x1b[17~",
	"f7":     "\x1b[18~",
	"f8":     "\x1b[19~",
	"f9":     "\x1b[20~",
	"f10":    "\x1b[21~",
	"f11":    "\x1b[23~",
	"f12":    "\x1b[24~",
}

// translateKey converts one send-keys argument to the bytes to write.
// tmux semantics: a known key name (or C-x / M-x modifier form) becomes
// its control sequence, anything else is sent literally.
func translateKey(arg string) string {
	if seq, ok := namedKeys[strings.ToLower(arg)]; ok {
		return seq
	}
	// C-a / ^a control keys (C-@ .. C-_)
	var c byte
	if len(arg) == 3 && arg[0] == 'C' && arg[1] == '-' {
		c = arg[2]
	} else if len(arg) == 2 && arg[0] == '^' {
		c = arg[1]
	}
	if c != 0 {
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if c >= '@' && c <= '_' {
			return string(c - '@')
		}
	}
	if strings.HasPrefix(arg, "M-") && len(arg) > 2 {
		return "\x1b" + translateKey(arg[2:])
	}
	return arg
}

// sendRequest performs a one-shot request/response against the daemon.
func sendRequest(req daemon.Request) daemon.Response {
	conn, err := net.Dial("unix", defaultSocketPath())
	if err != nil {
		log.Fatal("no daemon running")
	}
	defer conn.Close()

	daemon.WriteFrame(conn, daemon.FrameRequest, daemon.MarshalJSON(req))
	ft, payload, err := daemon.ReadFrame(conn)
	if err != nil || ft != daemon.FrameResponse {
		log.Fatal("unexpected response")
	}
	var resp daemon.Response
	json.Unmarshal(payload, &resp)
	return resp
}

// cmdSendKeys implements `codetty send-keys -s session [-t tab] [-l] keys...`,
// modeled after tmux send-keys.
func cmdSendKeys(args []string) {
	fs := flag.NewFlagSet("send-keys", flag.ExitOnError)
	session := fs.String("s", "", "session name")
	tab := fs.String("t", "", "tab: index or name (default: active tab)")
	literal := fs.Bool("l", false, "send arguments literally (no key name lookup)")
	fs.Parse(args)

	if *session == "" {
		log.Fatal("specify session name: codetty send-keys -s NAME [-t TAB] keys...")
	}
	if fs.NArg() == 0 {
		log.Fatal("no keys given")
	}

	var data strings.Builder
	for _, arg := range fs.Args() {
		if *literal {
			data.WriteString(arg)
		} else {
			data.WriteString(translateKey(arg))
		}
	}

	resp := sendRequest(daemon.Request{
		Action:  daemon.ActionSendKeys,
		Session: *session,
		Tab:     *tab,
		Data:    []byte(data.String()),
	})
	if !resp.OK {
		log.Fatalf("send-keys failed: %s", resp.Error)
	}
}

// cmdCapture implements `codetty capture -s session [-t tab] [-n rows]`,
// printing a tab's screen as plain text (tmux capture-pane analog).
func cmdCapture(args []string) {
	fs := flag.NewFlagSet("capture", flag.ExitOnError)
	session := fs.String("s", "", "session name")
	tab := fs.String("t", "", "tab: index or name (default: active tab)")
	rows := fs.Int("n", 0, "only the bottom N rows (default: whole screen)")
	fs.Parse(args)

	if *session == "" {
		log.Fatal("specify session name: codetty capture -s NAME [-t TAB] [-n ROWS]")
	}

	resp := sendRequest(daemon.Request{
		Action:  daemon.ActionCapture,
		Session: *session,
		Tab:     *tab,
		Rows:    uint16(*rows),
	})
	if !resp.OK {
		log.Fatalf("capture failed: %s", resp.Error)
	}
	fmt.Println(resp.Text)
}

// cmdSendMouse implements `codetty send-mouse -s session [-t tab] event x y`,
// synthesizing a mouse event at 1-based cell coordinates. The daemon encodes
// it with the mouse protocol the tab's application enabled.
func cmdSendMouse(args []string) {
	fs := flag.NewFlagSet("send-mouse", flag.ExitOnError)
	session := fs.String("s", "", "session name")
	tab := fs.String("t", "", "tab: index or name (default: active tab)")
	fs.Parse(args)

	if *session == "" || fs.NArg() != 3 {
		log.Fatal("usage: codetty send-mouse -s NAME [-t TAB] EVENT X Y\n" +
			"events: " + strings.Join(terminal.MouseEventNames(), " "))
	}
	x, errX := strconv.Atoi(fs.Arg(1))
	y, errY := strconv.Atoi(fs.Arg(2))
	if errX != nil || errY != nil {
		log.Fatal("X and Y must be integers (1-based cell coordinates)")
	}

	resp := sendRequest(daemon.Request{
		Action:  daemon.ActionSendMouse,
		Session: *session,
		Tab:     *tab,
		Mouse:   fs.Arg(0),
		MouseX:  x,
		MouseY:  y,
	})
	if !resp.OK {
		log.Fatalf("send-mouse failed: %s", resp.Error)
	}
}
