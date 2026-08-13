# JVPN

Production rebuild monorepo for the JVPN packet-tunnel client and Go head-end server.

## Layout

```text
JVPN/
  app/                 # Xcode: JVPN app + JVPNPacketTunnel
  server/              # Go module github.com/jackh54/jvpn-server
    Dockerfile
    docker-compose.yml
    deploy/            # example env + VPS notes
  .github/workflows/   # GHCR build + VPS compose deploy
  README.md
```

| Path | Role |
|------|------|
| `app/JVPN/` | Main iOS/macOS app (SwiftUI, `VPNManager`) |
| `app/JVPNPacketTunnel/` | Network Extension packet tunnel provider |
| `app/Configs/` | Extension Info.plist + shared `JVPN.xcconfig` |
| `server/cmd/jvpn-server/` | Server entrypoint |
| `server/internal/` | Protocol, hub, dashboard, session pool |

## Identifiers

- App bundle: `org.jackh54.JVPN`
- Packet Tunnel: `org.jackh54.JVPN.JVPNPacketTunnel`
- App Group: `group.org.jackh54.JVPN`
- Default host: `vpn.blakout.dev`

Open `app/JVPN.xcodeproj` in Xcode. Shared schemes live under `app/JVPN.xcodeproj/xcshareddata/xcschemes/`.

## Client secrets (do not commit)

Committed config uses placeholder token `REPLACE_WITH_YOUR_SERVER_TOKEN`.

1. Copy `app/Configs/Secrets.local.xcconfig.example` → `app/Configs/Secrets.local.xcconfig`
2. Set `JVPN_SHARED_TOKEN` to the same value as the server token file
3. Optionally override `JVPN_SERVER_HOST` / `JVPN_SERVER_PORT`
4. Build the **JVPN** scheme — values are injected into `Info.plist` and read by `JVPNServiceConfig`

`Secrets.local.xcconfig` is gitignored. An alternate stub is documented in `app/JVPN/JVPNServiceConfig.Local.swift.example`.

## Server (local binary)

```bash
cd server
go test ./...
go build -o jvpn-server ./cmd/jvpn-server
```

See `server/README.md` for TUN/NAT, TLS, admin dashboard, and telemetry framing.

Admin (loopback + Basic Auth): `-admin-listen 127.0.0.1:18080`. Unauthenticated `GET /healthz` returns `{"ok":true}` when the TUN is ready. Live UI uses SSE at `/api/stream`.

## Docker CI/CD setup

Primary deploy path is **GHCR image + `docker compose pull/up`** on the VPS (needs `/dev/net/tun` + `NET_ADMIN`).

### One-time VPS

1. Install Docker Engine + Compose plugin; confirm `/dev/net/tun` exists.
2. Place the compose stack (e.g. `/opt/jvpn/docker-compose.yml`), create `/etc/jvpn/token`, mount Let’s Encrypt certs, allow TCP 443, copy `server/deploy/.env.example` → `.env` (image name, admin user/pass, cert paths).
3. Put the deploy SSH user in the `docker` group (no interactive sudo for CI).
4. Optional first start: `cd /opt/jvpn && docker compose pull && docker compose up -d`.

Details: `server/deploy/README.md`.

### GitHub

1. Enable GHCR for the repo; Actions needs `packages: write` (workflow sets this for `GITHUB_TOKEN`).
2. Repository secrets:
   - `VPS_HOST`, `VPS_USER`, `VPS_SSH_KEY` (required)
   - `VPS_PORT` (optional; appleboy defaults to 22)
   - `VPS_COMPOSE_DIR` (optional; default `/opt/jvpn`)
   - `GHCR_PULL_TOKEN` (optional PAT/classic token with `read:packages` if the image is private — store login on the VPS or set this secret)
3. Push to `main` under `server/**` or run **Deploy JVPN server** → `workflow_dispatch`.
4. Workflow builds/pushes `ghcr.io/jackh54/jvpn-server:sha-…` and `:latest`, SSHes to the VPS, `compose pull/up`, then checks `http://127.0.0.1:18080/healthz`.

### Local compose (Linux + TUN)

```bash
cd server
cp deploy/.env.example .env   # set JVPN_ADMIN_PASS and cert/token paths
sudo docker compose up -d --build
curl -s http://127.0.0.1:18080/healthz
```

macOS Docker Desktop cannot expose a real TUN device; use a Linux VPS or Linux host.

### Admin access

```bash
ssh -L 18080:127.0.0.1:18080 user@vps
# open http://127.0.0.1:18080  (Basic Auth)
```

Paste the production token into local Release xcconfig (never commit). Apple Developer: Network Extension + App Groups + Location for the client.
