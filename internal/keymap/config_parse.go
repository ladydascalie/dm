package keymap

import (
	"fmt"
	"strconv"

	"dm/internal/kdl"
	"dm/internal/theme"
	"dm/internal/wlabel"
)

// parseKeymaps walks the top-level tokens and fills cfg.modes. It rejects
// unknown nodes, duplicate actions in one mode, empty keys and malformed
// blocks. Multiple keymap nodes for one mode merge.
func parseKeymaps(toks []kdl.Token, cfg *Config) error {
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
		default:
			return fmt.Errorf("line %d: unknown node %q", t.Line, t.Value)
		}
	}
	return nil
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
glyphs "auto"
window-label "{{if .Named}}{{.Name}}{{else}}{{.ID}}{{with base .Root}} {{.}}{{end}}{{end}}"
dashboard-preview 60

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
    "focus-left" "h"
    "focus-down" "j"
    "focus-up" "k"
    "focus-right" "l"
    "next-pane" "n"
    "prev-pane" "p"
    "close-pane" "x"
    "close-window" "X"
    "zoom-pane" "z"
    "new-window" "c"
    "next-window" "w"
    "prev-window" "W"
    "new-workspace" "t"
    "next-workspace" ")"
    "switcher" "a"
    "picker" "s"
    "rename-workspace" "r"
    "set-project" "P"
    "toggle-rail" "b"
    "message-box" "m"
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
    "input" "i"
    "diff" "d"
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

keymap "scroll" {
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
