# Copilot instructions for CodeTTY

CodeTTY is a Go 1.25+ cross-platform local terminal multiplexer with a daemon
architecture. A single binary `codetty` provides both the TUI frontend and the
background daemon. The daemon manages PTY sessions that persist beyond the TUI
lifetime (like tmux). Uses Unix PTY on Linux/macOS and Windows ConPTY on Windows.

## Build and test commands

- Build: `./build.sh`
- Install to `/usr/local/bin`: `./build.sh install`
- Build directly: `go build ./cmd/codetty`
- Build Windows: `GOOS=windows GOARCH=amd64 go build -o codetty.exe ./cmd/codetty`
- Run all tests: `go test ./...`
- Run one package's tests: `go test ./daemon`, `go test ./terminal`, `go test ./vt10x`
- Run a single test: `go test ./cmd/codetty -run '^TestTuiModel_'`
- Cross-compile check: `GOOS=windows GOARCH=amd64 go test -exec=/bin/true ./...`

## Architecture

- `cmd/codetty/main.go` is the CLI entry point. `codetty` with no args opens
  the TUI (auto-creating a shell session if none exist). Subcommands: `new`,
  `attach`, `ls`, `kill`, `kill-server`.
- `cmd/codetty/daemonize.go` auto-starts the daemon (`__server` subcommand) if
  no Unix socket is found. The daemon runs as a background process.
- `daemon/server.go` is the background daemon. It listens on a Unix socket,
  manages PTY sessions (`DaemonSession`), handles client connections, and
  broadcasts output to attached TUI clients and subscribers.
- `daemon/session.go` wraps a `terminal.Session` (PTY) with a `ScreenMirror`
  for snapshot replay. Output flows: PTY → ScreenMirror + broadcast to clients.
  When a native tab's process exits: non-shell commands respawn the default
  shell in place (in `tab.Dir`); shell tabs auto-close (last one ends the
  session) unless pinned to an explicit workspace dir, which respawns instead.
  Deliberate kills (`killed`/`closing` flags) and tmux sessions skip this.
  Tabs created without a dir inherit the active tab's working directory
  (live process cwd via `terminal.ProcessCwd`, falling back to `tab.Dir`).
- `daemon/client.go` handles a single attached TUI client: input forwarding,
  output streaming, resize, detach, session switching.
- `daemon/subscriber.go` provides an event subscription channel for the TUI
  to receive session lifecycle events and live output from all sessions.
- `cmd/codetty/tui.go` is the Bubble Tea TUI model. It connects to the daemon
  via subscriber mode to receive all session output, maintaining local
  `tuiSession` objects with vt10x terminals for rendering.
- `cmd/codetty/tui_input.go` handles keyboard input with Ctrl+A prefix for
  commands. Normal keys are forwarded to the focused session's PTY. Supports
  Shift/Ctrl/Alt + arrow keys with proper xterm encoding.
- `cmd/codetty/tui_selection.go` handles mouse events: click to focus panel,
  Ctrl+click to promote to big panel, scroll wheel forwarding.
- `cmd/codetty/tui_view.go` renders the TUI layout: split mode (grid),
  focus-big mode (one large + side panels), fullscreen mode.
- `cmd/codetty/tui_tabs.go` renders tab mode: a session tab bar on the top
  row (reusing `renderTabBar` via synthetic `tuiTab` items so active/agent
  colors match focus-big), the selected session below, plus its mouse
  handling with content coordinates offset one row down.
- `cmd/codetty/tui_list.go` renders list mode (the default): big panel on
  the left, a session-list sidebar on the right showing per-tab agent
  states. Shift+Tab cycles through list entries then back to the big
  panel; ↑/↓ + Enter switch sessions, agent-row clicks activate the tab.
- `cmd/codetty/tui_session.go` renders terminal content from vt10x state to
  ANSI strings. Handles cursor rendering, reverse video, wide characters.
- `terminal/session.go` defines the `Session` interface for PTY processes.
  Platform-specific implementations in `session_unix.go` (creack/pty) and
  `session_windows.go` (ConPTY).
- `terminal/screen.go` provides `ScreenMirror` using vt10x for snapshot replay.
- `vt10x/` is a local fork of `hinshun/vt10x` with wide character (CJK) support.
  The key patch is in `parse.go`: cursor advances by `runewidth.RuneWidth(c)`
  instead of always 1, with NUL continuation cells for wide characters.
- `daemon/protocol.go` defines the binary frame protocol over Unix socket:
  length-prefixed frames with a type byte, JSON for requests/responses, raw
  bytes for input/output.

## Project-specific conventions

- The TUI separates `m.selected` (which session is the big panel in focus-big
  mode) from `m.inputFocus` (which session receives keyboard input). When
  `inputFocus == -1`, input goes to `m.selected`.
- The daemon owns the session display order (`Server.order`, creation order
  by default) and the last big-panel session (`Server.active`). Subscribe
  snapshots, `ls`, and hot-upgrade state all follow them, so ordering and
  selection are stable across attach/TUI restart/upgrade. TUI reorders
  (h/l) sync via `FrameReorderSess`; selection changes sync centrally in
  `Update` via `FrameSetActive`; on subscribe the daemon sends an "active"
  event before the snapshots (an explicit attach/config target wins).
- Mouse clicks in split mode do NOT forward SGR mouse sequences to the PTY
  (bash doesn't enable mouse reporting, causing garbage echo). Only scroll
  wheel is forwarded. Fullscreen mode forwards all mouse events.
- Terminal rendering uses the local vt10x fork which properly handles wide
  characters. The `skipNext` logic in `renderTerminalANSI` skips NUL
  continuation cells placed by vt10x after wide characters.
- Cursor rendering: white block (`\x1b[30;107m`) when cursor is visible.
  When a program hides the cursor (e.g., tmux), we respect `CursorVisible()`
  and don't draw our own cursor. Reverse-video cells swap fg↔bg explicitly.
- Keep platform-specific PTY code behind the `terminal.Session` interface.
- When rendering terminal UI text, use `lipgloss.Width` or
  `charmbracelet/x/ansi` helpers for visual width and ANSI-aware truncation.
  Raw `len` is only safe for byte protocol or non-rendered data.
- Tests stay close to the affected package: daemon protocol/client tests in
  `daemon/`, Bubble Tea model tests by constructing `tea.Msg` values directly
  in `cmd/codetty/`, terminal helpers in `terminal/`.
- The daemon auto-exits when all sessions have exited and no clients are
  connected. This avoids orphaned background processes.

