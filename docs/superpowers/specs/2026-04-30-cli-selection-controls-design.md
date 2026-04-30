# CLI Selection Controls Design

## Problem

`codetty-cli` currently uses the current directory basename as its default client ID. Starting multiple clients from the same directory produces the same ID, so the hub treats the new connection as a replacement and only the last client remains visible.

The hub also needs faster selection controls beyond the existing `Ctrl+A n/p` prefix commands:

- Direct `Shift+Tab` should switch the selected client.
- Mouse clicks should select the clicked client panel.

## Goals

- Multiple default `codetty-cli` processes from the same directory can connect at the same time.
- Explicit duplicate `--id` keeps the current replace/reconnect behavior.
- `Shift+Tab` directly cycles the selected client without requiring the `Ctrl+A` prefix.
- Plain `Tab` remains forwarded to the selected remote PTY.
- Mouse single-click selects a client panel in hub-managed layout views.
- Fullscreen input and mouse handling remain raw/remote-oriented.

## Non-goals

- No persistent layout or selection configuration.
- No new command-line config for naming patterns.
- No hub-side duplicate-ID suffixing.
- No mouse-driven fullscreen entry; click selects only.
- No duplicate internal/display ID model in the protocol.

## Design

### Default client identity

The CLI will distinguish between implicit and explicit IDs.

- If the user does not pass `--id`, the CLI generates a process-unique ID at startup using the current directory basename, hostname, and process ID, for example `tmp-myhost-12345`.
- The generated ID remains stable for the life of the client process, so reconnects from the same process still map to the same session.
- The default title remains readable by showing the directory basename plus process ID, for example `claude@tmp#12345`.
- If the user passes `--id`, the CLI uses that value exactly and keeps the current title shape, for example `claude@my-id`.

The hub keeps its current session map semantics: a new connection with an existing ID replaces the old session. This preserves intentional replacement/reconnect behavior for explicit IDs while fixing the common same-directory default case.

### Keyboard selection

`Shift+Tab` becomes a direct hub shortcut outside fullscreen mode.

- It cycles selection through the current ordered session list.
- It uses the same selection state as `Ctrl+A n/p`, input forwarding, fullscreen entry, and rendering.
- `Tab` continues to use `keyToBytes` and is forwarded to the selected PTY.
- Existing prefix commands remain unchanged.

`Shift+Tab` will cycle to the next visible client in layout order. This gives users a single-key “switch client” action while preserving `Ctrl+A p` for explicit previous selection.

### Mouse selection

Mouse handling applies only in hub-managed views: grid and focus-big.

- A left-button click inside a client panel selects that client.
- Clicking the status bar, empty filler cells, hidden `+N more` indicators, or outside rendered panel bounds does nothing.
- Click selection does not reorder clients and does not enter fullscreen.
- In fullscreen mode, the hub fullscreen raw path remains responsible for input, so remote TUIs can continue to receive mouse input where supported.

### Layout hit testing

Rendering and mouse hit testing should share layout geometry rather than duplicating formulas by hand.

Add a small layout geometry layer that computes panel rectangles for the current view:

- Grid geometry uses the same ordered sessions, fixed/auto column logic, visible row trimming, and selected-row scrolling as `gridView`.
- Focus-big geometry mirrors wide and narrow focus-big placement, including secondary caps and hidden panels.
- Each hit region stores the ordered session index and a rectangle `(x, y, w, h)`.

The render functions can keep their current visual output, but mouse handling should use this shared geometry so future layout changes do not silently break click selection.

## Data flow

1. `codetty-cli` starts.
2. If `--id` was omitted, it builds a unique default ID and readable title.
3. The client registers with the hub using the generated or explicit ID.
4. The hub stores sessions exactly as it does today: unique by ID.
5. The TUI syncs layout order from active sessions.
6. `Shift+Tab` updates `Model.selected`.
7. A mouse click is converted to a hit-test against current layout regions; if it hits a client panel, `Model.selected` is updated.
8. Input forwarding, fullscreen entry, grid rendering, and focus-big rendering all continue to use the same ordered session list and selected index.

## Error handling and edge cases

- If hostname lookup fails, the implicit ID uses `unknown` for the hostname segment while still including basename and process ID.
- If the selected index is outside the active session list after disconnects, existing clamping behavior remains.
- Mouse clicks while there are no sessions do nothing.
- Mouse clicks on panels not currently rendered, including hidden focus-big secondaries, do nothing.
- Same explicit `--id` behavior remains replace-based, including after reconnects.
- Multiple implicit clients with the same basename and hostname still differ by process ID.

## Testing

Automated tests should cover:

- Implicit default identity generation is unique for same-directory processes.
- Explicit `--id` keeps replacement semantics in the hub.
- `Shift+Tab` changes selection and subsequent typed input goes to the newly selected session.
- Plain `Tab` is still forwarded to the selected PTY.
- Grid mouse click selects the clicked panel.
- Focus-big mouse click selects visible secondary panels.
- Mouse clicks on status/empty/hidden regions do not change selection.
- Fullscreen mode ignores Bubble Tea selection shortcuts and preserves existing fullscreen behavior.

Manual/tmux verification should cover:

- Start two `codetty-cli` instances from `/root/logsim-mount/tmp`; both remain visible.
- `Shift+Tab` switches selection and input target.
- Mouse click on each panel changes the selected/focused panel.
