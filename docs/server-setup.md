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

No UDP and none of the RustDesk ports 21115–21119 are needed: clients use
WebSocket over 443 only.

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
docker compose logs api | grep -i "admin password"   # first start only
```
