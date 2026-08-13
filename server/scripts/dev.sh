#!/usr/bin/env bash
# Run JVPN-Server locally with the same token as the iOS Debug client (jvpn-dev-token).
# TLS cert/key are auto-generated under DEV_DIR on first run (no openssl needed).
#
# Requires: Go. Needs root for TUN + interface setup.
# Linux: `ip`. macOS: `ifconfig`; TUN name utun[0-9]+ (default utun9).

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

DEV_DIR="${DEV_DIR:-$ROOT/.dev}"
LISTEN="${LISTEN:-:8443}"
if [[ "$(uname -s)" == "Darwin" ]]; then
  TUN_NAME="${TUN_NAME:-utun9}"
else
  TUN_NAME="${TUN_NAME:-jvpn0}"
fi
BIN="${BIN:-$ROOT/jvpn-server}"

mkdir -p "$DEV_DIR"
umask 077
printf '%s' 'jvpn-dev-token' > "$DEV_DIR/token"

if [[ ! -x "$BIN" ]]; then
  echo "Building $BIN ..."
  go build -o "$BIN" ./cmd/jvpn-server
fi

NAT_FLAGS=()
if [[ "$(uname -s)" == Linux ]]; then
  NAT_FLAGS=(-setup-nat)
fi
echo "Starting jvpn-server on $LISTEN (TUN=$TUN_NAME, data-dir=$DEV_DIR, token=jvpn-dev-token)..."
exec sudo "$BIN" \
  -listen "$LISTEN" \
  -data-dir "$DEV_DIR" \
  -tun-name "$TUN_NAME" \
  -setup-tun \
  "${NAT_FLAGS[@]}"
