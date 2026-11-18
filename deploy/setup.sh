#!/usr/bin/env bash
# One-time setup of the RD server on a fresh Ubuntu 24.04 (Hetzner Cloud).
#
#   ssh root@rd.sfohosting.dk
#   git clone https://github.com/zeonos/rd /opt/rd     # private repo: use a deploy key or token
#   cd /opt/rd/deploy && ./setup.sh you@example.com
#
# Safe to re-run: it only creates what is missing.
set -euo pipefail

ACME_EMAIL="${1:-}"
DEPLOY_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DEPLOY_DIR"

if [[ $EUID -ne 0 ]]; then
  echo "Run as root." >&2
  exit 1
fi

echo "==> Packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -q
apt-get upgrade -yq
apt-get install -yq docker.io docker-compose-v2 ufw unattended-upgrades fail2ban curl openssl
systemctl enable --now docker
dpkg-reconfigure -f noninteractive unattended-upgrades

echo "==> Swap (small VM: 2 GB swap file)"
if ! swapon --show | grep -q /swapfile; then
  fallocate -l 2G /swapfile
  chmod 600 /swapfile
  mkswap /swapfile
  swapon /swapfile
  grep -q '^/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
  sysctl -w vm.swappiness=10
  echo 'vm.swappiness=10' > /etc/sysctl.d/99-rd-swap.conf
fi

echo "==> Host firewall (also set a Hetzner Cloud Firewall: 22 from your IP, 80, 443, TCP 21115-21117, UDP 21116)"
ufw default deny incoming
ufw default allow outgoing
ufw allow 22/tcp
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 21115:21117/tcp
ufw allow 21116/udp
ufw --force enable

echo "==> .env"
if [[ ! -f .env ]]; then
  cp .env.example .env
  sed -i "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=$(openssl rand -hex 24)|" .env
  if [[ -n "$ACME_EMAIL" ]]; then
    sed -i "s|^ACME_EMAIL=.*|ACME_EMAIL=${ACME_EMAIL}|" .env
  fi
  echo "Created .env - review it: $DEPLOY_DIR/.env"
fi

mkdir -p data/rustdesk

echo "==> Start Caddy + hbbs + hbbr"
docker compose up -d caddy hbbs hbbr

echo "==> Waiting for the server key"
for _ in $(seq 1 30); do
  [[ -s data/rustdesk/id_ed25519.pub ]] && break
  sleep 1
done

echo
echo "Done. Server public key (goes into the client build as RD_SERVER_KEY):"
cat data/rustdesk/id_ed25519.pub 2>/dev/null || echo "(not created yet - check: docker compose logs hbbs)"
echo
echo "Back up data/rustdesk/id_ed25519 - losing it means rebuilding every client."
echo "When the API server is merged: docker compose --profile api up -d --build"
