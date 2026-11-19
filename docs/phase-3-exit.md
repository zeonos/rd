# Phase 3 exit test ("Feature freeze")

- [ ] **Android:** install `rd-android-arm64.apk` (docs/android.md) on a phone on mobile data; sign in with password + code; open a server; switch monitors; send Ctrl+Alt+Del; open Files
- [ ] **Browser:** on a PC with nothing installed, open `https://rd.sfohosting.dk/web/`, sign in, open a server; mouse, keyboard and Ctrl+Alt+Del work; switch monitors
- [ ] **Auto-update:** upload a new `rd-windows-x64.zip` in Updates → Roll out → every online device reports the new build within 15 minutes; an offline device updates within 15 minutes of coming back; open sessions reconnect
- [ ] **RDP session picker:** on a Server 2022 with someone logged in over RDP, connect from the viewer: it asks which session to show; both the console and the RDP session can be viewed
- [ ] **Viewer actions:** from the Windows viewer home: Run script shows output; Reboot asks for confirmation and the device comes back
