# RD — self-hosted remote control

A TeamViewer/AnyDesk-style remote control platform for your own Windows
servers and PCs, built on a fork of [RustDesk](https://github.com/rustdesk/rustdesk).

- **Clients:** Windows agent/viewer and Android viewer (forked RustDesk client).
- **Server:** RustDesk `hbbs`/`hbbr` behind Caddy, plus our own API server
  (login with TOTP, devices, location groups, enrolment).
- **Network:** everything runs over WebSocket on TCP 443 — one outbound
  firewall rule per site, nothing inbound.

## Layout

```
client/      RustDesk client, vendored at a pinned release + our patches (AGPL-3.0)
patches/     our changes to client/, as patch files (re-applied on upstream updates)
server/      API server (Go) and SQL migrations
installer/   Windows silent-install script
deploy/      docker-compose, Caddyfile, server setup script
docs/        setup guides, firewall test, phase exit tests
.github/     CI: API tests, Windows client build
```

## Start here

1. [Server setup](docs/server-setup.md)
2. [Firewall test](docs/firewall-test.md)
3. [Building the client](docs/client-build.md)
4. [Installing on Windows](docs/install-windows.md)

## Licence

`client/` is RustDesk, licensed AGPL-3.0; its notices are kept intact.
If the client is ever distributed outside your organisation, its source
(including `patches/`) must be published under AGPL-3.0.
Code outside `client/` and `patches/` is © the repository owner, all rights
reserved, until a licence is chosen.
