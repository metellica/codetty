# Reconnect Screen Restore — Design Spec

## Problem

When `codetty-hub` restarts, all hub-side `vt10x.Terminal` state is lost.
Clients reconnect automatically, but a fresh hub session starts with an empty
virtual terminal, so the TUI can show a black or partial screen until the remote
PTY produces new output.

The same class of problem can happen after network reconnects: replaying only a
suffix of raw PTY output is not equivalent to restoring current terminal state.
Full-screen TUIs like Copilot often paint the whole frame once and later emit
small cursor-addressed updates. If replay starts from the middle of that stream,
the hub cannot reconstruct the missing frame from an empty terminal.

## Approach

Client-side screen mirror with snapshot replay.

`codetty-cli` maintains a local `vt10x.Terminal` mirror of the PTY output. On
every connection, after `MsgRegister`, the client serializes the current mirror
into an ANSI screen snapshot and sends it as `MsgReplay`. The hub feeds this
snapshot into its fresh vt10x session, rebuilding the current visible screen from
blank state.

This replaces the initial raw-output suffix approach. Recent bytes cannot
guarantee a valid terminal-state boundary. A screen snapshot is bounded by
terminal size and always describes the current display.

## Protocol Change

Message type:

```go
MsgReplay MsgType = 'R' // cli -> hub: ANSI screen snapshot for recovery
```

Message sequence on connect:

1. `MsgRegister` — session registration with the current mirrored terminal size.
2. `MsgReplay` — complete ANSI snapshot of the current visible screen. Omitted
   if the client has not seen any PTY output yet.
3. Normal `MsgOutput` stream resumes.

`EncodeReplay(data []byte) []byte` prepends `'R'` to the snapshot bytes, matching
the existing message encoding style.

## Client-Side Screen Mirror

New file: `cmd/codetty-cli/screen_snapshot.go`.

`ScreenMirror` owns a `vt10x.Terminal`:

- `Write(data []byte)` feeds PTY output into the mirror.
- `Resize(cols, rows uint16)` keeps the mirror aligned with PTY resize messages.
- `Size()` returns the current mirrored terminal size for `MsgRegister`.
- `Snapshot()` returns ANSI bytes that restore exposed terminal modes, clear the
  target, repaint every visible cell, restore attributes, move the cursor, and
  restore cursor visibility.

The PTY reader writes output to local stdout, the mirror, and the hub output
queue. A shared mutex serializes mirror updates, output queue writes, queue
draining, and snapshot creation.

## Queue Draining on Reconnect

During hub downtime, `outputCh` can contain output chunks that have already been
applied to the screen mirror. If the client sends a snapshot and then sends those
queued chunks again, the hub can duplicate stream output or disturb cursor state.

Before creating the reconnect snapshot, the client drains `outputCh` while
holding the same mutex used by the PTY reader. This guarantees:

- chunks produced before the snapshot are included in the snapshot and not resent;
- chunks produced after the snapshot are queued and sent normally after replay.

## Hub-Side Handling

In `hub.go` `HandleConn`, `MsgReplay` is handled like a state restore:

```go
case protocol.MsgReplay:
    if sess == nil {
        return
    }
    sess.WriteOutput(payload)
    if h.fullscreenID == sess.ID && h.fsWriter != nil {
        h.fsWriter.Write(payload)
    }
    if h.program != nil && !inFullscreen {
        h.program.Send(ScreenUpdateMsg{ID: sess.ID, Data: nil})
    }
```

Replay data is a snapshot, not raw history. It updates vt10x, triggers a single
TUI refresh outside fullscreen, and directly repaints the fullscreen writer when
the matching session is already in fullscreen.

## Edge Cases

| Case | Handling |
|------|----------|
| First connection before PTY output | `Snapshot()` returns nil; no replay sent |
| Hub restart while client is idle | Client sends a complete screen snapshot immediately after reconnect |
| Multiple hub restarts | Snapshot is generated from the client-side mirror each time |
| Output produced while hub is down | Included in snapshot; stale queued chunks are drained before replay |
| Output produced after snapshot | Sent as normal `MsgOutput` after replay |
| Resize before reconnect | `MsgRegister` uses the mirror's current size |

## Files Changed

| File | Change |
|------|--------|
| `protocol/protocol.go` | Add `MsgReplay` constant and `EncodeReplay()` |
| `cmd/codetty-cli/screen_snapshot.go` | Add `ScreenMirror`, ANSI snapshot rendering, queue draining |
| `cmd/codetty-cli/main.go` | Feed PTY output into mirror, send snapshot on connect, keep mirror resized |
| `hub/hub.go` | Handle `MsgReplay` in `HandleConn` |

## Testing

- Protocol tests cover `MsgReplay` encoding and parsing.
- Screen snapshot tests verify a snapshot restores a full Copilot-like screen
  from blank state.
- Queue-drain tests verify reconnect snapshots consume queued output that is
  already represented in the snapshot.
- Snapshot tests verify terminal mode restore for alt-screen, app-cursor, wrap,
  and SGR mouse modes.
- Hub tests verify `MsgReplay` restores vt10x screen content and directly
  repaints the fullscreen writer.
- tmux verification starts real `copilot` in `/root/logsim-mount/tmp` and
  confirms the hub displays the full terminal after two consecutive hub restarts.
