// dm web: the phone's view of a session. The fleet polls, one pane streams.
// Everything arrives already shaped by the server, so this file only paints.

const REFRESH_MS = 3000;
// MARK_MS is the hidden page's slow clock. A hidden tab arms no view timers, but
// it still answers one question: is something waiting on me? One fleet request
// a minute is the whole cost, and it is what lets the tab title carry a mark.
const MARK_MS = 60000;
// HISTORY_STEP is how many rows of scrollback one climb asks for, and
// HISTORY_MAX the most a phone will hold. The daemon answers with the last n
// rows above the screen, so the page climbs by growing n, never by offset.
const HISTORY_STEP = 200;
const HISTORY_MAX = 2000;
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
const timers = { fleet: null, text: null, session: null, mark: null };
// repaintTimer is the one shot a rotation arms, held so a hide can clear it.
let repaintTimer = null;
// openShells names the projects whose shell panes are unfolded, so a poll does
// not fold a list the reader just opened.
const openShells = new Set();
// shelfOpen is this page's answer to the shelf fold. It starts closed: a
// shelved pane is work set aside, so the live fleet comes first on a phone.
let shelfOpen = false;
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

// paintWaiting marks a question outside the page itself: the tab title and the
// favicon. The strip and the rows live inside a page the reader has to be
// looking at, and a phone in a pocket is not looking at it.
function paintWaiting(f) {
  const n = ((f && f.ask) || []).length;
  const base = "dm " + ((f && f.session) || "");
  document.title = n === 0 ? base : "\u25cf " + base + (n > 1 ? " (" + n + ")" : "");
  paintIcon(n > 0);
}

// paintIcon swaps the favicon between a quiet dot and the blocked mark's
// colour. The state is kept, so a poll that changes nothing rebuilds nothing.
let iconWaiting = null;
function paintIcon(waiting) {
  if (iconWaiting === waiting) return;
  iconWaiting = waiting;
  const dot = waiting ? "#ffb454" : "#647b8c";
  const r = waiting ? 8 : 5;
  const svg = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">' +
    '<rect width="32" height="32" rx="7" fill="#101418"/>' +
    '<circle cx="16" cy="16" r="' + r + '" fill="' + dot + '"/></svg>';
  const link = $("icon");
  if (link) link.href = "data:image/svg+xml," + encodeURIComponent(svg);
}

// pollMarks is the hidden page's one request: the fleet, for the title and the
// icon alone. A visible page skips it, because the ordinary fleet poll already
// keeps those fresh.
async function pollMarks() {
  if (!document.hidden) return;
  try {
    const res = await fetchAPI("/api/fleet");
    if (res.ok) paintWaiting(await res.json());
  } catch (e) {
    // A mark is a courtesy. It never paints a banner, so a session that went
    // away while the phone slept still recovers on the next attach.
  }
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
  paintWaiting(f);
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
      renderPaneActions();
      syncAsk();
      // A pane the fleet now calls an agent may have written its first session
      // line since the last try, so the page asks again rather than staying on
      // the screen for good.
      const row = f.panes.find((p) => p.id === pane.id);
      if (row && hasSession(row) && pane.session === "none") pollSession();
    }
  }
}

// applyReadOnly hides the commands that write, so the page never offers a tap
// that answers 403. Views stay: the view switch, the picture toggle and the
// fold only read, and a read only page that hid them would hide the session
// itself.
const writeControls = ["sheet-open", "compose", "send", "pane-actions", "pane-stop"];

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
  // The shelf holds panes set aside: they belong to no project and to no
  // window, so they sit under the fleet rather than inside a project section.
  const live = f.panes.filter((p) => !p.shelf);
  const shelved = f.panes.filter((p) => p.shelf);
  if (live.length === 0 && shelved.length === 0) {
    box.append(el("div", "empty", "no panes"));
    return;
  }
  for (const g of projectGroups(live)) box.append(projectSection(g));
  if (shelved.length > 0) box.append(shelfSection(shelved));
}

// shelfCount is the shelf heading's own summary: how many panes it sets aside.
function shelfCount(n) {
  return n + (n === 1 ? " shelved pane" : " shelved panes");
}

// shelfSection is the shelf: a heading with its count and a fold that starts
// closed, then one row per shelved pane. A shelved pane still owns its PTY, so
// its row opens exactly like a live one: the same transcript or screen, and the
// same input row.
function shelfSection(panes) {
  const sec = el("section", "shelf");
  const btn = el("button", "fold");
  btn.type = "button";
  btn.setAttribute("aria-expanded", shelfOpen ? "true" : "false");
  btn.append(el("span", "caret", shelfOpen ? "\u25be" : "\u25b8"));
  btn.append(el("span", "", "shelf"));
  btn.append(el("span", "scount", shelfCount(panes.length)));
  btn.addEventListener("click", () => {
    shelfOpen = !shelfOpen;
    dirty.panes = true;
    renderVisible();
  });
  sec.append(btn);
  if (shelfOpen) for (const p of panes) sec.append(paneRow(p));
  return sec;
}

// hasSession is the fleet's answer to the only question the fold asks: does
// this pane have a session to read. The field is the server's: hasSession, true
// when the pane's snapshot carries a session file. The agent flag is a
// different question, because a pane can report a session and no status.
function hasSession(p) {
  return !!p.hasSession;
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
  // A pane carrying a question wants a person whatever its state word says, so
  // the question joins the states that need one.
  const need = f.panes.filter((p) => p.ask || NEED_STATES.includes(stateOf(p)));
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
    const want = p.ask ? "blocked" : stateOf(p);
    head.append(el("span", "cwant state " + want, p.ask ? "question" : want));
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
  const row = el("a", "row" + (p.shelf ? " shelved" : ""));
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
  // A pane waiting on the reader says so beside its own state: a question is
  // work standing still, not work in progress.
  if (p.ask) {
    const q = el("span", "state blocked");
    q.append(el("span", "dot blocked"));
    q.append(document.createTextNode("question"));
    title.append(q);
  }
  row.append(title);

  // The facts, each printed once: the id unless it is the name above, the
  // place it sits in, the branch with its diff, and the folder trimmed to its
  // tail. Each is a chip of its own, so a gap separates them and a line that
  // wraps leaves nothing dangling at its end.
  const where = el("div", "where");
  // A shelved pane has no window to name, so the row says what it is instead:
  // set aside and still running, not a pane the desk draws.
  if (p.shelf) where.append(el("span", "mark-shelved", "shelved"));
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

// sendBody runs one finished request and settles the view after it. Two
// commands move the reader: close-pane empties the view it was looking at, and
// new-window answers with the pane it opened, which the page then shows. A pop
// keeps the pane's id, so its hash still names it: the reloaded fleet is what
// says which window it landed in, and nothing here holds an old place.
async function sendBody(body) {
  const res = await runAction(body);
  if (res === null) return;
  applyReply(() => {
    if (body.id === "close-pane" && pane && body.target === pane.id) closePane();
    if (body.id === "new-window" && res.pane) location.hash = paneHash(res.pane);
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
function renderSheet(all) {
  const body = $("sheet-body");
  body.textContent = "";
  // A row that can name no target is a tap with nothing behind it, so an
  // ask-answer with no open ask is left out rather than offered empty.
  const rows = all.filter((c) => c.id !== "ask-answer" || targetOptions("ask").length > 0);
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
// real pane, window, workspace or ask and never a guess.
function targetOptions(kind) {
  const panes = (fleet && fleet.panes) || [];
  if (kind === "ask") {
    // The label names the pane that asked, which is how a reader tells two
    // open questions apart.
    return ((fleet && fleet.ask) || []).map((a) => ({ value: a.id, label: a.id + "  " + a.pane }));
  }
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
  if (kind === "ask") {
    // The open pane's own question first, then the oldest one listed.
    const asks = (fleet && fleet.ask) || [];
    const own = pane && asks.find((a) => a.pane === pane.id);
    return (own || asks[0] || {}).id || "";
  }
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
  if (cmd.id === "ask-answer") {
    // An answer is a form, so the row opens the pane whose question it names
    // rather than sending an answer nobody wrote.
    const id = ensureTarget("ask");
    const a = ((fleet && fleet.ask) || []).find((x) => x.id === id);
    dismissSheet();
    if (a) location.hash = paneHash(a.pane);
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
// A phone cannot show a split, so no split is offered here: the way to a
// second place to work is another window, opened on the folder the open pane
// is in. That is the one command that leaves this pane, so it is the one the
// page follows.
// The row a thumb reaches for holds work, not housekeeping: a phone opens a
// window or closes a pane. Rename stays in the sheet's command list, where the
// rare verbs live.
const paneActions = [
  { label: "new window", body: () => ({ id: "new-window", cwd: paneCwd() }) },
  // pop is the shelf's own control: a shelved pane is the only one with a place
  // to go back to, so the tap appears only while the pane is set aside. It is a
  // write, so a read only page hides it with the rest of this row.
  { label: "pop pane", body: () => ({ id: "pop-pane", target: pane.id }), shelved: true },
  { label: "close pane", body: () => ({ id: "close-pane", target: pane.id }), ask: () => "close " + pane.id + "?" },
];

// paneCwd is the folder the open pane stands in, which the fleet reports. An
// empty answer is the daemon's to fill: new-window inherits the workspace's.
function paneCwd() {
  const own = ((fleet && fleet.panes) || []).find((p) => pane && p.id === pane.id) || {};
  return own.cwd || "";
}

function renderPaneActions() {
  const box = $("pane-actions");
  box.textContent = "";
  // The fleet row is what says whether the open pane is set aside, so a pane
  // that was popped loses its pop tap on the next reply.
  const own = ((fleet && fleet.panes) || []).find((p) => pane && p.id === pane.id) || {};
  for (const act of paneActions) {
    if (act.shelved && !own.shelf) continue;
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
      dispatch(btn, body, act.ask ? act.ask() : "");
    });
    box.append(btn);
  }
}

function renderPaneChrome() {
  if (!pane) return;
  const own = ((fleet && fleet.panes) || []).find((p) => p.id === pane.id) || {};
  // A run the reader can see is a run they may need to stop: the phone has no
  // Escape key, so the control appears exactly while there is something to stop.
  const busy = own.status === "working" || own.status === "blocked";
  $("pane-stop").classList.toggle("show", busy);
  // The id is the title when the pane has no name, so it is not printed twice.
  const name = own.name || own.app || pane.id;
  $("pane-title").textContent = name === pane.id ? name : name + "  " + pane.id;
  // The fleet names the pane's state every three seconds, so the header stays
  // honest on the two views that never read the pane's text.
  if (own.status !== undefined) setPaneState(stateOf(own), own.message || "");
}

// stopPane sends one escape. pi treats the same key two ways: it interrupts a
// turn that is running, and it declines a question that is waiting, which is
// what the reader means in both cases.
async function stopPane() {
  if (!pane) return;
  disarm();
  let res;
  try {
    res = await fetchAPI("/api/keys", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ pane: pane.id, key: "esc" }),
    });
  } catch (e) {
    banner("stop failed: " + e);
    return;
  }
  if (!res.ok) {
    banner("stop failed: " + (await res.text()));
    return;
  }
  banner("");
}

// --- the command bar -----------------------------------------------------
//
// Three rows sit above the text: the view switch, the six pane commands, and
// the line that says why the screen is showing. On a phone that is still most
// of the pane, so a chevron in the title row folds them onto that one row.
// The device remembers the answer: a reader who wants the text wants it on
// the next visit too.

// SKIN_KEY is where this device keeps its page skin. The terminal coat is a
// preference, not a replacement: the page ships as it was, and one root
// attribute turns the coat on. A refused read, as in a private tab, is the
// plain page.
const SKIN_KEY = "dm.skin";

// storedSkin reads the device's answer.
function storedSkin() {
  try {
    return localStorage.getItem(SKIN_KEY) === "tui";
  } catch (e) {
    return false;
  }
}

// setSkin turns the coat on or off and remembers the answer. The stylesheet
// does the work; this only names the state and keeps the switch honest.
function setSkin(on) {
  document.documentElement.dataset.skin = on ? "tui" : "";
  const btn = $("skin-toggle");
  if (btn) {
    btn.setAttribute("aria-pressed", on ? "true" : "false");
    btn.title = on ? "plain skin" : "terminal skin";
  }
  try {
    localStorage.setItem(SKIN_KEY, on ? "tui" : "plain");
  } catch (e) {
    // A refused write costs the memory, not the coat.
  }
}

// COLLAPSE_KEY is where this device keeps its answer.
const COLLAPSE_KEY = "dm.paneCollapsed";

// storedCollapsed reads the device's answer. Storage that refuses writes, as a
// private tab does, simply starts expanded.
function storedCollapsed() {
  try {
    return localStorage.getItem(COLLAPSE_KEY) === "1";
  } catch (e) {
    return false;
  }
}

// setCollapsed folds or unfolds the bar. The chevron points the way its next
// tap moves it, and aria-expanded says the same thing to a reader.
function setCollapsed(on) {
  $("pane").classList.toggle("collapsed", on);
  const btn = $("pane-collapse");
  btn.textContent = on ? "⌄" : "⌃";
  btn.setAttribute("aria-expanded", on ? "false" : "true");
  btn.title = on ? "show the command bar" : "hide the command bar";
  try {
    localStorage.setItem(COLLAPSE_KEY, on ? "1" : "0");
  } catch (e) {
    // A refused write costs the memory, not the fold.
  }
}

// --- notifications -------------------------------------------------------
//
// One switch for this device's push subscription. iOS grants push only to a
// page installed on the home screen, and it asks for permission only inside a
// tap, so the whole subscribe sequence starts from the button and nowhere else.

// NOTIFY_KEY is where this device remembers whether it subscribed. The
// subscription itself is the truth: the load path reads the device and repairs
// a stale flag from it.
const NOTIFY_KEY = "dm.notify";

// notifyOn is what the page believes about the device's subscription, and
// notifyBusy drops a second tap while the first is still in flight.
let notifyOn = false;
let notifyBusy = false;

// notifySupported reports whether this browser has the three APIs a
// subscription needs. A page opened in a plain tab on a phone that never
// installed it has no PushManager, and no Notification means no permission.
function notifySupported() {
  return "serviceWorker" in navigator && "PushManager" in window && "Notification" in window;
}

// paintNotify puts the switch in step with the device: unsupported where the
// APIs are absent, denied where the browser refused, on with a subscription,
// off without one.
function paintNotify() {
  const btn = $("notify-toggle");
  if (!btn) return;
  let state = "on";
  if (!notifySupported()) state = "unsupported";
  else if (Notification.permission === "denied") state = "denied";
  else if (!notifyOn) state = "off";
  btn.dataset.state = state;
  btn.classList.toggle("on", state === "on");
  btn.setAttribute("aria-pressed", state === "on" ? "true" : "false");
  btn.title =
    state === "on" ? "notifications are on for this device"
    : state === "denied" ? "notifications are blocked in the browser's site settings"
    : state === "unsupported" ? "this browser cannot subscribe to notifications"
    : "turn notifications on for this device";
}

// storedNotify reads the device's last answer, which is all a browser that will
// not report its subscription leaves to go on.
function storedNotify() {
  try {
    return localStorage.getItem(NOTIFY_KEY) === "on";
  } catch (e) {
    return false;
  }
}

// setStoredNotify remembers the answer for the next load.
function setStoredNotify(on) {
  try {
    localStorage.setItem(NOTIFY_KEY, on ? "on" : "off");
  } catch (e) {
    // A refused write costs the memory, not the subscription.
  }
}

// swRegistration returns the worker registration this page already has. It
// never registers one: reading the state must not start the worker.
async function swRegistration() {
  try {
    return (await navigator.serviceWorker.getRegistration("/sw.js")) ||
      (await navigator.serviceWorker.getRegistration()) || null;
  } catch (e) {
    return null;
  }
}

// loadNotify reads the device's real subscription and hands it to dm again.
//
// The device is the truth source, but a subscription is only worth anything
// where dm has the row: a delivery comes from dm's store, never from the
// browser. So the load POSTs whatever the device holds, which the endpoint
// takes idempotently by endpoint, and the switch records on only when dm
// answers. That repairs a subscription kept through a failed drop and a row dm
// lost in one place, and it never claims a phone dm cannot reach.
async function loadNotify() {
  if (!notifySupported()) {
    notifyOn = false;
    paintNotify();
    return;
  }
  let sub = null;
  try {
    const reg = await swRegistration();
    sub = reg ? await reg.pushManager.getSubscription() : null;
  } catch (e) {
    // A browser that refuses to answer leaves nothing to hand dm.
    sub = null;
  }
  notifyOn = false;
  // A refused or unasked permission cannot show a notification, so a
  // subscription under it is not worth the write.
  if (sub && Notification.permission === "granted") {
    const post = await postSubscription(sub);
    notifyOn = !!post && post.ok;
  }
  setStoredNotify(notifyOn);
  paintNotify();
}

// b64url is a subscription key in the form dm stores: raw base64url.
function b64url(buf) {
  const bytes = new Uint8Array(buf);
  let s = "";
  for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

// fromB64url is the applicationServerKey the browser wants: the same bytes.
function fromB64url(text) {
  const raw = atob(text.replace(/-/g, "+").replace(/_/g, "/"));
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

// dropSubscription removes the device's subscription and reports whether the
// device let go. unsubscribe() is a call to the push service, so it can refuse
// while dm is reachable, and a refused drop leaves a live subscription behind.
// The caller must not tell dm the phone is gone on the strength of a drop that
// failed: dm would drop the row for a device that kept it.
async function dropSubscription(sub) {
  try {
    await sub.unsubscribe();
    return true;
  } catch (e) {
    return false;
  }
}

// postSubscription hands one subscription to dm and returns the reply, or null
// when the request never landed. The endpoint is idempotent by endpoint, so a
// second POST for the same phone replaces the row instead of adding one.
async function postSubscription(sub) {
  try {
    return await fetchAPI("/api/push/subscribe", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        endpoint: sub.endpoint,
        p256dh: b64url(sub.getKey("p256dh")),
        auth: b64url(sub.getKey("auth")),
      }),
    });
  } catch (e) {
    return null;
  }
}

// subscribePush registers the worker, asks for permission and hands the
// subscription to dm. The registration and the ask both start before the first
// await, because iOS drops a permission request that does not come straight
// from the tap.
async function subscribePush() {
  const registration = navigator.serviceWorker.register("/sw.js");
  const ask = Notification.permission === "granted"
    ? Promise.resolve("granted")
    : Notification.requestPermission();
  const [reg, permission] = await Promise.all([registration, ask]);
  if (permission !== "granted") {
    banner(permission === "denied"
      ? "notifications are blocked in the browser's site settings"
      : "notifications were not allowed");
    return;
  }
  const res = await fetchAPI("/api/push/config");
  const config = res.ok ? await res.json() : null;
  if (!config || !config.enabled || !config.publicKey) {
    banner("this server cannot push");
    return;
  }
  let sub = await reg.pushManager.getSubscription();
  if (!sub) {
    sub = await reg.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: fromB64url(config.publicKey),
    });
  }
  // A dropped connection, a tailnet reconnect and a browser that aborted the
  // request are one case with a refusal: dm does not have the phone.
  const post = await postSubscription(sub);
  if (!post || !post.ok) {
    // dm kept nothing, so the browser is asked for nothing back. A device that
    // keeps the subscription is not lost: the next load hands it to dm again
    // and records on only when dm takes it. A drop that fails is the case this
    // tap cannot repair, so it says so instead of pretending the device is off.
    const reason = post ? await post.text() : "cannot reach the session";
    const dropped = await dropSubscription(sub);
    banner("notifications failed: " + reason +
      (dropped ? "" : "; the browser kept the subscription"));
    return;
  }
  notifyOn = true;
  setStoredNotify(true);
  banner("notifications on");
}

// unsubscribePush drops the subscription on the device first, then tells dm.
// The device is what the switch reads, so the drop comes first even when the
// request never arrives; dm prunes a dead row when the push service answers
// 410.
async function unsubscribePush() {
  const reg = await swRegistration();
  const sub = reg ? await reg.pushManager.getSubscription() : null;
  if (sub) {
    const endpoint = sub.endpoint;
    if (!(await dropSubscription(sub))) {
      // The device kept the subscription, so the switch reads on after a
      // reload. Telling dm the phone is gone would drop the row for a device
      // that still holds it, and the page would then claim off for a phone dm
      // cannot reach. Leave both sides as they are and say what happened.
      banner("notifications are still on: the browser kept the subscription");
      return;
    }
    try {
      await fetchAPI("/api/push/unsubscribe", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ endpoint: endpoint }),
      });
    } catch (e) {
      // The device is off; dm prunes the dead row on a 410.
    }
  }
  notifyOn = false;
  setStoredNotify(false);
  banner("notifications off");
}

// toggleNotify is the tap: subscribe when the device has no subscription, drop
// it when it has one.
async function toggleNotify() {
  if (notifyBusy) return;
  if (!notifySupported()) {
    banner("this browser cannot subscribe to notifications");
    return;
  }
  notifyBusy = true;
  try {
    if (notifyOn) await unsubscribePush();
    else await subscribePush();
  } catch (e) {
    banner("notifications failed: " + (e && e.message ? e.message : e));
  } finally {
    notifyBusy = false;
    paintNotify();
  }
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
    // want is the view the link named, or empty for none: a link is a
    // preference about a pane that has a session, not a claim that there is
    // nothing to show. pick is the view the reader tapped, and a tap wins over
    // the link for the rest of the pane's life, including after a read fails
    // and the screen takes over for a moment.
    want: view ? viewOf(view) : "",
    pick: "",
    why: "",
    turns: [],
    sessionRev: "",
    textRev: "",
    // open holds the steps the reader unfolded, keyed turn:step, so a reply
    // that changes the session does not fold them again.
    open: new Set(),
    hold: false,
    painted: false,
    // hist is the scrollback the reader climbed to, oldest first, and histN the
    // width of the request that fetched it: it outlives a poll, so a screen
    // that changes does not drop the rows above it. histDone says the pane has
    // no older rows, and histBusy stops a second climb while one is running.
    hist: [],
    histN: 0,
    histDone: false,
    histBusy: false,
  };
  $("pane").classList.add("open");
  $("send").style.display = fleet && fleet.readOnly ? "none" : "";
  setPaneState("", "");
  renderPaneChrome();
  renderPaneActions();
  syncAsk();
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
  // The view the reader asked for: a tap wins, then the view the link named,
  // then the transcript. This is where a session that turned up after the pane
  // opened, or came back after a read that failed, takes the reader to the view
  // they meant. The switch is painted before the turns so the transcript has a
  // height to scroll in.
  if (first) {
    const want = at.pick || at.want || "transcript";
    if (want !== at.view) {
      at.view = want;
      renderViewChrome();
      markFollow();
    }
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
  pane.pick = want;
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
  at.text = d.lines || [];
  paintScreenText(at);
  // Follow the bottom, which is where a running agent's output is, unless the
  // reader scrolled up to read something.
  if (at.view === "screen" && !at.hold) $("text").scrollTop = $("text").scrollHeight;
  markFollow();
}

// paintScreenText writes the scrollback and the screen as one block. A reader
// who climbed keeps the rows they were reading in place: the text above them
// grew, so the box scrolls by the same amount.
function paintScreenText(at) {
  const pre = $("text");
  const fromBottom = pre.scrollHeight - pre.scrollTop;
  const rows = (at.hist || []).concat(at.text || []);
  pre.textContent = rows.join("\n");
  if ((at.hist || []).length > 0) pre.scrollTop = Math.max(0, pre.scrollHeight - fromBottom);
}

// loadOlder climbs one step further up the pane's scrollback. A refusal (a pane
// on the alternate screen, a pane that went away) ends the climb quietly: the
// screen below is still the whole answer the page can give.
async function loadOlder() {
  if (!pane || pane.view !== "screen" || pane.histBusy || pane.histDone) return;
  if (document.hidden) return;
  const at = pane;
  const n = Math.min(HISTORY_MAX, (at.histN || 0) + HISTORY_STEP);
  at.histBusy = true;
  let res;
  try {
    res = await fetchAPI("/api/history?pane=" + encodeURIComponent(at.id) + "&n=" + n);
  } catch (e) {
    at.histBusy = false;
    return;
  }
  if (!res.ok) {
    at.histBusy = false;
    at.histDone = true;
    return;
  }
  let d;
  try {
    d = await res.json();
  } catch (e) {
    at.histBusy = false;
    return;
  }
  at.histBusy = false;
  if (pane !== at) return;
  at.hist = d.lines || [];
  at.histN = n;
  at.histDone = !d.more;
  paintScreenText(at);
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
  if (t.ask) wrap.append(prose("you", t.askHtml, t.ask));
  finishedSteps(t).forEach((s, j) => wrap.append(stepBlock(s, i + ":" + j, at)));
  if (t.live) wrap.append(liveRow(t.live));
  wrap.append(summaryRow(t.summary));
  return wrap;
}

// prose paints one piece of agent prose. The daemon renders the markdown, so
// the page never parses any: html is what it sent, and text is the fallback for
// a render that produced nothing.
function prose(cls, html, text) {
  const box = el("div", cls + " md");
  if (html) {
    box.innerHTML = html;
    wrapTables(box);
  } else {
    box.textContent = text || "";
  }
  return box;
}

// wrapTables puts a wide table in its own scroll box, so a table that does not
// fit scrolls sideways by itself instead of carrying the whole transcript with
// it. The wrapper is built here because the daemon sends the table as markdown
// renders it, with no wrapper of its own.
function wrapTables(box) {
  for (const table of box.querySelectorAll("table")) {
    const wrap = el("div", "tablewrap");
    table.replaceWith(wrap);
    wrap.append(table);
  }
}

// stepBlock is one step of a turn: prose as itself, a thought as one collapsed
// row with its duration, and a tool as its name, the argument that matters, its
// duration and its outcome.
function stepBlock(s, key, at) {
  if (s.kind === "text") return prose("say", s.html, s.text);
  if (s.kind === "thinking") return thoughtBlock(s, key, at);
  return toolStep(s, key, at);
}

// thoughtBlock is one thought: a row carrying its duration, and under it the
// words themselves once a tap asks for them. A session that recorded no words
// keeps the row as a line, because there is nothing to open.
function thoughtBlock(s, key, at) {
  const has = !!(s.html || s.text);
  const open = has && at.open.has(key);
  const wrap = el("div", "think");
  wrap.classList.toggle("open", open);
  const row = has ? el("button", "thinkrow") : el("div", "thinkrow");
  if (has) row.type = "button";
  row.append(el("span", "chev", "\u25b8"));
  row.append(el("b", "", "thought"));
  row.append(el("span", "dia", durText(s.ms)));
  if (!has) {
    wrap.append(row);
    return wrap;
  }
  row.title = open ? "fold the thought" : "read the thought";
  row.setAttribute("aria-expanded", open ? "true" : "false");
  wrap.append(row);
  if (open) {
    const body = prose("thinktext", s.html, s.text);
    if (s.more) body.append(el("div", "more", moreText(s.more).trim()));
    wrap.append(body);
  }
  // A link inside a thought is for following, not for folding the row it sits
  // in, so the tap that lands on one keeps its own meaning.
  wrap.addEventListener("click", (e) => {
    if (e.target.closest("a")) return;
    toggleRow(at, key);
  });
  return wrap;
}

// toggleRow remembers one row's open state for the life of the view and paints
// it again: the thought's words only exist in the paint that opens them, so a
// class change alone would look like a dead tap.
function toggleRow(at, key) {
  if (at.open.has(key)) at.open.delete(key);
  else at.open.add(key);
  paintTurns();
}

// moreText is the trailer a cut body carries, so a reader knows the rest was
// clipped rather than lost.
function moreText(n) {
  return "\n" + n + (n === 1 ? " more line" : " more lines");
}

// toolStep is one tool call. A step that carries output is one tap target that
// opens it, so it keeps the 44 points a thumb needs; a step with nothing to show
// is a line, not a control.
function toolStep(s, key, at) {
  const bad = s.ok === false;
  // A call opens when it has anything to show: the arguments it was told, or
  // the output it answered with.
  const outs = !!s.output || !!s.args;
  const open = outs && at.open.has(key);
  const step = outs ? el("button", "step openable") : el("div", "step");
  if (outs) {
    step.type = "button";
    step.setAttribute("aria-expanded", open ? "true" : "false");
  }
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
    if (s.args) {
      const args = el("pre", "args", s.args);
      if (s.argsMore) args.append(el("span", "more", moreText(s.argsMore)));
      step.append(args);
    }
    if (s.output) {
      const out = el("pre", "out", s.output);
      if (s.more) out.append(el("span", "more", moreText(s.more)));
      step.append(out);
    }
    // A link can only come from the arguments, and following one is not the
    // same tap as folding the call.
    step.addEventListener("click", (e) => {
      if (e.target.closest("a")) return;
      toggleRow(at, key);
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
  askState = null;
  paintAsk();
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

// --- the ask form --------------------------------------------------------
//
// A pane whose agent asked a question wears the form above its views: one block
// per question, the options as rows, a field for something else, and the two
// answers the daemon takes, send and decline. The form is the pane's own, so it
// is drawn only while the pane it belongs to is open.

// askState is the form on screen: the ask it belongs to, one entry per
// question, the preview rows the reader opened, the refusal a refused answer
// left, and whether this page already answered it. It outlives a poll, so a
// reader's typing is not lost while the fleet refreshes.
let askState = null;

// openAskFor is the question set the fleet lists for one pane. The snapshot
// lists open asks only, so a settled ask leaves the fleet at once: this page
// can only tell it is over, never why.
function openAskFor(id) {
  const asks = (fleet && fleet.ask) || [];
  return asks.find((a) => a.pane === id) || null;
}

// syncAsk keeps the form in step with the fleet. A poll that changes no ask
// repaints nothing but the send control, so an open preview and a typed half
// answer stay put.
function syncAsk() {
  if (!pane) {
    askState = null;
    paintAsk();
    return;
  }
  const a = openAskFor(pane.id);
  if (!a) {
    // The ask left the fleet: this page's own answer settled it, or another
    // client answered it, or the agent withdrew it. A reader with words typed
    // keeps them on screen, frozen, rather than losing them to a form that
    // vanishes under their hands.
    if (askState && !askState.done && askDirty(askState)) {
      if (!askState.settled) {
        askState.settled = true;
        paintAsk();
      }
      return;
    }
    askState = null;
    paintAsk();
    return;
  }
  const ro = !!(fleet && fleet.readOnly);
  if (!askState || askState.id !== a.id) {
    askState = {
      id: a.id,
      ask: a,
      entries: (a.questions || []).map(() => ({ picks: [], text: "", note: "", open: new Set() })),
      err: "",
      done: "",
      ro,
    };
    paintAsk();
    return;
  }
  askState.ask = a;
  if (askState.ro !== ro) {
    askState.ro = ro;
    paintAsk();
    return;
  }
  refreshAskSend();
}

// askDirty reports whether the reader has given the form anything, a pick, a
// typed answer or a note. An untouched form can be taken away; a filled one
// cannot, because what is in it exists nowhere else.
function askDirty(at) {
  return at.entries.some((e) => e.picks.length > 0 || e.text.trim() !== "" || e.note.trim() !== "");
}

// askAnswered is the daemon's rule for one question said out loud: a
// multi-select needs one pick or text, a single select needs exactly one of
// them. The send control is disabled until every question meets it.
function askAnswered(q, e) {
  const picks = e.picks.length;
  const text = e.text.trim() !== "";
  if (q.multiSelect) return picks > 0 || text;
  return (picks === 1) !== text;
}

function askComplete() {
  if (!askState) return false;
  const qs = askState.ask.questions || [];
  return qs.every((q, i) => askAnswered(q, askState.entries[i]));
}

// refreshAskSend settles the one control the answers gate.
function refreshAskSend() {
  const btn = $("ask-send");
  if (btn) btn.disabled = !askComplete();
}

// paintAsk rebuilds the form from the state on hand. It runs when the ask
// changes, when a refusal lands and when an answer settles, never on a poll
// that only moves the clock.
function paintAsk() {
  const box = $("ask");
  box.textContent = "";
  const a = askState && askState.ask;
  box.classList.toggle("show", !!a);
  if (!a) return;
  const wrap = el("div", "ask");
  wrap.classList.toggle("settled", !!askState.settled);
  const hd = el("div", "ask-hd");
  hd.append(el("b", "", "asks you"));
  hd.append(el("span", "", "a question"));
  hd.append(el("span", "ago", agoOf(a.at)));
  wrap.append(hd);
  (a.questions || []).forEach((q, i) => wrap.append(askQuestion(q, i)));
  if (askState.err) wrap.append(el("div", "ask-err", askState.err));
  if (askState.done) {
    wrap.append(el("div", "ask-done", askState.done));
  } else if (askState.settled) {
    wrap.append(el("div", "ask-gone", "this question is no longer open"));
    wrap.append(el("div", "ask-hint",
      "another client answered it, or the agent withdrew it. Your draft was not sent."));
  } else if (askState.ro) {
    wrap.append(el("div", "ask-hint", "read only: this question is answered at the desk"));
  } else {
    wrap.append(askControls());
  }
  box.append(wrap);
  refreshAskSend();
}

// askQuestion is one question's block: its header, its text, the options, and
// the field for something else.
function askQuestion(q, i) {
  const e = askState.entries[i];
  const block = el("div", "ask-q");
  block.append(el("div", "ask-qh", q.header || "question " + (i + 1)));
  block.append(el("div", "ask-qt", q.question));
  block.append(el("div", "ask-hint", q.multiSelect
    ? "pick any that apply, or type something else"
    : "pick one, or type something else"));
  const opts = el("div", "ask-opts");
  const input = el("input", "ask-text");
  input.type = "text";
  input.autocomplete = "off";
  input.autocapitalize = "off";
  input.spellcheck = false;
  input.placeholder = "something else";
  input.value = e.text;
  // A question this page may not answer, or one already settled, is a record of
  // what the reader typed, not a field to keep typing in.
  if (askState.done || askState.ro || askState.settled) input.readOnly = true;
  input.addEventListener("input", () => {
    e.text = input.value;
    // A single-select question takes one option or text, never both, so
    // typing clears the row the reader had picked.
    if (!q.multiSelect && e.text.trim() !== "" && e.picks.length > 0) {
      e.picks = [];
      for (const row of opts.children) {
        row.classList.remove("picked");
        row.setAttribute("aria-checked", "false");
        const tick = row.querySelector(".ask-tick");
        if (tick) tick.textContent = "\u25cb";
      }
    }
    refreshAskSend();
  });
  (q.options || []).forEach((o, j) => opts.append(askOption(q, i, o, j, input)));
  block.append(opts);
  block.append(input);
  // A read-only page carries no note field: the note is one of the controls a
  // reader at the desk owns, so it leaves with send and decline. A settled form
  // keeps the note the reader typed and offers no field to edit it in.
  if (!askState.ro && !askState.settled) block.append(askNote(i));
  else if (askState.settled && e.note.trim() !== "") block.append(el("div", "ask-hint", "note: " + e.note.trim()));
  return block;
}

// askNote is one question's note: the line that shows the note the model will
// read, and one input row under it. The note rides the answer item, so typing
// here never touches the send control: an answer gates send, a note never
// does. The line reads the trimmed note, because that is what the item sends.
function askNote(i) {
  const e = askState.entries[i];
  const box = el("div", "ask-notebox");
  const shown = el("div", "ask-note-shown");
  const input = el("input", "ask-note");
  input.type = "text";
  input.autocomplete = "off";
  input.autocapitalize = "off";
  input.spellcheck = false;
  input.placeholder = "note for this question";
  input.value = e.note;
  const paint = () => {
    const note = e.note.trim();
    shown.textContent = note ? "note: " + note : "";
    shown.hidden = note === "";
  };
  input.addEventListener("input", () => {
    e.note = input.value;
    paint();
  });
  paint();
  box.append(shown, input);
  return box;
}

// askOption is one choice. A row is the tap target that picks it; the preview
// is a button of its own inside the row, so reading it never picks anything.
function askOption(q, i, o, j, input) {
  const e = askState.entries[i];
  const picked = e.picks.includes(j);
  const row = el("div", "ask-opt");
  row.setAttribute("role", q.multiSelect ? "checkbox" : "radio");
  row.setAttribute("aria-checked", picked ? "true" : "false");
  // A question already answered or one this page may not answer is a list to
  // read, not a set of rows to tap.
  const live = !askState.done && !askState.ro && !askState.settled;
  if (live) row.setAttribute("tabindex", "0");
  row.classList.toggle("picked", picked);
  const line = el("div", "ask-line");
  line.append(el("span", "ask-tick", tickOf(q, picked)));
  line.append(el("span", "ask-label", o.label));
  row.append(line);
  if (o.description) row.append(el("div", "ask-desc", o.description));
  if (o.preview) {
    const open = e.open.has(j);
    const more = el("button", "ask-more", "preview");
    more.type = "button";
    more.setAttribute("aria-expanded", open ? "true" : "false");
    const pre = el("pre", "ask-preview", o.preview);
    pre.hidden = !open;
    more.addEventListener("click", (ev) => {
      ev.stopPropagation();
      if (e.open.has(j)) e.open.delete(j);
      else e.open.add(j);
      pre.hidden = !e.open.has(j);
      more.setAttribute("aria-expanded", pre.hidden ? "false" : "true");
    });
    row.append(more);
    row.append(pre);
  }
  const pick = () => {
    if (!live) return;
    if (q.multiSelect) {
      const at = e.picks.indexOf(j);
      const on = at < 0;
      if (on) e.picks.push(j);
      else e.picks.splice(at, 1);
      e.picks.sort((x, y) => x - y);
      row.classList.toggle("picked", on);
      row.setAttribute("aria-checked", on ? "true" : "false");
      row.querySelector(".ask-tick").textContent = tickOf(q, on);
    } else {
      // A second tap on the chosen row clears it, so nobody has to type text
      // to take back a pick.
      const was = e.picks.length === 1 && e.picks[0] === j;
      e.picks = was ? [] : [j];
      if (!was) {
        e.text = "";
        input.value = "";
      }
      for (const other of row.parentNode.children) {
        other.classList.remove("picked");
        other.setAttribute("aria-checked", "false");
        const tick = other.querySelector(".ask-tick");
        if (tick) tick.textContent = tickOf(q, false);
      }
      if (!was) {
        row.classList.add("picked");
        row.setAttribute("aria-checked", "true");
        row.querySelector(".ask-tick").textContent = tickOf(q, true);
      }
    }
    refreshAskSend();
  };
  if (live) {
    row.addEventListener("click", (ev) => {
      // Reading the preview is its own tap, not a pick of the row it sits in.
      if (ev.target.closest(".ask-more, .ask-preview")) return;
      pick();
    });
    row.addEventListener("keydown", (ev) => {
      if (ev.key !== "Enter" && ev.key !== " ") return;
      // The space is the pick here, not a scroll of the form.
      ev.preventDefault();
      pick();
    });
  }
  return row;
}

// tickOf is the mark a row wears: a box for a multi-select and a circle for a
// single one, open or filled.
function tickOf(q, on) {
  if (q.multiSelect) return on ? "\u2611" : "\u2610";
  return on ? "\u25cf" : "\u25cb";
}

// askControls is send and decline. Send stays disabled until every question is
// answered, because the daemon takes the answer all at once or not at all.
function askControls() {
  const box = el("div", "ask-controls");
  const send = el("button", "send", "send");
  send.type = "button";
  send.id = "ask-send";
  send.addEventListener("click", () => answerAsk(false));
  const no = el("button", "", "decline");
  no.type = "button";
  no.addEventListener("click", () => answerAsk(true));
  box.append(send, no);
  return box;
}

// answerAsk sends the form's two answers. A refusal keeps its code in the
// banner and in the form, and keeps every answer the reader gave; a success
// settles the form until the ask leaves the fleet.
async function answerAsk(cancel) {
  if (!askState) return;
  const at = askState;
  const answer = cancel ? { cancel: true } : { items: entriesOf(at) };
  let res;
  try {
    res = await fetchAPI("/api/action", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id: "ask-answer", target: at.ask.id, answer }),
    });
  } catch (e) {
    applyReply(() => banner("answer failed: " + e));
    return;
  }
  if (!res.ok) {
    const why = (await res.text()).trim();
    applyReply(() => {
      banner(why);
      if (askState === at) {
        at.err = why;
        paintAsk();
      }
    });
    return;
  }
  applyReply(() => {
    banner("");
    if (askState === at) {
      at.err = "";
      at.done = cancel ? "declined" : "answered";
      paintAsk();
    }
    loadFleet();
  });
}

// entriesOf turns the form's answers into the daemon's items: one per
// question, the picks and the custom text, and neither when the reader gave
// none of that kind. A note rides the item trimmed, and a blank one is left out
// whole, so an item with no note is the body the store already expects and the
// bridge prints no empty "user notes:" line.
function entriesOf(at) {
  return at.entries.map((e, i) => {
    const item = { questionIndex: i, optionIndexes: e.picks };
    const text = e.text.trim();
    if (text) item.text = text;
    const note = e.note.trim();
    if (note) item.notes = note;
    return item;
  });
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

// SEND_ROWS is how many rows the composer grows to before it scrolls inside
// itself. SEND_VH caps that on a short window first, so a landscape phone never
// gives the composer half the screen.
const SEND_ROWS = 6;
const SEND_VH = 0.3;

// sendGrow fits the composer to its text: one row at rest, which is the height
// the row always had, then a row per line up to six rows or 30vh, then the box
// scrolls inside itself. The cap is computed here as well as in the page's
// stylesheet, because the overflow answer is this function's.
function sendGrow() {
  const box = $("send-text");
  const cs = getComputedStyle(box);
  const line = parseFloat(cs.lineHeight) || 21;
  const pad = parseFloat(cs.paddingTop) + parseFloat(cs.paddingBottom);
  const edge = parseFloat(cs.borderTopWidth) + parseFloat(cs.borderBottomWidth);
  const cap = Math.min(SEND_ROWS * line + pad + edge, window.innerHeight * SEND_VH);
  // auto first: scrollHeight then reports the text's own height, not the height
  // the last call pinned.
  box.style.height = "auto";
  const want = box.scrollHeight + edge;
  box.style.height = Math.min(want, cap) + "px";
  box.style.overflowY = want > cap ? "auto" : "hidden";
}

// sendText sends what the composer holds. One line goes as a typed command, the
// way it always did: the text, then one enter. A block with a newline goes as
// one paste and then one enter, so the program receives the block whole and
// submits once. A paste that failed is not followed by the enter: that would
// submit whatever the pane already holds.
async function sendText() {
  const input = $("send-text");
  const text = input.value;
  if (!pane || !text) return;
  disarm();
  if (text.includes("\n")) {
    if (await post("/api/msg", { pane: pane.id, text, paste: true })) {
      await post("/api/msg", { pane: pane.id, text: "", enter: true });
    }
  } else {
    await post("/api/msg", { pane: pane.id, text, enter: true });
  }
  input.value = "";
  sendGrow();
}

// attachImage sends one picture to the daemon and puts the path it answers in
// the input. A pane cannot receive a picture, and pi's read tool sends these
// four types as attachments, so a path is the whole handoff: the reader adds
// the words that say what to do with it, then sends.
async function attachImage(file) {
  if (!file || !pane) return;
  banner("");
  let res;
  try {
    res = await fetchAPI("/api/upload", {
      method: "POST",
      headers: { "Content-Type": file.type || "application/octet-stream" },
      body: file,
    });
  } catch (e) {
    banner("picture failed: " + e);
    return;
  }
  if (!res.ok) {
    banner("picture failed: " + (await res.text()));
    return;
  }
  const doc = await res.json();
  const input = $("send-text");
  input.value = input.value ? input.value + " " + doc.path : doc.path;
  sendGrow();
  input.focus();
}

// post writes one key message into the pane, reports a refusal in the banner
// and answers whether it landed, so a caller that sends two frames in order can
// stop at the first one that failed.
async function post(path, body) {
  try {
    const res = await fetchAPI(path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok) {
      banner(await res.text());
      return false;
    }
    banner("");
    return true;
  } catch (e) {
    banner("send failed: " + e);
    return false;
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
  // The mark clock runs whether or not the page is visible: it is the one thing
  // a hidden tab still does, so a reader learns a question landed while they
  // were in another app.
  timers.mark = setInterval(pollMarks, MARK_MS);
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
$("pane-stop").addEventListener("click", stopPane);
$("pane-collapse").addEventListener("click", () => {
  setCollapsed(!$("pane").classList.contains("collapsed"));
});
// Every view scrolls on its own, and the follow rule reads whichever is on
// screen.
for (const box of ["text", "transcript", "activity"]) {
  $(box).addEventListener("scroll", () => {
    if (!pane || activeScroll() !== $(box)) return;
    markFollow();
    // The screen view holds the scrollback above it, so the top of the box is
    // how a reader asks for older rows. The view stays where it was: the rows
    // that arrive land above it.
    if (box === "text" && $(box).scrollTop <= 0) loadOlder();
  });
}
$("latest").addEventListener("click", jumpLatest);
$("send-submit").addEventListener("click", () => sendText());
$("send-attach").addEventListener("click", () => $("send-image").click());
$("send-image").addEventListener("change", (e) => {
  const file = e.target.files && e.target.files[0];
  // The input is emptied so the same picture picked twice still fires.
  e.target.value = "";
  attachImage(file);
});
// Enter inserts a newline in the textarea and sends nothing: a phone that
// types a block needs the newline more than it needs the early submit. The send
// control is what sends, and it sends the block as one paste.
$("send-text").addEventListener("input", sendGrow);
$("sheet-open").addEventListener("click", openSheet);
$("thumbs-toggle").addEventListener("click", () => setThumbs(!thumbsOn()));
window.addEventListener("orientationchange", () => {
  // The composer's cap is a share of the window, so it is refitted at once.
  sendGrow();
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
$("skin-toggle").addEventListener("click", () => {
  setSkin(document.documentElement.dataset.skin !== "tui");
});
$("notify-toggle").addEventListener("click", toggleNotify);
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

setCollapsed(storedCollapsed());
renderPaneActions();

// The address bar carries the tab, so a reload lands where the reader stood; a
// page opened without one starts on the panes.
// The head script already applied the remembered skin before the first paint;
// this puts the switch in step with it, and lets a link carry the choice.
const wantSkin = new URLSearchParams(location.search).get("skin");
setSkin(wantSkin === "tui" ? true : wantSkin === "plain" ? false : storedSkin());
route();
if (!location.hash) {
  history.replaceState(null, "", location.pathname + location.search + "#" + tab);
}

// The switch starts from the device, not from the URL: a subscription made in
// an earlier visit is what it shows.
notifyOn = storedNotify();
paintNotify();
loadNotify();

// A reloaded page carries no token in the query, and the cookie is the
// credential. The fleet answers 401 when neither is usable, and that is where
// the token hint comes from.
loadFleet();
startTimers();
