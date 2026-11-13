package keymap

import (
	"fmt"
	"strconv"
	"strings"

	"dm/internal/kdl"
	"dm/internal/theme"
	"dm/internal/wlabel"
)

// parseKeymaps walks the top-level tokens and fills cfg.modes and cfg.commands.
// It rejects unknown nodes, duplicate actions in one mode, empty keys,
// malformed blocks and bindings to commands the file does not define. Multiple
// keymap nodes for one mode merge.
func parseKeymaps(toks []kdl.Token, cfg *Config) error {
	commands, err := findCommands(toks)
	if err != nil {
		return err
	}
	cfg.commands = commands
	i := 0
	for i < len(toks) {
		t := toks[i]
		if t.Kind == kdl.EOF {
			return nil
		}
		if t.Kind != kdl.Ident {
			return fmt.Errorf("line %d: expected node name, got %q", t.Line, t.Value)
		}
		switch t.Value {
		case "prefix":
			if i+1 >= len(toks) || toks[i+1].Kind != kdl.String {
				return fmt.Errorf("line %d: malformed prefix node", t.Line)
			}
			i += 2
		case "default-fold":
			if cfg.defaultFoldSet {
				return fmt.Errorf("line %d: duplicate default-fold node", t.Line)
			}
			if i+1 >= len(toks) || toks[i+1].Kind != kdl.String {
				return fmt.Errorf("line %d: expected quoted value after default-fold", t.Line)
			}
			value := toks[i+1].Value
			switch value {
			case "fold":
				cfg.defaultFold = false
			case "unfold":
				cfg.defaultFold = true
			default:
				return fmt.Errorf("line %d: default-fold must be \"fold\" or \"unfold\", got %q", toks[i+1].Line, value)
			}
			cfg.defaultFoldSet = true
			i += 2
		case "tab-min-width":
			if cfg.tabMin != 0 {
				return fmt.Errorf("line %d: duplicate tab-min-width node", t.Line)
			}
			n, next, err := parseTabWidth(toks, i, "tab-min-width")
			if err != nil {
				return err
			}
			cfg.tabMin = n
			i = next
		case "tab-max-width":
			if cfg.tabMax != 0 {
				return fmt.Errorf("line %d: duplicate tab-max-width node", t.Line)
			}
			n, next, err := parseTabWidth(toks, i, "tab-max-width")
			if err != nil {
				return err
			}
			cfg.tabMax = n
			i = next
		case "padding":
			if cfg.paddingSet {
				return fmt.Errorf("line %d: duplicate padding node", t.Line)
			}
			if i+1 >= len(toks) || (toks[i+1].Kind != kdl.Ident && toks[i+1].Kind != kdl.String) {
				return fmt.Errorf("line %d: expected a number after padding", t.Line)
			}
			v := toks[i+1]
			n, err := strconv.Atoi(v.Value)
			if err != nil {
				return fmt.Errorf("line %d: padding must be a number, got %q", v.Line, v.Value)
			}
			if n < 0 || n > maxPadding {
				return fmt.Errorf("line %d: padding must be 0 to %d, got %d", v.Line, maxPadding, n)
			}
			cfg.padding = n
			cfg.paddingSet = true
			i += 2
		case "history-lines":
			if cfg.historyLines != 0 {
				return fmt.Errorf("line %d: duplicate history-lines node", t.Line)
			}
			if i+1 >= len(toks) || (toks[i+1].Kind != kdl.Ident && toks[i+1].Kind != kdl.String) {
				return fmt.Errorf("line %d: expected a number after history-lines", t.Line)
			}
			v := toks[i+1]
			n, err := strconv.Atoi(v.Value)
			if err != nil {
				return fmt.Errorf("line %d: history-lines must be a number, got %q", v.Line, v.Value)
			}
			if n < minHistoryLines || n > maxHistoryLines {
				return fmt.Errorf("line %d: history-lines must be %d to %d, got %d", v.Line, minHistoryLines, maxHistoryLines, n)
			}
			cfg.historyLines = n
			i += 2
		case "glyphs":
			if cfg.glyphSetSet {
				return fmt.Errorf("line %d: duplicate glyphs node", t.Line)
			}
			if i+1 >= len(toks) || toks[i+1].Kind != kdl.String {
				return fmt.Errorf("line %d: expected a quoted set after glyphs", t.Line)
			}
			v := toks[i+1]
			switch v.Value {
			case "auto":
				cfg.glyphSet = theme.GlyphAuto
			case "ascii":
				cfg.glyphSet = theme.GlyphASCII
			case "unicode":
				cfg.glyphSet = theme.GlyphUnicode
			case "nerd":
				cfg.glyphSet = theme.GlyphNerd
			default:
				return fmt.Errorf("line %d: glyphs must be \"auto\", \"ascii\", \"unicode\" or \"nerd\", got %q", v.Line, v.Value)
			}
			cfg.glyphSetSet = true
			i += 2
		case "window-label":
			if cfg.windowLabelSet {
				return fmt.Errorf("line %d: duplicate window-label node", t.Line)
			}
			if i+1 >= len(toks) || toks[i+1].Kind != kdl.String {
				return fmt.Errorf("line %d: expected a quoted template after window-label", t.Line)
			}
			if err := wlabel.Check(toks[i+1].Value); err != nil {
				return fmt.Errorf("line %d: %w", toks[i+1].Line, err)
			}
			cfg.windowLabel = toks[i+1].Value
			cfg.windowLabelSet = true
			i += 2
		case "dashboard-preview":
			if cfg.dashPreviewSet {
				return fmt.Errorf("line %d: duplicate dashboard-preview node", t.Line)
			}
			if i+1 >= len(toks) || (toks[i+1].Kind != kdl.Ident && toks[i+1].Kind != kdl.String) {
				return fmt.Errorf("line %d: expected a number after dashboard-preview", t.Line)
			}
			v := toks[i+1]
			n, err := strconv.Atoi(v.Value)
			if err != nil {
				return fmt.Errorf("line %d: dashboard-preview must be a number, got %q", v.Line, v.Value)
			}
			if n < 0 || n > 100 {
				return fmt.Errorf("line %d: dashboard-preview must be 0 to 100, got %d", v.Line, n)
			}
			cfg.dashPreview = n
			cfg.dashPreviewSet = true
			i += 2
		case "dashboard-layout":
			if cfg.dashFullSet {
				return fmt.Errorf("line %d: duplicate dashboard-layout node", t.Line)
			}
			if i+1 >= len(toks) || toks[i+1].Kind != kdl.String {
				return fmt.Errorf("line %d: expected a quoted layout after dashboard-layout", t.Line)
			}
			v := toks[i+1]
			switch v.Value {
			case "full":
				cfg.dashFull = true
			case "float":
				cfg.dashFull = false
			default:
				return fmt.Errorf("line %d: dashboard-layout must be \"full\" or \"float\", got %q", v.Line, v.Value)
			}
			cfg.dashFullSet = true
			i += 2
		case "float-shadow":
			if cfg.floatShadowSet {
				return fmt.Errorf("line %d: duplicate float-shadow node", t.Line)
			}
			if i+1 >= len(toks) || toks[i+1].Kind != kdl.String {
				return fmt.Errorf("line %d: expected a quoted switch after float-shadow", t.Line)
			}
			v := toks[i+1]
			switch v.Value {
			case "off":
				cfg.floatShadow = false
			case "on":
				cfg.floatShadow = true
			default:
				return fmt.Errorf("line %d: float-shadow must be \"off\" or \"on\", got %q", v.Line, v.Value)
			}
			cfg.floatShadowSet = true
			i += 2
		case "diff-command":
			if cfg.diffCommand != "" {
				return fmt.Errorf("line %d: duplicate diff-command node", t.Line)
			}
			if i+1 >= len(toks) || toks[i+1].Kind != kdl.String {
				return fmt.Errorf("line %d: expected a quoted command after diff-command", t.Line)
			}
			v := toks[i+1]
			if strings.TrimSpace(v.Value) == "" {
				return fmt.Errorf("line %d: diff-command must not be empty", v.Line)
			}
			cfg.diffCommand = v.Value
			i += 2
		case "theme":
			if i+1 >= len(toks) || toks[i+1].Kind != kdl.String {
				return fmt.Errorf("line %d: expected quoted theme name after theme", t.Line)
			}
			if cfg.themeName != "" {
				return fmt.Errorf("line %d: duplicate theme node", t.Line)
			}
			name := toks[i+1].Value
			if err := theme.CheckThemeName(name); err != nil {
				return fmt.Errorf("line %d: %w", toks[i+1].Line, err)
			}
			cfg.themeName = name
			i += 2
		case "keymap":
			i++
			if i >= len(toks) || toks[i].Kind != kdl.String {
				return fmt.Errorf("line %d: expected quoted mode name after keymap", toks[i-1].Line)
			}
			modeName := toks[i].Value
			if modeName == "" {
				return fmt.Errorf("line %d: empty keymap mode", toks[i].Line)
			}
			i++
			if i >= len(toks) || toks[i].Kind != kdl.LBrace {
				return fmt.Errorf("line %d: expected '{' after keymap %q", toks[i-1].Line, modeName)
			}
			i++
			m, ok := cfg.modes[modeName]
			if !ok {
				m = &mode{
					actions:  make(map[string]string),
					sections: make(map[string]string),
					unbound:  make(map[string]bool),
				}
				cfg.modes[modeName] = m
			}
			if m.sections == nil {
				m.sections = make(map[string]string)
			}
			if m.unbound == nil {
				m.unbound = make(map[string]bool)
			}
			for {
				if i >= len(toks) {
					return fmt.Errorf("line %d: unexpected end of file, expected '}'", toks[len(toks)-1].Line)
				}
				if toks[i].Kind == kdl.RBrace {
					i++
					break
				}
				if toks[i].Kind != kdl.String {
					return fmt.Errorf("line %d: expected quoted action, got %q", toks[i].Line, toks[i].Value)
				}
				action := toks[i].Value
				actionLine := toks[i].Line
				i++
				if i >= len(toks) || toks[i].Kind != kdl.String {
					return fmt.Errorf("line %d: expected quoted key after action %q", actionLine, action)
				}
				key := toks[i].Value
				keyLine := toks[i].Line
				i++
				section := ""
				if i < len(toks) && toks[i].Line == keyLine && toks[i].Kind != kdl.RBrace && toks[i].Kind != kdl.EOF {
					tag := toks[i]
					if tag.Kind != kdl.Ident || tag.Value != "help" {
						return fmt.Errorf("line %d: expected help, got %q", tag.Line, tag.Value)
					}
					i++
					if i >= len(toks) || toks[i].Kind != kdl.String {
						return fmt.Errorf("line %d: expected quoted section after help", tag.Line)
					}
					section = toks[i].Value
					if section == "" {
						return fmt.Errorf("line %d: empty help section", toks[i].Line)
					}
					i++
				}
				if key == "" {
					return fmt.Errorf("line %d: empty key for action %q", keyLine, action)
				}
				if action == "" {
					continue
				}
				if _, exists := m.actions[action]; exists || m.unbound[action] {
					return fmt.Errorf("line %d: duplicate action %q in mode %q", actionLine, action, modeName)
				}
				if strings.HasPrefix(action, "command:") {
					name := action[len("command:"):]
					if _, ok := cfg.commands[name]; !ok {
						return fmt.Errorf("line %d: action %q names no command in this file", actionLine, action)
					}
				}
				if key == "none" {
					m.unbound[action] = true
					continue
				}
				seqs, err := KeySequences(key, cfg.prefixBytes)
				if err != nil {
					return fmt.Errorf("line %d: key %q: %w", keyLine, key, err)
				}
				m.actions[action] = key
				if section != "" {
					m.sections[action] = section
				}
				for _, seq := range seqs {
					m.bindings = append(m.bindings, binding{bytes: seq, action: action})
				}
			}
		case "command":
			// findCommands parsed and validated this block; consume its
			// tokens so the body is not read as nodes.
			if i+1 < len(toks) && toks[i+1].Kind == kdl.String {
				i += 2
			} else {
				i++
			}
			if i < len(toks) && toks[i].Kind == kdl.LBrace {
				i++
				depth := 1
				for i < len(toks) && depth > 0 {
					switch toks[i].Kind {
					case kdl.LBrace:
						depth++
					case kdl.RBrace:
						depth--
					}
					i++
				}
			}
		default:
			return fmt.Errorf("line %d: unknown node %q", t.Line, t.Value)
		}
	}
	return nil
}

// commandNameMax is the longest command name the config accepts, in bytes.
const commandNameMax = 32

// checkCommandName holds a command name to letters, digits, _ and -, 1 to 32
// bytes.
func checkCommandName(name string) error {
	if name == "" {
		return fmt.Errorf("command name must not be empty")
	}
	if len(name) > commandNameMax {
		return fmt.Errorf("command name %q is longer than %d bytes", name, commandNameMax)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return fmt.Errorf("command name %q must match [A-Za-z0-9_-]+", name)
		}
	}
	return nil
}

// builtinActionNames returns the action names the built-in keymap binds,
// read from the built-in text. That text defines no commands, so this never
// recurses into itself. A command sharing one of these names would blur the
// line between a built-in action and a user command, so the config rejects it.
func builtinActionNames() map[string]bool {
	names := make(map[string]bool)
	for _, m := range LoadDefault().Keymaps() {
		for _, a := range m.Actions {
			names[a.Action] = true
		}
	}
	return names
}

// findCommands walks the top-level tokens for command blocks and returns the
// commands they define. It rejects a malformed block, a duplicate command
// name, a duplicate, missing or empty run, an unknown key inside the block,
// and a name that collides with a built-in action.
func findCommands(toks []kdl.Token) (map[string]string, error) {
	commands := make(map[string]string)
	var builtin map[string]bool
	depth := 0
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.Kind == kdl.LBrace {
			depth++
			continue
		}
		if t.Kind == kdl.RBrace {
			depth--
			continue
		}
		if t.Kind != kdl.Ident || depth != 0 || t.Value != "command" {
			continue
		}
		cmd, next, err := parseCommandBlock(toks, i)
		if err != nil {
			return nil, err
		}
		if builtin == nil {
			builtin = builtinActionNames()
		}
		if _, dup := commands[cmd.Name]; dup {
			return nil, fmt.Errorf("line %d: duplicate command %q", t.Line, cmd.Name)
		}
		if builtin[cmd.Name] {
			return nil, fmt.Errorf("line %d: command name %q collides with a built-in action", t.Line, cmd.Name)
		}
		commands[cmd.Name] = cmd.Run
		i = next - 1
	}
	return commands, nil
}

// parseCommandBlock reads one top-level command block: command <quoted name>
// { run <quoted text> }. toks[i] is the command ident. It returns the command
// and the index past the closing brace.
func parseCommandBlock(toks []kdl.Token, i int) (Command, int, error) {
	line := toks[i].Line
	if i+1 >= len(toks) || toks[i+1].Kind != kdl.String {
		return Command{}, i, fmt.Errorf("line %d: expected a quoted name after command", line)
	}
	name := toks[i+1].Value
	nameLine := toks[i+1].Line
	if err := checkCommandName(name); err != nil {
		return Command{}, i, fmt.Errorf("line %d: %w", nameLine, err)
	}
	i += 2
	if i >= len(toks) || toks[i].Kind != kdl.LBrace {
		return Command{}, i, fmt.Errorf("line %d: expected '{' after command %q", line, name)
	}
	i++
	run := ""
	runSet := false
	for {
		if i >= len(toks) {
			return Command{}, i, fmt.Errorf("line %d: command %q needs a run key", nameLine, name)
		}
		if toks[i].Kind == kdl.RBrace {
			i++
			break
		}
		if toks[i].Kind != kdl.Ident || toks[i].Value != "run" {
			return Command{}, i, fmt.Errorf("line %d: unknown key %q in command %q", toks[i].Line, toks[i].Value, name)
		}
		if runSet {
			return Command{}, i, fmt.Errorf("line %d: duplicate run in command %q", toks[i].Line, name)
		}
		i++
		if i >= len(toks) || toks[i].Kind != kdl.String {
			return Command{}, i, fmt.Errorf("line %d: expected a quoted command after run", toks[i-1].Line)
		}
		if toks[i].Value == "" {
			return Command{}, i, fmt.Errorf("line %d: run in command %q must not be empty", toks[i].Line, name)
		}
		run = toks[i].Value
		runSet = true
		i++
	}
	if !runSet {
		return Command{}, i, fmt.Errorf("line %d: command %q needs a run key", nameLine, name)
	}
	return Command{Name: name, Run: run}, i, nil
}

// findPrefix scans the top-level tokens for the prefix node. It returns the
// default ctrl+b when no prefix node is present. It rejects a duplicate prefix
// node and a prefix key that is not one sequence.
func findPrefix(toks []kdl.Token) (string, []byte, error) {
	depth := 0
	found := false
	name := ""
	var keyBytes []byte
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch t.Kind {
		case kdl.LBrace:
			depth++
		case kdl.RBrace:
			depth--
		case kdl.Ident:
			if depth != 0 || t.Value != "prefix" {
				continue
			}
			if found {
				return "", nil, fmt.Errorf("line %d: duplicate prefix node", t.Line)
			}
			found = true
			if i+1 >= len(toks) || toks[i+1].Kind != kdl.String {
				return "", nil, fmt.Errorf("line %d: expected quoted key after prefix", t.Line)
			}
			name = toks[i+1].Value
			if name == "" {
				return "", nil, fmt.Errorf("line %d: empty prefix key", toks[i+1].Line)
			}
			seqs, err := KeySequences(name, nil)
			if err != nil {
				return "", nil, fmt.Errorf("line %d: prefix: %w", toks[i+1].Line, err)
			}
			if len(seqs) != 1 {
				return "", nil, fmt.Errorf("line %d: prefix key must be one sequence", toks[i+1].Line)
			}
			keyBytes = seqs[0]
			i++
		}
	}
	if !found {
		seqs, err := KeySequences("ctrl+b", nil)
		if err != nil {
			return "", nil, err
		}
		return "ctrl+b", seqs[0], nil
	}
	return name, keyBytes, nil
}

// tabWidthFloor is the smallest tab width the config accepts: one padding
// cell, one name cell and one padding cell.
const tabWidthFloor = 3

// defaultTabMin and defaultTabMax are the built-in tab width bounds in cells.
const (
	defaultTabMin = 8
	defaultTabMax = 20
)

// defaultDashboardPreview is the built-in share of the dashboard body the
// preview band takes when it opens.
const defaultDashboardPreview = 60

// defaultDiffCommand is the built-in command the dashboard diff key runs in a
// floating pane.
const defaultDiffCommand = "git diff"

// defaultPadding is the built-in blank cells between a pane's border and its
// content, and maxPadding is the largest the config accepts. A pane too small
// for the padding drops it rather than going empty. The default is 0: the
// border already separates a pane from its neighbours, and a blank ring costs
// two columns and two rows on every pane. A padding change resizes every pane
// on reload, so the reflow follows.
const (
	defaultPadding = 0
	maxPadding     = 4
)

// The history window bounds in lines, the bytes a line is budgeted, and the
// pane ring bounds. historyLineBytes is the average a terminal row costs, so
// the default 2000 lines is 500 KiB and historyFloorBytes keeps every pane at
// today's 512 KiB ring. The 32768 line maximum is the 8 MiB cap exactly.
const (
	defaultHistoryLines = 2000
	minHistoryLines     = 100
	maxHistoryLines     = 32768
	historyLineBytes    = 256
	historyFloorBytes   = 512 << 10
	historyMinBytes     = 64 << 10
	historyMaxBytes     = 8 << 20
)

// parseTabWidth reads the integer value of a tab width node. It accepts a bare
// number or a quoted one and rejects a value below the floor.
func parseTabWidth(toks []kdl.Token, i int, name string) (int, int, error) {
	if i+1 >= len(toks) || (toks[i+1].Kind != kdl.Ident && toks[i+1].Kind != kdl.String) {
		return 0, i, fmt.Errorf("line %d: expected a number after %s", toks[i].Line, name)
	}
	v := toks[i+1]
	n, err := strconv.Atoi(v.Value)
	if err != nil {
		return 0, i, fmt.Errorf("line %d: %s must be a number, got %q", v.Line, name, v.Value)
	}
	if n < tabWidthFloor {
		return 0, i, fmt.Errorf("line %d: %s must be at least %d, got %d", v.Line, name, tabWidthFloor, n)
	}
	return n, i + 2, nil
}

// parseConfig parses the frozen KDL keymap shape. It returns a clear error with
// the line when the text is malformed.
func parseConfig(src string) (*Config, error) {
	toks, err := kdl.Scan(src)
	if err != nil {
		return nil, err
	}
	prefixName, prefixBytes, err := findPrefix(toks)
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		prefixName:  prefixName,
		prefixBytes: prefixBytes,
		modes:       make(map[string]*mode),
		theme:       theme.DefaultTheme(),
	}
	if err := parseKeymaps(toks, cfg); err != nil {
		return nil, err
	}
	if cfg.tabMin != 0 && cfg.tabMax != 0 && cfg.tabMax < cfg.tabMin {
		return nil, fmt.Errorf("tab-max-width %d is below tab-min-width %d", cfg.tabMax, cfg.tabMin)
	}
	return cfg, nil
}

// ParseConfig parses the frozen KDL keymap shape. It returns a clear error with
// the line when the text is malformed.
func ParseConfig(src string) (*Config, error) {
	return parseConfig(src)
}

// defaultConfigKDL is the default keymap text. The default file writes this
// text, and LoadDefault parses it.
const defaultConfigKDL = `// dm keybinding defaults.
// Edit this file to remap keys. A missing file uses these defaults.
// Add help "Section" after a key to move its row in the help box.

prefix "ctrl+b"
default-fold "fold"
tab-min-width 8
tab-max-width 20
padding 0
// Set the scrollback window in lines; the pane ring budget is about 256 bytes per line, capped at 8 MiB.
history-lines 2000
glyphs "auto"
window-label "{{if .Named}}{{.Name}}{{else}}{{.ID}}{{with base .Root}} {{.}}{{end}}{{end}}"
dashboard-preview 60
// The diff key runs this in a floating pane through sh -c, in the selected pane folder.
diff-command "git diff"
// Set the drop shadow the floats paint beside and under their box.
float-shadow "off"
dashboard-layout "full"
// A command: define it here, then bind "command:<name>" to a key in a keymap.
// The run text goes through sh -c in the window's folder.
// command "tests" {
//     run "go test ./..."
// }

keymap "main" {
    "send-prefix" "prefix"
    "reload" "ctrl+r"
    "split-right" "|"
    "split-down" "-"
    "split-down-alt" "_"
    "split-left" "<"
    "split-up" "^"
    "scrollback" "["
    "copy-output" "y"
    "copy-screen" "Y"
    "copy-pane-id" "i"
    "focus-left" "h"
    "focus-down" "j"
    "focus-up" "K"
    "focus-right" "l"
    "next-pane" "n"
    "prev-pane" "p"
    "close-pane" "x"
    "close-window" "X"
    "zoom-pane" "z"
    "new-window" "c"
    "next-window" "w"
    "prev-window" "W"
    "swap-next" "o" help "Layout"
    "swap-prev" "O" help "Layout"
    "next-layout" "u" help "Layout"
    "layout-picker" "L" help "Layout"
    "new-workspace" "t"
    "next-workspace" ")"
    "synchronize-panes" "S" help "Session"
    "switcher" "a"
    "picker" "s"
    "command-palette" "k"
    "rename-workspace" "r"
    "set-project" "P"
    "toggle-rail" "b"
    "message-box" "m"
    "notification-centre" "N"
    "detach" "d"
    "detach-alt" "q"
    "window-1" "1"
    "window-2" "2"
    "window-3" "3"
    "window-4" "4"
    "window-5" "5"
    "window-6" "6"
    "window-7" "7"
    "window-8" "8"
    "window-9" "9"
    "window-10" "0"
    "help" "?"
    "dismiss-esc" "esc"
    "focus-up-arrow" "up"
    "focus-down-arrow" "down"
    "focus-left-arrow" "left"
    "focus-right-arrow" "right"
}

keymap "menu" {
    "down" "j"
    "down-arrow" "down"
    "up" "k"
    "up-arrow" "up"
    "pick" "enter"
    "close" "q"
    "close-esc" "esc"
    "pick-1" "1"
    "pick-2" "2"
    "pick-3" "3"
    "pick-4" "4"
    "pick-5" "5"
    "pick-6" "6"
    "pick-7" "7"
    "pick-8" "8"
    "pick-9" "9"
}

keymap "picker" {
    "down" "j"
    "down-arrow" "down"
    "up" "k"
    "up-arrow" "up"
    "pick" "enter"
    "close" "q"
    "close-s" "s"
    "close-esc" "esc"
    "close-left" "left"
    "close-right" "right"
    "pick-1" "1"
    "pick-2" "2"
    "pick-3" "3"
    "pick-4" "4"
    "pick-5" "5"
    "pick-6" "6"
    "pick-7" "7"
    "pick-8" "8"
    "pick-9" "9"
}

keymap "switcher" {
    "close" "ctrl+b"
    "close-ctrl-c" "ctrl+c"
    "close-esc" "esc"
    "pick" "enter"
    "filter" "/"
    "clear-query" "ctrl+l"
    "backspace" "backspace"
    "sort" "s"
    "preview-up" "ctrl+u"
    "preview-down" "ctrl+d"
    "preview-bigger" "+"
    "preview-smaller" "-"
    "peek" "p"
    "copy-pane-id" "i"
    "diff" "d"
    "diff-split" "D"
    "down-ctrl-n" "ctrl+n"
    "down" "j"
    "down-arrow" "down"
    "up-ctrl-p" "ctrl+p"
    "up" "k"
    "up-arrow" "up"
    "pick-1" "1"
    "pick-2" "2"
    "pick-3" "3"
    "pick-4" "4"
    "pick-5" "5"
    "pick-6" "6"
    "pick-7" "7"
    "pick-8" "8"
    "pick-9" "9"
}

keymap "layout" {
    "down" "j"
    "up" "k"
    "next-arrow" "down"
    "prev-arrow" "up"
    "pick" "enter"
    "close" "q"
    "close-esc" "esc"
    "close-prefix" "prefix"
}

keymap "scroll" {
    "search" "/"
    "next-match" "n"
    "prev-match" "N"
    "up" "k"
    "up-arrow" "up"
    "down" "j"
    "down-arrow" "down"
    "page-up" "pgup"
    "page-down" "pgdn"
    "top" "g"
    "close" "G"
    "close-q" "q"
    "close-bracket" "["
    "close-esc" "esc"
}

keymap "rename" {
    "cancel" "esc"
    "cancel-up" "up"
    "cancel-down" "down"
    "cancel-left" "left"
    "cancel-right" "right"
    "submit" "enter"
    "backspace" "backspace"
    "clear" "ctrl+u"
}

keymap "box" {
    "up" "k"
    "up-arrow" "up"
    "down" "j"
    "down-arrow" "down"
    "page-up" "pgup"
    "page-down" "pgdn"
    "top" "g"
    "bottom" "G"
    "toggle-page" "w"
    "close" "q"
    "close-m" "m"
    "close-prefix" "ctrl+b"
    "close-ctrl-c" "ctrl+c"
    "close-esc" "esc"
}

keymap "help" {
    "search" "/"
    "next-match" "n"
    "prev-match" "N"
    "up" "k"
    "up-arrow" "up"
    "down" "j"
    "down-arrow" "down"
    "page-up" "pgup"
    "page-down" "pgdn"
    "top" "g"
    "bottom" "G"
    "close" "q"
    "close-esc" "esc"
    "close-question" "?"
}
`

// mergeDefaults fills cfg with every default binding cfg does not name. An
// action the file binds keeps its key, including a remapped prefix. An action
// the file lists as "none" stays unbound. A mode the file omits is created.
// mergeDefaults reads the built-in keymap, so it runs once, after a successful
// file parse.
func mergeDefaults(cfg *Config) {
	if cfg == nil {
		return
	}
	if cfg.modes == nil {
		cfg.modes = make(map[string]*mode)
	}
	defaults := LoadDefault()
	for modeName, defMode := range defaults.modes {
		m, ok := cfg.modes[modeName]
		if !ok {
			m = &mode{
				actions:  make(map[string]string),
				sections: make(map[string]string),
				unbound:  make(map[string]bool),
			}
			cfg.modes[modeName] = m
		}
		if m.actions == nil {
			m.actions = make(map[string]string)
		}
		if m.sections == nil {
			m.sections = make(map[string]string)
		}
		if m.unbound == nil {
			m.unbound = make(map[string]bool)
		}
		seen := make(map[string]bool, len(defMode.bindings))
		for _, b := range defMode.bindings {
			if seen[b.action] {
				continue
			}
			seen[b.action] = true
			if _, exists := m.actions[b.action]; exists {
				continue
			}
			if m.unbound[b.action] {
				continue
			}
			key := defMode.actions[b.action]
			seqs, err := KeySequences(key, cfg.prefixBytes)
			if err != nil {
				continue
			}
			m.actions[b.action] = key
			if section, ok := defMode.sections[b.action]; ok {
				m.sections[b.action] = section
			}
			for _, seq := range seqs {
				m.bindings = append(m.bindings, binding{bytes: seq, action: b.action})
			}
		}
	}
}
