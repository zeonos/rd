# Phase 0 exit test

Goal: a client built from this repo controls a Server 2022 behind a 443-only firewall.

- [ ] Server running: `docker compose ps` shows caddy, hbbs, hbbr up
- [ ] `https://rd.sfohosting.dk` has a valid Let's Encrypt certificate
- [ ] `RD_SERVER_KEY` repository variable set; "Windows client" workflow green
- [ ] `test-connection.ps1` passes at every site (table below)
- [ ] From a Windows 11 viewer, connect to a Server 2022 at the strictest site:
  - [ ] Ctrl+Alt+Del works (logon screen appears)
  - [ ] switch between monitors
  - [ ] copy text both ways
  - [ ] copy a file into the session with Ctrl+C / Ctrl+V

| Site | DNS | Proxy | TCP 443 | TLS | WebSocket | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| | | | | | | |
