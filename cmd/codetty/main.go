package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"

	"codetty/config"
	"codetty/terminal"
	"github.com/mattn/go-runewidth"
)

// Build metadata, stamped via -ldflags by build.sh; plain `go build`
// falls back to the VCS info Go embeds automatically.
var (
	version   = "dev"
	buildTime = "unknown"
)

// versionString renders the version line for --version / `codetty version`.
func versionString() string {
	v, bt := version, buildTime
	if v == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok {
			var rev, vcsTime string
			dirty := false
			for _, s := range info.Settings {
				switch s.Key {
				case "vcs.revision":
					rev = s.Value
				case "vcs.time":
					vcsTime = s.Value
				case "vcs.modified":
					dirty = s.Value == "true"
				}
			}
			if len(rev) >= 12 {
				v = "dev-" + rev[:12]
				if dirty {
					v += "-dirty"
				}
			}
			if bt == "unknown" && vcsTime != "" {
				bt = vcsTime
			}
		}
	}
	return fmt.Sprintf("codetty %s (built %s, %s %s/%s)",
		v, bt, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

const usageTemplate = `codetty — terminal multiplexer

Usage:
  codetty                                      Open TUI (auto-create shell if no sessions)
  codetty [new] [-s name] [--] [command ...]   Create session and open TUI
  codetty attach [-s name]                     Open TUI focused on session (alias: a)
  codetty up [-f config.toml]                  Load session config, create sessions, open TUI
  codetty ls [-a]                              List active sessions (-a: all instances)
  codetty kill [-s name]                       Kill a session
  codetty kill-server                          Stop the daemon and all sessions
  codetty send-keys -s name [-t tab] keys...   Send keys to a session's tab (tmux style:
                                               Enter, C-c, M-x, Up... ; -l = literal)
  codetty capture -s name [-t tab] [-n rows]   Print a tab's screen as plain text
  codetty send-mouse -s name [-t tab] event x y  Send a mouse event at cell x,y (1-based;
                                               events: click1/2/3, down/up/drag1/2/3,
                                               move, wheelup, wheeldown)
  codetty upgrade [-r remote|all]              Hot-upgrade daemon binary without killing
                                               sessions (-r: deploy + upgrade a remote)
  codetty version                              Show version and build time

TUI Keys (%s prefix):
  d       Detach (exit TUI, sessions keep running)
  x       Kill focused session
  c       Create new session
  r       Rename focused session
  w       Session manager (hide/show/kill)
  n/p     Next/previous session
  f       Toggle fullscreen (no borders/status)
  T       Tab mode (session tabs on top row)
  h/l     Move session order left/right
  1-9     Jump to session by number

Other keys:
  Shift+Tab           Switch to next session
                      (list mode: cycle through list entries, ↑/↓ + Enter to switch)
                      (fullscreen: open session switcher with live previews)
  Mouse click         Select session panel

Options:
  -s, --session NAME    Target session name
  -i, --instance NAME   Use a named daemon instance, isolated from others on
                        this host (before subcommand; env: CODETTY_INSTANCE)
  --help, -h            Show this help
`

// usageText renders the CLI help text with the configured prefix key
// (see ~/.config/codetty/config.toml), falling back to Ctrl+A on error.
func usageText() string {
	label := "Ctrl+A"
	if cfg, err := config.Load(); err == nil {
		_, label = parsePrefixKey(cfg.Keybindings.Prefix)
	}
	return fmt.Sprintf(usageTemplate, label)
}

// normalizeRuneWidth pins ambiguous-width characters (·, box drawing, …) to
// narrow, matching how Windows Terminal, ConPTY and lipgloss measure them.
// go-runewidth's automatic East Asian detection (Windows console codepage
// 932/936/949/950, CJK locales on Unix) would otherwise make vt10x advance
// the cursor two cells for such characters, corrupting layout and cursor
// position for CJK-locale users. An explicit RUNEWIDTH_EASTASIAN=1 in the
// environment still forces wide handling.
func normalizeRuneWidth() {
	if os.Getenv("RUNEWIDTH_EASTASIAN") == "" {
		runewidth.EastAsianWidth = false
		runewidth.DefaultCondition.EastAsianWidth = false
	}
}

func main() {
	normalizeRuneWidth()
	rest, name, err := extractInstanceArgs(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	if name == "" {
		name = os.Getenv("CODETTY_INSTANCE")
	}
	if err := validateInstanceName(name); err != nil {
		log.Fatal(err)
	}
	instanceName = name
	if name != "" {
		// Propagate to the daemon (and thus to every PTY it spawns), so
		// codetty commands run inside a session target the same instance.
		os.Setenv("CODETTY_INSTANCE", name)
	}

	if len(rest) < 1 {
		cmdDefault()
		return
	}

	cmd := rest[0]
	args := rest[1:]

	switch cmd {
	case "new":
		cmdNew(args)
	case "attach", "a":
		cmdAttach(args)
	case "up":
		cmdUp(args)
	case "ls":
		cmdList(args)
	case "kill":
		cmdKill(args)
	case "kill-server":
		cmdKillServer(args)
	case "send-keys", "send":
		cmdSendKeys(args)
	case "capture", "capture-pane":
		cmdCapture(args)
	case "send-mouse":
		cmdSendMouse(args)
	case "upgrade":
		cmdUpgrade(args)
	case "reload":
		cmdReload(args)
	case "__server":
		cmdServer(args)
	case "__bridge":
		cmdBridge()
	case "--help", "-h", "help":
		fmt.Print(usageText())
	case "--version", "-v", "version":
		fmt.Println(versionString())
	default:
		// Treat unknown args as command to run in a new session
		cmdNew(rest)
	}
}

func cmdDefault() {
	sockPath := defaultSocketPath()
	if err := ensureDaemon(sockPath); err != nil {
		log.Fatalf("cannot start daemon: %v", err)
	}
	startTUI(sockPath, startupConfig{autoCreate: []string{terminal.DefaultShell()}})
}

// instanceName is the resolved daemon instance name ("" = default instance).
// Each instance has its own socket, daemon and sessions, fully isolated from
// other instances on the same host.
var instanceName string

// extractInstanceArgs strips a leading -i/--instance flag from args, returning
// the remaining args and the instance name. Only flags appearing before the
// subcommand are consumed, so options of a wrapped command (e.g.
// `codetty bash -i`) are left untouched.
func extractInstanceArgs(args []string) ([]string, string, error) {
	name := ""
	for len(args) > 0 {
		arg := args[0]
		switch {
		case arg == "-i" || arg == "--instance":
			if len(args) < 2 {
				return nil, "", fmt.Errorf("%s requires an instance name", arg)
			}
			name = args[1]
			args = args[2:]
		case strings.HasPrefix(arg, "--instance="):
			name = strings.TrimPrefix(arg, "--instance=")
			args = args[1:]
		case strings.HasPrefix(arg, "-i="):
			name = strings.TrimPrefix(arg, "-i=")
			args = args[1:]
		default:
			return args, name, nil
		}
	}
	return args, name, nil
}

var instanceNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// validateInstanceName rejects names that would produce unsafe or overlong
// socket paths. The empty name selects the default instance.
func validateInstanceName(name string) error {
	if name == "" {
		return nil
	}
	if len(name) > 64 || !instanceNameRe.MatchString(name) {
		return fmt.Errorf("invalid instance name %q: use [A-Za-z0-9_-], max 64 chars", name)
	}
	return nil
}

// runtimeSocketDir returns the directory holding codetty daemon sockets.
func runtimeSocketDir() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("codetty-%d", os.Getuid()))
}

// defaultSocketPath returns the Unix socket path for the daemon of the
// current instance: codetty.sock for the default instance, or
// codetty-<name>.sock for a named instance.
func defaultSocketPath() string {
	return instanceSocketPath(instanceName)
}

// instanceSocketPath returns the socket path for the given instance name.
func instanceSocketPath(name string) string {
	if name == "" {
		return filepath.Join(runtimeSocketDir(), "codetty.sock")
	}
	return filepath.Join(runtimeSocketDir(), "codetty-"+name+".sock")
}
