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
    logout("Your session ended. Sign in again.");
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

function brand(sub) {
  return h("div", { class: "brand" }, h("img", { class: "brand-mark", src: "/static/mark.svg", alt: "" }), "RD", h("span", { class: "sub" }, sub));
}

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
  const code = h("input", { type: "text", class: "code", inputmode: "numeric", autocomplete: "one-time-code", pattern: "[0-9]{6}", maxlength: "6" });
  const remember = h("input", { type: "checkbox" });
  const codeField = h("div", { hidden: true },
    h("label", { class: "field" }, h("span", {}, "Authenticator code"), code),
    h("label", { class: "check" }, remember, "Remember this browser for 30 days"),
    h("p", { class: "hint" }, "On a remembered browser you sign in with your password only. Changing your password or authenticator ends it."));
  const submit = h("button", { class: "btn primary", type: "submit" }, "Continue");

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
          submit.textContent = "Sign in";
          code.focus();
          return;
        }
      } else {
        res = await api("POST", "/api/login", { username: user.value, tfaCode: code.value, secret, type: "email_code", remember: remember.checked });
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
      brand("Console"),
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

const TABS = [["devices", "Devices"], ["jobs", "Jobs"], ["scripts", "Scripts"], ["vars", "Variables"], ["settings", "Settings"], ["updates", "Updates"], ["enrol", "Enrolment"]];

function shell(section, content) {
  const tabs = h("nav", { class: "tabs", "aria-label": "Sections" },
    TABS.map(([id, label]) => h("a", { href: "#/" + id, "aria-current": section === id ? "page" : false }, label)));
  return h("div", { class: "shell" },
    h("header", { class: "top" },
      brand("Console"),
      tabs,
      h("div", { class: "top-right" },
        h("span", {}, S.user),
        h("button", {
          class: "btn link", title: "Ask for the authenticator code again at the next sign-in from this browser",
          onclick: async () => { await api("POST", "/api/forget-browser"); toast("This browser is no longer remembered"); },
        }, "Forget this browser"),
        h("button", { class: "btn", onclick: () => logout() }, "Sign out"))),
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
  const showSync = S.devices.some((d) => d.sync && d.sync.level !== "none");

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
  h("td", {}, storageCell(d)),
  showSync ? h("td", {}, syncCell(d)) : null,
  h("td", { class: "opt" }, d.os || h("span", { class: "muted" }, "Not reported")),
  h("td", { class: "opt" }, d.username || ""),
  h("td", { class: "opt nowrap" }, d.online ? (d.conns ? `${d.conns} session${d.conns > 1 ? "s" : ""}` : "") : ago(d.last_seen)),
  h("td", {}, d.unattended ? "" : h("span", { class: "tag warn", title: "The device has not reported its unattended-access key yet" }, "Needs password"))));

  const table = h("div", { class: "table-wrap" + (selectedId ? " compact" : "") }, h("table", {},
    h("thead", {}, h("tr", {}, h("th", {}, allBox), h("th", {}, "Status"), h("th", {}, "Name"), h("th", {}, "ID"),
      h("th", {}, "Location"), h("th", {}, "Storage (free / total)"), showSync ? h("th", {}, "Syncthing") : null, h("th", { class: "opt" }, "OS"), h("th", { class: "opt" }, "User"), h("th", { class: "opt" }, "Activity"), h("th", {}, ""))),
    h("tbody", {}, rows.length ? rows : h("tr", {}, h("td", { colspan: showSync ? 11 : 10, class: "empty" },
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
  const vars = h("div", { class: "panel" }, h("h2", {}, "Variables"), h("p", { class: "muted" }, "Loading"));
  api("GET", "/api/admin/vars").then((v) => vars.replaceChildren(...deviceVarsView(d, v)))
    .catch((e) => vars.replaceChildren(h("p", { class: "error" }, e.message)));
  const hist = h("div", { class: "panel" }, h("h2", {}, "Script history"), h("p", { class: "muted" }, "Loading"));
  api("GET", `/api/admin/devices/${d.id}/runs`).then((runs) => hist.replaceChildren(h("h2", {}, "Script history"), ...runHistory(runs)))
    .catch((e) => hist.replaceChildren(h("p", { class: "error" }, e.message)));
  const sync = d.sync && d.sync.level !== "none" ? h("div", { class: "panel" }, h("h2", {}, "Syncthing"), h("p", { class: "muted" }, "Loading")) : null;
  if (sync) api("GET", `/api/admin/devices/${d.id}/sync`).then((x) => sync.replaceChildren(...syncView(x)))
    .catch((e) => sync.replaceChildren(h("p", { class: "error" }, e.message)));
  panel.append(info, ...(sync ? [sync] : []), edit, vars, hist, inv, pol);
  return panel;
}

// ---------- variables ----------

const BUILTINS = [
  ["RD_DEVICE_NAME", "Display name, or the computer name"], ["RD_HOSTNAME", "Computer name"],
  ["RD_DEVICE_ID", "RD ID"], ["RD_LOCATION", "Location path"], ["RD_PUBLIC_IP", "Public IP of the last heartbeat"],
];
const VAR_NAME = /^[A-Za-z_][A-Za-z0-9_]{0,63}$/;

// Values a location passes down: its ancestors first, nearest wins.
function inheritedVars(groupId, groupVars) {
  const chain = [];
  for (let g = groupById(groupId); g && chain.length < 64; g = g.parent_id != null ? groupById(g.parent_id) : null) chain.unshift(g);
  const out = {};
  for (const g of chain) for (const [k, v] of Object.entries(groupVars[g.id] || {})) out[k] = { value: v, from: g.path };
  return out;
}

function deviceVarsView(d, v) {
  const own = (v.devices || {})[d.id] || {};
  const inh = d.group_id != null ? inheritedVars(d.group_id, v.groups || {}) : {};
  const names = [...new Set([...Object.keys(inh), ...Object.keys(own)])].sort((a, b) => a.localeCompare(b));
  const rows = names.map((n) => h("tr", {},
    h("td", { class: "mono" }, n),
    h("td", {}, own[n] ?? inh[n].value),
    h("td", { class: "muted" }, own[n] !== undefined ? (inh[n] ? `this device (overrides ${inh[n].from})` : "this device") : `from ${inh[n].from}`)));
  return [
    h("div", { class: "head" }, h("h2", {}, "Variables"), h("div", { class: "spacer" }),
      h("button", { class: "btn small", onclick: () => varsDialog(`Variables for ${deviceName(d)}`, own, inh, (vals) => api("PUT", "/api/admin/vars", { devices: { [d.id]: vals } })) }, "Edit")),
    names.length ? h("div", { class: "table-wrap" }, h("table", {}, h("tbody", {}, rows)))
      : h("p", { class: "muted" }, "None set. Scripts still get the built-in RD_ variables."),
  ];
}

// Name/value rows with add and remove. inherited: {name: {value, from}} shown as placeholders.
function kvEditor(values, inherited = {}) {
  const box = h("div", { class: "kv" }, h("span", { class: "kh" }, "Name"), h("span", { class: "kh" }, "Value"), h("span", {}));
  const rows = [];
  const add = (k = "", v = "") => {
    const name = h("input", { type: "text", value: k, placeholder: "IP", "aria-label": "Variable name", spellcheck: "false" });
    const val = h("input", { type: "text", value: v, "aria-label": "Value", spellcheck: "false" });
    const hint = () => { const i = inherited[name.value.trim()]; val.placeholder = i ? `${i.value} (from ${i.from})` : ""; };
    name.addEventListener("input", hint); hint();
    const row = { name, val };
    const x = h("button", { type: "button", class: "x", title: "Remove", "aria-label": "Remove", onclick: () => { name.remove(); val.remove(); x.remove(); rows.splice(rows.indexOf(row), 1); } }, "×");
    rows.push(row);
    box.append(name, val, x);
  };
  for (const [k, v] of Object.entries(values)) add(k, v);
  for (const k of Object.keys(inherited)) if (!(k in values)) add(k, "");
  if (!rows.length) add();
  return {
    el: h("div", {}, box, h("button", { type: "button", class: "btn small", onclick: () => { add(); rows[rows.length - 1].name.focus(); } }, "Add variable")),
    values() {
      const out = {};
      for (const { name, val } of rows) {
        const k = name.value.trim();
        if (!k && !val.value) continue;
        if (!VAR_NAME.test(k)) throw new Error(`"${k}" is not a valid name: letters, digits and _, starting with a letter`);
        if (k in out) throw new Error(`${k} is listed twice`);
        if (val.value !== "") out[k] = val.value;
      }
      return out;
    },
  };
}

function varsDialog(title, own, inherited, save) {
  const ed = kvEditor(own, inherited);
  dialog(title, [ed.el, h("p", { class: "hint" }, "Leave a value empty to use the inherited one. Scripts get each variable as ",
    h("code", {}, "$NAME"), " (PowerShell) or ", h("code", {}, "%NAME%"), " (command prompt).")], "Save", async () => {
    await save(ed.values()); toast("Variables saved"); render();
  });
}

async function varsView() {
  const v = await api("GET", "/api/admin/vars");
  const devVars = v.devices || {};
  const grpVars = v.groups || {};
  let names = [...v.names];
  const inputs = []; // {kind, id, name, el, orig}
  const thead = h("thead", {});
  const tbody = h("tbody", {});
  const err = h("p", { class: "error", role: "alert" });
  const devices = [...S.devices].sort((a, b) => (a.group || "").localeCompare(b.group || "") || deviceName(a).localeCompare(deviceName(b)));

  function cell(kind, id, name, value, inherited) {
    const el = h("input", { type: "text", value: value ?? "", placeholder: inherited ? inherited.value : "", spellcheck: "false",
      title: inherited ? `Inherited from ${inherited.from}` : "", "aria-label": `${name} for ${kind} ${id}` });
    const orig = value ?? "";
    el.addEventListener("input", () => el.classList.toggle("changed", el.value !== orig));
    inputs.push({ kind, id, name, el, orig });
    return h("td", {}, el);
  }
  function build() {
    inputs.length = 0;
    thead.replaceChildren(h("tr", {}, h("th", {}, "Device or location"), names.map((n) => h("th", {}, h("span", { class: "mono" }, n),
      (v.used_by[n] || []).length ? null : h("button", { class: "x", title: `Remove the ${n} column (clears its values when you save)`, onclick: () => { names = names.filter((x) => x !== n); build(); } }, "×")))));
    const rows = [];
    if (S.groups.length) rows.push(h("tr", { class: "section" }, h("td", { colspan: names.length + 1 }, "Locations: sublocations and devices inherit these")));
    for (const g of S.groups) {
      const inh = g.parent_id != null ? inheritedVars(g.parent_id, grpVars) : {};
      rows.push(h("tr", {}, h("td", { class: "name" }, g.path), names.map((n) => cell("group", g.id, n, (grpVars[g.id] || {})[n], inh[n]))));
    }
    rows.push(h("tr", { class: "section" }, h("td", { colspan: names.length + 1 }, "Devices")));
    for (const d of devices) {
      const inh = d.group_id != null ? inheritedVars(d.group_id, grpVars) : {};
      rows.push(h("tr", {}, h("td", { class: "name" }, h("strong", {}, deviceName(d)), h("div", { class: "muted" }, d.group || "Unassigned")),
        names.map((n) => cell("device", d.id, n, (devVars[d.id] || {})[n], inh[n]))));
    }
    if (!names.length) rows.push(h("tr", {}, h("td", { class: "empty", colspan: 1 }, "No variables yet. Add one, or give a script variables and they appear here.")));
    tbody.replaceChildren(...rows);
  }
  build();

  const newName = h("input", { type: "text", size: "32", placeholder: "New variable, e.g. SYNC_PRIMARY", spellcheck: "false", "aria-label": "New variable name" });
  const addCol = h("form", {
    class: "row", onsubmit: (e) => {
      e.preventDefault(); err.textContent = "";
      const n = newName.value.trim();
      if (!VAR_NAME.test(n)) { err.textContent = `"${n}" is not a valid name: letters, digits and _, starting with a letter`; return; }
      if (/^RD_/i.test(n)) { err.textContent = "Names starting with RD_ are built in"; return; }
      if (!names.some((x) => x.toLowerCase() === n.toLowerCase())) { names.push(n); build(); }
      newName.value = "";
    },
  }, newName, h("button", { class: "btn", type: "submit" }, "Add column"));

  // Collect full maps for every row that changed (or lost a column).
  function collect() {
    const out = { devices: {}, groups: {} };
    const touched = new Set(inputs.filter((i) => i.el.value !== i.orig).map((i) => i.kind + ":" + i.id));
    for (const [id, vars] of Object.entries(devVars)) if (Object.keys(vars).some((k) => !names.includes(k))) touched.add("device:" + id);
    for (const [id, vars] of Object.entries(grpVars)) if (Object.keys(vars).some((k) => !names.includes(k))) touched.add("group:" + id);
    for (const key of touched) {
      const [kind, id] = key.split(/:(.*)/s);
      const vals = {};
      for (const i of inputs) if (i.kind === kind && String(i.id) === id && i.el.value !== "") vals[i.name] = i.el.value;
      (kind === "device" ? out.devices : out.groups)[id] = vals;
    }
    return out;
  }
  const save = async () => {
    err.textContent = "";
    const body = collect();
    const n = Object.keys(body.devices).length + Object.keys(body.groups).length;
    if (!n) { toast("Nothing changed"); return; }
    try { await api("PUT", "/api/admin/vars", body); toast(`Saved ${n} row${n > 1 ? "s" : ""}`); render(); } catch (ex) { err.textContent = ex.message; }
  };

  // CSV: kind;id;name;VAR1;VAR2... (import accepts , or ;)
  const exportCsv = () => {
    // Semicolons, so Excel with a Danish (or other comma-decimal) locale opens it in columns.
    const esc = (x) => /[";,\n\r]/.test(x) ? `"${x.replace(/"/g, '""')}"` : x;
    const lines = [["kind", "id", "name", ...names].map(esc).join(";")];
    for (const g of S.groups) lines.push(["location", String(g.id), g.path, ...names.map((n) => (grpVars[g.id] || {})[n] || "")].map(esc).join(";"));
    for (const d of devices) lines.push(["device", d.id, deviceName(d), ...names.map((n) => (devVars[d.id] || {})[n] || "")].map(esc).join(";"));
    download(`rd-variables-${new Date().toISOString().slice(0, 10)}.csv`, "\ufeff" + lines.join("\r\n") + "\r\n", "text/csv");
  };
  const file = h("input", { type: "file", accept: ".csv,text/csv", hidden: true });
  file.addEventListener("change", async () => {
    const f = file.files[0];
    file.value = "";
    if (!f) return;
    err.textContent = "";
    try {
      const rows = parseCsv((await f.text()).replace(/^\ufeff/, ""));
      const head = rows.shift() || [];
      const col = (n) => head.findIndex((x) => x.trim().toLowerCase() === n);
      const [ck, ci, cn] = [col("kind"), col("id"), col("name")];
      if (ci < 0 && cn < 0) throw new Error("The CSV needs an id or a name column");
      const varCols = head.map((x, i) => [x.trim(), i]).filter(([x, i]) => i !== ck && i !== ci && i !== cn && x);
      for (const [x] of varCols) if (!VAR_NAME.test(x) || /^RD_/i.test(x)) throw new Error(`Column "${x}" is not a valid variable name`);
      const body = { devices: {}, groups: {} };
      const unknown = [];
      for (const r of rows) {
        if (!r.some((x) => x.trim())) continue;
        const kind = ck >= 0 ? r[ck].trim().toLowerCase() : "device";
        const id = ci >= 0 ? (r[ci] || "").trim() : "";
        const name = cn >= 0 ? (r[cn] || "").trim().toLowerCase() : "";
        let key = null;
        if (kind === "location") {
          const g = S.groups.find((x) => String(x.id) === id) || S.groups.find((x) => x.path.toLowerCase() === name);
          if (g) key = ["groups", g.id];
        } else {
          const d = S.devices.find((x) => x.id === id) || S.devices.find((x) => [x.alias, x.hostname].some((n) => (n || "").toLowerCase() === name));
          if (d) key = ["devices", d.id];
        }
        if (!key) { unknown.push(id || name); continue; }
        const cur = { ...((key[0] === "devices" ? devVars : grpVars)[key[1]] || {}) };
        for (const [x, i] of varCols) { const val = (r[i] || "").trim(); if (val) cur[x] = val; else delete cur[x]; }
        body[key[0]][key[1]] = cur;
      }
      const n = Object.keys(body.devices).length + Object.keys(body.groups).length;
      if (!n) throw new Error("No rows matched a device or location" + (unknown.length ? `: ${unknown.slice(0, 5).join(", ")}` : ""));
      const cols = varCols.map(([x]) => x).join(", ");
      if (!confirm(`Set ${cols} on ${n} row${n > 1 ? "s" : ""} from ${f.name}? Empty cells clear the value.` + (unknown.length ? `\n\nNot found, skipped: ${unknown.slice(0, 10).join(", ")}` : ""))) return;
      await api("PUT", "/api/admin/vars", body);
      toast(`Imported ${n} row${n > 1 ? "s" : ""}`); render();
    } catch (ex) { err.textContent = ex.message; }
  });

  return [
    h("div", { class: "head" },
      h("div", {}, h("h1", {}, "Variables"), h("div", { class: "sub" }, "Values that differ per server, like an IP or the Syncthing primary. Scripts get them as $NAME.")),
      h("div", { class: "spacer" }),
      h("button", { class: "btn", onclick: exportCsv }, "Export CSV"),
      h("button", { class: "btn", onclick: () => file.click() }, "Import CSV"), file,
      h("button", { class: "btn primary", onclick: save }, "Save changes")),
    h("div", { class: "row", style: "margin-bottom:12px" }, addCol), err,
    h("div", { class: "table-wrap grid" }, h("table", {}, thead, tbody)),
    h("div", { class: "panel" }, h("h2", {}, "How values are picked"),
      h("p", {}, "For each device: a value typed when you start the script wins, then the device's own value, then its location (the nearest one up the tree), then the script's default."),
      h("p", {}, "Built in, always set:"),
      h("dl", { class: "facts" }, BUILTINS.flatMap(([n, d]) => [h("dt", { class: "mono" }, n), h("dd", {}, d)]))),
  ];
}

function download(name, text, type) {
  const a = h("a", { href: URL.createObjectURL(new Blob([text], { type })), download: name });
  document.body.append(a); a.click(); a.remove();
  setTimeout(() => URL.revokeObjectURL(a.href), 5000);
}

function parseCsv(text) {
  const rows = [];
  let row = [], field = "", q = false;
  const sep = (text.split(/\r?\n/, 1)[0].match(/;/g) || []).length > (text.split(/\r?\n/, 1)[0].match(/,/g) || []).length ? ";" : ",";
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (q) {
      if (c === '"') { if (text[i + 1] === '"') { field += '"'; i++; } else q = false; } else field += c;
    } else if (c === '"') q = true;
    else if (c === sep) { row.push(field); field = ""; }
    else if (c === "\n" || c === "\r") {
      if (c === "\r" && text[i + 1] === "\n") i++;
      row.push(field); rows.push(row); row = []; field = "";
    } else field += c;
  }
  if (field || row.length) { row.push(field); rows.push(row); }
  return rows;
}

// ---------- run history ----------

function varsLine(vars) {
  const keys = Object.keys(vars || {}).filter((k) => !k.startsWith("RD_")).sort();
  return keys.length ? keys.map((k) => `${k}=${vars[k]}`).join("  ") : "";
}

function runHistory(runs) {
  if (!runs.length) return [h("p", { class: "muted" }, "No scripts or power actions have run here yet.")];
  return [h("div", { class: "table-wrap" }, h("table", {},
    h("thead", {}, h("tr", {}, h("th", {}, "Job"), h("th", {}, "Result"), h("th", {}, "When"))),
    h("tbody", {}, runs.map((r) => {
      const [kind, label] = runStatus[r.status] || ["", r.status];
      const detail = r.output || varsLine(r.vars);
      return h("tr", {},
        h("td", {}, h("a", { href: "#/jobs/" + r.job_id }, r.job_name),
          detail ? h("details", { class: "run" }, h("summary", { class: "muted" }, "Output"),
            varsLine(r.vars) ? h("div", { class: "hint mono" }, varsLine(r.vars)) : null,
            r.output ? h("pre", { class: "out" }, r.output) : null) : null),
        h("td", {}, statusDot(kind, label), r.exit_code != null && r.status !== "success" ? h("div", { class: "muted" }, `exit ${r.exit_code}`) : null),
        h("td", { class: "nowrap" }, ago(r.finished_at || r.sent_at || r.created_at)));
    }))))];
}

// Fixed disks from the last inventory: name, bar of used space, free / total.
function storageCell(d) {
  if (!d.disks || !d.disks.length) return h("span", { class: "muted nowrap" }, d.online ? "Waiting for inventory" : "");
  const num = (n) => (n / 1e9).toFixed(n >= 1e11 ? 0 : 1);
  return h("div", { class: "disks" }, d.disks.map((k) => {
    const pct = k.size ? Math.round(k.free * 100 / k.size) : 0;
    return h("div", { class: "disk" + (pct < 10 ? " low" : ""), title: `${[k.name, k.label].filter(Boolean).join(" ")}: ${gb(k.free)} free of ${gb(k.size)} (${pct}% free)` },
      h("span", { class: "dname" }, k.name),
      h("meter", { min: 0, max: k.size || 1, value: k.size - k.free, low: (k.size || 1) * 0.75, high: (k.size || 1) * 0.9, optimum: 0, "aria-label": `${k.name} used` }),
      h("span", { class: "dval" }, `${num(k.free)} / ${gb(k.size)}`));
  }));
}

const SYNC_DOT = { ok: "ok", syncing: "wait", warn: "wait", error: "bad", stale: "hollow" };

function syncCell(d) {
  const x = d.sync;
  if (!x) return h("span", { class: "muted" }, d.online ? "No report yet" : "");
  if (x.level === "none") return h("span", { class: "muted" }, "Not installed");
  return h("span", { class: "status", title: (x.detail || [x.text]).join("\n") }, h("span", { class: "dot " + (SYNC_DOT[x.level] || ""), "aria-hidden": "true" }), x.text);
}

function syncView({ report: r, summary: sum }) {
  const head = h("h2", {}, "Syncthing");
  if (!r) return [head, h("p", { class: "muted" }, "No report yet.")];
  const out = [head, h("p", {}, h("span", { class: "status" }, h("span", { class: "dot " + (SYNC_DOT[sum.level] || "") }), sum.text)),
    (sum.detail || []).length > 1 ? h("ul", {}, sum.detail.map((t) => h("li", {}, t))) : null,
    h("p", { class: "hint" }, `${r.my_name || "This device"}, Syncthing ${r.version || ""}, reported ${ago(sum.at)}`)];
  if (r.error) out.push(h("pre", { class: "out" }, r.error));
  const names = Object.fromEntries((r.devices || []).map((x) => [x.id, x]));
  const types = { sendonly: "Send only", receiveonly: "Receive only", sendreceive: "Send & receive", receiveencrypted: "Encrypted" };
  for (const f of r.folders || []) {
    const local = f.paused ? "Paused" : f.need_bytes > 0 || (f.state || "").startsWith("sync")
      ? `${f.state || "syncing"}, ${gb(f.need_bytes) === "-" ? "" : gb(f.need_bytes) + " to go"}` : f.errors ? `${f.errors} failed items` : f.state === "idle" ? "Up to date" : (f.state || "");
    out.push(h("div", { class: "table-wrap", style: "margin-top:10px" }, h("table", {},
      h("thead", {}, h("tr", {}, h("th", {}, f.label || f.id, h("div", { class: "muted mono" }, f.path)), h("th", { class: "num" }, types[f.type] || ""))),
      h("tbody", {},
        h("tr", {}, h("td", {}, "This device"), h("td", { class: "num" }, local)),
        (f.devices || []).map((p) => {
          const dv = names[p.id] || {};
          const st = p.remote_state === "notSharing" ? "Not sharing" : p.remote_state === "paused" || dv.paused ? "Paused"
            : !dv.connected ? "Not connected" : p.completion >= 100 ? "Up to date" : `${Math.floor(p.completion)}%`;
          return h("tr", {}, h("td", {}, dv.name || p.id.split("-")[0]),
            h("td", { class: "num" + (st === "Not connected" ? " error" : "") }, st));
        })))));
  }
  return out.filter(Boolean);
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
  if (t.all) return "All devices";
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

async function runDialog(target, scriptId) {
  const scripts = (await api("GET", "/api/admin/scripts")) || [];
  let mode = scripts.length ? "saved" : "inline";
  const pick = h("select", {}, scripts.map((s) => h("option", { value: s.id, selected: s.id === scriptId }, s.name)));
  const shellSel = h("select", {}, h("option", { value: "powershell" }, "PowerShell"), h("option", { value: "cmd" }, "Command prompt"));
  const runAs = h("select", {}, h("option", { value: "system" }, "SYSTEM"), h("option", { value: "user" }, "Logged-on user"));
  const timeout = h("input", { type: "number", min: "1", max: "86400", value: "300" });
  const body = h("textarea", { spellcheck: "false", placeholder: "Get-Service | Where-Object Status -eq Stopped" });
  const paramBox = h("div", {});
  const preview = h("div", {});
  let paramInputs = {};
  const saved = h("div", {}, h("label", { class: "field" }, h("span", {}, "Script"), pick), paramBox);
  const inline = h("div", {},
    h("div", { class: "row" },
      h("label", { class: "field" }, h("span", {}, "Shell"), shellSel),
      h("label", { class: "field" }, h("span", {}, "Run as"), runAs),
      h("label", { class: "field" }, h("span", {}, "Timeout (seconds)"), timeout)),
    h("label", { class: "field" }, h("span", {}, "Script"), body),
    h("p", { class: "hint" }, "Variables set on the devices and their locations are available too, e.g. ", h("code", {}, "$IP"), "."));
  const bSaved = h("button", { type: "button", class: "btn", onclick: () => setMode("saved") }, "Saved script");
  const bInline = h("button", { type: "button", class: "btn", onclick: () => setMode("inline") }, "Write one now");
  function setMode(m) {
    mode = m; saved.hidden = m !== "saved"; inline.hidden = m !== "inline"; preview.hidden = m !== "saved";
    bSaved.setAttribute("aria-pressed", String(m === "saved")); bInline.setAttribute("aria-pressed", String(m === "inline"));
    if (m === "saved") refresh();
  }
  const values = () => {
    const out = {};
    for (const [k, el] of Object.entries(paramInputs)) if (el.value !== "") out[k] = el.value;
    return out;
  };
  let timer = null, seq = 0;
  async function loadPreview() {
    const my = ++seq;
    try {
      const res = await api("POST", "/api/admin/jobs/preview", { ...target, script_id: Number(pick.value), vars: values() });
      if (my !== seq) return;
      const params = res.params || [];
      if (!params.length) { preview.replaceChildren(); return; }
      const missing = res.devices.filter((d) => d.missing && d.missing.length).length;
      preview.replaceChildren(
        h("p", { class: missing ? "error" : "hint" }, missing ? `${missing} device${missing > 1 ? "s" : ""} lack a value. Type one above, or set it under Variables.` : "Values each device will get:"),
        h("div", { class: "table-wrap preview" }, h("table", {},
          h("thead", {}, h("tr", {}, h("th", {}, "Device"), params.map((p) => h("th", { class: "mono" }, p.name)))),
          h("tbody", {}, res.devices.map((d) => h("tr", {}, h("td", {}, d.name),
            params.map((p) => (d.missing || []).includes(p.name) ? h("td", { class: "miss" }, "missing") : h("td", {}, d.vars[p.name]))))))));
    } catch (ex) { if (my === seq) preview.replaceChildren(h("p", { class: "error" }, ex.message)); }
  }
  function refresh() {
    const sc = scripts.find((s) => String(s.id) === pick.value);
    paramInputs = {};
    const ps = (sc && sc.params) || [];
    paramBox.replaceChildren(...(ps.length ? [
      h("p", { class: "hint" }, "Leave empty to use each device's own value."),
      h("div", { class: "row" }, ps.map((p) => {
        const el = h("input", { type: "text", placeholder: p.default ? `Device value, else ${p.default}` : "Device value", spellcheck: "false",
          oninput: () => { clearTimeout(timer); timer = setTimeout(loadPreview, 300); } });
        paramInputs[p.name] = el;
        return h("label", { class: "field", title: p.description || "" }, h("span", { class: "mono" }, p.name), el);
      }))] : []));
    loadPreview();
  }
  pick.addEventListener("change", refresh);
  bSaved.disabled = !scripts.length;
  const sched = scheduleFields();
  const dlg = dialog("Run script", [h("p", {}, "On: ", h("strong", {}, targetText(target))), h("div", { class: "choice" }, bSaved, bInline), saved, inline, preview, sched.el],
    "Run script", async () => {
      const req = { ...target, ...sched.values() };
      if (mode === "saved") Object.assign(req, { script_id: Number(pick.value), vars: values() });
      else Object.assign(req, { shell: shellSel.value, run_as: runAs.value, timeout_sec: Number(timeout.value), body: body.value });
      const res = await api("POST", "/api/admin/jobs", req);
      toast(`Sent to ${res.devices} device${res.devices > 1 ? "s" : ""}`);
      go("#/jobs/" + res.job.id);
    });
  dlg.classList.add("wide");
  setMode(mode);
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
      h("td", {}, h("strong", {}, j.name), j.schedule_id ? h("span", { class: "pill" }, "Scheduled") : null,
        h("div", { class: "muted" }, j.kind === "power" ? "Power action" : j.kind === "update" ? "Client update" : j.shell === "cmd" ? "Command prompt" : "PowerShell")),
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
    const vl = varsLine(r.vars);
    const out = r.output || vl ? h("tr", {}, h("td", { colspan: 4 },
      vl ? h("div", { class: "hint mono" }, vl) : null,
      r.output ? h("pre", { class: "out" }, r.output) : null)) : null;
    return [row, out];
  });
  const meta = h("dl", { class: "facts" },
    h("dt", {}, "Target"), h("dd", {}, job.target),
    h("dt", {}, "Created"), h("dd", {}, job.schedule_id ? `${when(job.created_at)} by a schedule` : `${when(job.created_at)} by ${job.created_by}`),
    job.script_id ? [h("dt", {}, "Script"), h("dd", {}, h("a", { href: "#/scripts/" + job.script_id }, "Open the saved script"))] : [],
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

const DAYS = [["Mon", 1], ["Tue", 2], ["Wed", 3], ["Thu", 4], ["Fri", 5], ["Sat", 6], ["Sun", 0]];
const pad2 = (n) => String(n).padStart(2, "0");

function scheduleText(sc) {
  if (sc.kind === "hours") return sc.every_hours === 1 ? "Every hour" : `Every ${sc.every_hours} hours`;
  const at = `${pad2(Math.floor(sc.at_minute / 60))}:${pad2(sc.at_minute % 60)}`;
  const days = DAYS.filter(([, b]) => sc.weekdays & (1 << b)).map(([n]) => n);
  const when = days.length === 7 ? "Every day" : sc.weekdays === 0b0111110 ? "Weekdays" : days.join(", ");
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
  return `${when} at ${at}` + (sc.tz && sc.tz !== tz ? ` (${sc.tz})` : "");
}

async function exportScripts(ids) {
  const b = await api("GET", "/api/admin/scripts/export" + (ids ? "?ids=" + ids.join(",") : ""));
  const one = ids && b.scripts.length === 1 ? b.scripts[0].name.replace(/[^\w.-]+/g, "-") : "rd-scripts";
  download(`${one}-${new Date().toISOString().slice(0, 10)}.json`, JSON.stringify(b, null, 2) + "\n", "application/json");
}

function importScripts() {
  const file = h("input", { type: "file", accept: ".json,application/json", required: true });
  const replace = h("input", { type: "checkbox" });
  const result = h("div", {});
  dialog("Import scripts", [
    h("label", { class: "field" }, h("span", {}, "Export file (.json)"), file),
    h("label", { class: "check" }, replace, "Replace scripts that have the same name"),
    h("p", { class: "hint" }, "Without it, scripts whose name already exists are left alone. Schedules and device values are not part of the file."),
    result,
  ], "Import", async () => {
    const f = file.files[0];
    if (!f) throw new Error("Choose a file");
    let b;
    try { b = JSON.parse(await f.text()); } catch { throw new Error("That file is not JSON"); }
    const res = await api("POST", "/api/admin/scripts/import", { ...b, replace: replace.checked });
    const parts = [`${res.created} added`, `${res.updated} replaced`];
    if (res.skipped && res.skipped.length) parts.push(`${res.skipped.length} skipped (name exists)`);
    toast("Import: " + parts.join(", "));
    if (res.errors && res.errors.length) {
      result.replaceChildren(h("p", { class: "error" }, "Not imported:"), h("pre", { class: "out" }, res.errors.join("\n")));
      render();
      return false; // keep the dialog open to show the errors
    }
    render();
  });
}

function paramsEditor(params) {
  const box = h("div", { class: "kv three" }, h("span", { class: "kh" }, "Variable"), h("span", { class: "kh" }, "Default"), h("span", { class: "kh" }, "Description"), h("span", {}));
  const rows = [];
  const add = (p = { name: "", default: "", description: "" }) => {
    const name = h("input", { type: "text", value: p.name, placeholder: "IP", spellcheck: "false", "aria-label": "Variable name" });
    const def = h("input", { type: "text", value: p.default, placeholder: "None (required)", spellcheck: "false", "aria-label": "Default value" });
    const desc = h("input", { type: "text", value: p.description, placeholder: "What it is for", "aria-label": "Description" });
    const row = { name, def, desc };
    const x = h("button", { type: "button", class: "x", title: "Remove", "aria-label": "Remove", onclick: () => { [name, def, desc, x].forEach((e) => e.remove()); rows.splice(rows.indexOf(row), 1); } }, "×");
    rows.push(row);
    box.append(name, def, desc, x);
  };
  for (const p of params || []) add(p);
  return {
    el: h("div", {}, box, h("button", { type: "button", class: "btn small", onclick: () => { add(); rows[rows.length - 1].name.focus(); } }, "Add variable")),
    values() {
      const out = [];
      for (const r of rows) {
        const n = r.name.value.trim();
        if (!n && !r.def.value && !r.desc.value) continue;
        if (!VAR_NAME.test(n)) throw new Error(`"${n}" is not a valid variable name: letters, digits and _, starting with a letter`);
        out.push({ name: n, default: r.def.value, description: r.desc.value });
      }
      return out;
    },
  };
}

function scheduleDialog(script, sc) {
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  sc = sc || { kind: "days", every_hours: 6, at_minute: 6 * 60, weekdays: 0x7f, target: { all: true }, vars: {}, deadline_hours: 1, enabled: true, tz };
  const kind = h("select", {}, h("option", { value: "days" }, "At a set time"), h("option", { value: "hours" }, "Every few hours"));
  kind.value = sc.kind;
  const every = h("input", { type: "number", min: "1", max: "168", value: String(sc.every_hours || 6) });
  const at = h("input", { type: "time", value: `${pad2(Math.floor(sc.at_minute / 60))}:${pad2(sc.at_minute % 60)}`, required: true });
  const dayBoxes = DAYS.map(([n, b]) => { const c = h("input", { type: "checkbox", checked: !!(sc.weekdays & (1 << b)) }); c.dataset.bit = b; return [n, c]; });
  const target = h("select", {}, h("option", { value: "all" }, "All devices"), S.groups.map((g) => h("option", { value: String(g.id) }, g.path)));
  const custom = (sc.target.devices && sc.target.devices.length) || (sc.target.groups || []).length > 1;
  if (custom) target.prepend(h("option", { value: "keep" }, sc.target_text || "As before"));
  target.value = custom ? "keep" : sc.target.all ? "all" : String((sc.target.groups || [])[0] ?? "all");
  const deadline = h("select", {}, [[1, "1 hour"], [4, "4 hours"], [12, "12 hours"], [24, "24 hours"]].map(([v, l]) => h("option", { value: String(v) }, l)));
  deadline.value = String(sc.deadline_hours || 1);
  const enabled = h("input", { type: "checkbox", checked: sc.enabled });
  const vals = {};
  const hoursRow = h("div", { class: "row" }, h("label", { class: "field" }, h("span", {}, "Hours between runs"), every));
  const daysRow = h("div", {},
    h("label", { class: "field" }, h("span", {}, `Time (${sc.tz && sc.kind === "days" ? sc.tz : tz})`), at),
    h("div", { class: "field" }, h("span", { class: "kh" }, "Days"), h("div", { class: "days" }, dayBoxes.map(([n, c]) => h("label", {}, c, n)))));
  const sync = () => { hoursRow.hidden = kind.value !== "hours"; daysRow.hidden = kind.value !== "days"; };
  kind.addEventListener("change", sync); sync();
  const params = script.params || [];
  dialog(sc.id ? `Edit schedule: ${script.name}` : `Schedule ${script.name}`, [
    h("label", { class: "field" }, h("span", {}, "Repeat"), kind), hoursRow, daysRow,
    h("div", { class: "row" },
      h("label", { class: "field" }, h("span", {}, "Run on"), target),
      h("label", { class: "field" }, h("span", {}, "Offline devices run it if back within"), deadline)),
    params.length ? h("div", {}, h("h3", {}, "Values"), h("p", { class: "hint" }, "Leave empty to use each device's own value. Devices without a value are skipped and you get a notification."),
      h("div", { class: "row" }, params.map((p) => {
        const el = h("input", { type: "text", value: (sc.vars || {})[p.name] || "", placeholder: p.default ? `Device value, else ${p.default}` : "Device value", spellcheck: "false" });
        vals[p.name] = el;
        return h("label", { class: "field" }, h("span", { class: "mono" }, p.name), el);
      }))) : null,
    h("label", { class: "check" }, enabled, "Active"),
  ], sc.id ? "Save schedule" : "Create schedule", async () => {
    const [hh, mm] = at.value.split(":").map(Number);
    let weekdays = 0;
    for (const [, c] of dayBoxes) if (c.checked) weekdays |= 1 << Number(c.dataset.bit);
    const t = target.value === "keep" ? sc.target : target.value === "all" ? { all: true } : { groups: [Number(target.value)] };
    const v = {};
    for (const [k, el] of Object.entries(vals)) if (el.value !== "") v[k] = el.value;
    const body = { script_id: script.id, kind: kind.value, every_hours: Number(every.value), at_minute: hh * 60 + mm, weekdays,
      tz: kind.value === "days" && sc.id && sc.kind === "days" ? sc.tz : tz, target: t, vars: v, deadline_hours: Number(deadline.value), enabled: enabled.checked };
    const res = sc.id ? await api("PUT", "/api/admin/schedules/" + sc.id, body) : await api("POST", "/api/admin/schedules", body);
    toast(`Next run ${when(res.next_run)}`); render();
  });
}

function schedulesTable(list, script, showScript) {
  if (!list.length) return h("p", { class: "muted" }, showScript ? "No schedules. Open a script and choose Add schedule." : "Not scheduled.");
  return h("div", { class: "table-wrap" }, h("table", {},
    h("thead", {}, h("tr", {}, showScript ? h("th", {}, "Script") : null, h("th", {}, "When"), h("th", {}, "On"), h("th", {}, "Next"), h("th", {}, "Last"), h("th", {}, ""))),
    h("tbody", {}, list.map((sc) => h("tr", {},
      showScript ? h("td", {}, h("a", { href: "#/scripts/" + sc.script_id }, sc.script_name)) : null,
      h("td", {}, scheduleText(sc), sc.enabled ? null : h("div", {}, h("span", { class: "tag" }, "Paused"))),
      h("td", {}, sc.target_text || "All devices"),
      h("td", { class: "nowrap" }, sc.enabled ? ago(sc.next_run) : "-"),
      h("td", { class: "nowrap" }, sc.last_job_id ? h("a", { href: "#/jobs/" + sc.last_job_id }, ago(sc.last_run)) : sc.last_run ? ago(sc.last_run) : "never"),
      h("td", { class: "num" },
        h("button", {
          class: "btn small", title: "Start it now; the schedule stays as it is", onclick: async () => {
            const res = await api("POST", `/api/admin/schedules/${sc.id}/run`);
            if (res.note) alert(res.note);
            go("#/jobs/" + res.job.id);
          },
        }, "Run now"), " ",
        script ? h("button", { class: "btn small", onclick: () => scheduleDialog(script, sc) }, "Edit") : null, " ",
        h("button", {
          class: "btn link danger", onclick: async () => {
            if (!confirm(`Delete this schedule (${scheduleText(sc)})? Jobs it already started stay in the log.`)) return;
            await api("DELETE", "/api/admin/schedules/" + sc.id); toast("Schedule deleted"); render();
          },
        }, "Delete")))))));
}

function jobRunsTable(jobs) {
  if (!jobs.length) return h("p", { class: "muted" }, "Not run yet.");
  return h("div", { class: "table-wrap" }, h("table", {},
    h("thead", {}, h("tr", {}, h("th", {}, "Started"), h("th", { class: "num" }, "Done"), h("th", { class: "num" }, "Failed"), h("th", { class: "num" }, "Waiting"))),
    h("tbody", {}, jobs.map((j) => {
      const c = j.counts || {};
      return h("tr", { class: "click", onclick: () => go("#/jobs/" + j.id) },
        h("td", {}, h("div", { class: "nowrap" }, when(j.created_at), j.schedule_id ? h("span", { class: "pill" }, "Scheduled") : null), h("div", { class: "muted" }, j.target)),
        h("td", { class: "num" }, String(c.success || 0)),
        h("td", { class: "num" }, c.failed ? h("strong", { class: "error" }, String(c.failed)) : "0"),
        h("td", { class: "num" }, String((c.queued || 0) + (c.sent || 0))));
    }))));
}

async function scriptsView(id) {
  const [scripts, schedules] = await Promise.all([api("GET", "/api/admin/scripts"), api("GET", "/api/admin/schedules")]);
  const current = id === "new" ? { name: "", description: "", shell: "powershell", run_as: "system", timeout_sec: 300, body: "", params: [] }
    : (scripts || []).find((s) => String(s.id) === id);
  const list = h("div", { class: "table-wrap" }, h("table", {},
    h("thead", {}, h("tr", {}, h("th", {}, "Script"), h("th", {}, "Variables"), h("th", {}, "Changed"))),
    h("tbody", {}, scripts.length ? scripts.map((s) => h("tr", { class: "click" + (String(s.id) === id ? " sel" : ""), onclick: () => go("#/scripts/" + s.id) },
      h("td", {}, h("strong", {}, s.name), schedules.some((x) => x.script_id === s.id) ? h("span", { class: "pill" }, "Scheduled") : null,
        h("div", { class: "muted" }, s.description || (s.shell === "cmd" ? "Command prompt" : "PowerShell"))),
      h("td", { class: "mono" }, (s.params || []).map((p) => p.name).join(", ")),
      h("td", { class: "nowrap" }, ago(s.updated_at)))) : h("tr", {}, h("td", { colspan: 3, class: "empty" }, "No saved scripts. Save the ones you run often, or import an export file.")))));
  const out = [h("div", { class: "head" }, h("div", {}, h("h1", {}, "Scripts"), h("div", { class: "sub" }, "Saved scripts you can run on any device or location, by hand or on a schedule")),
    h("div", { class: "spacer" }),
    h("button", { class: "btn", onclick: importScripts }, "Import"),
    scripts.length ? h("button", { class: "btn", onclick: () => exportScripts() }, "Export all") : null,
    h("button", { class: "btn primary", onclick: () => go("#/scripts/new") }, "New script"))];
  if (!current) {
    out.push(list, h("div", { class: "panel" }, h("h2", {}, "Schedules"), schedulesTable(schedules, null, true)));
    return out;
  }

  const name = h("input", { type: "text", required: true, value: current.name });
  const desc = h("input", { type: "text", value: current.description });
  const shellSel = h("select", {}, h("option", { value: "powershell" }, "PowerShell"), h("option", { value: "cmd" }, "Command prompt"));
  shellSel.value = current.shell;
  const runAs = h("select", {}, h("option", { value: "system" }, "SYSTEM"), h("option", { value: "user" }, "Logged-on user"));
  runAs.value = current.run_as;
  const timeout = h("input", { type: "number", min: "1", max: "86400", value: String(current.timeout_sec) });
  const body = h("textarea", { spellcheck: "false", required: true }, current.body);
  body.addEventListener("keydown", (e) => {
    if (e.key !== "Tab" || e.shiftKey) return; // Tab indents; Shift+Tab leaves the editor
    e.preventDefault();
    body.setRangeText("    ", body.selectionStart, body.selectionEnd, "end");
  });
  const params = paramsEditor(current.params);
  const usage = h("p", { class: "hint" });
  const syncUsage = () => usage.replaceChildren(h("span", {}, "Use them as ", h("code", {}, shellSel.value === "cmd" ? "%IP%" : "$IP"),
    ". Each device gets its own value (set under Variables), its location's, or the default. Always set: ",
    BUILTINS.flatMap(([n], i) => [i ? ", " : "", h("code", {}, n)]), "."));
  shellSel.addEventListener("change", syncUsage); syncUsage();
  const err = h("p", { class: "error", role: "alert" });
  const form = h("form", {
    class: "panel", onsubmit: async (e) => {
      e.preventDefault(); err.textContent = "";
      try {
        const payload = { name: name.value, description: desc.value, shell: shellSel.value, run_as: runAs.value, timeout_sec: Number(timeout.value), body: body.value, params: params.values() };
        const saved = current.id ? await api("PUT", "/api/admin/scripts/" + current.id, payload) : await api("POST", "/api/admin/scripts", payload);
        toast("Script saved"); go("#/scripts/" + saved.id);
      } catch (ex) { err.textContent = ex.message; }
    },
  },
  h("div", { class: "head" }, h("h2", {}, current.id ? "Edit script" : "New script"), h("div", { class: "spacer" }),
    h("button", { class: "btn link", type: "button", onclick: () => go("#/scripts") }, "Close")),
  h("label", { class: "field" }, h("span", {}, "Name"), name),
  h("label", { class: "field" }, h("span", {}, "Description"), desc),
  h("div", { class: "row" },
    h("label", { class: "field" }, h("span", {}, "Shell"), shellSel),
    h("label", { class: "field" }, h("span", {}, "Run as"), runAs),
    h("label", { class: "field" }, h("span", {}, "Timeout (seconds)"), timeout)),
  h("div", { class: "field" }, h("h3", {}, "Variables"), params.el, usage),
  h("label", { class: "field" }, h("span", {}, "Script"), body), err,
  h("div", { class: "row" }, h("button", { class: "btn primary", type: "submit" }, "Save script"),
    current.id ? h("button", { class: "btn", type: "button", onclick: () => exportScripts([current.id]) }, "Export") : null,
    h("div", { class: "spacer" }),
    current.id ? h("button", {
      class: "btn danger", type: "button", onclick: async () => {
        if (!confirm(`Delete the script "${current.name}" and its schedules? Jobs already sent keep their copy.`)) return;
        await api("DELETE", "/api/admin/scripts/" + current.id); toast("Script deleted"); go("#/scripts");
      },
    }, "Delete script") : null));
  if (!current.id) {
    out.push(h("div", { class: "split" }, form, list));
    return out;
  }
  const mine = schedules.filter((x) => x.script_id === current.id);
  const history = await api("GET", "/api/admin/jobs?limit=25&script_id=" + current.id);
  const side = h("div", {},
    h("div", { class: "panel" },
      h("div", { class: "head" }, h("h2", {}, "Run"), h("div", { class: "spacer" }),
        h("button", { class: "btn primary", onclick: () => go("#/devices") }, "Pick devices")),
      h("p", { class: "hint" }, "Select devices or a location under Devices and choose Run script, or run it on a location here:"),
      h("div", { class: "row" }, h("select", { id: "run-loc", "aria-label": "Location" }, h("option", { value: "all" }, "All devices"), S.groups.map((g) => h("option", { value: String(g.id) }, g.path))),
        h("button", {
          class: "btn", onclick: () => {
            const v = document.getElementById("run-loc").value;
            runDialog(v === "all" ? { all: true } : { groups: [Number(v)] }, current.id);
          },
        }, "Run…"))),
    h("div", { class: "panel" },
      h("div", { class: "head" }, h("h2", {}, "Schedules"), h("div", { class: "spacer" }),
        h("button", { class: "btn", onclick: () => scheduleDialog(current) }, "Add schedule")),
      schedulesTable(mine, current, false)),
    h("div", { class: "panel" }, h("h2", {}, "Run history"), jobRunsTable(history || [])));
  out.push(h("div", { class: "split wide-side" }, form, side));
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
      case "vars": content = await varsView(); break;
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
