# Copilot instructions for CodeTTY

CodeTTY is a Go 1.25+ cross-platform terminal multiplexer made of two
binaries: `codetty-hub`, a Bubble Tea TUI/WebSocket server, and `codetty-cli`,
a PTY client that connects back to the hub. The CLI uses Unix PTY on
Linux/macOS and Windows ConPTY on Windows.

## Build and test commands

- Build both binaries: `./build.sh`
- Install built binaries to `/usr/local/bin`: `./build.sh install`
- Build one binary directly: `go build ./cmd/codetty-hub` or `go build ./cmd/codetty-cli`
- Build Windows binaries: `GOOS=windows GOARCH=amd64 go build -o codetty-cli.exe ./cmd/codetty-cli`
- Run all tests: `go test ./...`
- Run one package's tests: `go test ./hub`, `go test ./protocol`, or `go test ./cmd/codetty-cli`
- Run a single test: `go test ./hub -run '^TestModel_UsesCtrlAPrefixForHubCommands$'`
- Cross-compile tests without executing target binaries: `GOOS=windows GOARCH=amd64 go test -exec=/bin/true ./...`

## Architecture

- `cmd/codetty-hub/main.go` starts the Bubble Tea program and an HTTP
  WebSocket endpoint at `/ws`. Each accepted WebSocket is handed to
  `hub.Hub.HandleConn`.
- `hub.Hub` owns connected `ClientSession`s, active-session ordering, and the
  fullscreen output path. `HandleConn` expects a register message before output
  or replay messages, replaces existing sessions with the same ID, and sends
  Bubble Tea messages for connect/disconnect/screen updates.
- `hub.ClientSession` wraps a `vt10x.Terminal` for the hub-side screen model.
  Grid/focus views call `RenderANSI`/`SnapshotANSI` instead of reading raw PTY
  bytes directly.
- The TUI model in `hub/tui.go` uses Ctrl+A as the hub command prefix. Normal
  keys are forwarded to the selected remote session; prefix keys change
  selection, layout, fullscreen, or quit. Fullscreen mode runs through
  `tea.Exec` and forwards live session output through `Hub.fsWriter` because the
  Bubble Tea event loop is blocked while the exec handler runs.
- Layout code is split by concern: `layout.go` maintains ordering and mode,
  `layout_view.go` renders grid/focus-big panels and status bars, and
  `layout_geometry.go` computes hit regions for mouse selection.
- `cmd/codetty-cli/main.go` starts the requested command through the
  `terminalSession` abstraction, mirrors terminal output to local stdout, a hub
  output channel, and a `ScreenMirror`, then runs a reconnecting WebSocket loop.
  On reconnect, the CLI registers, sends a replay snapshot from `ScreenMirror`,
  and resumes forwarding output/input.
- `cmd/codetty-cli/terminal_unix.go` uses `creack/pty` for Linux/macOS;
  `terminal_windows.go` uses `UserExistsError/conpty` and Windows ConPTY.
- `protocol/protocol.go` defines the WebSocket binary frame format: the first
  byte is the message type; register/resize payloads are JSON, while
  output/input/replay payloads are raw terminal bytes.

## Project-specific conventions

- Use the helpers in `protocol` to encode/decode frames; do not duplicate type
  byte handling in callers.
- Route all hub-to-client WebSocket writes through `ConnWriter`. Gorilla
  WebSocket does not support concurrent writes, and this project protects slow
  clients by dropping messages when the write channel is full.
- Preserve the two vt10x screen models when changing terminal behavior:
  `ClientSession` is the hub display state, while CLI `ScreenMirror` is the
  reconnect replay source. Resize handling must update the PTY and the relevant
  vt10x state.
- Keep platform-specific terminal process code behind `terminalSession`.
  Connection logic should call `Read`, `Write`, `Resize`, `Wait`, and `Close`
  on the interface rather than importing PTY packages directly.
- Avoid `program.Send` from hub output handling while fullscreen is active; use
  the fullscreen writer path guarded by `fsMu` to prevent blocking the Bubble Tea
  event loop.
- Keep user-controlled session order in `layoutState.order`. Synchronize it with
  active hub sessions on connect/disconnect instead of sorting sessions ad hoc.
- When rendering terminal UI text, use `lipgloss.Width` or
  `charmbracelet/x/ansi` helpers for visual width and ANSI-aware truncation.
  Raw `len` is only safe for byte protocol or non-rendered data.
- CLI local input intentionally filters terminal focus events (`CSI I`/`CSI O`)
  before writing to the PTY while preserving other escape sequences.
- Tests generally stay close to the affected package: protocol frame round trips
  in `protocol`, WebSocket/session behavior in `hub` with `httptest`, Bubble Tea
  model updates by constructing `tea.Msg` values directly, and CLI PTY-adjacent
  helpers in `cmd/codetty-cli`.
