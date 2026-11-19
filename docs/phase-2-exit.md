# Phase 2 exit test ("All machines")

Needs: server with PR #13 deployed, clients from PR #14 or later installed.

- [ ] every machine you manage is enrolled and shows under its location in the console and the viewer
- [ ] console → select a location → **Run script** `hostname; Get-Date` → every device reports its output in Jobs within a minute
- [ ] the same from the viewer: device → **Run script** shows the output in the dialog
- [ ] run a script on an offline device: it shows "Waiting", then runs when the device comes back
- [ ] console → location → **Restart or shut down** → Reboot: all devices go offline and come back online within ~3 minutes
- [ ] Settings (policy): change a value for the account (e.g. image quality) → an online device reports it as effective within 1 minute (device → Effective settings)
- [ ] override the value for one location → devices there show "from <location>"
- [ ] device detail shows public IP, build and an inventory (disks, Windows build, uptime, programs) within ~5 minutes of the client starting
