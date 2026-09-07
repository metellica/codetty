# CodeTTY Demo Showcase & Screenshot Guide

This directory contains a pre-packaged, privacy-safe demo environment designed for capturing UI/UX screenshots for the README.

- **Zero private info**: Hostnames, usernames, paths, and commands are fully de-identified and static.
- **Isolated execution**: Uses named instance `-i demo`, completely separated from your daily CodeTTY sessions.
- **Agent state simulation**: Accurately triggers CodeTTY's AI Agent detection (Blocked / Working / Done / Idle states).

---

## 1. Quick Start

Run the demo in an isolated daemon instance:

```bash
codetty -i demo up -f demo/demo.toml
```

When you are done taking screenshots, clean up with:

```bash
codetty -i demo kill-server
```

---

## 2. Screenshot Checklist & How to Capture

### Shot 1: Focus-Big Multi-Pane Layout (Default)
- **What it shows**: Center-stage big panel with stacked live mini-previews on the right.
- **How to view**: Opens automatically on `codetty -i demo up -f demo/demo.toml`.

### Shot 2: AI Agent Collaboration Stream (List Layout)
- **What it shows**: Left main terminal showing Copilot CLI reviewing code in Blocked state; right sidebar highlighting all sessions and agent states (`■ blocked`, `● working`).
- **How to switch**: Press `Ctrl+A` then `s` (or set `layout = "list"` in `demo.toml`).

### Shot 3: Agent-Aware Tab Bar (Tabs Layout)
- **What it shows**: Top tab bar showing active and background agent states across tabs (`1:copilot ■`, `2:tests`).
- **How to switch**: Press `Ctrl+A` then `T` (or set `layout = "tabs"` in `demo.toml`).

### Shot 4: Interactive Session Switcher / Preview Modal
- **What it shows**: Fullscreen preview switcher dialog with live thumbnails.
- **How to trigger**:
  1. Toggle fullscreen: `Ctrl+A` then `f`
  2. Press `Shift+Tab` to open the live session switcher modal.

---

## 3. Directory Layout

- `demo.toml`: Session definition with 4 realistic sessions and multi-tab workflows.
- `bin/copilot`: Mock GitHub Copilot CLI in blocked approval state (`enter to confirm · esc to cancel`).
- `bin/claude`: Mock Claude Code in active thinking state with Braille spinner and OSC title.
- `mock_test.sh`: Mock clean unit test suite in watch mode.
- `mock_server.sh`: Mock API Gateway microservice with clean colorized HTTP access logs.
- `mock_top.sh`: Mock system monitor showing simulated process metrics without reading host data.
