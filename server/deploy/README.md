# JVPN server — VPS Docker deploy notes
#
# One-time host setup
# -------------------
# 1. Install Docker Engine + Compose plugin; confirm /dev/net/tun exists.
# 2. Create token dir and write the shared secret:
#      sudo mkdir -p /etc/jvpn
#      openssl rand -hex 32 | sudo tee /etc/jvpn/token
#      sudo chmod 600 /etc/jvpn/token
# 3. Issue Let's Encrypt certs for your VPN hostname (certbot or equivalent).
# 4. Copy compose stack to the VPS, e.g. /opt/jvpn/:
#      scp server/docker-compose.yml server/deploy/.env.example user@vps:/opt/jvpn/
#      ssh user@vps 'cd /opt/jvpn && cp .env.example .env && $EDITOR .env'
# 5. UFW (or equivalent): allow 443/tcp (and SSH). Admin stays on 127.0.0.1:18080.
# 6. Put the deploy user in the `docker` group so CI can run compose without sudo.
# 7. First pull/start:
#      cd /opt/jvpn && docker compose pull && docker compose up -d
#      docker compose ps   # healthcheck should become healthy
#
# Local Linux smoke test (build image locally)
# --------------------------------------------
#   cd server
#   cp deploy/.env.example .env   # set JVPN_ADMIN_PASS; adjust cert/token paths for auto mode if needed
#   # For a quick local auto-TLS run, override command in a compose override or run:
#   sudo docker compose build
#   # With host networking + TUN, prefer a real VPS; macOS Docker Desktop cannot provide TUN.
#
# Admin access
# ------------
#   ssh -L 18080:127.0.0.1:18080 user@vps
#   open http://127.0.0.1:18080  (Basic Auth from .env)
#   Health: curl -s http://127.0.0.1:18080/healthz
#
# GitHub Actions secrets (see .github/workflows/deploy-server.yml)
# ---------------------------------------------------------------
#   VPS_HOST      — hostname or IP
#   VPS_USER      — SSH user in the docker group
#   VPS_SSH_KEY   — private key (PEM)
#   VPS_PORT      — optional, default 22
#   VPS_COMPOSE_DIR — optional, default /opt/jvpn
#
# Optional: keep deploy/jvpn-server.service only if you still run a bare binary;
# Docker + compose on boot (systemd enable docker) is the primary path.
