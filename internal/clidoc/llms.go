package clidoc

import (
	"fmt"
	"strings"

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

	b.WriteString("\n## Keys\n\n")
	b.WriteString("The prefix key is `ctrl+b` by default. A key label is the key pressed after the prefix. The help overlay prints the label; the action name is the one `dm exec` and the config use.\n\n")
	b.WriteString("| Keys | Action | Help label |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, k := range llmsKeys() {
		fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", k.keys, k.actions, k.label)
	}

	b.WriteString("\n## Sidebar\n\n")
	b.WriteString("The sidebar lists workspaces, windows and panes. A left click on a pane row focuses that pane. A left click on a window row folds or unfolds that window. A right click on any row opens the row menu for that row: rename and close for a workspace, window or pane, new window, new workspace, fold or unfold. The bottom row of the sidebar is its control: a left click there toggles the rail between full and thin. The `toggle-rail` key cycles full, thin and hidden.\n")

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
	b.WriteString("List sessions:\n\n")
	b.WriteString("```sh\ndm ls\n```\n")

	b.WriteString("\n## Gotchas\n\n")
	b.WriteString("- `dm kill` stops a session's server. The panes keep running; attach again and they are there.\n")
	b.WriteString("- `dm purge` removes dead sessions whose panes are gone. Add `--dry-run` to see the list first.\n")
	b.WriteString("- A bare word is a session name: `dm work` attaches to `work`, it does not run a command called `work`.\n")

	return b.String()
}
