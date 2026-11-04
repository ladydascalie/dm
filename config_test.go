package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// configTestDefaultActions lists every action the frozen keymap header gives to
// each mode. The default keymap must bind all of them.
var configTestDefaultActions = map[string][]string{
	"main": {
		"send-prefix", "split-right", "split-down", "split-down-alt",
		"split-left", "split-up", "scrollback", "focus-left", "focus-down",
		"focus-up", "focus-right", "next-pane", "prev-pane", "close-pane",
		"new-window", "next-window", "prev-window", "new-workspace",
		"next-workspace", "switcher", "picker", "rename-workspace",
		"toggle-rail", "message-box", "detach", "detach-alt",
		"window-1", "window-2", "window-3", "window-4", "window-5",
		"window-6", "window-7", "window-8", "window-9", "window-10",
		"dismiss", "dismiss-esc", "focus-up-arrow", "focus-down-arrow",
		"focus-left-arrow", "focus-right-arrow",
	},
	"menu": {
		"down", "down-arrow", "up", "up-arrow", "pick", "close",
		"close-esc", "pick-1", "pick-2", "pick-3", "pick-4", "pick-5",
		"pick-6", "pick-7", "pick-8", "pick-9",
	},
	"picker": {
		"down", "down-arrow", "up", "up-arrow", "pick", "close",
		"close-s", "close-esc", "close-left", "close-right",
		"pick-1", "pick-2", "pick-3", "pick-4", "pick-5",
		"pick-6", "pick-7", "pick-8", "pick-9",
	},
	"switcher": {
		"close", "close-ctrl-c", "close-esc", "close-left",
		"close-right", "close-shift-tab", "pick", "clear-query",
		"backspace", "down-ctrl-n", "down", "down-arrow",
		"up-ctrl-p", "up", "up-arrow",
	},
	"scroll": {
		"up", "up-arrow", "down", "down-arrow", "page-up",
		"page-down", "top", "close", "close-q", "close-bracket",
		"close-esc",
	},
	"rename": {
		"cancel", "cancel-up", "cancel-down", "cancel-left",
		"cancel-right", "submit", "backspace", "clear",
	},
	"box": {
		"up", "up-arrow", "down", "down-arrow", "page-up",
		"page-down", "top", "bottom", "toggle-page", "close",
		"close-m", "close-prefix", "close-ctrl-c", "close-esc",
	},
}

// TestConfigDefaultsParse proves the default keymap parses and binds every
// default action in its mode.
func TestConfigDefaultsParse(t *testing.T) {
	cfg := LoadDefault()
	if cfg == nil {
		t.Fatal("LoadDefault returned nil")
	}
	if got := cfg.PrefixName(); got != "ctrl+b" {
		t.Fatalf("prefix name = %q, want ctrl+b", got)
	}
	if got := cfg.PrefixBytes(); !bytes.Equal(got, []byte{0x02}) {
		t.Fatalf("prefix bytes = %v, want [2]", got)
	}
	for mode, actions := range configTestDefaultActions {
		for _, action := range actions {
			if key := cfg.Key(mode, action); key == "" {
				t.Errorf("mode %q action %q is unbound", mode, action)
			}
		}
	}
}

// TestConfigOverride proves a sample KDL overrides the prefix and one binding
// in each mode.
func TestConfigOverride(t *testing.T) {
	src := `prefix "ctrl+a"

keymap "main" {
	"split-right" "S"
}

keymap "menu" {
	"down" "n"
}

keymap "picker" {
	"down" "n"
}

keymap "switcher" {
	"down" "n"
}

keymap "scroll" {
	"up" "u"
}

keymap "rename" {
	"cancel" "c"
}

keymap "box" {
	"up" "u"
}
`
	cfg, err := parseConfig(src)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if got := cfg.PrefixName(); got != "ctrl+a" {
		t.Errorf("prefix name = %q, want ctrl+a", got)
	}
	if got := cfg.PrefixBytes(); !bytes.Equal(got, []byte{0x01}) {
		t.Errorf("prefix bytes = %v, want [1]", got)
	}
	checks := []struct {
		mode   string
		action string
		key    string
	}{
		{"main", "split-right", "S"},
		{"menu", "down", "n"},
		{"picker", "down", "n"},
		{"switcher", "down", "n"},
		{"scroll", "up", "u"},
		{"rename", "cancel", "c"},
		{"box", "up", "u"},
	}
	for _, c := range checks {
		if got := cfg.Key(c.mode, c.action); got != c.key {
			t.Errorf("Key(%q, %q) = %q, want %q", c.mode, c.action, got, c.key)
		}
	}
	action, n, state := cfg.Match("main", []byte("S"))
	if state != Match || action != "split-right" || n != 1 {
		t.Errorf("Match(main, S) = %q, %d, %v; want split-right, 1, Match", action, n, state)
	}
}

// TestConfigUnknownNode proves an unknown top-level node fails with its line.
func TestConfigUnknownNode(t *testing.T) {
	_, err := parseConfig(`bogus "x"`)
	if err == nil {
		t.Fatal("parseConfig accepted an unknown node")
	}
	if !strings.Contains(err.Error(), "unknown node") {
		t.Fatalf("error = %q, want unknown node", err)
	}
	if !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("error = %q, want line 1", err)
	}
}

// TestConfigDuplicateAction proves a repeated action in one mode fails with its
// line.
func TestConfigDuplicateAction(t *testing.T) {
	_, err := parseConfig(`keymap "main" {
	"a" "x"
	"a" "y"
}`)
	if err == nil {
		t.Fatal("parseConfig accepted a duplicate action")
	}
	if !strings.Contains(err.Error(), "duplicate action") {
		t.Fatalf("error = %q, want duplicate action", err)
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("error = %q, want line 3", err)
	}
}

// TestConfigEmptyKey proves an empty key fails with its line.
func TestConfigEmptyKey(t *testing.T) {
	_, err := parseConfig(`keymap "main" {
	"a" ""
}`)
	if err == nil {
		t.Fatal("parseConfig accepted an empty key")
	}
	if !strings.Contains(err.Error(), "empty key") {
		t.Fatalf("error = %q, want empty key", err)
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("error = %q, want line 2", err)
	}
}

// TestConfigMatch proves the longest sequence wins, a prefix waits and an
// unknown key misses.
func TestConfigMatch(t *testing.T) {
	src := `keymap "main" {
	"esc" "esc"
	"up" "up"
}`
	cfg, err := parseConfig(src)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	action, n, state := cfg.Match("main", []byte{0x1b, '[', 'A'})
	if state != Match || action != "up" || n != 3 {
		t.Errorf("Match(ESC [ A) = %q, %d, %v; want up, 3, Match", action, n, state)
	}
	action, n, state = cfg.Match("main", []byte{0x1b})
	if state != Partial || action != "" || n != 0 {
		t.Errorf("Match(ESC) = %q, %d, %v; want empty, 0, Partial", action, n, state)
	}
	action, n, state = cfg.Match("main", []byte{'z'})
	if state != NoMatch || action != "" || n != 0 {
		t.Errorf("Match(z) = %q, %d, %v; want empty, 0, NoMatch", action, n, state)
	}
	src = `keymap "main" {
	"esc" "esc"
}`
	cfg, err = parseConfig(src)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	action, n, state = cfg.Match("main", []byte{0x1b})
	if state != Match || action != "esc" || n != 1 {
		t.Errorf("Match(ESC) = %q, %d, %v; want esc, 1, Match", action, n, state)
	}
}

// TestConfigSpecialKeys proves enter and backspace match both terminal
// encodings and ctrl+letter maps to the control byte.
func TestConfigSpecialKeys(t *testing.T) {
	src := `keymap "main" {
	"enter" "enter"
	"backspace" "backspace"
	"ctrl-a" "ctrl+a"
}`
	cfg, err := parseConfig(src)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	for _, b := range []byte{0x0d, 0x0a} {
		action, n, state := cfg.Match("main", []byte{b})
		if state != Match || action != "enter" || n != 1 {
			t.Errorf("Match(%#x) = %q, %d, %v; want enter, 1, Match", b, action, n, state)
		}
	}
	for _, b := range []byte{0x7f, 0x08} {
		action, n, state := cfg.Match("main", []byte{b})
		if state != Match || action != "backspace" || n != 1 {
			t.Errorf("Match(%#x) = %q, %d, %v; want backspace, 1, Match", b, action, n, state)
		}
	}
	action, n, state := cfg.Match("main", []byte{0x01})
	if state != Match || action != "ctrl-a" || n != 1 {
		t.Errorf("Match(0x01) = %q, %d, %v; want ctrl-a, 1, Match", action, n, state)
	}
}

// TestConfigMissingPathDefaults proves a missing explicit path uses the
// defaults and writes nothing.
func TestConfigMissingPathDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "config.kdl")
	cfg, err := LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if !reflect.DeepEqual(cfg, LoadDefault()) {
		t.Fatal("LoadPath on a missing path did not return the defaults")
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("LoadPath created %q", filepath.Dir(path))
	}
}

// TestConfigMissingDefaultWrites proves a missing default path writes nothing
// without the write flag and creates the directory and file with it. The file
// parses back to the defaults.
func TestConfigMissingDefaultWrites(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "dm", "config.kdl")

	cfg, err := LoadDefaultPath(false)
	if err != nil {
		t.Fatalf("LoadDefaultPath(false): %v", err)
	}
	if !reflect.DeepEqual(cfg, LoadDefault()) {
		t.Fatal("LoadDefaultPath(false) did not return the defaults")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("LoadDefaultPath(false) created %q", path)
	}

	cfg, err = LoadDefaultPath(true)
	if err != nil {
		t.Fatalf("LoadDefaultPath(true): %v", err)
	}
	if !reflect.DeepEqual(cfg, LoadDefault()) {
		t.Fatal("LoadDefaultPath(true) did not return the defaults")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("file mode = %v, want 0600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0700 {
		t.Errorf("dir mode = %v, want 0700", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	parsed, err := parseConfig(string(data))
	if err != nil {
		t.Fatalf("parse written file: %v", err)
	}
	if !reflect.DeepEqual(parsed, LoadDefault()) {
		t.Fatal("written file does not parse back to the defaults")
	}
}

// TestConfigLoadPathMalformed proves a malformed file returns the defaults and
// a clear error with its line.
func TestConfigLoadPathMalformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.kdl")
	if err := os.WriteFile(path, []byte(`bogus "x"`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadPath(path)
	if err == nil {
		t.Fatal("LoadPath accepted a malformed file")
	}
	if !strings.Contains(err.Error(), "unknown node") || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("error = %q, want unknown node on line 1", err)
	}
	if !reflect.DeepEqual(cfg, LoadDefault()) {
		t.Fatal("LoadPath on a malformed file did not return the defaults")
	}
}

// TestConfigHelpRows proves the default help rows match the current keymap
// table's order and labels, and that a remap changes the displayed key.
func TestConfigHelpRows(t *testing.T) {
	want := [][2]string{
		{"|", "split right"},
		{"-", "split down"},
		{"< / ^", "split left / up"},
		{"[", "scrollback"},
		{"h j k l", "focus pane"},
		{"n / p", "next/prev pane"},
		{"x", "close pane"},
		{"c", "new window"},
		{"w / W", "next/prev win"},
		{"0-9", "select window"},
		{"t", "new workspace"},
		{")", "next workspace"},
		{"s", "switch session"},
		{"r", "rename workspace"},
		{"m", "message box"},
		{"b", "cycle rail: full, thin, hidden"},
		{"d / q", "detach"},
		{"ctrl+b", "send prefix"},
	}
	if got := LoadDefault().HelpRows(); !reflect.DeepEqual(got, want) {
		t.Fatalf("HelpRows() = %#v, want %#v", got, want)
	}

	src := `prefix "ctrl+a"
keymap "main" {
	"split-right" "S"
	"send-prefix" "prefix"
}`
	cfg, err := parseConfig(src)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	rows := cfg.HelpRows()
	if len(rows) != 2 {
		t.Fatalf("remapped HelpRows() = %#v, want 2 rows", rows)
	}
	if rows[0][0] != "S" || rows[0][1] != "split right" {
		t.Errorf("row 0 = %#v, want S / split right", rows[0])
	}
	if rows[1][0] != "ctrl+a" || rows[1][1] != "send prefix" {
		t.Errorf("row 1 = %#v, want ctrl+a / send prefix", rows[1])
	}
}
