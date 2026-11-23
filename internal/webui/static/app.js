// dm web: the phone's view of a session. The fleet polls, one pane streams.
// Everything arrives already shaped by the server, so this file only paints.

const REFRESH_MS = 3000;
const TEXT_POLL_MS = 1000;

let fleet = null;
// pane is the open pane view: its id, the text revision it has, and the poll.
let pane = null;

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
  $("session").textContent = "dm " + f.session;
  renderCounts(f);
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

// openPane streams one pane into a terminal. The address bar carries the pane
// id, so a reload or a shared link lands on the same pane.
async function openPane(id) {
  closePane(false);
  pane = { id, rev: "", hold: false, timer: null };
  $("pane").classList.add("open");
  $("send").style.display = fleet && fleet.readOnly ? "none" : "flex";
  $("pane-state").textContent = "";

  const label = (fleet && fleet.panes.find((p) => p.id === id)) || {};
  $("pane-title").textContent = (label.name || label.app || id) + "  " + id;

  await pollText();
  pane.timer = setInterval(pollText, TEXT_POLL_MS);
}

// pollText reads the pane through the read action and paints it as text.
//
// The pane is not a terminal here: the text it printed is wrapped by this page
// at its own width and font, which is the whole reason a phone can read it
// without the session changing size.
async function pollText() {
  if (!pane) return;
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
  if (pane.timer) clearInterval(pane.timer);
  $("text").textContent = "";
  $("pane").classList.remove("open");
  pane = null;
  if (updateHash) history.replaceState(null, "", location.pathname + location.search);
}

// post writes one control message and reports a refusal in the banner.
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

function sendText(enter) {
  if (!pane) return;
  const input = $("send-text");
  post("/api/msg", { pane: pane.id, text: input.value, enter });
  input.value = "";
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
window.addEventListener("hashchange", route);
document.addEventListener("visibilitychange", () => {
  if (document.hidden) return;
  loadFleet();
  if (pane) pollText();
});

if (!TOKEN) {
  banner("this page needs its token: open the link with ?t=... on it");
} else {
  loadFleet();
}
route();
setInterval(loadFleet, REFRESH_MS);
