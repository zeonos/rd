# Server setup (Hetzner Cloud)

Current server: Hetzner CPX02-class VM (1 vCPU, 1 GB RAM), Ubuntu 24.04,
`rd.sfohosting.dk` → `2.29.149.12` / `2a01:4f9:c01d:1415::1`.

## 1. DNS (Cloudflare)

| Type | Name | Content | Proxy |
| --- | --- | --- | --- |
| A | rd | 2.29.149.12 | **DNS only** |
| AAAA | rd | 2a01:4f9:c01d:1415::1 | **DNS only** |

Keep the grey cloud. If Cloudflare proxies the name, TLS ends at Cloudflare,
WebSocket relay traffic goes through it, and the clients' server-key check
is pointless.

## 2. Hetzner Cloud Firewall

Inbound rules (everything else denied):

| Protocol | Port | Source |
| --- | --- | --- |
| TCP | 22 | your office/home IPs only |
| TCP | 80 | any (Let's Encrypt + redirect) |
| TCP | 443 | any |
| TCP | 21115-21117 | any (RustDesk ID/relay) |
| UDP | 21116 | any (RustDesk registration) |

The open-source RustDesk server only accepts device registration over
UDP 21116, so for now every site needs outbound **TCP 21115-21117 and
UDP 21116** to `rd.sfohosting.dk` in addition to 443. WebSocket-only
operation (443 only) needs our extended hbbs, which is in progress.

## 3. Install

```bash
ssh root@rd.sfohosting.dk
git clone https://github.com/zeonos/rd /opt/rd
cd /opt/rd/deploy
./setup.sh you@example.com
```

`setup.sh` installs Docker, adds a 2 GB swap file, enables ufw and automatic
security updates, writes `.env` with a random database password, and starts
Caddy + `hbbs` + `hbbr`. It prints the server public key at the end.

If the repo is private, clone with a read-only deploy key
(`ssh-keygen -t ed25519`, add the `.pub` under the repo's
Settings → Deploy keys) and `git clone git@github.com:zeonos/rd.git /opt/rd`.

## 4. Check it

```bash
curl -I https://rd.sfohosting.dk/ws/id          # expect 400/426 from hbbs, not 502
docker compose -f /opt/rd/deploy/docker-compose.yml ps
docker compose -f /opt/rd/deploy/docker-compose.yml logs -f hbbs
```

## 5. Server key

`/opt/rd/deploy/data/rustdesk/id_ed25519.pub` is baked into every client
(`RD_SERVER_KEY` GitHub secret / `client/rd/config.json`). Back up
`id_ed25519` and `id_ed25519.pub` somewhere safe: if they are lost, every
installed client must be rebuilt and reinstalled.

## 6. API server (phase 1)

```bash
cd /opt/rd && git pull
cd deploy && docker compose --profile api up -d --build
docker compose logs api | grep admin_password        # first start only: note the password
docker compose exec -it api rd-api totp-setup admin  # scan/enter the key, confirm with a code
```

Logins are refused until TOTP is set up. Optional: put an
[ntfy](https://ntfy.sh) topic URL in `ALERT_NTFY_URL` in `.env` to get a push
on every login, lockout and enrolment.

Useful commands (all `docker compose exec api rd-api ...`):

| Command | What it does |
| --- | --- |
| `token create --group "Office" --max-uses 20 --expires-hours 72` | enrolment token for the installer |
| `token list` / `token delete <id>` | manage tokens |
| `device list` | devices, location, online, unattended ready |
| `device move <id> "Datacenter/Rack A"` | change location |
| `device rename <id> "SQL01"` | set display name |
| `group list` / `group add "Home lab"` | locations |
| `set-password admin` | change your password (signs out all sessions) |

## 7. Web console

Open `https://rd.sfohosting.dk` and log in with the admin account and your
authenticator code. Browser sessions last at most 12 hours.

- **Devices:** live status by location, details, rename, move, remove; select
  devices to run a script or restart them.
- **Jobs:** results per device with output; cancel devices that have not
  picked a job up yet.
- **Scripts:** saved PowerShell / cmd scripts.
- **Settings:** account-wide device settings with per-location overrides;
  devices apply changes within about a minute.
- **Enrolment:** create install tokens and manage locations.

Scripts and power actions need the agent job runner in the client (phase 2,
next client release); until then jobs wait for devices and expire after
their deadline.

## 8. Updating

```bash
cd /opt/rd && git pull && cd deploy
docker compose pull hbbs hbbr caddy
docker compose --profile api up -d --build
```

## 9. Alerts

Set `ALERT_NTFY_URL` in `/opt/rd/deploy/.env` to an [ntfy](https://ntfy.sh) topic
(install the ntfy app on your phone and subscribe to the same topic). You get a
push for: every login, login lockouts, new enrolments, a device offline for
`ALERT_OFFLINE_MINUTES` (default 10) and back online, and failed jobs.

## 10. Backups

```bash
ln -sf /opt/rd/deploy/backup.sh /etc/cron.daily/rd-backup
/opt/rd/deploy/backup.sh
ls -l /var/backups/rd
```

Daily copies of the database and of the server key pair + `.env` go to
`/var/backups/rd` (kept `BACKUP_KEEP_DAYS`, default 14). For an off-site copy,
set `BACKUP_REMOTE` to a Hetzner Storage Box (`ssh-keygen` on the server, add
the public key to the Storage Box, then test with
`ssh -p 23 u123456@u123456.your-storagebox.de ls`).

### Restore onto a new server

```bash
# after setup.sh on the new server, stop the stack and put the keys back
cd /opt/rd/deploy && docker compose --profile api down
tar -xzf rd-keys-YYYYMMDD-HHMM.tar.gz -C /opt/rd/deploy
docker compose --profile api up -d postgres
gunzip -c rd-db-YYYYMMDD-HHMM.sql.gz | docker compose --profile api exec -T postgres psql -U rd -d rd
docker compose --profile api up -d --build
```

Point DNS at the new server; clients reconnect on their own because the key is the same.
