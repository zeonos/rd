#!/usr/bin/env bash
# Restore the RD server from the files deploy/backup.sh writes:
#   rd-keys-<stamp>.tar.gz  RustDesk key pair + .env
#   rd-db-<stamp>.sql.gz    PostgreSQL dump
#
# On a fresh VM (see docs/runbook.md, "Restore onto a new server"):
#   1. clone the repo to /opt/rd
#   2. copy both backup files to the VM
#   3. /opt/rd/deploy/restore.sh /root/rd-keys-<stamp>.tar.gz /root/rd-db-<stamp>.sql.gz
#
# The keys are restored before anything starts, so hbbs comes up with the old
# key pair and every installed client trusts the new server without changes.
# Uploaded client builds (Updates tab) are not in the backup: upload again.
set -euo pipefail
if [[ $EUID -ne 0 ]]; then
  echo "Run as root." >&2
  exit 1
fi
if [[ $# -ne 2 ]]; then
  echo "Usage: $0 <rd-keys-*.tar.gz> <rd-db-*.sql.gz>" >&2
  exit 1
fi
KEYS="$(readlink -f "$1")"
DB="$(readlink -f "$2")"
[[ -f "$KEYS" && -f "$DB" ]] || { echo "Backup file not found." >&2; exit 1; }
DEPLOY_DIR="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
cd "$DEPLOY_DIR"

if [[ -s data/rustdesk/id_ed25519 ]] && ! tar -xOzf "$KEYS" data/rustdesk/id_ed25519.pub | cmp -s - data/rustdesk/id_ed25519.pub; then
  echo "This server already has a different RustDesk key pair in $DEPLOY_DIR/data/rustdesk." >&2
  echo "Restoring would replace it. Move it away first if that is what you want." >&2
  exit 1
fi

echo "==> Keys and .env"
tar -xzf "$KEYS"
chmod 600 .env data/rustdesk/id_ed25519
echo "Server public key: $(cat data/rustdesk/id_ed25519.pub)"

echo "==> Base setup (Docker, firewall, caddy, hbbs, hbbr)"
./setup.sh

echo "==> Database"
docker compose --profile api up -d postgres
for _ in $(seq 1 60); do
  docker compose --profile api exec -T postgres pg_isready -U rd -d rd >/dev/null 2>&1 && break
  sleep 2
done
gunzip -c "$DB" | docker compose --profile api exec -T postgres psql -q -v ON_ERROR_STOP=1 -U rd -d rd >/dev/null
echo "Restored $(docker compose --profile api exec -T postgres psql -tA -U rd -d rd -c 'SELECT count(*) FROM devices') devices."

echo "==> API server"
docker compose --profile api up -d --build api
for _ in $(seq 1 60); do
  curl -fsS "https://${DOMAIN:-$(grep -oP '^DOMAIN=\K.*' .env)}/health" >/dev/null 2>&1 && break
  sleep 5
done
docker compose --profile api ps
echo
echo "Done. Point DNS for the domain at this server if it changed; clients reconnect"
echo "within a few minutes. Log in to the console and check the devices come online."
echo "Re-install the nightly backup: ln -sf $DEPLOY_DIR/backup.sh /etc/cron.daily/rd-backup"
