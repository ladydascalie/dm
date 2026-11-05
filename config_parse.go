package main

import "fmt"

// parseKeymaps walks the top-level tokens and fills cfg.modes. It rejects
// unknown nodes, duplicate actions in one mode, empty keys and malformed
// blocks. Multiple keymap nodes for one mode merge.
func parseKeymaps(toks []token, cfg *Config) error {
	i := 0
	for i < len(toks) {
		t := toks[i]
		if t.kind == tokEOF {
			return nil
		}
		if t.kind != tokIdent {
			return fmt.Errorf("line %d: expected node name, got %q", t.line, t.value)
		}
		switch t.value {
		case "prefix":
			if i+1 >= len(toks) || toks[i+1].kind != tokString {
				return fmt.Errorf("line %d: malformed prefix node", t.line)
			}
			i += 2
		case "theme":
			if i+1 >= len(toks) || toks[i+1].kind != tokString {
				return fmt.Errorf("line %d: expected quoted theme name after theme", t.line)
			}
			if cfg.themeName != "" {
				return fmt.Errorf("line %d: duplicate theme node", t.line)
			}
			name := toks[i+1].value
			if err := checkThemeName(name); err != nil {
				return fmt.Errorf("line %d: %w", toks[i+1].line, err)
			}
			cfg.themeName = name
			i += 2
		case "keymap":
			i++
			if i >= len(toks) || toks[i].kind != tokString {
				return fmt.Errorf("line %d: expected quoted mode name after keymap", toks[i-1].line)
			}
			modeName := toks[i].value
			if modeName == "" {
				return fmt.Errorf("line %d: empty keymap mode", toks[i].line)
			}
			i++
			if i >= len(toks) || toks[i].kind != tokLBrace {
				return fmt.Errorf("line %d: expected '{' after keymap %q", toks[i-1].line, modeName)
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
					return fmt.Errorf("line %d: unexpected end of file, expected '}'", toks[len(toks)-1].line)
				}
				if toks[i].kind == tokRBrace {
					i++
					break
				}
				if toks[i].kind != tokString {
					return fmt.Errorf("line %d: expected quoted action, got %q", toks[i].line, toks[i].value)
				}
				action := toks[i].value
				actionLine := toks[i].line
				i++
				if i >= len(toks) || toks[i].kind != tokString {
					return fmt.Errorf("line %d: expected quoted key after action %q", actionLine, action)
				}
				key := toks[i].value
				keyLine := toks[i].line
				i++
				section := ""
				if i < len(toks) && toks[i].line == keyLine && toks[i].kind != tokRBrace && toks[i].kind != tokEOF {
					tag := toks[i]
					if tag.kind != tokIdent || tag.value != "help" {
						return fmt.Errorf("line %d: expected help, got %q", tag.line, tag.value)
					}
					i++
					if i >= len(toks) || toks[i].kind != tokString {
						return fmt.Errorf("line %d: expected quoted section after help", tag.line)
					}
					section = toks[i].value
					if section == "" {
						return fmt.Errorf("line %d: empty help section", toks[i].line)
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
				seqs, err := keySequences(key, cfg.prefixBytes)
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
			return fmt.Errorf("line %d: unknown node %q", t.line, t.value)
		}
	}
	return nil
}

// parseConfig parses the frozen KDL keymap shape. It returns a clear error with
// the line when the text is malformed.
func parseConfig(src string) (*Config, error) {
	toks, err := scanKDL(src)
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
		theme:       DefaultTheme(),
	}
	if err := parseKeymaps(toks, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// defaultConfigKDL is the default keymap text. The default file writes this
// text, and LoadDefault parses it.
const defaultConfigKDL = `// dm keybinding defaults.
// Edit this file to remap keys. A missing file uses these defaults.
// Add help "Section" after a key to move its row in the help box.

prefix "ctrl+b"

keymap "main" {
    "send-prefix" "prefix"
    "reload" "ctrl+r"
    "split-right" "|"
    "split-down" "-"
    "split-down-alt" "_"
    "split-left" "<"
    "split-up" "^"
    "scrollback" "["
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
    "clear-query" "ctrl+u"
    "backspace" "backspace"
    "down-ctrl-n" "ctrl+n"
    "down" "j"
    "down-arrow" "down"
    "up-ctrl-p" "ctrl+p"
    "up" "k"
    "up-arrow" "up"
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
			seqs, err := keySequences(key, cfg.prefixBytes)
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
