# Tmux Session Support Design

**Date:** 2026-05-24
**Status:** Draft

## Summary

CodeTTY will auto-discover existing tmux sessions and display them as panels alongside native sessions in the TUI. The implementation reuses the existing `DaemonSession` infrastructure by spawning `tmux attach -t <name>` as the session command—no new output protocol or rendering path needed.

## Goals

- Let users view and interact with tmux sessions inside CodeTTY's TUI
- No disruption to existing native session workflow
- Graceful degradation when tmux is not installed
- Users retain full control: can hide unwanted tmux panels, and "kill" only detaches (doesn't destroy the tmux session)

## Non-Goals

- Replacing CodeTTY's native PTY sessions with tmux
- Supporting tmux control mode (-C) or capture-pane polling
- Managing tmux session creation/destruction from within CodeTTY
- Cross-platform tmux support (tmux is Unix-only; Windows users unaffected)

## Technical Approach

### Core Idea

A tmux session in CodeTTY is just a `DaemonSession` whose command is `["tmux", "attach-session", "-t", "<tmux_session_name>"]`. The existing readLoop → ScreenMirror → broadcast pipeline works unchanged because `tmux attach` writes its rendered output to the PTY like any other program.

### Discovery

**When:**
- Daemon startup (automatic)
- TUI user triggers manual refresh (Ctrl+A T)

**How:**
```
tmux list-sessions -F "#{session_name}"
```

Filters:
- Skip sessions already managed by a CodeTTY DaemonSession (match by `TmuxTarget` field)
- If `tmux` binary is not found, skip discovery silently

### Data Model Changes

`DaemonSession` gains two fields:

```go
type DaemonSession struct {
    Name       string
    Command    []string
    Type       string // "native" | "tmux"
    TmuxTarget string // original tmux session name (Type=="tmux" only)
    // ... existing fields unchanged
}
```

### Protocol Changes

**`daemon/protocol.go`:**
- New action: `ActionRefreshTmux Action = "refresh_tmux"`
- `SessionInfo` gains: `Type string` field

**New frame type not needed** — refresh uses existing FrameRequest/FrameResponse.

### Server Changes (`daemon/server.go`)

- On startup (after `Start()`): call `discoverAndAttachTmux()`
- New handler: `handleRefreshTmux(conn, req)` — re-runs discovery, creates DaemonSessions for newly found tmux sessions
- Discovery creates sessions with default size (cols=80, rows=24). When the TUI subscribes and receives the session list, it sends a resize for each tmux session to match the actual panel dimensions. This matches the existing behavior for native sessions created before a TUI connects.

### New File: `daemon/tmux.go`

```go
// IsTmuxAvailable checks if the tmux binary exists in PATH.
func IsTmuxAvailable() bool

// DiscoverTmuxSessions returns the list of tmux session names.
func DiscoverTmuxSessions() ([]string, error)
```

### TUI Changes

**`cmd/codetty/tui_input.go`:**
- Ctrl+A T → send `ActionRefreshTmux` request to daemon

**`cmd/codetty/tui_session.go` / `tui_view.go`:**
- Panel title rendering: if session type is "tmux", prepend `[tmux] ` to name

**`cmd/codetty/tui.go`:**
- `tuiSession` gains a `Type` field populated from `SessionInfo.Type`

### Lifecycle Semantics

| Operation | Native Session | Tmux Session |
|-----------|---------------|--------------|
| Ctrl+A x (kill) | Terminates the process | Kills `tmux attach` subprocess (tmux session itself stays alive) |
| Session process exits externally | Panel removed + notification | Same (tmux attach exits → panel removed + notification) |
| Ctrl+A d (detach from CodeTTY) | Session keeps running in daemon | Same |
| Manual refresh (Ctrl+A T) | N/A | Discovers new tmux sessions, creates DaemonSessions |

### Resize

When the TUI resizes a tmux panel:
1. `DaemonSession.Resize()` resizes the underlying PTY
2. `tmux attach` receives SIGWINCH → tmux re-renders at new size
3. Output flows back through the normal path

**Multi-attach caveat:** If the same tmux session is attached from both CodeTTY and another terminal, tmux uses the smallest client size. This is standard tmux behavior and not something CodeTTY needs to solve.

### Error Handling

| Scenario | Handling |
|----------|----------|
| tmux not installed | Skip discovery, no error shown |
| `tmux attach` fails (session gone) | Session creation fails silently, logged |
| tmux server crashes mid-session | All tmux DaemonSessions exit → auto-remove panels + notifications |
| Permission denied | `tmux attach` exits immediately → panel not shown |
| Duplicate discovery (session already attached) | Filtered out by TmuxTarget check |

### Hiding/Showing Tmux Panels

- User can "hide" a tmux panel via Ctrl+A x (which detaches)
- Hidden tmux sessions reappear on next manual refresh (Ctrl+A T) unless the underlying tmux session was destroyed
- No persistent "blocklist" in v1 — user hides per-session-of-CodeTTY

## Files to Change

| File | Change |
|------|--------|
| `daemon/tmux.go` | **New** — discovery logic |
| `daemon/session.go` | Add `Type`, `TmuxTarget` fields |
| `daemon/server.go` | Startup discovery, `handleRefreshTmux`, helper `discoverAndAttachTmux()` |
| `daemon/protocol.go` | Add `ActionRefreshTmux`, `Type` to `SessionInfo` |
| `cmd/codetty/tui_input.go` | Ctrl+A T keybinding |
| `cmd/codetty/tui_session.go` | `[tmux]` prefix in panel title |
| `cmd/codetty/tui.go` | `Type` field on `tuiSession` |

## Testing Strategy

- Unit test for `DiscoverTmuxSessions()` with mocked `tmux` command output
- Integration test: start a real tmux session, run CodeTTY daemon, verify the tmux session appears in `ActionList` response with correct type
- Edge case test: tmux not installed → verify no panic, graceful skip
- Edge case test: tmux session killed externally → verify DaemonSession exits cleanly
