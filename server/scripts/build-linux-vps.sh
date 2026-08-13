#!/usr/bin/env bash
# Cross-compile jvpn-server for Linux VPS from macOS, Windows, or Linux.
# Produces static-ish binaries (CGO_ENABLED=0) in ./dist/ for amd64 and arm64.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

mkdir -p dist

LDFLAGS=${LDFLAGS:--s -w}
export CGO_ENABLED=0

echo "Building linux/amd64 (most x86_64 VPS)..."
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o dist/jvpn-server-linux-amd64 ./cmd/jvpn-server

echo "Building linux/arm64 (Graviton / Ampere / many ARM VPS)..."
GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$LDFLAGS" -o dist/jvpn-server-linux-arm64 ./cmd/jvpn-server

ls -la dist/jvpn-server-linux-*
echo
echo "Upload the file that matches your VPS CPU:"
echo "  x86_64  -> scp dist/jvpn-server-linux-amd64 user@vps:/usr/local/bin/jvpn-server"
echo "  aarch64 -> scp dist/jvpn-server-linux-arm64 user@vps:/usr/local/bin/jvpn-server"
echo "Then on the VPS: chmod +x /usr/local/bin/jvpn-server"
