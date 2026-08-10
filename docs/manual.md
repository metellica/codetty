# CodeTTY User Manual

## Table of contents

- [Overview](#overview)
- [Installation](#installation)
- [Basic concepts](#basic-concepts)
- [Command-line usage](#command-line-usage)
- [Session config files](#session-config-files)
- [The TUI](#the-tui)
- [Key reference](#key-reference)
- [Mouse operations](#mouse-operations)
- [Layout modes](#layout-modes)
- [Session management](#session-management)
- [Floating windows](#floating-windows)
- [Scrollback](#scrollback)
- [Text selection and copy](#text-selection-and-copy)
- [AI agent state detection](#ai-agent-state-detection)
- [Scripting sessions](#scripting-sessions)
- [Hot upgrade](#hot-upgrade)
- [Workflow examples](#workflow-examples)
- [Troubleshooting](#troubleshooting)

---

## Overview

CodeTTY is a local terminal multiplexer providing persistent session
management like tmux/screen. It consists of two components:

1. **Daemon (background process)** — manages PTY sessions; sessions keep
   running inside the daemon
2. **TUI (frontend)** — an interactive terminal UI built on Bubble Tea

Highlights:

- Sessions keep running after the TUI closes (detach/attach)
- Reconnect after an SSH drop and pick up right where you left off
- Manage many terminal sessions at once; a session can hold multiple tabs
- AI coding agents inside tabs are detected automatically with
  idle/working/blocked state display
- Managed remote tabs: connect to a remote CodeTTY daemon over OpenSSH;
  network drops don't interrupt remote tasks
- Mouse drag to reorder panels, title-bar buttons
- Text selection with OSC 52 clipboard copy

---

## Installation

### Build from source

```bash
git clone <repo-url> && cd codetty
./build.sh install    # Linux/macOS: build and install to /usr/local/bin
```

Windows:

```powershell
.\build.bat           # produces codetty.exe
```

### Requirements

- Go 1.25+
- Linux/macOS, or Windows 10 1809+

---

## Basic concepts

### The prefix key

CodeTTY uses **Ctrl+A** as the command prefix by default (like tmux's
Ctrl+B). It can be customized via `keybindings.prefix` in
`~/.config/codetty/config.toml` (see the
[README "Configuration" section](../README.md#configuration)). This manual
uses the default `Ctrl+A` throughout; substitute your own prefix if you
changed it.

Usage: press Ctrl+A, release, then press the command key. For example
`Ctrl+A d` = press Ctrl+A, release, press d.

To send a literal Ctrl+A to the program (e.g. beginning-of-line in bash),
press `Ctrl+A Ctrl+A`.

After pressing the prefix, the status bar shows the full list of available
commands.

### Sessions

A session is managed by the daemon; the TUI is just a frontend for viewing
and interacting with it. Sessions can be shown, hidden, renamed, or killed.

A session can hold multiple tabs, each wrapping one PTY process (shell, vim,
htop, ...). When a tab's process exits:

- a non-shell command (e.g. `htop`, `vim`) exits → the default shell
  respawns in place and the panel stays usable
- a shell (e.g. `bash`, `zsh`) exits → the tab closes and focus moves to the
  remaining tabs
- the last tab's shell exits → the whole session ends

### Panels

The TUI shows each visible session as a panel. Panels appear in different
arrangements: big panel, side panels, fullscreen, floating windows.

---

## Command-line usage

### codetty (no arguments)

Opens the TUI and connects to the daemon. Starts the daemon if it isn't
running. Auto-creates a shell session if none exist.

```bash
codetty
```

### codetty new

Creates a session and opens the TUI.

```bash
codetty new                     # default shell session
codetty new -s myproject        # named session
codetty new -- htop             # run a specific command
codetty new -s dev -- vim .     # name + command
```

Unknown arguments are treated as an implicit `new`, so `codetty vim .` is
equivalent to `codetty new -- vim .`.

### codetty attach (a)

Opens the TUI focused on an existing session.

```bash
codetty attach                  # attach (focuses the last active session)
codetty a -s myproject          # attach focused on a specific session
```

### codetty up

Loads a session layout from a config file, incrementally creates missing
sessions, then opens the TUI. See [Session config files](#session-config-files).

```bash
codetty up                      # look for .codetty.toml
codetty up -f dev.toml          # use a specific config file
```

### codetty ls

Lists active sessions. `-a` also includes sessions from all instances and
all owners on this host.

```bash
codetty ls
codetty ls -a
```

### codetty kill

Kills a session.

```bash
codetty kill -s myproject
```

### codetty kill-server

Stops the daemon and all sessions. `--all` also kills sessions owned by
other hosts.

```bash
codetty kill-server
```

### codetty reload

Incrementally syncs running sessions/tabs with the config file (creates
missing items, applies changes) without restarting the daemon.

```bash
codetty reload                  # look for the config file automatically
codetty reload -f dev.toml      # use a specific config file
```

### codetty send-keys / capture / send-mouse

Control sessions from external scripts; see
[Scripting sessions](#scripting-sessions).

### codetty upgrade

Hot-upgrades the daemon binary without interrupting sessions; see
[Hot upgrade](#hot-upgrade).

### codetty version

Shows the version and build time (stamped by `build.sh`/`build.bat`).

```bash
codetty version
```

### Named instances (-i / --instance)

Run multiple isolated daemons on one host (each with its own socket and
session list), like `tmux -L`. `-i` goes before the subcommand; the
`CODETTY_INSTANCE` environment variable works too.

```bash
codetty -i work                     # use the "work" instance
codetty --instance work ls          # list sessions of the work instance
codetty ls -a                       # list sessions across all instances
CODETTY_INSTANCE=work codetty ls    # select the instance via env var
```

---

## Session config files

CodeTTY can define session layouts in a TOML config file: each session may
contain multiple tabs, reuse tab templates, and declare managed remote
targets. Use `codetty up` to load the file and create everything at once.

### Usage

```bash
codetty up                      # find the config file automatically
codetty up -f myconfig.toml     # use a specific file
```

Config file search order:
1. the path given with `-f`
2. `.codetty.toml` in the current directory
3. `~/.config/codetty/sessions.toml`

### Delta behavior

`codetty up` is incremental: sessions that already exist in the daemon are
skipped. You can run `codetty up` repeatedly; only missing sessions are
created.

### Basic example

```toml
layout = "list"               # initial layout: list / focus-big / fullscreen / tabs
focus = "editor"              # session to focus initially

# Native session (runs a command directly)
[[session]]
name = "editor"
command = ["nvim", "."]

[[session]]
name = "server"
command = ["npm", "run", "dev"]
dir = "./backend"             # working dir (relative to the config file)

[[session]]
name = "shell"                # omit command to run the default shell
hidden = true                 # hidden at startup (show via Ctrl+A w)
```

### Managed remote tabs

Managed remote mode runs a CodeTTY daemon on the remote host; SSH only
carries the CodeTTY protocol. When the network drops, remote PTYs, tasks,
and agent state detection keep running; the local TUI reconnects
continuously.

Sessions are always a local concept; `remote` is a tab-level attribute, so
one session can mix local and remote tabs. Remote tabs live in a same-named
session on the remote daemon, namespaced per owner (a hash of local
hostname+uid+instance) — same-named sessions from different machines never
see or clobber each other. The TUI merges them into a single tab bar (agent
state display matches local tabs).

```toml
[remote.gpu]
host = "gpu01"                # ~/.ssh/config Host aliases supported
instance = "default"
deploy = "auto"               # auto-deploy the current codetty binary

[[session]]
name = "dev"

  [[session.tab]]
  name = "shell"              # local tab

  [[session.tab]]
  name = "agent"
  remote = "gpu"              # remote tab
  command = ["copilot"]
  dir = "/workspace/project"  # remote path
```

A session-level `remote` is the default for all of its tabs (the single-tab
shorthand `remote = "gpu"` + `command` still works); an individual tab can
force local execution with `local = true`. When creating a tab with
`Ctrl+A t` in the TUI, a target picker appears: local, each `[remote.*]`
from the config, and Host aliases read from `~/.ssh/config`
(defaulting to `instance = "default"`, `deploy = "auto"`; a `[remote.*]`
definition wins on name conflicts). Remote tabs created ad hoc through the
picker are recorded in `~/.config/codetty/<instance>/remotes.json`, so the
TUI reconnects to those remotes on restart and the tabs are still there; an
entry is removed automatically once all its tabs on the remote are closed.

`deploy = "never"` suits hosts that already have CodeTTY installed, with an
optional `binary` path. `deploy = "auto"` requires matching local/remote
platforms (it deploys the currently running binary); on a platform mismatch
(e.g. Windows client → Linux remote) it falls back to the binary already
deployed on the remote, and errors only if none exists. Without a `remote`
field everything runs locally; `command = ["ssh", "gpu01"]` remains plain
raw SSH. Sessions/tabs pointing at the same `host + instance` share one TUI
bridge connection. Remote tabs are marked `@<remote>` in tab bars and the
list sidebar (e.g. `2:agent@gpu2`) to distinguish them from local tabs.

Version compatibility follows the SSH model: during the subscribe handshake
both sides exchange protocol version ranges and capability lists, then talk
using the highest common version and the capability intersection. If the
ranges don't overlap, a clear error tells you to upgrade the older side.
Day-to-day evolution happens through new JSON fields/capabilities, so the
two binaries don't have to match exactly.

`codetty version` shows the version and build time (stamped by `build.sh`).
Remote hot update: `codetty upgrade -r <name>` deploys the local binary to
the named remote (a `[remote.*]` name, an `~/.ssh/config` alias, or any SSH
host) and hot-upgrades its daemon in place without interrupting sessions;
`-r all` updates every remote known from config and the registry.

### Multi-tab sessions

A session can define multiple tabs inline, each running one command:

```toml
[[session]]
name = "monitoring"

  [[session.tab]]
  name = "logs"
  command = ["tail", "-f", "/var/log/app.log"]

  [[session.tab]]
  name = "htop"
  command = ["htop"]

  # omit command to run the default shell
  [[session.tab]]
  name = "shell"
```

### Templates

Define reusable tab sets shared by multiple sessions:

```toml
[template.dev]

  [[template.dev.tab]]
  name = "copilot"
  command = ["copilot", "--allow-all"]

  [[template.dev.tab]]
  name = "git"
  command = ["lazygit"]

  [[template.dev.tab]]
  name = "code"
  command = ["nvim", "."]

  [[template.dev.tab]]
  name = "shell"

# Use the template
[[session]]
name = "frontend"
template = "dev"
dir = "./frontend"            # default working dir for all tabs
```

### Inheriting and extending templates

A session can inherit a template and customize it — same-name tabs override
template fields, new names are appended:

```toml
[[session]]
name = "fullstack"
template = "dev"

  # same name → overrides template fields (command/dir/env/remote/local)
  [[session.tab]]
  name = "code"
  command = ["nvim", "~/projects"]

  # new name → appended as a new tab
  [[session.tab]]
  name = "db"
  command = ["docker", "compose", "up", "db"]
```

Merge rules:

| Operation | How |
|-----------|-----|
| Add a tab | use a new `name` in `session.tab` → appended to the end |
| Modify a tab | use the same `name` as the template → overrides `command`/`dir`/`env`/`remote`/`local` |

### Field reference

**Top-level fields:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `layout` | string | `"list"` | initial layout mode (list / focus-big / fullscreen / tabs) |
| `focus` | string | first session | session to focus initially |

**remote.\<name\>:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `host` | string | **required** | SSH host (`~/.ssh/config` Host aliases supported) |
| `instance` | string | `"default"` | remote daemon instance name |
| `deploy` | string | `"auto"` | `"auto"` (deploy the local binary) or `"never"` |
| `binary` | string | codetty in remote PATH | remote binary path when `deploy = "never"` |

**template.\<name\>:**

| Field | Type | Description |
|-------|------|-------------|
| `tab[]` | array | tab definitions |

**session[]:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | **required** | session name (unique) |
| `command` | string[] | default shell | command for single-tab sessions (also the default for tabs) |
| `dir` | string | config file dir | working directory (resolved relative to the config file when local) |
| `env` | map | `{}` | extra environment variables |
| `hidden` | bool | `false` | hidden at startup |
| `remote` | string | — | managed remote target name, default for all tabs |
| `template` | string | — | template to inherit |
| `tab[]` | array | — | inline tab definitions or template overrides |

**tab[]:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | **required** | tab name |
| `command` | string[] | session `command`, else default shell | tab command |
| `dir` | string | session `dir` | working directory |
| `env` | map | session `env` | extra environment variables |
| `remote` | string | session `remote` | managed remote target name |
| `local` | bool | `false` | force local execution (overrides session-level `remote`) |

---

## The TUI

After startup the TUI shows the session panels with a status bar at the
bottom listing sessions and key hints.

### No-session state

When no sessions exist, the center of the screen shows the `⬡ codetty` logo
and a hint:

- normally: "No sessions. Press Ctrl+A c to create one."
- while auto-creating: "No sessions. Creating one..."

All overlay operations (creating sessions, etc.) still work in this state;
the status bar appears only while an overlay is active.

### Status bar

The bottom status bar shows:

- the focused session's name and position (e.g. `[bash] 1/3`)
- the current layout mode (list / fullscreen)
- action hints
- the full command list after pressing the `Ctrl+A` prefix

### Title bars

Each panel's title bar shows the session name and number. Side-panel title
bars have action buttons on the right:

- `[-]` — hide the panel (the session keeps running, it's just not shown)
- `[x]` — kill the session (with a confirmation dialog)

Side-panel title bars have a pale green background; the big panel's title
bar is plain. On narrow terminals (width < 120) focus-big mode switches to a
vertical layout with a small panel list at the bottom.

---

## Key reference

All prefix commands are `Ctrl+A`, release, then the command key. After
pressing the prefix, the status bar lists all available commands.

### Session management

| Key | Action |
|-----|--------|
| `Ctrl+A d` | detach — exit the TUI, sessions keep running |
| `Ctrl+A c` | create a new session |
| `Ctrl+A x` | kill the focused session |
| `Ctrl+A X` | close the current tab (when the session has more than one) |
| `Ctrl+A r` | rename the focused session (pre-filled with the current name) |
| `Ctrl+A t` | create a new tab in the current session (pick a local or remote target) |
| `Ctrl+A w` | open the session manager |
| `Ctrl+A m` | create a temporary floating shell |
| `Ctrl+A R` | reload the session config file |

### Navigation

| Key | Action |
|-----|--------|
| `Ctrl+A n` | next session |
| `Ctrl+A p` | previous session |
| `Ctrl+A 1`-`9` | jump to session by number |
| `Shift+Tab` | list mode: step through sidebar entries (agents needing attention first, back to the big panel after the last); focus-big mode: cycle side-panel highlight (Enter floats / Space promotes); floating window: close it and highlight the next panel; fullscreen: open the session switcher (live previews); tab mode: next session |
| `Enter` | open a floating window on the highlighted panel (focus-big side panels) |

### Scrollback

| Key | Action |
|-----|--------|
| `Ctrl+A [` or `Ctrl+A PgUp` | enter scrollback mode |
| `↑`/`↓`, `j`/`k` | scroll one line |
| `PgUp`/`PgDn`, `b`/`f` | scroll one page |
| `u`/`d` | scroll half a page |
| `g` / `Home` | jump to the top of history |
| `G` / `End` | jump to the bottom |
| `q` / `Esc` / `Enter` | return to the live view |

### Layout control

| Key | Action |
|-----|--------|
| `Ctrl+A f` | toggle layout: floating→fullscreen→parent mode; focus-big/list→fullscreen; fullscreen→back |
| `Ctrl+A q` | close the floating window |
| `Ctrl+A s` | switch to list mode (default) |
| `Ctrl+A b` | switch to focus-big mode |
| `Ctrl+A T` | switch to tab mode (session tab bar on the top row) |

### Panel reordering

| Key | Action |
|-----|--------|
| `Ctrl+A h` | move the current session left (display order) |
| `Ctrl+A l` | move the current session right (display order) |

### Direct keys (no prefix)

| Key | Action |
|-----|--------|
| `Shift+Tab` | next session / cycle panel highlight / step through list entries / open the fullscreen session switcher |
| `Ctrl+A Ctrl+A` | send a literal Ctrl+A to the session |

---

## Mouse operations

### Panel clicks

| Action | Effect |
|--------|--------|
| click a side panel's content | open a floating window with that session |
| click the big panel's content | if the session enabled mouse reporting (tmux, vim, copilot, ...), press/drag/release events are forwarded to the PTY in real time; otherwise text selection starts |
| Ctrl+click a side panel | promote that session to the big panel (swap) |

### Title-bar buttons

Side-panel title bars have `[-]` and `[x]` buttons on the right, available
on all non-big panels in focus-big mode.

| Button | Effect |
|--------|--------|
| `[-]` | hide the panel (the session keeps running; restore via the manager) |
| `[x]` | kill the session (confirmation dialog) |

### Drag to reorder

Press and hold on a side panel's title bar, drag to another panel, and
release — the two panels swap positions.

While dragging, the target panel's title bar shows a `⬇` indicator.

**Drag vs click:** dragging starts only after the pointer moves beyond a
threshold; a plain click never triggers a drag.

### Text selection and copy

In big panels (fullscreen / the focus-big main panel / floating windows):

1. hold the left button and drag to select text
2. on release, the selection is copied to the system clipboard (OSC 52)
3. the selection is highlighted (reverse video)

**Note:** side panels don't support text selection; only big
panels/floating windows do.

### Scroll wheel

Wheel events are forwarded to session PTYs with mouse reporting enabled
(tmux, vim, ...). For sessions without mouse reporting, the wheel scrolls
the local scrollback buffer instead (see [Scrollback](#scrollback)).

---

## Layout modes

### List mode (default)

The big panel occupies the left of the screen; the right side is a
fixed-width (28-column) session list sidebar. The list shows each session's
name, and below it one line per agent-carrying tab with its state (a session
can have zero or more agents):

- `■ copilot blocked` (red) — the agent is waiting for user input
- `● copilot working` (amber) — the agent is working (●/○ blinking)
- `✓ copilot done` (green) — the agent finished, not yet reviewed
- `· copilot idle` (gray) — the agent is idle

Interaction:

- `Shift+Tab` steps through sidebar entries: agents needing attention first
  (blocked, then done), then each session row; past the last entry focus
  returns to the big panel (the status bar shows `list*` while the list has
  focus)
- with the list focused: `↑`/`↓` move the cursor between session rows and
  agent rows (wrapping); `Enter` makes the cursor item the big panel, and on
  an agent row also activates that agent's tab; `Esc` or any other key
  returns to the big panel (no input leaks to the PTY)
- clicking a session row switches the big panel; clicking an agent row also
  activates that agent's tab; the wheel scrolls the sidebar
- the current session has a low-contrast background; the list cursor is a
  bright highlight
- `Ctrl+A n/p` and `Ctrl+A 1-9` still switch directly (staying in list mode)
- the big panel's keyboard/mouse behavior matches focus-big mode

### Focus-big mode

One session takes most of the screen (the big panel, identical in behavior
to list mode's big panel); the rest appear as small side panels on the
right. Enter with `Ctrl+A b`.

- the big panel receives keyboard input and mouse events (rendering, tab
  bar, and bottom info line match list mode)
- side panels never forward keyboard/mouse input to their sessions
- clicking a side panel opens a floating window with that session (handy
  for quick tasks without disturbing the main workspace); Ctrl+click
  promotes it to the big panel and activates its priority tab
  (blocked > done > current active tab)
- `Shift+Tab` cycles the side-panel highlight (past the last, focus returns
  to the big panel; status bar shows `big*`); while highlighted, `↑`/`↓`
  move the highlight, `Enter` opens a floating window, `Space` promotes to
  the big panel, any other key returns to the big panel (no input leaks to
  the PTY)
- `Ctrl+A n/p`, `Ctrl+A 1-9` switch the big-panel session directly
- side-panel title bars show an aggregated agent badge: no dot when the
  session has no agents; otherwise `■` (blocked) / blinking `●○` (working) /
  `●` (done) / `○` (idle); title bar and border colors match the list-mode
  sidebar (blocked red / working amber / done green)

### Fullscreen mode

The current session fills the entire terminal.

- all mouse events are forwarded to the PTY (vim, tmux, etc. mouse support)
- text selection and copy work
- `Ctrl+A f` returns to the previous mode (via the mode stack)
- `Shift+Tab` opens the session switcher (below)

### Tab mode

The top row is a tab bar of all sessions; the rest of the screen is the
selected session's content (no borders; the bottom info line matches
fullscreen mode). Enter with `Ctrl+A T`.

- tab colors match the focus-big tab bar: current session highlighted blue,
  agent states colored (blocked red background, working blinking amber,
  done green)
- clicking a tab switches sessions (jumping to the agent tab that needs
  attention)
- `Shift+Tab` switches to the next session
- the content area supports text selection, wheel, and mouse forwarding
  (same as fullscreen)

### Fullscreen session switcher (Shift+Tab)

In fullscreen, `Shift+Tab` pops up a floating switcher listing all visible
sessions except the current one, each rendered like a focus-big side panel
with a live output preview (~10 rows), so you can pick by current state.

- `↓` / `j` — move down (same column; wraps from the last row to the first)
- `↑` / `k` — move up (same column; wraps from the first row to the last)
- `→` / `l` — move right (same row; wraps at the end of the row)
- `←` / `h` — move left (same row; wraps at the start of the row)
- `Tab` / `Shift+Tab` — highlight the next panel in order
- `Enter` — switch to the highlighted session (staying fullscreen)
- `1`-`9` — switch by number
- `Esc` / `q` — close the switcher, stay on the current session
- left click on a panel — switch to that session
- click outside the box — close the switcher
- wheel — cycle the highlight

### Mode-stack navigation

`Ctrl+A f` navigates using a mode stack:

- list/focus-big/tab mode → fullscreen
- floating window → fullscreen (with the floating session)
- fullscreen (normal session) → pop the mode stack, skipping intermediate
  floating states
- fullscreen (temporary shell) → back to the parent floating window
- a temporary shell closing while fullscreen → back to the floating window

---

## Session management

### Creating a session (Ctrl+A c)

`Ctrl+A c` enters name-input mode:

- type the session name, `Enter` to confirm
- `Esc` cancels
- `Backspace` deletes

### Creating a tab (Ctrl+A t)

Creates a tab in the current session. If the config defines `[remote.*]`
targets or `~/.ssh/config` has Host aliases, a target picker appears first
(local / each remote target), then the tab name prompt. New tabs inherit the
current tab's working directory.

### Renaming a session (Ctrl+A r)

- enters rename mode **pre-filled with the current name** for editing
- `Enter` confirms, `Esc` cancels, `Backspace` deletes

### Killing a session (confirmation dialog)

A confirmation dialog appears before killing:

```
┌─ Kill session "bash"? ───────┐
│                               │
│  [y/Enter] confirm  [n/Esc] cancel  │
└───────────────────────────────┘
```

`y`/`Enter` confirms; `n`/`Esc` cancels.

### Session manager (Ctrl+A w)

Opens an overlay listing all sessions (including hidden ones) with status:

```
┌─── ⬡ Session Manager ─────────────────────┐
│                                            │
│  2 visible / 3 total                       │
│  ─────────────────────────────             │
│  ▸ visible  bash                           │
│    visible  vim                            │
│    hidden   htop                           │
│  ─────────────────────────────             │
│                                            │
│  ↑↓ nav  space/enter toggle  x kill        │
│  h hide  s show  esc close                 │
└────────────────────────────────────────────┘
```

Controls:

| Key | Action |
|-----|--------|
| `j` / `↓` | move cursor down |
| `k` / `↑` | move cursor up |
| `Space` / `Enter` | toggle visible/hidden |
| `h` | hide the selected session |
| `s` | show the selected session |
| `x` | kill the selected session (confirmation) |
| `Esc` | close the manager |

### Hiding / showing sessions

After hiding a session:

- it keeps running in the background
- it disappears from the panel layout
- restore it via the session manager (`Ctrl+A w`)
- the side-panel `[-]` button also hides

---

## Floating windows

A floating window shows a single session on top of the current layout at
80% of the terminal size. It looks like the big panel: no top title bar, a
bottom info line with the tab bar on the left (when multi-tab) and
`[n] name  (^A q close)` on the right; clicking a tab in the info line
switches tabs.

### Opening a floating window

- list/focus-big mode: click a side panel (or a sidebar session row) —
  opens that session in a floating window
- `Enter` on a highlighted side panel — opens that session in a floating
  window
- `Ctrl+A m` — creates a new temporary floating shell

### Working with a floating window

- `Ctrl+A q` — close it (temporary shells are killed automatically)
- `Shift+Tab` — close it and highlight the next small panel
- `Ctrl+A f` — go from floating to fullscreen
- keyboard input is forwarded to the floating session normally
- mouse: text selection and click forwarding are supported

### Temporary floating shells

Shells created by `Ctrl+A m` behave specially:

- killed automatically when the floating window closes
- never appear in the main panel layout
- ideal for running a quick command and getting back to work

---

## Scrollback

`Ctrl+A [` or `Ctrl+A PgUp` puts the focused panel into scrollback mode to
review terminal history (tmux copy-mode analog). While scrolled back, the
panel shows a position indicator, and new output doesn't move the view.

- `↑`/`↓` or `j`/`k` — one line
- `PgUp`/`PgDn` or `b`/`f` — one page
- `u`/`d` — half a page
- `g` / `Home` — top of history
- `G` / `End` — bottom
- `q` / `Esc` / `Enter` — return to the live view

The mouse wheel also scrolls back in big panels; scrolling to the bottom
returns to live. While a floating window is open, scrollback applies to the
floating session.

---

## Text selection and copy

In big panels, fullscreen mode, or floating windows, select and copy text
with the mouse:

1. **start** — hold the left button in the panel content area
2. **drag** — the selected text is highlighted in reverse video
3. **copy** — release; the text is sent to the system clipboard via OSC 52
4. **cancel** — click elsewhere to clear the selection

If the session enabled mouse reporting (tmux, vim, copilot, ...), press,
drag, and release events are forwarded to the PTY program in real time
instead of starting a selection, so in-app clicking and dragging work
normally.

### OSC 52 clipboard

CodeTTY sets the system clipboard with the OSC 52 escape sequence. Most
modern terminals support it:

- iTerm2 ✓
- Alacritty ✓
- kitty ✓
- Windows Terminal ✓
- tmux (needs `set -g set-clipboard on`)

If your terminal doesn't support OSC 52, the clipboard just won't update.

---

## AI agent state detection

CodeTTY detects AI coding agents running inside tabs by mapping each PTY's
foreground process to a registered agent, then infers the agent's state from
the visible screen tail:

- **idle** — waiting at its prompt with nothing to do
- **working** — actively processing (blinking indicator)
- **blocked** — needs human input (approval, selection); shown red
- **done** — finished working, not yet reviewed; shown green

The state appears in the list-mode sidebar (one row per agent tab), on
focus-big side-panel badges, and in tab bars. Switching to a session
activates its priority tab automatically (blocked > done > active).

Currently **Claude Code**, **Codex CLI**, and **Copilot CLI** are recognized.
Each agent is detected by its process name and its on-screen prompt/spinner/
approval patterns. The state-probing approach was
inspired by [herdr](https://github.com/herdrdev/herdr).

---

## Scripting sessions

Control sessions from scripts or other tools without attaching:

```bash
# Send keys (tmux-style names: Enter, C-c, M-x, Up, ...; -l = literal)
codetty send-keys -s mysession 'echo hi' Enter
codetty send-keys -s dev -t 1 C-c        # target tab 1

# Capture a tab's screen as plain text
codetty capture -s dev                   # full screen
codetty capture -s dev -n 50             # last 50 rows

# Send a mouse event at cell x,y (1-based)
# events: click1/2/3, down/up/drag1/2/3, move, wheelup, wheeldown
codetty send-mouse -s dev click1 10 5
```

`send-mouse` requires the application to have mouse reporting enabled;
SGR/X10 encoding is chosen automatically.

---

## Hot upgrade

`codetty upgrade` replaces the running daemon binary in place using
same-PID exec: every session, tab process, and client connection is
preserved. Combine with named instances via `-i`.

```bash
codetty upgrade              # upgrade the local daemon to the current binary
codetty upgrade -r gpu       # deploy the local binary to remote "gpu" and upgrade it
codetty upgrade -r all       # upgrade all known remotes
codetty version              # verify version and build time
```

For remote maintenance over SSH, remember the daemon socket lives in
`XDG_RUNTIME_DIR` and named instances use `codetty-<name>.sock`:

```bash
ssh host 'XDG_RUNTIME_DIR=/run/user/$(id -u) codetty -i default upgrade'
```

---

## Workflow examples

### New project development

```bash
# Start CodeTTY; a shell is created automatically
codetty

# In the TUI:
# Ctrl+A c create an editor session (name "vim", command "vim .")
# Ctrl+A c create a second shell for running tests
# Ctrl+A n/p switch between sessions
# Ctrl+A f fullscreen the editor
# Ctrl+A f back to the multi-panel view
```

### SSH remote work

```bash
# On the remote server
ssh user@server
codetty new -s work -- vim project/

# After the SSH connection drops
ssh user@server
codetty attach -s work   # vim is exactly where you left it
```

### Remote agents with managed remote tabs

```bash
# .codetty.toml
# [remote.gpu]
# host = "gpu01"
#
# [[session]]
# name = "train"
#   [[session.tab]]
#   name = "agent"
#   remote = "gpu"
#   command = ["copilot"]

codetty up      # deploys codetty to gpu01 and opens the session
# Close the laptop; the remote agent keeps working.
# Later: codetty attach — the tab reconnects with full state.
```

### Multitasking

```bash
codetty

# After creating several sessions, organize with the session manager:
# Ctrl+A w open the manager
# hide sessions you don't need right now (Space toggles)
# restore them when needed

# Reorder side panels by dragging title bars with the mouse
# or with Ctrl+A h/l
```

---

## Troubleshooting

### The daemon won't start

```bash
# Check for stale socket files
ls "${XDG_RUNTIME_DIR:-/tmp}"/codetty*.sock

# Force-stop an old daemon
codetty kill-server

# Remove stale sockets and start again
rm "${XDG_RUNTIME_DIR:-/tmp}"/codetty*.sock
codetty
```

### Keyboard shortcuts don't respond

- make sure you press `Ctrl+A`, release, then the command key
- check whether an overlay is active (session manager, pickers) — press Esc
  first
- some actions require a specific mode (e.g. `Ctrl+A m` only works outside
  fullscreen)
- the `Ctrl+A` prefix still works inside floating windows

### Rendering issues

- misaligned characters usually mean the terminal or font lacks proper
  wide-character support
- resizing the terminal window forces a full re-render
- make sure the terminal supports 256 colors or true color

### Copy doesn't work

- confirm your terminal supports OSC 52 (see the compatibility list above)
- inside tmux you need `set -g set-clipboard on`
- some terminals require explicitly enabling clipboard access

### Windows notes

- requires Windows 10 version 1809 or later (ConPTY)
- Windows Terminal is recommended (full ANSI, mouse, OSC 52 support)
- cmd.exe and PowerShell ISE may not render correctly
- before Windows 11 22621, ConPTY drops VT mouse sequences, so apps inside
  CodeTTY never receive mouse events
