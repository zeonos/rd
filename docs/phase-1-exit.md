# Phase 1 exit test ("Pilot: 5 servers")

- [ ] API deployed: `docker compose --profile api up -d --build`, `https://rd.sfohosting.dk/health` says `ok`
- [ ] TOTP set up for admin; login from the RD client asks for the code; wrong code refused
- [ ] 5 failed logins lock the login for 15 minutes
- [ ] 5 Server 2022 machines at 2+ sites installed with `install.ps1`, no prompts
- [ ] each appears in `rd-api device list` under its location, `UNATTENDED yes`, online within 1 minute
- [ ] RD viewer home shows them under their location; connecting needs no password
- [ ] no tray icon, no accept prompt and no connection window on the servers
- [ ] reboot a server from the session toolbar; the viewer reconnects to its logon screen within 2 minutes
- [ ] reinstall one server: it keeps its ID and works again after `install.ps1`
