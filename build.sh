#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

echo "Building codetty..."
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
# CGO_ENABLED=0 gives a static binary so remote auto-deploy works on hosts
# with older glibc.
CGO_ENABLED=0 go build -trimpath \
    -ldflags "-X main.version=$VERSION -X main.buildTime=$BUILD_TIME" \
    -o codetty ./cmd/codetty
echo "Done: codetty $VERSION (static, built $BUILD_TIME)"

if [[ "${1:-}" == "install" ]]; then
    echo "Installing to /usr/local/bin..."
    rm -f /usr/local/bin/codetty
    cp codetty /usr/local/bin/codetty
    echo "Installed: /usr/local/bin/codetty"
    echo "Hot-upgrading daemon..."
    codetty upgrade 2>/dev/null && echo "Daemon upgraded." || echo "No daemon running (skipped)."
fi
