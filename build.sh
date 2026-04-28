#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

echo "Building codetty..."
go build -o codetty-hub ./cmd/codetty-hub
go build -o codetty-cli ./cmd/codetty-cli
echo "Done: codetty-hub, codetty-cli"
