package configdoc

import (
	"fmt"

	"dm/internal/keymap"
	"dm/internal/theme"
)

// ConfigNodes lists every top-level config.kdl node in the order the parser
// accepts them.
var ConfigNodes = []ConfigNode{
	{
		Name:    "prefix",
		Value:   `a quoted key name, one sequence`,
		Default: `"ctrl+b"`,
		Desc:    "Set the prefix key that opens the main keymap.",
	},
	{
		Name:    "default-fold",
		Value:   `"fold" or "unfold"`,
		Default: `"fold"`,
		Desc:    "Set whether a new window's rail starts folded or unfolded. A reload applies it to windows created after it.",
	},
	{
		Name:    "tab-min-width",
		Value:   "an integer of at least 3 cells",
		Default: "8",
		Desc:    "Set the smallest tab width in the window tab bar.",
	},
	{
		Name:    "tab-max-width",
		Value:   "an integer of at least 3 cells, not below tab-min-width",
		Default: "20",
		Desc:    "Set the largest tab width before the label is cut.",
	},
	{
		Name:    "padding",
		Value:   "an integer from 0 to 4 cells",
		Default: "0",
		Desc:    "Set the blank cells between a pane's border and its content. A reload resizes every pane.",
	},
	{
		Name:    "history-lines",
		Value:   "an integer from 100 to 32768 lines",
		Default: "2000",
		Desc:    "Set the scrollback window and the pane ring budget: about 256 bytes per line, capped at 8 MiB.",
	},
	{
		Name:    "glyphs",
		Value:   `"auto", "ascii", "unicode" or "nerd"`,
		Default: `"auto"`,
		Desc:    "Choose the glyph vocabulary the chrome draws with.",
	},
	{
		Name:    "window-label",
		Value:   "a quoted Go template",
		Default: `"{{if .Named}}{{.Name}}{{else}}{{.ID}}{{with base .Root}} {{.}}{{end}}{{end}}"`,
		Desc:    "Set the template that renders a window label.",
	},
	{
		Name:    "dashboard-preview",
		Value:   "an integer from 0 to 100",
		Default: "60",
		Desc:    "Set the share of the dashboard body the preview band opens at. Zero opens on the table alone; the peek key still shows the band.",
	},
	{
		Name:    "diff-command",
		Value:   "a quoted shell command",
		Default: `"git diff"`,
		Desc:    "Set the command the dashboard diff key runs in a floating pane. It runs through sh -c in the selected pane folder.",
	},
	{
		Name:    "dashboard-layout",
		Value:   `"full" or "float"`,
		Default: `"full"`,
		Desc:    "Draw the dashboard over the whole screen, or as a palette centred over the panes.",
	},
	{
		Name:    "theme",
		Value:   `a quoted theme name matching [A-Za-z0-9_-]+`,
		Default: "",
		Desc:    "Select a theme from the themes directory beside config.kdl; no node keeps the built-in theme.",
	},
	{
		Name:    "keymap",
		Value:   `a quoted mode name and a { "action" "key" } block`,
		Default: "the built-in table",
		Desc:    "Patch one keymap mode; an action the file leaves out keeps its built-in key.",
	},
	{
		Name:    "command",
		Value:   `a quoted name matching [A-Za-z0-9_-]+ and a { run "..." } block`,
		Default: "none",
		Desc:    `Define a command a keymap can run: bind "command:<name>" to a key and it runs the run text through sh -c in the window's folder.`,
	},
}

// EnvVars lists every environment variable dm reads. The source comment names
// the file that reads each one.
var EnvVars = []EnvVar{
	// internal/paths/paths.go
	{Name: "DM_STATE", Desc: "State directory; overrides XDG_STATE_HOME."},
	// internal/paths/paths.go
	{Name: "XDG_STATE_HOME", Desc: "State directory base; dm uses $XDG_STATE_HOME/dm."},
	// internal/keymap/config_load.go
	{Name: "XDG_CONFIG_HOME", Desc: "Config directory base; dm reads $XDG_CONFIG_HOME/dm/config.kdl."},
	// internal/keymap/config_load.go
	{Name: "HOME", Desc: "Home directory base; dm reads $HOME/.config/dm and $HOME/.local/state/dm when the XDG variables are unset."},
	// internal/cli/cli.go and internal/client/client_model.go
	{Name: "DM_SESSION", Desc: "The session a pane belongs to; dm refuses to attach that session from inside it."},
	// internal/cli/cli.go
	{Name: "DM_PANE", Desc: "The pane id a pane's shell runs in."},
	// internal/rail/rail.go
	{Name: "DM_NERD", Desc: "1 forces Nerd Font glyphs, 0 forces the Unicode fallback."},
	// internal/notify/notify.go
	{Name: "DM_NOTIFY", Desc: "0 turns desktop notifications off."},
	// internal/client/client_model.go
	{Name: "DM_STATS", Desc: `Non-empty prints two stderr lines when the client exits: "dm: style cache: <hits> hits, <misses> misses, <rate>% hit rate" and "dm: pacing: <frames> frames written, <wakes> wakes, <ms>ms interval".`},
	// internal/cli/cli.go
	{Name: "COLUMNS", Desc: "Terminal width when the client cannot ask the terminal."},
	// internal/pane/pane.go and internal/server/server_core.go
	{Name: "SHELL", Desc: "The shell a new pane runs."},
	// internal/client/client_input.go
	{Name: "TMUX", Desc: "Set inside tmux; dm uses it to pick its terminal behaviour."},
	// internal/esctest/esctest.go
	{Name: "ESCTEST_DIR", Desc: "Test only: the esctest2 directory the conformance harness searches.", TestOnly: true},
}

// Files lists the state and config files dm reads and writes.
var Files = []File{
	{
		Path:  "$DM_STATE/sessions/<name>/state.json",
		Desc:  "Session state: workspaces, windows, panes, layouts and names.",
		Write: true,
	},
	{
		Path:  "$DM_STATE/sessions/<name>/server.sock",
		Desc:  "Unix socket the session server listens on.",
		Write: true,
	},
	{
		Path:  "$DM_STATE/sessions/<name>/panes/<id>.sock",
		Desc:  "Unix socket one pane's mux listens on.",
		Write: true,
	},
	{
		Path:  "$DM_STATE/sessions/<name>/panes/<id>.log",
		Desc:  "Pane output log.",
		Write: true,
	},
	{
		Path:  "$XDG_CONFIG_HOME/dm/config.kdl",
		Desc:  "Prefix, keymaps, commands, default-fold, tab widths, padding, glyphs, window label and theme name; written on first run.",
		Write: true,
	},
	{
		Path:  "$XDG_CONFIG_HOME/dm/themes/<name>.kdl",
		Desc:  "Theme palette, accents, glyphs and state styles.",
		Write: false,
	},
}

// ActionDesc describes every action the built-in keymap binds. The text says
// what the code does, not what the key is called.
var ActionDesc = map[string]string{
	// main
	"send-prefix":         "Send the prefix key through to the pane instead of waiting for a key command.",
	"reload":              "Reload config.kdl and its theme file.",
	"split-right":         "Split the focused pane side by side, with the new pane on the right.",
	"split-down":          "Split the focused pane top and bottom, with the new pane below.",
	"split-down-alt":      "Split the focused pane top and bottom, with the new pane below; alternative key.",
	"split-left":          "Split the focused pane side by side, with the new pane on the left.",
	"split-up":            "Split the focused pane top and bottom, with the new pane above.",
	"scrollback":          "Open the focused pane's scrollback.",
	"copy-output":         "Copy the focused pane's output since the last Enter.",
	"copy-screen":         "Copy the focused pane's visible screen.",
	"copy-pane-id":        "Copy a pane's id to the clipboard: the focused pane, or the selected row in the dashboard.",
	"focus-left":          "Move focus to the pane left of the focused pane.",
	"focus-down":          "Move focus to the pane below the focused pane.",
	"focus-up":            "Move focus to the pane above the focused pane.",
	"focus-right":         "Move focus to the pane right of the focused pane.",
	"next-pane":           "Focus the next pane in the window.",
	"prev-pane":           "Focus the previous pane in the window.",
	"close-pane":          "Close the focused pane.",
	"close-window":        "Close the active window and every pane in it.",
	"zoom-pane":           "Toggle the focused pane between zoomed and tiled.",
	"new-window":          "Create a window in the active workspace.",
	"next-window":         "Select the next window in the active workspace.",
	"prev-window":         "Select the previous window in the active workspace.",
	"swap-next":           "Swap the focused pane with the neighbour to its right.",
	"swap-prev":           "Swap the focused pane with the neighbour to its left.",
	"next-layout":         "Cycle the active window to the next preset layout.",
	"synchronize-panes":   "Toggle synchronization of panes in the active window; on and false set, absent toggles.",
	"new-workspace":       "Create a workspace.",
	"next-workspace":      "Select the next workspace.",
	"switcher":            "Open the dashboard: a full-screen table of every pane.",
	"picker":              "Open the session picker.",
	"rename-workspace":    "Rename the active workspace.",
	"set-project":         "Set or clear the active workspace project.",
	"toggle-rail":         "Cycle the rail between full, thin and hidden.",
	"message-box":         "Open the message box.",
	"notification-centre": "Open the notification centre: recent notify/ entries.",
	"detach":              "Detach the client and leave the session running.",
	"detach-alt":          "Detach the client and leave the session running; alternative key.",
	"window-1":            "Select window 1 in the active workspace.",
	"window-2":            "Select window 2 in the active workspace.",
	"window-3":            "Select window 3 in the active workspace.",
	"window-4":            "Select window 4 in the active workspace.",
	"window-5":            "Select window 5 in the active workspace.",
	"window-6":            "Select window 6 in the active workspace.",
	"window-7":            "Select window 7 in the active workspace.",
	"window-8":            "Select window 8 in the active workspace.",
	"window-9":            "Select window 9 in the active workspace.",
	"window-10":           "Select window 10 in the active workspace.",
	"help":                "Open the help overlay.",
	"dismiss-esc":         "Dismiss the current overlay without choosing anything.",
	"focus-up-arrow":      "Move focus to the pane above the focused pane; arrow key alternative.",
	"focus-down-arrow":    "Move focus to the pane below the focused pane; arrow key alternative.",
	"focus-left-arrow":    "Move focus to the pane left of the focused pane; arrow key alternative.",
	"focus-right-arrow":   "Move focus to the pane right of the focused pane; arrow key alternative.",

	// menu
	"down":       "Move the selection down one row.",
	"down-arrow": "Move the selection down one row; arrow key alternative.",
	"up":         "Move the selection up one row.",
	"up-arrow":   "Move the selection up one row; arrow key alternative.",
	"pick":       "Run the selected item.",
	"close":      "Close the overlay without choosing anything.",
	"close-esc":  "Close the overlay without choosing anything; esc is an alternative close key.",
	"pick-1":     "Run item 1.",
	"pick-2":     "Run item 2.",
	"pick-3":     "Run item 3.",
	"pick-4":     "Run item 4.",
	"pick-5":     "Run item 5.",
	"pick-6":     "Run item 6.",
	"pick-7":     "Run item 7.",
	"pick-8":     "Run item 8.",
	"pick-9":     "Run item 9.",

	// picker
	"close-s":     "Close the picker without choosing; s is an alternative close key.",
	"close-left":  "Close the picker without choosing; left is an alternative close key.",
	"close-right": "Close the picker without choosing; right is an alternative close key.",

	// switcher
	"close-ctrl-c":    "Close the overlay without choosing; ctrl+c is an alternative close key.",
	"filter":          "Start filtering; in filter mode every printable key edits the query and esc leaves it.",
	"clear-query":     "Clear the dashboard query.",
	"backspace":       "Delete the last character of the query or name.",
	"down-ctrl-n":     "Move the selection down one row; ctrl+n is an alternative key.",
	"up-ctrl-p":       "Move the selection up one row; ctrl+p is an alternative key.",
	"sort":            "Cycle the dashboard ordering: recent, project, status.",
	"preview-up":      "Scroll the preview toward older rows.",
	"preview-down":    "Scroll the preview toward newer rows.",
	"preview-bigger":  "Widen the preview band.",
	"preview-smaller": "Narrow the preview band.",
	"peek":            "Hide or show the preview band.",
	"diff":            "Run the diff command on the selected pane in a floating pane.",

	// scroll and box
	"page-up":     "Scroll up one page.",
	"page-down":   "Scroll down one page.",
	"top":         "Jump to the top of the list or scrollback.",
	"bottom":      "Jump to the bottom of the list or scrollback.",
	"close-q":     "Close without choosing anything; q is an alternative close key.",
	"toggle-page": "Switch the message box between the entry page and the list page.",

	// scroll
	"close-bracket": "Close the scrollback; [ is an alternative close key.",

	// rename
	"cancel":       "Cancel the rename and keep the old name.",
	"cancel-up":    "Cancel the rename; up is an alternative cancel key.",
	"cancel-down":  "Cancel the rename; down is an alternative cancel key.",
	"cancel-left":  "Cancel the rename; left is an alternative cancel key.",
	"cancel-right": "Cancel the rename; right is an alternative cancel key.",
	"submit":       "Save the new name.",
	"clear":        "Clear the whole input.",

	// box
	"close-m":      "Close the message box; m is an alternative close key.",
	"close-prefix": "Close the message box; the prefix key is an alternative close key.",

	// help
	"search":         "Start a search in the open view.",
	"next-match":     "Jump to the next search match.",
	"prev-match":     "Jump to the previous search match.",
	"close-question": "Close help; ? is an alternative close key.",
}

// modeDesc names the moment each keymap mode takes keys.
var modeDesc = map[string]string{
	"main":     "The table the prefix key opens.",
	"menu":     "The right-click menu.",
	"picker":   "The session picker.",
	"switcher": "The dashboard, a full-screen pane table.",
	"scroll":   "The scrollback view.",
	"rename":   "The rename prompt.",
	"box":      "The message box.",
	"help":     "The keymap view and the armed help overlay.",
}

// pickFold is the run of numbered pick actions the docs print as one row. The
// menu, the picker and the dashboard bind the same nine keys to the same job,
// so nine rows per mode say no more than one. The help overlay folds the window
// digits the same way.
var pickFold = [9]string{
	"pick-1", "pick-2", "pick-3", "pick-4", "pick-5",
	"pick-6", "pick-7", "pick-8", "pick-9",
}

// KeymapModes returns every mode the built-in config binds, in the order the
// keymap view lists them. Action names and keys come from LoadDefault, so a
// change to the default keymap reaches this table without an edit here.
func KeymapModes() []Mode {
	src := keymap.LoadDefault().Keymaps()
	out := make([]Mode, 0, len(src))
	for _, km := range src {
		m := Mode{Name: km.Name, Desc: modeDesc[km.Name]}
		for _, a := range km.Actions {
			m.Actions = append(m.Actions, Action{
				Name: a.Action,
				Key:  a.Key,
				Desc: ActionDesc[a.Action],
			})
		}
		m.Actions = foldPicks(m.Actions)
		out = append(out, m)
	}
	return out
}

// foldPicks prints a complete run of numbered pick actions as one row, named by
// the whole run so a config still learns the action pattern. A partial run
// stays as it is, so the table never claims a key the mode does not bind.
func foldPicks(actions []Action) []Action {
	out := make([]Action, 0, len(actions))
	for i := 0; i < len(actions); i++ {
		if actions[i].Name != pickFold[0] {
			out = append(out, actions[i])
			continue
		}
		n := 1
		for n < len(pickFold) && i+n < len(actions) && actions[i+n].Name == pickFold[n] {
			n++
		}
		if n < len(pickFold) {
			out = append(out, actions[i])
			continue
		}
		out = append(out, Action{
			Name: pickFold[0] + ".." + pickFold[len(pickFold)-1],
			Key:  actions[i].Key + "-" + actions[i+n-1].Key,
			Desc: "Run item 1 to 9.",
		})
		i += n - 1
	}
	return out
}

// stateNames is the order the state keys appear in a theme file and in this
// table.
var stateNames = []string{"working", "idle", "blocked", "done", "error", "unknown", "shell"}

// ThemeKeys returns every theme-file key with the built-in value mapped from
// DefaultTheme. A field rename in the theme package fails the build here, so
// the defaults are never retyped by hand.
func ThemeKeys() []ThemeKey {
	d := theme.DefaultTheme()
	keys := []ThemeKey{
		{"palette", "rail-bg", rgbHex(d.Palette.RailBG), "Rail background."},
		{"palette", "rail-focus-bg", rgbHex(d.Palette.RailFocusBG), "Focused rail row background."},
		{"palette", "rail-hover-bg", rgbHex(d.Palette.RailHoverBG), "Hovered rail row background."},
		{"palette", "rail-text", rgbHex(d.Palette.RailText), "Primary rail text."},
		{"palette", "rail-dim", rgbHex(d.Palette.RailDim), "Dim rail text and secondary rows."},
		{"palette", "rail-header", rgbHex(d.Palette.RailHeader), "Workspace header text."},
		{"palette", "rail-window", rgbHex(d.Palette.RailWindow), "Window row text."},
		{"palette", "rail-diff-add", rgbHex(d.Palette.RailDiffAdd), "Added-line count."},
		{"palette", "rail-diff-del", rgbHex(d.Palette.RailDiffDel), "Deleted-line count."},
		{"palette", "rail-working", rgbHex(d.Palette.RailWorking), "Working state text in the rail."},
		{"palette", "bar-accent", rgbHex(d.Palette.BarAccent), "Status bar accent."},

		{"accents", "accent-1", rgbHex(d.Accents[0]), "Workspace accent colour 1."},
		{"accents", "accent-2", rgbHex(d.Accents[1]), "Workspace accent colour 2."},
		{"accents", "accent-3", rgbHex(d.Accents[2]), "Workspace accent colour 3."},
		{"accents", "accent-4", rgbHex(d.Accents[3]), "Workspace accent colour 4."},
		{"accents", "accent-5", rgbHex(d.Accents[4]), "Workspace accent colour 5."},
		{"accents", "accent-6", rgbHex(d.Accents[5]), "Workspace accent colour 6."},

		{"glyphs", "grip", d.Glyphs.Grip, "Drag grip on a pane divider."},
		{"glyphs", "divider-row", d.Glyphs.DividerRow, "Horizontal split divider."},
		{"glyphs", "divider-col", d.Glyphs.DividerCol, "Vertical split divider."},
		{"glyphs", "border-top-left", d.Glyphs.BorderTopLeft, "Top-left pane border corner."},
		{"glyphs", "border-top-right", d.Glyphs.BorderTopRight, "Top-right pane border corner."},
		{"glyphs", "border-bottom-left", d.Glyphs.BorderBottomLeft, "Bottom-left pane border corner."},
		{"glyphs", "border-bottom-right", d.Glyphs.BorderBottomRight, "Bottom-right pane border corner."},
		{"glyphs", "border-horizontal", d.Glyphs.BorderHorizontal, "Horizontal pane border run."},
		{"glyphs", "border-vertical", d.Glyphs.BorderVertical, "Vertical pane border run."},
		{"glyphs", "border-tee-left", d.Glyphs.BorderTeeLeft, "Left-facing border tee."},
		{"glyphs", "border-tee-right", d.Glyphs.BorderTeeRight, "Right-facing border tee."},
		{"glyphs", "fold-open", d.Glyphs.FoldOpen, "Open fold caret; also replaces the Nerd Font form."},
		{"glyphs", "fold-closed", d.Glyphs.FoldClosed, "Closed fold caret; also replaces the Nerd Font form."},
		{"glyphs", "active-mark", d.Glyphs.ActiveMark, "Mark beside the active window."},
		{"glyphs", "workspace-bar", d.Glyphs.WorkspaceBar, "Bar beside the active workspace."},
		{"glyphs", "dirty", d.Glyphs.Dirty, "Dirty worktree dot."},
	}
	for _, name := range stateNames {
		style := d.States[name]
		keys = append(keys,
			ThemeKey{"states." + name, "glyph", style.Glyph, fmt.Sprintf("%s state glyph; also replaces the Nerd Font form.", name)},
			ThemeKey{"states." + name, "colour", rgbHex(style.Colour), fmt.Sprintf("%s state colour.", name)},
		)
	}
	return keys
}

// rgbHex spells an RGB colour as the theme file does.
func rgbHex(c theme.RGB) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// ThemeSampleKDL is a theme file that names every theme key at its built-in
// value. Pasted as a theme it paints the built-in theme.
const ThemeSampleKDL = `palette {
    "rail-bg"       "#14131a"
    "rail-focus-bg" "#2c2a38"
    "rail-hover-bg" "#21202d"
    "rail-text"     "#e9eaf1"
    "rail-dim"      "#8f93a8"
    "rail-header"   "#a6a9bd"
    "rail-window"   "#9599ae"
    "rail-diff-add" "#78dc82"
    "rail-diff-del" "#ff7d7d"
    "rail-working"  "#f5be50"
    "bar-accent"    "#7ac0eb"
}

accents {
    "accent-1" "#bb9af7"
    "accent-2" "#6edceb"
    "accent-3" "#f08cd7"
    "accent-4" "#7ea9ff"
    "accent-5" "#f0a05a"
    "accent-6" "#8cd79b"
}

glyphs {
    "grip"                "•"
    "divider-row"         "━"
    "divider-col"         "┃"
    "border-top-left"     "┌"
    "border-top-right"    "┐"
    "border-bottom-left"  "└"
    "border-bottom-right" "┘"
    "border-horizontal"   "─"
    "border-vertical"     "│"
    "border-tee-left"     "├"
    "border-tee-right"    "┤"
    "fold-open"           "▾"
    "fold-closed"         "▸"
    "active-mark"         "▸"
    "workspace-bar"       "▌"
    "dirty"               "●"
}

states {
    "working" {
        "glyph"  "●"
        "colour" "#f5be50"
    }
    "idle" {
        "glyph"  "○"
        "colour" "#78c8e1"
    }
    "blocked" {
        "glyph"  "▲"
        "colour" "#ff6e6e"
    }
    "done" {
        "glyph"  "✓"
        "colour" "#6ee191"
    }
    "error" {
        "glyph"  "✕"
        "colour" "#ff82a0"
    }
    "unknown" {
        "glyph"  "?"
        "colour" "#8f93a8"
    }
    "shell" {
        "glyph"  "▶"
        "colour" "#8f93a8"
    }
}
`
