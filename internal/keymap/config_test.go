package keymap

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"dm/internal/theme"
	"dm/internal/wlabel"
)

// configTestDefaultActions lists every action the frozen keymap header gives to
// each mode. The default keymap must bind all of them.
var configTestDefaultActions = map[string][]string{
	"main": {
		"send-prefix", "reload", "split-right", "split-down", "split-down-alt",
		"split-left", "split-up", "scrollback", "copy-output", "copy-screen",
		"focus-left", "focus-down",
		"focus-up", "focus-right", "next-pane", "prev-pane", "close-pane",
		"zoom-pane", "new-window", "next-window", "prev-window", "new-workspace",
		"next-workspace", "switcher", "picker", "rename-workspace", "set-project",
		"toggle-rail", "message-box", "detach", "detach-alt",
		"window-1", "window-2", "window-3", "window-4", "window-5",
		"window-6", "window-7", "window-8", "window-9", "window-10",
		"help", "dismiss-esc", "focus-up-arrow", "focus-down-arrow",
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
		"close", "close-ctrl-c", "close-esc", "pick", "filter", "clear-query",
		"backspace", "sort", "preview-up", "preview-down",
		"preview-bigger", "preview-smaller", "peek", "input", "diff",
		"down-ctrl-n", "down", "down-arrow",
		"up-ctrl-p", "up", "up-arrow",
		"pick-1", "pick-2", "pick-3", "pick-4", "pick-5",
		"pick-6", "pick-7", "pick-8", "pick-9",
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
	"help": {
		"up", "up-arrow", "down", "down-arrow", "page-up",
		"page-down", "top", "bottom", "close", "close-esc",
		"close-question", "search", "next-match", "prev-match",
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
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode = %v, want 0600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
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
	if err := os.WriteFile(path, []byte(`bogus "x"`), 0o600); err != nil {
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
		{"y / Y", "copy output / screen"},
		{"h j k l", "focus pane"},
		{"n / p", "next/prev pane"},
		{"x", "close pane"},
		{"X", "close window"},
		{"z", "zoom pane"},
		{"c", "new window"},
		{"w / W", "next/prev win"},
		{"0-9", "select window"},
		{"t", "new workspace"},
		{")", "next workspace"},
		{"s", "switch session"},
		{"r", "rename workspace"},
		{"P", "set project"},
		{"m", "message box"},
		{"b", "cycle rail: full, thin, hidden"},
		{"d / q", "detach"},
		{"ctrl+b", "send prefix"},
		{"ctrl+r", "reload config"},
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

// TestConfigCloseWindowBinding proves the default main keymap binds close-window
// to X and the help box carries its row right after close pane.
func TestConfigCloseWindowBinding(t *testing.T) {
	cfg := LoadDefault()
	if got := cfg.Key("main", "close-window"); got != "X" {
		t.Fatalf("Key(main, close-window) = %q, want X", got)
	}
	rows := cfg.HelpRows()
	for i, r := range rows {
		if r[1] != "close window" {
			continue
		}
		if r[0] != "X" {
			t.Fatalf("close window row key = %q, want X", r[0])
		}
		if i == 0 || rows[i-1][1] != "close pane" {
			t.Fatalf("close window row is not after close pane: %#v", rows)
		}
		return
	}
	t.Fatalf("no close window help row: %#v", rows)
}

// writeConfigFile writes src to dir/config.kdl and returns the path.
func writeConfigFile(t *testing.T, dir, src string) string {
	t.Helper()
	path := filepath.Join(dir, "config.kdl")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestConfigMergeAddsMissingDefaults proves a file that names one action
// inherits every default action it omits, including one from a mode it does not
// create.
func TestConfigMergeAddsMissingDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadPath(writeConfigFile(t, dir, `keymap "main" {
	"close-pane" "x"
}
`))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if got := cfg.Key("main", "close-pane"); got != "x" {
		t.Fatalf("Key(main, close-pane) = %q, want x", got)
	}
	if got := cfg.Key("main", "close-window"); got != "X" {
		t.Fatalf("Key(main, close-window) = %q, want X from the defaults", got)
	}
	action, n, state := cfg.Match("main", []byte("X"))
	if state != Match || action != "close-window" || n != 1 {
		t.Fatalf("Match(main, X) = %q, %d, %v; want close-window, 1, Match", action, n, state)
	}
	if got := cfg.Key("main", "split-right"); got != "|" {
		t.Fatalf("Key(main, split-right) = %q, want | from the defaults", got)
	}
	if got := cfg.Key("help", "close"); got != "q" {
		t.Fatalf("Key(help, close) = %q, want q in the created mode", got)
	}
}

// TestConfigMergeKeepsRemappedKey proves a file binding wins over the default
// key for the same action and the default key no longer matches.
func TestConfigMergeKeepsRemappedKey(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadPath(writeConfigFile(t, dir, `keymap "main" {
	"close-window" "Q"
}
`))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if got := cfg.Key("main", "close-window"); got != "Q" {
		t.Fatalf("Key(main, close-window) = %q, want Q", got)
	}
	if got := cfg.Key("main", "close-pane"); got != "x" {
		t.Fatalf("Key(main, close-pane) = %q, want x from the defaults", got)
	}
	action, n, state := cfg.Match("main", []byte("Q"))
	if state != Match || action != "close-window" || n != 1 {
		t.Fatalf("Match(main, Q) = %q, %d, %v; want close-window, 1, Match", action, n, state)
	}
	action, n, state = cfg.Match("main", []byte("X"))
	if state != NoMatch || action != "" || n != 0 {
		t.Fatalf("Match(main, X) = %q, %d, %v; want empty, 0, NoMatch", action, n, state)
	}
}

// TestConfigNoneUnbinds proves a "none" key records the action as unbound: no
// binding, no help row, and the merge does not restore the default.
func TestConfigNoneUnbinds(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadPath(writeConfigFile(t, dir, `keymap "main" {
	"close-pane" "x"
	"close-window" "none"
}
`))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if got := cfg.Key("main", "close-window"); got != "" {
		t.Fatalf("Key(main, close-window) = %q, want empty", got)
	}
	if action, n, state := cfg.Match("main", []byte("X")); state != NoMatch || action != "" || n != 0 {
		t.Fatalf("Match(main, X) = %q, %d, %v; want empty, 0, NoMatch", action, n, state)
	}
	rows := cfg.HelpEntries()
	sawClosePane := false
	for _, row := range rows {
		if row.Action == "close window" {
			t.Fatalf("HelpEntries() kept the close window row: %#v", rows)
		}
		if row.Action == "close pane" {
			sawClosePane = true
		}
	}
	if !sawClosePane {
		t.Fatalf("HelpEntries() dropped the close pane row: %#v", rows)
	}
}

// TestConfigNoneCountsAsDuplicate proves "none" still takes part in the
// duplicate-action check in both directions.
func TestConfigNoneCountsAsDuplicate(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "none then key",
			src:  "keymap \"main\" {\n\t\"close-window\" \"none\"\n\t\"close-window\" \"Q\"\n}",
		},
		{
			name: "key then none",
			src:  "keymap \"main\" {\n\t\"close-window\" \"Q\"\n\t\"close-window\" \"none\"\n}",
		},
		{
			name: "none twice",
			src:  "keymap \"main\" {\n\t\"close-window\" \"none\"\n\t\"close-window\" \"none\"\n}",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseConfig(c.src)
			if err == nil {
				t.Fatal("parseConfig accepted a duplicate action")
			}
			if !strings.Contains(err.Error(), "duplicate action") || !strings.Contains(err.Error(), "line 3") {
				t.Fatalf("error = %q, want a duplicate action on line 3", err)
			}
		})
	}
}

// TestConfigBadKeyError proves a key name that is not "none" keeps the old
// unknown-key error text.
func TestConfigBadKeyError(t *testing.T) {
	_, err := parseConfig("keymap \"main\" {\n\t\"close-window\" \"bogus\"\n}")
	if err == nil {
		t.Fatal("parseConfig accepted a bad key")
	}
	want := `line 2: key "bogus": unknown key name "bogus"`
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

// TestConfigLoadPathMergesLikeDefaultPath proves both loaders merge the same
// file to the same keymap.
func TestConfigLoadPathMergesLikeDefaultPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	src := "keymap \"main\" {\n\t\"close-window\" \"Q\"\n}\n"
	cfgDir := filepath.Join(dir, "dm")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.kdl"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	explicitDir := filepath.Join(dir, "explicit")
	if err := os.MkdirAll(explicitDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fromDefault, err := LoadDefaultPath(false)
	if err != nil {
		t.Fatalf("LoadDefaultPath: %v", err)
	}
	fromPath, err := LoadPath(writeConfigFile(t, explicitDir, src))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if !reflect.DeepEqual(fromPath, fromDefault) {
		t.Fatalf("LoadPath and LoadDefaultPath merged differently:\nLoadPath: %#v\nLoadDefaultPath: %#v", fromPath, fromDefault)
	}
	for _, cfg := range []*Config{fromPath, fromDefault} {
		if got := cfg.Key("main", "close-window"); got != "Q" {
			t.Fatalf("Key(main, close-window) = %q, want Q", got)
		}
		if got := cfg.Key("main", "close-pane"); got != "x" {
			t.Fatalf("Key(main, close-pane) = %q, want x from the defaults", got)
		}
	}
}

// TestConfigDefaultFileStaysDefault proves merging the default text into itself
// adds nothing: the default keymap and help rows stay as they were.
func TestConfigDefaultFileStaysDefault(t *testing.T) {
	cfg, err := LoadPath(writeConfigFile(t, t.TempDir(), defaultConfigKDL))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if !reflect.DeepEqual(cfg, LoadDefault()) {
		t.Fatal("merging the default file changed the default keymap")
	}
	if got, want := cfg.HelpRows(), LoadDefault().HelpRows(); !reflect.DeepEqual(got, want) {
		t.Fatalf("HelpRows() = %#v, want %#v", got, want)
	}
}

// TestConfigHelpTag proves a tagged binding carries its section in HelpEntries
// and in the per-mode lookup, and an untagged binding carries none.
func TestConfigHelpTag(t *testing.T) {
	src := `keymap "main" {
	"split-right" "|" help "Pane"
	"split-down" "-"
}`
	cfg, err := parseConfig(src)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if got := cfg.HelpSection("main", "split-right"); got != "Pane" {
		t.Errorf("HelpSection(main, split-right) = %q, want Pane", got)
	}
	if got := cfg.HelpSection("main", "split-down"); got != "" {
		t.Errorf("HelpSection(main, split-down) = %q, want empty", got)
	}
	entries := cfg.HelpEntries()
	if len(entries) != 2 {
		t.Fatalf("HelpEntries() = %#v, want 2 rows", entries)
	}
	if entries[0].Key != "|" || entries[0].Action != "split right" || entries[0].Section != "Pane" {
		t.Errorf("entry 0 = %#v, want | / split right / Pane", entries[0])
	}
	if entries[1].Key != "-" || entries[1].Action != "split down" || entries[1].Section != "" {
		t.Errorf("entry 1 = %#v, want - / split down / empty", entries[1])
	}
}

// TestConfigHelpTagPerMode proves a tag in one mode does not reach another
// mode's row.
func TestConfigHelpTagPerMode(t *testing.T) {
	src := `keymap "main" {
	"split-right" "|"
}
keymap "scroll" {
	"split-right" "|" help "Pane"
}`
	cfg, err := parseConfig(src)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if got := cfg.HelpSection("scroll", "split-right"); got != "Pane" {
		t.Errorf("HelpSection(scroll, split-right) = %q, want Pane", got)
	}
	if got := cfg.HelpSection("main", "split-right"); got != "" {
		t.Errorf("HelpSection(main, split-right) = %q, want empty", got)
	}
	entries := cfg.HelpEntries()
	if len(entries) != 1 {
		t.Fatalf("HelpEntries() = %#v, want 1 row", entries)
	}
	if entries[0].Section != "" {
		t.Errorf("main row section = %q, want empty", entries[0].Section)
	}
}

// TestConfigHelpTagMalformed proves each malformed tag fails with its line.
func TestConfigHelpTagMalformed(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "no section",
			src:  "keymap \"main\" {\n\t\"a\" \"x\" help\n}",
			want: "line 2: expected quoted section after help",
		},
		{
			name: "empty section",
			src:  "keymap \"main\" {\n\t\"a\" \"x\" help \"\"\n}",
			want: "line 2: empty help section",
		},
		{
			name: "unknown third token",
			src:  "keymap \"main\" {\n\t\"a\" \"x\" bogus \"Y\"\n}",
			want: "line 2: expected help, got \"bogus\"",
		},
		{
			name: "string third token",
			src:  "keymap \"main\" {\n\t\"a\" \"x\" \"Y\"\n}",
			want: "line 2: expected help, got \"Y\"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseConfig(c.src)
			if err == nil {
				t.Fatal("parseConfig accepted a malformed tag")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %q, want %q", err, c.want)
			}
		})
	}
}

// TestConfigHelpEntriesDefault proves the default config has no tags and that
// HelpRows is HelpEntries without the section.
func TestConfigHelpEntriesDefault(t *testing.T) {
	cfg := LoadDefault()
	entries := cfg.HelpEntries()
	if len(entries) == 0 {
		t.Fatal("HelpEntries() returned no rows")
	}
	for _, e := range entries {
		if e.Section != "" {
			t.Errorf("default entry %#v carries a section", e)
		}
	}
	rows := cfg.HelpRows()
	if len(rows) != len(entries) {
		t.Fatalf("HelpRows() has %d rows, HelpEntries() has %d", len(rows), len(entries))
	}
	for i := range rows {
		if rows[i][0] != entries[i].Key || rows[i][1] != entries[i].Action {
			t.Errorf("row %d = %#v, entry = %#v", i, rows[i], entries[i])
		}
	}
}

// writeTheme puts a theme file in dir/themes and returns its path.
func writeTheme(t *testing.T, dir, name, src string) string {
	t.Helper()
	path := filepath.Join(dir, "themes", name+".kdl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestConfigThemeSelection proves a top-level theme node names the theme and
// that parseConfig alone keeps the built-in theme.
func TestConfigThemeSelection(t *testing.T) {
	cfg, err := parseConfig(`theme "night"`)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.themeName != "night" {
		t.Fatalf("themeName = %q, want night", cfg.themeName)
	}
	if !reflect.DeepEqual(cfg.Theme(), theme.DefaultTheme()) {
		t.Fatal("parseConfig did not leave the built-in theme")
	}
	if !reflect.DeepEqual(LoadDefault().Theme(), theme.DefaultTheme()) {
		t.Fatal("LoadDefault did not leave the built-in theme")
	}
}

// TestConfigThemeDuplicate proves a second theme node fails with its line.
func TestConfigThemeDuplicate(t *testing.T) {
	_, err := parseConfig("theme \"night\"\ntheme \"day\"")
	if err == nil {
		t.Fatal("parseConfig accepted two theme nodes")
	}
	if !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "duplicate theme node") {
		t.Fatalf("error = %q, want a duplicate on line 2", err)
	}
}

// TestConfigThemeUnsafeNames proves a theme name outside [A-Za-z0-9_-]+ fails
// with its line, so it can never leave the themes directory.
func TestConfigThemeUnsafeNames(t *testing.T) {
	for _, name := range []string{"", "a/b", "../x", "..", "a.b", "a b", "/abs", `a\b`, "nihgt!"} {
		src := "theme " + strconvQuote(name)
		_, err := parseConfig(src)
		if err == nil {
			t.Fatalf("parseConfig accepted theme name %q", name)
		}
		if !strings.Contains(err.Error(), "line 1") {
			t.Fatalf("name %q: error = %q, want line 1", name, err)
		}
	}
	for _, name := range []string{"night", "Day_2", "a-b", "A9"} {
		cfg, err := parseConfig("theme " + strconvQuote(name))
		if err != nil {
			t.Fatalf("parseConfig rejected theme name %q: %v", name, err)
		}
		if cfg.themeName != name {
			t.Fatalf("themeName = %q, want %q", cfg.themeName, name)
		}
	}
}

// strconvQuote quotes a name for a KDL string.
func strconvQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// TestConfigThemeLoad proves LoadPath resolves <dir of config>/themes/<name>.kdl
// after parsing the keymap.
func TestConfigThemeLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.kdl")
	src := "theme \"night\"\nkeymap \"main\" {\n\t\"split-right\" \"S\"\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTheme(t, dir, "night", `palette { "rail-bg" "#010203" }
glyphs { "grip" "@" }`)

	cfg, err := LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if got := cfg.Key("main", "split-right"); got != "S" {
		t.Fatalf("theme selection dropped the keymap: split-right = %q", got)
	}
	want := theme.DefaultTheme()
	want.Palette.RailBG = theme.RGB{R: 1, G: 2, B: 3}
	want.Glyphs.Grip = "@"
	if !reflect.DeepEqual(cfg.Theme(), want) {
		t.Fatalf("theme = %+v, want %+v", cfg.Theme(), want)
	}
}

// TestConfigThemeMissingFallsBack proves a named theme with no file returns the
// keymap with the built-in theme and the error.
func TestConfigThemeMissingFallsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.kdl")
	src := "theme \"night\"\nkeymap \"main\" {\n\t\"split-right\" \"S\"\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadPath(path)
	if err == nil {
		t.Fatal("LoadPath accepted a missing theme file")
	}
	if !os.IsNotExist(err) {
		t.Fatalf("error = %v, want a missing-file error", err)
	}
	if got := cfg.Key("main", "split-right"); got != "S" {
		t.Fatalf("missing theme dropped the keymap: split-right = %q", got)
	}
	if !reflect.DeepEqual(cfg.Theme(), theme.DefaultTheme()) {
		t.Fatal("missing theme did not fall back to the built-in theme")
	}
}

// TestConfigThemeMalformedFallsBack proves a malformed theme file returns the
// keymap with the built-in theme and an error that names the line.
func TestConfigThemeMalformedFallsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.kdl")
	src := "theme \"night\"\nkeymap \"main\" {\n\t\"split-right\" \"S\"\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTheme(t, dir, "night", "palette {\n\t\"rail-bg\" \"#12345\"\n}")
	cfg, err := LoadPath(path)
	if err == nil {
		t.Fatal("LoadPath accepted a malformed theme file")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("error = %q, want line 2", err)
	}
	if got := cfg.Key("main", "split-right"); got != "S" {
		t.Fatalf("malformed theme dropped the keymap: split-right = %q", got)
	}
	if !reflect.DeepEqual(cfg.Theme(), theme.DefaultTheme()) {
		t.Fatal("malformed theme did not fall back to the built-in theme")
	}
}

// TestConfigThemeDefaultPath proves LoadDefaultPath resolves the theme next to
// the default config file.
func TestConfigThemeDefaultPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "dm")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfgDir, "config.kdl")
	if err := os.WriteFile(path, []byte("theme \"night\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTheme(t, cfgDir, "night", `accents { "accent-2" "#0a0b0c" }`)

	cfg, err := LoadDefaultPath(false)
	if err != nil {
		t.Fatalf("LoadDefaultPath: %v", err)
	}
	want := theme.DefaultTheme()
	want.Accents[1] = theme.RGB{R: 10, G: 11, B: 12}
	if !reflect.DeepEqual(cfg.Theme(), want) {
		t.Fatalf("theme = %+v, want %+v", cfg.Theme(), want)
	}
}

// TestConfigDashboardPreview reads the dashboard's starting preview share: the
// node, the default, the zero that hides the band, and the bounds it rejects.
func TestConfigDashboardPreview(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"absent", `prefix "ctrl+b"`, defaultDashboardPreview},
		{"zero", `dashboard-preview 0`, 0},
		{"share", `dashboard-preview 25`, 25},
		{"full", `dashboard-preview 100`, 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := parseConfig(c.src)
			if err != nil {
				t.Fatalf("parseConfig: %v", err)
			}
			if got := cfg.DashboardPreview(); got != c.want {
				t.Fatalf("DashboardPreview() = %d, want %d", got, c.want)
			}
		})
	}
	if got := LoadDefault().DashboardPreview(); got != defaultDashboardPreview {
		t.Fatalf("LoadDefault().DashboardPreview() = %d, want %d", got, defaultDashboardPreview)
	}
	for _, bad := range []string{"dashboard-preview 101", "dashboard-preview -1", "dashboard-preview wide"} {
		if _, err := parseConfig(bad); err == nil {
			t.Fatalf("parseConfig accepted %q", bad)
		}
	}
	if _, err := parseConfig("dashboard-preview 10\ndashboard-preview 20"); err == nil {
		t.Fatal("parseConfig accepted two dashboard-preview nodes")
	}
}

// TestConfigDefaultFold proves the default fold setting: absent means folded,
// "fold" means folded and "unfold" means unfolded.
func TestConfigDefaultFold(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"absent", `prefix "ctrl+b"`, false},
		{"fold", `default-fold "fold"`, false},
		{"unfold", `default-fold "unfold"`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := parseConfig(c.src)
			if err != nil {
				t.Fatalf("parseConfig: %v", err)
			}
			if got := cfg.DefaultFold(); got != c.want {
				t.Fatalf("DefaultFold() = %v, want %v", got, c.want)
			}
		})
	}
	if got := LoadDefault().DefaultFold(); got {
		t.Fatal("LoadDefault().DefaultFold() = true, want false")
	}
}

// TestConfigDefaultFoldDuplicate proves a second default-fold node fails with
// its line.
func TestConfigDefaultFoldDuplicate(t *testing.T) {
	_, err := parseConfig("default-fold \"fold\"\ndefault-fold \"unfold\"")
	if err == nil {
		t.Fatal("parseConfig accepted two default-fold nodes")
	}
	if !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "duplicate default-fold node") {
		t.Fatalf("error = %q, want a duplicate on line 2", err)
	}
}

// TestConfigDefaultFoldMalformed proves a value outside fold and unfold fails
// with its line, and a missing value fails.
func TestConfigDefaultFoldMalformed(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"unknown value", `default-fold "bogus"`, `default-fold must be "fold" or "unfold", got "bogus"`},
		{"missing value", `default-fold`, "expected quoted value after default-fold"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseConfig(c.src)
			if err == nil {
				t.Fatal("parseConfig accepted a malformed default-fold node")
			}
			if !strings.Contains(err.Error(), "line 1") || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %q, want line 1 and %q", err, c.want)
			}
		})
	}
}

// TestConfigTabWidth proves the tab width bounds: absent means the built-in
// defaults, a bare number sets one bound, and a quoted number is accepted.
func TestConfigTabWidth(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantMin int
		wantMax int
	}{
		{"absent", `prefix "ctrl+b"`, defaultTabMin, defaultTabMax},
		{"both", "tab-min-width 6\ntab-max-width 12", 6, 12},
		{"quoted", `tab-min-width "6"` + "\n" + `tab-max-width "12"`, 6, 12},
		{"min only", "tab-min-width 6", 6, defaultTabMax},
		{"max only", "tab-max-width 12", defaultTabMin, 12},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := parseConfig(c.src)
			if err != nil {
				t.Fatalf("parseConfig: %v", err)
			}
			if got := cfg.TabMinWidth(); got != c.wantMin {
				t.Fatalf("TabMinWidth() = %d, want %d", got, c.wantMin)
			}
			if got := cfg.TabMaxWidth(); got != c.wantMax {
				t.Fatalf("TabMaxWidth() = %d, want %d", got, c.wantMax)
			}
		})
	}
	if got := LoadDefault().TabMinWidth(); got != defaultTabMin {
		t.Fatalf("LoadDefault().TabMinWidth() = %d, want %d", got, defaultTabMin)
	}
	if got := LoadDefault().TabMaxWidth(); got != defaultTabMax {
		t.Fatalf("LoadDefault().TabMaxWidth() = %d, want %d", got, defaultTabMax)
	}
}

// TestConfigWindowLabel proves the window label template: absent means the
// built-in default, a quoted template is kept, and a bad template fails.
func TestConfigWindowLabel(t *testing.T) {
	if got := LoadDefault().WindowLabel(); got != wlabel.Default {
		t.Fatalf("LoadDefault().WindowLabel() = %q, want %q", got, wlabel.Default)
	}
	cfg, err := parseConfig(`window-label "{{.ID}} - {{.Name}} - {{base .Cwd}}"`)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if got, want := cfg.WindowLabel(), "{{.ID}} - {{.Name}} - {{base .Cwd}}"; got != want {
		t.Fatalf("WindowLabel() = %q, want %q", got, want)
	}
	for _, src := range []string{`window-label "{{.Nope}}"`, `window-label "{{base .Nope}}"`, `window-label "{{.Name"`} {
		if _, err := parseConfig(src); err == nil {
			t.Fatalf("parseConfig(%q) accepted a bad template", src)
		}
	}
	if _, err := parseConfig("window-label \"{{.Name}}\"\nwindow-label \"{{.Name}}\""); err == nil {
		t.Fatal("parseConfig accepted a duplicate window-label node")
	}
}

// TestConfigTabWidthMalformed proves a bad tab width node fails with its line.
func TestConfigTabWidthMalformed(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"missing value", "tab-min-width", "expected a number after tab-min-width"},
		{"not a number", "tab-min-width wide", "tab-min-width must be a number"},
		{"below floor", "tab-min-width 2", "tab-min-width must be at least 3"},
		{"max below min", "tab-min-width 12\ntab-max-width 6", "tab-max-width 6 is below tab-min-width 12"},
		{"duplicate", "tab-min-width 6\ntab-min-width 8", "duplicate tab-min-width node"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseConfig(c.src)
			if err == nil {
				t.Fatal("parseConfig accepted a malformed tab width node")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %q, want %q", err, c.want)
			}
		})
	}
}

// TestConfigPadding proves the pane padding: absent means the built-in default,
// a bare or quoted number is kept, and a value outside the range fails.
func TestConfigPadding(t *testing.T) {
	if got := LoadDefault().Padding(); got != defaultPadding {
		t.Fatalf("LoadDefault().Padding() = %d, want %d", got, defaultPadding)
	}
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"absent", `prefix "ctrl+b"`, defaultPadding},
		{"zero", "padding 0", 0},
		{"one", "padding 1", 1},
		{"max", "padding 4", maxPadding},
		{"quoted", `padding "2"`, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := parseConfig(c.src)
			if err != nil {
				t.Fatalf("parseConfig: %v", err)
			}
			if got := cfg.Padding(); got != c.want {
				t.Fatalf("Padding() = %d, want %d", got, c.want)
			}
		})
	}
	for _, src := range []string{"padding -1", "padding 5", "padding wide", "padding 1\npadding 2"} {
		if _, err := parseConfig(src); err == nil {
			t.Fatalf("parseConfig(%q) accepted a bad padding", src)
		}
	}
}

// TestConfigGlyphs proves the glyph set key: absent means auto, each spelling
// maps to its set, and an unknown spelling fails.
func TestConfigGlyphs(t *testing.T) {
	if got := LoadDefault().GlyphSet(); got != theme.GlyphAuto {
		t.Fatalf("LoadDefault().GlyphSet() = %v, want auto", got)
	}
	cases := map[string]theme.GlyphSet{
		`glyphs "auto"`:    theme.GlyphAuto,
		`glyphs "ascii"`:   theme.GlyphASCII,
		`glyphs "unicode"`: theme.GlyphUnicode,
		`glyphs "nerd"`:    theme.GlyphNerd,
	}
	for src, want := range cases {
		cfg, err := parseConfig(src)
		if err != nil {
			t.Fatalf("parseConfig(%q): %v", src, err)
		}
		if got := cfg.GlyphSet(); got != want {
			t.Fatalf("parseConfig(%q).GlyphSet() = %v, want %v", src, got, want)
		}
	}
	for _, src := range []string{`glyphs "emoji"`, `glyphs nerd`, `glyphs "nerd"` + "\n" + `glyphs "ascii"`} {
		if _, err := parseConfig(src); err == nil {
			t.Fatalf("parseConfig(%q) accepted a bad glyph set", src)
		}
	}
}
