# Unified TUI Design: Merged Hub + Daemon Client

## Problem Statement

CodeTTY currently has two separate interfaces:
- `codetty hub` — a Bubble Tea TUI showing remote sessions in grid/focus view
- `codetty new/attach` — raw terminal mode for daemon sessions with Ctrl+A shortcuts

This creates a split experience. The goal is to merge them: `codetty` becomes a
single TUI that manages local daemon sessions using the same visual framework as
the hub (focus-big layout), with full Ctrl+A command support.

## Design

### Entry Points

| Command | Behavior |
|---------|----------|
| `codetty` | Ensure daemon running. No sessions → create default shell. Has sessions → focus most recent. Enter TUI. |
| `codetty new [-s name] [cmd]` | Ensure daemon. Create session. Enter TUI focused on it. |
| `codetty a [-s name]` | Ensure daemon. Enter TUI focused on named/latest session. |
| `codetty ls` | Print session list to stdout (no TUI). |
| `codetty kill -s name` | Kill session (no TUI). |
| `codetty kill-server` | Stop daemon and all sessions (no TUI). |
| `codetty hub [flags]` | Start hub WebSocket server (existing functionality, unchanged). |

### TUI Architecture

The TUI is a Bubble Tea program that connects to the daemon over Unix socket.
It does NOT embed the daemon — the daemon runs as a separate process.

```
┌────────────────────────────────────────────────┐
│  codetty (Bubble Tea TUI)                      │
│                                                │
│  ┌─ daemon client ──────────────────────────┐  │
│  │  Unix socket → daemon server             │  │
│  │  Receives: output frames per session     │  │
│  │  Sends: input, resize, create, kill      │  │
│  └──────────────────────────────────────────┘  │
│                                                │
│  ┌─ TUI model ─────────────────────────────┐  │
│  │  layoutState (focus-big / grid)          │  │
│  │  sessions[] with vt10x terminals         │  │
│  │  status bar                              │  │
│  └──────────────────────────────────────────┘  │
└────────────────────────────────────────────────┘
         │ Unix socket
         ▼
┌────────────────────────────────────────────────┐
│  daemon server                                 │
│  DaemonSession[] (PTY + ScreenMirror + Hub)    │
└────────────────────────────────────────────────┘
```

### Layout Modes

Reuses existing hub layout code (`hub/layout.go`, `hub/layout_view.go`):

1. **Focus-big** (default): Selected session occupies ~80% of terminal width.
   Other sessions shown as small thumbnails on the side. This is the mode
   entered on `codetty`, `codetty new`, `codetty a`.

2. **Grid**: All sessions shown equally sized. Entered via `Ctrl+A g`.
   Click or Enter on a session returns to focus-big on that session.

### Keyboard Bindings (Ctrl+A prefix)

In focus-big mode, all non-prefixed input goes to the focused session's PTY.

| Key | Action |
|-----|--------|
| `Ctrl+A d` | Detach — exit TUI, daemon continues running |
| `Ctrl+A x` | Close/kill the currently focused session |
| `Ctrl+A c` | Create a new session (default shell) |
| `Ctrl+A n` | Focus next session |
| `Ctrl+A p` | Focus previous session |
| `Ctrl+A g` | Switch to grid overview |
| `Ctrl+A 1-9` | Jump to session by index |
| `Ctrl+A Ctrl+A` | Send literal Ctrl+A to focused session |

In grid mode, arrow keys navigate, Enter selects (returns to focus-big).

### Daemon Protocol Extension

The existing daemon protocol (Unix socket frames) needs a new mode: **multi-session
subscription**. Currently a client attaches to ONE session. The TUI needs output
from ALL sessions simultaneously.

New action: `ActionSubscribeAll`

```go
ActionSubscribeAll Action = "subscribe_all"
```

Server behavior for subscribe_all:
- Send a response listing all current sessions
- Begin forwarding output from ALL sessions (each frame tagged with session name)
- Accept input frames tagged with a target session
- Notify on session create/exit events

New frame types for multi-session mode:

```go
FrameTaggedOutput    FrameType = 0x10  // [session_name_len][session_name][output_data]
FrameTaggedInput     FrameType = 0x11  // [session_name_len][session_name][input_data]
FrameSessionEvent    FrameType = 0x12  // JSON: {event, session, ...}
FrameCreateSession   FrameType = 0x13  // JSON: {name, command, cols, rows}
FrameKillSession     FrameType = 0x14  // JSON: {name}
```

### TUI Session Model

Each session in the TUI holds:
- Name, command, created_at
- A `vt10x.Terminal` for local rendering (same as hub's ClientSession)
- Connection state (alive/exited)

On connect, the TUI receives snapshots for all sessions (replayed into vt10x).
Then it receives live tagged output and feeds it into the appropriate vt10x terminal.

### Status Bar

Bottom of screen shows:
```
[session-name] 1/3 | Ctrl+A ? for help | hub: connected
```

### Daemon Lifecycle

- `ensureDaemon()` called on TUI start (unchanged from current implementation)
- Daemon auto-exits when last session's PTY exits AND no TUI client is connected
- `codetty kill-server` forcefully stops daemon regardless of state
- Multiple TUI clients can connect simultaneously (each sees all sessions)

### What's NOT Changing

- `codetty hub` subcommand — stays as-is (WebSocket server + TUI for remote sessions)
- Daemon server core (`daemon/server.go`, `daemon/session.go`) — only extended with subscribe_all
- Hub connection per session — each daemon session can still connect to a remote hub
- `codetty ls` / `codetty kill` / `codetty kill-server` — stay as non-TUI commands

### Implementation Strategy

1. Extend daemon protocol with multi-session subscription
2. Create TUI model in `cmd/codetty/tui.go` reusing hub's layout/rendering code
3. Replace raw-terminal `runAttachLoop` with TUI for `new`/`attach`/bare `codetty`
4. Keep single-session raw mode available as `codetty run` fallback

### Future Work (not this iteration)

- Remote sessions displayed alongside local sessions in the same TUI
- Session groups / workspaces
- Split panes within a single session
