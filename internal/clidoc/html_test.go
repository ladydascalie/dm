package clidoc

import (
	"encoding/json"
	"strings"
	"testing"

	"dm/internal/configdoc"
	"dm/internal/keymap"
	"dm/internal/theme"
)

// TestEveryActionHasAParsedExample checks every action the server accepts has
// one example in the tables, and that example is valid JSON naming the action.
// A broken example teaches an LLM to send a broken request.
func TestEveryActionHasAParsedExample(t *testing.T) {
	for _, n := range manActionNames {
		ex, ok := actionExampleOf(n)
		if !ok {
			t.Errorf("action %q has no example", n)
			continue
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(ex), &body); err != nil {
			t.Errorf("example for %q does not parse: %v", n, err)
			continue
		}
		if body["action"] != n {
			t.Errorf("example for %q carries action %v", n, body["action"])
		}
	}
}

// TestHTMLPageCarriesEverySection checks the page renders one section per part
// of the surface, under the stable ids the page's navigation relies on.
func TestHTMLPageCarriesEverySection(t *testing.T) {
	page := HTMLPage()
	for _, id := range []string{
		"sec-introduction", "sec-commands", "sec-keys", "sec-config",
		"sec-keymaps", "sec-themes", "sec-theme-sample", "sec-prompt-marks",
		"sec-environment", "sec-files", "sec-actions", "sec-action-names",
		"sec-action-desc",
	} {
		if !strings.Contains(page, `id="`+id+`"`) {
			t.Errorf("page has no section %q", id)
		}
	}
}

// TestHTMLPageNamesEveryRow checks the page carries a row for every command,
// key, config node, theme key, prompt mark, environment variable, file and
// action. A row the page drops is a row the docs lose.
func TestHTMLPageNamesEveryRow(t *testing.T) {
	page := HTMLPage()
	for _, c := range cliCommands {
		if !strings.Contains(page, htmlEscape(c.line())) {
			t.Errorf("page does not carry the usage line %q", c.line())
		}
		if !strings.Contains(page, htmlEscape(c.summary)) {
			t.Errorf("page does not carry the summary of %q", c.name)
		}
		for _, f := range c.flags {
			if !strings.Contains(page, htmlEscape(f.text())) {
				t.Errorf("page does not carry flag %q of %q", f.name, c.name)
			}
		}
	}
	for _, e := range keymap.LoadDefault().HelpEntries() {
		if !strings.Contains(page, htmlEscape(e.Key)) {
			t.Errorf("page does not carry the key %q", e.Key)
		}
		if !strings.Contains(page, htmlEscape(e.Action)) {
			t.Errorf("page does not carry the key action %q", e.Action)
		}
	}
	for _, n := range configdoc.ConfigNodes {
		if !strings.Contains(page, htmlEscape(n.Name)) || !strings.Contains(page, htmlEscape(n.Desc)) {
			t.Errorf("page does not carry the config node %q", n.Name)
		}
	}
	for _, k := range configdoc.ThemeKeys() {
		if !strings.Contains(page, htmlEscape(k.Name)) || !strings.Contains(page, htmlEscape(k.Default)) {
			t.Errorf("page does not carry the theme key %q", k.Name)
		}
	}
	for _, m := range PromptMarks {
		if !strings.Contains(page, htmlEscape(m.Meaning)) {
			t.Errorf("page does not carry prompt mark %q", m.Letter)
		}
	}
	for _, e := range configdoc.EnvVars {
		if e.TestOnly {
			if strings.Contains(page, htmlEscape(e.Name)) {
				t.Errorf("page names the test-only variable %q", e.Name)
			}
			continue
		}
		if !strings.Contains(page, htmlEscape(e.Name)) || !strings.Contains(page, htmlEscape(e.Desc)) {
			t.Errorf("page does not carry the variable %q", e.Name)
		}
	}
	for _, f := range configdoc.Files {
		if !strings.Contains(page, htmlEscape(f.Path)) || !strings.Contains(page, htmlEscape(f.Desc)) {
			t.Errorf("page does not carry the file %q", f.Path)
		}
	}
	for _, f := range manActionFields {
		if !strings.Contains(page, htmlEscape(f.desc)) {
			t.Errorf("page does not carry the action field %q", f.name)
		}
	}
	for _, n := range manActionNames {
		if !strings.Contains(page, htmlEscape(n)) {
			t.Errorf("page does not name the action %q", n)
		}
	}
}

// TestHTMLPageEscapesUsageLines checks angle brackets in a usage line reach the
// page as text, not as markup the browser reads.
func TestHTMLPageEscapesUsageLines(t *testing.T) {
	page := HTMLPage()
	if !strings.Contains(page, htmlEscape("<pane> --msg T")) {
		t.Error("page does not carry the write-to usage line")
	}
	if strings.Contains(page, "<pane>") {
		t.Error("page carries an unescaped angle-bracket argument")
	}
}

// TestHTMLPageIsSelfContained checks the page loads with no network: no script
// source, no image, no style import, no external font. The one allowed URL is
// the semantic-prompts proposal, which is a citation, not a resource.
func TestHTMLPageIsSelfContained(t *testing.T) {
	page := HTMLPage()
	for _, bad := range []string{"<script src", "<img", "<link ", "@import", "url(", "srcset"} {
		if strings.Contains(page, bad) {
			t.Errorf("page is not self-contained: contains %q", bad)
		}
	}
	if !strings.Contains(page, PromptMarkSpec) {
		t.Error("page does not cite the semantic prompts proposal")
	}
	if n := strings.Count(page, "http"); n > 1 {
		t.Errorf("page carries %d URLs, want only the proposal citation", n)
	}
}

// TestIDGenHandsOutUniqueStableIDs pins the id generator: the first use of a
// text gets the bare base, repeats get a numbered suffix, and the sequence is
// stable across pages so the committed snapshot does not churn.
func TestIDGenHandsOutUniqueStableIDs(t *testing.T) {
	g := newIDGen()
	first := g.id("cmd", "msg post")
	if first != "cmd-msg-post" {
		t.Fatalf("first id = %q", first)
	}
	second := g.id("cmd", "msg post")
	third := g.id("cmd", "msg post")
	if second != "cmd-msg-post-2" || third != "cmd-msg-post-3" {
		t.Fatalf("repeat ids = %q, %q", second, third)
	}
	if again := newIDGen(); again.id("cmd", "msg post") != first {
		t.Error("ids are not stable across pages")
	}
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		id := g.id("k", "same")
		if seen[id] {
			t.Errorf("id %q handed out twice", id)
		}
		seen[id] = true
	}
}

// TestHTMLSlugSlugsLikeAnAnchor checks the slug is lowercase, keeps letters and
// digits, and collapses every other run to one dash.
func TestHTMLSlugSlugsLikeAnAnchor(t *testing.T) {
	cases := map[string]string{
		"msg post":   "msg-post",
		"New Window": "new-window",
		"a  b":       "a-b",
		" -- ":       "x",
		"":           "x",
		"A-1":        "a-1",
	}
	for in, want := range cases {
		if got := htmlSlug(in); got != want {
			t.Errorf("htmlSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestHTMLEscapeEscapesMarkup pins the escaper on the characters that change
// markup: angle brackets, the ampersand, quotes and the apostrophe.
func TestHTMLEscapeEscapesMarkup(t *testing.T) {
	got := htmlEscape(`<a href="x">&'`)
	want := "&lt;a href=&#34;x&#34;&gt;&amp;&#39;"
	if got != want {
		t.Errorf("htmlEscape = %q, want %q", got, want)
	}
}

// TestThemeVarsSpellThePalette checks the CSS block names every colour the
// page paints, one per line, and the two colour spellers round-trip a value.
func TestThemeVarsSpellThePalette(t *testing.T) {
	vars := htmlThemeVars()
	for _, name := range []string{
		"--bg", "--panel", "--panel-2", "--text", "--dim", "--header",
		"--accent", "--accent-2", "--link", "--add", "--del", "--warn",
		"--border", "--code-bg",
	} {
		if !strings.Contains(vars, name+": #") {
			t.Errorf("theme vars do not spell %s as a colour", name)
		}
	}
	if n := strings.Count(vars, "\n") + 1; n != 14 {
		t.Errorf("theme vars spell %d lines, want 14", n)
	}
	c := theme.RGB{R: 0x12, G: 0x34, B: 0x56}
	if got := htmlRGB(c); got != "#123456" {
		t.Errorf("htmlRGB = %q, want #123456", got)
	}
	if got := htmlRGBA(c, 0x40); got != "#12345640" {
		t.Errorf("htmlRGBA = %q, want #12345640", got)
	}
}

// TestLLMsCellEscapesPipes checks a pipe in a cell cannot end the table row,
// and text without pipes is untouched.
func TestLLMsCellEscapesPipes(t *testing.T) {
	if got := llmsCell("a|b"); got != `a\|b` {
		t.Errorf("llmsCell = %q, want a\\|b", got)
	}
	if got := llmsCell("no pipes"); got != "no pipes" {
		t.Errorf("llmsCell changed text without pipes: %q", got)
	}
}

// TestLLMsExampleFallsBackThroughTheTables checks the example chain: a row's
// own example, then the curated table, then the usage line.
func TestLLMsExampleFallsBackThroughTheTables(t *testing.T) {
	if got := llmsExample(cliCommand{example: "dm one"}); got != "dm one" {
		t.Errorf("own example lost: %q", got)
	}
	if got := llmsExample(cliCommand{name: "attach"}); got != "dm attach -s work" {
		t.Errorf("curated example lost: %q", got)
	}
	if got := llmsExample(cliCommand{name: "zzz", args: "<pane>"}); got != "dm zzz <pane>" {
		t.Errorf("fallback is not the usage line: %q", got)
	}
}

// TestLLMsTableRowsCarryTheExtraForms checks the flattened rows include the
// alternate usage shapes, each with its curated example.
func TestLLMsTableRowsCarryTheExtraForms(t *testing.T) {
	rows := llmsTableRows()
	wantLine := "dm read <pane> --scrollback N"
	found := false
	for _, r := range rows {
		if r.line == wantLine {
			found = true
			if r.example != "dm read w1p1 --scrollback 200" {
				t.Errorf("row %q carries example %q", wantLine, r.example)
			}
			if r.summary == "" {
				t.Errorf("row %q has no summary", wantLine)
			}
		}
	}
	if !found {
		t.Errorf("no row %q", wantLine)
	}
	extra := 0
	for _, c := range cliCommands {
		extra += len(c.extra)
	}
	if len(rows) != len(cliCommands)+extra {
		t.Errorf("llmsTableRows = %d rows, want %d commands + %d extras", len(rows), len(cliCommands), extra)
	}
}

// TestLLMsTextCarriesEverySection checks llms.txt renders every section of the
// surface and keeps the test-only variables out.
func TestLLMsTextCarriesEverySection(t *testing.T) {
	doc := LLMsText()
	for _, head := range []string{
		"## Identifiers", "## Commands", "## Actions: `dm exec`", "## Keys",
		"## Sidebar", "## Config", "## Keymaps", "## Theme", "## Prompt marks",
		"## Environment", "## Files", "## Recipes", "## Gotchas",
	} {
		if !strings.Contains(doc, head) {
			t.Errorf("llms.txt has no %q", head)
		}
	}
	for _, n := range manActionNames {
		if !strings.Contains(doc, "`"+n+"`") {
			t.Errorf("llms.txt does not name the action %q", n)
		}
	}
	for _, e := range configdoc.EnvVars {
		if e.TestOnly && strings.Contains(doc, "`"+e.Name+"`") {
			t.Errorf("llms.txt names the test-only variable %q", e.Name)
		}
	}
	// The prompt-mark hook and the compact mark line render verbatim.
	if !strings.Contains(doc, PromptMarkHook) {
		t.Error("llms.txt does not carry the prompt-mark hook")
	}
	if !strings.Contains(doc, PromptMarkLine) {
		t.Error("llms.txt does not carry the compact mark line")
	}
}

// TestLLMsThemeBlocksGroupByBlock checks the theme table groups keys under
// their block, keeps the block's description, and spells each key with its
// built-in default.
func TestLLMsThemeBlocksGroupByBlock(t *testing.T) {
	blocks := llmsThemeBlocks()
	if len(blocks) == 0 {
		t.Fatal("no theme blocks")
	}
	seen := map[string]bool{}
	for _, blk := range blocks {
		if seen[blk.name] {
			t.Errorf("theme block %q appears twice", blk.name)
		}
		seen[blk.name] = true
		if blk.keys == "" {
			t.Errorf("theme block %q carries no keys", blk.name)
		}
		if blk.desc != llmsThemeBlockDesc[blk.name] {
			t.Errorf("theme block %q carries desc %q", blk.name, blk.desc)
		}
	}
	// Every theme key appears in exactly one block row.
	for _, k := range configdoc.ThemeKeys() {
		_, state, _ := strings.Cut(k.Block, ".")
		name := k.Name
		if state != "" {
			name = state + "." + k.Name
		}
		entry := "`" + name + "` " + k.Default
		n := 0
		for _, blk := range blocks {
			n += strings.Count(blk.keys, entry)
		}
		if n != 1 {
			t.Errorf("theme key %s appears %d times across the blocks", entry, n)
		}
	}
}
