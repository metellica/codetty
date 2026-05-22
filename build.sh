#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

echo "Building codetty..."
go build -o codetty ./cmd/codetty
go build -o codetty-hub ./cmd/codetty-hub
go build -o codetty-cli ./cmd/codetty-cli
echo "Done: codetty, codetty-hub, codetty-cli"

if [[ "${1:-}" == "install" ]]; then
    echo "Installing to /usr/local/bin..."
    cp codetty codetty-hub codetty-cli /usr/local/bin/
    echo "Installed: /usr/local/bin/codetty, /usr/local/bin/codetty-hub, /usr/local/bin/codetty-cli"
fi
