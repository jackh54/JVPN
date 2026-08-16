#!/usr/bin/env bash
set -euo pipefail

# JVPN smoke test (no iOS app required).
# Validates TLS/WebSocket connect + JVPN handshake + assigned client IP response.
#
# Examples:
#   scripts/test.sh --host 15.204.210.101 --port 8443 --token-file /etc/jvpn/token
#   scripts/test.sh --host 15.204.210.101 --port 8443 --token "$(cat /etc/jvpn/token)" --transport ws --ws-path /ws
#   scripts/test.sh --host vpn.example.com --port 443 --token-file /etc/jvpn/token --transport uot --secure

HOST=""
PORT="8443"
TOKEN=""
TOKEN_FILE=""
TRANSPORT="ws"
WS_PATH="/ws"
INSECURE="1"
TIMEOUT_SECS="8"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host) HOST="${2:-}"; shift 2 ;;
    --port) PORT="${2:-}"; shift 2 ;;
    --token) TOKEN="${2:-}"; shift 2 ;;
    --token-file) TOKEN_FILE="${2:-}"; shift 2 ;;
    --transport) TRANSPORT="${2:-}"; shift 2 ;;
    --ws-path) WS_PATH="${2:-}"; shift 2 ;;
    --insecure) INSECURE="1"; shift ;;
    --secure) INSECURE="0"; shift ;;
    --timeout) TIMEOUT_SECS="${2:-}"; shift 2 ;;
    -h|--help)
      sed -n '1,24p' "$0"
      exit 0
      ;;
    *)
      echo "Unknown arg: $1" >&2
      exit 2
      ;;
  esac
done

if [[ -z "$HOST" ]]; then
  echo "Missing --host" >&2
  exit 2
fi
if [[ -z "$TOKEN" && -n "$TOKEN_FILE" ]]; then
  TOKEN="$(<"$TOKEN_FILE")"
fi
if [[ -z "$TOKEN" ]]; then
  echo "Missing token: pass --token or --token-file" >&2
  exit 2
fi

python3 - "$HOST" "$PORT" "$TOKEN" "$TRANSPORT" "$WS_PATH" "$INSECURE" "$TIMEOUT_SECS" <<'PY'
import base64
import os
import socket
import ssl
import struct
import sys
from urllib.parse import quote

host, port_s, token, transport, ws_path, insecure_s, timeout_s = sys.argv[1:]
port = int(port_s)
insecure = insecure_s == "1"
timeout = float(timeout_s)

def recv_exact(sock, n):
    out = b""
    while len(out) < n:
        chunk = sock.recv(n - len(out))
        if not chunk:
            raise RuntimeError("connection closed")
        out += chunk
    return out

def ws_send_binary(sock, payload: bytes):
    # client frames must be masked
    fin_opcode = 0x82
    mask_key = os.urandom(4)
    n = len(payload)
    if n < 126:
        header = bytes([fin_opcode, 0x80 | n])
    elif n < 65536:
        header = bytes([fin_opcode, 0x80 | 126]) + struct.pack("!H", n)
    else:
        header = bytes([fin_opcode, 0x80 | 127]) + struct.pack("!Q", n)
    masked = bytes(payload[i] ^ mask_key[i % 4] for i in range(n))
    sock.sendall(header + mask_key + masked)

def ws_recv_message(sock) -> bytes:
    # reads one complete (non-fragmented) message; handles ping/pong/close
    while True:
        b1, b2 = recv_exact(sock, 2)
        fin = (b1 & 0x80) != 0
        opcode = b1 & 0x0F
        masked = (b2 & 0x80) != 0
        ln = b2 & 0x7F
        if ln == 126:
            ln = struct.unpack("!H", recv_exact(sock, 2))[0]
        elif ln == 127:
            ln = struct.unpack("!Q", recv_exact(sock, 8))[0]
        mask = recv_exact(sock, 4) if masked else b""
        payload = recv_exact(sock, ln) if ln else b""
        if masked:
            payload = bytes(payload[i] ^ mask[i % 4] for i in range(len(payload)))
        if opcode == 0x9:  # ping
            # pong
            sock.sendall(bytes([0x8A, len(payload)]) + payload)
            continue
        if opcode == 0x8:
            raise RuntimeError("websocket closed by server")
        if opcode != 0x2:
            continue
        if not fin:
            raise RuntimeError("fragmented websocket message not supported in test client")
        return payload

def open_tls():
    raw = socket.create_connection((host, port), timeout=timeout)
    if insecure:
        ctx = ssl.create_default_context()
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE
    else:
        ctx = ssl.create_default_context()
    return ctx.wrap_socket(raw, server_hostname=host)

def open_ws_tls():
    s = open_tls()
    path = ws_path if ws_path.startswith("/") else "/" + ws_path
    path = quote(path, safe="/?=&")
    key = base64.b64encode(os.urandom(16)).decode()
    req = (
        f"GET {path} HTTP/1.1\r\n"
        f"Host: {host}:{port}\r\n"
        "Upgrade: websocket\r\n"
        "Connection: Upgrade\r\n"
        f"Sec-WebSocket-Key: {key}\r\n"
        "Sec-WebSocket-Version: 13\r\n"
        "\r\n"
    ).encode()
    s.sendall(req)
    resp = b""
    while b"\r\n\r\n" not in resp:
        chunk = s.recv(4096)
        if not chunk:
            raise RuntimeError("websocket upgrade failed: no response")
        resp += chunk
        if len(resp) > 16384:
            raise RuntimeError("websocket upgrade failed: headers too large")
    head, _ = resp.split(b"\r\n\r\n", 1)
    line0 = head.split(b"\r\n", 1)[0].decode(errors="replace")
    if " 101 " not in line0:
        raise RuntimeError(f"websocket upgrade failed: {line0}")
    return s

tok = token.strip().encode("utf-8")
if not tok:
    raise SystemExit("empty token")
if len(tok) > 4096:
    raise SystemExit("token too long")

hello = b"JVPN" + bytes([1]) + struct.pack("!H", len(tok)) + tok

def uot_send(sock, payload: bytes):
    if not payload or len(payload) > 65535:
        raise RuntimeError("invalid uot payload length")
    sock.sendall(struct.pack("!H", len(payload)) + payload)

def uot_recv(sock) -> bytes:
    n = struct.unpack("!H", recv_exact(sock, 2))[0]
    if n == 0:
        raise RuntimeError("invalid uot record length")
    return recv_exact(sock, n)

def open_uot_tls():
    s = open_tls()
    req = (
        "POST /dns-query HTTP/1.1\r\n"
        f"Host: {host}:{port}\r\n"
        "Content-Type: application/dns-message\r\n"
        "Accept: application/dns-message\r\n"
        "Content-Length: 0\r\n"
        "Connection: keep-alive\r\n"
        "\r\n"
    ).encode()
    s.sendall(req)
    resp = b""
    while b"\r\n\r\n" not in resp:
        chunk = s.recv(4096)
        if not chunk:
            raise RuntimeError("uot upgrade failed: no response")
        resp += chunk
        if len(resp) > 16384:
            raise RuntimeError("uot upgrade failed: headers too large")
    head, rest = resp.split(b"\r\n\r\n", 1)
    line0 = head.split(b"\r\n", 1)[0].decode(errors="replace")
    if " 200 " not in line0:
        raise RuntimeError(f"uot upgrade failed: {line0}")
    return s, rest

transport_l = transport.lower()
if transport_l == "ws":
    sock = open_ws_tls()
    ws_send_binary(sock, hello)
    first = ws_recv_message(sock)
elif transport_l == "uot":
    sock, leftover = open_uot_tls()
    if leftover:
        raise RuntimeError("uot upgrade returned unexpected body bytes")
    uot_send(sock, hello)
else:
    sock = open_tls()
    sock.sendall(hello)

if transport_l == "ws":
    buf = bytearray(first)
    def ws_read_exact(n):
        while len(buf) < n:
            buf.extend(ws_recv_message(sock))
        out = bytes(buf[:n])
        del buf[:n]
        return out
    read_exact = ws_read_exact
elif transport_l == "uot":
    buf = bytearray()
    def uot_read_exact(n):
        while len(buf) < n:
            buf.extend(uot_recv(sock))
        out = bytes(buf[:n])
        del buf[:n]
        return out
    read_exact = uot_read_exact
else:
    read_exact = lambda n: recv_exact(sock, n)

status = read_exact(1)[0]
if status != 0:
    print(f"FAIL: handshake denied (status={status})")
    sys.exit(1)
body = read_exact(5)
ip = ".".join(str(x) for x in body[:4])
prefix = body[4]
print(f"OK: authenticated, assigned {ip}/{prefix}, transport={transport.lower()}, {host}:{port}")
sock.close()
PY
