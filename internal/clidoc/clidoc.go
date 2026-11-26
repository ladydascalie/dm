package clidoc

import (
	"fmt"
	"strings"
)

// The CLI surface lives here, once. UsageText renders the help text from it,
// and the man page and llms.txt render from the same rows. A test proves every
// case in runCLI has a row and every row has a case.

// cliFlag is one flag a command accepts. arg is empty for a boolean flag.
type cliFlag struct {
	name string
	arg  string
	desc string
}

// cliExtra is an alternate usage line for a command, such as the scrollback
// form of read. It carries its own summary because the shape differs.
type cliExtra struct {
	args    string
	summary string
}

// cliCommand is one row of the CLI surface. name is the word after dm; an
// empty name is the bare invocation, which attaches. args is the argument
// shape after the name. group is the section the verb renders under, both in
// the top-level help and in the html page.
type cliCommand struct {
	name    string
	aliases []string
	args    string
	summary string
	flags   []cliFlag
	example string
	group   string
	extra   []cliExtra
}

const (
	groupSessions = "sessions"
	groupLayouts  = "layouts"
	groupPanes    = "panes"
	groupWindows  = "windows and workspaces"
	groupObserve  = "observe"
	groupBox      = "message box"
	groupAsk      = "ask"
	groupSecrets  = "secrets"
	groupDocs     = "docs"
)

// cliGroups is every group in the order the sections render, with the header
// each section prints.
var cliGroups = []struct {
	id    string
	title string
}{
	{groupSessions, "Sessions"},
	{groupLayouts, "Layouts"},
	{groupPanes, "Panes"},
	{groupWindows, "Windows and workspaces"},
	{groupObserve, "Observe"},
	{groupBox, "Message box"},
	{groupAsk, "Ask"},
	{groupSecrets, "Secrets"},
	{groupDocs, "Docs"},
}

// cliCommands is the whole CLI, in the order the usage text prints it: rows
// of one group sit together, and the groups follow cliGroups.
var cliCommands = []cliCommand{
	// Sessions.
	// -L and --force ride the args line of the verbs that start a session
	// rather than their flag rows: the layout path registers both itself, so a
	// row that declared them would register each one twice. --force sits inside
	// the -L brackets because it means nothing without a layout: the code
	// refuses it rather than reset a session. The LAYOUTS section says what
	// they do.
	{
		name:    "",
		args:    "[-s name] [-L layout [--force]]",
		summary: "attach to a session, creating it if needed",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupSessions,
	},
	{
		name:    "attach",
		aliases: []string{"a"},
		args:    "[-s name] [-L layout [--force]]",
		summary: "attach to a session, creating it if needed",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupSessions,
	},
	{
		name:    "new",
		args:    "[-s name] [-L layout [--force]]",
		summary: "start a session without attaching",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupSessions,
	},
	{
		name:    "ls",
		aliases: []string{"list-sessions"},
		summary: "list sessions with pane counts and project",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"json", "", "print JSON"},
		},
		group: groupSessions,
	},
	{
		name:    "web",
		args:    "[-s name] [--addr host:port] [--token t] [--read-only]",
		summary: "serve the session to a browser, for a phone on your tailnet",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"addr", "host:port", "listen address"},
			{"token", "t", "access token, generated when empty"},
			{"read-only", "", "refuse keys, messages and actions"},
		},
		example: "dm web --addr 100.71.227.52:8099",
		group:   groupSessions,
	},
	{
		name:    "kill",
		args:    "[-s name]",
		summary: "stop a session's server (panes survive)",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupSessions,
	},
	{
		name:    "purge",
		args:    "[--dry-run]",
		summary: "remove dead sessions whose panes are gone",
		flags:   []cliFlag{{"dry-run", "", "print what would be removed"}},
		group:   groupSessions,
	},
	{
		name:    "reset",
		args:    "[-s name | --all] --force",
		summary: "discard a session and its panes for a full restart",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"all", "", "reset every session"},
			{"force", "", "confirm the discard"},
		},
		group: groupSessions,
	},
	{
		name:    "upgrade",
		args:    "[--check] [--force]",
		summary: "move this binary to the latest release",
		flags: []cliFlag{
			{"check", "", "report whether a newer release exists, and write nothing"},
			{"force", "", "install even from a dev build, or when already current"},
		},
		example: "dm upgrade --check",
		group:   groupSessions,
	},
	{
		name:    "exec",
		args:    "'<json>' [-s name]",
		summary: "send an action as JSON",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"action-json", "", "action JSON"},
		},
		example: `dm exec '{"action":"split","target":"w1p1","dir":"right"}'`,
		group:   groupSessions,
	},

	// Layouts: capture a session's shape under a name, then seed a new
	// session from it.
	{
		name:    "layout save",
		args:    "<name> [-s name] [--force]",
		summary: "capture a session's shape as a named layout file",
		flags: []cliFlag{
			{"s", "name", "session the layout is captured from"},
			{"force", "", "overwrite an existing layout file"},
		},
		example: `dm layout save work -s base`,
		group:   groupLayouts,
	},
	{
		name:    "layout ls",
		summary: "list layout files with their workspace, window and pane counts",
		group:   groupLayouts,
	},
	{
		name:    "layout show",
		args:    "<name>",
		summary: "print a layout file unchanged",
		example: `dm layout show work`,
		group:   groupLayouts,
	},
	{
		name:    "layout rm",
		args:    "<name>",
		summary: "delete a layout file",
		group:   groupLayouts,
	},

	// Panes.
	{
		name:    "write-to",
		aliases: []string{"write"},
		args:    "<pane> --msg T",
		summary: "type text into a pane (--enter to accept)",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"msg", "", "message text"},
			{"enter", "", "press enter after the message"},
		},
		example: `dm write-to w1p1 --msg 'ls -la' --enter`,
		group:   groupPanes,
	},
	{
		name:    "split",
		args:    "<pane> --dir D",
		summary: "split a pane: left right up down",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"dir", "D", "split direction: left right up down"},
			{"argv", "CMD", "run CMD as the new pane's program; repeat once per word"},
			{"json", "", "print JSON"},
		},
		group: groupPanes,
	},
	{
		name:    "focus",
		args:    "<pane>",
		summary: "focus a pane",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupPanes,
	},
	{
		name:    "next-pane",
		summary: "focus the next pane",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupPanes,
	},
	{
		name:    "prev-pane",
		summary: "focus the previous pane",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupPanes,
	},

	// Plugins.
	{
		name:    "plugin list",
		summary: "list the plugins the config declares",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"json", "", "print the server's JSON"},
		},
		group: groupPanes,
	},
	{
		name:    "plugin start",
		args:    "<name> [--split | --window] [--target <pane>]",
		summary: "open a plugin pane",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"split", "", "open beside the focused pane (the default)"},
			{"window", "", "open in a new window"},
			{"target", "PANE", "the pane to open beside, or the source window"},
		},
		group: groupPanes,
	},

	// Windows and workspaces.
	{
		name:    "rename-workspace",
		args:    "<ws> <name>",
		summary: "rename a workspace",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupWindows,
	},
	{
		name:    "set-project",
		args:    "<ws> [name]",
		summary: "set a workspace's project, or clear it with no name",
		flags:   []cliFlag{{"s", "name", "session name"}},
		example: "dm set-project s1 dm",
		group:   groupWindows,
	},
	{
		name:    "new-window",
		args:    "[--argv CMD]...",
		summary: "open a new window",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"argv", "CMD", "run CMD as the new pane's program; repeat once per word"},
		},
		group: groupWindows,
	},
	{
		name:    "next-window",
		summary: "focus the next window",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupWindows,
	},
	{
		name:    "prev-window",
		summary: "focus the previous window",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupWindows,
	},

	// Observe: read a pane or the tree without touching it.
	{
		name:    "status",
		args:    "[pane]",
		summary: "show OSC 7501 agent status",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupObserve,
	},
	{
		name:    "list",
		summary: "print panes as a table, or the raw tree with --json",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"json", "", "print JSON"},
		},
		group: groupObserve,
	},
	{
		name:    "dash",
		args:    "[-s name]",
		summary: "print the palette rows",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupObserve,
	},
	{
		name:    "peek",
		args:    "<id> [--lines N]",
		summary: "print the screen a palette row lands on",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"lines", "N", "number of screen lines"},
		},
		group: groupObserve,
	},
	{
		name:    "wait",
		args:    "<pane> [--timeout D]",
		summary: "stream a pane's output until a command ends, the next one when nothing runs, then name the exit code",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"timeout", "D", "quiet-idle limit, not total"},
			{"json", "", "print JSON"},
		},
		example: `dm wait w1p1 --timeout 10m`,
		group:   groupObserve,
	},
	{
		name:    "read",
		args:    "<pane> [--lines N]",
		summary: "print a pane's screen",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"lines", "N", "number of screen lines"},
			{"scrollback", "N", "history rows above the screen"},
		},
		extra: []cliExtra{
			{args: "<pane> --scrollback N", summary: "print N rows above the screen"},
		},
		group: groupObserve,
	},
	{
		name:    "yank",
		args:    "<pane> [--lines N]",
		summary: "print a pane's output since the last Enter",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"lines", "N", "number of output lines"},
		},
		example: `dm yank w1p1`,
		group:   groupObserve,
	},

	// The box, under dm msg.
	{
		name:    "msg post",
		args:    "--state S [--key K] [--msg T] [--body T | --body-file P]",
		summary: "post one entry to the box",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"state", "S", "box state: note working blocked done error"},
			{"key", "K", "box key"},
			{"msg", "", "message text"},
			{"body", "", "long body text under the message line"},
			{"body-file", "P", "read the body from a path, - for stdin"},
		},
		example: `dm msg post --key build --state done --msg 'all green'`,
		group:   groupBox,
	},
	{
		name:    "msg list",
		args:    "[--key K | --prefix P] [--after SEQ] [--limit N] [--json]",
		summary: "print the box newest first",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"key", "K", "box key"},
			{"prefix", "P", "box key prefix"},
			{"after", "SEQ", "box seq cursor: entries after this one"},
			{"limit", "N", "list rows, 0 for the whole box"},
			{"json", "", "print JSON"},
		},
		group: groupBox,
	},
	{
		name:    "msg show",
		args:    "--key K [--seq N | --from PANE]",
		summary: "print one entry's body, or its summary line",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"key", "K", "box key"},
			{"seq", "N", "the entry's seq"},
			{"from", "PANE", "the posting pane"},
		},
		example: `dm msg show --key build --seq 12`,
		group:   groupBox,
	},
	{
		name:    "msg wait",
		args:    "[--key K | --prefix P] [--state S] [--after SEQ] [--timeout 5m] [--json]",
		summary: "block until an entry matches",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"key", "K", "box key"},
			{"prefix", "P", "box key prefix"},
			{"state", "S", "box state: note working blocked done error"},
			{"after", "SEQ", "box seq cursor: entries after this one"},
			{"timeout", "D", "wait timeout"},
			{"json", "", "print JSON"},
		},
		example: `dm msg wait --key build --state done --timeout 10m`,
		group:   groupBox,
	},
	{
		name:    "msg sub",
		args:    "[--key K | --prefix P] [--state S] [--after SEQ] [--json]",
		summary: "stream entries as they arrive",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"key", "K", "box key"},
			{"prefix", "P", "box key prefix"},
			{"state", "S", "box state: note working blocked done error"},
			{"after", "SEQ", "box seq cursor: entries after this one"},
			{"json", "", "print JSON"},
		},
		example: `dm msg sub --prefix build --json`,
		group:   groupBox,
	},
	{
		name:    "msg clear",
		args:    "[--key K | --prefix P]",
		summary: "clear one key, or a prefix branch, or the whole box",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"key", "K", "box key"},
			{"prefix", "P", "box key prefix"},
		},
		group: groupBox,
	},
	{
		name:    "msg watchers",
		args:    "[--json]",
		summary: "print the live watches",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"json", "", "print JSON"},
		},
		group: groupBox,
	},
	{
		name:    "msg schema",
		summary: "print the box schema the server serves",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupBox,
	},

	// The ask store, under dm ask: the questions an agent puts to the reader.
	// A pi extension in a pane drives these verbs, because it has no other way
	// in. The bodies come from stdin: a question set is too big for argv.
	{
		name:    "ask list",
		args:    "[--json]",
		summary: "print the questions the session lists, oldest first",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"json", "", "print JSON"},
		},
		group: groupAsk,
	},
	{
		name:    "ask open",
		args:    "--pane P --tool-call ID [--json]",
		summary: "open an ask on a pane, reading {questions} on stdin",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"pane", "P", "pane the ask belongs to"},
			{"tool-call", "ID", "the agent's tool call the questions belong to"},
			{"json", "", "print JSON"},
		},
		example: `echo '{"questions":[{"question":"Which base?","header":"base","options":[{"label":"main"}]}]}' | dm ask open --pane w1p1 --tool-call toolu_1`,
		group:   groupAsk,
	},
	{
		name:    "ask await",
		args:    "<id> [--timeout 25s] [--json]",
		summary: "block until the ask is answered, or print pending",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"timeout", "D", "wait timeout, capped at 25s"},
			{"json", "", "print JSON"},
		},
		example: `dm ask await a1 --json`,
		group:   groupAsk,
	},
	{
		name:    "ask answer",
		args:    "<id> [--json]",
		summary: "answer an ask, reading {items,globalNote,cancel} on stdin",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"json", "", "print JSON"},
		},
		example: `echo '{"items":[{"questionIndex":0,"optionIndexes":[0]}]}' | dm ask answer a1`,
		group:   groupAsk,
	},
	{
		name:    "ask close",
		args:    "<id> [--json]",
		summary: "drop an ask from the store, idempotently",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"json", "", "print JSON"},
		},
		group: groupAsk,
	},

	// Secrets: the registry, the proxy endpoint and the value's routes. A
	// fetched value never reaches stdout, argv, a log or the state file.
	{
		name:    "secret register",
		args:    "<name> --tool op --ref R [--upstream URL --header T] [--env K=V]...",
		summary: "register a secret: how to fetch it and where it rides",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"tool", "op", "tool that prints the value: op or pass"},
			{"ref", "R", "reference the tool resolves: an op:// URI or a store path"},
			{"upstream", "URL", "pin the proxy upstream the secret rides to"},
			{"header", "T", "proxy header template, {} marks the value slot"},
			{"env", "K=V", "extra environment entry for the tool; repeatable, credential names refused"},
		},
		example: `dm secret register gh-cli --tool op --ref 'op://Personal/github/token' --upstream https://api.github.com --header 'Authorization: Bearer {}'`,
		group:   groupSecrets,
	},
	{
		name:    "secret list",
		summary: "print registered secret names and references only",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupSecrets,
	},
	{
		name:    "secret revoke",
		args:    "<name>",
		summary: "remove a secret and drop its value everywhere",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupSecrets,
	},
	{
		name:    "secret use",
		args:    "<name>",
		summary: "fetch a secret, redact it in every pane, and print the proxy endpoint URL when the secret rides one",
		flags:   []cliFlag{{"s", "name", "session name"}},
		example: `dm secret use gh-cli`,
		group:   groupSecrets,
	},
	{
		name:    "secret hydrate",
		args:    "<name> --to P",
		summary: "fetch a secret and write it to a file at 0600",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"to", "P", "file path to write"},
		},
		group: groupSecrets,
	},
	{
		name:    "secret type",
		args:    "<name> --to PANE",
		summary: "fetch a secret and type it into a pane",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"to", "PANE", "target pane id"},
		},
		example: `dm secret type gh-cli --to w1p1`,
		group:   groupSecrets,
	},
	{
		name:    "secret sync",
		args:    "[--from M] [--trust]",
		summary: "apply a .dm-secrets.kdl mapping: register its secrets, ingest its files, redact everywhere",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"from", "M", "mapping path, default .dm-secrets.kdl in the working directory"},
			{"trust", "", "trust a mapping outside the config directory: allow secret blocks and file paths anywhere"},
		},
		example: `dm secret sync`,
		group:   groupSecrets,
	},

	// Documentation.
	{
		name:    "version",
		args:    "[--json]",
		summary: "print the version, commit and platform",
		flags:   []cliFlag{{"json", "", "print JSON"}},
		group:   groupDocs,
	},
	{
		name:    "man",
		args:    "[-o path]",
		summary: "print the man page, or write it to a file",
		flags:   []cliFlag{{"o", "path", "write the page to this path, or - for stdout"}},
		group:   groupDocs,
	},
	{
		name:    "llms",
		args:    "[-o path]",
		summary: "print llms.txt, or write it to a file",
		flags:   []cliFlag{{"o", "path", "write the document to this path, or - for stdout"}},
		group:   groupDocs,
	},
	{
		name:    "html",
		args:    "[-o path]",
		summary: "open this page as HTML in a browser, or write it to a file",
		flags:   []cliFlag{{"o", "path", "write the page to this path, or - for stdout"}},
		example: "dm html -o dm.html",
		group:   groupDocs,
	},
	{
		name:    "provision",
		args:    "[--check | --print | --uninstall]",
		summary: "link this checkout's extension and skill into the pi harness",
		flags: []cliFlag{
			{"check", "", "report each target and exit non-zero when one is not ok"},
			{"print", "", "print the plan and write nothing"},
			{"uninstall", "", "remove the links this checkout owns"},
		},
		example: "dm provision --check",
		group:   groupDocs,
	},
}

// usageHeader is the first line of the help text.
const usageHeader = "dm - durable terminal multiplexer (prototype)"

// usageFooter is the closing block: the action JSON is the same on every
// command that takes one.
const usageFooter = `Actions are the same everywhere:
  {"action":"write-to","target":"w1p1","msg":"ls","enter":true}
`

// usageMaxWidth is the widest command line that still carries a summary. A
// longer line prints alone, so the box verbs do not push every summary right.
// The width holds the longest of the verbs that start a session, with its
// layout flags, without letting the box verbs in.
const usageMaxWidth = 42

// cliLine is the command line a row prints, without the leading indent.
func (c cliCommand) line() string {
	s := "dm"
	if c.name != "" {
		s += " " + c.name
	}
	if c.args != "" {
		s += " " + c.args
	}
	return s
}

// usageRow is one line of a help block: the left column and its summary.
type usageRow struct {
	line    string
	summary string
}

// writeRows renders rows in the table's two-column style: the left column
// aligned, a summary only when the line fits the width.
func writeRows(b *strings.Builder, rows []usageRow) {
	width := 0
	for _, r := range rows {
		if r.summary != "" && len(r.line) <= usageMaxWidth {
			width = max(width, len(r.line))
		}
	}
	for _, r := range rows {
		b.WriteString("  ")
		b.WriteString(r.line)
		if r.summary != "" && len(r.line) <= usageMaxWidth {
			b.WriteString(strings.Repeat(" ", width-len(r.line)+2))
			b.WriteString(r.summary)
		}
		b.WriteString("\n")
	}
}

// Flag is one flag a verb accepts, as the table declares it. Arg is empty for
// a boolean flag.
type Flag struct {
	Name string
	Arg  string
	Desc string
}

// Verbs lists every verb name the table carries, in table order, including
// the msg subcommands. The bare attach row has no name and is not listed.
func Verbs() []string {
	var names []string
	for _, c := range cliCommands {
		if c.name != "" {
			names = append(names, c.name)
		}
	}
	return names
}

// VerbFlags returns the flags one verb's row declares, or nil when the table
// has no row for the name.
func VerbFlags(verb string) []Flag {
	c, ok := verbRow(verb)
	if !ok {
		return nil
	}
	flags := make([]Flag, 0, len(c.flags))
	for _, f := range c.flags {
		flags = append(flags, Flag{Name: f.name, Arg: f.arg, Desc: f.desc})
	}
	return flags
}

// verbRow finds one row by name or alias.
func verbRow(verb string) (cliCommand, bool) {
	for _, c := range cliCommands {
		if c.name == verb {
			return c, true
		}
		for _, a := range c.aliases {
			if a == verb {
				return c, true
			}
		}
	}
	return cliCommand{}, false
}

// UsageText renders the top-level help: one section per group, in table
// order, with a two-line gap between sections.
func UsageText() string {
	sections := make([]string, 0, len(cliGroups))
	for _, g := range cliGroups {
		var rows []usageRow
		for _, c := range cliCommands {
			if c.group != g.id {
				continue
			}
			rows = append(rows, usageRow{c.line(), c.summary})
			for _, e := range c.extra {
				rows = append(rows, usageRow{"dm " + c.name + " " + e.args, e.summary})
			}
		}
		var b strings.Builder
		b.WriteString(g.title)
		b.WriteString("\n")
		writeRows(&b, rows)
		sections = append(sections, b.String())
	}
	var out strings.Builder
	out.WriteString(usageHeader)
	out.WriteString("\n\n")
	out.WriteString(strings.Join(sections, "\n\n"))
	out.WriteString("\n")
	out.WriteString(usageFooter)
	return out.String()
}

// VerbHelp renders one verb's help: the summary line from the table, its
// alternate forms, its flags in the table's two-column style, then a hint
// naming dm help and dm <verb> -h.
func VerbHelp(verb string) string {
	c, ok := verbRow(verb)
	if !ok {
		return ""
	}
	var b strings.Builder
	b.WriteString(c.line())
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(c.summary)
	b.WriteString("\n")
	for _, e := range c.extra {
		b.WriteString("dm ")
		b.WriteString(c.name)
		b.WriteString(" ")
		b.WriteString(e.args)
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(e.summary)
		b.WriteString("\n")
	}
	if len(c.flags) > 0 {
		b.WriteString("\n")
		rows := make([]usageRow, 0, len(c.flags))
		for _, f := range c.flags {
			rows = append(rows, usageRow{f.text(), f.desc})
		}
		writeRows(&b, rows)
	}
	fmt.Fprintf(&b, "\n  run dm help for every command, or dm %s -h for this help.\n", verb)
	return b.String()
}

// MsgSubcommands renders the list dm msg prints with no subcommand: one row
// per msg verb, in table order.
func MsgSubcommands() string {
	var b strings.Builder
	b.WriteString("dm msg\n")
	var rows []usageRow
	for _, c := range cliCommands {
		if strings.HasPrefix(c.name, "msg ") {
			rows = append(rows, usageRow{c.line(), c.summary})
		}
	}
	writeRows(&b, rows)
	b.WriteString("\n  run dm help for every command, or dm msg <verb> -h for one verb.\n")
	return b.String()
}

// AskSubcommands is the bare ask verb's help: one row per subverb.
func AskSubcommands() string {
	var b strings.Builder
	b.WriteString("dm ask\n")
	var rows []usageRow
	for _, c := range cliCommands {
		if strings.HasPrefix(c.name, "ask ") {
			rows = append(rows, usageRow{c.line(), c.summary})
		}
	}
	writeRows(&b, rows)
	b.WriteString("\n  run dm help for every command, or dm ask <verb> -h for one verb.\n")
	return b.String()
}

// SecretSubcommands is the bare secret verb's help: one row per subverb.
func SecretSubcommands() string {
	var b strings.Builder
	b.WriteString("dm secret\n")
	var rows []usageRow
	for _, c := range cliCommands {
		if strings.HasPrefix(c.name, "secret ") {
			rows = append(rows, usageRow{c.line(), c.summary})
		}
	}
	writeRows(&b, rows)
	b.WriteString("\n  run dm help for every command, or dm secret <verb> -h for one verb.\n")
	return b.String()
}

// PluginSubcommands is the bare plugin verb's help: one row per subverb.
func PluginSubcommands() string {
	var b strings.Builder
	b.WriteString("dm plugin\n")
	var rows []usageRow
	for _, c := range cliCommands {
		if strings.HasPrefix(c.name, "plugin ") {
			rows = append(rows, usageRow{c.line(), c.summary})
		}
	}
	writeRows(&b, rows)
	b.WriteString("\n  run dm help for every command, or dm plugin <verb> -h for one verb.\n")
	return b.String()
}

// cliFlagText renders one flag the way a man page names it.
func (f cliFlag) text() string {
	if f.arg == "" {
		return "--" + f.name
	}
	return fmt.Sprintf("--%s %s", f.name, f.arg)
}
