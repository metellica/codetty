#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

echo "Building codetty..."
go build -o codetty ./cmd/codetty
echo "Done: codetty"

if [[ "${1:-}" == "install" ]]; then
    echo "Installing to /usr/local/bin..."
    cp codetty /usr/local/bin/
    echo "Installed: /usr/local/bin/codetty"
fi
