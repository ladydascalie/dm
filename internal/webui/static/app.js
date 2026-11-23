// dm web: the phone's view of a session. The fleet polls, one pane streams.
// Everything arrives already shaped by the server, so this file only paints.

const REFRESH_MS = 3000;
// TEXT_POLL_MS is the cadence one pane is read at. The session read rides the
// same cadence the pane text had before the views, and only one of them runs:
// the view on screen decides which.
const TEXT_POLL_MS = 1000;

let fleet = null;
// tab is the view on screen, and dirty says which view a newer fleet left to
// paint. A tab paints its own rows on the way in, and nothing paints a view
// nobody is looking at.
let tab = "panes";
const dirty = { panes: true, box: true };
// pane is the open pane: its id, the view on screen, the session it read, and
// the polls that view runs.
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
const timers = { fleet: null, text: null, session: null };
// repaintTimer is the one shot a rotation arms, held so a hide can clear it.
let repaintTimer = null;
// openShells names the projects whose shell panes are unfolded, so a poll does
// not fold a list the reader just opened.
const openShells = new Set();
// seen is when this page first saw each pane's status and message, keyed by
// pane id. A row has no timestamp of its own, so this is the fallback clock a
// needs-you card measures from.
const seen = new Map();
// needCards maps a pane id to the card's ago line, so a poll that changes no
// card still moves its clock and keeps the strip's sideways scroll.
const needCards = new Map();
let needKey = "";

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
  noteSeen(f);
  // Both views are stale now; only the one on screen is painted.
  dirty.panes = true;
  dirty.box = true;
  renderVisible();
  renderNeeds(f);
  if (thumbsOn() && tab === "panes") renderThumbs(f);
  $("session").textContent = "dm " + f.session;
  renderSummary(f);
  applyReadOnly(!!f.readOnly);
  if (pane) {
    if (!f.panes.some((p) => p.id === pane.id)) closePane();
    else {
      renderPaneChrome();
      // A pane the fleet now calls an agent may have written its first session
      // line since the last try, so the page asks again rather than staying on
      // the screen for good.
      const row = f.panes.find((p) => p.id === pane.id);
      if (row && hasSession(row) && pane.session === "none") pollSession();
    }
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

// ACCENTS is how many accent slots the rail carries. A project's carried slot
// names its hue, and the page paints the same six colours.
const ACCENTS = 6;

// NEED_STATES are the two states that want a decision now. A pane in either one
// is pinned in the strip, so it is never below a fold.
const NEED_STATES = ["blocked", "error"];

// accentOf is the class that carries a project's hue. Slot zero is the first
// accent, and a row that carries no slot lands there too.
function accentOf(p) {
  const slot = (p.projectAccent || 0) % ACCENTS;
  return "a" + (slot < 0 ? slot + ACCENTS : slot);
}

// sortWithin is one project's row order: the states that want a decision first,
// then the id, which is the rail's own tie break.
function sortWithin(a, b) {
  const d = (STATE_RANK[stateOf(a)] ?? 5) - (STATE_RANK[stateOf(b)] ?? 5);
  return d !== 0 ? d : a.id.localeCompare(b.id);
}

// projectGroups folds the fleet into one group per project, in the rail's own
// order: the first pane's place decides where a project sits.
function projectGroups(panes) {
  const groups = new Map();
  for (const p of panes) {
    const key = p.project || "";
    let g = groups.get(key);
    if (!g) {
      g = { key, name: p.project || "no project", accent: accentOf(p), panes: [] };
      groups.set(key, g);
    }
    g.panes.push(p);
  }
  return [...groups.values()];
}

// renderPanes lists the fleet by project. Every blocked or error pane is pinned
// in the strip as well, so the rows a phone must not miss are on the first
// screen whichever project owns them.
function renderPanes(f) {
  const box = $("panes");
  box.textContent = "";
  if (f.panes.length === 0) {
    box.append(el("div", "empty", "no panes"));
    return;
  }
  for (const g of projectGroups(f.panes)) box.append(projectSection(g));
}

// hasSession is the fleet's answer to the only question the fold asks: does
// this pane have a session to read. The agent flag is a different question: a
// pane can report a session and no status, and a pi pane that has written
// nothing yet has neither.
function hasSession(p) {
  return !!p.session;
}

// projectSection is one project: its accent mark, its name, the count of panes
// and agents it holds, then its rows and its folded shells.
function projectSection(g) {
  const sec = el("section", "project");
  const head = el("h2", "proj " + g.accent);
  head.append(el("span", "mark"));
  head.append(el("span", "pname", g.name));
  head.append(el("span", "pcount", sectionCount(g.panes)));
  sec.append(head);
  const agents = g.panes.filter(hasSession).sort(sortWithin);
  const shells = g.panes.filter((p) => !hasSession(p));
  for (const p of agents) sec.append(paneRow(p));
  if (shells.length > 0) {
    sec.append(shellFold(g, shells.length));
    if (openShells.has(g.key)) for (const p of shells.sort(sortWithin)) sec.append(paneRow(p));
  }
  return sec;
}

// sectionCount is a project's own summary: how many panes it holds and how many
// of them carry a session, which is what makes a pane an agent to the phone.
function sectionCount(panes) {
  const n = panes.length;
  const agents = panes.filter(hasSession).length;
  return n + (n === 1 ? " pane" : " panes") + ", " + agents + (agents === 1 ? " agent" : " agents");
}

// shellFold is the line a project's shell panes fold to. It is one tap target
// that opens their rows, so a folder is still reachable without a row each.
function shellFold(g, count) {
  const open = openShells.has(g.key);
  const btn = el("button", "fold");
  btn.type = "button";
  btn.setAttribute("aria-expanded", open ? "true" : "false");
  btn.append(el("span", "caret", open ? "\u25be" : "\u25b8"));
  btn.append(el("span", "", count + (count === 1 ? " shell pane" : " shell panes")));
  btn.addEventListener("click", () => {
    if (open) openShells.delete(g.key);
    else openShells.add(g.key);
    dirty.panes = true;
    renderVisible();
  });
  return btn;
}

// renderNeeds paints the needs-you strip: one card per blocked or error pane,
// with the project, the place, the agent's message and how long it has wanted an
// answer. A strip with nothing to say is absent.
function renderNeeds(f) {
  const box = $("needs");
  const need = f.panes.filter((p) => NEED_STATES.includes(stateOf(p)));
  need.sort((a, b) => {
    const d = NEED_STATES.indexOf(stateOf(a)) - NEED_STATES.indexOf(stateOf(b));
    return d !== 0 ? d : wantSince(a, f) - wantSince(b, f);
  });
  // The cards are rebuilt only when one of them changes; a poll that changes
  // none only moves their clocks, so the strip keeps its sideways scroll.
  const key = need.map((p) => p.id + ":" + stateOf(p) + ":" + (p.message || "")).join("|");
  if (key === needKey) {
    for (const p of need) {
      const c = needCards.get(p.id);
      if (c) c.ago.textContent = agoText(wantSince(p, f));
    }
    return;
  }
  needKey = key;
  needCards.clear();
  box.textContent = "";
  if (need.length === 0) {
    box.classList.remove("show");
    return;
  }
  for (const p of need) {
    const ago = el("span", "ago", agoText(wantSince(p, f)));
    const card = el("a", "card " + accentOf(p) + " " + stateOf(p));
    card.href = paneHash(p.id);
    const head = el("div", "chead");
    head.append(el("span", "cname", p.project || "no project"));
    const place = placeOf(p);
    if (place) head.append(el("span", "cplace", place));
    head.append(el("span", "cwant state " + stateOf(p), stateOf(p)));
    head.append(ago);
    card.append(head);
    card.append(el("div", "cmsg", p.message || "no message"));
    needCards.set(p.id, { ago });
    box.append(card);
  }
  box.classList.add("show");
}

// placeOf is a pane's place in the rail's own words: the workspace, then the
// window inside it.
function placeOf(p) {
  const ws = p.workspaceName || p.workspace || "";
  const win = p.windowName || p.window || "";
  if (ws && win) return ws + " > " + win;
  return ws || win;
}

// wantSince is when a pane began to want an answer. A box entry the pane wrote
// carries the daemon's clock, so it answers first; a pane that wrote none falls
// back to when this page first saw the state.
function wantSince(p, f) {
  let at = 0;
  for (const e of f.box || []) {
    if (e.from !== p.id || NEED_STATES.indexOf(e.state) < 0) continue;
    const t = Date.parse(e.at) || 0;
    if (t > at) at = t;
  }
  if (at > 0) return at;
  const was = seen.get(p.id);
  return was ? was.at : Date.now();
}

// noteSeen stamps each pane's state and message, so a card can say how long the
// pane has been that way for as long as this page watches it.
function noteSeen(f) {
  const now = Date.now();
  for (const p of f.panes) {
    const sig = (p.status || "") + "\u0000" + (p.message || "");
    const was = seen.get(p.id);
    if (!was || was.sig !== sig) seen.set(p.id, { sig, at: now });
  }
}

// agoText is a duration a thumb can read.
function agoText(ms) {
  const secs = Math.max(0, Math.round((Date.now() - ms) / 1000));
  if (secs < 45) return "just now";
  const mins = Math.round(secs / 60);
  if (mins < 60) return Math.max(1, mins) + "m ago";
  const hours = Math.round(mins / 60);
  if (hours < 24) return hours + "h ago";
  return Math.round(hours / 24) + "d ago";
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
    if (e.from) title.append(el("span", "id", e.from));
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
    refreshPane();
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
    return panes.map((p) => {
      // One rule for the label: the id alone when it is the only name.
      const name = p.name || p.app || p.id;
      return { value: p.id, label: name === p.id ? p.id : name + "  " + p.id };
    });
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
  // The id is the title when the pane has no name, so it is not printed twice.
  const name = own.name || own.app || pane.id;
  $("pane-title").textContent = name === pane.id ? name : name + "  " + pane.id;
  const at = paneContext();
  setStripName("strip-window", at.window, at.windowName);
  setStripName("strip-workspace", at.workspace, at.workspaceName);
  // The fleet names the pane's state every three seconds, so the header stays
  // honest on the two views that never read the pane's text.
  if (own.status !== undefined) setPaneState(stateOf(own), own.message || "");
}

function setStripName(id, code, name) {
  const n = $(id);
  n.textContent = "";
  n.append(el("b", "", code || "-"));
  if (name) n.append(document.createTextNode("  " + name));
}

// --- the three views -----------------------------------------------------
//
// One tap apart, and the address bar carries the one on screen. The transcript
// is the agent's own session reshaped as turns, the activity is the same steps
// as one row each, newest first, and the screen is the text the pane printed.
// A pane whose session cannot be read has the screen only.

// VIEWS are the three view names the address bar carries.
const VIEWS = ["transcript", "activity", "screen"];

// viewOf keeps a view a link names, and falls back to the transcript, which is
// what an agent pane opens on.
function viewOf(name) {
  return VIEWS.includes(name) ? name : "transcript";
}

// viewHash is the address of the open pane in one view.
function viewHash(id, view) {
  return paneHash(id) + "&view=" + view;
}

// activeScroll is the box the visible view scrolls in, which is where the
// follow rule reads the reader's place.
function activeScroll() {
  if (!pane) return null;
  if (pane.view === "screen") return $("text");
  return pane.view === "activity" ? $("activity") : $("transcript");
}

// openPane opens one pane on the view the address bar names, or on the one its
// kind decides: an agent pane on its transcript, a shell pane on its screen.
async function openPane(id, view) {
  closePane(false);
  const row = ((fleet && fleet.panes) || []).find((p) => p.id === id) || {};
  pane = {
    id,
    view: viewOf(view),
    // session is what this page knows about the pane's session: checking until
    // a reply settles it, ok, none when the pane has no session, or failed when
    // the read was refused.
    session: "checking",
    // chosen is true once the reader picked a view. Until then this page picks,
    // so an agent pane lands on its transcript whenever the session shows up.
    chosen: !!view,
    why: "",
    turns: [],
    sessionRev: "",
    textRev: "",
    // open holds the steps the reader unfolded, keyed turn:step, so a reply
    // that changes the session does not fold them again.
    open: new Set(),
    hold: false,
    painted: false,
  };
  $("pane").classList.add("open");
  $("send").style.display = fleet && fleet.readOnly ? "none" : "";
  setPaneState("", "");
  renderPaneChrome();
  renderViewChrome();
  markFollow();
  // A pane with no session has nothing to read. The screen is then the whole
  // view, and the probe would only cost a request.
  if (!hasSession(row)) {
    pane.session = "none";
    pane.view = "screen";
    renderViewChrome();
    startTimers();
    await pollText();
    return;
  }
  for (const box of ["transcript", "activity"]) {
    $(box).textContent = "";
    $(box).append(el("div", "empty", "reading the session"));
  }
  startTimers();
  await pollSession();
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
    const res = await fetchAPI("/api/text?pane=" + encodeURIComponent(at.id) + "&since=" + encodeURIComponent(at.textRev));
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

// pollSession reads the pane's own session file through the transcript
// endpoint. The reply carries every turn of the window and the revision it was
// read at, so a quiet session answers the same small payload and paints
// nothing.
async function pollSession() {
  const at = pane;
  if (!at || document.hidden) return;
  let res;
  try {
    res = await fetchAPI("/api/session?pane=" + encodeURIComponent(at.id)
      + "&since=" + encodeURIComponent(at.sessionRev));
  } catch (e) {
    applyReply(() => paintSession(at, null, "cannot read the session: " + e));
    return;
  }
  if (res.status === 404) {
    applyReply(() => paintSession(at, null, ""));
    return;
  }
  if (!res.ok) {
    const why = await refusalText(res);
    applyReply(() => paintSession(at, null, why));
    return;
  }
  let d;
  try {
    d = await res.json();
  } catch (e) {
    applyReply(() => paintSession(at, null, "the session reply is not JSON: " + e));
    return;
  }
  applyReply(() => paintSession(at, d, ""));
}

// refusalText names a refusal in the daemon's own words when its body carries
// one, so the one line under the switch reads like the server, not like a
// status code.
async function refusalText(res) {
  const body = await res.text();
  try {
    const j = JSON.parse(body);
    if (j && j.error) return j.error;
  } catch (e) {
    // Not JSON: the body is the message.
  }
  return body || "the session answered " + res.status;
}

// paintSession applies one session reply. A pane with no session, or one whose
// session this page cannot read, keeps the screen and says why in one line: this
// page never paints an empty transcript over a pane it cannot read.
function paintSession(at, d, why) {
  if (pane !== at) return;
  if (why) {
    at.session = "failed";
    dropToScreen(why);
    return;
  }
  if (!d) {
    at.session = "none";
    at.turns = [];
    dropToScreen("");
    return;
  }
  const first = at.session !== "ok";
  at.session = "ok";
  at.why = "";
  // A session that turned up after the pane opened, or came back after a read
  // that failed, brings its transcript with it, unless the reader is the one
  // who chose the screen. The switch is painted before the turns so the
  // transcript has a height to scroll in.
  if (first && !at.chosen && at.view === "screen") {
    at.view = "transcript";
    renderViewChrome();
    markFollow();
  }
  if (d.rev) at.sessionRev = d.rev;
  if (d.changed || !at.painted) {
    at.painted = true;
    at.turns = d.turns || [];
    paintTurns();
  }
  if (first) {
    renderViewChrome();
    startTimers();
  }
}

// dropToScreen puts the pane on the screen view and says why in one line. A
// pane with no session has nothing to say, so the line stays empty. A pane that
// is already on the screen only takes the new line, so a retry that fails again
// touches no view and starts no extra poll.
function dropToScreen(why) {
  if (!pane) return;
  const was = pane.view;
  pane.why = why || "";
  if (was === "screen") {
    renderViewChrome();
    return;
  }
  pane.view = "screen";
  renderViewChrome();
  markFollow();
  startTimers();
  pollText();
}

// refreshPane polls the source the visible view reads.
function refreshPane() {
  if (!pane) return;
  if (pane.view === "screen") pollText();
  else pollSession();
}

// renderViewChrome paints the view switch, the one view on screen, and the line
// that says why the screen is showing instead of a session.
function renderViewChrome() {
  if (!pane) return;
  // A pane with no session offers no other view. A refused session keeps the
  // switch, so the reader can ask again.
  const switchable = pane.session === "ok" || pane.session === "failed";
  $("views").classList.toggle("show", switchable);
  for (const v of VIEWS) {
    const a = $("view-" + v);
    a.href = viewHash(pane.id, v);
    a.setAttribute("aria-selected", v === pane.view ? "true" : "false");
  }
  const body = $("screen");
  for (const v of VIEWS) body.classList.toggle("show-" + v, pane.view === v);
  const note = $("pane-note");
  note.textContent = pane.why || "";
  note.classList.toggle("show", !!pane.why);
}

// setView shows one view of the open pane. A pane with no session has only the
// screen, so a link that names another view lands there.
function setView(view) {
  if (!pane) return;
  const want = pane.session === "none" ? "screen" : viewOf(view);
  if (want === pane.view) return;
  pane.chosen = true;
  pane.view = want;
  renderViewChrome();
  markFollow();
  startTimers();
  refreshPane();
}

// FOLLOW_GAP is how far from the live edge counts as reading back through the
// view rather than following it.
const FOLLOW_GAP = 48;

// awayFromEdge reports whether the reader has scrolled away from the edge new
// work lands on: the bottom of the transcript and the screen, the top of the
// activity, which puts the newest step first.
function awayFromEdge() {
  const box = activeScroll();
  if (!box) return false;
  if (pane.view === "activity") return box.scrollTop > FOLLOW_GAP;
  return box.scrollHeight - box.scrollTop - box.clientHeight > FOLLOW_GAP;
}

// markFollow settles the follow state and the latest control from the reader's
// place in the view. One rule owns both, so a scroll, a poll and a tap on the
// control cannot disagree.
function markFollow() {
  const away = !!pane && awayFromEdge();
  if (pane) pane.hold = away;
  $("latest").classList.toggle("show", away);
}

// jumpLatest returns to the edge that holds the newest work and follows it
// again. The view keeps the next tap: the control is only there while the
// reader is away from it.
function jumpLatest() {
  const box = activeScroll() || $("text");
  box.scrollTop = pane && pane.view === "activity" ? 0 : box.scrollHeight;
  if (pane) pane.hold = false;
  $("latest").classList.remove("show");
}

// setPaneState paints the pane header's state line: the state word first, then
// the message as far as the header has room. The word is its own box, so a line
// too narrow for the message still names the state and still ends in the colon
// that says a message is there.
function setPaneState(state, message) {
  $("pane-state").className = "state " + (state || "note");
  $("pane-state-word").textContent = state ? state + (message ? ":" : "") : "";
  // The space is a no-break one, so a line that clips the message still reads
  // as a word and a message rather than as one long token.
  $("pane-state-msg").textContent = message ? "\u00a0" + message : "";
}

// paintText applies one pane reply, and only to the pane that asked for it: a
// reply that lands after another pane was opened is not painted into it.
function paintText(at, d) {
  if (pane !== at) return;
  at.textRev = d.rev;
  setPaneState(d.state, d.message);
  if (!d.changed) return;
  const pre = $("text");
  pre.textContent = (d.lines || []).join("\n");
  // Follow the bottom, which is where a running agent's output is, unless the
  // reader scrolled up to read something.
  if (at.view === "screen" && !at.hold) pre.scrollTop = pre.scrollHeight;
  markFollow();
}

// --- the session views ---------------------------------------------------

// paintTurns rebuilds both session views from the turns on hand, so switching
// between them costs no request. A reader who scrolled up keeps their place; a
// reader at the bottom keeps following it.
function paintTurns() {
  if (!pane) return;
  const box = activeScroll();
  const keep = box ? box.scrollTop : 0;
  const follow = !pane.hold;
  paintTranscript(pane);
  paintActivity(pane);
  keepScroll(pane, keep, follow);
  markFollow();
}

// keepScroll restores the reader's place after a rebuild, and follows the edge
// when they were already there. The transcript follows its bottom, the activity
// its top. The follow gets a second pass on the next frame: a box that lands in
// this paint can measure taller once the browser has settled it, and a single
// pass then stops short of the newest line.
function keepScroll(at, keep, follow) {
  const box = activeScroll();
  if (!box) return;
  const top = pane && pane.view === "activity";
  if (!follow) {
    box.scrollTop = keep;
    return;
  }
  box.scrollTop = top ? 0 : box.scrollHeight;
  requestAnimationFrame(() => {
    if (pane !== at || document.hidden) return;
    const again = activeScroll();
    if (!again) return;
    again.scrollTop = top ? 0 : again.scrollHeight;
    markFollow();
  });
}

// paintTranscript is one block per turn, oldest first: who and when, the ask in
// a quoted block, the steps in order, the unfinished step as a live row, and the
// numbers that close the turn.
function paintTranscript(at) {
  const box = $("transcript");
  box.textContent = "";
  if (!at.turns.length) {
    box.append(el("div", "empty", "no turns yet"));
    return;
  }
  const body = el("div", "body");
  const total = at.turns.length;
  at.turns.forEach((t, i) => body.append(turnBlock(t, i, total, at)));
  box.append(body);
}

// finishedSteps are a turn's finished steps. The payload copies the unfinished
// step into live and leaves it at the end of steps as well, so the page drops
// that copy and paints the live row once. A live step this page cannot find at
// the end of the list keeps every step: a duplicate is better than a lost one.
function finishedSteps(t) {
  const steps = t.steps || [];
  const live = t.live;
  if (!live || steps.length === 0) return steps;
  const last = steps[steps.length - 1];
  const same = last.kind === live.kind && last.tool === live.tool && last.summary === live.summary;
  return same ? steps.slice(0, steps.length - 1) : steps;
}

// turnBlock is one turn. The number counts within the window this page holds,
// which is the last three turns of the session.
function turnBlock(t, i, total, at) {
  const wrap = el("div", "turn");
  const hd = el("div", "turn-hd");
  hd.append(el("b", "", "you"));
  hd.append(el("span", "", clockOf(t.at)));
  hd.append(el("span", "tnum", "turn " + (i + 1) + " of " + total));
  wrap.append(hd);
  if (t.ask) wrap.append(el("div", "you", t.ask));
  finishedSteps(t).forEach((s, j) => wrap.append(stepBlock(s, i + ":" + j, at)));
  if (t.live) wrap.append(liveRow(t.live));
  wrap.append(summaryRow(t.summary));
  return wrap;
}

// stepBlock is one step of a turn: prose as itself, a thought as one collapsed
// row with its duration, and a tool as its name, the argument that matters, its
// duration and its outcome.
function stepBlock(s, key, at) {
  if (s.kind === "text") return el("p", "say", s.text || "");
  if (s.kind === "thinking") {
    const row = el("div", "thinkrow");
    row.append(el("span", "chev", "\u25b8"));
    row.append(el("b", "", "thought"));
    row.append(el("span", "", durText(s.ms)));
    return row;
  }
  return toolStep(s, key, at);
}

// toolStep is one tool call. A step that carries output is one tap target that
// opens it, so it keeps the 44 points a thumb needs; a step with nothing to show
// is a line, not a control.
function toolStep(s, key, at) {
  const bad = s.ok === false;
  const outs = !!s.output;
  const open = outs && at.open.has(key);
  const step = outs ? el("button", "step openable") : el("div", "step");
  if (outs) step.type = "button";
  step.classList.toggle("bad", bad);
  step.classList.toggle("open", open);
  step.append(el("span", "ic", iconOf(s)));
  const t = el("span", "t");
  t.append(el("b", "", s.tool || "tool"));
  if (s.summary) t.append(document.createTextNode(" " + s.summary));
  t.title = (s.tool || "tool") + " " + (s.summary || "");
  step.append(t);
  step.append(el("span", "dur", durText(s.ms) + (bad && s.exit !== undefined ? " \u00b7 exit " + s.exit : "")));
  if (outs) {
    const out = el("pre", "out", s.output);
    if (s.more) out.append(el("span", "more", "\n" + s.more + (s.more === 1 ? " more line" : " more lines")));
    step.append(out);
    step.addEventListener("click", () => {
      if (at.open.has(key)) at.open.delete(key);
      else at.open.add(key);
      step.classList.toggle("open");
    });
  }
  return step;
}

// liveRow is the step the agent is in the middle of. The session carries no
// start time for it, so this page says it is running and invents no clock.
function liveRow(live) {
  const row = el("div", "live");
  row.append(el("span", "spin"));
  const t = el("span", "");
  t.append(el("b", "", live.tool || "step"));
  if (live.summary) t.append(document.createTextNode(" " + live.summary));
  row.append(t);
  row.append(el("span", "when", "running"));
  return row;
}

// summaryRow closes a turn with what it did and what it cost.
function summaryRow(s) {
  const row = el("div", "turnft");
  if (!s) return row;
  const calls = el("span");
  const n = s.calls || 0;
  calls.append(el("b", "", n + (n === 1 ? " call" : " calls")));
  calls.append(document.createTextNode(" "));
  if (s.errors) calls.append(el("span", "bad", s.errors + (s.errors === 1 ? " error" : " errors")));
  else calls.append(document.createTextNode("0 errors"));
  row.append(calls);
  if (s.toolMs) {
    const sp = el("span");
    sp.append(el("b", "", durText(s.toolMs)));
    sp.append(document.createTextNode(" tools"));
    row.append(sp);
  }
  if (s.cost) {
    const sp = el("span");
    sp.append(el("b", "", costText(s.cost)));
    row.append(sp);
  }
  if (s.model) row.append(el("span", "", s.model));
  return row;
}

// paintActivity flattens the same steps to one row each, newest first, with a
// divider where a new turn starts. Every row carries the age of the turn it
// belongs to, because the session times a turn, not a step.
function paintActivity(at) {
  const box = $("activity");
  box.textContent = "";
  if (!at.turns.length) {
    box.append(el("div", "empty", "no steps yet"));
    return;
  }
  for (let i = at.turns.length - 1; i >= 0; i--) {
    const t = at.turns[i];
    if (i !== at.turns.length - 1) {
      box.append(el("div", "actsplit",
        "turn " + (i + 1) + " \u00b7 " + clockOf(t.at) + " \u00b7 " + agoOf(t.at)));
    }
    if (t.live) box.append(actRow(t.live, t.at, true));
    for (const s of finishedSteps(t).slice().reverse()) box.append(actRow(s, t.at, false));
    box.append(askRow(t));
  }
}

// actRow is one step as an activity row: its mark, the argument that matters,
// its duration or outcome, then its age and the first line it said.
function actRow(s, at, live) {
  const row = el("div", "act");
  const ic = el("span", "ic");
  if (live) ic.append(el("span", "spin"));
  else ic.textContent = iconOf(s);
  row.append(ic);
  if (s.kind === "text") {
    row.append(el("span", "t", "said"));
    row.append(el("span", "dur", ""));
    row.append(el("span", "sub", agoOf(at) + (s.text ? " \u00b7 " + head(s.text, 80) : "")));
    return row;
  }
  if (s.kind === "thinking") {
    row.append(el("span", "t", "thought"));
    row.append(el("span", "dur", durText(s.ms)));
    row.append(el("span", "sub", agoOf(at)));
    return row;
  }
  const bad = s.ok === false;
  row.classList.toggle("fail", bad);
  row.classList.toggle("now", live);
  const t = el("span", "t");
  t.append(el("b", "", s.tool || "tool"));
  if (s.summary) t.append(document.createTextNode(" " + s.summary));
  t.title = s.summary || "";
  row.append(t);
  row.append(el("span", "dur", live
    ? "running"
    : durText(s.ms) + (bad && s.exit !== undefined ? " \u00b7 exit " + s.exit : "")));
  const sub = [live ? "running now" : agoOf(at)];
  const said = head(s.output || "", 80);
  if (said && !live) sub.push(said);
  row.append(el("span", "sub", sub.join(" \u00b7 ")));
  return row;
}

// askRow is the turn's ask, the oldest event of its block.
function askRow(t) {
  const row = el("div", "act");
  row.append(el("span", "ic", "\u2709"));
  row.append(el("span", "t", "you asked"));
  row.append(el("span", "dur", agoOf(t.at)));
  if (t.ask) row.append(el("span", "sub", head(t.ask, 110)));
  return row;
}

// TOOL_ICONS is the mark a tool wears. A tool this page does not know keeps a
// plain arrow rather than a wrong glyph.
const TOOL_ICONS = {
  bash: "$",
  read: "\u25c7",
  write: "\u270e",
  edit: "\u270e",
  codemode: "\u2630",
  ask_user_question: "?",
};

function iconOf(s) {
  if (s.kind === "text") return "\u00b6";
  if (s.kind === "thinking") return "\u2726";
  return TOOL_ICONS[s.tool] || "\u203a";
}

// head is the first line of a long text, cut to n characters, for a row that
// has no room for the whole of it.
function head(text, n) {
  const line = ((text || "").split("\n").find((l) => l.trim() !== "") || "").trim();
  return line.length > n ? line.slice(0, n - 1) + "\u2026" : line;
}

// clockOf is a turn's wall clock time in the phone's own zone. A time this page
// cannot read prints nothing rather than a wrong hour.
function clockOf(iso) {
  const t = Date.parse(iso || "");
  if (!t) return "";
  return new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
}

// agoOf is the same instant as a distance from now.
function agoOf(iso) {
  const t = Date.parse(iso || "");
  return t ? agoText(t) : "";
}

// durText is a step's own duration: milliseconds under ten, seconds above it.
function durText(ms) {
  if (!ms) return "";
  if (ms < 10) return Math.round(ms) + "ms";
  return (ms / 1000).toFixed(1) + "s";
}

// costText is the money a turn spent, in the dollars the session reports. A
// cost below a cent keeps enough places to prove it is not zero: rounding
// 0.00033 to three places printed $0.000, which reads as free. The ordinary
// case stays short, and the padding zeros are trimmed off it.
function costText(n) {
  if (!n) return "";
  if (n >= 0.1) return "$" + n.toFixed(2);
  if (n < 0.0001) return "<$0.0001";
  const places = n >= 0.01 ? 3 : n >= 0.001 ? 4 : 5;
  return "$" + n.toFixed(places).replace(/0+$/, "").replace(/\.$/, "");
}

// closePane stops the polls and, when asked, clears the address bar.
function closePane(updateHash = true) {
  if (!pane) return;
  pane = null;
  disarm();
  for (const box of ["text", "transcript", "activity"]) $(box).textContent = "";
  $("views").classList.remove("show");
  $("pane-note").classList.remove("show");
  markFollow();
  $("pane").classList.remove("open");
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

// startTimers arms the polls the visible view needs, and only those: the screen
// reads the pane's text, the two session views read its session. A hidden page
// arms none, so a phone in a pocket makes no request and runs no timer.
function startTimers() {
  stopTimers();
  if (document.hidden) return;
  timers.fleet = setInterval(loadFleet, REFRESH_MS);
  if (!pane) return;
  if (pane.view !== "screen") {
    // The two session views read the session. A pane with no session has
    // nothing to ask for, whatever view it is showing.
    if (pane.session !== "none") timers.session = setInterval(pollSession, TEXT_POLL_MS);
    return;
  }
  timers.text = setInterval(pollText, TEXT_POLL_MS);
  // A session that failed to read is a state to retry, not a verdict: the pane
  // keeps asking while it is open, so a file a live writer is in the middle of
  // lands on the transcript again without the reader tapping anything.
  if (pane.session === "failed") timers.session = setInterval(pollSession, TEXT_POLL_MS);
}

function stopTimers() {
  for (const k of Object.keys(timers)) {
    if (timers[k]) clearInterval(timers[k]);
    timers[k] = null;
  }
}

// route shows the tab and the view the address bar names, and opens the pane it
// names. The hash is the whole view: #panes, #box, #panes&pane=w1p1, and
// #panes&pane=w1p1&view=activity for one of a pane's three views.
function route() {
  // A link from before the tabs still names a pane alone, so it opens that pane
  // on the panes tab.
  const parts = location.hash.slice(1).split("&");
  if (/^pane=/.test(parts[0])) parts.unshift("panes");
  setTab(parts[0]);
  const m = /^pane=(.+)$/.exec(parts[1] || "");
  const id = m ? decodeURIComponent(m[1]) : "";
  const vm = /^view=(.+)$/.exec(parts[2] || "");
  const view = vm ? decodeURIComponent(vm[1]) : "";
  if (id && (!pane || pane.id !== id)) openPane(id, view);
  else if (id && view) setView(view);
  else if (!id && pane) closePane(false);
}

$("pane-close").addEventListener("click", () => closePane());
// Every view scrolls on its own, and the follow rule reads whichever is on
// screen.
for (const box of ["text", "transcript", "activity"]) {
  $(box).addEventListener("scroll", () => {
    if (!pane || activeScroll() !== $(box)) return;
    markFollow();
  });
}
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
  refreshPane();
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
