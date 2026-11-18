# Installing on Windows

Works on Windows Server 2022 and Windows 11. UAC disabled is fine.

1. On the server, create an enrolment token:
   ```bash
   docker compose exec api rd-api token create --group "Office" --max-uses 20 --expires-hours 72
   ```
   Leave out `--group` to let each install choose with `-Group`.
2. Copy the `rd-windows-x64` build to the machine (or a share).
3. Run as Administrator (or as SYSTEM from Gizmo / GPO / Intune):
   ```powershell
   powershell -ExecutionPolicy Bypass -File install.ps1 -Token rde_xxx -Group "Office/Rack 1"
   ```

What `install.ps1` does:

1. `rustdesk.exe --silent-install` — installs to `C:\Program Files\RD` as a service
   that starts at boot, before anyone logs on.
2. `--password <random>` — a random unattended password that is never written
   down. The client reports only `sha256(password + salt)` to the API server.
3. `--assign --token … --device_name … --device_group_name …` — registers the
   device on the server in its location.

Log: `C:\ProgramData\RD\install.log`.

## Connecting

On your own PC (installed the same way, or just run `rustdesk.exe`):
Account → Log in → username, password, then the authenticator code.
The address book lists every device tagged with its location; double-click to
connect. No password prompt: the viewer uses the hash from the address book.

Ctrl+Alt+Del, monitor switching, clipboard and file copy/paste, and
reboot ("Restart remote device" in the session toolbar) are standard RustDesk
features and need no extra setup.

## Re-installing or moving a machine

Run `install.ps1` again with a valid token. A reinstall gets a new machine UUID
and a new unattended password; the server replaces the old ones.
To move a device without reinstalling: `rd-api device move <id> "New location"`.

## Uninstalling

`"C:\Program Files\RD\rustdesk.exe" --uninstall`, then `rd-api device delete <id>`.
