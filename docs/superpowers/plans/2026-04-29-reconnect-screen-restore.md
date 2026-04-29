# Reconnect Screen Restore Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When a client reconnects to the hub after hub restart or network drop, immediately restore the current terminal display without waiting for new PTY output.

**Architecture:** `codetty-cli` mirrors PTY output into a local `vt10x.Terminal`. On every connection, after `MsgRegister`, it sends a `MsgReplay` ANSI snapshot that restores visible cells, cursor state, and exposed terminal modes. The hub feeds that snapshot into its fresh vt10x session and, when already in fullscreen, writes the snapshot directly to the fullscreen writer.

**Tech Stack:** Go, gorilla/websocket, hinshun/vt10x, creack/pty

---

## File Structure

| File | Role | Action |
|------|------|--------|
| `protocol/protocol.go` | Protocol message types and encoding | `MsgReplay` is an ANSI screen snapshot |
| `protocol/protocol_test.go` | Protocol unit tests | Covers `EncodeReplay` and parsing |
| `cmd/codetty-cli/screen_snapshot.go` | Client-side screen mirror and snapshot renderer | Creates replay snapshots and drains stale output |
| `cmd/codetty-cli/screen_snapshot_test.go` | Snapshot regression tests | Covers full-screen restore, terminal modes, repeated replay, queue draining |
| `cmd/codetty-cli/main.go` | CLI entry point and reconnect loop | Feeds PTY output into mirror and sends snapshot replay |
| `hub/hub.go` | Hub connection handler | Handles `MsgReplay` and fullscreen replay forwarding |
| `hub/hub_test.go` | Hub integration tests | Covers replay restore and fullscreen writer forwarding |

---

## Final Task Set

### Task 1: Protocol replay message

- [x] Add `MsgReplay` to `protocol/protocol.go`.
- [x] Add `EncodeReplay(data []byte) []byte`.
- [x] Add protocol tests for encode and `ParseType`.

### Task 2: Client-side screen snapshot

- [x] Add `ScreenMirror` backed by `vt10x.Terminal`.
- [x] Feed PTY output into the mirror in the PTY reader loop.
- [x] Serialize snapshots that clear the target screen, restore terminal modes, repaint cells, restore cursor position, and restore cursor visibility.
- [x] Drain queued output while holding the replay mutex so already-snapshotted chunks are not resent as stale `MsgOutput`.
- [x] Keep the mirror size aligned with resize messages.

### Task 3: Hub replay handling

- [x] Feed `MsgReplay` into the hub-side session vt10x.
- [x] Send one TUI refresh after replay when not in fullscreen.
- [x] Forward snapshot replay directly to the fullscreen writer when the matching session is already in fullscreen.

### Task 4: Verification

- [x] Add regression tests for:
  - full-screen snapshot restore from a blank terminal;
  - empty snapshot before any PTY output;
  - terminal mode restore (`ModeAltScreen`, `ModeAppCursor`, wrap, SGR mouse);
  - repeated replay into fresh terminals;
  - queue draining before replay;
  - hub replay screen restore;
  - fullscreen replay writer forwarding.
- [x] Run `go test ./...`.
- [x] Run `go build ./cmd/codetty-cli/` and `go build ./cmd/codetty-hub/`.
- [x] Use tmux to start real `copilot` in `/root/logsim-mount/tmp` and verify the hub still shows the Copilot screen after two consecutive hub restarts.

---

## Notes

The initial ring-buffer approach was intentionally removed. A raw suffix of PTY
output cannot reliably reconstruct a full-screen terminal from blank state,
because it may start after the application painted the full frame. The final
implementation restores from a client-side terminal snapshot instead.
