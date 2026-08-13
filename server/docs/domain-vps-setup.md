# JVPN Domain + Dedicated VPS Setup

This guide sets up `jvpn-server` on its own VPS using a real domain and Let's Encrypt cert, with WebSocket-over-TLS transport (`wss`) for better DPI resistance than raw IP/self-signed setups. It also enables the built-in admin dashboard (localhost + basic auth via SSH tunnel).

## 1) Prerequisites

- Ubuntu/Debian VPS with root access
- Domain you control (example: `vpn.example.com`)
- Ports open to VPS:
  - `443/tcp` (VPN traffic)
  - `80/tcp` (Let's Encrypt HTTP challenge)
  - `22/tcp` (SSH)

## 2) DNS

Create an `A` record:

- Host: `vpn`
- Value: `<YOUR_VPS_PUBLIC_IP>`
- Resulting FQDN: `vpn.example.com`

Wait for DNS propagation:

```bash
dig +short vpn.example.com
```

It must resolve to your VPS IP before cert issuance.

## 3) Install dependencies on VPS

```bash
sudo apt update
sudo apt install -y certbot iproute2 iptables
```

## 4) Install jvpn-server binary

Copy your built binary to the VPS and install:

```bash
sudo install -m 0755 /root/jv/jvpn-server-linux-amd64 /usr/local/bin/jvpn-server
sudo mkdir -p /etc/jvpn
sudo chmod 700 /etc/jvpn
```

## 5) Issue Let's Encrypt certificate

Stop anything bound to `:80` first, then run:

```bash
sudo certbot certonly --standalone -d vpn.example.com
```

Cert/key paths:

- Cert: `/etc/letsencrypt/live/vpn.example.com/fullchain.pem`
- Key: `/etc/letsencrypt/live/vpn.example.com/privkey.pem`

## 6) Create VPN token

```bash
openssl rand -hex 32 | sudo tee /etc/jvpn/token >/dev/null
sudo chmod 600 /etc/jvpn/token
```

## 7) Create systemd service (VPN + dashboard)

Create `/etc/systemd/system/jvpn-server.service`:

```ini
[Unit]
Description=JVPN server (TLS + WebSocket + TUN + Admin Dashboard)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/jvpn-server \
  -listen :443 \
  -transport ws \
  -ws-path /ws \
  -cert /etc/letsencrypt/live/vpn.example.com/fullchain.pem \
  -key /etc/letsencrypt/live/vpn.example.com/privkey.pem \
  -token-file /etc/jvpn/token \
  -tun-name jvpn0 \
  -setup-tun \
  -setup-nat \
  -admin-listen 127.0.0.1:18080 \
  -admin-user ADMIN_USER \
  -admin-pass CHANGE_ME_STRONG_PASSWORD
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now jvpn-server
sudo systemctl status jvpn-server
```

Live logs:

```bash
sudo journalctl -u jvpn-server -f
```

If you edit the unit later:

```bash
sudo systemctl daemon-reload
sudo systemctl restart jvpn-server
```

## 8) Access admin dashboard securely (SSH tunnel)

Do **not** expose dashboard directly on public IP. Keep `-admin-listen 127.0.0.1:18080` and tunnel from your laptop:

```bash
ssh -N -L 28080:127.0.0.1:18080 root@vpn.example.com
```

Open locally:

- `http://127.0.0.1:28080`

Sign in with the `-admin-user` / `-admin-pass` values from your systemd unit.

## 9) Firewall

Using UFW:

```bash
sudo ufw allow 22/tcp
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
sudo ufw status
```

## 10) Cert auto-renew

Check renewal timer:

```bash
systemctl list-timers | rg certbot
```

Test renewal dry run:

```bash
sudo certbot renew --dry-run
```

## 11) Server validation (no iOS app required)

From your local machine:

```bash
./scripts/test.sh \
  --host vpn.example.com \
  --port 443 \
  --transport ws \
  --ws-path /ws \
  --token-file /path/to/token-copy
```

Expected:

```text
OK: authenticated, assigned 10.8.0.x/24, transport=ws, vpn.example.com:443
```

TLS sanity check:

```bash
echo | openssl s_client -connect vpn.example.com:443 -servername vpn.example.com -tls1_3
```

## 12) iOS app configuration

In `JVPN/JVPN/JVPNServiceConfig.swift` set:

- `serverHost = "vpn.example.com"`
- `serverPort = 443`
- `transport = "ws"` for single-listener server setup on filtered Wi-Fi
- `webSocketPath = "/ws"`
- `sharedToken = "<contents of /etc/jvpn/token>"`
- `acceptSelfSignedTLS = false` (important with Let's Encrypt)

Rebuild app and reconnect.

## 13) Common failure checks

- `Connection reset during TLS`: usually network filtering/DPI or wrong SNI/hostname; verify app uses domain, not IP.
- `Authentication denied`: token mismatch; compare app token with `/etc/jvpn/token`.
- Connects but no internet: ensure service uses `-setup-nat` (or equivalent manual forwarding+NAT).
- Cert errors: verify `vpn.example.com` DNS points to the same VPS running this service.
- Dashboard reconnect/kick behavior: current admin disconnect blocks that client briefly; this is expected to prevent instant reconnect loops.

