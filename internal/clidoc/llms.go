package clidoc

import (
	"fmt"
	"strings"

	"dm/internal/configdoc"
	"dm/internal/keymap"
)

// LLMsText renders llms.txt: the whole dm surface as one dense markdown page an
// LLM can read in a single pass. It draws from the same table the usage text and
// the man page draw from. docs/llms.txt is a committed snapshot of this output,
// and a test fails when the two drift.

// llmsKeyRow is one keymap row in llms.txt: the bound keys, the action names
// they run, and the help overlay's label for the row.
type llmsKeyRow struct {
	keys    string
	actions string
	label   string
}

// llmsKeys renders the default keymap. Keys and labels come from
// LoadDefault().HelpEntries(); the action names come from the help table, so a
// reader can see both the label and the action the key runs.
func llmsKeys() []llmsKeyRow {
	var rows []llmsKeyRow
	for _, e := range keymap.LoadDefault().HelpEntries() {
		actions := keymap.HelpActions(e.Action)
		llms := make([]string, 0, len(actions))
		for _, a := range actions {
			llms = append(llms, a)
		}
		rows = append(rows, llmsKeyRow{
			keys:    e.Key,
			actions: strings.Join(llms, " / "),
			label:   e.Action,
		})
	}
	return rows
}

// llmsExamples are concrete examples for the commands whose table row carries
// none. The key is the command name, or the name plus the extra usage shape.
var llmsExamples = map[string]string{
	"":                           "dm -s work",
	"attach":                     "dm attach -s work",
	"new":                        "dm new -s work",
	"ls":                         "dm ls",
	"kill":                       "dm kill -s work",
	"purge":                      "dm purge --dry-run",
	"reset":                      "dm reset -s work --force",
	"read":                       "dm read w1p1 --lines 20",
	"read <pane> --scrollback N": "dm read w1p1 --scrollback 200",
	"split":                      "dm split w1p1 --dir right",
	"focus":                      "dm focus w1p2",
	"rename-workspace":           "dm rename-workspace s1 build",
	"next-pane":                  "dm next-pane",
	"prev-pane":                  "dm prev-pane",
	"new-window":                 "dm new-window",
	"next-window":                "dm next-window",
	"prev-window":                "dm prev-window",
	"status":                     "dm status w1p1",
	"list":                       "dm list",
	"dash":                       "dm dash",
	"peek":                       "dm peek w1p1 --lines 10",
	"msg list":                   "dm msg list --key build --limit 20",
	"msg clear":                  "dm msg clear --key build",
	"msg watchers":               "dm msg watchers",
	"msg schema":                 "dm msg schema",
	"man":                        "dm man -o dm.1",
	"llms":                       "dm llms -o llms.txt",
}

// llmsExample is the example a command row prints: its own example when it has
// one, then the table above, then the command line itself.
func llmsExample(c cliCommand) string {
	if c.example != "" {
		return c.example
	}
	if ex, ok := llmsExamples[c.name]; ok {
		return ex
	}
	return c.line()
}

// llmsTableRow is one row of the command table. extra rows carry the alternate
// usage shape of a command, such as the scrollback form of read.
type llmsTableRow struct {
	line    string
	args    string
	summary string
	example string
}

// llmsTableRows flattens cliCommands into the rows the command table prints.
func llmsTableRows() []llmsTableRow {
	var rows []llmsTableRow
	for _, c := range cliCommands {
		summary := c.summary
		if len(c.aliases) > 0 {
			summary += " (alias: " + strings.Join(c.aliases, ", ") + ")"
		}
		line := "dm"
		if c.name != "" {
			line += " " + c.name
		}
		rows = append(rows, llmsTableRow{
			line:    line,
			args:    c.args,
			summary: summary,
			example: llmsExample(c),
		})
		for _, e := range c.extra {
			key := c.name + " " + e.args
			example, ok := llmsExamples[key]
			if !ok {
				example = "dm " + key
			}
			rows = append(rows, llmsTableRow{
				line:    "dm " + key,
				summary: e.summary,
				example: example,
			})
		}
	}
	return rows
}

// llmsCell escapes a table cell. A pipe would end the cell, so it is escaped.
func llmsCell(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}

// llmsActionGroups is how the action table packs its rows. A page that gave
// every action its own row would push llms.txt past its one-pass line budget,
// so the actions with no fields of their own share a row with their nearest
// sibling. Every action in manActionNames appears here exactly once.
var llmsActionGroups = [][]string{
	{"ping", "list"},
	{"read"}, {"history"}, {"replay"}, {"status"},
	{"write-to"}, {"exec"}, {"keys"},
	{"split"}, {"zoom-pane"}, {"set-rail"}, {"resize-split"},
	{"focus"}, {"focus-dir"}, {"next-pane", "prev-pane"}, {"close-pane"},
	{"close-window", "close-workspace"}, {"new-window"},
	{"next-window", "prev-window"}, {"select-window"},
	{"new-workspace"}, {"next-workspace", "prev-workspace"},
	{"list-workspaces", "list-panes"},
	{"pane"}, {"toggle-fold"}, {"set-pane-name"},
	{"focus-window", "focus-workspace"},
	{"rename-window", "rename-workspace", "set-project"},
	{"msg-post"}, {"msg-list"}, {"msg-clear"}, {"msg-wait"}, {"msg-sub"},
	{"msg-watchers", "msg-schema"},
}

// LLMsText renders the whole page.
func LLMsText() string {
	var b strings.Builder

	b.WriteString("# dm\n\n")
	b.WriteString("dm is a durable terminal multiplexer in one binary. One process is the server; it owns the panes and their pty state, and it keeps running after you leave. A second mode is the TUI client, which draws the panes and sends actions. A third is the CLI, which sends the same actions to the same server, so a script and a hand on the keyboard drive one surface. Detach and reattach, and the shells keep running.\n")

	b.WriteString("\n## Identifiers\n\n")
	b.WriteString("A workspace is `s1`, `s2`, and so on. A window is `w1`, `w2`. A pane is its window id plus `p` and a per-window sequence: `w2p3` is the third pane opened in window 2. Ids are readable and constructible, and a counter only moves forward, so a closed id is never reused.\n")

	b.WriteString("\n## Commands\n\n")
	b.WriteString("A command is the first word after `dm`. With no command, `dm` attaches to a session, creating it if needed.\n\n")
	b.WriteString("| Command | Arguments | What it does | Example |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, r := range llmsTableRows() {
		args := r.args
		if args == "" {
			args = "-"
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | %s | `%s` |\n",
			llmsCell(r.line), llmsCell(args), llmsCell(r.summary), llmsCell(r.example))
	}

	b.WriteString("\n## Actions: `dm exec`\n\n")
	b.WriteString("`dm exec` takes one JSON object. The fields are:\n\n")
	b.WriteString("| Field | Meaning |\n")
	b.WriteString("| --- | --- |\n")
	for _, f := range manActionFields {
		fmt.Fprintf(&b, "| `%s` | %s |\n", f.name, f.desc)
	}

	b.WriteString("\nThe action field names one of these. Each example is one JSON object, carrying only the fields that action needs.\n\n")
	b.WriteString("| Action | Example |\n")
	b.WriteString("| --- | --- |\n")
	for _, group := range llmsActionGroups {
		names := make([]string, 0, len(group))
		examples := make([]string, 0, len(group))
		for _, n := range group {
			names = append(names, "`"+n+"`")
			ex, ok := actionExampleOf(n)
			if !ok {
				ex = `{"action":"` + n + `"}`
			}
			examples = append(examples, "`"+llmsCell(ex)+"`")
		}
		fmt.Fprintf(&b, "| %s | %s |\n", strings.Join(names, ", "), strings.Join(examples, ", "))
	}

	b.WriteString("\n## Keys\n\n")
	b.WriteString("The prefix key is `ctrl+b` by default. A key label is the key pressed after the prefix. The help overlay prints the label; the action name is the one `dm exec` and the config use.\n\n")
	b.WriteString("| Keys | Action | Help label |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, k := range llmsKeys() {
		fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", k.keys, k.actions, k.label)
	}

	b.WriteString("\n## Sidebar\n\n")
	b.WriteString("The sidebar is a tree of workspaces, windows and panes. A workspace row is a foldable header. Under an unfolded workspace, each window gets a window row with its fold caret, its rendered window label, branch and dirty counts, and a dim note row when that pane has a commit message. The label falls back to the folder, then the window name, when the template renders empty. The source pane is the focused pane when it carries a branch, else the first pane carrying a branch, else the focused pane, else the window's first pane. The window's pane rows follow when the window is unfolded. A folded workspace hides its windows and panes; a folded window hides only its panes. A left click on a workspace row focuses the first available pane in that workspace. A left click on a workspace caret toggles the workspace fold without changing focus. A left click on a window row focuses the first available pane in that window and makes it the active window. A left click on a window caret toggles the window fold without changing focus. A left click on a pane row focuses that pane. Double-clicking a workspace row opens the workspace rename prompt; double-clicking a window row opens the window rename prompt; double-clicking a pane row opens the pane rename prompt. A right click on any row opens the row menu for that row: rename, close, new, fold and focus, as the row allows. The bottom row of the sidebar is its control: a left click there toggles the rail between full and thin. The `toggle-rail` key cycles full, thin and hidden. A window tab bar sits above the pane area, to the right of the rail. One tab per window in the active workspace. A tab is its label plus one padding cell on each side, clamped to `tab-min-width` and `tab-max-width` from `config.kdl` (defaults 8 and 20 cells); a label wider than the maximum is cut with an ellipsis. The tab label, the window row's leading label and the status bar window chip come from the `window-label` Go template, which defaults to the window name for a named window and the id plus repository folder for an unnamed one. Clicking a tab selects that window; double-clicking opens the window rename prompt.\n")

	b.WriteString("\n## Config\n\n")
	b.WriteString("`$XDG_CONFIG_HOME/dm/config.kdl`, or `$HOME/.config/dm/config.kdl` when XDG_CONFIG_HOME is unset, is a patch over the built-in values: a node the file leaves out keeps its built-in value, and an action left out of a `keymap` block keeps its built-in key. dm writes a default file on first run.\n\n")
	b.WriteString("| Node | Value | Default | What it does |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, n := range configdoc.ConfigNodes {
		def := "none"
		if n.Default != "" {
			def = "`" + llmsCell(n.Default) + "`"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n",
			n.Name, llmsCell(n.Value), def, llmsCell(n.Desc))
	}

	b.WriteString("\n## Keymaps\n\n")
	b.WriteString("Each mode is a table of actions. The table lists the built-in binding; `dm exec` and a `keymap` node use the action name.\n\n")
	b.WriteString("| Mode | Action | Key |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, m := range configdoc.KeymapModes() {
		for _, a := range m.Actions {
			fmt.Fprintf(&b, "| `%s` | `%s` | `%s` |\n", m.Name, a.Name, llmsCell(a.Key))
		}
	}

	b.WriteString("\n## Theme\n\n")
	b.WriteString("A theme file is `$XDG_CONFIG_HOME/dm/themes/<name>.kdl`, named by the `theme` node; no node keeps the built-in theme. Every block and every key is optional, because the file reads over the built-in theme. A colour is `#rrggbb`; a glyph is one rune one cell wide. The built-in values are:\n\n")
	b.WriteString("| Block | Keys and built-in defaults | What it carries |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, blk := range llmsThemeBlocks() {
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", blk.name, llmsCell(blk.keys), llmsCell(blk.desc))
	}

	b.WriteString("\n## Environment\n\n")
	b.WriteString("| Variable | What it sets |\n")
	b.WriteString("| --- | --- |\n")
	for _, e := range configdoc.EnvVars {
		if e.TestOnly {
			continue
		}
		fmt.Fprintf(&b, "| `%s` | %s |\n", e.Name, llmsCell(e.Desc))
	}

	b.WriteString("\n## Files\n\n")
	b.WriteString("| Path | What it holds |\n")
	b.WriteString("| --- | --- |\n")
	for _, f := range configdoc.Files {
		fmt.Fprintf(&b, "| `%s` | %s |\n", llmsCell(f.Path), llmsCell(f.Desc))
	}

	b.WriteString("\n## Recipes\n\n")
	b.WriteString("Attach to a session, creating it if needed:\n\n")
	b.WriteString("```sh\ndm attach -s work\n```\n\n")
	b.WriteString("Run a command in a pane:\n\n")
	b.WriteString("```sh\ndm write-to w1p1 --msg 'go test ./...' --enter\n```\n\n")
	b.WriteString("Read a pane's screen, or rows above it:\n\n")
	b.WriteString("```sh\ndm read w1p1 --lines 20\ndm read w1p1 --scrollback 200\n```\n\n")
	b.WriteString("Split a pane:\n\n")
	b.WriteString("```sh\ndm split w1p1 --dir right\n```\n\n")
	b.WriteString("Close a window and every pane in it:\n\n")
	b.WriteString("```sh\ndm exec '{\"action\":\"close-window\",\"target\":\"w1\"}'\n```\n\n")
	b.WriteString("Post to the box and wait on it:\n\n")
	b.WriteString("```sh\ndm msg post --key build --state done --msg 'all green'\ndm msg wait --key build --state done --timeout 10m\n```\n\n")
	b.WriteString("\n## Gotchas\n\n")
	b.WriteString("- `dm kill` stops a session's server. The panes keep running; attach again and they are there.\n")
	b.WriteString("- `dm purge` removes dead sessions whose panes are gone. Add `--dry-run` to see the list first.\n")
	b.WriteString("- `dm reset` discards a session and its panes for a full restart. It needs `--force`; `--all` resets every session.\n")
	b.WriteString("- `toggle-fold` takes a workspace id, a window id, or `project:<name>` to fold a whole project group.\n")
	b.WriteString("- A bare word is a session name: `dm work` attaches to `work`, it does not run a command called `work`.\n")

	return b.String()
}

// llmsThemeBlock is one theme block row in llms.txt: its name, every key with
// its built-in default, and what the block paints.
type llmsThemeBlock struct {
	name string
	keys string
	desc string
}

// llmsThemeBlockDesc says what each theme block paints.
var llmsThemeBlockDesc = map[string]string{
	"palette": "Rail and status bar colours.",
	"accents": "Workspace accent colours, assigned in order.",
	"glyphs":  "Box, divider, fold and mark glyphs.",
	"states":  "Glyph and colour per pane state: working, idle, blocked, done, error, unknown, shell.",
}

// llmsThemeBlocks groups configdoc.ThemeKeys by block, keeping the theme
// file's order, and spells each key with its built-in default.
func llmsThemeBlocks() []llmsThemeBlock {
	var out []llmsThemeBlock
	for _, k := range configdoc.ThemeKeys() {
		root, state, _ := strings.Cut(k.Block, ".")
		name := k.Name
		if state != "" {
			name = state + "." + k.Name
		}
		entry := "`" + name + "` " + k.Default
		if len(out) > 0 && out[len(out)-1].name == root {
			out[len(out)-1].keys += ", " + entry
			continue
		}
		out = append(out, llmsThemeBlock{name: root, keys: entry, desc: llmsThemeBlockDesc[root]})
	}
	return out
}
