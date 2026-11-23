// dm web: the phone's view of a session. The fleet polls, one pane streams.
// Everything arrives already shaped by the server, so this file only paints.

const REFRESH_MS = 3000;
const TEXT_POLL_MS = 1000;

let fleet = null;
// pane is the open pane view: its id, the text revision it has, and the poll.
let pane = null;
// catalogue is the command rows, fetched once. The list lives in Go.
let catalogue = null;
// sheet is the actions sheet: whether it is open, and whether this page pushed
// the history entry the back gesture pops.
let sheetOpen = false;
let sheetPending = false;
let sheetPushed = false;
// armed is the destructive button waiting for its second tap.
let armed = null;
// target is the id each kind of command runs against, chosen in the sheet.
const target = { pane: "", window: "", workspace: "" };
// timers holds the polls while the page is visible, and nothing at all while
// it is hidden.
const timers = { fleet: null, text: null };

const $ = (id) => document.getElementById(id);

// TOKEN is the access token this page was opened with. The server sets a
// cookie too, but a page opened from a QR scanner is a cross site navigation
// and a Strict cookie does not ride it, so the token is carried here and
// appended to every request rather than assumed.
const TOKEN = (() => {
  const q = new URLSearchParams(location.search).get("t");
  if (q) {
    try { sessionStorage.setItem("dm_token", q); } catch (e) { /* private mode */ }
    return q;
  }
  try { return sessionStorage.getItem("dm_token") || ""; } catch (e) { return ""; }
})();

// api appends the token to an endpoint path.
function api(path) {
  return path + (path.includes("?") ? "&" : "?") + "t=" + encodeURIComponent(TOKEN);
}

// el builds one element with an optional class and text.
function el(tag, cls, text) {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined && text !== null) n.textContent = text;
  return n;
}

function banner(msg) {
  const b = $("banner");
  b.textContent = msg || "";
  b.classList.toggle("show", !!msg);
}

// loadFleet paints the dashboard. A hidden tab does not poll: a phone in a
// pocket should not keep a session awake for a screen nobody reads.
async function loadFleet() {
  if (document.hidden) return;
  let f;
  try {
    const res = await fetch(api("/api/fleet"));
    if (!res.ok) {
      banner("cannot reach the session: " + res.status);
      return;
    }
    f = await res.json();
  } catch (e) {
    banner("cannot reach the session: " + e);
    return;
  }
  fleet = f;
  banner(f.error || "");
  renderPanes(f);
  renderBox(f);
  if (thumbsOn()) renderThumbs(f);
  $("session").textContent = "dm " + f.session;
  renderCounts(f);
  // Read only offers nothing that writes, so the sheet and the compose go.
  $("sheet-open").style.display = f.readOnly ? "none" : "";
  $("compose").style.display = f.readOnly ? "none" : "";
  if (pane) {
    if (!f.panes.some((p) => p.id === pane.id)) closePane();
    else renderPaneChrome();
  }
}

// renderCounts is the header line: how many agents sit in each state.
function renderCounts(f) {
  const counts = {};
  for (const p of f.panes) {
    const s = p.status || "note";
    counts[s] = (counts[s] || 0) + 1;
  }
  const order = ["blocked", "error", "working", "done", "note"];
  const parts = [];
  for (const s of order) {
    if (counts[s]) parts.push(counts[s] + " " + s);
  }
  $("counts").textContent = parts.join("  ");
}

function stateOf(p) {
  return p.status && p.status !== "clear" ? p.status : "note";
}

// renderPanes lists the fleet, blocked and error first: the rows that want a
// decision are the ones a phone should show without scrolling.
function renderPanes(f) {
  const box = $("panes");
  box.textContent = "";
  const panes = f.panes.slice().sort((a, b) => {
    const rank = { blocked: 0, error: 1, working: 2, done: 3, note: 4 };
    const d = (rank[stateOf(a)] ?? 5) - (rank[stateOf(b)] ?? 5);
    return d !== 0 ? d : a.id.localeCompare(b.id);
  });
  if (panes.length === 0) {
    box.append(el("div", "empty", "no panes"));
    return;
  }
  for (const p of panes) {
    const row = el("a", "row");
    row.href = "#pane=" + encodeURIComponent(p.id);

    const title = el("div", "title");
    title.append(document.createTextNode(p.name || p.app || p.id));
    const kind = [p.workspaceName, p.windowName].filter(Boolean).join(" / ");
    if (kind) title.append(el("span", "kind", "  " + kind));
    row.append(title);

    const st = el("div", "state " + stateOf(p));
    st.append(el("span", "dot " + stateOf(p)));
    st.append(document.createTextNode(stateOf(p)));
    row.append(st);

    const where = el("div", "where");
    where.append(el("span", "id", p.id + " "));
    if (p.branch) {
      where.append(el("b", "", p.branch));
      if (p.dirty) where.append(el("b", "", " +" + (p.added || 0) + "/-" + (p.deleted || 0)));
      where.append(document.createTextNode("  "));
    }
    if (p.cwd) where.append(document.createTextNode(p.cwd));
    row.append(where);

    if (p.message) row.append(el("div", "msg", p.message));
    if (p.active) row.append(el("div", "msg", "on screen at the desk"));
    box.append(row);
  }
}

// renderBox lists the newest box rows.
function renderBox(f) {
  const box = $("box");
  box.textContent = "";
  if (f.box.length === 0) {
    box.append(el("div", "empty", "the box is empty"));
    return;
  }
  for (const e of f.box) {
    const row = el("div", "row");
    const title = el("div", "title");
    title.append(el("span", "id", e.key || "-"));
    title.append(document.createTextNode("  " + (e.from || "?")));
    row.append(title);
    const st = el("div", "state " + (e.state || "note"));
    st.append(el("span", "dot " + (e.state || "note")));
    st.append(document.createTextNode(e.state || "note"));
    row.append(st);
    row.append(el("div", "msg", e.msg));
    box.append(row);
  }
}

// --- the actions ---------------------------------------------------------
//
// Every command is a row the server published at /api/commands, sent back to
// /api/action by id. This file names no action of its own.

// runAction sends one catalogue command. A refusal paints the daemon's message
// in the banner and changes nothing else; a success refreshes the fleet at
// once, so the page never waits for the poll to show what it just did.
async function runAction(body) {
  let res;
  try {
    res = await fetch(api("/api/action"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
  } catch (e) {
    banner("action failed: " + e);
    return null;
  }
  if (!res.ok) {
    banner(await res.text());
    return null;
  }
  banner("");
  const text = await res.text();
  await loadFleet();
  if (pane) pollText();
  return text ? JSON.parse(text) : {};
}

// arm turns one button into its own question. The next tap on that button
// sends; any other command first takes the question away.
function arm(btn, question) {
  armed = { el: btn, label: btn.textContent };
  btn.textContent = question;
  btn.classList.add("armed");
}

function disarm() {
  if (!armed) return;
  armed.el.textContent = armed.label;
  armed.el.classList.remove("armed");
  armed = null;
}

// dispatch sends one command. A command that destroys something asks once,
// with its target named, and only the second tap sends. Nothing else asks.
async function dispatch(btn, body, question) {
  if (question) {
    if (armed && armed.el === btn) {
      disarm();
    } else {
      disarm();
      arm(btn, question);
      return;
    }
  }
  const res = await runAction(body);
  if (res === null) return;
  if (body.id === "close-pane" && pane && body.target === pane.id) closePane();
}

// --- the sheet -----------------------------------------------------------

// loadCommands fetches the catalogue once.
async function loadCommands() {
  if (catalogue) return catalogue;
  let rows;
  try {
    const res = await fetch(api("/api/commands"));
    if (!res.ok) {
      banner(await res.text());
      return null;
    }
    const doc = await res.json();
    rows = doc.commands || [];
  } catch (e) {
    banner("cannot load commands: " + e);
    return null;
  }
  catalogue = rows;
  return catalogue;
}

async function openSheet() {
  if (sheetOpen || sheetPending || !fleet || fleet.readOnly) return;
  sheetPending = true;
  const rows = await loadCommands();
  sheetPending = false;
  if (!rows) return;
  renderSheet(rows);
  sheetOpen = true;
  $("sheet").classList.add("open");
  // Push one entry, so the back gesture is the sheet's own dismiss.
  history.pushState({ sheet: 1 }, "", location.href);
  sheetPushed = true;
}

// dismissSheet closes the sheet the way the back gesture would, keeping the
// history in step with what is on screen.
function dismissSheet() {
  if (!sheetOpen) return;
  if (sheetPushed) {
    history.back();
    return;
  }
  closeSheet();
}

function closeSheet() {
  sheetOpen = false;
  sheetPushed = false;
  disarm();
  $("sheet").classList.remove("open");
}

// renderSheet paints the rows in the order the server sent them, with one
// target picker per kind above the group that needs one.
function renderSheet(rows) {
  const body = $("sheet-body");
  body.textContent = "";
  let group = "";
  for (const cmd of rows) {
    if (cmd.group !== group) {
      group = cmd.group;
      body.append(el("h2", "", group));
      const kinds = [];
      for (const c of rows) {
        if (c.group === group && c.target && !kinds.includes(c.target)) kinds.push(c.target);
      }
      for (const kind of kinds) body.append(targetPicker(kind));
    }
    body.append(commandRow(cmd));
  }
}

// targetPicker is one kind's target, defaulting to the focused pane.
function targetPicker(kind) {
  const wrap = el("label", "targets");
  wrap.append(el("span", "", kind));
  const sel = el("select");
  for (const o of targetOptions(kind)) {
    const opt = el("option", "", o.label);
    opt.value = o.value;
    if (o.value === ensureTarget(kind)) opt.selected = true;
    sel.append(opt);
  }
  sel.addEventListener("change", () => {
    target[kind] = sel.value;
    disarm();
  });
  wrap.append(sel);
  return wrap;
}

// targetOptions lists the ids of one kind from the fleet, so the sheet names a
// real pane, window or workspace and never a guess.
function targetOptions(kind) {
  const panes = (fleet && fleet.panes) || [];
  if (kind === "pane") {
    return panes.map((p) => ({ value: p.id, label: (p.name || p.app || p.id) + "  " + p.id }));
  }
  const seen = new Map();
  for (const p of panes) {
    const id = kind === "window" ? p.window : p.workspace;
    const name = kind === "window" ? p.windowName : p.workspaceName;
    if (id && !seen.has(id)) seen.set(id, { value: id, label: id + "  " + (name || "") });
  }
  return [...seen.values()];
}

// ensureTarget keeps the chosen target, and falls back to the focused pane when
// it names a pane, window or workspace the fleet no longer has.
function ensureTarget(kind) {
  const options = targetOptions(kind);
  if (!options.some((o) => o.value === target[kind])) target[kind] = defaultTarget(kind);
  return target[kind];
}

function defaultTarget(kind) {
  const panes = (fleet && fleet.panes) || [];
  const at = panes.find((p) => p.focus) || panes.find((p) => pane && p.id === pane.id) || panes[0];
  if (!at) return "";
  if (kind === "pane") return at.id;
  if (kind === "window") return at.window || "";
  return at.workspace || "";
}

// commandRow is one command. A row that carries values of its own, the split
// sites or the layout presets, offers them as chips; every other row is one
// large button.
function commandRow(cmd) {
  const row = el("div", "cmd");
  if (cmd.sites || cmd.layouts) {
    row.append(el("div", "cmd-label", cmd.label));
    const chips = el("div", "chips");
    const values = cmd.sites || cmd.layouts;
    const field = cmd.sites ? "site" : "layout";
    for (const v of values) {
      const chip = el("button", "", v);
      chip.addEventListener("click", () => sheetSend(chip, cmd, { [field]: v }));
      chips.append(chip);
    }
    row.append(chips);
    return row;
  }
  const btn = el("button", "", cmd.label);
  btn.addEventListener("click", () => sheetSend(btn, cmd, {}));
  row.append(btn);
  return row;
}

// sheetSend assembles the request from the row and the group's target. A row
// that names a name asks for one first, because a rename with no name is a
// refusal from the daemon and a wasted tap.
async function sheetSend(btn, cmd, extra) {
  if (cmd.id === "msg-post") {
    // The box group points at the compose form rather than a second one.
    dismissSheet();
    focusCompose();
    return;
  }
  const body = { id: cmd.id };
  if (cmd.target) body.target = ensureTarget(cmd.target);
  Object.assign(body, extra);
  if ((cmd.fields || []).includes("name")) {
    const name = prompt(cmd.label + ": name");
    if (name === null) return;
    body.name = name;
  }
  const question = cmd.id === "close-pane" || cmd.id === "close-window"
    ? "close " + (body.target || "") + "?"
    : "";
  await dispatch(btn, body, question);
}

// --- the pane view -------------------------------------------------------

// paneActions are the pane's own commands, under its header. Each runs against
// the open pane, so the phone never has to open the sheet for them.
const paneActions = [
  { label: "split right", body: () => ({ id: "split", target: pane.id, site: "right" }) },
  { label: "split down", body: () => ({ id: "split", target: pane.id, site: "down" }) },
  { label: "zoom", body: () => ({ id: "zoom-pane" }) },
  { label: "next pane", body: () => ({ id: "next-pane" }) },
  { label: "rename", body: () => ({ id: "set-pane-name", target: pane.id }) },
  { label: "close", body: () => ({ id: "close-pane", target: pane.id }), ask: () => "close " + pane.id + "?" },
];

function renderPaneActions() {
  const box = $("pane-actions");
  box.textContent = "";
  for (const act of paneActions) {
    const btn = el("button", "", act.label);
    btn.addEventListener("click", () => {
      if (!pane) return;
      const body = act.body();
      if (body.id === "set-pane-name") {
        const name = prompt("name for " + pane.id);
        if (name === null) return;
        body.name = name;
      }
      dispatch(btn, body, act.ask ? act.ask() : "");
    });
    box.append(btn);
  }
}

// activeContext is the window and workspace the session stands on: what the
// strip names and walks, and the pane's own place when the session is elsewhere.
function activeContext() {
  const panes = (fleet && fleet.panes) || [];
  return panes.find((p) => p.active) || panes.find((p) => pane && p.id === pane.id) || {};
}

function renderPaneChrome() {
  if (!pane) return;
  const own = ((fleet && fleet.panes) || []).find((p) => p.id === pane.id) || {};
  $("pane-title").textContent = (own.name || own.app || pane.id) + "  " + pane.id;
  const at = activeContext();
  setStripName("strip-window", at.window, at.windowName);
  setStripName("strip-workspace", at.workspace, at.workspaceName);
}

function setStripName(id, code, name) {
  const n = $(id);
  n.textContent = "";
  n.append(el("b", "", code || "-"));
  if (name) n.append(document.createTextNode("  " + name));
}

// openPane streams one pane into a terminal. The address bar carries the pane
// id, so a reload or a shared link lands on the same pane.
async function openPane(id) {
  closePane(false);
  pane = { id, rev: "", hold: false };
  $("pane").classList.add("open");
  $("send").style.display = fleet && fleet.readOnly ? "none" : "flex";
  $("pane-state").textContent = "";
  renderPaneChrome();
  await pollText();
  startTimers();
}

// pollText reads the pane through the read action and paints it as text.
//
// The pane is not a terminal here: the text it printed is wrapped by this page
// at its own width and font, which is the whole reason a phone can read it
// without the session changing size.
async function pollText() {
  if (!pane || document.hidden) return;
  let d;
  try {
    const res = await fetch(api("/api/text?pane=" + encodeURIComponent(pane.id) + "&since=" + encodeURIComponent(pane.rev)));
    if (!res.ok) {
      banner(await res.text());
      return;
    }
    d = await res.json();
  } catch (e) {
    banner("cannot read the pane: " + e);
    return;
  }
  pane.rev = d.rev;
  $("pane-state").textContent = [d.state, d.message].filter(Boolean).join(": ");
  $("pane-state").className = "state " + (d.state || "note");
  if (!d.changed) return;
  const pre = $("text");
  pre.textContent = (d.lines || []).join("\n");
  // Follow the bottom, which is where a running agent's output is, unless the
  // reader scrolled up to read something.
  if (!pane.hold) pre.scrollTop = pre.scrollHeight;
}

// closePane stops the poll and, when asked, clears the address bar.
function closePane(updateHash = true) {
  if (!pane) return;
  pane = null;
  disarm();
  $("text").textContent = "";
  $("pane").classList.remove("open");
  stopTimers();
  startTimers();
  if (updateHash) history.replaceState(null, "", location.pathname + location.search);
}

// --- the box compose -----------------------------------------------------

function focusCompose() {
  const key = $("compose-key");
  key.scrollIntoView({ block: "center" });
  key.focus();
}

// composePost posts one box entry through msg-post, so a blocked lane is
// answered without typing into its pty.
async function composePost(ev) {
  ev.preventDefault();
  const key = $("compose-key").value.trim();
  const msg = $("compose-msg").value.trim();
  if (!key || !msg) {
    banner("the box wants a key and a message");
    focusCompose();
    return;
  }
  const res = await runAction({ id: "msg-post", key, state: $("compose-state").value, msg });
  if (res === null) return;
  $("compose-key").value = "";
  $("compose-msg").value = "";
}

function sendText(enter) {
  if (!pane) return;
  const input = $("send-text");
  post("/api/msg", { pane: pane.id, text: input.value, enter });
  input.value = "";
}

// post writes one key message into the pane and reports a refusal in the
// banner.
async function post(path, body) {
  try {
    const res = await fetch(api(path), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok) banner(await res.text());
    else banner("");
  } catch (e) {
    banner("send failed: " + e);
  }
}

// --- the pictures ---------------------------------------------------------
//
// A tile is the pane's screen painted as blocks: text in the foreground
// colour, background where a cell has one. Glyphs at this size are noise, but
// a diff's colour, a progress bar or an error still read at a glance.

// BASE is the 16-colour ANSI palette a terminal starts from.
const BASE = [
  "#000000", "#cd3131", "#0dbc79", "#e5e510", "#2472c8", "#bc3fbc", "#11a8cd", "#e5e5e5",
  "#666666", "#f14c4c", "#23d18b", "#f5f543", "#3b8eea", "#d670d6", "#29b8db", "#ffffff",
];

// CUBE is the per-channel level of the 6x6x6 colour cube above index 16.
const CUBE = [0, 95, 135, 175, 215, 255];

// palette resolves a 256-colour index.
function palette(n) {
  if (n < 16) return BASE[n];
  if (n < 232) {
    const i = n - 16;
    return "rgb(" + CUBE[Math.floor(i / 36)] + "," + CUBE[Math.floor(i / 6) % 6] + "," + CUBE[i % 6] + ")";
  }
  const v = 8 + (n - 232) * 10;
  return "rgb(" + v + "," + v + "," + v + ")";
}

// decodeColour resolves the pane's packed colour: 0 default, 1+n a palette
// index, and 1<<24|rgb a true colour. The packing is vt's; it is opaque here.
function decodeColour(v) {
  if (!v) return null;
  if (v & 0x1000000) {
    const rgb = (v & 0xffffff).toString(16).padStart(6, "0");
    return "#" + rgb;
  }
  return palette(v - 1);
}

const DEFAULT_FG = "#d7dee5";
const DEFAULT_BG = "#101418";

// paintShot draws one snapshot into a canvas. Runs of equally styled cells
// share one rectangle, so a screen of text costs a few fills per row.
function paintShot(canvas, snap) {
  const dpr = Math.min(2, window.devicePixelRatio || 1);
  const w = canvas.clientWidth || 1;
  const h = canvas.clientHeight || 1;
  if (canvas.width !== Math.round(w * dpr) || canvas.height !== Math.round(h * dpr)) {
    canvas.width = Math.round(w * dpr);
    canvas.height = Math.round(h * dpr);
  }
  const ctx = canvas.getContext("2d");
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.fillStyle = DEFAULT_BG;
  ctx.fillRect(0, 0, w, h);

  const cols = snap.cw || 1;
  const rows = snap.ch || 1;
  const cw = w / cols;
  const ch = h / rows;
  const styles = snap.y || [];

  for (let y = 0; y < rows; y++) {
    const row = snap.l[y];
    if (!row || !row.c) continue;
    for (let x = 0; x < row.c.length; x++) {
      const cell = row.c[x];
      if (cell === null || cell === "") continue;
      if (typeof cell === "object" && cell.k) continue; // a wide cell's second slot
      const text = typeof cell === "string" ? cell : (cell.c || "");
      const st = typeof cell === "object" && cell.s ? styles[cell.s] : null;
      const bg = (st && decodeColour(st.b)) || null;
      const fg = (st && decodeColour(st.f)) || DEFAULT_FG;
      if (bg) {
        ctx.fillStyle = bg;
        ctx.fillRect(Math.floor(x * cw), Math.floor(y * ch), Math.ceil(cw) + 1, Math.ceil(ch) + 1);
      }
      if (text.trim() !== "") {
        ctx.fillStyle = fg;
        ctx.fillRect(Math.floor(x * cw), Math.floor(y * ch) + 1, Math.max(1, cw - 0.5), Math.max(1, ch - 2));
      }
    }
  }
}

// thumbState is the live picture view: the grid, its one stream and its tiles.
const thumbState = { on: false, es: null, tiles: new Map() };

// thumbsOn reports whether the picture grid is showing.
function thumbsOn() {
  return thumbState.on;
}

// renderThumbs builds the grid from the fleet's active window. Tiles keep the
// arrangement the desk shows, normalised to the box the panes cover, so the
// rail and the tab bar do not eat the phone's screen. One stream carries every
// tile.
function renderThumbs(f) {
  const box = $("thumbs");
  const panes = f.panes.filter((p) => p.active && p.rect && p.rect.w > 0 && p.rect.h > 0);
  const wanted = panes.map((p) => p.id).join(",");
  if (box.dataset.panes === wanted && thumbState.es) {
    // Same panes, new boxes: the desk or a claim moved them, so move the tiles
    // instead of rebuilding the grid and its stream.
    positionTiles(panes);
    return;
  }
  box.dataset.panes = wanted;
  box.textContent = "";
  thumbState.tiles.clear();
  closePictures();
  if (panes.length === 0) {
    box.append(el("div", "empty", "no panes in the active window"));
    return;
  }

  for (const p of panes) {
    const tile = el("a", "tile");
    tile.href = "#pane=" + encodeURIComponent(p.id);
    const frame = el("div", "frame");
    const label = el("div", "label", (p.name || p.app || p.id) + "  " + (p.status || "note"));
    const canvas = el("canvas");
    frame.append(label, canvas);
    tile.append(frame);
    box.append(tile);
    thumbState.tiles.set(p.id, { tile, canvas, snap: null });
  }
  positionTiles(panes);

  const es = new EventSource(api("/api/thumbs?panes=" + encodeURIComponent(wanted)));
  thumbState.es = es;
  es.addEventListener("shot", (ev) => {
    const d = JSON.parse(ev.data);
    const t = thumbState.tiles.get(d.pane);
    if (!t || !d.snap) return;
    t.tile.classList.remove("gone");
    t.snap = d.snap;
    paintShot(t.canvas, d.snap);
  });
  es.addEventListener("gone", (ev) => {
    const d = JSON.parse(ev.data);
    const t = thumbState.tiles.get(d.pane);
    if (t) t.tile.classList.add("gone");
  });
  es.addEventListener("end", () => {
    es.close();
    if (thumbState.es === es) thumbState.es = null;
  });
}

// closePictures hangs up the grid's stream and forgets the pane set, so a page
// in a pocket holds no connection and no tile.
function closePictures() {
  if (thumbState.es) thumbState.es.close();
  thumbState.es = null;
  const box = $("thumbs");
  box.dataset.panes = "";
  thumbState.tiles.clear();
}

// positionTiles places the tiles by their boxes, normalised to the box the
// panes cover, so the rail and the tab bar do not eat the phone's screen.
function positionTiles(panes) {
  let x0 = Infinity, y0 = Infinity, x1 = 0, y1 = 0;
  for (const p of panes) {
    x0 = Math.min(x0, p.rect.x);
    y0 = Math.min(y0, p.rect.y);
    x1 = Math.max(x1, p.rect.x + p.rect.w);
    y1 = Math.max(y1, p.rect.y + p.rect.h);
  }
  const gw = x1 - x0 || 1;
  const gh = y1 - y0 || 1;
  for (const p of panes) {
    const t = thumbState.tiles.get(p.id);
    if (!t) continue;
    t.tile.style.left = ((p.rect.x - x0) / gw) * 100 + "%";
    t.tile.style.top = ((p.rect.y - y0) / gh) * 100 + "%";
    t.tile.style.width = (p.rect.w / gw) * 100 + "%";
    t.tile.style.height = (p.rect.h / gh) * 100 + "%";
  }
}

// repaintThumbs repaints every tile from the last picture it had. A rotation
// changes the canvas size without a new frame from a quiet pane, so without
// this a rotated phone keeps a stretched or one pixel picture.
function repaintThumbs() {
  for (const t of thumbState.tiles.values()) {
    if (t.snap) paintShot(t.canvas, t.snap);
  }
}

// setThumbs shows or hides the picture grid and its stream.
function setThumbs(on) {
  thumbState.on = on;
  document.body.classList.toggle("thumbs", on);
  $("thumbs-toggle").classList.toggle("on", on);
  if (!on) {
    closePictures();
    $("thumbs").textContent = "";
    return;
  }
  if (fleet) renderThumbs(fleet);
}

// --- the clock -----------------------------------------------------------

// startTimers arms the polls. A hidden page arms none, so a phone in a pocket
// makes no request and runs no timer.
function startTimers() {
  if (!TOKEN || document.hidden) return;
  if (!timers.fleet) timers.fleet = setInterval(loadFleet, REFRESH_MS);
  if (pane && !timers.text) timers.text = setInterval(pollText, TEXT_POLL_MS);
}

function stopTimers() {
  if (timers.fleet) clearInterval(timers.fleet);
  if (timers.text) clearInterval(timers.text);
  timers.fleet = null;
  timers.text = null;
}

// route opens the pane the address bar names.
function route() {
  const m = /^#pane=(.+)$/.exec(location.hash);
  if (m) openPane(decodeURIComponent(m[1]));
  else closePane(false);
}

$("pane-close").addEventListener("click", () => closePane());
$("text").addEventListener("scroll", () => {
  if (!pane) return;
  const pre = $("text");
  pane.hold = pre.scrollHeight - pre.scrollTop - pre.clientHeight > 48;
});
$("send-plain").addEventListener("click", () => sendText(false));
$("send-enter").addEventListener("click", () => sendText(true));
$("send-text").addEventListener("keydown", (e) => {
  if (e.key === "Enter") {
    e.preventDefault();
    sendText(true);
  }
});
$("win-prev").addEventListener("click", () => runAction({ id: "prev-window" }));
$("win-next").addEventListener("click", () => runAction({ id: "next-window" }));
$("ws-prev").addEventListener("click", () => runAction({ id: "prev-workspace" }));
$("ws-next").addEventListener("click", () => runAction({ id: "next-workspace" }));
$("sheet-open").addEventListener("click", openSheet);
$("thumbs-toggle").addEventListener("click", () => setThumbs(!thumbsOn()));
window.addEventListener("orientationchange", () => {
  // Text re-wraps on its own; only the pictures need repainting.
  if (thumbsOn()) setTimeout(repaintThumbs, 250);
});
window.addEventListener("resize", () => {
  if (fleet && thumbsOn()) {
    renderThumbs(fleet);
    repaintThumbs();
  }
});
$("sheet-close").addEventListener("click", dismissSheet);
$("sheet").addEventListener("click", (e) => {
  // A tap outside the panel is the sheet's own dismiss.
  if (e.target === $("sheet")) dismissSheet();
});
$("compose").addEventListener("submit", composePost);
window.addEventListener("hashchange", route);
window.addEventListener("popstate", () => {
  if (sheetOpen) closeSheet();
});
document.addEventListener("visibilitychange", () => {
  if (document.hidden) {
    stopTimers();
    closePictures();
    return;
  }
  loadFleet();
  if (pane) pollText();
  startTimers();
});

renderPaneActions();

if (!TOKEN) {
  banner("this page needs its token: open the link with ?t=... on it");
} else {
  loadFleet();
  startTimers();
}
route();
