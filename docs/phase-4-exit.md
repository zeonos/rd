# Phase 4 exit test ("Release decision")

- [ ] **Restore:** follow docs/runbook.md "Restore onto a new server" on a fresh Hetzner VM using last night's backup; time it (target < 1 hour); all devices come online on the new VM; a viewer connects without a password; then switch DNS back and delete the test VM
- [ ] **Offline alert:** unplug the network of a test server → ntfy alert within `ALERT_OFFLINE_MINUTES` (+1 minute); plug back in → "back online"
- [ ] **Disk alert:** on a test machine fill a disk below 10 % free (or set `ALERT_DISK_PERCENT=99` temporarily) → ntfy alert within 6 hours of the next inventory (restart the RD service to send one after 3 minutes)
- [ ] **Failed job alert:** run `exit 3` as a script → ntfy "job failed"
- [ ] **Security:** Actions → Security audit → Run workflow: green; `docs/security-review.md` "action for you" items done (password + TOTP rotated, SSH restricted); external scan matches the expected ports
- [ ] No open high-severity findings
