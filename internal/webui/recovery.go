package webui

import (
	"io"
	"net/http"
)

// recoveryPage answers a page request that carried no valid token. The token
// lives in the link the terminal printed, so a phone that opened the bare
// address gets a form instead of a dead end: it is a recovery form, not a
// login screen, so it keeps the 401 and the rest of the surface keeps the
// plain refusal.
func recoveryPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = io.WriteString(w, recoveryHTML)
}

// recoveryHTML carries the index page's colours and font, and nothing else: no
// asset, no fetch, so it paints on a phone that has never loaded the app.
const recoveryHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="color-scheme" content="dark">
<title>dm</title>
<style>
  :root {
    --bg: #101418;
    --panel: #171d23;
    --line: #263039;
    --fg: #d7dee5;
    --dim: #7d8b98;
    --working: #63a9ff;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0;
    background: var(--bg);
    color: var(--fg);
    font: 15px/1.4 ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif;
    padding: env(safe-area-inset-top) env(safe-area-inset-right) env(safe-area-inset-bottom) env(safe-area-inset-left);
  }
  main { max-width: 480px; padding: 16px 12px; }
  p { margin: 0 0 12px; }
  form { display: flex; flex-direction: column; gap: 8px; }
  input {
    background: var(--bg); color: var(--fg);
    border: 1px solid var(--line); border-radius: 6px;
    padding: 9px 10px; font-size: 15px; min-width: 0; min-height: 44px;
  }
  button {
    background: var(--bg); color: var(--fg);
    border: 1px solid var(--working); border-radius: 6px;
    padding: 10px 12px; font-size: 14px; min-height: 44px;
  }
  .hint { margin: 12px 0 0; color: var(--dim); font-size: 13px; }
</style>
</head>
<body>
<main>
  <p>The link that opens dm carries its token: paste it here.</p>
  <form id="recover" action="/" method="get">
    <input id="t" name="t" type="text" placeholder="token"
      autocomplete="off" autocapitalize="off" autocorrect="off" spellcheck="false" autofocus>
    <button type="submit">Open</button>
  </form>
  <p class="hint">The terminal that runs dm web prints the link.</p>
</main>
<script>
  document.getElementById("recover").addEventListener("submit", function (event) {
    event.preventDefault();
    var token = document.getElementById("t").value.trim();
    location.assign("/?t=" + encodeURIComponent(token));
  });
</script>
</body>
</html>
`
