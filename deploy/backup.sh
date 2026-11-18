#!/usr/bin/env bash
# Nightly backup of everything needed to rebuild the RD server:
#   - PostgreSQL (devices, locations, users, settings, scripts, jobs, job key)
#   - the RustDesk server key pair (losing it means reinstalling every client)
#   - .env (database password, settings)
#
# Install (once):
#   ln -sf /opt/rd/deploy/backup.sh /etc/cron.daily/rd-backup
# Run by hand:
#   /opt/rd/deploy/backup.sh
set -euo pipefail
DEPLOY_DIR="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"
cd "$DEPLOY_DIR"
set -a; [[ -f .env ]] && . ./.env; set +a

DEST=/var/backups/rd
KEEP="${BACKUP_KEEP_DAYS:-14}"
STAMP="$(date +%Y%m%d-%H%M)"
mkdir -p "$DEST"
chmod 700 "$DEST"

docker compose --profile api exec -T postgres pg_dump -U rd -d rd --clean --if-exists | gzip > "$DEST/rd-db-$STAMP.sql.gz.tmp"
mv "$DEST/rd-db-$STAMP.sql.gz.tmp" "$DEST/rd-db-$STAMP.sql.gz"
tar -czf "$DEST/rd-keys-$STAMP.tar.gz" data/rustdesk/id_ed25519 data/rustdesk/id_ed25519.pub .env
chmod 600 "$DEST"/rd-*-"$STAMP".*

find "$DEST" -name 'rd-*' -mtime +"$KEEP" -delete

if [[ -n "${BACKUP_REMOTE:-}" ]]; then
  rsync -a --delete -e "ssh -p ${BACKUP_REMOTE_PORT:-23} -o BatchMode=yes" "$DEST/" "$BACKUP_REMOTE/"
fi
echo "Backup written to $DEST ($STAMP)"
