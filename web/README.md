# RD browser viewer

A small, dependency-free browser client for RD devices, served by the API
server at `https://<domain>/web/` (the console's device page links to it).

- `src/session.ts` speaks RustDesk's protocol: hbbs over `/ws/id`, hbbr over
  `/ws/relay`, the NaCl handshake (legacy key exchange), password login with
  the unattended hash from the address book, VP9/VP8 video via WebCodecs,
  mouse and keyboard (legacy keyboard mode), Ctrl+Alt+Del, screen switching,
  quality, and the Windows session picker.
- `src/pb.ts` is a schema-driven protobuf codec; `gen_proto.py` generates
  `src/schema.json` from the client's `.proto` files.
- `vendor/nacl-fast.js` is TweetNaCl.js 0.14.5 (public domain, Unlicense).

Not supported on purpose: files, clipboard, audio, H.264/H.265 (the device is
asked for VP9).

Build (output committed to `server/internal/webclient/static/app.js`, checked
in CI):

```bash
npm install -g esbuild@0.28.2 typescript@6.0.3
web/build.sh
```
