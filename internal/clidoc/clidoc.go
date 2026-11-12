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
	groupPanes    = "panes"
	groupWindows  = "windows and workspaces"
	groupObserve  = "observe"
	groupBox      = "message box"
	groupDocs     = "docs"
)

// cliGroups is every group in the order the sections render, with the header
// each section prints.
var cliGroups = []struct {
	id    string
	title string
}{
	{groupSessions, "Sessions"},
	{groupPanes, "Panes"},
	{groupWindows, "Windows and workspaces"},
	{groupObserve, "Observe"},
	{groupBox, "Message box"},
	{groupDocs, "Docs"},
}

// cliCommands is the whole CLI, in the order the usage text prints it: rows
// of one group sit together, and the groups follow cliGroups.
var cliCommands = []cliCommand{
	// Sessions.
	{
		name:    "",
		args:    "[-s name]",
		summary: "attach to a session, creating it if needed",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupSessions,
	},
	{
		name:    "attach",
		aliases: []string{"a"},
		args:    "[-s name]",
		summary: "attach to a session, creating it if needed",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupSessions,
	},
	{
		name:    "new",
		args:    "[-s name]",
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
		summary: "open a new window",
		flags:   []cliFlag{{"s", "name", "session name"}},
		group:   groupWindows,
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

// cliFlagText renders one flag the way a man page names it.
func (f cliFlag) text() string {
	if f.arg == "" {
		return "--" + f.name
	}
	return fmt.Sprintf("--%s %s", f.name, f.arg)
}
