# Android viewer

The **Android client** workflow builds `rd-android-arm64.apk` (artifact
`rd-android-arm64`) on every push to `main` that touches `client/`, and on
demand (Actions → Android client → Run workflow). It runs on any 64-bit ARM
phone or tablet with Android 5.1 or later.

The app is the RD viewer only: sign in (password + authenticator code),
devices by location, connect, files, run script, reboot, shut down. It
installs next to the RustDesk app (package `dk.sfohosting.rd`).

## Install (sideload)

1. Download the artifact from the workflow run and unzip it.
2. Copy `rd-android-arm64.apk` to the phone and open it. Allow "Install
   unknown apps" for the file manager or browser when Android asks.
3. Open RD and sign in.

## Release signing key (do this once)

Without a signing key the APK is debug-signed with a key that changes per CI
run, so Android refuses to install a newer build over an older one: you have
to uninstall first. With a release key, new builds install as updates.

Create the key on the RD server (it has Docker; the key never leaves it except
as a GitHub secret). Pick a long password:

```bash
mkdir -p /root/rd-android && chmod 700 /root/rd-android
cd /root/rd-android && read -s -p "Keystore password: " RDKS && echo
cd /root/rd-android && docker run --rm -v /root/rd-android:/out eclipse-temurin:17-jdk keytool -genkeypair -keystore /out/rd-release.jks -alias rd -keyalg RSA -keysize 4096 -validity 10000 -dname "CN=RD, O=sfohosting.dk" -storepass "$RDKS" -keypass "$RDKS"
cd /root/rd-android && base64 -w0 rd-release.jks > rd-release.jks.b64 && wc -c rd-release.jks.b64
```

Then in GitHub: **zeonos/rd → Settings → Secrets and variables → Actions →
New repository secret**, four times:

| Secret | Value |
|---|---|
| `ANDROID_SIGNING_KEY` | the contents of `/root/rd-android/rd-release.jks.b64` (`cat` it) |
| `ANDROID_ALIAS` | `rd` |
| `ANDROID_KEY_STORE_PASSWORD` | the password you typed |
| `ANDROID_KEY_PASSWORD` | the same password |

Keep `/root/rd-android/rd-release.jks` and its password: `deploy/backup.sh`
does not include it, so copy it somewhere safe as well. Losing it means users
must uninstall once and install a build signed with a new key.

The first build after adding the key is signed with it; uninstall any
debug-signed RD app once before installing it.
