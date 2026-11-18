# Building the client

The Windows client is built by GitHub Actions (`.github/workflows/windows-client.yml`)
on every push to `main` that touches `client/` or `installer/`, or manually from
the Actions tab ("Windows client" → Run workflow). A build takes 40–70 minutes
the first time and less once the Rust and vcpkg caches are warm.

## One-time setup

In the repo: **Settings → Secrets and variables → Actions → Variables**, add

| Variable | Value |
| --- | --- |
| `RD_SERVER_KEY` | contents of `/opt/rd/deploy/data/rustdesk/id_ed25519.pub` on the server |

The key is public (it lets clients verify the server), so a variable is fine.
Alternatively add `"key": "<value>"` to `override-settings` in `client/rd/config.json`.

## Output

The run's artifact `rd-windows-x64` contains:

```
rd\                  the client (rustdesk.exe and its DLLs)
install.ps1          silent install + enrolment
test-connection.ps1  firewall check
README.txt
```

## What is changed from upstream RustDesk

All changes are in `patches/0001-rd-fork.patch` (applied to `client/`):

| File | Change |
| --- | --- |
| `client/src/rd.rs` | new: built-in config, unattended-access hash in sysinfo |
| `client/rd/config.json` | new: server, WebSocket only, no auto-update from rustdesk.com, silent mode |
| `client/src/common.rs` | hook: load the built-in config; `read_custom_client` split so it can be reused |
| `client/src/hbbs_http/sync.rs` | hook: add the hash to the sysinfo upload |
| `client/src/lib.rs` | `mod rd;` |

Settings applied by `config.json`:

- server `rd.sfohosting.dk`, API `https://rd.sfohosting.dk`, WebSocket only (`allow-websocket`)
- `approve-mode=password`, `verification-method=use-permanent-password`, `allow-hide-cm`: no
  accept prompt and no connection window on the remote screen
- `hide-tray`: no tray icon; `hide-server-settings`: users cannot repoint the client
- `allow-auto-update=N`, `enable-check-update=N`: never replaced by upstream RustDesk builds
- `app-name=RD`: install folder `C:\Program Files\RD`, service and window titles say RD

Not yet rebranded: the executable is still `rustdesk.exe` and icons are RustDesk's.
A full rebrand (exe name, icons, Windows resources) is a later task.

## Updating to a new RustDesk release

```bash
scripts/update-client.sh 1.5.4
```

Re-vendors upstream into `client/`, commits it unmodified, then re-applies
`patches/*.patch`. Resolve conflicts if any, build, test, commit, and refresh
the patch file with `git diff <vendor-commit> -- client > patches/0001-rd-fork.patch`.
