#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

echo "Building codetty..."
go build -o codetty ./cmd/codetty
echo "Done: codetty"

if [[ "${1:-}" == "install" ]]; then
    echo "Installing to /usr/local/bin..."
    rm -f /usr/local/bin/codetty
    cp codetty /usr/local/bin/codetty
    echo "Installed: /usr/local/bin/codetty"
    echo "Hot-upgrading daemon..."
    codetty upgrade 2>/dev/null && echo "Daemon upgraded." || echo "No daemon running (skipped)."
fi
