// RD web console. No build step, no dependencies: plain ES modules served by
// the API server. All text goes through textContent (via h()), never innerHTML.

const S = {
  token: sessionStorage.getItem("rd.token") || "",
  user: sessionStorage.getItem("rd.user") || "",
  devices: [],
  groups: [],
  loc: "all", // "all" | "unassigned" | group id
  selected: new Set(),
  search: "",
  timer: null,
};

// ---------- helpers ----------

function h(tag, props, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "class") el.className = v;
    else if (k === "style") el.style.cssText = v; // CSSOM, allowed by the CSP
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "value") el.value = v;
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const kid of kids.flat()) {
    if (kid === undefined || kid === null || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

class ApiError extends Error {
  constructor(status, msg) { super(msg); this.status = status; }
}

async function api(method, path, body) {
  const headers = { "Content-Type": "application/json" };
  if (S.token) headers.Authorization = "Bearer " + S.token;
  const res = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  if (res.status === 401 && S.token) {
    logout("Your session ended. Log in again.");
    throw new ApiError(401, "Unauthorized");
  }
  if (res.status === 204) return null;
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = text; }
  if (!res.ok) throw new ApiError(res.status, (data && data.error) || `HTTP ${res.status}`);
  return data;
}

function toast(msg) {
  const t = h("div", { class: "toast", role: "status" }, msg);
  document.body.append(t);
  setTimeout(() => t.remove(), 2600);
}

function ago(iso) {
  if (!iso) return "never";
  const s = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 0) return "in " + dur(-s);
  if (s < 45) return "just now";
  return dur(s) + " ago";
}
function dur(s) {
  if (s < 90) return `${s} s`;
  if (s < 5400) return `${Math.round(s / 60)} min`;
  if (s < 129600) return `${Math.round(s / 3600)} h`;
  return `${Math.round(s / 86400)} days`;
}
function when(iso) { return iso ? new Date(iso).toLocaleString() : ""; }
function deviceName(d) { return d.alias || d.hostname || d.id; }
function go(hash) { if (location.hash === hash) render(); else location.hash = hash; }

function statusDot(kind, label) {
  return h("span", { class: "status" }, h("span", { class: "dot " + kind, "aria-hidden": "true" }), label);
}
const runStatus = {
  queued: ["wait", "Waiting"], sent: ["wait", "Running"], success: ["ok", "Done"],
  failed: ["bad", "Failed"], expired: ["", "Expired"], cancelled: ["", "Cancelled"],
};

// ---------- auth ----------

function logout(msg) {
  if (S.token) api("POST", "/api/logout").catch(() => {});
  S.token = ""; S.user = "";
  sessionStorage.removeItem("rd.token"); sessionStorage.removeItem("rd.user");
  clearInterval(S.timer);
  renderLogin(msg);
}

function renderLogin(msg) {
  const app = document.getElementById("app");
  let secret = null;
  const err = h("p", { class: "error", role: "alert" }, msg || "");
  const user = h("input", { type: "text", autocomplete: "username", required: true, value: S.user || "admin" });
  const pass = h("input", { type: "password", autocomplete: "current-password", required: true });
  const code = h("input", { type: "text", inputmode: "numeric", autocomplete: "one-time-code", pattern: "[0-9]{6}", maxlength: "6" });
  const codeField = h("label", { class: "field", hidden: true }, h("span", {}, "Authenticator code"), code);
  const submit = h("button", { class: "btn primary", type: "submit" }, "Log in");

  async function onsubmit(e) {
    e.preventDefault();
    err.textContent = "";
    submit.disabled = true;
    try {
      let res;
      if (!secret) {
        res = await api("POST", "/api/login", { username: user.value, password: pass.value, type: "account" });
        if (res.type === "email_check") {
          secret = res.secret;
          codeField.hidden = false;
          user.disabled = pass.disabled = true;
          submit.textContent = "Verify";
          code.focus();
          return;
        }
      } else {
        res = await api("POST", "/api/login", { username: user.value, tfaCode: code.value, secret, type: "email_code" });
      }
      if (res.type === "access_token") {
        S.token = res.access_token; S.user = res.user.name;
        sessionStorage.setItem("rd.token", S.token); sessionStorage.setItem("rd.user", S.user);
        if (!location.hash || location.hash === "#/") location.hash = "#/devices";
        render();
      }
    } catch (ex) {
      err.textContent = ex.message;
      if (secret && ex.status === 401) { code.value = ""; code.focus(); }
    } finally {
      submit.disabled = false;
    }
  }

  app.replaceChildren(h("div", { class: "login" },
    h("form", { onsubmit },
      h("div", { class: "brand" }, h("span", { class: "brand-mark" }), "RD console"),
      h("label", { class: "field" }, h("span", {}, "Username"), user),
      h("label", { class: "field" }, h("span", {}, "Password"), pass),
      codeField, err, submit)));
  (S.user ? pass : user).focus();
}

// ---------- data ----------

async function loadFleet() {
  const [devices, groups] = await Promise.all([api("GET", "/api/admin/devices"), api("GET", "/api/admin/groups")]);
  S.devices = devices || [];
  S.groups = groups || [];
}

function groupById(id) { return S.groups.find((g) => g.id === id); }
function inSubtree(d, gid) {
  if (d.group_id == null) return false;
  const g = groupById(gid);
  const dg = groupById(d.group_id);
  if (!g || !dg) return false;
  return dg.id === g.id || dg.path.toLowerCase().startsWith(g.path.toLowerCase() + "/");
}
function devicesIn(loc) {
  if (loc === "all") return S.devices;
  if (loc === "unassigned") return S.devices.filter((d) => d.group_id == null);
  return S.devices.filter((d) => inSubtree(d, loc));
}

// ---------- shell ----------

const TABS = [["devices", "Devices"], ["jobs", "Jobs"], ["scripts", "Scripts"], ["settings", "Settings"], ["updates", "Updates"], ["enrol", "Enrolment"]];

function shell(section, content) {
  const tabs = h("nav", { class: "tabs", "aria-label": "Sections" },
    TABS.map(([id, label]) => h("a", { href: "#/" + id, "aria-current": section === id ? "page" : false }, label)));
  return h("div", { class: "shell" },
    h("header", { class: "top" },
      h("div", { class: "brand" }, h("span", { class: "brand-mark" }), "RD console"),
      tabs,
      h("div", { class: "top-right" },
        h("span", { class: "muted" }, S.user),
        h("button", { class: "btn", onclick: () => logout() }, "Log out"))),
    locationRail(section),
    h("main", { class: "main", id: "main" }, content));
}

function locationRail(section) {
  const node = (key, label, list, depth) => {
    const online = list.filter((d) => d.online).length;
    const pct = list.length ? Math.round((online / list.length) * 100) : 0;
    return h("li", {},
      h("button", {
        class: "node", style: `padding-left:${8 + depth * 14}px`,
        "aria-current": String(S.loc) === String(key) && section === "devices" ? "true" : "false",
        title: `${online} of ${list.length} online`,
        onclick: () => { S.loc = key; S.selected.clear(); go("#/devices"); },
      },
      h("span", { class: "label" }, label),
      h("span", { class: "leds" }, `${online}/${list.length}`, h("span", { class: "bar" }, h("span", { style: `width:${pct}%` })))));
  };
  const items = [node("all", "All devices", S.devices, 0)];
  const walk = (parent, depth) => {
    for (const g of S.groups.filter((x) => (x.parent_id ?? null) === parent)) {
      items.push(node(g.id, g.name, devicesIn(g.id), depth));
      walk(g.id, depth + 1);
    }
  };
  walk(null, 0);
  const un = devicesIn("unassigned");
  if (un.length) items.push(node("unassigned", "Unassigned", un, 0));
  return h("aside", { class: "rail", "aria-label": "Locations" }, h("h2", {}, "Locations"), h("ul", { class: "tree" }, items));
}

// ---------- devices ----------

function devicesView(selectedId) {
  const list = devicesIn(S.loc).filter((d) => {
    if (!S.search) return true;
    const q = S.search.toLowerCase();
    return [d.id, d.alias, d.hostname, d.username, d.os, d.group].some((x) => (x || "").toLowerCase().includes(q));
  });
  const locName = S.loc === "all" ? "All devices" : S.loc === "unassigned" ? "Unassigned" : (groupById(S.loc)?.path || "");
  const online = list.filter((d) => d.online).length;

  const search = h("input", {
    type: "search", placeholder: "Search name, ID, user", value: S.search, "aria-label": "Search devices",
    oninput: (e) => { S.search = e.target.value; const pos = e.target.selectionStart; render(); const s = document.querySelector("input[type=search]"); s.focus(); s.setSelectionRange(pos, pos); },
  });
  const sel = [...S.selected].filter((id) => S.devices.some((d) => d.id === id));
  const allBox = h("input", {
    type: "checkbox", "aria-label": "Select all",
    checked: list.length > 0 && list.every((d) => S.selected.has(d.id)),
    onchange: (e) => { list.forEach((d) => e.target.checked ? S.selected.add(d.id) : S.selected.delete(d.id)); render(); },
  });

  const rows = list.map((d) => h("tr", {
    class: "click" + (d.id === selectedId ? " sel" : ""),
    onclick: (e) => { if (e.target.type !== "checkbox") go("#/devices/" + d.id); },
  },
  h("td", {}, h("input", {
    type: "checkbox", "aria-label": "Select " + deviceName(d), checked: S.selected.has(d.id),
    onchange: (e) => { e.target.checked ? S.selected.add(d.id) : S.selected.delete(d.id); render(); },
  })),
  h("td", {}, statusDot(d.online ? "ok" : "", d.online ? "Online" : "Offline")),
  h("td", { class: "nowrap" }, h("strong", {}, deviceName(d)), d.alias && d.hostname ? h("div", { class: "muted" }, d.hostname) : null),
  h("td", { class: "nowrap" }, d.id),
  h("td", { class: "nowrap" }, d.group),
  h("td", { class: "opt" }, d.os || h("span", { class: "muted" }, "Not reported")),
  h("td", { class: "opt" }, d.username || ""),
  h("td", { class: "opt nowrap" }, d.online ? (d.conns ? `${d.conns} session${d.conns > 1 ? "s" : ""}` : "") : ago(d.last_seen)),
  h("td", {}, d.unattended ? "" : h("span", { class: "tag warn", title: "The device has not reported its unattended-access key yet" }, "Needs password"))));

  const table = h("div", { class: "table-wrap" + (selectedId ? " compact" : "") }, h("table", {},
    h("thead", {}, h("tr", {}, h("th", {}, allBox), h("th", {}, "Status"), h("th", {}, "Name"), h("th", {}, "ID"),
      h("th", {}, "Location"), h("th", { class: "opt" }, "OS"), h("th", { class: "opt" }, "User"), h("th", { class: "opt" }, "Activity"), h("th", {}, ""))),
    h("tbody", {}, rows.length ? rows : h("tr", {}, h("td", { colspan: 9, class: "empty" },
      S.devices.length ? "No devices match." : "No devices yet. Create an enrolment token and install the client.")))));

  const actions = sel.length ? [
    h("span", { class: "muted" }, `${sel.length} selected`),
    h("button", { class: "btn primary", onclick: () => runDialog({ devices: sel }) }, "Run script"),
    h("button", { class: "btn", onclick: () => powerDialog({ devices: sel }) }, "Restart or shut down"),
    h("button", { class: "btn", onclick: () => moveDialog(sel) }, "Move"),
  ] : (typeof S.loc === "number" ? [
    h("button", { class: "btn", onclick: () => runDialog({ groups: [S.loc] }) }, "Run script on location"),
  ] : []);

  const body = [
    h("div", { class: "head" },
      h("div", {}, h("h1", {}, locName), h("div", { class: "sub" }, `${online} of ${list.length} online`)),
      h("div", { class: "spacer" }), ...actions, search),
  ];
  const detail = selectedId ? S.devices.find((d) => d.id === selectedId) : null;
  if (detail) body.push(h("div", { class: "split" }, table, deviceDetail(detail)));
  else body.push(table);
  return body;
}

function deviceDetail(d) {
  const panel = h("div", {});
  const alias = h("input", { type: "text", value: d.alias, placeholder: d.hostname });
  const note = h("input", { type: "text", value: d.note });
  const info = h("div", { class: "panel" },
    h("div", { class: "head" }, h("h2", {}, deviceName(d)), h("div", { class: "spacer" }),
      h("button", { class: "btn link", onclick: () => go("#/devices") }, "Close")),
    h("dl", { class: "facts" },
      h("dt", {}, "ID"), h("dd", {}, d.id),
      h("dt", {}, "Status"), h("dd", {}, d.online ? "Online" : `Offline, last seen ${ago(d.last_seen)}`),
      h("dt", {}, "Location"), h("dd", {}, d.group),
      h("dt", {}, "Computer"), h("dd", {}, d.hostname || "-"),
      h("dt", {}, "OS"), h("dd", {}, d.os || "-"),
      h("dt", {}, "Logged on"), h("dd", {}, d.username || "-"),
      h("dt", {}, "CPU"), h("dd", {}, d.cpu || "-"),
      h("dt", {}, "Memory"), h("dd", {}, d.memory || "-"),
      h("dt", {}, "Public IP"), h("dd", {}, d.ip || "-"),
      h("dt", {}, "Client"), h("dd", {}, (d.version || "-") + (d.build ? ` (build ${d.build})` : "")),
      h("dt", {}, "Enrolled"), h("dd", {}, when(d.created_at))),
    h("div", { class: "row" },
      h("button", { class: "btn primary", onclick: () => window.open("/web/#" + encodeURIComponent(d.id), "_blank") }, "Open in browser"),
      h("button", { class: "btn", onclick: () => runDialog({ devices: [d.id] }) }, "Run script"),
      h("button", { class: "btn", onclick: () => powerDialog({ devices: [d.id] }) }, "Restart or shut down"),
      h("button", { class: "btn", onclick: () => moveDialog([d.id]) }, "Move")));
  const edit = h("form", {
    class: "panel", onsubmit: async (e) => {
      e.preventDefault();
      await api("PATCH", "/api/admin/devices/" + d.id, { alias: alias.value, note: note.value });
      toast("Saved"); await loadFleet(); render();
    },
  },
  h("h2", {}, "Name and note"),
  h("label", { class: "field" }, h("span", {}, "Display name"), alias),
  h("label", { class: "field" }, h("span", {}, "Note"), note),
  h("div", { class: "row" }, h("button", { class: "btn primary", type: "submit" }, "Save"), h("div", { class: "spacer" }),
    h("button", {
      class: "btn danger", type: "button", onclick: async () => {
        if (!confirm(`Remove ${deviceName(d)} from RD? The client stays installed until you uninstall it.`)) return;
        await api("DELETE", "/api/admin/devices/" + d.id); toast("Device removed"); await loadFleet(); go("#/devices");
      },
    }, "Remove device")));
  const pol = h("div", { class: "panel" }, h("h2", {}, "Effective settings"), h("p", { class: "muted" }, "Loading"));
  Promise.all([api("GET", `/api/admin/devices/${d.id}/policy`), catalogue()]).then(([res, cat]) => {
    pol.replaceChildren(h("h2", {}, "Effective settings"), h("dl", { class: "facts" },
      cat.flatMap((s) => {
        const r = res[s.key];
        const v = r.value === "" ? describe(s, s.default) : describe(s, r.value);
        return [h("dt", {}, s.label), h("dd", {}, v, " ", h("span", { class: "muted" }, r.source ? `from ${r.source}` : "default"))];
      })));
  }).catch((e) => pol.replaceChildren(h("p", { class: "error" }, e.message)));
  const inv = h("div", { class: "panel" }, h("h2", {}, "Inventory"), h("p", { class: "muted" }, "Loading"));
  api("GET", `/api/admin/devices/${d.id}/inventory`).then((x) => inv.replaceChildren(...inventoryView(x, d)))
    .catch((e) => inv.replaceChildren(h("p", { class: "error" }, e.message)));
  panel.append(info, edit, inv, pol);
  return panel;
}

function gb(n) { return n ? (n / 1e9).toFixed(n >= 1e11 ? 0 : 1) + " GB" : "-"; }

// Inventory as the agent reports it: see client/src/rd/inventory.ps1.
function inventoryView(x, d) {
  const head = h("h2", {}, "Inventory");
  if (!x) return [head, h("p", { class: "muted" }, "No inventory yet. Clients from build 1 on report it a few minutes after starting, then every 6 hours.")];
  const out = [head, h("p", { class: "muted" }, `Reported ${ago(d.inventory_at)}`)];
  const facts = [];
  const fact = (k, v) => { if (v !== undefined && v !== null && v !== "") facts.push(h("dt", {}, k), h("dd", {}, String(v))); };
  fact("Manufacturer", [x.manufacturer, x.model].filter(Boolean).join(" "));
  fact("Serial number", x.serial);
  fact("Windows", [x.os_caption, x.os_build && `build ${x.os_build}`].filter(Boolean).join(", "));
  fact("Up since", x.boot_time && `${when(x.boot_time)} (${ago(x.boot_time).replace(" ago", "")})`);
  fact("Pending updates", x.pending_updates === undefined || x.pending_updates < 0 ? "" : x.pending_updates);
  fact("Reboot pending", x.reboot_pending === undefined ? "" : x.reboot_pending ? "Yes" : "No");
  out.push(h("dl", { class: "facts" }, facts));
  if (x.disks && x.disks.length) {
    out.push(h("div", { class: "table-wrap" }, h("table", {},
      h("thead", {}, h("tr", {}, h("th", {}, "Disk"), h("th", {}, "Size"), h("th", {}, "Free"), h("th", {}, ""))),
      h("tbody", {}, x.disks.map((k) => {
        const pct = k.size ? Math.round(k.free * 100 / k.size) : 0;
        return h("tr", {}, h("td", {}, [k.name, k.label].filter(Boolean).join(" ")), h("td", {}, gb(k.size)), h("td", {}, gb(k.free)),
          h("td", {}, h("span", { class: pct < 10 ? "error" : "muted" }, `${pct}% free`)));
      })))));
  }
  if (x.software && x.software.length) {
    const filter = h("input", { type: "search", placeholder: `Filter ${x.software.length} programs` });
    const body = h("tbody", {});
    const fill = () => {
      const q = filter.value.toLowerCase();
      body.replaceChildren(...x.software.filter((p) => !q || (p.name || "").toLowerCase().includes(q) || (p.publisher || "").toLowerCase().includes(q))
        .map((p) => h("tr", {}, h("td", {}, p.name), h("td", {}, p.version || ""), h("td", {}, p.publisher || ""))));
    };
    filter.addEventListener("input", fill);
    fill();
    out.push(h("details", {}, h("summary", {}, `Installed programs (${x.software.length})`),
      h("label", { class: "field" }, h("span", {}, "Filter"), filter),
      h("div", { class: "table-wrap" }, h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, "Name"), h("th", {}, "Version"), h("th", {}, "Publisher"))), body))));
  }
  return out;
}

// ---------- updates ----------

async function updatesView() {
  const res = await api("GET", "/api/admin/releases");
  const rels = res.releases || [];
  const file = h("input", { type: "file", accept: ".zip,application/zip", required: true });
  const status = h("p", { class: "muted" });
  const upload = h("form", {
    class: "panel", onsubmit: async (e) => {
      e.preventDefault();
      const f = file.files[0];
      if (!f) return;
      status.textContent = `Uploading ${f.name} (${(f.size / 1e6).toFixed(0)} MB)…`;
      const r = await fetch("/api/admin/releases", { method: "POST", headers: { Authorization: "Bearer " + S.token, "Content-Type": "application/zip" }, body: f });
      const data = await r.json().catch(() => ({}));
      if (!r.ok) { status.textContent = data.error || `Upload failed (HTTP ${r.status})`; status.className = "error"; return; }
      toast(`Build ${data.build} uploaded`); render();
    },
  },
  h("h2", {}, "Upload a build"),
  h("p", { class: "muted" }, "Download rd-windows-x64.zip from the Windows client run in GitHub Actions and upload it here unchanged."),
  h("label", { class: "field" }, h("span", {}, "rd-windows-x64.zip"), file),
  h("button", { class: "btn primary", type: "submit" }, "Upload"), status);

  const current = rels.find((r) => r.current);
  const rows = rels.map((r) => h("tr", {},
    h("td", {}, `Build ${r.build}`, r.current ? h("span", { class: "pill" }, " Rolling out") : null),
    h("td", {}, r.version || "-"),
    h("td", {}, when(r.created_at)),
    h("td", { class: "num" }, `${r.devices}`),
    h("td", { class: "num" },
      r.current
        ? h("button", { class: "btn", onclick: async () => { await api("PUT", "/api/admin/releases/current", { build: 0 }); toast("Roll-out stopped"); render(); } }, "Stop roll-out")
        : h("button", {
          class: "btn primary", onclick: async () => {
            if (!confirm(`Update every online device on an older build to build ${r.build}? Each device restarts its RD service; open sessions to it drop for about a minute.`)) return;
            await api("PUT", "/api/admin/releases/current", { build: r.build }); toast(`Rolling out build ${r.build}`); render();
          },
        }, "Roll out"), " ",
      r.current ? null : h("button", {
        class: "btn link", onclick: async () => {
          if (!confirm(`Delete build ${r.build}?`)) return;
          try { await api("DELETE", "/api/admin/releases/" + r.build); render(); } catch (ex) { alert(ex.message); }
        },
      }, "Delete"))));
  const byBuild = {};
  for (const d of S.devices) byBuild[d.build || 0] = (byBuild[d.build || 0] || 0) + 1;
  const behind = current ? S.devices.filter((d) => d.build && d.build < current.build) : [];
  const list = h("div", { class: "panel" }, h("h2", {}, "Builds"),
    current ? h("p", {}, `Rolling out build ${current.build}: ${behind.length} device${behind.length === 1 ? "" : "s"} still on an older build. Online devices get the update within a few minutes; offline ones when they come back.`)
      : h("p", { class: "muted" }, "No roll-out. Devices keep their build until you roll one out."),
    res.unknown_build_devices ? h("p", { class: "muted" }, `${res.unknown_build_devices} device(s) run a client from before auto-update and need install.ps1 once more.`) : null,
    h("div", { class: "table-wrap" }, h("table", {},
      h("thead", {}, h("tr", {}, h("th", {}, "Build"), h("th", {}, "Version"), h("th", {}, "Uploaded"), h("th", { class: "num" }, "Devices on it"), h("th", {}, ""))),
      h("tbody", {}, rows.length ? rows : h("tr", {}, h("td", { colspan: 5, class: "empty" }, "No builds uploaded yet."))))));
  void byBuild;
  return [
    h("div", { class: "head" }, h("div", {}, h("h1", {}, "Updates"), h("div", { class: "sub" }, "Upload client builds and roll them out to your devices"))),
    h("div", { class: "split" }, upload, list),
  ];
}

let catCache = null;
async function catalogue() { return catCache || (catCache = await api("GET", "/api/admin/policy/catalogue")); }
function describe(s, v) {
  if (s.kind === "bool") return v === "Y" ? "On" : "Off";
  if (v === "") return "None";
  return v;
}

// ---------- dialogs ----------

function dialog(title, body, submitLabel, onsubmit) {
  const err = h("p", { class: "error", role: "alert" });
  const submit = h("button", { class: "btn primary", type: "submit" }, submitLabel);
  const dlg = h("dialog", {}, h("form", {
    onsubmit: async (e) => {
      e.preventDefault(); err.textContent = ""; submit.disabled = true;
      try { if ((await onsubmit()) !== false) dlg.close(); } catch (ex) { err.textContent = ex.message; } finally { submit.disabled = false; }
    },
  }, h("h2", {}, title), h("div", { style: "margin-top:12px" }, body), err,
  h("div", { class: "actions" }, h("button", { class: "btn", type: "button", onclick: () => dlg.close() }, "Cancel"), submit)));
  dlg.addEventListener("close", () => dlg.remove());
  document.body.append(dlg);
  dlg.showModal();
  return dlg;
}

function targetText(t) {
  if (t.devices?.length) return t.devices.length === 1 ? deviceName(S.devices.find((d) => d.id === t.devices[0]) || { id: t.devices[0] }) : `${t.devices.length} devices`;
  if (t.groups?.length) return t.groups.map((g) => groupById(g)?.path).join(", ") + " (including sublocations)";
  return "";
}

function scheduleFields() {
  const start = h("select", {},
    h("option", { value: "0" }, "Now"), h("option", { value: "15" }, "In 15 minutes"), h("option", { value: "60" }, "In 1 hour"),
    h("option", { value: "240" }, "In 4 hours"), h("option", { value: "720" }, "In 12 hours"));
  const deadline = h("select", {},
    h("option", { value: "1" }, "1 hour"), h("option", { value: "24", selected: true }, "24 hours"), h("option", { value: "168" }, "7 days"));
  return {
    el: h("div", { class: "row" },
      h("label", { class: "field" }, h("span", {}, "Start"), start),
      h("label", { class: "field" }, h("span", {}, "Offline devices run it if back within"), deadline)),
    values: () => ({ start_in_minutes: Number(start.value), deadline_hours: Number(deadline.value) }),
  };
}

async function runDialog(target) {
  const scripts = (await api("GET", "/api/admin/scripts")) || [];
  let mode = scripts.length ? "saved" : "inline";
  const pick = h("select", {}, scripts.map((s) => h("option", { value: s.id }, s.name)));
  const shellSel = h("select", {}, h("option", { value: "powershell" }, "PowerShell"), h("option", { value: "cmd" }, "Command prompt"));
  const runAs = h("select", {}, h("option", { value: "system" }, "SYSTEM"), h("option", { value: "user" }, "Logged-on user"));
  const timeout = h("input", { type: "number", min: "1", max: "86400", value: "300" });
  const body = h("textarea", { spellcheck: "false", placeholder: "Get-Service | Where-Object Status -eq Stopped" });
  const saved = h("div", {}, h("label", { class: "field" }, h("span", {}, "Script"), pick));
  const inline = h("div", {},
    h("div", { class: "row" },
      h("label", { class: "field" }, h("span", {}, "Shell"), shellSel),
      h("label", { class: "field" }, h("span", {}, "Run as"), runAs),
      h("label", { class: "field" }, h("span", {}, "Timeout (seconds)"), timeout)),
    h("label", { class: "field" }, h("span", {}, "Script"), body));
  const bSaved = h("button", { type: "button", class: "btn", onclick: () => setMode("saved") }, "Saved script");
  const bInline = h("button", { type: "button", class: "btn", onclick: () => setMode("inline") }, "Write one now");
  function setMode(m) {
    mode = m; saved.hidden = m !== "saved"; inline.hidden = m !== "inline";
    bSaved.setAttribute("aria-pressed", String(m === "saved")); bInline.setAttribute("aria-pressed", String(m === "inline"));
  }
  bSaved.disabled = !scripts.length;
  setMode(mode);
  const sched = scheduleFields();
  dialog("Run script", [h("p", {}, "On: ", h("strong", {}, targetText(target))), h("div", { class: "choice" }, bSaved, bInline), saved, inline, sched.el],
    "Run script", async () => {
      const req = { ...target, ...sched.values() };
      if (mode === "saved") req.script_id = Number(pick.value);
      else Object.assign(req, { shell: shellSel.value, run_as: runAs.value, timeout_sec: Number(timeout.value), body: body.value });
      const res = await api("POST", "/api/admin/jobs", req);
      toast(`Sent to ${res.devices} device${res.devices > 1 ? "s" : ""}`);
      go("#/jobs/" + res.job.id);
    });
}

function powerDialog(target) {
  const action = h("select", {},
    h("option", { value: "reboot" }, "Restart"),
    h("option", { value: "reboot-safe-mode" }, "Restart into Safe Mode with networking"),
    h("option", { value: "shutdown" }, "Shut down"),
    h("option", { value: "logoff" }, "Log off the console user"));
  const sched = scheduleFields();
  dialog("Restart or shut down", [h("p", {}, "On: ", h("strong", {}, targetText(target))),
    h("label", { class: "field" }, h("span", {}, "Action"), action), sched.el,
    h("p", { class: "muted" }, "Shut down cannot be undone remotely unless Wake-on-LAN or out-of-band access is available.")],
  "Send", async () => {
    const res = await api("POST", "/api/admin/jobs", { ...target, action: action.value, ...sched.values() });
    toast(`Sent to ${res.devices} device${res.devices > 1 ? "s" : ""}`);
    go("#/jobs/" + res.job.id);
  });
}

function moveDialog(ids) {
  const opts = [h("option", { value: "" }, "Unassigned"), ...S.groups.map((g) => h("option", { value: g.path }, g.path))];
  const sel = h("select", {}, opts);
  const fresh = h("input", { type: "text", placeholder: "Or a new location, e.g. Office/Rack 2" });
  dialog(`Move ${ids.length === 1 ? deviceName(S.devices.find((d) => d.id === ids[0])) : ids.length + " devices"}`,
    [h("label", { class: "field" }, h("span", {}, "Location"), sel), h("label", { class: "field" }, h("span", {}, "New location"), fresh)],
    "Move", async () => {
      const group = fresh.value.trim() || sel.value;
      for (const id of ids) await api("PATCH", "/api/admin/devices/" + id, { group });
      toast("Moved"); S.selected.clear(); await loadFleet(); render();
    });
}

// ---------- jobs ----------

async function jobsView() {
  const jobs = (await api("GET", "/api/admin/jobs?limit=200")) || [];
  const rows = jobs.map((j) => {
    const c = j.counts || {};
    const total = Object.values(c).reduce((a, b) => a + b, 0);
    const pending = (c.queued || 0) + (c.sent || 0);
    return h("tr", { class: "click", onclick: () => go("#/jobs/" + j.id) },
      h("td", {}, h("strong", {}, j.name), h("div", { class: "muted" }, j.kind === "power" ? "Power action" : j.shell === "cmd" ? "Command prompt" : "PowerShell")),
      h("td", {}, j.target),
      h("td", {}, when(j.created_at), new Date(j.not_before) > new Date() ? h("div", { class: "muted" }, "starts " + ago(j.not_before)) : null),
      h("td", { class: "num" }, String(c.success || 0)),
      h("td", { class: "num" }, c.failed ? h("strong", { style: "color:var(--bad)" }, String(c.failed)) : "0"),
      h("td", { class: "num" }, String(pending)),
      h("td", { class: "num" }, String(total)));
  });
  return [
    h("div", { class: "head" }, h("div", {}, h("h1", {}, "Jobs"), h("div", { class: "sub" }, "Scripts and power actions sent to devices")),
      h("div", { class: "spacer" }), h("button", { class: "btn primary", onclick: () => go("#/devices") }, "Pick devices to run on")),
    h("div", { class: "table-wrap" }, h("table", {},
      h("thead", {}, h("tr", {}, h("th", {}, "Job"), h("th", {}, "Target"), h("th", {}, "Created"),
        h("th", { class: "num" }, "Done"), h("th", { class: "num" }, "Failed"), h("th", { class: "num" }, "Waiting"), h("th", { class: "num" }, "Devices"))),
      h("tbody", {}, rows.length ? rows : h("tr", {}, h("td", { colspan: 7, class: "empty" }, "No jobs yet. Select devices and choose Run script."))))),
  ];
}

async function jobView(id) {
  const { job, runs } = await api("GET", "/api/admin/jobs/" + id);
  const byId = Object.fromEntries(S.devices.map((d) => [d.id, d]));
  const pending = runs.some((r) => r.status === "queued" || r.status === "sent");
  const rows = runs.flatMap((r) => {
    const d = byId[r.device_id];
    const [kind, label] = runStatus[r.status] || ["", r.status];
    const row = h("tr", {},
      h("td", {}, h("strong", {}, d ? deviceName(d) : r.device_id), h("div", { class: "muted" }, d ? d.group : "Removed device")),
      h("td", {}, statusDot(kind, label)),
      h("td", {}, r.exit_code == null ? "" : String(r.exit_code)),
      h("td", {}, r.finished_at ? when(r.finished_at) : r.sent_at ? "started " + ago(r.sent_at) : (d && !d.online ? "waiting for device to come online" : "")));
    const out = r.output ? h("tr", {}, h("td", { colspan: 4 }, h("pre", { class: "out" }, r.output))) : null;
    return [row, out];
  });
  const meta = h("dl", { class: "facts" },
    h("dt", {}, "Target"), h("dd", {}, job.target),
    h("dt", {}, "Created"), h("dd", {}, `${when(job.created_at)} by ${job.created_by}`),
    h("dt", {}, "Starts"), h("dd", {}, when(job.not_before)),
    h("dt", {}, "Runs until"), h("dd", {}, when(job.deadline)),
    job.kind === "script" ? [h("dt", {}, "Runs as"), h("dd", {}, job.run_as === "user" ? "Logged-on user" : "SYSTEM"),
      h("dt", {}, "Timeout"), h("dd", {}, `${job.timeout_sec} s`)] : []);
  return [
    h("div", { class: "head" },
      h("div", {}, h("h1", {}, job.name), h("div", { class: "sub" }, job.kind === "power" ? "Power action" : job.shell === "cmd" ? "Command prompt script" : "PowerShell script")),
      h("div", { class: "spacer" }),
      pending ? h("button", {
        class: "btn danger", onclick: async () => {
          if (!confirm("Cancel this job for devices that have not received it yet?")) return;
          await api("POST", `/api/admin/jobs/${job.id}/cancel`); toast("Cancelled"); render();
        },
      }, "Cancel waiting devices") : null,
      h("button", { class: "btn", onclick: () => go("#/jobs") }, "All jobs")),
    h("div", { class: "split" },
      h("div", { class: "table-wrap" }, h("table", {},
        h("thead", {}, h("tr", {}, h("th", {}, "Device"), h("th", {}, "Status"), h("th", {}, "Exit code"), h("th", {}, "When"))),
        h("tbody", {}, rows))),
      h("div", {}, h("div", { class: "panel" }, h("h2", {}, "Details"), meta,
        job.body ? h("pre", { class: "out" }, job.body) : null))),
  ];
}

// ---------- scripts ----------

async function scriptsView(id) {
  const scripts = (await api("GET", "/api/admin/scripts")) || [];
  const current = id === "new" ? { name: "", description: "", shell: "powershell", run_as: "system", timeout_sec: 300, body: "" }
    : scripts.find((s) => String(s.id) === id);
  const list = h("div", { class: "table-wrap" }, h("table", {},
    h("thead", {}, h("tr", {}, h("th", {}, "Script"), h("th", {}, "Shell"), h("th", {}, "Changed"))),
    h("tbody", {}, scripts.length ? scripts.map((s) => h("tr", { class: "click" + (String(s.id) === id ? " sel" : ""), onclick: () => go("#/scripts/" + s.id) },
      h("td", {}, h("strong", {}, s.name), s.description ? h("div", { class: "muted" }, s.description) : null),
      h("td", {}, s.shell === "cmd" ? "Command prompt" : "PowerShell"),
      h("td", {}, ago(s.updated_at)))) : h("tr", {}, h("td", { colspan: 3, class: "empty" }, "No saved scripts. Save the ones you run often.")))));
  const out = [h("div", { class: "head" }, h("div", {}, h("h1", {}, "Scripts"), h("div", { class: "sub" }, "Saved scripts you can run on any device or location")),
    h("div", { class: "spacer" }), h("button", { class: "btn primary", onclick: () => go("#/scripts/new") }, "New script"))];
  if (!current) { out.push(list); return out; }

  const name = h("input", { type: "text", required: true, value: current.name });
  const desc = h("input", { type: "text", value: current.description });
  const shellSel = h("select", {}, h("option", { value: "powershell" }, "PowerShell"), h("option", { value: "cmd" }, "Command prompt"));
  shellSel.value = current.shell;
  const runAs = h("select", {}, h("option", { value: "system" }, "SYSTEM"), h("option", { value: "user" }, "Logged-on user"));
  runAs.value = current.run_as;
  const timeout = h("input", { type: "number", min: "1", max: "86400", value: String(current.timeout_sec) });
  const body = h("textarea", { spellcheck: "false", required: true }, current.body);
  const err = h("p", { class: "error", role: "alert" });
  const form = h("form", {
    class: "panel", onsubmit: async (e) => {
      e.preventDefault(); err.textContent = "";
      const payload = { name: name.value, description: desc.value, shell: shellSel.value, run_as: runAs.value, timeout_sec: Number(timeout.value), body: body.value };
      try {
        const saved = current.id ? await api("PUT", "/api/admin/scripts/" + current.id, payload) : await api("POST", "/api/admin/scripts", payload);
        toast("Script saved"); go("#/scripts/" + saved.id);
      } catch (ex) { err.textContent = ex.message; }
    },
  },
  h("h2", {}, current.id ? "Edit script" : "New script"),
  h("label", { class: "field" }, h("span", {}, "Name"), name),
  h("label", { class: "field" }, h("span", {}, "Description"), desc),
  h("div", { class: "row" },
    h("label", { class: "field" }, h("span", {}, "Shell"), shellSel),
    h("label", { class: "field" }, h("span", {}, "Run as"), runAs),
    h("label", { class: "field" }, h("span", {}, "Timeout (seconds)"), timeout)),
  h("label", { class: "field" }, h("span", {}, "Script"), body), err,
  h("div", { class: "row" }, h("button", { class: "btn primary", type: "submit" }, "Save script"),
    h("button", { class: "btn", type: "button", onclick: () => go("#/scripts") }, "Close"),
    h("div", { class: "spacer" }),
    current.id ? h("button", {
      class: "btn danger", type: "button", onclick: async () => {
        if (!confirm(`Delete the script "${current.name}"? Jobs already sent keep their copy.`)) return;
        await api("DELETE", "/api/admin/scripts/" + current.id); toast("Script deleted"); go("#/scripts");
      },
    }, "Delete script") : null));
  out.push(h("div", { class: "split" }, form, list));
  return out;
}

// ---------- settings (policy) ----------

async function settingsView(scope) {
  const [cat, pols] = await Promise.all([catalogue(), api("GET", "/api/admin/policies")]);
  scope = scope || "account";
  const cur = pols.find((p) => p.scope === scope) || pols[0];
  const scopeSel = h("select", { "aria-label": "Apply to", onchange: (e) => go("#/settings/" + e.target.value) },
    pols.map((p) => h("option", { value: p.scope, selected: p.scope === cur.scope }, p.scope === "account" ? "Whole account" : p.name)));
  const inputs = {};
  const groups = [...new Set(cat.map((s) => s.group))];
  const sections = groups.map((gname) => h("section", { class: "panel settings-group" }, h("h2", {}, gname),
    cat.filter((s) => s.group === gname).map((s) => {
      const v = cur.values[s.key] ?? "";
      const inherit = cur.scope === "account" ? `Default (${describe(s, s.default)})` : "Same as parent";
      let input;
      if (s.kind === "bool") {
        input = h("select", { "aria-label": s.label }, h("option", { value: "" }, inherit), h("option", { value: "Y" }, "On"), h("option", { value: "N" }, "Off"));
        input.value = v;
      } else {
        input = h("input", { type: s.kind === "number" ? "number" : "text", value: v, placeholder: inherit, "aria-label": s.label, min: s.kind === "number" ? "1" : undefined });
      }
      inputs[s.key] = input;
      return h("div", { class: "setting" }, h("div", {}, h("div", {}, h("strong", {}, s.label)), h("div", { class: "desc" }, s.description)), input);
    })));
  const err = h("p", { class: "error", role: "alert" });
  const form = h("form", {
    onsubmit: async (e) => {
      e.preventDefault(); err.textContent = "";
      const values = {};
      for (const [k, el] of Object.entries(inputs)) if (el.value.trim() !== "") values[k] = el.value.trim();
      try { await api("PUT", "/api/admin/policies/" + cur.scope, { values }); toast("Settings saved. Devices pick them up within a minute."); render(); }
      catch (ex) { err.textContent = ex.message; }
    },
  }, sections, err, h("button", { class: "btn primary", type: "submit" }, "Save settings"));
  return [
    h("div", { class: "head" }, h("div", {}, h("h1", {}, "Settings"),
      h("div", { class: "sub" }, cur.scope === "account" ? "Apply to every device unless a location overrides them" : `Override the account settings for ${cur.name} and its sublocations`)),
    h("div", { class: "spacer" }), h("label", { class: "row" }, h("span", { class: "muted" }, "Apply to"), scopeSel)),
    form,
  ];
}

// ---------- enrolment ----------

async function enrolView() {
  const tokens = (await api("GET", "/api/admin/tokens")) || [];
  const now = Date.now();
  const group = h("input", { type: "text", list: "locs", placeholder: "Leave empty to choose at install" });
  const datalist = h("datalist", { id: "locs" }, S.groups.map((g) => h("option", { value: g.path })));
  const maxUses = h("input", { type: "number", min: "0", value: "10" });
  const expires = h("select", {}, h("option", { value: "24" }, "1 day"), h("option", { value: "72", selected: true }, "3 days"),
    h("option", { value: "168" }, "7 days"), h("option", { value: "720" }, "30 days"), h("option", { value: "0" }, "Never"));
  const note = h("input", { type: "text", placeholder: "e.g. Office servers" });
  const result = h("div", {});
  const create = h("form", {
    class: "panel", onsubmit: async (e) => {
      e.preventDefault();
      const t = await api("POST", "/api/admin/tokens", { group: group.value.trim(), max_uses: Number(maxUses.value), expires_in_hours: Number(expires.value), note: note.value });
      const cmd = `powershell -ExecutionPolicy Bypass -File install.ps1 -Token ${t.token}` + (t.group ? "" : ` -Group "Office"`);
      const copy = (text) => h("button", { class: "btn", type: "button", onclick: () => navigator.clipboard.writeText(text).then(() => toast("Copied")) }, "Copy");
      result.replaceChildren(
        h("p", {}, h("strong", {}, "Token created. It is shown only once.")),
        h("div", { class: "secret" }, h("span", { class: "mono", style: "flex:1" }, t.token), copy(t.token)),
        h("p", { class: "muted" }, "Install command (run as Administrator next to install.ps1):"),
        h("div", { class: "secret" }, h("span", { class: "mono", style: "flex:1" }, cmd), copy(cmd)));
      await loadFleet();
      const list = document.getElementById("token-list");
      if (list) list.replaceWith(await tokenTable());
    },
  },
  h("h2", {}, "New enrolment token"),
  h("div", { class: "row" },
    h("label", { class: "field" }, h("span", {}, "Location"), group, datalist),
    h("label", { class: "field" }, h("span", {}, "Devices (0 = no limit)"), maxUses),
    h("label", { class: "field" }, h("span", {}, "Valid for"), expires)),
  h("label", { class: "field" }, h("span", {}, "Note"), note),
  h("button", { class: "btn primary", type: "submit" }, "Create token"), result);

  async function tokenTable() {
    const ts = (await api("GET", "/api/admin/tokens")) || [];
    return h("div", { class: "table-wrap", id: "token-list" }, h("table", {},
      h("thead", {}, h("tr", {}, h("th", {}, "Location"), h("th", {}, "Used"), h("th", {}, "Expires"), h("th", {}, "Note"), h("th", {}, ""))),
      h("tbody", {}, ts.length ? ts.map((t) => {
        const expired = t.expires_at && new Date(t.expires_at).getTime() <= now;
        const used = t.max_uses && t.uses >= t.max_uses;
        return h("tr", {},
          h("td", {}, t.group || h("span", { class: "muted" }, "Chosen at install")),
          h("td", {}, t.max_uses ? `${t.uses} of ${t.max_uses}` : `${t.uses}`),
          h("td", {}, expired ? "Expired" : t.expires_at ? when(t.expires_at) : "Never", used ? h("div", { class: "muted" }, "Used up") : null),
          h("td", {}, t.note),
          h("td", { class: "num" }, h("button", {
            class: "btn danger", onclick: async () => {
              if (!confirm("Delete this token? Devices already enrolled are not affected.")) return;
              await api("DELETE", "/api/admin/tokens/" + t.id); render();
            },
          }, "Delete")));
      }) : h("tr", {}, h("td", { colspan: 5, class: "empty" }, "No tokens. Create one to install the client on new machines.")))));
  }

  const newLoc = h("input", { type: "text", placeholder: "e.g. Office/Rack 2", required: true });
  const locations = h("div", { class: "panel" }, h("h2", {}, "Locations"),
    h("form", {
      class: "row", onsubmit: async (e) => {
        e.preventDefault(); await api("POST", "/api/admin/groups", { path: newLoc.value }); toast("Location added"); await loadFleet(); render();
      },
    }, h("label", { class: "field" }, h("span", {}, "New location"), newLoc), h("button", { class: "btn", type: "submit" }, "Add location")),
    h("div", { class: "table-wrap" }, h("table", {}, h("tbody", {}, S.groups.map((g) => h("tr", {},
      h("td", {}, g.path),
      h("td", { class: "num" }, `${devicesIn(g.id).length} devices`),
      h("td", { class: "num" },
        h("button", {
          class: "btn link", onclick: async () => {
            const n = prompt("New name for " + g.name, g.name);
            if (!n || n === g.name) return;
            try { await api("PATCH", "/api/admin/groups/" + g.id, { name: n }); await loadFleet(); render(); } catch (ex) { alert(ex.message); }
          },
        }, "Rename"), " ",
        h("button", {
          class: "btn link", onclick: async () => {
            if (!confirm(`Delete the location ${g.path}?`)) return;
            try { await api("DELETE", "/api/admin/groups/" + g.id); await loadFleet(); render(); } catch (ex) { alert(ex.message); }
          },
        }, "Delete"))))))));
  void tokens;
  return [
    h("div", { class: "head" }, h("div", {}, h("h1", {}, "Enrolment"), h("div", { class: "sub" }, "Tokens for installing the client, and your locations"))),
    h("div", { class: "split" }, h("div", {}, create, h("h2", { style: "margin:20px 0 10px" }, "Tokens"), await tokenTable()), locations),
  ];
}

// ---------- router ----------

let renderSeq = 0;
async function render() {
  if (!S.token) return renderLogin();
  const seq = ++renderSeq;
  const [, section = "devices", arg] = (location.hash || "#/devices").split("/");
  clearInterval(S.timer);
  try {
    if (!S.devices.length && !S.groups.length || section === "devices" || section === "updates") await loadFleet();
    let content;
    switch (section) {
      case "jobs": content = arg ? await jobView(arg) : await jobsView(); break;
      case "scripts": content = await scriptsView(arg); break;
      case "settings": content = await settingsView(arg); break;
      case "enrol": content = await enrolView(); break;
      case "updates": content = await updatesView(); break;
      default: content = devicesView(arg ? decodeURIComponent(arg) : null);
    }
    if (seq !== renderSeq) return;
    const app = document.getElementById("app");
    const scroll = document.getElementById("main")?.scrollTop || 0;
    app.replaceChildren(shell(section, content));
    document.getElementById("main").scrollTop = scroll;
    // live refresh where it matters, without stealing focus from inputs
    if (section === "devices" || (section === "jobs" && arg)) {
      S.timer = setInterval(() => {
        const a = document.activeElement;
        if (document.querySelector("dialog[open]") || (a && /INPUT|TEXTAREA|SELECT/.test(a.tagName))) return;
        render();
      }, section === "devices" ? 15000 : 5000);
    }
  } catch (ex) {
    if (ex.status === 401) return;
    document.getElementById("app").replaceChildren(shell(section, [h("p", { class: "error" }, ex.message)]));
  }
}

window.addEventListener("hashchange", render);
render();
