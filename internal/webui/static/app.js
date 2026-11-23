// dm web: the phone's view of a session. The fleet polls, one pane streams.
// Everything arrives already shaped by the server, so this file only paints.

const REFRESH_MS = 3000;
const TEXT_POLL_MS = 1000;

let fleet = null;
// tab is the view on screen, and dirty says which view a newer fleet left to
// paint. A tab paints its own rows on the way in, and nothing paints a view
// nobody is looking at.
let tab = "panes";
const dirty = { panes: true, box: true };
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
// repaintTimer is the one shot a rotation arms, held so a hide can clear it.
let repaintTimer = null;

const $ = (id) => document.getElementById(id);

// TOKEN is the access token this load was opened with, when the link carried
// one. The server sets the cookie on that first load, and the cookie carries
// every later request. The token stays in memory only as the fallback for a
// caller that holds a token and no cookie.
const TOKEN = new URLSearchParams(location.search).get("t") || "";

// The address bar is cleaned at once, so a copied URL or a screenshot hands
// over nothing. The cookie this load set is the credential from here on.
if (TOKEN) {
  const rest = new URLSearchParams(location.search);
  rest.delete("t");
  const search = rest.toString();
  history.replaceState(null, "", location.pathname + (search ? "?" + search : "") + location.hash);
}

// queryToken is true once the cookie has proved unusable: the request that
// answers 401 with a token in hand retries with the token in the query, and
// every later one rides it too.
let queryToken = false;

// api renders an endpoint path, carrying the token in the query only when the
// cookie did not stick.
function api(path) {
  if (!queryToken || !TOKEN) return path;
  return path + (path.includes("?") ? "&" : "?") + "t=" + encodeURIComponent(TOKEN);
}

// fetchAPI is one request. It retries once with the token in the query when the
// cookie is missing, which is the one case the in-memory token is for.
async function fetchAPI(path, opts) {
  const res = await fetch(api(path), opts);
  if (res.status !== 401 || !TOKEN || queryToken) return res;
  // The retry is another request, so it is reply work and waits for a visible
  // page. A dropped retry leaves queryToken alone, so the next visible call
  // takes the fallback.
  const again = applyReply(() => {
    queryToken = true;
    return fetch(api(path), opts);
  });
  return again ?? res;
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

// applyReply runs the work a server reply asks for, and drops it when the page
// is hidden. The hide path closes the streams and clears the timers; this is the
// other half of the same rule, so a reply that lands after the hide changes no
// DOM, opens no stream and starts no request.
//
// Every continuation after an await runs through it, and the two things a
// hidden page must never start sit behind it as well: renderThumbs is the only
// place an EventSource is created, and fetchAPI's retry is the only request a
// reply can send. A path added later that follows the same shape cannot miss
// the rule.
function applyReply(work) {
  if (document.hidden) return undefined;
  return work();
}

// loadFleet asks for the fleet and applies the reply. A hidden tab does not
// poll: a phone in a pocket should not keep a session awake for a screen nobody
// reads.
async function loadFleet() {
  if (document.hidden) return;
  let f;
  try {
    const res = await fetchAPI("/api/fleet");
    if (!res.ok) {
      applyReply(() => banner(res.status === 401
        ? "this page needs its token: open the link with ?t=... on it"
        : "cannot reach the session: " + res.status));
      return;
    }
    f = await res.json();
  } catch (e) {
    applyReply(() => banner("cannot reach the session: " + e));
    return;
  }
  applyReply(() => paintFleet(f));
}

// paintFleet applies one fleet reply. It runs through applyReply, so a reply
// that lands after the hide paints nothing and builds no grid.
function paintFleet(f) {
  fleet = f;
  banner(f.error || "");
  // Both views are stale now; only the one on screen is painted.
  dirty.panes = true;
  dirty.box = true;
  renderVisible();
  if (thumbsOn() && tab === "panes") renderThumbs(f);
  $("session").textContent = "dm " + f.session;
  renderSummary(f);
  applyReadOnly(!!f.readOnly);
  if (pane) {
    if (!f.panes.some((p) => p.id === pane.id)) closePane();
    else renderPaneChrome();
  }
}

// applyReadOnly hides the commands that write, so the page never offers a tap
// that answers 403. Views stay: the strip's arrows, the picture toggle and the
// grid only read, and a read only page that hid them would hide the session
// itself.
const writeControls = ["sheet-open", "compose", "send", "pane-actions"];

function applyReadOnly(ro) {
  for (const id of writeControls) {
    $(id).style.display = ro ? "none" : "";
  }
  if (ro) dismissSheet();
}

// --- the tabs ------------------------------------------------------------
//
// Panes and Box are two views of one fleet reply. The tab on screen owns the
// header's count line, and the address bar carries the tab, so a reload or a
// shared link lands on the same one.

// STATE_WORDS are the five words a row paints. A pane with any other state, an
// idle shell or one that never reported, has no word this page can print.
const STATE_WORDS = ["note", "working", "blocked", "done", "error"];

// STATE_RANK puts the rows that want a decision first.
const STATE_RANK = { blocked: 0, error: 1, working: 2, done: 3, note: 4 };

// stateOf is the word one pane paints, or "" when it has no agent status. The
// dot and the word are one thing, so a row with no word paints no dot either.
function stateOf(p) {
  return STATE_WORDS.indexOf(p.status) >= 0 ? p.status : "";
}

// paneHash is the address of the panes view with a pane open in it.
function paneHash(id) {
  return "#panes&pane=" + encodeURIComponent(id);
}

// setTab shows one view. It repaints the view it shows when a newer fleet left
// it stale, and touches nothing the other view paints.
function setTab(next) {
  const want = next === "box" ? "box" : "panes";
  const on = $("tab-" + want);
  for (const t of document.querySelectorAll("nav.tabs a")) {
    t.setAttribute("aria-selected", t === on ? "true" : "false");
  }
  if (want === tab && document.body.classList.contains("tab-" + want)) return;
  tab = want;
  document.body.classList.toggle("tab-panes", tab === "panes");
  document.body.classList.toggle("tab-box", tab === "box");
  if (!fleet) return;
  renderSummary(fleet);
  renderVisible();
  // The picture grid belongs to the panes view: leaving it hangs up the
  // stream, and coming back builds the grid again.
  if (tab === "panes" && thumbsOn()) renderThumbs(fleet);
  else closePictures();
}

// renderVisible paints the view on screen and leaves the other one stale.
function renderVisible() {
  if (!fleet) return;
  if (tab === "panes" && dirty.panes) {
    renderPanes(fleet);
    dirty.panes = false;
  }
  if (tab === "box" && dirty.box) {
    renderBox(fleet);
    dirty.box = false;
  }
}

// renderSummary is the header line: the state counts on the panes tab, and the
// box entries waiting on a decision on the box tab.
function renderSummary(f) {
  $("counts").textContent = tab === "box" ? boxSummary(f) : paneSummary(f);
}

// paneSummary names each state the fleet holds, blocked and error first. It
// counts the same five words a row paints, and names the panes leaving that
// column empty as well, so the line matches the list a thumb scrolls.
function paneSummary(f) {
  const counts = {};
  let quiet = 0;
  for (const p of f.panes) {
    const s = stateOf(p);
    if (s) counts[s] = (counts[s] || 0) + 1;
    else quiet++;
  }
  const order = ["blocked", "error", "working", "done", "note"];
  const parts = [];
  for (const s of order) {
    if (counts[s]) parts.push(counts[s] + " " + s);
  }
  if (quiet > 0) parts.push(quiet + " no agent");
  return parts.length > 0 ? parts.join("  ") : "no panes";
}

// boxSummary counts the entries that want an answer: the lanes that left a
// message blocked or in error.
function boxSummary(f) {
  let n = 0;
  for (const e of f.box) {
    if (e.state === "blocked" || e.state === "error") n++;
  }
  return n + " waiting";
}

// renderPanes lists the fleet, blocked and error first: the rows that want a
// decision are the ones a phone should show without scrolling.
function renderPanes(f) {
  const box = $("panes");
  box.textContent = "";
  const panes = f.panes.slice().sort((a, b) => {
    const d = (STATE_RANK[stateOf(a)] ?? 5) - (STATE_RANK[stateOf(b)] ?? 5);
    return d !== 0 ? d : a.id.localeCompare(b.id);
  });
  if (panes.length === 0) {
    box.append(el("div", "empty", "no panes"));
    return;
  }
  for (const p of panes) box.append(paneRow(p));
}

// paneRow is one pane: the name a person would use on the first line, its
// state on the right of it, and the facts that place it on the second.
function paneRow(p) {
  const row = el("a", "row");
  row.href = paneHash(p.id);

  const name = p.name || p.app || p.id;
  const title = el("div", "title");
  title.append(el("span", "name", name));
  const state = stateOf(p);
  if (state) {
    const st = el("span", "state " + state);
    st.append(el("span", "dot " + state));
    st.append(document.createTextNode(state));
    title.append(st);
  }
  row.append(title);

  // The facts, each printed once: the id unless it is the name above, the
  // place it sits in, the branch with its diff, and the folder trimmed to its
  // tail. Each is a chip of its own, so a gap separates them and a line that
  // wraps leaves nothing dangling at its end.
  const where = el("div", "where");
  if (name !== p.id) where.append(el("span", "id", p.id));
  // Workspace then window, the way the rail names the pair.
  const window = p.windowName || p.window;
  if (p.workspaceName || p.workspace) {
    const place = el("span", "", p.workspaceName || p.workspace);
    if (window) place.append(document.createTextNode(" > " + window));
    where.append(place);
  } else if (window) {
    where.append(el("span", "", window));
  }
  if (p.branch) {
    const branch = el("b", "", p.branch);
    if (p.dirty) branch.append(document.createTextNode(" +" + (p.added || 0) + "/-" + (p.deleted || 0)));
    where.append(branch);
  }
  if (p.cwd) {
    const cwd = el("span", "cwd");
    // The path keeps its reading order inside the box that clips its head.
    const tail = document.createElement("bdi");
    tail.setAttribute("dir", "ltr");
    tail.textContent = p.cwd;
    cwd.append(tail);
    cwd.title = p.cwd;
    where.append(cwd);
  }
  if (where.childNodes.length > 0) row.append(where);

  if (p.message) row.append(el("div", "msg", p.message));
  if (p.active) row.append(el("div", "msg", "on screen at the desk"));
  return row;
}

// renderBox lists the box rows, newest first.
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
    // The key the entry answers to leads; the pane that wrote it follows.
    title.append(el("span", "name", e.key || "-"));
    title.append(el("span", "id", e.from || "?"));
    const state = STATE_WORDS.indexOf(e.state) >= 0 ? e.state : "note";
    const st = el("span", "state " + state);
    st.append(el("span", "dot " + state));
    st.append(document.createTextNode(state));
    title.append(st);
    row.append(title);
    if (e.msg) row.append(el("div", "msg", e.msg));
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
    res = await fetchAPI("/api/action", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
  } catch (e) {
    applyReply(() => banner("action failed: " + e));
    return null;
  }
  if (!res.ok) {
    const refused = await res.text();
    applyReply(() => banner(refused));
    return null;
  }
  const text = await res.text();
  await applyReply(async () => {
    banner("");
    await loadFleet();
    if (pane) pollText();
  });
  return text ? JSON.parse(text) : {};
}

// arm turns one button into its own question and freezes the request the
// question names. The confirmation sends that request exactly, so a pane or
// window that went away since gets the daemon's refusal, and no other one is
// hit.
function arm(btn, question, body) {
  armed = { el: btn, label: btn.textContent, body };
  btn.textContent = question;
  btn.classList.add("armed");
}

// disarm restores the button and forgets the question. Every other command, a
// new target, a new question and a sheet close run through here, so one voice
// owns the armed state.
function disarm() {
  if (!armed) return;
  armed.el.textContent = armed.label;
  armed.el.classList.remove("armed");
  armed = null;
}

// sendBody runs one finished request and settles the view after it.
async function sendBody(body) {
  const res = await runAction(body);
  if (res === null) return;
  applyReply(() => {
    if (body.id === "close-pane" && pane && body.target === pane.id) closePane();
  });
}

// dispatch sends one command. A command that destroys something asks once, with
// its target named, and only the second tap sends, exactly what was named.
// Every other command takes the question away first.
async function dispatch(btn, body, question) {
  if (!question) {
    disarm();
    return sendBody(body);
  }
  if (armed && armed.el === btn) {
    const frozen = armed.body;
    disarm();
    return sendBody(frozen);
  }
  disarm();
  arm(btn, question, body);
}

// --- the sheet -----------------------------------------------------------

// loadCommands fetches the catalogue once.
async function loadCommands() {
  if (catalogue) return catalogue;
  let rows;
  try {
    const res = await fetchAPI("/api/commands");
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
  applyReply(() => {
    renderSheet(rows);
    sheetOpen = true;
    $("sheet").classList.add("open");
    // Push one entry, so the back gesture is the sheet's own dismiss.
    history.pushState({ sheet: 1 }, "", location.href);
    sheetPushed = true;
  });
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
    if (name === null) {
      // A cancelled prompt sends nothing, and it is still another command: the
      // question on the row goes with it.
      disarm();
      return;
    }
    body.name = name;
  }
  const question = cmd.id === "close-pane" || cmd.id === "close-window"
    ? "close " + (body.target || "") + "?"
    : "";
  await dispatch(btn, body, question);
}

// --- the pane view -------------------------------------------------------

// paneActions are the pane's own commands, under its header. Each acts on the
// open pane, so the phone never has to open the sheet for them.
//
// zoom-pane and next-pane carry no target on the wire: the server acts on the
// active window's focus (server_rpc.go:319, :425). focusFirst sends focus for the
// open pane first, so zoom zooms the pane on screen and next pane steps from the
// pane on screen instead of from whatever the desk happened to focus.
const paneActions = [
  { label: "split right", body: () => ({ id: "split", target: pane.id, site: "right" }) },
  { label: "split down", body: () => ({ id: "split", target: pane.id, site: "down" }) },
  { label: "zoom", body: () => ({ id: "zoom-pane" }), focusFirst: true },
  { label: "next pane", body: () => ({ id: "next-pane" }), focusFirst: true },
  { label: "rename", body: () => ({ id: "set-pane-name", target: pane.id }) },
  { label: "close", body: () => ({ id: "close-pane", target: pane.id }), ask: () => "close " + pane.id + "?" },
];

// actOnPane runs a command the server targets at the session's focus. The focus
// goes to the open pane first, so a refused focus stops before the command.
//
// The view follows the desk after the command: next pane steps the session's
// focus, and a phone that kept looking at the old pane would see nothing happen.
// The fleet the command just refreshed names the pane the session now stands on,
// so no extra request is needed.
async function actOnPane(body) {
  if (!pane) return;
  disarm();
  const focused = await runAction({ id: "focus", target: pane.id });
  if (focused === null) return;
  const stepped = await runAction(body);
  if (stepped === null) return;
  applyReply(() => {
    const next = focusedPaneID();
    if (next && pane && next !== pane.id) location.hash = paneHash(next);
  });
}

// focusedPaneID is the pane the session stands on: the active window's focus.
// The fleet carries both flags, so one lookup answers it.
function focusedPaneID() {
  const panes = (fleet && fleet.panes) || [];
  const at = panes.find((p) => p.active && p.focus) || panes.find((p) => p.focus);
  return at ? at.id : "";
}

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
        if (name === null) {
          disarm();
          return;
        }
        body.name = name;
      }
      if (act.focusFirst) {
        actOnPane(body);
        return;
      }
      dispatch(btn, body, act.ask ? act.ask() : "");
    });
    box.append(btn);
  }
}

// paneContext is the window and workspace the open pane belongs to. The strip
// names this pane's place, not the session's focus: watching a pane must not
// report another window, and must not move the desk to read it.
function paneContext() {
  const panes = (fleet && fleet.panes) || [];
  return panes.find((p) => pane && p.id === pane.id) || panes.find((p) => p.active) || panes[0] || {};
}

// orderedIDs lists the window or workspace ids in counter order, so prev and
// next follow the rail's order rather than the fleet's status sort.
function orderedIDs(kind) {
  const panes = (fleet && fleet.panes) || [];
  const seen = new Set();
  for (const p of panes) {
    const id = kind === "window" ? p.window : p.workspace;
    if (id) seen.add(id);
  }
  return [...seen].sort((a, b) => Number(a.slice(1)) - Number(b.slice(1)));
}

// stepPlace opens the pane of the neighbouring window or workspace. The strip
// walks the session through the view alone: no command reaches the socket and
// the desk keeps its focus.
function stepPlace(kind, step) {
  const ids = orderedIDs(kind);
  const own = paneContext()[kind];
  const at = ids.indexOf(own);
  if (at < 0 || ids.length < 2) return;
  const next = ids[(at + step + ids.length) % ids.length];
  const panes = (fleet && fleet.panes) || [];
  const inside = panes.filter((p) => (kind === "window" ? p.window : p.workspace) === next);
  const pick = inside.find((p) => p.focus) || inside[0];
  if (pick) location.hash = paneHash(pick.id);
}

function renderPaneChrome() {
  if (!pane) return;
  const own = ((fleet && fleet.panes) || []).find((p) => p.id === pane.id) || {};
  $("pane-title").textContent = (own.name || own.app || pane.id) + "  " + pane.id;
  const at = paneContext();
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
  $("send").style.display = fleet && fleet.readOnly ? "none" : "";
  $("pane-state").textContent = "";
  markFollow();
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
  const at = pane;
  if (!at || document.hidden) return;
  let d;
  try {
    const res = await fetchAPI("/api/text?pane=" + encodeURIComponent(at.id) + "&since=" + encodeURIComponent(at.rev));
    if (!res.ok) {
      const refused = await res.text();
      applyReply(() => banner(refused));
      return;
    }
    d = await res.json();
  } catch (e) {
    applyReply(() => banner("cannot read the pane: " + e));
    return;
  }
  applyReply(() => paintText(at, d));
}

// FOLLOW_GAP is how far from the bottom counts as reading back through the
// text rather than following it.
const FOLLOW_GAP = 48;

// awayFromBottom reports whether the reader has scrolled up out of the follow.
function awayFromBottom() {
  const pre = $("text");
  return pre.scrollHeight - pre.scrollTop - pre.clientHeight > FOLLOW_GAP;
}

// markFollow settles the follow state and the latest control from the reader's
// place in the text. One rule owns both, so a scroll, a poll and a tap on the
// control cannot disagree.
function markFollow() {
  const away = !!pane && awayFromBottom();
  if (pane) pane.hold = away;
  $("latest").classList.toggle("show", away);
}

// jumpLatest returns to the bottom and follows it again. The text keeps the
// next tap: the control is only there while the reader is away from it.
function jumpLatest() {
  const pre = $("text");
  pre.scrollTop = pre.scrollHeight;
  if (pane) pane.hold = false;
  $("latest").classList.remove("show");
}

// paintText applies one pane reply, and only to the pane that asked for it: a
// reply that lands after another pane was opened is not painted into it.
function paintText(at, d) {
  if (pane !== at) return;
  at.rev = d.rev;
  $("pane-state").textContent = [d.state, d.message].filter(Boolean).join(": ");
  $("pane-state").className = "state " + (d.state || "note");
  if (!d.changed) return;
  const pre = $("text");
  pre.textContent = (d.lines || []).join("\n");
  // Follow the bottom, which is where a running agent's output is, unless the
  // reader scrolled up to read something.
  if (!at.hold) pre.scrollTop = pre.scrollHeight;
  markFollow();
}

// closePane stops the poll and, when asked, clears the address bar.
function closePane(updateHash = true) {
  if (!pane) return;
  pane = null;
  disarm();
  $("text").textContent = "";
  markFollow();
  $("pane").classList.remove("open");
  stopTimers();
  startTimers();
  // The pane closes back onto the tab it was opened from.
  if (updateHash) {
    history.replaceState(null, "", location.pathname + location.search + "#" + tab);
  }
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
  disarm();
  const key = $("compose-key").value.trim();
  const msg = $("compose-msg").value.trim();
  if (!key || !msg) {
    banner("the box wants a key and a message");
    focusCompose();
    return;
  }
  const res = await runAction({ id: "msg-post", key, state: $("compose-state").value, msg });
  if (res === null) return;
  applyReply(() => {
    $("compose-key").value = "";
    $("compose-msg").value = "";
  });
}

function sendText(enter) {
  if (!pane) return;
  disarm();
  const input = $("send-text");
  post("/api/msg", { pane: pane.id, text: input.value, enter });
  input.value = "";
}

// post writes one key message into the pane and reports a refusal in the
// banner.
async function post(path, body) {
  try {
    const res = await fetchAPI(path, {
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

// MIN_TILE is the smallest tile a thumb can hit. A grid whose tiles fall below
// it is a picture, not a set of links: it keeps the desk's arrangement, the
// tiles stop being links, and the pane list below it stays the way in.
const MIN_TILE = 44;

// renderThumbs builds the grid from the fleet's active window. Tiles keep the
// arrangement the desk shows, normalised to the box the panes cover, so the
// rail and the tab bar do not eat the phone's screen. One stream carries every
// tile.
function renderThumbs(f) {
  // The one place this page creates an EventSource, and it will not do it on a
  // hidden page.
  return applyReply(() => paintThumbs(f));
}

// paintThumbs builds the grid and its stream. It runs through renderThumbs.
function paintThumbs(f) {
  const box = $("thumbs");
  // The fleet publishes the stream's pane limit, so the grid asks for exactly
  // the panes it paints, first ones first. One place decides; both agree.
  let panes = f.panes.filter((p) => p.active && p.rect && p.rect.w > 0 && p.rect.h > 0);
  if (f.maxPictures > 0) panes = panes.slice(0, f.maxPictures);
  const wanted = panes.map((p) => p.id).join(",");
  if (box.dataset.panes === wanted && thumbState.es) {
    // Same panes, new boxes: the desk or a claim moved them, so move the tiles
    // instead of rebuilding the grid and its stream.
    positionTiles(panes);
    applyTileRule();
    return;
  }
  // Close the old stream before the key moves, so the key always names the set
  // the live stream carries. A poll that changes nothing keeps its one stream.
  closePictures();
  box.dataset.panes = wanted;
  box.textContent = "";
  if (panes.length === 0) {
    box.append(el("div", "empty", "no panes in the active window"));
    return;
  }

  for (const p of panes) {
    // No href yet: applyTileRule links a tile only once it measures thumb sized.
    const tile = el("a", "tile");
    const frame = el("div", "frame");
    const state = stateOf(p);
    const label = el("div", "label", (p.name || p.app || p.id) + (state ? "  " + state : ""));
    const canvas = el("canvas");
    frame.append(label, canvas);
    tile.append(frame);
    box.append(tile);
    thumbState.tiles.set(p.id, { pane: p.id, tile, canvas, snap: null });
  }
  positionTiles(panes);
  applyTileRule();

  const es = new EventSource(api("/api/thumbs?panes=" + encodeURIComponent(wanted)));
  thumbState.es = es;
  es.onerror = () => {
    // A stream cannot retry itself with the token, so a page whose cookie did
    // not stick flips the fallback here and rebuilds once.
    if (queryToken || !TOKEN) return;
    applyReply(() => {
      queryToken = true;
      closePictures();
      if (fleet) renderThumbs(fleet);
    });
  };
  es.addEventListener("shot", (ev) => {
    const d = JSON.parse(ev.data);
    applyReply(() => {
      const t = thumbState.tiles.get(d.pane);
      if (!t || !d.snap) return;
      t.tile.classList.remove("gone");
      t.snap = d.snap;
      paintShot(t.canvas, d.snap);
    });
  });
  es.addEventListener("gone", (ev) => {
    const d = JSON.parse(ev.data);
    applyReply(() => {
      const t = thumbState.tiles.get(d.pane);
      if (t) t.tile.classList.add("gone");
    });
  });
  es.addEventListener("end", () => {
    es.close();
    if (thumbState.es === es) thumbState.es = null;
  });
}

// closePictures hangs up the grid's stream and forgets its tiles, so a page in
// a pocket holds no connection. The pane key stays: it names the set the tiles
// were built for, and the guard above reads it with the stream's own state.
function closePictures() {
  if (thumbState.es) thumbState.es.close();
  thumbState.es = null;
  thumbState.tiles.clear();
}

// applyTileRule makes a tile a link only when it is a thumb sized target. A
// dense grid keeps its picture and gives up its links, so a stray tap cannot
// open a pane nobody meant to open, and the pane list below the grid stays
// visible as the way in.
function applyTileRule() {
  let live = thumbState.tiles.size > 0;
  for (const t of thumbState.tiles.values()) {
    const box = t.tile.getBoundingClientRect();
    if (box.width < MIN_TILE || box.height < MIN_TILE) live = false;
  }
  document.body.classList.toggle("grid-live", live);
  for (const t of thumbState.tiles.values()) {
    if (live) t.tile.href = paneHash(t.pane);
    else t.tile.removeAttribute("href");
  }
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
    document.body.classList.remove("grid-live");
    $("thumbs").textContent = "";
    delete $("thumbs").dataset.panes;
    return;
  }
  // The grid only exists in the panes view, so the box tab opens no stream.
  if (fleet && tab === "panes") renderThumbs(fleet);
}

// --- the clock -----------------------------------------------------------

// startTimers arms the polls. A hidden page arms none, so a phone in a pocket
// makes no request and runs no timer.
function startTimers() {
  if (document.hidden) return;
  if (!timers.fleet) timers.fleet = setInterval(loadFleet, REFRESH_MS);
  if (pane && !timers.text) timers.text = setInterval(pollText, TEXT_POLL_MS);
}

function stopTimers() {
  if (timers.fleet) clearInterval(timers.fleet);
  if (timers.text) clearInterval(timers.text);
  timers.fleet = null;
  timers.text = null;
}

// route shows the tab the address bar names and opens the pane it names. The
// hash is the whole view: #panes, #box, and #panes&pane=w1p1 for a pane.
function route() {
  // A link from before the tabs still names a pane alone, so it opens that pane
  // on the panes tab.
  const parts = location.hash.slice(1).split("&");
  if (/^pane=/.test(parts[0])) parts.unshift("panes");
  setTab(parts[0]);
  const m = /^pane=(.+)$/.exec(parts[1] || "");
  const id = m ? decodeURIComponent(m[1]) : "";
  if (id && (!pane || pane.id !== id)) openPane(id);
  else if (!id && pane) closePane(false);
}

$("pane-close").addEventListener("click", () => closePane());
$("text").addEventListener("scroll", () => {
  if (!pane) return;
  markFollow();
});
$("latest").addEventListener("click", jumpLatest);
$("send-plain").addEventListener("click", () => sendText(false));
$("send-enter").addEventListener("click", () => sendText(true));
$("send-text").addEventListener("keydown", (e) => {
  if (e.key === "Enter") {
    e.preventDefault();
    sendText(true);
  }
});
$("win-prev").addEventListener("click", () => stepPlace("window", -1));
$("win-next").addEventListener("click", () => stepPlace("window", 1));
$("ws-prev").addEventListener("click", () => stepPlace("workspace", -1));
$("ws-next").addEventListener("click", () => stepPlace("workspace", 1));
$("sheet-open").addEventListener("click", openSheet);
$("thumbs-toggle").addEventListener("click", () => setThumbs(!thumbsOn()));
window.addEventListener("orientationchange", () => {
  // Text re-wraps on its own; only the pictures need repainting. The handle is
  // held so a hide inside the window clears it: a hidden page runs no timer.
  if (document.hidden || !thumbsOn()) return;
  clearTimeout(repaintTimer);
  repaintTimer = setTimeout(() => {
    repaintTimer = null;
    repaintThumbs();
  }, 250);
});
window.addEventListener("resize", () => {
  // A resize while hidden must not reopen the stream the hide closed.
  if (document.hidden || !fleet || !thumbsOn() || tab !== "panes") return;
  renderThumbs(fleet);
  repaintThumbs();
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
    // A rotation a moment ago may have armed the repaint; it goes with the
    // rest, so nothing runs while the page is in a pocket. Replies already in
    // flight are dropped by applyReply, not here.
    if (repaintTimer) {
      clearTimeout(repaintTimer);
      repaintTimer = null;
    }
    stopTimers();
    closePictures();
    return;
  }
  loadFleet();
  if (pane) pollText();
  startTimers();
});

renderPaneActions();

// The address bar carries the tab, so a reload lands where the reader stood; a
// page opened without one starts on the panes.
route();
if (!location.hash) {
  history.replaceState(null, "", location.pathname + location.search + "#" + tab);
}

// A reloaded page carries no token in the query, and the cookie is the
// credential. The fleet answers 401 when neither is usable, and that is where
// the token hint comes from.
loadFleet();
startTimers();
