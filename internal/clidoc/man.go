package clidoc

import (
	"fmt"
	"strings"

	"dm/internal/configdoc"
	"dm/internal/keymap"
)

// ManPage renders the man page from cliCommands, the same table the usage text
// comes from. docs/dm.1 is a committed snapshot of this output, and a test
// fails when the two drift.

// manDate is the page date. It changes only when the page changes.
const manDate = "2026-11-08"

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
	"ping", "list", "read", "history", "yank", "replay", "status",
	"write-to", "exec", "keys",
	"split", "zoom-pane", "set-rail", "resize-split",
	"focus", "focus-dir", "next-pane", "prev-pane", "close-pane",
	"close-window", "new-window", "next-window", "prev-window", "select-window",
	"new-workspace", "next-workspace", "prev-workspace", "list-workspaces",
	"list-panes", "pane", "toggle-fold", "set-pane-name",
	"focus-window", "focus-workspace", "close-workspace",
	"rename-window", "rename-workspace", "set-project",
	"msg-post", "msg-list", "msg-clear", "msg-wait", "msg-sub",
	"msg-watchers", "msg-schema",
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

// ManPage renders the whole page.
func ManPage() string {
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
	b.WriteString("The action field names one of these. Each example is one JSON object, carrying only the fields that action needs.\n")
	for _, n := range manActionNames {
		b.WriteString(".TP\n")
		b.WriteString(roffBold(n) + "\n")
		ex, ok := actionExampleOf(n)
		if !ok {
			ex = `{"action":"` + n + `"}`
		}
		b.WriteString(".nf\n")
		b.WriteString(roffEscape(ex) + "\n")
		b.WriteString(".fi\n")
	}

	b.WriteString(".SH KEYS\n")
	b.WriteString("The default keymap. A key label is the key pressed after the prefix key.\n")
	for _, e := range keymap.LoadDefault().HelpEntries() {
		b.WriteString(".TP\n")
		b.WriteString(roffBold(e.Key) + "\n")
		action := e.Action
		if e.Section != "" {
			action += " (" + e.Section + ")"
		}
		b.WriteString(roffEscape(action) + "\n")
	}

	b.WriteString(manKeymaps())

	b.WriteString(manConfig())
	b.WriteString(manTheme())

	b.WriteString(".SH FILES\n")
	b.WriteString("Paths use $DM_STATE and $XDG_CONFIG_HOME as ENVIRONMENT describes.\n")
	for _, f := range configdoc.Files {
		b.WriteString(".TP\n")
		b.WriteString(roffBold(f.Path) + "\n")
		b.WriteString(roffEscape(f.Desc) + "\n")
	}

	b.WriteString(".SH ENVIRONMENT\n")
	for _, e := range configdoc.EnvVars {
		if e.TestOnly {
			continue
		}
		b.WriteString(".TP\n")
		b.WriteString(roffBold(e.Name) + "\n")
		b.WriteString(roffEscape(e.Desc) + "\n")
	}

	b.WriteString(".SH SEE ALSO\n")
	b.WriteString(".BR tmux (1)\n")

	return b.String()
}

// manKeymaps renders the KEYMAPS section: one entry per mode, and the actions
// the built-in config binds in it.
func manKeymaps() string {
	var b strings.Builder
	b.WriteString(".SH KEYMAPS\n")
	b.WriteString("A keymap mode is a table of actions. A keymap node in config.kdl patches one mode, and an action the file leaves out keeps its built-in key. The built-in bindings are:\n")
	for _, m := range configdoc.KeymapModes() {
		b.WriteString(".TP\n")
		b.WriteString(roffBold(m.Name) + "\n")
		if m.Desc != "" {
			b.WriteString(roffEscape(m.Desc) + "\n")
		}
		parts := make([]string, 0, len(m.Actions))
		for _, a := range m.Actions {
			parts = append(parts, roffBold(a.Name)+" "+roffItalic(a.Key))
		}
		if len(parts) > 0 {
			b.WriteString(".br\n")
			b.WriteString(strings.Join(parts, ", ") + "\n")
		}
	}
	return b.String()
}

// manConfig renders the CONFIGURATION section from configdoc.ConfigNodes.
func manConfig() string {
	var b strings.Builder
	b.WriteString(".SH CONFIGURATION\n")
	b.WriteString("dm reads $XDG_CONFIG_HOME/dm/config.kdl, or $HOME/.config/dm/config.kdl when XDG_CONFIG_HOME is unset. A missing file means the built-in values; dm writes a default file on first run. The file is a patch over the built-in values: a node the file leaves out keeps its built-in value. The nodes are:\n")
	for _, n := range configdoc.ConfigNodes {
		b.WriteString(".TP\n")
		b.WriteString(roffBold(n.Name) + "\n")
		b.WriteString("Value: " + roffItalic(n.Value) + ". ")
		if n.Default == "" {
			b.WriteString("Default: none.\n")
		} else {
			b.WriteString("Default: " + roffItalic(n.Default) + ".\n")
		}
		b.WriteString(roffEscape(n.Desc) + "\n")
	}
	return b.String()
}

// manTheme renders the THEME section from configdoc.ThemeKeys, grouped by
// block in the order the keys appear in a theme file.
func manTheme() string {
	var b strings.Builder
	b.WriteString(".SH THEME\n")
	b.WriteString("A theme file is $XDG_CONFIG_HOME/dm/themes/<name>.kdl. The theme node in config.kdl names one, and no theme node keeps the built-in theme. Every block and every key is optional: the file reads over the built-in theme, so it carries only what it changes. A block or key given twice, an unknown block or key, a colour that is not #rrggbb, or a glyph that is not one cell wide is an error. The keys are:\n")
	block := ""
	for _, k := range configdoc.ThemeKeys() {
		root, state, _ := strings.Cut(k.Block, ".")
		if root != block {
			block = root
			b.WriteString(".PP\n")
			b.WriteString(roffBold(root) + "\n")
		}
		name := k.Name
		if state != "" {
			name = state + " " + k.Name
		}
		b.WriteString(".TP\n")
		b.WriteString(roffBold(name) + "\n")
		b.WriteString(roffEscape(k.Desc) + " Default " + roffItalic(k.Default) + ".\n")
	}
	return b.String()
}
