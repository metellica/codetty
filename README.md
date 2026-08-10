# CodeTTY

A local terminal multiplexer, similar to tmux/screen. A background daemon manages PTY sessions while a TUI frontend provides multi-panel interaction. Sessions keep running after SSH disconnects or the terminal closes — just re-attach to pick up where you left off.

## Architecture

```
┌─────────────────────────────────────────────┐
│            codetty (TUI frontend)            │
│  Bubble Tea rendering · layouts · mouse/keys │
└──────────────────┬──────────────────────────┘
                   │ Unix Socket
┌──────────────────▼──────────────────────────┐
│        codetty daemon (background)           │
│  Manages PTY sessions · auto start/exit      │
│  ┌─────┐ ┌─────┐ ┌─────┐                    │
│  │ PTY │ │ PTY │ │ PTY │  ...               │
│  │bash │ │ vim │ │htop │                    │
│  └─────┘ └─────┘ └─────┘                    │
└─────────────────────────────────────────────┘
```

## Features

- **Daemon architecture** — sessions keep running in the background; detach/attach never disturbs the processes, so you can recover after an SSH drop
- **Multi-tab sessions** — a session can hold multiple tabs; when a non-shell command exits, the default shell respawns in place, and new tabs inherit the current tab's working directory
- **Multiple layout modes** — list (default), focus-big, tabs, fullscreen, floating windows
- **AI agent state detection** — automatically recognizes AI coding agents running inside tabs and shows idle/working/blocked state in the tab bar/sidebar; switching sessions focuses the tab that needs human input first (supports [Claude Code](https://github.com/anthropics/claude-code), [Codex CLI](https://github.com/openai/codex), and [Copilot CLI](https://github.com/github/copilot-cli); the state-probing approach was inspired by [herdr](https://github.com/herdrdev/herdr))
- **Managed remote tabs** — auto-deploys and connects to a remote CodeTTY daemon over the system OpenSSH client; a session can mix local and remote tabs, and tasks plus agent state survive network drops
- **Hot upgrade** — `codetty upgrade` replaces the daemon binary in place (same-PID exec), keeping every session and tab process alive; `-r` upgrades remotes
- **Scriptable control** — `send-keys` / `capture` / `send-mouse` inject keystrokes, capture screen text, and send mouse events from outside
- **Scrollback** — prefix+`[` or prefix+PgUp enters history scroll mode (vi-style j/k/u/d/g/G); the mouse wheel works too
- **Configurable command prefix** — defaults to Ctrl+A (like tmux/screen), customizable via config file
- **Session manager** — hide/show/kill sessions with panel buttons
- **Mouse support** — click to focus, Ctrl+click to switch, drag title bars to reorder, text selection & copy
- **Wide character support** — renders CJK (Chinese/Japanese/Korean) correctly; ambiguous-width characters are forced narrow under CJK locales to avoid layout corruption
- **Shift/Ctrl/Alt arrow keys** — modifier combinations are forwarded correctly (e.g. tmux pane switching)
- **Cross-platform PTY** — Unix PTY on Linux/macOS, ConPTY on Windows
- **Automatic daemon** — starts on first run, exits when all sessions have ended
- **Named instances** — `-i/--instance` runs multiple isolated daemons on one host (like `tmux -L`)
- **Session config files** — define session/tab layouts in `.codetty.toml`, with reusable tab templates and remote target declarations

## Quick start

### Build

```bash
./build.sh          # build the codetty binary
./build.sh install  # install to /usr/local/bin
```

Windows:

```powershell
.\build.bat         # build codetty.exe
```

Or with Go directly:

```bash
go build -o codetty ./cmd/codetty
```

### Usage

```bash
# Start (auto-creates a shell session and opens the TUI)
codetty

# Create a session running a specific command
codetty new -- htop
codetty new -s myserver -- ssh user@host

# List active sessions
codetty ls

# Re-attach to existing sessions
codetty attach
codetty a -s myserver

# Load a session layout from a config file
codetty up                          # look for .codetty.toml
codetty up -f dev.toml              # use a specific config file
codetty reload                      # incrementally sync running sessions with the config

# Kill a session
codetty kill -s myserver

# Stop the daemon and all sessions
codetty kill-server

# Script sessions from outside (tmux-style key names)
codetty send-keys -s myserver 'echo hi' Enter
codetty send-keys -s dev -t 1 C-c            # send Ctrl+C to a specific tab
codetty capture -s dev -n 50                 # capture the last 50 screen rows as plain text
codetty send-mouse -s dev click1 10 5        # left-click at cell (10,5)

# Hot-upgrade the daemon (sessions uninterrupted)
codetty upgrade                     # upgrade the local daemon to the current binary
codetty upgrade -r gpu              # deploy to and upgrade remote target "gpu"
codetty version                     # show version and build info

# Named instances: multiple isolated daemons on one host (like tmux -L)
codetty -i work                     # use the "work" instance (own socket & sessions)
codetty --instance work ls          # list sessions of the work instance
codetty ls -a                       # list sessions across all instances on this host
CODETTY_INSTANCE=work codetty ls    # instances can also be selected via env var
```

### TUI keys (at a glance)

The default prefix key is `Ctrl+A` (customizable in the config file, see [Configuration](#configuration) below):

| Key | Action |
|-----|--------|
| `Ctrl+A d` | Detach (exit the TUI, sessions keep running) |
| `Ctrl+A c` | Create a new session |
| `Ctrl+A t` | Create a new tab in the current session (pick a local or remote target) |
| `Ctrl+A x` / `Ctrl+A X` | Kill the current session / close the current tab |
| `Ctrl+A r` | Rename the current session |
| `Ctrl+A w` | Open the session manager |
| `Ctrl+A n/p` | Next / previous session |
| `Ctrl+A [` or `Ctrl+A PgUp` | Enter scrollback mode (↑↓/jk/PgUp/PgDn/u/d/g/G, `q`/Esc to return) |
| `Ctrl+A f` | Toggle fullscreen / floating window |
| `Ctrl+A q` | Close the floating window |
| `Ctrl+A s` / `Ctrl+A b` | Switch to list / focus-big mode |
| `Ctrl+A T` | Tab mode (session tab bar on the top row) |
| `Ctrl+A h/l` | Move the session left / right in the display order |
| `Ctrl+A m` | Open a temporary floating shell |
| `Ctrl+A R` | Reload the session config file |
| `Ctrl+A 1-9` | Jump to session by number |

See [docs/manual.md](docs/manual.md) for the full key reference and detailed usage.

## Configuration

CodeTTY reads an optional user config file (TOML) at
`~/.config/codetty/config.toml`. If the file or any field is missing,
CodeTTY falls back to sensible defaults.

[config.example.toml](config.example.toml) in the repository root is a
commented sample; copy it to `~/.config/codetty/config.toml` and edit as
needed:

```bash
mkdir -p ~/.config/codetty
cp config.example.toml ~/.config/codetty/config.toml
```

Supported options:

| Option | Description | Default |
|--------|-------------|---------|
| `keybindings.prefix` | Command prefix key, in the form `"ctrl+<letter>"` (e.g. `"ctrl+a"`, `"ctrl+b"`) | `ctrl+a` |

Tip: if you nest CodeTTY with tmux and both use `ctrl+a`, set a different
prefix here (e.g. `"ctrl+b"`) to avoid conflicts.

## Project layout

```
codetty/
├── cmd/codetty/     # entry point, CLI commands, TUI rendering, daemon mgmt, remote deploy
├── daemon/          # background process: session/tab mgmt, Unix socket protocol, hot upgrade
├── terminal/        # PTY abstraction (Unix/Windows), ScreenMirror, mouse encoding
├── agentdetect/     # AI agent identification and idle/working/blocked state inference
├── vt10x/           # local fork of vt10x terminal emulation (wide-char support)
├── config/          # user config (config.toml) and session config (.codetty.toml) loading
├── docs/            # documentation
├── config.example.toml    # user config sample
├── sessions.example.toml  # session layout config sample
├── build.sh         # Linux/macOS build script
├── build.bat        # Windows build script
├── go.mod
└── go.sum
```

## Third-party dependencies

| Dependency | Purpose |
|------------|---------|
| [charmbracelet/bubbletea](https://github.com/charmbracelet/bubbletea) | TUI framework (Elm architecture) |
| [charmbracelet/lipgloss](https://github.com/charmbracelet/lipgloss) | TUI styling and layout |
| [charmbracelet/x/ansi](https://github.com/charmbracelet/x) | ANSI escape sequence handling |
| [creack/pty](https://github.com/creack/pty) | Unix PTY creation and management |
| [mattn/go-runewidth](https://github.com/mattn/go-runewidth) | Unicode character width |
| [golang.org/x/term](https://pkg.go.dev/golang.org/x/term) | terminal raw mode |
| [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) | syscalls (Unix PTY ioctl, Windows ConPTY, process management) |
| [BurntSushi/toml](https://github.com/BurntSushi/toml) | user config file (config.toml) parsing |

## Requirements

- Go 1.25+
- Linux / macOS / Windows
- Windows requires Windows 10 version 1809+ (ConPTY)

## Platform support status

| Platform | Status |
|----------|--------|
| Linux | ✅ Fairly stable — the primary development and daily-use platform |
| macOS | ⚠️ Works, but not thoroughly tested |
| Windows | ⚠️ More limitations (bounded by ConPTY, e.g. mouse events cannot pass through ConPTY before Win11 22621) and barely tested |

## Known limitations

- Agent state detection currently supports **Claude Code**, **Codex CLI**, and **Copilot CLI**; other agents (Aider, Cursor, etc.) are not yet supported
- The agent state-probing approach was inspired by [herdr](https://github.com/herdrdev/herdr)
