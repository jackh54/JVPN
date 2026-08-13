# JVPN-Server

Go module `github.com/jackh54/jvpn-server` (lives under `server/` in the JVPN monorepo).

Linux VPN head-end for the JVPN iOS client. It accepts **TLS 1.3** on TCP **443**, authenticates a **pre-shared token**, assigns a **10.8.0.0/24** address, and bridges IPv4 between clients and a **TUN** interface using a simple length-prefixed IP frame protocol.

For DPI-heavy networks, it also supports **WebSocket over TLS** transport (`-transport ws`) while keeping the same authenticated JVPN framing inside the tunnel.

## Requirements

- Linux with TUN support
- Root or capabilities: `CAP_NET_ADMIN` (TUN, addressing) and usually `CAP_NET_BIND_SERVICE` (bind :443), or run as root for development
- `ip` from iproute2 (for `-setup-tun`)
- For **production**, a real TLS certificate (e.g. Let’s Encrypt). If you **omit** `-cert`, `-key`, and `-token-file`, the server **creates** `tls.crt`, `tls.key`, and `token` under `-data-dir` (default `jvpn-data/`) on first run—fine for quick tests; use real certs for anything public.

### Docker / Pterodactyl

**jvpn-server needs a real TUN device on Linux.** Many panel containers (including typical Pterodactyl “yolk” images) do **not** mount `/dev/net/tun` and do **not** grant `CAP_NET_ADMIN`, so startup fails with `tun: no such file or directory` even though TLS and the token file were created.

**Practical options:**

1. Run on a normal Linux VPS (SSH, root or capabilities) — recommended.
2. If you control Docker/Wings: expose TUN and add `NET_ADMIN`, for example:

   ```yaml
   cap_add:
     - NET_ADMIN
   devices:
     - /dev/net/tun
   ```

   or `docker run --cap-add=NET_ADMIN --device=/dev/net/tun ...`

   Pterodactyl must be configured to pass these through for that egg; many hosts will not allow it on shared game slots.

## Build

```bash
cd JVPN-Server
go build -o jvpn-server ./cmd/jvpn-server
```

### Cross-compile for a Linux VPS (from your laptop)

Build **amd64** and **arm64** Linux binaries (no C toolchain on the VPS required; `CGO_ENABLED=0`):

```bash
cd JVPN-Server
chmod +x scripts/build-linux-vps.sh
./scripts/build-linux-vps.sh
```

Artifacts:

| File | Use on |
|------|--------|
| `dist/jvpn-server-linux-amd64` | Typical x86_64 VPS (DigitalOcean, Hetzner CX, etc.) |
| `dist/jvpn-server-linux-arm64` | ARM64 VPS (AWS Graviton, OCI Ampere, some Raspberry Pi–class hosts) |

Copy to the server, mark executable, then run (see **Run (example)** below), e.g.:

```bash
scp dist/jvpn-server-linux-amd64 user@your.vps:/tmp/jvpn-server
ssh user@your.vps 'sudo mv /tmp/jvpn-server /usr/local/bin/jvpn-server && sudo chmod +x /usr/local/bin/jvpn-server'
```

From the repo root, **`./scripts/dev.sh`** builds if needed, seeds token **`jvpn-dev-token`** under `.dev/`, auto-generates TLS there, and runs with `-setup-tun`.

### Auto TLS + token (no `-cert` / `-key` / `-token-file`)

First start creates **`jvpn-data/tls.crt`**, **`jvpn-data/tls.key`**, and **`jvpn-data/token`** (32-byte random hex) unless you point `-data-dir` elsewhere. Optional env **`JVPN_TLS_SAN`**: comma-separated extra DNS names or IPs for the self-signed cert (e.g. `vpn.example.com,203.0.113.5`).

```bash
sudo ./jvpn-server -listen :30100 -tun-name jvpn0 -setup-tun -setup-nat
```

`-setup-nat` (Linux) turns on **IPv4 forwarding** and **iptables MASQUERADE + FORWARD** so phones can reach the internet; without NAT, the tunnel connects but DNS and browsing fail. Copy the secret from `jvpn-data/token` into your client. For production, prefer Let’s Encrypt and pass **`-cert`**, **`-key`**, and **`-token-file`** explicitly (all three together).

### Local dev (matches iOS **Debug** build)

The iOS **Debug** build uses host `127.0.0.1`, port **8443**, token **`jvpn-dev-token`**. Use `scripts/dev.sh` or:

```bash
sudo ./jvpn-server -listen :8443 -data-dir /tmp/jvpn-dev -tun-name jvpn0 -setup-tun
```

(seeding `/tmp/jvpn-dev/token` with `jvpn-dev-token` first if you need that exact token.)

From the **Simulator** or a **phone**, `127.0.0.1` is the device itself, not your Mac—point the app at your server’s real IP (e.g. Mac LAN address) and use a cert whose SAN matches that IP or name. TLS must still validate on iOS (trusted or development exception).

## Token file

**Auto mode:** the first run writes `token` under `-data-dir`; copy its contents into the app.

**Manual:** create a single-line shared secret:

```bash
openssl rand -hex 32 | sudo tee /etc/jvpn.token
sudo chmod 600 /etc/jvpn.token
```

## Run (example)

**Let’s Encrypt (explicit paths):**

```bash
sudo ./jvpn-server \
  -listen :443 \
  -transport ws \
  -ws-path /ws \
  -cert /path/to/fullchain.pem \
  -key /path/to/privkey.pem \
  -token-file /etc/jvpn.token \
  -tun-name jvpn0 \
  -setup-tun \
  -setup-nat
```

**Quick test (auto files under `./jvpn-data`):**

```bash
sudo ./jvpn-server -listen :443 -tun-name jvpn0 -setup-tun -setup-nat

# Obfuscated transport over HTTPS-like websocket framing:
sudo ./jvpn-server -listen :443 -transport ws -ws-path /ws -tun-name jvpn0 -setup-tun -setup-nat
```

`-setup-tun` runs:

- `ip addr add 10.8.0.1/24 dev jvpn0`
- `ip link set jvpn0 up`

If you prefer to configure the interface yourself, omit `-setup-tun` and set `10.8.0.1/24` on the TUN device the server prints.

**`-setup-nat` (Linux, optional but needed for internet):** sets `net.ipv4.ip_forward=1`, detects the default WAN with `ip route get 8.8.8.8`, then adds **iptables** rules: MASQUERADE for `10.8.0.0/24` out that interface, plus FORWARD accept between `jvpn0` and WAN (and established return). Omit it if you manage **nftables/iptables yourself** (see below). Idempotent: existing rules are not duplicated.

## Routing and NAT

If you did **not** use `-setup-nat`, configure forwarding and NAT manually. Enable IPv4 forwarding:

```bash
sudo sysctl -w net.ipv4.ip_forward=1
```

Assume WAN is `eth0` and TUN is `jvpn0`. Example **nftables** masquerade:

```bash
sudo nft add table ip jvpn
sudo nft add chain ip jvpn postrouting { type nat hook postrouting priority 100 \; }
sudo nft add rule ip jvpn postrouting oifname "eth0" ip saddr 10.8.0.0/24 masquerade
```

Replace `eth0` with your uplink interface. Equivalent **iptables**:

```bash
sudo iptables -t nat -A POSTROUTING -s 10.8.0.0/24 -o eth0 -j MASQUERADE
```

## Firewall

Allow inbound TCP **443** to the process (and SSH, etc., as needed):

```bash
sudo nft add rule inet filter input tcp dport 443 accept
```

## Protocol (after TLS)

1. Client sends `JVPN` + version `0x01`/`0x02`/`0x03` + token length (uint16 BE) + token (UTF-8), plus optional device metadata / resume token on v2/v3.
2. Server replies with 1 byte status (`0` = OK). On OK, 4-byte assigned client IPv4 + 1-byte prefix length (e.g. `24`); v3 also returns a resume token.
3. Both sides exchange frames as **uint32 length (BE) + payload** (max 65535 bytes):
   - **IPv4 packet** (normal traffic)
   - **Control** (not IPv4): `0xC0 0x01` + UTF-8 JSON telemetry, or `0xC0 0x02` heartbeat (no body)
4. Telemetry JSON keys: `client_id`, `device_name`, `model`, `os`, `battery_pct`, `charging`, `lat`, `lon`, `updated_at`
5. Sessions idle out after ~90s without any framed traffic (IP, telemetry, or heartbeat).

## Security notes

- Use a long random token and restrict who can reach the server.
- This is not a substitute for a full threat model; restrictive networks may still block or interfere with TLS to unknown endpoints.

## Admin dashboard (localhost + auth)

Optional built-in dashboard with live SSE updates, device telemetry (battery/GPS), traffic, DNS history, and disconnect/block controls:

```bash
sudo ./jvpn-server \
  ...existing flags... \
  -admin-listen 127.0.0.1:18080 \
  -admin-user admin \
  -admin-pass 'strong-password'
```

Keep it bound to loopback and access it through SSH port-forward:

```bash
ssh -L 18080:127.0.0.1:18080 user@your.vps
```

Then open `http://127.0.0.1:18080` locally and sign in with basic auth.

- `GET /healthz` — unauthenticated JSON `{"ok":true,"tun_ready":true}` (for Docker/CI healthchecks)
- `GET /api/stream` — SSE `event: metrics` snapshots (Basic Auth)
- `GET /api/metrics` — same snapshot as JSON
- `POST /api/disconnect?session_id=&block_minutes=` — disconnect (`0`) or temporary block

## Docker deploy

See root `README.md` (Docker CI/CD) and `deploy/README.md`. Compose file: `docker-compose.yml` (`network_mode: host`, `NET_ADMIN`, `/dev/net/tun`).
