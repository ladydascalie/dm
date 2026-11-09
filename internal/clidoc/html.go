package clidoc

import (
	"fmt"
	"html"
	"strings"

	"dm/internal/configdoc"
	"dm/internal/keymap"
)

// HTMLPage renders the whole dm surface as one self-contained HTML page. It
// draws from the same tables as the usage text, the man page and llms.txt:
// cliCommands, manActionFields, manActionNames, the default keymap and the
// configdoc tables. docs/dm.html is a committed snapshot of this output, and a
// test fails when the two drift.

// idGen hands out stable, unique element ids. The base is a prefix and a slug
// of the row text; a repeated base gets a numeric suffix.
type idGen struct {
	seen map[string]int
}

func newIDGen() *idGen { return &idGen{seen: map[string]int{}} }

func (g *idGen) id(prefix, text string) string {
	base := prefix + "-" + htmlSlug(text)
	if g.seen[base] == 0 {
		g.seen[base] = 1
		return base
	}
	for n := 2; ; n++ {
		cand := fmt.Sprintf("%s-%d", base, n)
		if g.seen[cand] == 0 {
			g.seen[cand] = 1
			return cand
		}
	}
}

// htmlSlug turns text into a lowercase id fragment: letters and digits stay, a
// run of anything else becomes one dash.
func htmlSlug(text string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(text) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "x"
	}
	return out
}

// htmlEscape escapes source text for element content and attributes. A row's
// text is untrusted as HTML whatever its table says.
func htmlEscape(s string) string { return html.EscapeString(s) }

// htmlPageHead opens the document and carries the whole style sheet. The page
// has no external resource: no link, no script src, no image, no web font.
const htmlPageHead = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>dm - durable terminal multiplexer</title>
<style>
:root {
  --bg: #14131a;
  --panel: #1f1e27;
  --panel-2: #191820;
  --text: #e9eaf1;
  --dim: #8f93a8;
  --header: #a6a9bd;
  --accent: #bb9af7;
  --accent-2: #6edceb;
  --link: #7ac0eb;
  --add: #78dc82;
  --del: #ff7d7d;
  --warn: #f5be50;
  --border: #2c2b38;
  --code-bg: #191820;
}
@media (prefers-color-scheme: light) {
  :root {
    --bg: #f7f6fb;
    --panel: #efedf7;
    --panel-2: #f1f0f8;
    --text: #1b1a22;
    --dim: #5d6072;
    --header: #3f4152;
    --accent: #6a48c9;
    --accent-2: #0f7a90;
    --link: #1d6fa5;
    --add: #1d7a36;
    --del: #b3261e;
    --warn: #8a5a00;
    --border: #d9d6e6;
    --code-bg: #f0eef8;
  }
}
* { box-sizing: border-box; }
html { scroll-behavior: smooth; }
body {
  margin: 0;
  background: var(--bg);
  color: var(--text);
  font: 16px/1.55 system-ui, -apple-system, "Segoe UI", sans-serif;
}
a { color: var(--link); text-decoration: none; }
a:hover { text-decoration: underline; }
code, kbd, pre { font-family: ui-monospace, "SFMono-Regular", Menlo, Consolas, monospace; }
code { background: var(--code-bg); border: 1px solid var(--border); border-radius: .25rem; padding: 0 .25rem; }
pre { background: var(--code-bg); border: 1px solid var(--border); border-radius: .4rem; padding: .75rem 1rem; overflow-x: auto; }
pre code { background: none; border: 0; padding: 0; }
kbd { border: 1px solid var(--border); border-bottom-width: 2px; border-radius: .25rem; padding: 0 .3rem; font-size: .85em; }
h1 { color: var(--accent); font-size: 2rem; margin: 0 0 .75rem; }
h2 { color: var(--accent); border-bottom: 1px solid var(--border); padding-bottom: .3rem; margin: 0 0 1rem; }
h3 { color: var(--accent-2); margin: 1.75rem 0 .5rem; }
p { margin: .5rem 0 .75rem; }
.sidebar {
  position: fixed;
  inset: 0 auto 0 0;
  width: 18rem;
  padding: 1rem 1rem 2rem;
  overflow-y: auto;
  background: var(--panel);
  border-right: 1px solid var(--border);
}
.search input {
  width: 100%;
  padding: .45rem .6rem;
  color: var(--text);
  background: var(--panel-2);
  border: 1px solid var(--border);
  border-radius: .35rem;
  font: inherit;
}
.search input:focus { outline: 2px solid var(--accent); outline-offset: 1px; }
.search-count { display: block; min-height: 1.25rem; margin-top: .3rem; color: var(--dim); font-size: .8rem; }
.hint { color: var(--dim); font-size: .8rem; margin: .25rem 0 .75rem; }
nav ol { list-style: none; margin: 0; padding: 0; }
nav li { margin: .15rem 0; }
.toc-link { display: block; padding: .2rem .45rem; border-radius: .3rem; color: var(--text); }
.toc-link:hover { background: var(--panel-2); text-decoration: none; }
main {
  margin-left: 18rem;
  padding: 2rem clamp(1rem, 4vw, 3rem) 5rem;
  max-width: 62rem;
}
section.sec { margin-bottom: 3rem; }
table { width: 100%; border-collapse: collapse; margin: .75rem 0 1rem; font-size: .95rem; }
th, td { text-align: left; vertical-align: top; padding: .4rem .6rem; border-bottom: 1px solid var(--border); }
th { color: var(--header); font-weight: 600; }
dl.clist { margin: 0; }
dl.clist > div.row { border-bottom: 1px solid var(--border); padding: .9rem 0; }
dl.clist dt { margin: 0; }
dt code.usage { color: var(--accent-2); font-size: 1rem; border: 0; background: none; padding: 0; }
dl.clist dd { margin: .4rem 0 0; }
.summary { margin: 0; }
.meta { color: var(--dim); font-size: .9rem; margin: .3rem 0 0; }
dl.flags { margin: .6rem 0 0; }
dl.flags > div { display: flex; gap: .75rem; padding: .15rem 0; }
dl.flags dt { flex: 0 0 14rem; }
dl.flags dt code { color: var(--text); }
dl.flags dd { margin: 0; color: var(--dim); }
.provenance { color: var(--dim); font-size: .9rem; border-left: 3px solid var(--accent); padding-left: .75rem; }
[hidden] { display: none !important; }
@media (max-width: 800px) {
  .sidebar {
    position: static;
    width: auto;
    inset: auto;
    max-height: none;
    border-right: 0;
    border-bottom: 1px solid var(--border);
  }
  .sidebar nav ol { display: flex; flex-wrap: wrap; gap: .15rem .5rem; }
  .toc-link { padding: .2rem .4rem; }
  main { margin-left: 0; padding: 1.25rem; }
  dl.flags > div { display: block; }
  dl.flags dt { margin-bottom: .1rem; }
}
</style>
</head>
<body>
<aside class="sidebar">
<div class="search">
<input id="search" type="search" placeholder="Search this page" aria-label="Search this page" autocomplete="off">
<span id="search-count" class="search-count"></span>
</div>
<p class="hint"><kbd>/</kbd> focus <kbd>Esc</kbd> clear</p>
<nav aria-label="Contents">
<ol>
<li><a class="toc-link" href="#sec-introduction">Introduction</a></li>
<li><a class="toc-link" href="#sec-commands">Commands</a></li>
<li><a class="toc-link" href="#sec-keys">Keys</a></li>
<li><a class="toc-link" href="#sec-config">Config</a></li>
<li><a class="toc-link" href="#sec-themes">Themes</a></li>
<li><a class="toc-link" href="#sec-environment">Environment</a></li>
<li><a class="toc-link" href="#sec-files">Files</a></li>
<li><a class="toc-link" href="#sec-actions">Actions</a></li>
</ol>
</nav>
</aside>
<main id="content">
<h1>dm</h1>
`

// htmlPageScript closes the document. The script filters rows as you type,
// hides a section with no match, and binds "/" and Esc. It is inline so the
// page carries no external resource.
const htmlPageScript = `</main>
<script>
(function () {
  var box = document.getElementById('search');
  var count = document.getElementById('search-count');
  var sections = Array.prototype.slice.call(document.querySelectorAll('section.sec'));

  function apply() {
    var q = box.value.trim().toLowerCase();
    var total = 0;
    sections.forEach(function (sec) {
      var rows = Array.prototype.slice.call(sec.querySelectorAll('[data-row]'));
      var shown = 0;
      rows.forEach(function (row) {
        var match = q === '' || row.textContent.toLowerCase().indexOf(q) !== -1;
        row.hidden = !match;
        if (match) { shown += 1; }
      });
      sec.hidden = q !== '' && shown === 0;
      var link = document.querySelector('a.toc-link[href="#' + sec.id + '"]');
      if (link) { link.hidden = sec.hidden; }
      total += shown;
    });
    if (q === '') {
      count.textContent = '';
    } else {
      count.textContent = total + (total === 1 ? ' match' : ' matches');
    }
  }

  box.addEventListener('input', apply);

  document.addEventListener('keydown', function (e) {
    var t = e.target;
    var typing = t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'SELECT' || t.isContentEditable);
    if (e.key === '/' && !typing) {
      e.preventDefault();
      box.focus();
      box.select();
      return;
    }
    if (e.key === 'Escape' && (typing || box.value !== '')) {
      if (box.value !== '') {
        box.value = '';
        apply();
      }
      box.blur();
    }
  });
})();
</script>
</body>
</html>
`

// HTMLPage renders the whole page.
func HTMLPage() string {
	ids := newIDGen()
	var b strings.Builder
	b.WriteString(htmlPageHead)

	b.WriteString(`<section class="sec" id="sec-introduction">`)
	b.WriteString(`<h2 id="introduction">Introduction</h2>`)
	b.WriteString(`<p>dm is a durable terminal multiplexer in one binary. One process is the server: it owns the panes and their pty state, and it keeps running after you leave. A second mode is the TUI client, which draws the panes and sends actions. A third is the CLI, which sends the same actions to the same server, so a script and a hand on the keyboard drive one surface. Detach and reattach, and the shells keep running.</p>`)
	b.WriteString(`<p>A workspace is <code>s1</code>, <code>s2</code>, and so on. A window is <code>w1</code>, <code>w2</code>. A pane is its window id plus <code>p</code> and a per-window sequence: <code>w2p3</code> is the third pane opened in window 2. Ids are readable and constructible, and a counter only moves forward, so a closed id is never reused.</p>`)
	b.WriteString(`<p class="provenance">Where this page comes from: <code>dm html</code> renders it from the Go tables <code>cliCommands</code>, <code>manActionFields</code>, <code>manActionNames</code>, <code>keymap.LoadDefault().HelpEntries()</code> and the <code>configdoc</code> tables.</p>`)
	b.WriteString(`</section>`)

	b.WriteString(htmlCommands(ids))
	b.WriteString(htmlKeys(ids))
	b.WriteString(htmlConfig(ids))
	b.WriteString(htmlThemes(ids))
	b.WriteString(htmlEnvironment(ids))
	b.WriteString(htmlFiles(ids))
	b.WriteString(htmlActions(ids))

	b.WriteString(htmlPageScript)
	return b.String()
}

// htmlCommands renders the COMMANDS section: one row per cliCommands entry,
// with its usage line in monospace and its flag rows under it.
func htmlCommands(ids *idGen) string {
	var b strings.Builder
	b.WriteString(`<section class="sec" id="sec-commands">`)
	b.WriteString(`<h2 id="commands">Commands</h2>`)
	b.WriteString(`<p>A command is the first word after <code>dm</code>. With no command, <code>dm</code> attaches to a session, creating it if needed. Flags take one dash or two: <code>-s</code> and <code>--s</code> are the same flag.</p>`)
	b.WriteString(`<dl class="clist">`)
	for _, c := range cliCommands {
		var text strings.Builder
		text.WriteString(c.line())
		text.WriteString(" ")
		text.WriteString(c.summary)
		text.WriteString(" ")
		text.WriteString(c.group)
		for _, a := range c.aliases {
			text.WriteString(" ")
			text.WriteString(a)
		}
		for _, f := range c.flags {
			text.WriteString(" ")
			text.WriteString(f.name)
			text.WriteString(" ")
			text.WriteString(f.arg)
			text.WriteString(" ")
			text.WriteString(f.desc)
		}
		for _, e := range c.extra {
			text.WriteString(" dm ")
			text.WriteString(c.name)
			text.WriteString(" ")
			text.WriteString(e.args)
			text.WriteString(" ")
			text.WriteString(e.summary)
		}
		text.WriteString(" ")
		text.WriteString(c.example)

		fmt.Fprintf(&b, `<div class="row" id="%s" data-row>`, htmlEscape(ids.id("cmd", c.name)))
		fmt.Fprintf(&b, "<dt><code class=\"usage\">%s</code></dt>", htmlEscape(c.line()))
		b.WriteString("<dd>")
		fmt.Fprintf(&b, `<p class="summary">%s</p>`, htmlEscape(c.summary))
		b.WriteString(`<p class="meta">`)
		fmt.Fprintf(&b, "group %s", htmlEscape(c.group))
		if len(c.aliases) > 0 {
			fmt.Fprintf(&b, " &middot; aliases %s", htmlEscape(strings.Join(c.aliases, ", ")))
		}
		b.WriteString("</p>")
		if len(c.flags) > 0 {
			b.WriteString(`<dl class="flags">`)
			for _, f := range c.flags {
				fmt.Fprintf(&b, `<div id="%s">`, htmlEscape(ids.id("cmd", c.name+" flag "+f.name)))
				fmt.Fprintf(&b, "<dt><code>%s</code></dt>", htmlEscape(f.text()))
				fmt.Fprintf(&b, "<dd>%s</dd>", htmlEscape(f.desc))
				b.WriteString("</div>")
			}
			b.WriteString("</dl>")
		}
		for _, e := range c.extra {
			fmt.Fprintf(&b, `<p class="meta" id="%s">Other form: <code>dm %s %s</code> &middot; %s</p>`,
				htmlEscape(ids.id("cmd", c.name+" form "+e.args)),
				htmlEscape(c.name), htmlEscape(e.args), htmlEscape(e.summary))
		}
		if c.example != "" {
			fmt.Fprintf(&b, "<pre><code>%s</code></pre>", htmlEscape(c.example))
		}
		b.WriteString("</dd></div>")
	}
	b.WriteString(`</dl></section>`)
	return b.String()
}

// htmlKeys renders the KEYS section from the default keymap's help rows.
func htmlKeys(ids *idGen) string {
	var b strings.Builder
	b.WriteString(`<section class="sec" id="sec-keys">`)
	b.WriteString(`<h2 id="keys">Keys</h2>`)
	b.WriteString(`<p>The prefix key is <code>ctrl+b</code> by default. A key label is the key pressed after the prefix. The help overlay prints the label; the action name is the one <code>dm exec</code> and the config use.</p>`)
	b.WriteString("<table><thead><tr><th>Keys</th><th>Action</th><th>Section</th></tr></thead><tbody>")
	for _, e := range keymap.LoadDefault().HelpEntries() {
		section := e.Section
		if section == "" {
			section = "-"
		}
		fmt.Fprintf(&b, `<tr id="%s" data-row><td><code>%s</code></td><td>%s</td><td>%s</td></tr>`,
			htmlEscape(ids.id("key", e.Key+" "+e.Action)),
			htmlEscape(e.Key), htmlEscape(e.Action), htmlEscape(section))
	}
	b.WriteString("</tbody></table></section>")
	return b.String()
}

// htmlConfig renders the CONFIG section: one row per config.kdl node, then the
// built-in keymap patches from configdoc.
func htmlConfig(ids *idGen) string {
	var b strings.Builder
	b.WriteString(`<section class="sec" id="sec-config">`)
	b.WriteString(`<h2 id="config">Config</h2>`)
	b.WriteString(`<p>dm reads <code>$XDG_CONFIG_HOME/dm/config.kdl</code>, or <code>$HOME/.config/dm/config.kdl</code> when that variable is unset. A missing file means the built-in values, and dm writes a default file on first run. The file is a patch over the built-in values: a node the file leaves out keeps its built-in value.</p>`)
	b.WriteString("<table><thead><tr><th>Node</th><th>Value</th><th>Default</th><th>What it does</th></tr></thead><tbody>")
	for _, n := range configdoc.ConfigNodes {
		def := n.Default
		if def == "" {
			def = "none"
		}
		fmt.Fprintf(&b, `<tr id="%s" data-row><td><code>%s</code></td><td>%s</td><td><code>%s</code></td><td>%s</td></tr>`,
			htmlEscape(ids.id("config", n.Name)), htmlEscape(n.Name),
			htmlEscape(n.Value), htmlEscape(def), htmlEscape(n.Desc))
	}
	b.WriteString("</tbody></table>")

	b.WriteString(`<h3 id="sec-keymaps">Keymaps</h3>`)
	b.WriteString(`<p>A keymap mode is a table of actions. A <code>keymap</code> node patches one mode, and an action the file leaves out keeps its built-in key. These are the built-in bindings.</p>`)
	b.WriteString("<table><thead><tr><th>Mode</th><th>Action</th><th>Key</th><th>What it does</th></tr></thead><tbody>")
	for _, m := range configdoc.KeymapModes() {
		for _, a := range m.Actions {
			fmt.Fprintf(&b, `<tr id="%s" data-row><td><code>%s</code></td><td><code>%s</code></td><td><code>%s</code></td><td>%s</td></tr>`,
				htmlEscape(ids.id("keymap", m.Name+" "+a.Name)),
				htmlEscape(m.Name), htmlEscape(a.Name), htmlEscape(a.Key), htmlEscape(a.Desc))
		}
	}
	b.WriteString("</tbody></table></section>")
	return b.String()
}

// htmlThemes renders the THEMES section: one row per theme-file key, then a
// sample file that names every key at its built-in value.
func htmlThemes(ids *idGen) string {
	var b strings.Builder
	b.WriteString(`<section class="sec" id="sec-themes">`)
	b.WriteString(`<h2 id="themes">Themes</h2>`)
	b.WriteString(`<p>A theme file is <code>$XDG_CONFIG_HOME/dm/themes/&lt;name&gt;.kdl</code>. The <code>theme</code> node in config.kdl names one, and no theme node keeps the built-in theme. Every block and every key is optional: the file reads over the built-in theme, so it carries only what it changes. A block or key given twice, an unknown block or key, a colour that is not <code>#rrggbb</code>, or a glyph that is not one cell wide is an error.</p>`)
	b.WriteString("<table><thead><tr><th>Block</th><th>Key</th><th>Built-in</th><th>What it paints</th></tr></thead><tbody>")
	for _, k := range configdoc.ThemeKeys() {
		fmt.Fprintf(&b, `<tr id="%s" data-row><td><code>%s</code></td><td><code>%s</code></td><td><code>%s</code></td><td>%s</td></tr>`,
			htmlEscape(ids.id("theme", k.Block+" "+k.Name)),
			htmlEscape(k.Block), htmlEscape(k.Name), htmlEscape(k.Default), htmlEscape(k.Desc))
	}
	b.WriteString("</tbody></table>")
	b.WriteString(`<h3 id="sec-theme-sample">Sample theme</h3>`)
	b.WriteString(`<p>A theme file that names every key at its built-in value. Pasted as a theme, it paints the built-in theme.</p>`)
	fmt.Fprintf(&b, `<pre><code id="%s">%s</code></pre>`, htmlEscape(ids.id("theme", "sample")), htmlEscape(configdoc.ThemeSampleKDL))
	b.WriteString(`</section>`)
	return b.String()
}

// htmlEnvironment renders the ENVIRONMENT section. A test-only variable stays
// out of the user-facing page.
func htmlEnvironment(ids *idGen) string {
	var b strings.Builder
	b.WriteString(`<section class="sec" id="sec-environment">`)
	b.WriteString(`<h2 id="environment">Environment</h2>`)
	b.WriteString("<table><thead><tr><th>Variable</th><th>What it sets</th></tr></thead><tbody>")
	for _, e := range configdoc.EnvVars {
		if e.TestOnly {
			continue
		}
		fmt.Fprintf(&b, `<tr id="%s" data-row><td><code>%s</code></td><td>%s</td></tr>`,
			htmlEscape(ids.id("env", e.Name)), htmlEscape(e.Name), htmlEscape(e.Desc))
	}
	b.WriteString("</tbody></table></section>")
	return b.String()
}

// htmlFiles renders the FILES section.
func htmlFiles(ids *idGen) string {
	var b strings.Builder
	b.WriteString(`<section class="sec" id="sec-files">`)
	b.WriteString(`<h2 id="files">Files</h2>`)
	b.WriteString(`<p>Paths use <code>$DM_STATE</code> and <code>$XDG_CONFIG_HOME</code> as the Environment section describes.</p>`)
	b.WriteString("<table><thead><tr><th>Path</th><th>What it holds</th><th>dm writes it</th></tr></thead><tbody>")
	for _, f := range configdoc.Files {
		write := "no"
		if f.Write {
			write = "yes"
		}
		fmt.Fprintf(&b, `<tr id="%s" data-row><td><code>%s</code></td><td>%s</td><td>%s</td></tr>`,
			htmlEscape(ids.id("file", f.Path)), htmlEscape(f.Path), htmlEscape(f.Desc), write)
	}
	b.WriteString("</tbody></table></section>")
	return b.String()
}

// htmlActions renders the ACTIONS section: the dm exec JSON fields, the action
// names the server accepts, and what each keymap action does.
func htmlActions(ids *idGen) string {
	var b strings.Builder
	b.WriteString(`<section class="sec" id="sec-actions">`)
	b.WriteString(`<h2 id="actions">Actions</h2>`)
	b.WriteString(`<p><code>dm exec</code> takes one JSON object. The fields are:</p>`)
	b.WriteString("<table><thead><tr><th>Field</th><th>Meaning</th></tr></thead><tbody>")
	for _, f := range manActionFields {
		fmt.Fprintf(&b, `<tr id="%s" data-row><td><code>%s</code></td><td>%s</td></tr>`,
			htmlEscape(ids.id("action", "field "+f.name)), htmlEscape(f.name), htmlEscape(f.desc))
	}
	b.WriteString("</tbody></table>")

	b.WriteString(`<h3 id="sec-action-names">Action names</h3>`)
	b.WriteString(`<p>The <code>action</code> field names one of these. The action is the same over the socket, from a script and from a keymap. Each example is one JSON object, carrying only the fields that action needs.</p>`)
	b.WriteString(`<dl class="clist">`)
	for _, n := range manActionNames {
		ex, ok := actionExampleOf(n)
		if !ok {
			ex = `{"action":"` + n + `"}`
		}
		fmt.Fprintf(&b, `<div class="row" id="%s" data-row>`, htmlEscape(ids.id("action", "name "+n)))
		fmt.Fprintf(&b, "<dt><code>%s</code></dt>", htmlEscape(n))
		fmt.Fprintf(&b, "<dd><pre><code>%s</code></pre></dd></div>", htmlEscape(ex))
	}
	b.WriteString(`</dl>`)

	b.WriteString(`<h3 id="sec-action-desc">Keymap actions</h3>`)
	b.WriteString(`<p>What each action the built-in keymap binds does. The table says what the code does, not what the key is called.</p>`)
	b.WriteString("<table><thead><tr><th>Action</th><th>What it does</th></tr></thead><tbody>")
	for _, m := range configdoc.KeymapModes() {
		for _, a := range m.Actions {
			fmt.Fprintf(&b, `<tr id="%s" data-row><td><code>%s</code></td><td>%s</td></tr>`,
				htmlEscape(ids.id("action", "desc "+a.Name)), htmlEscape(a.Name), htmlEscape(a.Desc))
		}
	}
	b.WriteString("</tbody></table></section>")
	return b.String()
}
