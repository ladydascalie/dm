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
// shape after the name. group orders the rows in the usage text.
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
	groupSession = "session"
	groupPane    = "pane"
	groupBox     = "box"
	groupDocs    = "docs"
)

// cliCommands is the whole CLI, in the order the usage text prints it.
var cliCommands = []cliCommand{
	// Sessions.
	{
		name:    "",
		args:    "[-s name]",
		summary: "attach to a session, creating it if needed",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupSession,
	},
	{
		name:    "attach",
		aliases: []string{"a"},
		args:    "[-s name]",
		summary: "attach to a session, creating it if needed",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupSession,
	},
	{
		name:    "new",
		args:    "[-s name]",
		summary: "start a session without attaching",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupSession,
	},
	{
		name:    "ls",
		aliases: []string{"list-sessions"},
		summary: "list sessions with pane counts and project",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"json", "", "print JSON"},
		},
		group: groupSession,
	},
	{
		name:    "kill",
		args:    "[-s name]",
		summary: "stop a session's server (panes survive)",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupSession,
	},
	{
		name:    "purge",
		args:    "[--dry-run]",
		summary: "remove dead sessions whose panes are gone",
		flags:   []cliFlag{{"dry-run", "", "print what would be removed"}},
		group:   groupSession,
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
		group: groupSession,
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
		group:   groupSession,
	},

	// Panes and windows.
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
		group:   groupPane,
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
		group: groupPane,
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
		group:   groupPane,
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
		group:   groupPane,
	},
	{
		name:    "split",
		args:    "<pane> --dir D",
		summary: "split a pane: left right up down",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"dir", "D", "split direction: left right up down"},
			{"json", "", "print JSON"},
		},
		group: groupPane,
	},
	{
		name:    "focus",
		args:    "<pane>",
		summary: "focus a pane",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupPane,
	},
	{
		name:    "rename-workspace",
		args:    "<ws> <name>",
		summary: "rename a workspace",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupPane,
	},
	{
		name:    "set-project",
		args:    "<ws> [name]",
		summary: "set a workspace's project, or clear it with no name",
		flags:   []cliFlag{{"s", "name", "session name"}},
		example: "dm set-project s1 dm",
		group:   groupPane,
	},
	{
		name:    "next-pane",
		summary: "focus the next pane",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupPane,
	},
	{
		name:    "prev-pane",
		summary: "focus the previous pane",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupPane,
	},
	{
		name:    "new-window",
		summary: "open a new window",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupPane,
	},
	{
		name:    "next-window",
		summary: "focus the next window",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupPane,
	},
	{
		name:    "prev-window",
		summary: "focus the previous window",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupPane,
	},
	{
		name:    "status",
		args:    "[pane]",
		summary: "show OSC 7501 agent status",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupPane,
	},
	{
		name:    "list",
		summary: "print panes as a table, or the raw tree with --json",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"json", "", "print JSON"},
		},
		group: groupPane,
	},
	{
		name:    "dash",
		args:    "[-s name]",
		summary: "print the palette rows",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupPane,
	},
	{
		name:    "peek",
		args:    "<id> [--lines N]",
		summary: "print the screen a palette row lands on",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"lines", "N", "number of screen lines"},
		},
		group: groupPane,
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
		args:    "[--key K | --prefix P] [--state S] [--after SEQ] [--timeout 5m]",
		summary: "block until an entry matches",
		flags: []cliFlag{
			{"s", "name", "session name"},
			{"key", "K", "box key"},
			{"prefix", "P", "box key prefix"},
			{"state", "S", "box state: note working blocked done error"},
			{"after", "SEQ", "box seq cursor: entries after this one"},
			{"timeout", "D", "wait timeout"},
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

	// Documentation.
	{
		name:    "man",
		args:    "[-o path]",
		summary: "print the man page, or write it to a file",
		flags:   []cliFlag{{"o", "path", "write the page to this path"}},
		group:   groupDocs,
	},
	{
		name:    "llms",
		args:    "[-o path]",
		summary: "print llms.txt, or write it to a file",
		flags:   []cliFlag{{"o", "path", "write the document to this path"}},
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
const usageMaxWidth = 40

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

// UsageText renders the help text from cliCommands. Rows print in table order,
// grouped the way the table groups them, with the command column aligned.
func UsageText() string {
	type row struct {
		line    string
		summary string
	}
	var rows []row
	for _, c := range cliCommands {
		rows = append(rows, row{c.line(), c.summary})
		for _, e := range c.extra {
			rows = append(rows, row{"dm " + c.name + " " + e.args, e.summary})
		}
	}
	width := 0
	for _, r := range rows {
		if r.summary != "" && len(r.line) <= usageMaxWidth {
			width = max(width, len(r.line))
		}
	}
	var b strings.Builder
	b.WriteString(usageHeader)
	b.WriteString("\n\n")
	for _, r := range rows {
		b.WriteString("  ")
		b.WriteString(r.line)
		if r.summary != "" && len(r.line) <= usageMaxWidth {
			b.WriteString(strings.Repeat(" ", width-len(r.line)+2))
			b.WriteString(r.summary)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(usageFooter)
	return b.String()
}

// cliFlagText renders one flag the way a man page names it.
func (f cliFlag) text() string {
	if f.arg == "" {
		return "--" + f.name
	}
	return fmt.Sprintf("--%s %s", f.name, f.arg)
}
