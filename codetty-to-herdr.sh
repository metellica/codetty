#!/usr/bin/env bash
# codetty-to-herdr.sh — replicate a .codetty.toml layout in herdr.
# For each [[session]]: create a herdr workspace (label=name, cwd=dir).
# For each template window: create a tab and run its command in the root pane.
#
# Usage: ./codetty-to-herdr.sh [path/to/.codetty.toml] [--dry-run]
set -euo pipefail

CONF=".codetty.toml"
DRY_RUN=0
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    *) CONF="$arg" ;;
  esac
done

[[ -f "$CONF" ]] || { echo "config not found: $CONF" >&2; exit 1; }

parse_toml() {
  python3 - "$CONF" <<'EOF'
import sys
try:
    import tomllib as toml_mod
except ImportError:
    import tomli as toml_mod
with open(sys.argv[1], "rb") as f:
    cfg = toml_mod.load(f)
templates = cfg.get("template", {})
for s in cfg.get("session", []):
    print("S\t%s\t%s" % (s["name"], s.get("dir", "")))
    tpl = templates.get(s.get("template", ""), {})
    for w in tpl.get("window", []):
        print("W\t%s\t%s" % (w.get("name", "shell"), w.get("command", "")))
EOF
}

run() {
  if (( DRY_RUN )); then
    echo "  \$ herdr $*" >&2
    echo '{}'
  else
    herdr "$@"
  fi
}

ws_id="" ws_dir="" first_tab="" first_pane="" win_idx=0

while IFS=$'\t' read -r kind a b; do
  if [[ "$kind" == "S" ]]; then
    echo ">> workspace: $a  ($b)"
    out=$(run workspace create --label "$a" --cwd "$b" --no-focus)
    ws_id=$(jq -r '.result.workspace.workspace_id // "WS"' <<<"$out")
    first_tab=$(jq -r '.result.tab.tab_id // "TAB"' <<<"$out")
    first_pane=$(jq -r '.result.root_pane.pane_id // "PANE"' <<<"$out")
    ws_dir="$b"
    win_idx=0
  else
    win_idx=$((win_idx + 1))
    if (( win_idx == 1 )); then
      # Reuse the workspace's initial tab for the first window.
      tab_id="$first_tab" pane_id="$first_pane"
      run tab rename "$tab_id" "$a" >/dev/null
    else
      out=$(run tab create --workspace "$ws_id" --cwd "$ws_dir" --label "$a" --no-focus)
      pane_id=$(jq -r '.result.root_pane.pane_id // "PANE"' <<<"$out")
    fi
    if [[ -n "$b" ]]; then
      run pane run "$pane_id" "$b" >/dev/null
      echo "   tab: $a  ->  $b"
    else
      echo "   tab: $a  (plain shell)"
    fi
  fi
done < <(parse_toml)

echo "done."
