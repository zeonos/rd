// RD browser viewer: sign in (or reuse the console's session), pick a device,
// view and control it. Limited on purpose: screen, mouse, keyboard,
// Ctrl+Alt+Del, screens, scale, quality, typing text, text clipboard. No files.

import { enums } from "./pb";
import {
  BUTTON_LEFT,
  BUTTON_RIGHT,
  BUTTON_WHEEL,
  Display,
  MOUSE_DOWN,
  MOUSE_MOVE,
  MOUSE_UP,
  Session,
} from "./session";

declare const RD_SERVER_KEY: string;

interface Device {
  id: string;
  name: string;
  group: string;
  online: boolean;
  os: string;
}

const S = {
  token: sessionStorage.getItem("rd.token") || "",
  user: sessionStorage.getItem("rd.user") || "",
  hashes: new Map<string, string>(),
};

// ------------------------------------------------------------- helpers

function h(tag: string, props: Record<string, any> = {}, ...kids: any[]): HTMLElement {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? "" : String(v));
  }
  for (const kid of kids.flat()) {
    if (kid === undefined || kid === null || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

const app = () => document.getElementById("app")!;

async function api(method: string, path: string, body?: any) {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (S.token) headers.Authorization = "Bearer " + S.token;
  const res = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await res.text();
  let data: any = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = text;
  }
  if (res.status === 401 && S.token) {
    signOut();
    throw new Error("Your session ended. Sign in again.");
  }
  if (!res.ok) throw new Error((data && data.error) || `HTTP ${res.status}`);
  return data;
}

function signOut() {
  S.token = "";
  sessionStorage.removeItem("rd.token");
  sessionStorage.removeItem("rd.user");
  renderSignIn();
}

const mark = () => {
  const s = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  s.setAttribute("viewBox", "0 0 64 64");
  s.setAttribute("class", "mark");
  s.setAttribute("aria-hidden", "true");
  s.innerHTML =
    '<rect width="64" height="64" rx="14" fill="#0F6E74"/><rect x="11" y="14" width="42" height="30" rx="4" fill="none" stroke="#fff" stroke-width="4"/><path d="M29 21 L29 39 L33.5 34.5 L37 41 L40 39.5 L36.5 33 L42 32 Z" fill="#F2B134"/>';
  return s;
};

// ------------------------------------------------------------- sign in

function renderSignIn(msg = "") {
  const user = h("input", { type: "text", autocomplete: "username", required: true }) as HTMLInputElement;
  const pass = h("input", { type: "password", autocomplete: "current-password", required: true }) as HTMLInputElement;
  const code = h("input", { type: "text", inputmode: "numeric", autocomplete: "one-time-code", maxlength: "6", class: "code" }) as HTMLInputElement;
  const err = h("p", { class: "error", role: "alert" }, msg);
  let secret = "";
  const stepCode = h("div", { hidden: true }, h("label", { class: "field" }, h("span", {}, "Authenticator code"), code));
  const button = h("button", { class: "btn primary wide", type: "submit" }, "Continue");
  const form = h(
    "form",
    {
      class: "card",
      onsubmit: async (e: Event) => {
        e.preventDefault();
        err.textContent = "";
        try {
          if (!secret) {
            const r = await api("POST", "/api/login", { username: user.value.trim(), password: pass.value, type: "account", id: "web" });
            if (r.type === "access_token") return signedIn(r);
            secret = r.secret;
            stepCode.hidden = false;
            button.textContent = "Sign in";
            code.focus();
          } else {
            const r = await api("POST", "/api/login", { username: user.value.trim(), type: "email_code", tfaCode: code.value.trim(), verificationCode: code.value.trim(), secret, id: "web" });
            if (r.type === "access_token") signedIn(r);
          }
        } catch (ex) {
          err.textContent = (ex as Error).message;
        }
      },
    },
    h("div", { class: "brand" }, mark(), h("strong", {}, "RD")),
    h("h1", {}, "Sign in"),
    h("p", { class: "muted" }, "Server: ", h("span", { class: "mono" }, location.host)),
    h("label", { class: "field" }, h("span", {}, "Username"), user),
    h("label", { class: "field" }, h("span", {}, "Password"), pass),
    stepCode,
    err,
    button,
  );
  app().replaceChildren(h("div", { class: "center" }, form));
  user.focus();
}

function signedIn(r: any) {
  S.token = r.access_token;
  S.user = (r.user && r.user.name) || "";
  sessionStorage.setItem("rd.token", S.token);
  sessionStorage.setItem("rd.user", S.user);
  route();
}

// ------------------------------------------------------------- devices

async function loadHashes() {
  const ab = await api("GET", "/api/ab");
  const data = typeof ab.data === "string" ? JSON.parse(ab.data) : ab.data;
  S.hashes.clear();
  for (const p of data.peers || []) if (p.hash) S.hashes.set(String(p.id), p.hash);
}

async function renderDevices() {
  const list: any[] = (await api("GET", "/api/admin/devices")) || [];
  const devices: Device[] = list
    .map((d) => ({ id: d.id, name: d.alias || d.hostname || d.id, group: d.group || "Unassigned", online: !!d.online, os: d.os || "" }))
    .sort((a, b) => (a.online === b.online ? a.name.localeCompare(b.name) : a.online ? -1 : 1));
  const search = h("input", { type: "search", placeholder: "Search name or ID", "aria-label": "Search devices" }) as HTMLInputElement;
  const body = h("div", { class: "list" });
  const fill = () => {
    const q = search.value.trim().toLowerCase();
    body.replaceChildren(
      ...devices
        .filter((d) => !q || d.name.toLowerCase().includes(q) || d.id.includes(q.replace(/\s/g, "")))
        .map((d) =>
          h(
            "a",
            { class: "row", href: "#" + d.id },
            h("span", { class: d.online ? "dot on" : "dot off", role: "img", "aria-label": d.online ? "Online" : "Offline" }),
            h("span", { class: "name" }, d.name),
            h("span", { class: "muted" }, d.group),
            h("span", { class: "mono muted" }, d.id),
          ),
        ),
    );
  };
  search.addEventListener("input", fill);
  fill();
  app().replaceChildren(
    h(
      "div",
      { class: "page" },
      h("header", { class: "top" }, mark(), h("strong", {}, "RD"), search, h("span", { class: "spacer" }), h("span", { class: "muted-on-ink" }, S.user), h("button", { class: "btn ghost", onclick: signOut }, "Sign out")),
      h("main", { class: "devices" }, h("h1", {}, "Devices"), h("p", { class: "muted" }, "The browser viewer is for quick access: screen, mouse, keyboard, Ctrl+Alt+Del and text clipboard. Use the RD app for file transfer."), body),
    ),
  );
  search.focus();
}

// ------------------------------------------------------------- session

const CK = enums.ControlKey;
const CONTROL_KEYS: Record<string, number> = {
  Enter: CK.Return, Backspace: CK.Backspace, Tab: CK.Tab, Escape: CK.Escape, Delete: CK.Delete, Insert: CK.Insert,
  Home: CK.Home, End: CK.End, PageUp: CK.PageUp, PageDown: CK.PageDown, ArrowUp: CK.UpArrow, ArrowDown: CK.DownArrow,
  ArrowLeft: CK.LeftArrow, ArrowRight: CK.RightArrow, CapsLock: CK.CapsLock, Shift: CK.Shift, Control: CK.Control,
  Alt: CK.Alt, Meta: CK.Meta, ContextMenu: CK.Apps, PrintScreen: CK.Snapshot, Pause: CK.Pause,
  F1: CK.F1, F2: CK.F2, F3: CK.F3, F4: CK.F4, F5: CK.F5, F6: CK.F6, F7: CK.F7, F8: CK.F8, F9: CK.F9, F10: CK.F10, F11: CK.F11, F12: CK.F12,
};

function segmented(label: string, options: [string, string][], current: string, pick: (v: string) => void) {
  const wrap = h("div", { class: "seg", role: "group", "aria-label": label });
  const render = (cur: string) =>
    wrap.replaceChildren(
      ...options.map(([v, text]) =>
        h("button", { type: "button", "aria-pressed": v === cur ? "true" : "false", onclick: () => { render(v); pick(v); } }, text),
      ),
    );
  render(current);
  return h("div", { class: "group" }, h("span", { class: "label" }, label), wrap);
}

async function renderSession(id: string) {
  if (!S.hashes.size) await loadHashes();
  const hash = S.hashes.get(id);
  if (!hash) {
    app().replaceChildren(h("div", { class: "center" }, h("div", { class: "card" }, h("h1", {}, "Not available"), h("p", {}, `Device ${id} has not reported an unattended password yet, or is not in your address book. Restart its RD service and try again.`), h("a", { class: "btn", href: "#" }, "Back to devices"))));
    return;
  }
  const canvas = h("canvas", { tabindex: "0", "aria-label": "Remote screen" }) as HTMLCanvasElement;
  const ctx = canvas.getContext("2d")!;
  const status = h("div", { class: "overlay", role: "status" }, "Connecting…");
  const stage = h("div", { class: "stage fit" }, canvas, status);
  const screens = h("div", { class: "group" });
  let scale = "fit";
  let disp: Display = { x: 0, y: 0, width: 0, height: 0 };

  const sess: Session = new Session(
    {
      peerId: id,
      h1: Uint8Array.from(atob(hash), (c) => c.charCodeAt(0)),
      serverKey: RD_SERVER_KEY,
      myName: S.user || "Browser",
      quality: enums.ImageQuality.Balanced,
    },
    {
      status: (t) => (status.textContent = t),
      connected: (info) => {
        document.title = `${info.hostname} – RD`;
        title.textContent = info.hostname;
        disp = info.displays[info.current] || disp;
        renderScreens(info.displays, info.current);
        status.textContent = "Waiting for the picture…";
      },
      display: (cur, d) => {
        disp = d;
        renderScreens(sess.displays, cur);
      },
      frame: (f) => {
        if (canvas.width !== f.displayWidth || canvas.height !== f.displayHeight) {
          canvas.width = f.displayWidth;
          canvas.height = f.displayHeight;
        }
        ctx.drawImage(f, 0, 0);
        f.close();
        status.hidden = true;
      },
      pickSession: (sessions) =>
        new Promise((resolve) => {
          status.hidden = false;
          status.replaceChildren(
            h("p", {}, "This server has more than one Windows session. Which one do you want to see?"),
            ...sessions.map((s) => h("button", { class: "btn", onclick: () => { status.textContent = "Opening the session…"; resolve(s.sid); } }, s.name)),
          );
        }),
      clipboard: (text) => clip.fromRemote(text),
      closed: (reason) => {
        status.hidden = false;
        status.replaceChildren(h("p", {}, reason), h("button", { class: "btn primary", onclick: () => renderSession(id) }, "Reconnect"), " ", h("a", { class: "btn", href: "#" }, "Back to devices"));
      },
    },
  );

  // Text clipboard. Remote to here: written when the device's clipboard
  // changes, or when this tab next has focus (browsers only allow writes
  // from a focused page). Here to remote: sent whenever the tab gains focus,
  // once the browser allows reading; "Send clipboard" asks for that.
  const clipNote = h("span", { class: "label", role: "status" });
  const clip = {
    last: "", // last text either side had, so we never echo it back
    pending: "",
    note(t: string) {
      clipNote.textContent = t;
      setTimeout(() => { if (clipNote.textContent === t) clipNote.textContent = ""; }, 3000);
    },
    async fromRemote(text: string) {
      if (text === this.last) return;
      this.last = text;
      this.pending = text;
      await this.flush();
    },
    async flush() {
      if (!this.pending || !document.hasFocus()) return;
      try {
        await navigator.clipboard.writeText(this.pending);
        this.pending = "";
        this.note("Copied from remote");
      } catch {}
    },
    async toRemote(ask: boolean) {
      if (!ask) {
        try {
          const p = await navigator.permissions.query({ name: "clipboard-read" as PermissionName });
          if (p.state !== "granted") return;
        } catch {
          return;
        }
      }
      let text = "";
      try {
        text = await navigator.clipboard.readText();
      } catch {
        if (ask) this.note("The browser did not allow reading the clipboard");
        return;
      }
      if (!text || text === this.last) {
        if (ask) this.note(text ? "Already on the remote clipboard" : "Your clipboard has no text");
        return;
      }
      this.last = text;
      sess.sendClipboard(text);
      this.note("Sent to remote clipboard");
    },
  };
  const onFocus = () => {
    clip.flush();
    clip.toRemote(false);
  };
  window.addEventListener("focus", onFocus);

  function renderScreens(displays: Display[], cur: number) {
    if (displays.length <= 1) {
      screens.replaceChildren();
      return;
    }
    screens.replaceChildren(
      segmented("Screen", displays.map((_, i) => [String(i), String(i + 1)]), String(cur), (v) => sess.switchDisplay(Number(v))),
    );
  }

  // mouse: canvas pixels map 1:1 to the remote display
  const toRemote = (e: MouseEvent) => {
    const r = canvas.getBoundingClientRect();
    const sx = canvas.width / r.width;
    const sy = canvas.height / r.height;
    return [disp.x + (e.clientX - r.left) * sx, disp.y + (e.clientY - r.top) * sy];
  };
  const btn = (b: number) => (b === 2 ? BUTTON_RIGHT : b === 1 ? BUTTON_WHEEL : BUTTON_LEFT);
  let lastMove = 0;
  canvas.addEventListener("mousemove", (e) => {
    const now = performance.now();
    if (now - lastMove < 12) return;
    lastMove = now;
    const [x, y] = toRemote(e);
    sess.mouse(MOUSE_MOVE, 0, x, y);
  });
  canvas.addEventListener("mousedown", (e) => {
    canvas.focus();
    const [x, y] = toRemote(e);
    sess.mouse(MOUSE_DOWN, btn(e.button), x, y);
    e.preventDefault();
  });
  canvas.addEventListener("mouseup", (e) => {
    const [x, y] = toRemote(e);
    sess.mouse(MOUSE_UP, btn(e.button), x, y);
    e.preventDefault();
  });
  canvas.addEventListener("contextmenu", (e) => e.preventDefault());
  canvas.addEventListener(
    "wheel",
    (e) => {
      e.preventDefault();
      if (e.deltaY) sess.wheel(e.deltaY > 0 ? -1 : 1);
      if (e.deltaX) sess.wheel(0, e.deltaX > 0 ? -1 : 1);
    },
    { passive: false },
  );

  // keyboard: control keys up/down, characters as text, Ctrl/Alt combos as chr + modifiers
  const mods = (e: KeyboardEvent) => [e.ctrlKey && CK.Control, e.altKey && CK.Alt, e.shiftKey && CK.Shift, e.metaKey && CK.Meta].filter(Boolean) as number[];
  canvas.addEventListener("keydown", (e) => {
    e.preventDefault();
    const ck = CONTROL_KEYS[e.key];
    if (ck !== undefined) {
      sess.key({ control_key: ck, down: true, modifiers: mods(e) });
    } else if (e.key.length === 1) {
      if (e.ctrlKey || e.altKey || e.metaKey) sess.key({ chr: e.key.toLowerCase().charCodeAt(0), down: true, modifiers: mods(e) });
      else sess.sendText(e.key);
    }
  });
  canvas.addEventListener("keyup", (e) => {
    e.preventDefault();
    const ck = CONTROL_KEYS[e.key];
    if (ck !== undefined) sess.key({ control_key: ck, down: false });
    else if (e.key.length === 1 && (e.ctrlKey || e.altKey || e.metaKey)) sess.key({ chr: e.key.toLowerCase().charCodeAt(0), down: false });
  });

  const title = h("strong", { class: "title" }, id);
  const toolbar = h(
    "div",
    { class: "toolbar", role: "toolbar", "aria-label": "Session" },
    screens,
    h("button", { class: "btn", type: "button", onclick: () => { sess.ctrlAltDel(); canvas.focus(); } }, "Ctrl+Alt+Del"),
    segmented("Scale", [["fit", "Fit window"], ["100", "100%"]], scale, (v) => {
      scale = v;
      stage.classList.toggle("fit", v === "fit");
      canvas.focus();
    }),
    segmented("Quality", [["Best", "Best"], ["Balanced", "Balanced"], ["Low", "Fast"]], "Balanced", (v) => {
      sess.setQuality(enums.ImageQuality[v]);
      canvas.focus();
    }),
    h(
      "button",
      {
        class: "btn",
        type: "button",
        onclick: () => {
          const t = prompt("Text to type on the remote computer:");
          if (t) sess.sendText(t);
          canvas.focus();
        },
      },
      "Type text…",
    ),
    h("div", { class: "group" },
      h("button", { class: "btn", type: "button", title: "Put the text on your clipboard on the remote clipboard. After you allow it once, this happens by itself when you come back to this tab.", onclick: async () => { await clip.toRemote(true); canvas.focus(); } }, "Send clipboard"),
      clipNote),
    h("span", { class: "spacer" }),
    h("button", { class: "btn danger", type: "button", onclick: () => { sess.close("Disconnected."); } }, "Disconnect"),
  );
  app().replaceChildren(h("div", { class: "session" }, h("header", { class: "top" }, mark(), title, h("span", { class: "spacer" }), h("a", { class: "btn ghost", href: "#" }, "Devices")), toolbar, stage));
  canvas.focus();
  window.addEventListener("hashchange", () => { window.removeEventListener("focus", onFocus); sess.close("Disconnected."); }, { once: true });
  sess.start();
}

// ------------------------------------------------------------- router

async function route() {
  if (!S.token) return renderSignIn();
  if (!("VideoDecoder" in window)) {
    app().replaceChildren(h("div", { class: "center" }, h("div", { class: "card" }, h("h1", {}, "Browser not supported"), h("p", {}, "The RD browser viewer needs a current Chrome, Edge or Firefox (WebCodecs)."))));
    return;
  }
  const id = decodeURIComponent(location.hash.slice(1));
  try {
    if (id) await renderSession(id);
    else await renderDevices();
  } catch (e) {
    if (S.token) app().replaceChildren(h("div", { class: "center" }, h("div", { class: "card" }, h("p", { class: "error" }, (e as Error).message), h("a", { class: "btn", href: "#" }, "Back"))));
  }
}

window.addEventListener("hashchange", route);
route();
