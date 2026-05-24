#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

echo "Building codetty..."
go build -o codetty ./cmd/codetty
echo "Done: codetty"

if [[ "${1:-}" == "install" ]]; then
    echo "Stopping running codetty processes..."
    pkill -f codetty || true
    sleep 0.5
    echo "Installing to /usr/local/bin..."
    rm -f /usr/local/bin/codetty
    cp codetty /usr/local/bin/codetty
    echo "Installed: /usr/local/bin/codetty"
fi
