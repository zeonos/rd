# Release preparation (phase 4.6)

RD is internal today. This is what has to be true before it is given to
anyone outside your own machines.

## Licences

- `client/` is RustDesk, **AGPL-3.0**. If you distribute the client (an
  installer, the APK) to anyone, you must offer them the complete
  corresponding source of that build, including our patch
  (`patches/0001-rd-fork.patch`, `client/rd/`, `client/src/rd*`,
  `client/flutter/lib/rd/`). The simplest way: make the repository (or a
  mirror of `client/` and `patches/`) public, and link it from the app's
  settings page and the download page. Keep RustDesk's copyright notices.
- `rendezvous/` is rustdesk-server, also **AGPL-3.0**. The AGPL covers network
  use: if others use your hbbs/hbbr, they are entitled to its source too. Same
  answer: publish `rendezvous/`.
- `server/`, `installer/`, `deploy/`: ours. **Decision needed: pick a licence**
  (e.g. AGPL-3.0 to match, or MIT/Apache-2.0 if you want it permissive) and
  add a `LICENSE` file at the root and in `server/`.
- Fonts: IBM Plex, SIL Open Font License (licence file shipped in
  `client/flutter/assets/fonts/`). Free to bundle.

## Name and trademarks

"RD" is a working name. "RustDesk" is a trademark of its authors: a public
release must not present itself as RustDesk (we already replaced the name,
icons and the "Powered by RustDesk" header) and should say "based on
RustDesk" in the About text, which Settings already does.

## Before a public release

- [ ] Licence chosen for our code, `LICENSE` files added
- [ ] Source offer: repository or mirror public, linked from the app
- [ ] Windows binaries code-signed (Authenticode certificate; unsigned
      installers trigger SmartScreen for other people)
- [ ] Android: release signing key set (`docs/android.md`)
- [ ] Multi-user: today every logged-in user can reach every device (see
      `docs/security-review.md`, "Data the server holds"); per-user access
      would be needed for anyone else's machines
- [ ] Privacy: RD runs hidden by design (no tray icon, no prompt); for other
      people's machines that needs their explicit consent and probably a
      visible indicator option
- [ ] Phase 4 exit test passed (`docs/phase-4-exit.md`)
