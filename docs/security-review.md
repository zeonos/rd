# Security review (phase 4.5)

Review of the RD platform as of November 2026: the API server (`server/`), our
hbbs/hbbr build (`rendezvous/`), the client patch (`client/`, see
`patches/0001-rd-fork.patch`), the installer and the deployment (`deploy/`).

## What an attacker can reach

| Exposed | Port | Who should use it | Protection |
|---|---|---|---|
| Caddy (console, API, WebSocket) | 443/tcp, 80 → 443 | viewers, agents, you | TLS (Let's Encrypt), HSTS, see below per route |
| hbbs (rendezvous) | 21115–21116/tcp, 21116/udp | every client | server key pair (clients refuse another server); TCP key exchange encrypts logged-in viewers' requests |
| hbbr (relay) | 21117/tcp | every client | relays only between two clients that hbbs paired; sessions are end-to-end encrypted by RustDesk |
| SSH | 22/tcp | you | key login, fail2ban |
| PostgreSQL, API port 8080, hbbs/hbbr WebSocket ports | none | containers only | not published |

## Findings

### Fixed in this review

- **Clickjacking / referrer leaks** on the console: Caddy now sends
  `X-Frame-Options: DENY` and `Referrer-Policy: no-referrer` (the console
  already sends a strict `Content-Security-Policy` without inline script).
- **Dependency monitoring**: CI runs `govulncheck` on every API change; the
  weekly *Security audit* workflow runs `govulncheck`, `cargo audit` on
  `rendezvous/` and `client/`, and Trivy on the API and hbbs images.

### Accounts and sessions — OK

- Passwords: Argon2id (64 MiB, 3 passes, 2 lanes), constant-time compare.
- TOTP is required for every login (`RD_REQUIRE_TOTP=1`); a code can be used
  once (last accepted step stored), so a shoulder-surfed code is useless.
- Lockout: 5 failures per IP / 10 per user in 15 minutes lock for 15 minutes;
  every login and lockout sends an ntfy alert.
- Sessions: random 256-bit tokens stored hashed; 7 days idle, 30 days max;
  logout deletes the session.

### Device side — OK, one known limit

- Enrolment needs a token (scoped to a location, expiry and use count).
- Agent requests are signed with the device's RustDesk Ed25519 key (recorded
  on first sysinfo after enrolment, never replaced) and carry a timestamp
  (±5 minutes). Jobs are signed by the server's job key, which is built into
  the client; the agent refuses anything else, jobs for another device, and
  expired jobs. Results and inventories are signed over a hash of their content.
- Update jobs carry the release's SHA-256 inside the signed payload; the
  download itself is public and unauthenticated by design.
- **Known limit (low):** heartbeat and sysinfo are unsigned, as in upstream
  RustDesk. Someone who learns a device's ID *and* its machine UUID (sent only
  over TLS to our server) could overwrite its reported password hash or build
  number. That breaks unattended login to that device (viewers get "wrong
  password") until the device's next real sysinfo, but gives no access: the
  hash is checked by the device itself. Fix if needed: sign sysinfo with the
  device key in the client patch and require it once a key is registered.

### Data the server holds — handle with care

- The address book hands each device's unattended-password hash (h1) to
  logged-in users. RustDesk accepts h1 for login, so **h1 is as good as the
  password**: anyone with an admin session can open any device. This is the
  design (unattended access without anyone knowing passwords); the protection
  is the admin login (password + TOTP). Do not create additional users unless
  they should reach every machine.
- The job key (`server_keys` table) lets whoever holds it run code as SYSTEM
  on every device. It lives only in PostgreSQL and in the backups.
- Backups (`/var/backups/rd`, mode 700, and the Storage Box) contain the job
  key, the RustDesk private key, the database password and all hashes. Keep
  the Storage Box SSH key on the server only.

### Operations — action for you

1. **Rotate the admin password and TOTP secret.** Both were visible in
   screenshots during setup:
   `cd /opt/rd/deploy && docker compose --profile api exec -it api rd-api set-password admin` and
   `cd /opt/rd/deploy && docker compose --profile api exec -it api rd-api totp-setup admin`.
2. **Restrict SSH** in the Hetzner firewall to the IPs you administer from
   (ufw on the VM allows 22 from anywhere; fail2ban limits guessing).
3. **Enrolment tokens:** delete tokens you no longer need; prefer a short
   expiry and a use count.
4. Keep `ALERT_NTFY_URL` set: login, lockout, enrolment, offline, failed-job
   and low-disk alerts are the early warning.

### External scan

Run from a machine outside the server (e.g. your PC with nmap, or an online
scanner such as SSL Labs for the TLS part):

```bash
nmap -Pn -sS -sU -p 22,80,443,21115-21119 rd.sfohosting.dk
```

Expected: 22, 80, 443, 21115–21117/tcp open, 21116/udp open|filtered,
21118–21119 closed. SSL Labs (https://www.ssllabs.com/ssltest/) should grade
the domain A or better.

## Open high-severity findings

None.
