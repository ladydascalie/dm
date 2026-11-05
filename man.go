package main

import (
	"fmt"
	"strings"
)

// manPage renders the man page from cliCommands, the same table the usage text
// comes from. docs/dm.1 is a committed snapshot of this output, and a test
// fails when the two drift.

// manDate is the page date. It changes only when the page changes.
const manDate = "2026-11-06"

// manField is one JSON field dm exec accepts.
type manField struct {
	name string
	desc string
}

// manActionFields is the JSON surface of dm exec, in the order the page lists
// it. The names are the json tags on Msg.
var manActionFields = []manField{
	{"action", "the action name, required"},
	{"target", "the pane, window or workspace id the action acts on"},
	{"dir", "split or focus direction: left, right, up or down"},
	{"other", "resize-split: the pane on the other side of the split"},
	{"msg", "text to type into a pane"},
	{"enter", "press enter after msg"},
	{"name", "a new name for a window, workspace or pane"},
	{"window", "new-workspace: the window name"},
	{"index", "select-window: the window number; set-rail: the rail width"},
	{"id", "request id, echoed in the reply"},
	{"cwd", "working directory for a new pane, window or workspace"},
	{"ratio", "split ratio, 0 to 1"},
	{"nofocus", "do not move focus to the new pane"},
	{"cols", "terminal columns"},
	{"rows", "terminal rows"},
	{"keys", "raw bytes to write to a pane"},
	{"data", "raw bytes"},
	{"count", "history rows above the screen"},
	{"res", "the verb payload for the msg-* actions"},
}

// manActionNames is every action the server accepts through dm exec.
var manActionNames = []string{
	"ping", "list", "read", "history", "replay", "status",
	"write-to", "exec", "keys",
	"split", "zoom-pane", "set-rail", "resize-split",
	"focus", "focus-dir", "next-pane", "prev-pane", "close-pane",
	"close-window", "new-window", "next-window", "prev-window", "select-window",
	"new-workspace", "next-workspace", "prev-workspace", "list-workspaces",
	"list-panes", "pane", "toggle-fold", "set-pane-name",
	"focus-window", "focus-workspace", "close-workspace",
	"rename-window", "rename-workspace",
	"msg-post", "msg-list", "msg-clear", "msg-wait", "msg-sub",
	"msg-watchers", "msg-schema",
}

// manEnv is one environment variable the page documents.
type manEnv struct {
	name string
	desc string
}

var manEnvs = []manEnv{
	{"DM_STATE", "state directory; overrides XDG_STATE_HOME"},
	{"XDG_STATE_HOME", "state directory base; dm uses $XDG_STATE_HOME/dm"},
	{"XDG_CONFIG_HOME", "config directory base; dm reads $XDG_CONFIG_HOME/dm/config.kdl"},
	{"DM_SESSION", "the session a pane belongs to; dm refuses to attach that session from inside it"},
	{"DM_PANE", "the pane id a pane's shell runs in"},
	{"DM_NERD", "1 forces Nerd font glyphs, 0 forces the Unicode fallback"},
	{"DM_NOTIFY", "0 turns desktop notifications off"},
	{"COLUMNS", "terminal width when the client cannot ask the terminal"},
	{"SHELL", "the shell a new pane runs"},
	{"TMUX", "set inside tmux; dm uses it to pick its terminal behaviour"},
}

// manFiles is the file layout the page documents.
var manFiles = []string{
	"$DM_STATE/sessions/<name>/state.json",
	"$DM_STATE/sessions/<name>/server.sock",
	"$DM_STATE/sessions/<name>/panes/<id>.sock",
	"$DM_STATE/sessions/<name>/panes/<id>.log",
	"$XDG_CONFIG_HOME/dm/config.kdl",
}

// roffEscape makes text safe for a roff line. A backslash becomes \e, and a
// leading dot or apostrophe, which roff reads as a control line, gets \&.
func roffEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\e`)
	if strings.HasPrefix(s, ".") || strings.HasPrefix(s, "'") {
		s = `\&` + s
	}
	return s
}

// roffBold wraps escaped text in bold.
func roffBold(s string) string { return `\fB` + roffEscape(s) + `\fP` }

// roffItalic wraps escaped text in italic.
func roffItalic(s string) string { return `\fI` + roffEscape(s) + `\fP` }

// manPage renders the whole page.
func manPage() string {
	var b strings.Builder
	fmt.Fprintf(&b, ".TH DM 1 %q %q %q\n", manDate, "dm", "User Commands")

	b.WriteString(".SH NAME\n")
	b.WriteString("dm \\- durable terminal multiplexer\n")

	b.WriteString(".SH SYNOPSIS\n")
	b.WriteString(".B dm\n")
	b.WriteString("[\\fIcommand\\fP] [\\fIoptions\\fP]\n")
	b.WriteString(".br\n")
	b.WriteString(".B dm\n")
	b.WriteString("[\\fB\\-s\\fP \\fIname\\fP]\n")

	b.WriteString(".SH COMMANDS\n")
	b.WriteString("A command is the first word after dm. With no command, dm attaches to a session, creating it if needed.\n")
	for _, c := range cliCommands {
		b.WriteString(".TP\n")
		b.WriteString(roffBold(c.line()) + "\n")
		b.WriteString(roffEscape(c.summary) + "\n")
		if len(c.aliases) > 0 {
			b.WriteString(".br\n")
			b.WriteString("Aliases: " + roffEscape(strings.Join(c.aliases, ", ")) + ".\n")
		}
		for _, f := range c.flags {
			b.WriteString(".br\n")
			b.WriteString(roffBold(f.text()) + " " + roffEscape(f.desc) + "\n")
		}
		for _, e := range c.extra {
			b.WriteString(".br\n")
			b.WriteString(roffBold("dm "+c.name+" "+e.args) + " " + roffEscape(e.summary) + "\n")
		}
		if c.example != "" {
			b.WriteString(".br\n")
			b.WriteString("Example: " + roffBold(c.example) + "\n")
		}
	}

	b.WriteString(".SH ACTIONS\n")
	b.WriteString("dm exec takes one JSON object. The fields are:\n")
	for _, f := range manActionFields {
		b.WriteString(".TP\n")
		b.WriteString(roffBold(f.name) + "\n")
		b.WriteString(roffEscape(f.desc) + "\n")
	}
	b.WriteString(".PP\n")
	b.WriteString("The action field names one of: " + roffEscape(strings.Join(manActionNames, ", ")) + ".\n")

	b.WriteString(".SH KEYS\n")
	b.WriteString("The default keymap. A key label is the key pressed after the prefix key.\n")
	for _, e := range LoadDefault().HelpEntries() {
		b.WriteString(".TP\n")
		b.WriteString(roffBold(e.Key) + "\n")
		action := e.Action
		if e.Section != "" {
			action += " (" + e.Section + ")"
		}
		b.WriteString(roffEscape(action) + "\n")
	}

	b.WriteString(".SH FILES\n")
	for _, f := range manFiles {
		b.WriteString(".TP\n")
		b.WriteString(roffBold(f) + "\n")
	}

	b.WriteString(".SH ENVIRONMENT\n")
	for _, e := range manEnvs {
		b.WriteString(".TP\n")
		b.WriteString(roffBold(e.name) + "\n")
		b.WriteString(roffEscape(e.desc) + "\n")
	}

	b.WriteString(".SH SEE ALSO\n")
	b.WriteString(".BR tmux (1)\n")

	return b.String()
}
