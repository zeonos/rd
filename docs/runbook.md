# RD operations runbook

Everything you do on the RD server, with the full commands. The server is the
Hetzner VM behind `rd.sfohosting.dk`; the repo is checked out at `/opt/rd`.

## Deploy a change to the server

After a pull request touching `server/`, `deploy/` or `rendezvous/` is merged:

```bash
cd /opt/rd && git checkout main && git pull
cd /opt/rd/deploy && docker compose --profile api pull hbbs hbbr
cd /opt/rd/deploy && docker compose --profile api up -d --build
cd /opt/rd/deploy && docker compose --profile api ps
cd /opt/rd/deploy && docker compose --profile api logs --tail=30 api
```

Database migrations run by themselves when the API container starts.

## Roll out a new Windows client

1. Open the green **Windows client** run for `main` in GitHub Actions and
   download the `rd-windows-x64` artifact (a zip containing
   `rd-windows-x64.zip`); unzip it once.
2. Console → **Updates** → upload `rd-windows-x64.zip` → **Roll out**.
3. Online devices update within a few minutes (their RD service restarts, open
   sessions drop for about a minute); offline devices update when they return.
   The Builds table shows how many devices are on each build.
4. To stop: **Stop roll-out**. Devices keep whatever build they have.

Devices installed before auto-update (build "–" in the console) need
`install.ps1` from a new zip once.

## Add a machine

1. Console → **Enrolment** → create a token for its location.
2. On the machine, as Administrator, next to the unzipped files:

```powershell
powershell -ExecutionPolicy Bypass -File install.ps1 -Token <token>
```

   Add `-WebSocket` where only outbound 443 is open
   (`test-connection.ps1` tells you).

## Remove a machine

Console → device → **Remove device**, then on the machine:

```powershell
& "C:\Program Files\RD\RD.exe" --uninstall
```

## Android app

See `docs/android.md` (install, release signing key).

## Alerts

ntfy (`ALERT_NTFY_URL` in `/opt/rd/deploy/.env`) gets: logins and lockouts,
enrolments, devices offline for `ALERT_OFFLINE_MINUTES` (and back), failed
jobs, disks below `ALERT_DISK_PERCENT` free (once a day per disk). After
changing `.env`:

```bash
cd /opt/rd/deploy && docker compose --profile api up -d api
```

## Account

```bash
cd /opt/rd/deploy && docker compose --profile api exec -it api rd-api set-password admin
cd /opt/rd/deploy && docker compose --profile api exec -it api rd-api totp-setup admin
```

## Backups

Nightly via cron (`/etc/cron.daily/rd-backup` → `deploy/backup.sh`): database
dump plus RustDesk keys and `.env` in `/var/backups/rd`, kept 14 days, copied to
the Storage Box when `BACKUP_REMOTE` is set. Run one by hand and list them:

```bash
/opt/rd/deploy/backup.sh
ls -lh /var/backups/rd
```

Not in the backup: uploaded client builds (upload again) and the Android
signing key (keep your own copy, see `docs/android.md`).

## Restore onto a new server

Target: under one hour from a new Hetzner VM to all clients reconnected.

1. Create the VM (Ubuntu 24.04, same size or larger), attach the Hetzner
   firewall (22 from your IP, 80, 443, 21115–21117/tcp, 21116/udp).
2. Copy the newest backup pair to it, from the old server or the Storage Box:

```bash
scp -P 23 'uXXXXX@uXXXXX.your-storagebox.de:rd/rd-*-<stamp>.*' /root/
```

3. On the new VM:

```bash
apt-get update && apt-get install -y git
git clone https://github.com/zeonos/rd.git /opt/rd
/opt/rd/deploy/restore.sh /root/rd-keys-<stamp>.tar.gz /root/rd-db-<stamp>.sql.gz
```

   (A private repo needs a deploy key or token for the clone, as on the first
   server.)
4. Point the `rd.sfohosting.dk` A record at the new VM's IP (Cloudflare: DNS
   only, no proxy). Caddy gets a certificate on the first request.
5. Check: console login works, devices come online within a few minutes,
   a viewer connects without a password prompt.
6. Re-enable backups on the new VM:

```bash
ln -sf /opt/rd/deploy/backup.sh /etc/cron.daily/rd-backup
```

## Troubleshooting

| Symptom | Look at |
|---|---|
| Viewer "connecting to RD network" forever | `docker compose logs --tail=50 hbbs` on the server; `test-connection.ps1` on the viewer PC |
| "Failed to secure tcp" | hbbs image older than the key-exchange fix: pull and restart hbbs |
| "Wrong password" on unattended connect | device has not reported its hash yet: restart its RD service, then Refresh in the viewer |
| Job stays "Waiting" | device offline, or a client older than the job runner; device log `C:\Windows\ServiceProfiles\LocalService\AppData\Roaming\RD\log` or `C:\Windows\System32\config\systemprofile\AppData\Roaming\RD\log` |
| Update never arrives | console → Updates: is a build rolled out? device build "–" means it needs `install.ps1` once; failed update jobs show in Jobs |

Server logs:

```bash
cd /opt/rd/deploy && docker compose --profile api logs --since 30m api hbbs hbbr caddy
```
