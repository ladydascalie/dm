package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MatchState tells the caller what Match found in the input buffer.
type MatchState int

const (
	// NoMatch means no binding matches the buffer.
	NoMatch MatchState = iota
	// Partial means the buffer is a prefix of a longer binding. The caller
	// waits for more bytes. The action and consumed count are zero.
	Partial
	// Match means a binding matched. The action and consumed count are set.
	Match
)

// binding maps a key sequence to an action name.
type binding struct {
	bytes  []byte
	action string
}

// mode is one keymap: action to key name, and key bytes to action.
type mode struct {
	actions  map[string]string
	bindings []binding
}

// Config is the loaded keymap. It carries the prefix key and one keymap per
// mode. A zero Config is not valid; use LoadDefault or a Load function.
type Config struct {
	prefixName  string
	prefixBytes []byte
	modes       map[string]*mode
}

// PrefixBytes returns the bytes the prefix key sends.
func (c *Config) PrefixBytes() []byte {
	return c.prefixBytes
}

// PrefixName returns the display name of the prefix key.
func (c *Config) PrefixName() string {
	return c.prefixName
}

// Prefix returns the prefix bytes and display name.
func (c *Config) Prefix() ([]byte, string) {
	return c.prefixBytes, c.prefixName
}

// Match looks for a binding in mode that matches buf. It returns the action,
// the bytes consumed and the match state. A longer binding wins over a shorter
// one. When buf is a prefix of a longer binding, the state is Partial and the
// caller waits for more bytes. Partial returns no action and zero consumed.
func (c *Config) Match(modeName string, buf []byte) (string, int, MatchState) {
	m, ok := c.modes[modeName]
	if !ok {
		return "", 0, NoMatch
	}
	var best *binding
	partial := false
	for i := range m.bindings {
		b := &m.bindings[i]
		if len(b.bytes) > len(buf) && bytes.HasPrefix(b.bytes, buf) {
			partial = true
			continue
		}
		if bytes.HasPrefix(buf, b.bytes) {
			if best == nil || len(b.bytes) > len(best.bytes) {
				best = b
			}
		}
	}
	if partial {
		return "", 0, Partial
	}
	if best != nil {
		return best.action, len(best.bytes), Match
	}
	return "", 0, NoMatch
}

// Key returns the configured key name for action in mode. It returns "" when
// the action is unbound. A key name of "prefix" resolves to the prefix display
// name.
func (c *Config) Key(modeName, action string) string {
	m, ok := c.modes[modeName]
	if !ok {
		return ""
	}
	key, ok := m.actions[action]
	if !ok {
		return ""
	}
	if key == "prefix" {
		return c.prefixName
	}
	return key
}

// helpRow is one row of the help overlay. actions lists the actions the row
// groups; label is the action text.
type helpRow struct {
	actions []string
	label   string
}

// helpRows is the help overlay's table, in the current keymap var's order.
var helpRows = []helpRow{
	{[]string{"split-right"}, "split right"},
	{[]string{"split-down"}, "split down"},
	{[]string{"split-left", "split-up"}, "split left / up"},
	{[]string{"scrollback"}, "scrollback"},
	{[]string{"focus-left", "focus-down", "focus-up", "focus-right"}, "focus pane"},
	{[]string{"next-pane", "prev-pane"}, "next/prev pane"},
	{[]string{"close-pane"}, "close pane"},
	{[]string{"new-window"}, "new window"},
	{[]string{"next-window", "prev-window"}, "next/prev win"},
	{[]string{"window-1", "window-2", "window-3", "window-4", "window-5", "window-6", "window-7", "window-8", "window-9", "window-10"}, "select window"},
	{[]string{"new-workspace"}, "new workspace"},
	{[]string{"next-workspace"}, "next workspace"},
	{[]string{"picker"}, "switch session"},
	{[]string{"rename-workspace"}, "rename workspace"},
	{[]string{"message-box"}, "message box"},
	{[]string{"toggle-rail"}, "cycle rail: full, thin, hidden"},
	{[]string{"detach", "detach-alt"}, "detach"},
	{[]string{"send-prefix"}, "send prefix"},
}

// HelpRows returns the help overlay's display rows: key label and action text,
// in the current keymap table's order. A row is omitted when none of its
// actions is bound.
func (c *Config) HelpRows() [][2]string {
	var rows [][2]string
	for _, row := range helpRows {
		keys := make([]string, 0, len(row.actions))
		for _, action := range row.actions {
			if key := c.Key("main", action); key != "" {
				keys = append(keys, key)
			}
		}
		if len(keys) == 0 {
			continue
		}
		label := strings.Join(keys, " ")
		if len(row.actions) == 2 {
			label = strings.Join(keys, " / ")
		}
		if len(row.actions) == 10 && row.actions[0] == "window-1" {
			defaults := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "0"}
			same := len(keys) == len(defaults)
			if same {
				for i := range keys {
					if keys[i] != defaults[i] {
						same = false
						break
					}
				}
			}
			if same {
				label = "0-9"
			}
		}
		rows = append(rows, [2]string{label, row.label})
	}
	return rows
}

// keySequences turns a key name into one or more byte sequences. A name maps to
// more than one sequence when the key has two terminal encodings: enter is CR
// and LF, backspace is DEL and BS. "prefix" resolves to prefixBytes. A nil
// prefixBytes rejects "prefix", so the prefix key cannot refer to itself.
func keySequences(name string, prefixBytes []byte) ([][]byte, error) {
	if name == "" {
		return nil, fmt.Errorf("empty key")
	}
	switch name {
	case "esc":
		return [][]byte{{0x1b}}, nil
	case "enter":
		return [][]byte{{0x0d}, {0x0a}}, nil
	case "tab":
		return [][]byte{{0x09}}, nil
	case "space":
		return [][]byte{{0x20}}, nil
	case "backspace":
		return [][]byte{{0x7f}, {0x08}}, nil
	case "up":
		return [][]byte{{0x1b, '[', 'A'}}, nil
	case "down":
		return [][]byte{{0x1b, '[', 'B'}}, nil
	case "right":
		return [][]byte{{0x1b, '[', 'C'}}, nil
	case "left":
		return [][]byte{{0x1b, '[', 'D'}}, nil
	case "pgup":
		return [][]byte{{0x1b, '[', '5', '~'}}, nil
	case "pgdn":
		return [][]byte{{0x1b, '[', '6', '~'}}, nil
	case "shift-tab":
		return [][]byte{{0x1b, '[', 'Z'}}, nil
	case "prefix":
		if prefixBytes == nil {
			return nil, fmt.Errorf("prefix key is not defined")
		}
		return [][]byte{prefixBytes}, nil
	}
	if strings.HasPrefix(name, "ctrl+") {
		rest := name[len("ctrl+"):]
		if len(rest) != 1 {
			return nil, fmt.Errorf("ctrl+<letter> must be one letter")
		}
		c := rest[0]
		var b byte
		switch {
		case c >= 'a' && c <= 'z':
			b = c - 'a' + 1
		case c >= 'A' && c <= 'Z':
			b = c - 'A' + 1
		default:
			return nil, fmt.Errorf("ctrl+<letter> must be a letter")
		}
		return [][]byte{{b}}, nil
	}
	if len(name) == 1 {
		b := name[0]
		if b >= 0x20 && b < 0x7f {
			return [][]byte{{b}}, nil
		}
		return nil, fmt.Errorf("non-printable character %q", name)
	}
	return nil, fmt.Errorf("unknown key name %q", name)
}

// tokenKind is the kind of a KDL token.
type tokenKind int

const (
	tokIdent tokenKind = iota
	tokString
	tokLBrace
	tokRBrace
	tokEOF
)

// token is one KDL token with the line it started on.
type token struct {
	kind  tokenKind
	value string
	line  int
}

// scanKDL splits src into tokens. It accepts comments and blank lines. It is
// the minimal scanner for the frozen keymap shape.
func scanKDL(src string) ([]token, error) {
	var toks []token
	i := 0
	line := 1
	for i < len(src) {
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			start := line
			i += 2
			closed := false
			for i < len(src) {
				if src[i] == '*' && i+1 < len(src) && src[i+1] == '/' {
					i += 2
					closed = true
					break
				}
				if src[i] == '\n' {
					line++
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("line %d: unterminated block comment", start)
			}
		case c == '{':
			toks = append(toks, token{kind: tokLBrace, value: "{", line: line})
			i++
		case c == '}':
			toks = append(toks, token{kind: tokRBrace, value: "}", line: line})
			i++
		case c == '"':
			start := line
			i++
			var b strings.Builder
			closed := false
			for i < len(src) {
				ch := src[i]
				if ch == '\\' && i+1 < len(src) {
					i++
					switch src[i] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					case 'r':
						b.WriteByte('\r')
					case '"':
						b.WriteByte('"')
					case '\\':
						b.WriteByte('\\')
					default:
						b.WriteByte(src[i])
					}
					i++
					continue
				}
				if ch == '"' {
					i++
					closed = true
					break
				}
				if ch == '\n' {
					line++
				}
				b.WriteByte(ch)
				i++
			}
			if !closed {
				return nil, fmt.Errorf("line %d: unterminated string", start)
			}
			toks = append(toks, token{kind: tokString, value: b.String(), line: start})
		default:
			start := i
			for i < len(src) {
				ch := src[i]
				if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '{' || ch == '}' || ch == '"' {
					break
				}
				if ch == '/' && i+1 < len(src) && (src[i+1] == '/' || src[i+1] == '*') {
					break
				}
				i++
			}
			if i == start {
				return nil, fmt.Errorf("line %d: unexpected character %q", line, c)
			}
			toks = append(toks, token{kind: tokIdent, value: src[start:i], line: line})
		}
	}
	toks = append(toks, token{kind: tokEOF, line: line})
	return toks, nil
}

// findPrefix scans the top-level tokens for the prefix node. It returns the
// default ctrl+b when no prefix node is present. It rejects a duplicate prefix
// node and a prefix key that is not one sequence.
func findPrefix(toks []token) (string, []byte, error) {
	depth := 0
	found := false
	name := ""
	var keyBytes []byte
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch t.kind {
		case tokLBrace:
			depth++
		case tokRBrace:
			depth--
		case tokIdent:
			if depth != 0 || t.value != "prefix" {
				continue
			}
			if found {
				return "", nil, fmt.Errorf("line %d: duplicate prefix node", t.line)
			}
			found = true
			if i+1 >= len(toks) || toks[i+1].kind != tokString {
				return "", nil, fmt.Errorf("line %d: expected quoted key after prefix", t.line)
			}
			name = toks[i+1].value
			if name == "" {
				return "", nil, fmt.Errorf("line %d: empty prefix key", toks[i+1].line)
			}
			seqs, err := keySequences(name, nil)
			if err != nil {
				return "", nil, fmt.Errorf("line %d: prefix: %w", toks[i+1].line, err)
			}
			if len(seqs) != 1 {
				return "", nil, fmt.Errorf("line %d: prefix key must be one sequence", toks[i+1].line)
			}
			keyBytes = seqs[0]
			i++
		}
	}
	if !found {
		seqs, err := keySequences("ctrl+b", nil)
		if err != nil {
			return "", nil, err
		}
		return "ctrl+b", seqs[0], nil
	}
	return name, keyBytes, nil
}

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
				m = &mode{actions: make(map[string]string)}
				cfg.modes[modeName] = m
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
				if key == "" {
					return fmt.Errorf("line %d: empty key for action %q", keyLine, action)
				}
				if action == "" {
					continue
				}
				if _, exists := m.actions[action]; exists {
					return fmt.Errorf("line %d: duplicate action %q in mode %q", actionLine, action, modeName)
				}
				seqs, err := keySequences(key, cfg.prefixBytes)
				if err != nil {
					return fmt.Errorf("line %d: key %q: %w", keyLine, key, err)
				}
				m.actions[action] = key
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

prefix "ctrl+b"

keymap "main" {
    "send-prefix" "prefix"
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
    "dismiss" "?"
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
`

// LoadDefault returns the built-in default keymap without touching disk. It
// panics when the built-in text is malformed, which is a programming error.
func LoadDefault() *Config {
	cfg, err := parseConfig(defaultConfigKDL)
	if err != nil {
		panic("dm: default keymap is malformed: " + err.Error())
	}
	return cfg
}

// defaultConfigPath returns the default keymap path. It honours XDG_CONFIG_HOME
// and falls back to $HOME/.config.
func defaultConfigPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "dm", "config.kdl"), nil
	}
	home := os.Getenv("HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home directory not found: %w", err)
		}
	}
	return filepath.Join(home, ".config", "dm", "config.kdl"), nil
}

// LoadDefaultPath loads the default path. A missing file returns the built-in
// defaults. When write is true and the file is missing, it writes the default
// file first. A write or parse failure returns the defaults and the error, so
// the caller can toast it and keep running.
func LoadDefaultPath(write bool) (*Config, error) {
	path, err := defaultConfigPath()
	if err != nil {
		return LoadDefault(), err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return LoadDefault(), err
		}
		cfg := LoadDefault()
		if !write {
			return cfg, nil
		}
		if err := writeDefaultConfig(path); err != nil {
			return cfg, err
		}
		return cfg, nil
	}
	cfg, err := parseConfig(string(data))
	if err != nil {
		return LoadDefault(), err
	}
	return cfg, nil
}

// LoadPath loads an explicit path. It never writes. A missing file returns the
// built-in defaults. A parse failure returns the defaults and the error.
func LoadPath(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LoadDefault(), nil
		}
		return LoadDefault(), err
	}
	cfg, err := parseConfig(string(data))
	if err != nil {
		return LoadDefault(), err
	}
	return cfg, nil
}

// writeDefaultConfig writes the default keymap text. It creates the directory
// with 0700 and the file with 0600.
func writeDefaultConfig(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(defaultConfigKDL), 0600)
}
