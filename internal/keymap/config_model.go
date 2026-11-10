// Package keymap holds the keymap config model, the KDL loader and the help
// table.
package keymap

import (
	"bytes"
	"fmt"
	"strings"

	"dm/internal/theme"
	"dm/internal/wlabel"
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

// mode is one keymap: action to key name, key bytes to action, the help
// section tag per action, and the actions the file unbind with "none".
type mode struct {
	actions  map[string]string
	sections map[string]string
	unbound  map[string]bool
	bindings []binding
}

// Config is the loaded keymap. It carries the prefix key and one keymap per
// mode. A zero Config is not valid; use LoadDefault or a Load function.
type Config struct {
	prefixName     string
	prefixBytes    []byte
	modes          map[string]*mode
	themeName      string
	theme          theme.Theme
	defaultFold    bool
	defaultFoldSet bool
	tabMin         int
	tabMax         int
	padding        int
	paddingSet     bool
	glyphSet       theme.GlyphSet
	glyphSetSet    bool
	windowLabel    string
	windowLabelSet bool
	// dashPreview is the dashboard's starting preview share of the body, in
	// percent. Zero means the band starts hidden; dashPreviewSet separates that
	// from a config that never named the node.
	dashPreview    int
	dashPreviewSet bool
	// dashFull is the dashboard's shape: true takes the whole screen, false
	// centres the palette over the panes. dashFullSet separates the default
	// from a config that never named the node.
	dashFull    bool
	dashFullSet bool
	// diffCommand is the shell command the dashboard diff key runs in a
	// floating pane. An empty value means the config never named the node.
	diffCommand string
}

// Theme returns the resolved theme. It is never the zero Theme: a Config
// without a loaded theme reports the built-in one.
func (c *Config) Theme() theme.Theme {
	if c == nil || c.theme.States == nil {
		return theme.DefaultTheme()
	}
	return c.theme
}

// DefaultFoldSet reports whether the config carried a default-fold node.
// A false result leaves each restored window's saved fold state alone.
func (c *Config) DefaultFoldSet() bool {
	if c == nil {
		return false
	}
	return c.defaultFoldSet
}

// DefaultFold reports whether a new window's rail starts unfolded. The zero
// value means folded.
func (c *Config) DefaultFold() bool {
	if c == nil {
		return false
	}
	return c.defaultFold
}

// TabMinWidth returns the smallest tab width in cells. A config without the
// node reports the built-in default.
func (c *Config) TabMinWidth() int {
	if c == nil || c.tabMin < tabWidthFloor {
		return defaultTabMin
	}
	return c.tabMin
}

// TabMaxWidth returns the largest tab width in cells. A config without the
// node reports the built-in default.
func (c *Config) TabMaxWidth() int {
	if c == nil || c.tabMax < tabWidthFloor {
		return defaultTabMax
	}
	return c.tabMax
}

// Padding returns the blank cells between a pane's border and its content. A
// config without the node reports the built-in default.
func (c *Config) Padding() int {
	if c == nil || !c.paddingSet {
		return defaultPadding
	}
	if c.padding < 0 {
		return 0
	}
	if c.padding > maxPadding {
		return maxPadding
	}
	return c.padding
}

// DashboardPreview returns the dashboard's starting preview share of the body,
// in percent. Zero starts with the band hidden and leaves the peek key to show
// it. A config without the node reports the built-in default.
func (c *Config) DashboardPreview() int {
	if c == nil || !c.dashPreviewSet {
		return defaultDashboardPreview
	}
	if c.dashPreview < 0 {
		return 0
	}
	if c.dashPreview > 100 {
		return 100
	}
	return c.dashPreview
}

// DiffCommand returns the shell command the dashboard diff key runs in a
// floating pane. It runs through sh -c in the selected pane's folder. A config
// without the node reports the built-in default.
func (c *Config) DiffCommand() string {
	if c == nil || c.diffCommand == "" {
		return defaultDiffCommand
	}
	return c.diffCommand
}

// DashboardFullScreen reports whether the dashboard takes the whole screen. A
// false result draws it as a centred palette over the panes. A config without
// the node reports the built-in default, which is the full screen.
func (c *Config) DashboardFullScreen() bool {
	if c == nil || !c.dashFullSet {
		return true
	}
	return c.dashFull
}

// GlyphSet returns the glyph vocabulary the chrome draws with. A config
// without the node reports auto, which picks Nerd Font glyphs when the font is
// present and Unicode otherwise.
func (c *Config) GlyphSet() theme.GlyphSet {
	if c == nil || !c.glyphSetSet {
		return theme.GlyphAuto
	}
	return c.glyphSet
}

// WindowLabel returns the template the tab bar and the sidebar window row
// render a window label with. A config without the node reports the built-in
// default.
func (c *Config) WindowLabel() string {
	if c == nil || c.windowLabel == "" {
		return wlabel.Default
	}
	return c.windowLabel
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

// HelpSection returns the help section tag for action in mode. It returns ""
// when the action is unbound or carries no tag.
func (c *Config) HelpSection(modeName, action string) string {
	m, ok := c.modes[modeName]
	if !ok {
		return ""
	}
	return m.sections[action]
}

// HasMode reports whether the config carries a keymap mode. A config that
// predates a mode does not, so a caller can fall back to the built-in one.
func (c *Config) HasMode(name string) bool {
	if c == nil {
		return false
	}
	_, ok := c.modes[name]
	return ok
}

// KeymapMode is one keymap as the keymap view lists it: the mode's name and its
// bindings.
type KeymapMode struct {
	Name    string
	Actions []KeymapAction
}

// KeymapAction is one binding in a mode: the action it names and the key it
// runs on, as the config displays the key.
type KeymapAction struct {
	Action string
	Key    string
}

// keymapOrder is the order the keymap view lists modes in. A mode outside the
// list is not shown.
var keymapOrder = []string{
	"main", "menu", "picker", "switcher", "scroll", "rename", "box", "help",
}

// Keymaps returns every mode the config carries, in the fixed order the keymap
// view needs. A mode missing from the config is left out. Actions follow the
// order the mode declares them in and each action appears once: a key with two
// terminal encodings is one action.
func (c *Config) Keymaps() []KeymapMode {
	if c == nil {
		return nil
	}
	var out []KeymapMode
	for _, name := range keymapOrder {
		m, ok := c.modes[name]
		if !ok {
			continue
		}
		km := KeymapMode{Name: name}
		seen := make(map[string]bool, len(m.bindings))
		for _, b := range m.bindings {
			if seen[b.action] {
				continue
			}
			seen[b.action] = true
			km.Actions = append(km.Actions, KeymapAction{
				Action: b.action,
				Key:    c.Key(name, b.action),
			})
		}
		out = append(out, km)
	}
	return out
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
	{[]string{"copy-output", "copy-screen"}, "copy output / screen"},
	{[]string{"copy-pane-id"}, "copy pane id"},
	{[]string{"focus-left", "focus-down", "focus-up", "focus-right"}, "focus pane"},
	{[]string{"next-pane", "prev-pane"}, "next/prev pane"},
	{[]string{"close-pane"}, "close pane"},
	{[]string{"close-window"}, "close window"},
	{[]string{"zoom-pane"}, "zoom pane"},
	{[]string{"new-window"}, "new window"},
	{[]string{"next-window", "prev-window"}, "next/prev win"},
	{[]string{"window-1", "window-2", "window-3", "window-4", "window-5", "window-6", "window-7", "window-8", "window-9", "window-10"}, "select window"},
	{[]string{"new-workspace"}, "new workspace"},
	{[]string{"next-workspace"}, "next workspace"},
	{[]string{"picker"}, "switch session"},
	{[]string{"rename-workspace"}, "rename workspace"},
	{[]string{"set-project"}, "set project"},
	{[]string{"message-box"}, "message box"},
	{[]string{"toggle-rail"}, "cycle rail: full, thin, hidden"},
	{[]string{"detach", "detach-alt"}, "detach"},
	{[]string{"send-prefix"}, "send prefix"},
	{[]string{"reload"}, "reload config"},
}

// HelpActions returns the action names the help row with the given label
// groups. It returns nil when no row carries the label.
func HelpActions(label string) []string {
	for _, r := range helpRows {
		if r.label == label {
			return r.actions
		}
	}
	return nil
}

// HelpEntry is one row of the help overlay: the key label, the action text and
// the section the row belongs to. Section is empty when no bound action in the
// row carries a help tag.
type HelpEntry struct {
	Key     string
	Action  string
	Section string
}

// HelpEntries returns the help overlay's rows: key label, action text and
// section, in the current keymap table's order. A row is omitted when none of
// its actions is bound. Section comes from the first bound action in the row
// that carries a help tag.
func (c *Config) HelpEntries() []HelpEntry {
	var entries []HelpEntry
	for _, row := range helpRows {
		keys := make([]string, 0, len(row.actions))
		section := ""
		for _, action := range row.actions {
			if key := c.Key("main", action); key != "" {
				keys = append(keys, key)
				if section == "" {
					section = c.HelpSection("main", action)
				}
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
		entries = append(entries, HelpEntry{Key: label, Action: row.label, Section: section})
	}
	return entries
}

// HelpRows returns the help overlay's display rows: key label and action text,
// in the current keymap table's order. It is HelpEntries without the section.
func (c *Config) HelpRows() [][2]string {
	entries := c.HelpEntries()
	if len(entries) == 0 {
		return nil
	}
	rows := make([][2]string, len(entries))
	for i, e := range entries {
		rows[i] = [2]string{e.Key, e.Action}
	}
	return rows
}

// KeySequences turns a key name into one or more byte sequences. A name maps to
// more than one sequence when the key has two terminal encodings: enter is CR
// and LF, backspace is DEL and BS. "prefix" resolves to prefixBytes. A nil
// prefixBytes rejects "prefix", so the prefix key cannot refer to itself.
func KeySequences(name string, prefixBytes []byte) ([][]byte, error) {
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
