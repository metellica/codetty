# Daemon Session Design: Detach/Attach for CodeTTY CLI

## Problem Statement

When running `codetty` (CLI) over SSH, the process is tied to the SSH session.
If SSH disconnects, the PTY process dies. Users need tmux-like detach/attach
functionality: PTY processes persist independently of the controlling terminal,
and users can reattach after reconnecting.

## Approach

Adopt a centralized server model (like tmux): a single daemon process manages all
PTY sessions and hub WebSocket connections. User-facing commands are thin clients
that communicate with this daemon over a Unix socket.

Additionally, merge `codetty-hub` and `codetty-cli` into a single `codetty` binary
with subcommand routing.

---

## Architecture

### Process Roles

| Role | Description |
|------|-------------|
| **Server (daemon)** | Background process owning all PTY sessions and hub WebSocket connections |
| **Client** | Short-lived foreground process; connects to server via Unix socket for terminal I/O |

### Data Flow

```
[User Terminal] ←(unix socket)→ [codetty server daemon] ←(pty)→ shell/command
                                         ↓
                                 [WebSocket × N → hub]
```

### Lifecycle

```
$ codetty -s work                  # First run
  → Detect server → not running → fork daemon (codetty __server)
  → Server creates PTY session "work" (starts $SHELL)
  → Client attaches to "work"
  → User works...
  → SSH disconnects / Ctrl+A d → client exits, server + PTY continue

$ codetty attach -t work           # After re-SSH
  → Connect to existing server
  → Attach to session "work"
  → Screen snapshot restores terminal state
  → Continue working
```

---

## Unified Binary: `codetty`

### Command Structure

```bash
# Hub mode (replaces codetty-hub)
codetty hub [--addr :8080] [--cert F] [--key F] [--ca F] [--insecure]

# Session operations (default mode, replaces codetty-cli)
codetty [-s name] [-- command]           # Create session + attach (default)
codetty new [-s name] [-- command]       # Explicit create
codetty attach [-t session]              # Attach to existing session
codetty ls                               # List sessions
codetty kill [-t session]                # Kill a session
codetty kill-server                      # Shutdown daemon and all sessions

# Legacy/script mode (foreground, no daemon, for scripts/CI)
codetty run [flags] -- command           # One-shot mode, no server, exits with command
                                         # Equivalent to current codetty-cli behavior

# Internal (not user-facing)
codetty __server                         # Daemon server mode
```

### Default Behavior

- `codetty` with no subcommand = `codetty new` (create session + attach)
- No command specified = starts `$SHELL`
- Shell detection: `$SHELL` → `/bin/sh` (Unix) / `powershell.exe` → `cmd.exe` (Windows)

### Session Naming

- `-s <name>`: explicit name
- Omitted: auto-generated as `<command>-<index>` (e.g., `bash-0`, `bash-1`)

### tmux Alignment

| tmux | codetty |
|------|---------|
| `tmux new -s work` | `codetty new -s work` |
| `tmux attach -t work` | `codetty attach -t work` |
| `tmux kill-session -t work` | `codetty kill -t work` |
| `tmux ls` | `codetty ls` |
| `Ctrl+B d` | `Ctrl+A d` |
| `Ctrl+B n/p` | `Ctrl+A n/p` |

---

## Server Daemon

### Daemonization

When a client command discovers no running server:
1. Exec self as `codetty __server` with `setsid` (new session, detach from terminal)
2. Close stdin/stdout/stderr (redirect to log file)
3. Write PID to `~/.codetty/server.pid`
4. Listen on `~/.codetty/server.sock`

### Socket Path

```
~/.codetty/server.sock       # Unix domain socket
~/.codetty/server.pid        # PID file for liveness checks
~/.codetty/server.log        # Optional daemon log
```

### Exit Conditions

- Last session's PTY exits and no remaining sessions → server auto-exits
- `codetty kill-server` → sends SIGHUP to all PTYs, waits, cleans up socket

### Signal Handling (Server)

- `SIGHUP` → ignored (daemon must not die on hangup)
- `SIGTERM` → graceful shutdown (SIGHUP to PTYs, wait, cleanup)

---

## Local Protocol (Unix Socket)

### Frame Format

```
[4 bytes: length (big-endian)][1 byte: type][payload...]
```

### Message Types

| Type | Direction | Payload | Description |
|------|-----------|---------|-------------|
| `0x01` | C→S | JSON | Request: {action, session, cmd, env, cols, rows} |
| `0x02` | S→C | JSON | Response: {ok, error, sessions[]} |
| `0x03` | S→C | raw bytes | Terminal output from PTY |
| `0x04` | C→S | raw bytes | Keyboard input to PTY |
| `0x05` | C→S | JSON | Resize: {cols, rows} |
| `0x06` | C→S | (empty) | Detach |
| `0x07` | C→S | JSON | Session switch: {target} |
| `0x08` | S→C | JSON | Notification: {event, session, exit_code} |

### Request Actions

- `new_session`: Create PTY session (fields: session name, command, env, cols, rows, hub config)
- `attach`: Attach to existing session (fields: target session name)
- `list`: Query all sessions
- `kill`: Kill specified session
- `kill_server`: Shutdown server

---

## Attach Screen Restoration

When a client attaches to an existing session:

1. Server renders current screen via `ScreenMirror.SnapshotANSI()`
2. Sends snapshot as initial Output frame
3. Subsequent PTY output streamed in real-time

This reuses the existing `ScreenMirror` / `vt10x` infrastructure that currently
powers hub reconnect replays.

---

## Multi-Client Attach

Multiple clients can attach to the same session simultaneously (tmux behavior):
- All clients receive the same output
- Any client's input is forwarded to the PTY
- Terminal size = minimum of all attached clients' dimensions
- When a client detaches, size recalculates from remaining clients

---

## Detach Behavior

| Trigger | Behavior |
|---------|----------|
| `Ctrl+A d` | Explicit detach (client sends Detach frame, exits) |
| SSH disconnect | Socket EOF detected by server → implicit detach |
| External `kill <client-pid>` | Socket closes → implicit detach |

### Raw Mode Input

- `Ctrl+C`, `Ctrl+Z`, etc. are NOT intercepted by the client
- They are forwarded as raw bytes to the PTY (tmux behavior)
- Only the prefix sequence (`Ctrl+A`) is intercepted by the client

### Session Switch While Attached

- `Ctrl+A n` → next session
- `Ctrl+A p` → previous session
- `Ctrl+A <number>` → switch to session N

On switch, client sends SessionSwitch frame; server responds with new session's
screen snapshot.

---

## Session Exit Handling

When a PTY process exits:
1. Server detects `Terminal.Wait()` return
2. Destroys the session immediately (tmux default behavior)
3. Notifies all attached clients via Notification frame: `{event: "session_exited", exit_code: N}`
4. Clients restore terminal state and exit with appropriate message

---

## Client Signal Handling

- `SIGHUP` (SSH hangup) → close socket connection (implicit detach)
- `SIGTERM` → close socket (implicit detach, does NOT kill session)
- `SIGWINCH` → read new terminal size, send Resize frame to server

---

## Hub Integration

### Per-Session Hub Connection

Each session independently maintains a WebSocket connection to the hub:

```
Session "work"  ←→ hub WebSocket (clientID: "work")
Session "build" ←→ hub WebSocket (clientID: "build")
```

- Reuses existing `connectionLoop` logic (auto-reconnect)
- Hub connection persists regardless of client attach state
- Output is always pushed to hub even when no client is attached

### Hub Connection Configuration

Hub parameters are provided at session creation time and stored in session metadata:

```bash
codetty -s work --server wss://hub.example.com/ws --cert-dir ~/.codetty/certs
```

### No-Hub Mode

For users who only want detach/attach without a remote hub:

```bash
codetty -s local --no-hub    # Pure local session, no hub connection
```

### Hub Transparency

From the hub's perspective, nothing changes:
- Each session is still a WebSocket client
- Register, output forwarding, replay, resize behavior is identical
- Client detach/attach is transparent to hub

---

## Code Organization

### Package Structure

```
cmd/codetty/
  main.go          # Subcommand routing
  hub.go           # codetty hub (migrated from cmd/codetty-hub)
  cli.go           # new/attach/ls/kill subcommand handlers
  server.go        # __server daemon entry point
  run.go           # Foreground one-shot mode (legacy compat)

daemon/            # New package: server core logic
  server.go        # Server struct, accept loop, client management
  session.go       # Session lifecycle, PTY management
  client.go        # Client connection handling, I/O forwarding
  protocol.go      # Local socket frame encode/decode
  hubconn.go       # Per-session hub WebSocket management

hub/               # Unchanged: hub TUI logic
protocol/          # Unchanged: hub WebSocket protocol
certs/             # Unchanged: TLS utilities
```

### Code Reuse

Directly reusable from current codebase:
- `terminalSession` interface and platform implementations (PTY management)
- `ScreenMirror` (vt10x screen snapshot/replay)
- `connectionLoop` + `runConnection` (hub WebSocket reconnection)
- `protocol` package (hub frame encode/decode)
- `buildClientIdentity` (naming logic)
- TLS/cert infrastructure

### Migration Path

| Phase | Action |
|-------|--------|
| Development | New `cmd/codetty/` coexists with old `cmd/codetty-hub/`, `cmd/codetty-cli/` |
| Feature-complete | Old directories marked deprecated, `build.sh` builds only `codetty` |
| Final | Remove old cmd directories |

---

## Platform Support

### Unix Socket

- Linux/macOS: native AF_UNIX
- Windows 10 1803+ / Windows Server 2019+: AF_UNIX supported via Winsock
- Go `net.Listen("unix", path)` works cross-platform

### PTY

- Existing `terminalSession` abstraction handles platform differences
- Unix: `creack/pty`
- Windows: ConPTY (via `UserExistsError/conpty`)
