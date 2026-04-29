# Prefix Layout Controls — Design Spec

## Problem

The hub TUI currently lays out sessions by connection order and auto-balanced
grid sizing. Users cannot reorder panels, pin a column count, or focus one
important session while keeping the others visible.

Direct hub shortcuts also compete with remote terminal applications. Existing
direct shortcuts like `Ctrl+]`, `Ctrl+F`, and `Ctrl+\` are intercepted by the
hub even when the user wants to send them to the selected remote PTY.

## Goals

- Add tmux/screen-style prefix controls with `Ctrl+A`.
- Make hub commands prefix-only so normal keys are forwarded to the remote PTY.
- Support manual session ordering.
- Support auto grid, fixed column grid, and focus-big layout modes.
- Keep layout state in memory for this version.

## Non-Goals

- No arbitrary tmux-style split tree.
- No mouse resizing or dragging.
- No persisted layout file in this version.
- No per-session custom panel sizes beyond focus-big.

## Keymap

Hub commands are triggered by `Ctrl+A` followed by one command key:

| Sequence | Behavior |
|----------|----------|
| `Ctrl+A n` | Select next session |
| `Ctrl+A p` | Select previous session |
| `Ctrl+A f` | Enter fullscreen for selected session |
| `Ctrl+A \` | Quit hub |
| `Ctrl+A h` / `Ctrl+A Left` | Move selected panel earlier |
| `Ctrl+A l` / `Ctrl+A Right` | Move selected panel later |
| `Ctrl+A 1` | Fixed grid with 1 column |
| `Ctrl+A 2` | Fixed grid with 2 columns |
| `Ctrl+A 3` | Fixed grid with 3 columns |
| `Ctrl+A 4` | Fixed grid with 4 columns |
| `Ctrl+A a` | Auto grid |
| `Ctrl+A b` | Focus-big layout |
| `Ctrl+A g` | Normal grid using current auto/fixed column mode |
| `Ctrl+A Ctrl+A` | Send a real `Ctrl+A` byte to the selected remote PTY |

Legacy direct hub shortcuts are removed:

- `Ctrl+]` is forwarded to the selected remote PTY.
- `Ctrl+F` is forwarded to the selected remote PTY.
- `Ctrl+\` is forwarded to the selected remote PTY.

Quitting the hub is only `Ctrl+A \`.

## Layout Modes

### Auto Grid

Current auto behavior remains available. The hub chooses 1-4 columns to keep
panels close to the existing target aspect ratio.

### Fixed Grid

The user can set fixed columns with `Ctrl+A 1` through `Ctrl+A 4`.

Rules:

- column count is clamped to the number of sessions;
- column count is clamped down if the terminal would make panels narrower than
  the existing minimum usable width;
- `Ctrl+A a` returns to auto grid.

### Focus Big

`Ctrl+A b` makes the selected session the primary panel while keeping other
sessions visible.

Rules:

- With one session, focus-big renders like a single grid panel.
- If terminal width is at least 120 and there is more than one session:
  - primary panel uses about 70% of width on the left;
  - secondary panels are stacked vertically on the right.
- Otherwise:
  - primary panel uses about 65% of height on top;
  - secondary panels are arranged horizontally below.
- `Ctrl+A n` / `Ctrl+A p` changes the selected primary session.
- `Ctrl+A g` exits focus-big and returns to grid mode.

## Data Model

Add layout state to the TUI model:

```go
type layoutMode int

const (
    layoutAutoGrid layoutMode = iota
    layoutFixedGrid
    layoutFocusBig
)

type layoutState struct {
    mode      layoutMode
    fixedCols int
    order     []string
    prefix    bool
}
```

`Model` gains:

```go
layout layoutState
```

`layout.order` stores session IDs in user-defined display order.

## Session Ordering

On connect:

1. Refresh `m.sessions` from `hub.Sessions()`.
2. Preserve existing IDs in `layout.order`.
3. Append newly connected IDs to the end.

On disconnect:

1. Refresh `m.sessions`.
2. Remove missing IDs from `layout.order`.
3. Clamp `selected` to a valid ordered-session index.

Rendering uses:

```go
func (m *Model) orderedSessions() []*ClientSession
```

This returns sessions sorted by `layout.order`.

## Prefix Handling

`Ctrl+A` sets `layout.prefix = true` and is not forwarded.

If `layout.prefix` is true, the next key is interpreted as a hub command:

- recognized command: execute and clear prefix;
- `Ctrl+A`: send byte `0x01` to selected remote PTY and clear prefix;
- unknown command: clear prefix and do not forward it.

When prefix is false, all non-prefix keys are forwarded to the selected session.

## Rendering Structure

The current `gridView()` should be refactored to use ordered sessions. Focus-big
can be implemented as a separate view function:

```go
func (m *Model) focusBigView() string
```

Suggested file split:

| File | Responsibility |
|------|----------------|
| `hub/tui.go` | Model, Update, top-level View, key handling |
| `hub/layout.go` | layout state, session ordering, movement, column selection |
| `hub/layout_view.go` | grid view and focus-big view rendering |

The split is part of this feature because the current `hub/tui.go` already owns
input handling, layout calculation, and rendering. Separating layout logic keeps
the prefix feature testable.

## Status Bar

Normal mode:

```text
Prefix Ctrl+A | n/p switch | f fullscreen | h/l move | 1-4 cols | a auto | b focus-big | g grid | \ quit | layout: auto | clients: 4
```

Prefix pending:

```text
PREFIX Ctrl+A: n/p switch  f fullscreen  h/l move  1-4 cols  a auto  b focus-big  g grid  \ quit  Ctrl+A send
```

## Tests

Add tests for:

- non-prefix `Ctrl+]`, `Ctrl+F`, and `Ctrl+\` are forwarded to the selected PTY;
- `Ctrl+A` enters prefix mode and is not forwarded;
- `Ctrl+A Ctrl+A` forwards a real `Ctrl+A`;
- `Ctrl+A n` / `Ctrl+A p` changes selected session;
- `Ctrl+A h` / `Ctrl+A l` reorders selected session;
- fixed column mode clamps column count correctly;
- `Ctrl+A a` returns to auto grid;
- `Ctrl+A b` enters focus-big mode;
- focus-big renders selected session as the primary panel;
- disconnected sessions are removed from layout order and selected index is clamped.

## Open Future Work

Persisting layout to `~/.config/codetty/layout.json` is useful but should be a
separate feature after session ID stability and disconnected-session behavior are
decided.
