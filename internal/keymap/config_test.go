package keymap

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
		"copy-pane-id",
		"focus-left", "focus-down",
		"focus-up", "focus-right", "next-pane", "prev-pane", "close-pane",
		"zoom-pane", "new-window", "next-window", "prev-window", "new-workspace",
		"next-workspace", "switcher", "picker", "command-palette",
		"rename-workspace", "set-project",
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
		"preview-bigger", "preview-smaller", "peek", "copy-pane-id", "diff", "diff-split",
		"down-ctrl-n", "down", "down-arrow",
		"up-ctrl-p", "up", "up-arrow",
		"pick-1", "pick-2", "pick-3", "pick-4", "pick-5",
		"pick-6", "pick-7", "pick-8", "pick-9",
	},
	"scroll": {
		"search", "next-match", "prev-match",
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
		"page-down", "top", "bottom", "toggle-page", "body",
		"filter-notify", "jump-pane", "clear-entry", "clear-filter",
		"close", "close-prefix", "close-ctrl-c", "close-esc",
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

// TestConfigCommandPaletteCollision proves the K the command palette takes is
// free: prefix K matches the palette, focus-up keeps k, prev-pane keeps p, and
// set-project keeps P. The default K belonged to focus-up; the palette took it
// and focus-up moved to k, so the two never race on one key.
func TestConfigCommandPaletteCollision(t *testing.T) {
	cfg := LoadDefault()
	action, n, state := cfg.Match("main", []byte{'K'})
	if state != Match || action != "command-palette" || n != 1 {
		t.Fatalf("Match(main, K) = %q, %d, %v; want command-palette, 1, Match", action, n, state)
	}
	if got := cfg.Key("main", "focus-up"); got != "k" {
		t.Fatalf("Key(main, focus-up) = %q, want k", got)
	}
	if got := cfg.Key("main", "prev-pane"); got != "p" {
		t.Fatalf("Key(main, prev-pane) = %q, want p", got)
	}
	if got := cfg.Key("main", "command-palette"); got != "K" {
		t.Fatalf("Key(main, command-palette) = %q, want K", got)
	}
	if got := cfg.Key("main", "set-project"); got != "P" {
		t.Fatalf("Key(main, set-project) = %q, want P", got)
	}
}

// TestConfigMergeSkipsTakenDefaultKey proves a pre-palette user keymap keeps
// its own k: mergeDefaults leaves command-palette unbound instead of wearing
// the stolen key, so no help row and no keymap view row advertises a key that
// never fires.
func TestConfigMergeSkipsTakenDefaultKey(t *testing.T) {
	for _, src := range []string{
		`keymap "main" {
	"focus-up" "K"
}
`,
		`keymap "main" {
	"close-pane" "K"
}
`,
	} {
		cfg, err := LoadPath(writeConfigFile(t, t.TempDir(), src))
		if err != nil {
			t.Fatalf("LoadPath: %v", err)
		}
		if got := cfg.Key("main", "command-palette"); got != "" {
			t.Fatalf("Key(main, command-palette) = %q, want unbound when the file holds k", got)
		}
		action, n, state := cfg.Match("main", []byte{'K'})
		if state != Match || action != "focus-up" && action != "close-pane" || n != 1 {
			t.Fatalf("Match(main, K) = %q, %d, %v; want the file's own action", action, n, state)
		}
		for _, e := range cfg.HelpEntries() {
			if e.Action == "command-palette" {
				t.Fatalf("the help box advertises command-palette on %q", e.Key)
			}
		}
	}
}

// TestConfigMergeSkipsTakenDefaultKeyLonger proves a user binding that only
// extends the default's key also keeps it: the longer sequence owns the key
// presses, so the default would never fire. An arrow binding starts with esc,
// so the default esc binding stays out.
func TestConfigMergeSkipsTakenDefaultKeyLonger(t *testing.T) {
	cfg, err := LoadPath(writeConfigFile(t, t.TempDir(), `keymap "main" {
	"focus-up-arrow" "up"
}
`))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if got := cfg.Key("main", "dismiss-esc"); got != "" {
		t.Fatalf("Key(main, dismiss-esc) = %q, want unbound under the longer arrow binding", got)
	}
	action, n, state := cfg.Match("main", []byte{0x1b, '[', 'A'})
	if state != Match || action != "focus-up-arrow" || n != 3 {
		t.Fatalf("Match(main, up) = %q, %d, %v; want focus-up-arrow, 3, Match", action, n, state)
	}
}

// TestConfigMergeKeepsNestedDefaultKey proves a longer default survives a
// shorter existing binding: a file that binds esc keeps the arrow defaults,
// because esc nesting under the arrows is how the prefix table resolves, and
// Match picks the longer sequence first.
func TestConfigMergeKeepsNestedDefaultKey(t *testing.T) {
	cfg, err := LoadPath(writeConfigFile(t, t.TempDir(), `keymap "main" {
	"dismiss-esc" "esc"
}
`))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if got := cfg.Key("main", "focus-up-arrow"); got != "up" {
		t.Fatalf("Key(main, focus-up-arrow) = %q, want up from the defaults", got)
	}
	action, n, state := cfg.Match("main", []byte{0x1b, '[', 'A'})
	if state != Match || action != "focus-up-arrow" || n != 3 {
		t.Fatalf("Match(main, up) = %q, %d, %v; want focus-up-arrow, 3, Match", action, n, state)
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

// TestConfigMissingDefaultWrites proves a missing explicit path writes nothing
// without the write flag and creates the directory and file with it. The file
// parses back to the defaults.
func TestConfigMissingDefaultWrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "dm", "config.kdl")

	cfg, err := LoadDefaultFrom(path, false)
	if err != nil {
		t.Fatalf("LoadDefaultFrom(path, false): %v", err)
	}
	if !reflect.DeepEqual(cfg, LoadDefault()) {
		t.Fatal("LoadDefaultFrom(path, false) did not return the defaults")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("LoadDefaultFrom(path, false) created %q", path)
	}

	cfg, err = LoadDefaultFrom(path, true)
	if err != nil {
		t.Fatalf("LoadDefaultFrom(path, true): %v", err)
	}
	if !reflect.DeepEqual(cfg, LoadDefault()) {
		t.Fatal("LoadDefaultFrom(path, true) did not return the defaults")
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
		{"i", "copy pane id"},
		{"h j k l", "focus pane"},
		{"n / p", "next/prev pane"},
		{"x", "close pane"},
		{"X", "close window"},
		{"z", "zoom pane"},
		{"c", "new window"},
		{"w / W", "next/prev win"},
		{"0-9", "select window"},
		{"o / O", "swap with next / prev pane"},
		{"u", "cycle layout"},
		{"t", "new workspace"},
		{")", "next workspace"},
		{"S", "synchronize panes"},
		{"ctrl+g", "toggle secret redaction"},
		{"s", "switch session"},
		{"K", "command palette"},
		{"r", "rename workspace"},
		{"P", "set project"},
		{"m", "message box"},
		{"N", "notifications"},
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

// TestConfigLoadDefaultFromMergesLikeLoadPath proves both loaders merge the
// same file to the same keymap.
func TestConfigLoadDefaultFromMergesLikeLoadPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := "keymap \"main\" {\n\t\"close-window\" \"Q\"\n}\n"
	cfgDir := filepath.Join(dir, "dm")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	defaultPath := filepath.Join(cfgDir, "config.kdl")
	if err := os.WriteFile(defaultPath, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	explicitDir := filepath.Join(dir, "explicit")
	if err := os.MkdirAll(explicitDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fromDefault, err := LoadDefaultFrom(defaultPath, false)
	if err != nil {
		t.Fatalf("LoadDefaultFrom: %v", err)
	}
	fromPath, err := LoadPath(writeConfigFile(t, explicitDir, src))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if !reflect.DeepEqual(fromPath, fromDefault) {
		t.Fatalf("LoadPath and LoadDefaultFrom merged differently:\nLoadPath: %#v\nLoadDefaultFrom: %#v", fromPath, fromDefault)
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

// TestConfigHelpEntriesDefault proves the default config tags only the five
// layout and session actions and that HelpRows is HelpEntries without the
// section.
func TestConfigHelpEntriesDefault(t *testing.T) {
	cfg := LoadDefault()
	entries := cfg.HelpEntries()
	if len(entries) == 0 {
		t.Fatal("HelpEntries() returned no rows")
	}
	wantSection := map[string]string{
		"swap with next / prev pane": "Layout",
		"cycle layout":               "Layout",
		"synchronize panes":          "Session",
		"toggle secret redaction":    "Session",
	}
	for _, e := range entries {
		want, tagged := wantSection[e.Action]
		if e.Section != "" && (!tagged || e.Section != want) {
			t.Errorf("default entry %#v carries an unexpected section", e)
		}
		if e.Section == "" && tagged {
			t.Errorf("default entry %#v owes section %q", e, want)
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

// TestConfigThemeLoadDefaultFrom proves LoadDefaultFrom resolves the theme next
// to the explicit config file.
func TestConfigThemeLoadDefaultFrom(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "dm")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfgDir, "config.kdl")
	if err := os.WriteFile(path, []byte("theme \"night\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTheme(t, cfgDir, "night", `accents { "accent-2" "#0a0b0c" }`)

	cfg, err := LoadDefaultFrom(path, false)
	if err != nil {
		t.Fatalf("LoadDefaultFrom: %v", err)
	}
	want := theme.DefaultTheme()
	want.Accents[1] = theme.RGB{R: 10, G: 11, B: 12}
	if !reflect.DeepEqual(cfg.Theme(), want) {
		t.Fatalf("theme = %+v, want %+v", cfg.Theme(), want)
	}
}

// TestConfigDashboardLayout reads the dashboard's shape: the node, its default
// of the full screen, and the two words it accepts.
// TestConfigDefaultPathEnvFallback proves LoadDefaultPath still delegates to
// the env-based path: XDG_CONFIG_HOME wins, then $HOME/.config. It keeps
// t.Setenv and stays serial because t.Setenv mutates process env and so cannot
// run in parallel.
func TestConfigDefaultPathEnvFallback(t *testing.T) {
	src := "keymap \"main\" {\n\t\"close-window\" \"Q\"\n}\n"

	t.Run("xdg", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", dir)
		cfgDir := filepath.Join(dir, "dm")
		if err := os.MkdirAll(cfgDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfgDir, "config.kdl"), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadDefaultPath(false)
		if err != nil {
			t.Fatalf("LoadDefaultPath: %v", err)
		}
		if got := cfg.Key("main", "close-window"); got != "Q" {
			t.Fatalf("Key(main, close-window) = %q, want Q", got)
		}
	})

	t.Run("home", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", dir)
		cfgDir := filepath.Join(dir, ".config", "dm")
		if err := os.MkdirAll(cfgDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfgDir, "config.kdl"), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadDefaultPath(false)
		if err != nil {
			t.Fatalf("LoadDefaultPath: %v", err)
		}
		if got := cfg.Key("main", "close-window"); got != "Q" {
			t.Fatalf("Key(main, close-window) = %q, want Q", got)
		}
	})
}

// TestConfigFloatShadow reads the float drop shadow switch: the node, the
// default and the values it rejects.
func TestConfigFloatShadow(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"absent", `prefix "ctrl+b"`, false},
		{"off", `float-shadow "off"`, false},
		{"on", `float-shadow "on"`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := parseConfig(c.src)
			if err != nil {
				t.Fatalf("parseConfig: %v", err)
			}
			if got := cfg.FloatShadow(); got != c.want {
				t.Fatalf("FloatShadow() = %v, want %v", got, c.want)
			}
		})
	}
	if LoadDefault().FloatShadow() {
		t.Fatal("LoadDefault().FloatShadow() = true, want false")
	}
	for _, bad := range []string{`float-shadow "maybe"`, `float-shadow 1`} {
		if _, err := parseConfig(bad); err == nil {
			t.Fatalf("parseConfig accepted %q", bad)
		}
	}
	if _, err := parseConfig("float-shadow \"off\"\nfloat-shadow \"on\""); err == nil {
		t.Fatal("parseConfig accepted two float-shadow nodes")
	}
}

func TestConfigDashboardLayout(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"absent", `prefix "ctrl+b"`, false},
		{"full", `dashboard-layout "full"`, true},
		{"float", `dashboard-layout "float"`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := parseConfig(c.src)
			if err != nil {
				t.Fatalf("parseConfig: %v", err)
			}
			if got := cfg.DashboardFullScreen(); got != c.want {
				t.Fatalf("DashboardFullScreen() = %v, want %v", got, c.want)
			}
		})
	}
	if LoadDefault().DashboardFullScreen() {
		t.Fatal("LoadDefault().DashboardFullScreen() = true, want false")
	}
	for _, bad := range []string{`dashboard-layout "wide"`, `dashboard-layout 1`} {
		if _, err := parseConfig(bad); err == nil {
			t.Fatalf("parseConfig accepted %q", bad)
		}
	}
	if _, err := parseConfig("dashboard-layout \"full\"\ndashboard-layout \"float\""); err == nil {
		t.Fatal("parseConfig accepted two dashboard-layout nodes")
	}
}

// TestConfigShelfPlacement reads the shelf float's placement and share: the
// defaults, both layouts, the size bounds it rejects, and the duplicate rule.
func TestConfigShelfPlacement(t *testing.T) {
	placements := []struct {
		name string
		src  string
		want bool
	}{
		{"absent", `prefix "ctrl+b"`, defaultShelfCentered},
		{"centered", `shelf-layout "centered"`, true},
		{"pane", `shelf-layout "pane"`, false},
	}
	for _, c := range placements {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := parseConfig(c.src)
			if err != nil {
				t.Fatalf("parseConfig: %v", err)
			}
			if got := cfg.ShelfCentered(); got != c.want {
				t.Fatalf("ShelfCentered() = %v, want %v", got, c.want)
			}
		})
	}
	if got := LoadDefault().ShelfCentered(); got != defaultShelfCentered {
		t.Fatalf("LoadDefault().ShelfCentered() = %v, want %v", got, defaultShelfCentered)
	}

	sizes := []struct {
		name string
		src  string
		want int
	}{
		{"absent", `prefix "ctrl+b"`, defaultShelfSize},
		{"thirty", `shelf-size 30`, 30},
		{"floor", `shelf-size 20`, 20},
		{"ceiling", `shelf-size 100`, 100},
	}
	for _, c := range sizes {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := parseConfig(c.src)
			if err != nil {
				t.Fatalf("parseConfig: %v", err)
			}
			if got := cfg.ShelfSize(); got != c.want {
				t.Fatalf("ShelfSize() = %d, want %d", got, c.want)
			}
		})
	}
	if got := LoadDefault().ShelfSize(); got != defaultShelfSize {
		t.Fatalf("LoadDefault().ShelfSize() = %d, want %d", got, defaultShelfSize)
	}

	for _, bad := range []string{
		`shelf-layout "wide"`, `shelf-layout 1`,
		`shelf-size 19`, `shelf-size 101`, `shelf-size "big"`,
	} {
		if _, err := parseConfig(bad); err == nil {
			t.Fatalf("parseConfig accepted %q", bad)
		}
	}
	for _, dup := range []string{
		"shelf-layout \"centered\"\nshelf-layout \"pane\"",
		"shelf-size 30\nshelf-size 40",
	} {
		if _, err := parseConfig(dup); err == nil {
			t.Fatalf("parseConfig accepted two nodes in %q", dup)
		}
	}
}

// TestConfigPaletteRows reads the command palette's row cap: the node, the
// default, and the bounds it rejects.
func TestConfigPaletteRows(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"absent", `prefix "ctrl+b"`, defaultPaletteRows},
		{"one", `palette-rows 1`, 1},
		{"twelve", `palette-rows 12`, 12},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := parseConfig(c.src)
			if err != nil {
				t.Fatalf("parseConfig: %v", err)
			}
			if got := cfg.PaletteRows(); got != c.want {
				t.Fatalf("PaletteRows() = %d, want %d", got, c.want)
			}
		})
	}
	if got := LoadDefault().PaletteRows(); got != defaultPaletteRows {
		t.Fatalf("LoadDefault().PaletteRows() = %d, want %d", got, defaultPaletteRows)
	}
	for _, bad := range []string{"palette-rows 0", "palette-rows -3", "palette-rows wide"} {
		if _, err := parseConfig(bad); err == nil {
			t.Fatalf("parseConfig accepted %q", bad)
		}
	}
	if _, err := parseConfig("palette-rows 5\npalette-rows 9"); err == nil {
		t.Fatal("parseConfig accepted a duplicate palette-rows node")
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

// TestConfigDiffCommand reads the dashboard diff command: the node, its
// default, and the empty and missing values it rejects.
func TestConfigDiffCommand(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"absent", `prefix "ctrl+b"`, "hunk diff"},
		{"set", `diff-command "git diff --stat"`, "git diff --stat"},
		{"compound", `diff-command "git diff -- src && true"`, "git diff -- src && true"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := parseConfig(c.src)
			if err != nil {
				t.Fatalf("parseConfig: %v", err)
			}
			if got := cfg.DiffCommand(); got != c.want {
				t.Fatalf("DiffCommand() = %q, want %q", got, c.want)
			}
		})
	}
	if got := LoadDefault().DiffCommand(); got != defaultDiffCommand {
		t.Fatalf("LoadDefault().DiffCommand() = %q, want %q", got, defaultDiffCommand)
	}
	if defaultDiffCommand != "hunk diff" {
		t.Fatalf("defaultDiffCommand = %q, want hunk diff", defaultDiffCommand)
	}
}

// TestConfigDiffCommandEmpty proves an empty or missing value fails with the
// node name, and a second node fails as a duplicate.
func TestConfigDiffCommandEmpty(t *testing.T) {
	for _, src := range []string{`diff-command ""`, `diff-command`} {
		_, err := parseConfig(src)
		if err == nil {
			t.Fatalf("parseConfig accepted %q", src)
		}
		if !strings.Contains(err.Error(), "diff-command") {
			t.Fatalf("error = %q, want the node name", err)
		}
	}
	_, err := parseConfig(`diff-command ""`)
	if err == nil || !strings.Contains(err.Error(), "diff-command must not be empty") {
		t.Fatalf("error = %q, want diff-command must not be empty", err)
	}
	if _, err := parseConfig("diff-command \"git diff\"\ndiff-command \"git show\""); err == nil {
		t.Fatal("parseConfig accepted two diff-command nodes")
	}
}

// TestConfigDefaultFold proves the default fold setting: absent means unfolded,
// "fold" means folded and "unfold" means unfolded.
func TestConfigDefaultFold(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"absent", `prefix "ctrl+b"`, true},
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
	if !LoadDefault().DefaultFold() {
		t.Fatal("LoadDefault().DefaultFold() = false, want true")
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

// TestConfigHistoryLines proves the history window: absent means the built-in
// default, a bare or quoted number is kept, both bounds are accepted, and a
// value outside them fails with its node and line.
func TestConfigHistoryLines(t *testing.T) {
	if got := LoadDefault().HistoryLines(); got != defaultHistoryLines {
		t.Fatalf("LoadDefault().HistoryLines() = %d, want %d", got, defaultHistoryLines)
	}
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"absent", `prefix "ctrl+b"`, defaultHistoryLines},
		{"default", "history-lines 2000", 2000},
		{"min", "history-lines 100", minHistoryLines},
		{"max", "history-lines 32768", maxHistoryLines},
		{"quoted", `history-lines "5000"`, 5000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := parseConfig(c.src)
			if err != nil {
				t.Fatalf("parseConfig: %v", err)
			}
			if got := cfg.HistoryLines(); got != c.want {
				t.Fatalf("HistoryLines() = %d, want %d", got, c.want)
			}
		})
	}
	bad := []struct {
		src  string
		want string
	}{
		{"history-lines 99", "line 1: history-lines must be 100 to 32768, got 99"},
		{"history-lines 32769", "line 1: history-lines must be 100 to 32768, got 32769"},
		{"history-lines wide", `line 1: history-lines must be a number, got "wide"`},
		{"history-lines", "line 1: expected a number after history-lines"},
		{"history-lines 2000\nhistory-lines 3000", "line 2: duplicate history-lines node"},
	}
	for _, c := range bad {
		_, err := parseConfig(c.src)
		if err == nil {
			t.Fatalf("parseConfig(%q) accepted a bad history-lines", c.src)
		}
		if err.Error() != c.want {
			t.Fatalf("parseConfig(%q) error = %q, want %q", c.src, err, c.want)
		}
	}
}

// TestConfigHistoryBytes proves the pane ring budget: a small window keeps the
// 512 KiB floor, the default buys 2.5 MiB, a large window grows above it,
// the 32768 line maximum reaches the 8 MiB cap, and a nil config reports the
// built-in default.
func TestConfigHistoryBytes(t *testing.T) {
	cases := []struct {
		name string
		cfg  *Config
		want int
	}{
		{"nil", nil, 10000 * 256},
		{"default", LoadDefault(), 10000 * 256},
		{"floor", &Config{historyLines: minHistoryLines}, 512 << 10},
		{"default lines", &Config{historyLines: 2000}, 512 << 10},
		{"above floor", &Config{historyLines: 4000}, 4000 * 256},
		{"cap", &Config{historyLines: maxHistoryLines}, 8 << 20},
		{"over cap", &Config{historyLines: 40000}, 8 << 20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cfg.HistoryBytes(); got != c.want {
				t.Fatalf("HistoryBytes() = %d, want %d", got, c.want)
			}
		})
	}
	parsed, err := parseConfig("history-lines 2000")
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if got := parsed.HistoryBytes(); got != 512<<10 {
		t.Fatalf("parsed HistoryBytes() = %d, want the 512 KiB ring 2000 lines buys", got)
	}
	parsed, err = parseConfig("history-lines 32768")
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if got := parsed.HistoryBytes(); got != 8<<20 {
		t.Fatalf("parsed HistoryBytes() at the maximum = %d, want the 8 MiB cap", got)
	}
}

// TestConfigScrollSearchBindings proves the scroll keymap binds the history
// search keys and the keymap view lists them first.
func TestConfigScrollSearchBindings(t *testing.T) {
	cfg := LoadDefault()
	for action, want := range map[string]string{"search": "/", "next-match": "n", "prev-match": "N"} {
		if got := cfg.Key("scroll", action); got != want {
			t.Errorf("Key(scroll, %q) = %q, want %q", action, got, want)
		}
	}
	for key, want := range map[byte]string{'/': "search", 'n': "next-match", 'N': "prev-match"} {
		action, n, state := cfg.Match("scroll", []byte{key})
		if state != Match || action != want || n != 1 {
			t.Errorf("Match(scroll, %q) = %q, %d, %v; want %q, 1, Match", key, action, n, state, want)
		}
	}
	var modes []string
	for _, m := range cfg.Keymaps() {
		modes = append(modes, m.Name)
	}
	if len(modes) != len(keymapOrder) {
		t.Fatalf("Keymaps() = %v, want every mode", modes)
	}
	scroll := cfg.Keymaps()[5]
	if scroll.Name != "scroll" {
		t.Fatalf("mode 4 = %q, want scroll", scroll.Name)
	}
	want := []string{"search", "next-match", "prev-match"}
	for i, name := range want {
		if scroll.Actions[i].Action != name {
			t.Errorf("scroll row %d = %q, want %q", i, scroll.Actions[i].Action, name)
		}
	}
}

// TestConfigCommand parses one command block: the Commands() list, a binding
// that names it, and the help tag on the row.
func TestConfigCommand(t *testing.T) {
	src := `command "tests" {
	run "go test ./..."
}
keymap "main" {
	"command:tests" "t" help "Run"
}`
	cfg, err := parseConfig(src)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	want := []Command{{Name: "tests", Run: "go test ./..."}}
	if got := cfg.Commands(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Commands() = %#v, want %#v", got, want)
	}
	if got := cfg.Key("main", "command:tests"); got != "t" {
		t.Fatalf("Key(main, command:tests) = %q, want t", got)
	}
	if got := cfg.HelpSection("main", "command:tests"); got != "Run" {
		t.Fatalf("HelpSection(main, command:tests) = %q, want Run", got)
	}
}

// TestConfigCommandSorted proves Commands() sorts by name no matter the file
// order.
func TestConfigCommandSorted(t *testing.T) {
	src := `command "zebra" {
	run "echo z"
}
command "alpha" {
	run "echo a"
}`
	cfg, err := parseConfig(src)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	want := []Command{{Name: "alpha", Run: "echo a"}, {Name: "zebra", Run: "echo z"}}
	if got := cfg.Commands(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Commands() = %#v, want %#v", got, want)
	}
}

// TestConfigCommandDuplicateName proves a second command with the same name
// fails with its line.
func TestConfigCommandDuplicateName(t *testing.T) {
	_, err := parseConfig("command \"x\" { run \"a\" }\ncommand \"x\" { run \"b\" }")
	if err == nil {
		t.Fatal("parseConfig accepted two commands with the same name")
	}
	if !strings.Contains(err.Error(), "duplicate command") || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("error = %q, want a duplicate command on line 2", err)
	}
}

// TestConfigPlugin parses one plugin block, proves every field reaches the
// accessor, and binds it as plugin:<name>.
func TestConfigPlugin(t *testing.T) {
	cfg, err := parseConfig(`plugin "rail" {
	exec "/home/b/rail/run" "--watch"
	cwd "/home/b/rail"
	where "window"
	env "A=1"
	env "B=2"
}
keymap "main" {
	"plugin:rail" "g"
}`)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	want := []Plugin{{
		Name:  "rail",
		Exec:  []string{"/home/b/rail/run", "--watch"},
		Cwd:   "/home/b/rail",
		Where: "window",
		Env:   []string{"A=1", "B=2"},
	}}
	if got := cfg.Plugins(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Plugins() = %#v, want %#v", got, want)
	}
	if p, ok := cfg.Plugin("rail"); !ok || p.Where != "window" {
		t.Fatalf("Plugin(rail) = %#v/%v, want the declaration", p, ok)
	}
	if got := cfg.Key("main", "plugin:rail"); got != "g" {
		t.Fatalf("Key(plugin:rail) = %q, want g", got)
	}
}

// TestConfigPluginErrors proves each malformed block fails at parse with a
// sentence naming the fault.
func TestConfigPluginErrors(t *testing.T) {
	bad := []struct{ name, src, want string }{
		{"missing name", `plugin {`, "expected a quoted name after plugin"},
		{"bad name", `plugin "a b" { exec "/bin/true" where "split" }`, "plugin name"},
		{"missing exec", `plugin "rail" { where "split" }`, "needs exec"},
		{"empty exec", `plugin "rail" { exec "" where "split" }`, "exec has an empty element"},
		{"relative exec", `plugin "rail" { exec "rail" where "split" }`, "must be an absolute path"},
		{"comma exec", `plugin "rail" { exec "/bin/true" "a,b" where "split" }`, "contains a comma"},
		{"missing where", `plugin "rail" { exec "/bin/true" }`, "needs where"},
		{"bad where", `plugin "rail" { exec "/bin/true" where "float" }`, "must be \"split\" or \"window\""},
		{"duplicate exec", `plugin "rail" { exec "/bin/true" exec "/bin/false" where "split" }`, "duplicate exec"},
		{"duplicate cwd", `plugin "rail" { exec "/bin/true" cwd "/a" cwd "/b" where "split" }`, "duplicate cwd"},
		{"relative cwd", `plugin "rail" { exec "/bin/true" cwd "a" where "split" }`, "cwd must be an absolute path"},
		{"unknown key", `plugin "rail" { exec "/bin/true" where "split" key "x" }`, "unknown key"},
		{"duplicate plugin", `plugin "rail" { exec "/bin/true" where "split" } plugin "rail" { exec "/bin/true" where "split" }`, "duplicate plugin"},
		{"collision", `plugin "reload" { exec "/bin/true" where "split" }`, "collides with a built-in action"},
		{"duplicate env", `plugin "rail" { exec "/bin/true" where "split" env "A=1" env "A=2" }`, "duplicate env key"},
		{"bad env", `plugin "rail" { exec "/bin/true" where "split" env "A" }`, "must be KEY=VALUE"},
		{"reserved env", `plugin "rail" { exec "/bin/true" where "split" env "DM_PANE=x" }`, "is reserved"},
		{"unknown binding", `keymap "main" { "plugin:nope" "g" }`, "names no plugin"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseConfig(tc.src)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("parseConfig = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestConfigCommandCollides proves a command whose name is also a built-in
// action name fails with its line.
func TestConfigCommandCollides(t *testing.T) {
	for _, name := range []string{"reload", "split-right", "down", "close"} {
		src := "command " + strconvQuote(name) + " {\n\trun \"x\"\n}"
		_, err := parseConfig(src)
		if err == nil {
			t.Fatalf("parseConfig accepted command %q colliding with a built-in action", name)
		}
		if !strings.Contains(err.Error(), "collides with a built-in action") || !strings.Contains(err.Error(), "line 1") {
			t.Fatalf("name %q: error = %q, want a collision on line 1", name, err)
		}
	}
}

// TestConfigCommandMalformed proves each malformed command block fails with
// its line.
func TestConfigCommandMalformed(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"duplicate run", "command \"x\" {\n\trun \"a\"\n\trun \"b\"\n}", `line 3: duplicate run in command "x"`},
		{"missing run", "command \"x\" {\n}", `line 1: command "x" needs a run key`},
		{"empty run", "command \"x\" {\n\trun \"\"\n}", `line 2: run in command "x" must not be empty`},
		{"run without value", "command \"x\" {\n\trun\n}", "line 2: expected a quoted command after run"},
		{"unknown key", "command \"x\" {\n\trun \"a\"\n\tbogus \"b\"\n}", `line 3: unknown key "bogus" in command "x"`},
		{"missing name", "command {\n\trun \"a\"\n}", "line 1: expected a quoted name after command"},
		{"missing brace", "command \"x\" run \"a\"", `line 1: expected '{' after command "x"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseConfig(c.src)
			if err == nil {
				t.Fatal("parseConfig accepted a malformed command block")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %q, want %q", err, c.want)
			}
		})
	}
}

// TestConfigCommandName proves the name charset: letters, digits, _ and -,
// 1 to 32 bytes, and nothing else.
func TestConfigCommandName(t *testing.T) {
	for _, name := range []string{"tests", "go_test", "go-test", "A9z", "a", "x", strings.Repeat("b", 32)} {
		cfg, err := parseConfig("command " + strconvQuote(name) + " { run \"x\" }")
		if err != nil {
			t.Fatalf("parseConfig rejected command name %q: %v", name, err)
		}
		if got := cfg.Commands(); len(got) != 1 || got[0].Name != name {
			t.Fatalf("Commands() = %#v, want one command named %q", got, name)
		}
	}
	for _, name := range []string{"", "a b", "a/b", "a.b", "ä", strings.Repeat("b", 33)} {
		_, err := parseConfig("command " + strconvQuote(name) + " { run \"x\" }")
		if err == nil {
			t.Fatalf("parseConfig accepted command name %q", name)
		}
		if !strings.Contains(err.Error(), "line 1") {
			t.Fatalf("name %q: error = %q, want line 1", name, err)
		}
	}
}

// TestConfigCommandBindingUnknown proves a keymap action with the command:
// prefix fails when the file defines no such command, and that a bound
// command still parses when its block comes later in the file.
func TestConfigCommandBindingUnknown(t *testing.T) {
	_, err := parseConfig("keymap \"main\" {\n\t\"command:nope\" \"x\"\n}")
	if err == nil {
		t.Fatal("parseConfig accepted a binding to an unknown command")
	}
	if !strings.Contains(err.Error(), "names no command") || !strings.Contains(err.Error(), "nope") || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("error = %q, want the action and line 2", err)
	}
	_, err = parseConfig("keymap \"main\" {\n\t\"command:nope\" \"none\"\n}")
	if err == nil {
		t.Fatal("parseConfig accepted an unbound unknown command")
	}
	cfg, err := parseConfig("keymap \"main\" {\n\t\"command:tests\" \"t\"\n}\ncommand \"tests\" {\n\trun \"go test ./...\"\n}")
	if err != nil {
		t.Fatalf("parseConfig rejected a binding to a later command: %v", err)
	}
	if got := cfg.Key("main", "command:tests"); got != "t" {
		t.Fatalf("Key(main, command:tests) = %q, want t", got)
	}
}

// TestConfigCommandKeymaps proves Keymaps() lists a command action with its
// key alongside the built-in actions.
func TestConfigCommandKeymaps(t *testing.T) {
	src := `command "tests" {
	run "go test ./..."
}
keymap "main" {
	"command:tests" "t"
}`
	cfg, err := parseConfig(src)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	for _, m := range cfg.Keymaps() {
		if m.Name != "main" {
			continue
		}
		for _, a := range m.Actions {
			if a.Action == "command:tests" {
				if a.Key != "t" {
					t.Fatalf("command:tests key = %q, want t", a.Key)
				}
				return
			}
		}
		t.Fatalf("main Keymaps() has no command:tests action: %#v", m.Actions)
	}
	t.Fatal("Keymaps() has no main mode")
}

// TestConfigCommandDefaultDefinesNone proves the built-in config defines no
// commands and that its text stays a comment, so a file that names a command
// still merges to the defaults.
func TestConfigCommandDefaultDefinesNone(t *testing.T) {
	if got := LoadDefault().Commands(); len(got) != 0 {
		t.Fatalf("LoadDefault().Commands() = %#v, want none", got)
	}
	if !strings.Contains(defaultConfigKDL, "command \"tests\"") {
		t.Fatal("defaultConfigKDL does not show the command shape")
	}
	dir := t.TempDir()
	cfg, err := LoadPath(writeConfigFile(t, dir, `command "tests" {
	run "go test ./..."
}
keymap "main" {
	"command:tests" "t"
}
`))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if got := cfg.Key("main", "command:tests"); got != "t" {
		t.Fatalf("Key(main, command:tests) = %q, want t", got)
	}
	if got := cfg.Key("main", "split-right"); got != "|" {
		t.Fatalf("Key(main, split-right) = %q, want | from the defaults", got)
	}
}

// TestLayoutModeDefaults pins the layout mode: the keymap view lists it after
// the switcher, and the defaults bind the walk, the pick and the three closes.
func TestLayoutModeDefaults(t *testing.T) {
	cfg := LoadDefault()
	var modes []string
	for _, m := range cfg.Keymaps() {
		modes = append(modes, m.Name)
	}
	i := -1
	for j, name := range modes {
		if name == "layout" {
			i = j
		}
	}
	if i < 1 || modes[i-1] != "switcher" {
		t.Fatalf("layout mode sits at %d in %v, want right after switcher", i, modes)
	}
	want := map[string]string{
		"down": "j", "up": "k", "next-arrow": "down", "prev-arrow": "up",
		"pick": "enter", "close": "q", "close-esc": "esc", "close-prefix": cfg.PrefixName(),
	}
	for action, key := range want {
		if got := cfg.Key("layout", action); got != key {
			t.Errorf("Key(layout, %q) = %q, want %q", action, got, key)
		}
	}
	if got := len(cfg.Keymaps()[i].Actions); got != len(want) {
		t.Errorf("layout mode binds %d actions, want %d", got, len(want))
	}
}

// TestLayoutPickerKeyFree proves L was free in the built-in main table before
// the layout-picker took it: no other action answers L, so the picker key
// displaced nothing.
func TestLayoutPickerKeyFree(t *testing.T) {
	cfg := LoadDefault()
	for _, a := range cfg.Keymaps()[0].Actions {
		if a.Key != "L" {
			continue
		}
		if a.Action != "layout-picker" {
			t.Errorf("main binds %q on L, want only layout-picker", a.Action)
		}
	}
}

// TestLayoutModePatchable: a config.kdl keymap "layout" block patches the
// built-in mode, and an action the block leaves out keeps its built-in key.
func TestLayoutModePatchable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.kdl")
	src := "keymap \"layout\" {\n    \"down\" \"n\"\n    \"close\" \"none\"\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if got := cfg.Key("layout", "down"); got != "n" {
		t.Errorf("Key(layout, down) = %q, want n", got)
	}
	if got := cfg.Key("layout", "pick"); got != "enter" {
		t.Errorf("Key(layout, pick) = %q, want the built-in enter", got)
	}
	if got := cfg.Key("layout", "close"); got != "" {
		t.Errorf("Key(layout, close) = %q, want unbound", got)
	}
}

// TestBuiltinActions proves the parser's action whitelist is exactly the set
// the built-in keymap binds, in both directions. A name on the list that no
// mode binds would reject a config that binds nothing real; a bound name off
// the list would reject the built-in text itself.
func TestBuiltinActions(t *testing.T) {
	bound := map[string]bool{}
	for _, m := range LoadDefault().Keymaps() {
		for _, a := range m.Actions {
			bound[a.Action] = true
		}
	}
	var missing, extra []string
	for name := range bound {
		if !builtinActions[name] {
			missing = append(missing, name)
		}
	}
	for name := range builtinActions {
		if !bound[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("actions the built-in binds but the whitelist lacks: %s", strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		t.Errorf("whitelisted actions no mode binds: %s", strings.Join(extra, ", "))
	}
}

// TestParseRejectsDuplicateBytes proves two bindings in one mode that resolve
// to the same bytes fail the parse: Match gives the equal-length race to the
// first declaration, so the second would be dead while the help box still
// advertises it. Different key names for one byte, ctrl+A and ctrl+a, and two
// encodings of one byte, enter and ctrl+m, both collide.
func TestParseRejectsDuplicateBytes(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"case-fold", `keymap "main" {
	"copy-output" "ctrl+a"
	"new-window" "ctrl+A"
}`, "copy-output"},
		{"enter-ctrl-m", `keymap "main" {
	"copy-output" "enter"
	"new-window" "ctrl+m"
}`, "copy-output"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseConfig(c.src)
			if err == nil {
				t.Fatal("parseConfig accepted two bindings on the same bytes")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not name the winning action %q", err, c.want)
			}
		})
	}
}

// TestParseKeepsNestedBindings proves bindings that nest are not duplicates:
// esc under the arrow escape sequences is how the prefix table resolves, and
// Match gives the longer sequence the press when it completes.
func TestParseKeepsNestedBindings(t *testing.T) {
	src := `keymap "main" {
	"dismiss-esc" "esc"
	"focus-up" "up"
}`
	cfg, err := parseConfig(src)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	action, n, state := cfg.Match("main", []byte{0x1b, '[', 'A'})
	if state != Match || action != "focus-up" || n != 3 {
		t.Errorf("Match(main, up) = %q, %d, %v; want focus-up, 3, Match", action, n, state)
	}
}

// TestLoadRejectsUnknownAction proves a typo in an action name fails the
// load and names the typo, instead of parsing clean, taking the key and
// leaving the real action silently unbound.
func TestLoadRejectsUnknownAction(t *testing.T) {
	cfg, err := LoadPath(writeConfigFile(t, t.TempDir(), `keymap "main" {
	"cloes-pane" "x"
}
`))
	if err == nil {
		t.Fatal("LoadPath accepted the typo cloes-pane")
	}
	if !strings.Contains(err.Error(), "cloes-pane") {
		t.Errorf("error %q does not name the typo", err)
	}
	if got := cfg.Key("main", "close-pane"); got != "x" {
		t.Errorf("Key(main, close-pane) = %q, want the built-in x on the fallback defaults", got)
	}
}

// TestParseRejectsPrefixKeyBinding proves a main-mode binding on the prefix
// bytes fails the parse: the prefix arms before main mode sees the key, so
// the binding is dead unarmed and the help box would advertise it anyway.
// send-prefix is the one action that means the prefix bytes.
func TestParseRejectsPrefixKeyBinding(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"letter", "prefix \"s\"\nkeymap \"main\" {\n\"picker\" \"s\"\n}\n", "is the prefix key"},
		{"esc", "prefix \"esc\"\nkeymap \"main\" {\n\"dismiss-esc\" \"esc\"\n}\n", "is the prefix key"},
		{"enter ctrl m", "prefix \"ctrl+m\"\nkeymap \"main\" {\n\"message-box\" \"enter\"\n}\n", "is the prefix key"},
		{"other mode is fine", "prefix \"s\"\nkeymap \"picker\" {\n\"close-s\" \"s\"\n}\n", ""},
		{"send-prefix is fine", "prefix \"s\"\nkeymap \"main\" {\n\"send-prefix\" \"prefix\"\n}\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := LoadPath(writeConfigFile(t, t.TempDir(), c.src))
			if c.want == "" {
				if err != nil {
					t.Fatalf("LoadPath: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("LoadPath accepted a main-mode binding on the prefix bytes")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want %q", err, c.want)
			}
		})
	}
	if _, err := parseConfig(defaultConfigKDL); err != nil {
		t.Fatalf("the default config now fails: %v", err)
	}
}

// TestMergeSkipsDefaultOnPrefixKey proves a remapped prefix that lands on a
// default main-mode key leaves that default unbound instead of advertising a
// key the prefix eats, and that send-prefix keeps the prefix bytes.
func TestMergeSkipsDefaultOnPrefixKey(t *testing.T) {
	cfg, err := LoadPath(writeConfigFile(t, t.TempDir(), "prefix \"y\"\n"))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if got := cfg.Key("main", "copy-output"); got != "" {
		t.Errorf("Key(main, copy-output) = %q, want unbound: y is the prefix key now", got)
	}
	// Key prints the prefix key's display name, which the remap moved to y.
	if got := cfg.Key("main", "send-prefix"); got != "y" {
		t.Errorf("Key(main, send-prefix) = %q, want the prefix key y", got)
	}
	if action, _, state := cfg.Match("main", []byte{'y'}); state != Match || action != "send-prefix" {
		t.Errorf("Match(main, y) = %q, %v, want send-prefix, Match", action, state)
	}
}

// TestConfigBoxClosePrefixFollowsPrefix proves the box and layout close-prefix
// bindings resolve the live prefix key, not a stale literal, so a remapped
// prefix still closes the overlays.
func TestConfigBoxClosePrefixFollowsPrefix(t *testing.T) {
	cfg, err := LoadPath(writeConfigFile(t, t.TempDir(), "prefix \"ctrl+a\"\n"))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	for _, mode := range []string{"box", "layout"} {
		if got := cfg.Key(mode, "close-prefix"); got != "ctrl+a" {
			t.Errorf("Key(%s, close-prefix) = %q, want the live prefix ctrl+a", mode, got)
		}
		if action, _, state := cfg.Match(mode, []byte{0x01}); state != Match || action != "close-prefix" {
			t.Errorf("Match(%s, ctrl+a) = %q, %v, want close-prefix, Match", mode, action, state)
		}
	}
}

// TestConfigTabWidthAgainstDefaultBound proves a tab width node alone is
// checked against the effective other bound, the default included: a min
// above the default max or a max below the default min would clamp every tab
// into the default and silently ignore the node.
func TestConfigTabWidthAgainstDefaultBound(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"min above default max", "tab-min-width 30", "tab-min-width 30 is above the default tab-max-width 20"},
		{"max below default min", "tab-max-width 5", "tab-max-width 5 is below the default tab-min-width 8"},
		{"min at default max", "tab-min-width 20", ""},
		{"max at default min", "tab-max-width 8", ""},
		// A pair that names both bounds is consistent whatever the defaults
		// are; the default bound is only for a node the file omits.
		{"both above default max", "tab-min-width 25\ntab-max-width 30", ""},
		{"both below default min", "tab-min-width 3\ntab-max-width 5", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseConfig(c.src)
			if c.want == "" {
				if err != nil {
					t.Fatalf("parseConfig: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("parseConfig accepted a min or max the default bound silences")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want %q", err, c.want)
			}
		})
	}
}

// TestConfigUnknownMode proves a keymap mode outside the fixed nine fails the
// parse instead of binding keys nothing reads.
func TestConfigUnknownMode(t *testing.T) {
	_, err := parseConfig("keymap \"float\" {\n\"close\" \"q\"\n}\n")
	if err == nil {
		t.Fatal("parseConfig accepted an unknown keymap mode")
	}
	if !strings.Contains(err.Error(), `unknown keymap mode "float"`) {
		t.Errorf("error = %q, want an unknown keymap mode", err)
	}
	for _, mode := range keymapOrder {
		src := "keymap \"" + mode + "\" {\n\"close\" \"q\"\n}\n"
		if _, err := parseConfig(src); err != nil {
			t.Errorf("parseConfig(%q): %v", src, err)
		}
	}
}

// TestLoadFallsBackToGoodConfig proves a broken config file runs the last
// copy that parsed instead of the built-in defaults, and that the error names
// both the broken line and the fallback.
func TestLoadFallsBackToGoodConfig(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigFile(t, dir, "prefix \"ctrl+a\"\n")
	cfg, err := LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if got := cfg.PrefixName(); got != "ctrl+a" {
		t.Fatalf("PrefixName = %q, want ctrl+a", got)
	}
	if _, err := os.Stat(path + ".good"); err != nil {
		t.Fatalf("the good copy is missing: %v", err)
	}

	if err := os.WriteFile(path, []byte("bogus \"x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadPath(path)
	if err == nil {
		t.Fatal("LoadPath accepted the broken file")
	}
	if !strings.Contains(err.Error(), "unknown node") || !strings.Contains(err.Error(), "last good config") {
		t.Errorf("error = %q, want the broken line and the fallback", err)
	}
	if got := cfg.PrefixName(); got != "ctrl+a" {
		t.Fatalf("the fallback ran the defaults: PrefixName = %q, want ctrl+a", got)
	}

	if err := os.Remove(path + ".good"); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadPath(path)
	if err == nil {
		t.Fatal("LoadPath accepted the broken file")
	}
	if !reflect.DeepEqual(cfg, LoadDefault()) {
		t.Fatal("without a good copy the load did not return the defaults")
	}
	if strings.Contains(err.Error(), "last good config") {
		t.Errorf("error = %q, want no fallback note without a good copy", err)
	}
}

// TestHelpSummaryToggleSecretsCaveat proves the toggle's caveat stays out of
// the help label and in the note. The label is the action column the box
// paints, and 31 cells still splits the box into two columns at 120x30 while
// 35 does not; the caveat costs one row there instead of widening that column.
// A reword that drops either half makes the toggle read as a security control
// again.
func TestHelpSummaryToggleSecretsCaveat(t *testing.T) {
	t.Parallel()
	cfg := LoadDefault()
	label := ""
	for _, e := range cfg.HelpEntries() {
		for _, action := range HelpActions(e.Action) {
			if action == "toggle-secrets" {
				label = e.Action
			}
		}
	}
	if label == "" {
		t.Fatal("no help row carries the toggle-secrets action")
	}
	if len(label) > 31 {
		t.Errorf("help label %q is %d cells, want at most 31 so the box keeps two columns at 120x30", label, len(label))
	}
	for _, caveat := range []string{"edit in place", "not a control"} {
		if strings.Contains(label, caveat) {
			t.Errorf("help label %q carries the caveat %q, want it in the note", label, caveat)
		}
	}
	notes := cfg.HelpNotes()
	if len(notes) != 1 {
		t.Fatalf("HelpNotes() = %#v, want the toggle-secrets note alone", notes)
	}
	for _, caveat := range []string{"edit in place", "not a control"} {
		if !strings.Contains(notes[0], caveat) {
			t.Errorf("help note %q does not say %q", notes[0], caveat)
		}
	}
}

// TestHelpNotesNameARow proves every note is keyed by a label the table
// carries, so a reworded row cannot orphan its caveat.
func TestHelpNotesNameARow(t *testing.T) {
	t.Parallel()
	labels := make(map[string]bool, len(helpRows))
	for _, row := range helpRows {
		labels[row.label] = true
	}
	for label := range helpNotes {
		if !labels[label] {
			t.Errorf("helpNotes[%q] names no help row", label)
		}
	}
}

// TestHelpNotesFollowTheirRow proves a note paints only while its row is
// bound, so a keymap that drops toggle-secrets leaves no stray caveat behind.
func TestHelpNotesFollowTheirRow(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig(`prefix "ctrl+a"
keymap "main" {
	"split-right" "|"
}`)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if notes := cfg.HelpNotes(); len(notes) != 0 {
		t.Fatalf("HelpNotes() = %#v, want none without the toggle-secrets row", notes)
	}
}
