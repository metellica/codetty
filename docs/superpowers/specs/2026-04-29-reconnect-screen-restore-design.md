# Reconnect Screen Restore — Design Spec

## Problem

When the codetty-hub server restarts, all `vt10x.Terminal` session state is lost.
Clients (codetty-cli) reconnect automatically, but the new session starts with an
empty virtual terminal — the TUI shows a black screen until the remote PTY
produces new output.

The same problem occurs when a client disconnects and reconnects while the hub is
running: `addSession` replaces the old session with a fresh one that has an empty
vt10x.

## Approach

Client-side ring buffer with replay on reconnect.

The client maintains a fixed-size ring buffer of recent raw PTY output. On every
(re)connection, after sending `MsgRegister`, the client sends a `MsgReplay`
message containing the buffered output. The hub feeds this data into the new
session's `vt10x.Terminal`, reconstructing the terminal screen state.

## Protocol Change

New message type:

```
MsgReplay MsgType = 'R'   // cli → hub: buffered PTY output for screen recovery
```

Message sequence on connect:

1. `MsgRegister` — session registration (unchanged)
2. `MsgReplay` — ring buffer contents (raw PTY output bytes). Omitted if buffer
   is empty (first connection with no output yet).
3. Normal `MsgOutput` stream resumes.

Encoding: `EncodeReplay(data []byte) []byte` — prepends `'R'` byte to data,
same pattern as existing encode functions.

## Client-Side Ring Buffer

### Data Structure

New file `cmd/codetty-cli/ringbuf.go`:

```go
type RingBuffer struct {
    mu   sync.Mutex
    buf  []byte
    size int   // fixed capacity
    pos  int   // next write position
    full bool  // true once buffer has wrapped around
}
```

### Behavior

- **Capacity:** 256KB default, configurable via `--replay-buf` flag.
- **Write:** Called from the PTY reader goroutine after each `ptmx.Read()`.
  Appends data, wraps around when full.
- **Bytes():** Returns the buffer contents in chronological order (handles
  wrap-around). Called during reconnection.
- **Thread safety:** `sync.Mutex` protects all access. The PTY reader writes
  concurrently with the reconnection sender reading.

### Integration Point

In `main.go`, the PTY reader loop writes to both `os.Stdout`, `outputCh`, and
the ring buffer:

```go
// existing: os.Stdout.Write(buf[:n])
// existing: outputCh <- cp
ringBuf.Write(cp)   // new
```

In `runConnection()`, after sending `MsgRegister`:

```go
replay := ringBuf.Bytes()
if len(replay) > 0 {
    conn.WriteMessage(websocket.BinaryMessage, protocol.EncodeReplay(replay))
}
```

## Hub-Side Handling

In `hub.go` `HandleConn`, add a case for `MsgReplay`:

```go
case protocol.MsgReplay:
    if sess == nil {
        return
    }
    sess.WriteOutput(payload)
    // Send a single refresh notification to TUI (not per-byte updates)
    if h.program != nil {
        h.fsMu.RLock()
        inFullscreen := h.fullscreenID != ""
        h.fsMu.RUnlock()
        if !inFullscreen {
            h.program.Send(ScreenUpdateMsg{ID: sess.ID, Data: nil})
        }
    }
```

Key decisions:

- Replay data is fed into `vt10x` identically to normal output — vt10x
  correctly parses all ANSI escape sequences to rebuild terminal state.
- Only one `ScreenUpdateMsg` is sent after the entire replay, avoiding
  intermediate render flicker.
- In fullscreen mode, replay is not written to stdout (no visual flash of
  scrolling history). The vt10x state is updated silently; exiting fullscreen
  reads the correct state.
- `ScreenUpdateMsg.Data` being nil signals a refresh-only update. The TUI's
  `View()` already reads from vt10x on every render, so no data payload is
  needed.

## Edge Cases

| Case | Handling |
|------|----------|
| First connection (no prior output) | Ring buffer empty, no `MsgReplay` sent |
| Hub in fullscreen during replay | vt10x updated, stdout not written to |
| Multiple clients reconnecting simultaneously | Each client sends its own replay, sessions are independent |
| Replay data exceeds vt10x terminal size | vt10x handles scrollback naturally; only visible screen matters |
| Client process exited before reconnection | Ring buffer retains the last output; replay still works |

## Files Changed

| File | Change |
|------|--------|
| `protocol/protocol.go` | Add `MsgReplay` constant, `EncodeReplay()` function |
| `cmd/codetty-cli/ringbuf.go` | New file: `RingBuffer` implementation |
| `cmd/codetty-cli/main.go` | Integrate ring buffer in PTY reader, send replay on connect |
| `hub/hub.go` | Handle `MsgReplay` in `HandleConn` |

## Testing

- **RingBuffer unit tests** (`cmd/codetty-cli/ringbuf_test.go`): write/read,
  wrap-around, empty buffer, concurrent access.
- **Protocol tests** (`protocol/protocol_test.go`): `EncodeReplay` round-trip
  with `ParseType`.
- **Hub integration test** (`hub/hub_test.go`): simulate Register → Replay →
  verify vt10x screen content matches expected state.
