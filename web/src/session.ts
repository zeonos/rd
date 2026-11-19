// One remote-control session from the browser to an RD device.
//
// Path: WebSocket to hbbs (/ws/id) with a PunchHoleRequest marked SYMMETRIC,
// so the device answers through the relay; hbbs passes on the RelayResponse
// with the device's public key signed by the server key. Then a WebSocket to
// hbbr (/ws/relay), the same NaCl handshake as the native client (legacy key
// exchange, version 0), and login with the unattended-password hash from the
// address book: sha256(h1 || challenge).

import nacl from "../vendor/nacl-fast.js";
import { decode, encode, enums, Msg } from "./pb";

export const MOUSE_MOVE = 0;
export const MOUSE_DOWN = 1;
export const MOUSE_UP = 2;
export const MOUSE_WHEEL = 3;
export const BUTTON_LEFT = 1;
export const BUTTON_RIGHT = 2;
export const BUTTON_WHEEL = 4;

export interface Display {
  x: number;
  y: number;
  width: number;
  height: number;
  name?: string;
}

export interface SessionEvents {
  status(text: string): void;
  connected(info: { hostname: string; username: string; platform: string; displays: Display[]; current: number }): void;
  display(current: number, d: Display): void;
  frame(frame: VideoFrame): void;
  pickSession(sessions: { sid: number; name: string }[]): Promise<number>;
  closed(reason: string): void;
}

export interface SessionOptions {
  peerId: string;
  h1: Uint8Array; // sha256(password + salt) of the device's unattended password
  serverKey: string; // base64 Ed25519 public key of hbbs
  myName: string;
  quality: number; // enums.ImageQuality
}

const b64 = (s: string) => Uint8Array.from(atob(s), (c) => c.charCodeAt(0));
const te = new TextEncoder();

function nonce(seq: bigint): Uint8Array {
  const n = new Uint8Array(24);
  new DataView(n.buffer).setBigUint64(0, seq, true);
  return n;
}

function wsUrl(path: string) {
  return (location.protocol === "https:" ? "wss://" : "ws://") + location.host + path;
}

function openWs(url: string): Promise<WebSocket> {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(url);
    ws.binaryType = "arraybuffer";
    ws.onopen = () => resolve(ws);
    ws.onerror = () => reject(new Error("Cannot reach " + url));
  });
}

/** Queue of incoming WebSocket messages, read with next(). */
class Inbox {
  private q: Uint8Array[] = [];
  private waiters: ((b: Uint8Array | null) => void)[] = [];
  closed = false;
  constructor(ws: WebSocket) {
    ws.onmessage = (e) => this.push(new Uint8Array(e.data as ArrayBuffer));
    ws.onclose = () => {
      this.closed = true;
      for (const w of this.waiters.splice(0)) w(null);
    };
  }
  push(b: Uint8Array) {
    const w = this.waiters.shift();
    if (w) w(b);
    else this.q.push(b);
  }
  next(timeoutMs: number): Promise<Uint8Array | null> {
    const b = this.q.shift();
    if (b) return Promise.resolve(b);
    if (this.closed) return Promise.resolve(null);
    return new Promise((resolve) => {
      const t = setTimeout(() => {
        const i = this.waiters.indexOf(done);
        if (i >= 0) this.waiters.splice(i, 1);
        resolve(null);
      }, timeoutMs);
      const done = (x: Uint8Array | null) => {
        clearTimeout(t);
        resolve(x);
      };
      this.waiters.push(done);
    });
  }
}

export class Session {
  private ws: WebSocket | null = null;
  private streamKey: Uint8Array | null = null;
  private sendSeq = 0n;
  private recvSeq = 0n;
  private decoder: VideoDecoder | null = null;
  private codec = "";
  private waitKey = true;
  private closedReason = "";
  displays: Display[] = [];
  current = 0;
  platform = "";

  constructor(private opt: SessionOptions, private ev: SessionEvents) {}

  async start() {
    try {
      await this.run();
    } catch (e) {
      this.close((e as Error).message || String(e));
    }
  }

  close(reason = "Disconnected") {
    if (!this.closedReason) {
      this.closedReason = reason;
      try {
        this.ws?.close();
      } catch {}
      try {
        this.decoder?.close();
      } catch {}
      this.ev.closed(reason);
    }
  }

  // ------------------------------------------------------------ handshake

  private async run() {
    const o = this.opt;
    const serverPk = b64(o.serverKey);

    this.ev.status("Asking the device to connect…");
    const id = await openWs(wsUrl("/ws/id"));
    const idIn = new Inbox(id);
    id.send(
      encode("RendezvousMessage", {
        punch_hole_request: {
          id: o.peerId,
          nat_type: enums.NatType.SYMMETRIC,
          licence_key: o.serverKey,
          conn_type: enums.ConnType.DEFAULT_CONN,
          version: "1.5.3",
          force_relay: true,
        },
      }),
    );
    let rr: Msg | null = null;
    for (;;) {
      const b = await idIn.next(20000);
      if (!b) break;
      const m = decode("RendezvousMessage", b);
      if (m.punch_hole_response) {
        const f = m.punch_hole_response.failure ?? 0;
        const why = ["The device is not known to the RD server.", "", "The device is offline.", "The RD server key does not match.", "Too many devices for the licence."][f];
        throw new Error(m.punch_hole_response.other_failure || why || "The device cannot be reached.");
      }
      if (m.relay_response) {
        rr = m.relay_response;
        break;
      }
    }
    id.close();
    if (!rr) throw new Error("The device did not answer. It may be offline or busy.");
    if (rr.refuse_reason) throw new Error(rr.refuse_reason);

    // The device's signing key, vouched for by the RD server.
    const idPkSigned = nacl.sign.open(rr.pk || new Uint8Array(), serverPk);
    if (!idPkSigned) throw new Error("The RD server did not vouch for this device (key mismatch).");
    const idPk = decode("IdPk", idPkSigned);
    if (idPk.id !== o.peerId || !idPk.pk || idPk.pk.length !== 32) throw new Error("Unexpected device identity.");
    const deviceSignPk: Uint8Array = idPk.pk;

    this.ev.status("Connecting through the relay…");
    const relay = await openWs(wsUrl("/ws/relay"));
    this.ws = relay;
    const inbox = new Inbox(relay);
    relay.send(
      encode("RendezvousMessage", {
        request_relay: { licence_key: o.serverKey, id: o.peerId, uuid: rr.uuid, conn_type: enums.ConnType.DEFAULT_CONN },
      }),
    );

    // The device opens with its ephemeral box key, signed with its own key.
    const first = await inbox.next(20000);
    if (!first) throw new Error("The relay connection closed before the device answered.");
    const si = decode("Message", first).signed_id;
    if (!si) throw new Error("The device did not start the secure handshake.");
    const inner = nacl.sign.open(si.id || new Uint8Array(), deviceSignPk);
    if (!inner) throw new Error("The device's signature does not verify.");
    const theirs = decode("IdPk", inner);
    if (theirs.id !== o.peerId || !theirs.pk || theirs.pk.length !== 32) throw new Error("Unexpected device key.");
    const mine = nacl.box.keyPair();
    const key = nacl.randomBytes(32);
    const sealed = nacl.box(key, new Uint8Array(24), theirs.pk, mine.secretKey);
    this.sendRaw(encode("Message", { public_key: { asymmetric_value: mine.publicKey, symmetric_value: sealed } }));
    this.streamKey = key;

    this.ev.status("Signing in…");
    for (;;) {
      const raw = await inbox.next(60000);
      if (!raw) throw new Error(inbox.closed ? "The device closed the connection." : "The device stopped answering.");
      await this.handle(this.decrypt(raw));
      if (this.closedReason) return;
    }
  }

  // ------------------------------------------------------------ transport

  private sendRaw(b: Uint8Array) {
    b = b.buffer instanceof ArrayBuffer ? b : b.slice();
    if (this.ws && this.ws.readyState === WebSocket.OPEN) this.ws.send(b as Uint8Array<ArrayBuffer>);
  }

  private send(msg: Msg) {
    let b: Uint8Array = encode("Message", msg);
    if (this.streamKey) {
      this.sendSeq++;
      b = nacl.secretbox(b, nonce(this.sendSeq), this.streamKey);
    }
    this.sendRaw(b);
  }

  private decrypt(b: Uint8Array): Uint8Array {
    if (!this.streamKey || b.length <= 1) return b;
    this.recvSeq++;
    const out = nacl.secretbox.open(b, nonce(this.recvSeq), this.streamKey);
    if (!out) throw new Error("Decryption failed: the connection is out of step.");
    return out;
  }

  // ------------------------------------------------------------ messages

  private async handle(b: Uint8Array) {
    const m = decode("Message", b);
    if (m.hash) {
      const c = te.encode(m.hash.challenge || "");
      const buf = new Uint8Array(this.opt.h1.length + c.length);
      buf.set(this.opt.h1);
      buf.set(c, this.opt.h1.length);
      const password = new Uint8Array(await crypto.subtle.digest("SHA-256", buf));
      const sid = new Uint8Array(8);
      crypto.getRandomValues(sid);
      this.send({
        login_request: {
          username: this.opt.peerId,
          password,
          my_id: "web",
          my_name: this.opt.myName,
          my_platform: "Web",
          version: "1.5.3",
          session_id: new DataView(sid.buffer).getBigUint64(0) >> 1n,
          option: {
            image_quality: this.opt.quality,
            supported_decoding: { ability_vp9: 1, ability_vp8: 1, prefer: enums["SupportedDecoding.PreferCodec"].VP9 },
          },
        },
      });
    } else if (m.login_response) {
      const lr = m.login_response;
      if (lr.error) {
        this.close(lr.error === "Wrong Password" ? "The device refused the unattended password. Restart its RD service and try again." : lr.error);
        return;
      }
      if (lr.peer_info) await this.onPeerInfo(lr.peer_info);
    } else if (m.peer_info) {
      await this.onPeerInfo(m.peer_info);
    } else if (m.video_frame) {
      this.onVideo(m.video_frame);
    } else if (m.test_delay) {
      if (!m.test_delay.from_client) this.send({ test_delay: m.test_delay });
    } else if (m.misc) {
      const mi = m.misc;
      if (mi.switch_display) {
        const s = mi.switch_display;
        this.current = s.display ?? 0;
        const d = { x: s.x ?? 0, y: s.y ?? 0, width: s.width ?? 0, height: s.height ?? 0 };
        this.displays[this.current] = { ...this.displays[this.current], ...d };
        this.waitKey = true;
        this.ev.display(this.current, d);
      }
      if (mi.close_reason) this.close(mi.close_reason);
    } else if (m.message_box) {
      const mb = m.message_box;
      if (/error/.test(mb.msgtype || "")) this.ev.status(`${mb.title || ""}: ${mb.text || ""}`);
    }
  }

  private async onPeerInfo(pi: Msg) {
    const ws = pi.windows_sessions;
    if (ws && ws.sessions && ws.sessions.length > 1) {
      const sid = await this.ev.pickSession(ws.sessions.map((s: Msg) => ({ sid: s.sid ?? 0, name: s.name || `Session ${s.sid}` })));
      this.send({ misc: { selected_sid: sid } });
      return; // the device sends PeerInfo again for the chosen session
    }
    this.platform = pi.platform || "";
    this.displays = (pi.displays || []).map((d: Msg) => ({ x: d.x ?? 0, y: d.y ?? 0, width: d.width ?? 0, height: d.height ?? 0, name: d.name }));
    this.current = pi.current_display ?? 0;
    this.ev.connected({ hostname: pi.hostname || this.opt.peerId, username: pi.username || "", platform: this.platform, displays: this.displays, current: this.current });
  }

  // ------------------------------------------------------------ video

  private onVideo(vf: Msg) {
    const kinds: [string, string][] = [["vp9s", "vp09.00.10.08"], ["vp8s", "vp8"]];
    for (const [field, codec] of kinds) {
      const frames = vf[field]?.frames as Msg[] | undefined;
      if (!frames) continue;
      if (this.codec !== codec || !this.decoder || this.decoder.state === "closed") this.makeDecoder(codec);
      for (const f of frames) {
        if (this.waitKey && !f.key) continue;
        this.waitKey = false;
        try {
          this.decoder!.decode(new EncodedVideoChunk({ type: f.key ? "key" : "delta", timestamp: Number(f.pts ?? 0) * 1000, data: f.data }));
        } catch {
          this.waitKey = true;
          this.send({ misc: { refresh_video: true } });
        }
      }
      return;
    }
  }

  private makeDecoder(codec: string) {
    try {
      this.decoder?.close();
    } catch {}
    this.codec = codec;
    this.waitKey = true;
    this.decoder = new VideoDecoder({
      output: (frame) => this.ev.frame(frame),
      error: () => {
        this.waitKey = true;
        this.send({ misc: { refresh_video: true } });
      },
    });
    this.decoder.configure({ codec, optimizeForLatency: true });
    this.send({ misc: { refresh_video: true } });
  }

  // ------------------------------------------------------------ input

  mouse(type: number, buttons: number, x: number, y: number) {
    this.send({ mouse_event: { mask: type | (buttons << 3), x: Math.round(x), y: Math.round(y) } });
  }

  wheel(dy: number, dx = 0) {
    this.send({ mouse_event: { mask: MOUSE_WHEEL, x: dx, y: dy } });
  }

  key(ev: Msg) {
    this.send({ key_event: { ...ev, mode: enums.KeyboardMode.Legacy } });
  }

  ctrlAltDel() {
    const CK = enums.ControlKey;
    if (this.platform === "Windows") this.key({ control_key: CK.CtrlAltDel, down: true });
    else this.key({ control_key: CK.Delete, modifiers: [CK.Control, CK.Alt], press: true });
  }

  switchDisplay(i: number) {
    this.send({ misc: { switch_display: { display: i } } });
  }

  setQuality(q: number) {
    this.send({ misc: { option: { image_quality: q } } });
  }

  sendText(text: string) {
    this.key({ seq: text, press: true });
  }
}
