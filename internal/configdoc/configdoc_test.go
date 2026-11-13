package configdoc

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"dm/internal/kdl"
	"dm/internal/keymap"
	"dm/internal/theme"
	"dm/internal/wlabel"
)

// TestActionDesc proves every action the built-in keymap binds has a
// description, and that the map carries no action the keymap does not bind.
func TestActionDesc(t *testing.T) {
	bound := map[string]bool{}
	for _, m := range keymap.LoadDefault().Keymaps() {
		for _, a := range m.Actions {
			bound[a.Action] = true
		}
	}
	var missing, extra []string
	for name := range bound {
		if ActionDesc[name] == "" {
			missing = append(missing, name)
		}
	}
	for name := range ActionDesc {
		if !bound[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("actions without a description: %s", strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		t.Errorf("descriptions for actions no mode binds: %s", strings.Join(extra, ", "))
	}
}

// TestKeymapModes proves the table carries the nine modes in the keymap
// view's order and that each action has a key and a description.
func TestKeymapModes(t *testing.T) {
	want := []string{"main", "menu", "picker", "switcher", "layout", "scroll", "rename", "box", "help"}
	modes := KeymapModes()
	if len(modes) != len(want) {
		t.Fatalf("got %d modes, want %d", len(modes), len(want))
	}
	for i, name := range want {
		if modes[i].Name != name {
			t.Errorf("mode %d = %q, want %q", i, modes[i].Name, name)
		}
		if modes[i].Desc == "" {
			t.Errorf("mode %q has no description", name)
		}
		if len(modes[i].Actions) == 0 {
			t.Errorf("mode %q has no actions", name)
		}
		for _, a := range modes[i].Actions {
			if a.Key == "" {
				t.Errorf("mode %q action %q has no key", name, a.Name)
			}
			if a.Desc == "" {
				t.Errorf("mode %q action %q has no description", name, a.Name)
			}
		}
	}
}

// TestKeymapModesFoldsPicks proves the nine numbered pick actions print as one
// row per mode that binds the whole run, and that no row claims a key a mode
// does not bind.
func TestKeymapModesFoldsPicks(t *testing.T) {
	for _, m := range KeymapModes() {
		folded := false
		for _, a := range m.Actions {
			if a.Name == "pick-1" {
				t.Errorf("mode %q lists pick-1 on its own", m.Name)
			}
			if a.Name != "pick-1..pick-9" {
				continue
			}
			folded = true
			if a.Key != "1-9" || a.Desc == "" {
				t.Errorf("mode %q folded row = %+v, want key 1-9 and a description", m.Name, a)
			}
		}
		switch m.Name {
		case "menu", "picker", "switcher":
			if !folded {
				t.Errorf("mode %q binds the nine pick keys but has no folded row", m.Name)
			}
		default:
			if folded {
				t.Errorf("mode %q has a folded row but does not bind the run", m.Name)
			}
		}
	}
}

// TestConfigNodesParse builds a config that names every node in the table and
// proves the parser accepts it and the getters return the values it sets.
func TestConfigNodesParse(t *testing.T) {
	samples := map[string]string{
		"prefix":            `prefix "ctrl+a"`,
		"default-fold":      `default-fold "unfold"`,
		"tab-min-width":     `tab-min-width 10`,
		"tab-max-width":     `tab-max-width 30`,
		"padding":           `padding 3`,
		"history-lines":     `history-lines 4000`,
		"glyphs":            `glyphs "nerd"`,
		"window-label":      `window-label "{{.ID}}"`,
		"dashboard-preview": `dashboard-preview 25`,
		"diff-command":      `diff-command "git diff --stat"`,
		"dashboard-layout":  `dashboard-layout "float"`,
		"float-shadow":      `float-shadow "on"`,
		"theme":             `theme "probe"`,
		"keymap":            `keymap "main" { "detach" "D" }`,
		"command":           `command "check" { run "go test ./..." }`,
	}
	if len(samples) != len(ConfigNodes) {
		t.Fatalf("table has %d nodes, test has %d samples", len(ConfigNodes), len(samples))
	}
	var b strings.Builder
	for _, n := range ConfigNodes {
		s, ok := samples[n.Name]
		if !ok {
			t.Fatalf("node %q has no sample", n.Name)
		}
		b.WriteString(s)
		b.WriteString("\n")
	}
	src := b.String()

	cfg, err := keymap.ParseConfig(src)
	if err != nil {
		t.Fatalf("ParseConfig: %v\n%s", err, src)
	}
	if got := cfg.PrefixName(); got != "ctrl+a" {
		t.Errorf("PrefixName = %q, want ctrl+a", got)
	}
	if !cfg.DefaultFoldSet() || !cfg.DefaultFold() {
		t.Errorf("DefaultFoldSet = %v, DefaultFold = %v, want true, true", cfg.DefaultFoldSet(), cfg.DefaultFold())
	}
	if got := cfg.TabMinWidth(); got != 10 {
		t.Errorf("TabMinWidth = %d, want 10", got)
	}
	if got := cfg.TabMaxWidth(); got != 30 {
		t.Errorf("TabMaxWidth = %d, want 30", got)
	}
	if got := cfg.Padding(); got != 3 {
		t.Errorf("Padding = %d, want 3", got)
	}
	if got := cfg.HistoryLines(); got != 4000 {
		t.Errorf("HistoryLines = %d, want 4000", got)
	}
	if got := cfg.GlyphSet(); got != theme.GlyphNerd {
		t.Errorf("GlyphSet = %v, want nerd", got)
	}
	if got := cfg.WindowLabel(); got != "{{.ID}}" {
		t.Errorf("WindowLabel = %q, want %q", got, "{{.ID}}")
	}
	if got := cfg.DashboardPreview(); got != 25 {
		t.Errorf("DashboardPreview = %d, want 25", got)
	}
	if got := cfg.DiffCommand(); got != "git diff --stat" {
		t.Errorf("DiffCommand = %q, want %q", got, "git diff --stat")
	}
	if !cfg.FloatShadow() {
		t.Error("FloatShadow = false, want true")
	}
	if cfg.DashboardFullScreen() {
		t.Error("DashboardFullScreen = true, want the float layout")
	}
	if got := cfg.Key("main", "detach"); got != "D" {
		t.Errorf("Key(main, detach) = %q, want D", got)
	}

	// The theme node carries no name getter, so LoadPath is what proves it
	// reaches the theme loader. The theme file sets one slot the built-in theme
	// does not use.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.kdl"), src)
	writeFile(t, filepath.Join(dir, "themes", "probe.kdl"), `palette {
	"rail-bg" "#010203"
}`)
	loaded, err := keymap.LoadPath(filepath.Join(dir, "config.kdl"))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	want := theme.RGB{R: 1, G: 2, B: 3}
	if got := loaded.Theme().Palette.RailBG; got != want {
		t.Errorf("theme rail-bg = %+v, want %+v", got, want)
	}
}

// TestConfigNodeDefaults proves the built-in default each table row names is
// the one the code uses.
func TestConfigNodeDefaults(t *testing.T) {
	want := map[string]string{
		"prefix":            `"` + keymap.LoadDefault().PrefixName() + `"`,
		"default-fold":      `"fold"`,
		"tab-min-width":     "8",
		"tab-max-width":     "20",
		"padding":           "0",
		"history-lines":     "2000",
		"glyphs":            `"auto"`,
		"window-label":      `"` + wlabel.Default + `"`,
		"dashboard-preview": "60",
		"diff-command":      `"git diff"`,
		"dashboard-layout":  `"full"`,
		"float-shadow":      `"off"`,
		"theme":             "",
		"keymap":            "the built-in table",
		"command":           "none",
	}
	for _, n := range ConfigNodes {
		if got, ok := want[n.Name]; !ok {
			t.Errorf("node %q has no expected default in the test", n.Name)
		} else if n.Default != got {
			t.Errorf("node %q default = %q, want %q", n.Name, n.Default, got)
		}
	}
	for name := range want {
		found := false
		for _, n := range ConfigNodes {
			if n.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("test expects node %q, table has none", name)
		}
	}
}

// TestThemeKeys writes a theme file that names every key in the table and loads
// it. The parser rejects an unknown key, so the test proves the list matches
// the parser. Naming every key at its default must reproduce the built-in
// theme.
func TestThemeKeys(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "themes", "probe.kdl"), themeFileFromKeys(ThemeKeys()))
	got, err := theme.LoadThemeFile(dir, "probe")
	if err != nil {
		t.Fatalf("LoadThemeFile: %v", err)
	}
	if want := sampleDefaultTheme(); !reflect.DeepEqual(got, want) {
		t.Errorf("theme from named defaults does not equal the built-in theme")
	}
}

// TestThemeSampleKDL proves the sample parses, paints the built-in theme, and
// names every key in the table.
func TestThemeSampleKDL(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "themes", "sample.kdl"), ThemeSampleKDL)
	got, err := theme.LoadThemeFile(dir, "sample")
	if err != nil {
		t.Fatalf("LoadThemeFile: %v", err)
	}
	if want := sampleDefaultTheme(); !reflect.DeepEqual(got, want) {
		t.Errorf("ThemeSampleKDL does not paint the built-in theme")
	}

	named := sampleKeyNames(t, ThemeSampleKDL)
	for _, k := range ThemeKeys() {
		name := k.Block + "." + k.Name
		if !named[name] {
			t.Errorf("ThemeSampleKDL does not name %s", name)
		}
	}
}

// TestEnvVars proves every variable in the table is read by os.Getenv in a
// non-test source file.
func TestEnvVars(t *testing.T) {
	root := repoRoot(t)
	reads := map[string]bool{}
	re := regexp.MustCompile(`os\.Getenv\("([A-Z0-9_]+)"\)`)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "testdata" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range re.FindAllStringSubmatch(string(data), -1) {
			reads[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	for _, e := range EnvVars {
		if !reads[e.Name] {
			t.Errorf("%s has no os.Getenv read in a source file", e.Name)
		}
	}
}

// sampleDefaultTheme is the theme a file that names every key at its default
// paints. An explicit fold caret or state glyph also replaces its Nerd Font
// form.
func sampleDefaultTheme() theme.Theme {
	d := theme.DefaultTheme()
	d.Glyphs.NerdFoldOpen = d.Glyphs.FoldOpen
	d.Glyphs.NerdFoldClosed = d.Glyphs.FoldClosed
	for name, style := range d.States {
		style.NerdGlyph = style.Glyph
		d.States[name] = style
	}
	return d
}

// themeFileFromKeys renders a theme file that names every key at its default.
func themeFileFromKeys(keys []ThemeKey) string {
	var palette, accents, glyphs []ThemeKey
	states := map[string][]ThemeKey{}
	for _, k := range keys {
		switch {
		case k.Block == "palette":
			palette = append(palette, k)
		case k.Block == "accents":
			accents = append(accents, k)
		case k.Block == "glyphs":
			glyphs = append(glyphs, k)
		case strings.HasPrefix(k.Block, "states."):
			name := strings.TrimPrefix(k.Block, "states.")
			states[name] = append(states[name], k)
		default:
			panic("unknown theme block " + k.Block)
		}
	}
	var b strings.Builder
	writeBlock := func(block string, ks []ThemeKey) {
		b.WriteString(block)
		b.WriteString(" {\n")
		for _, k := range ks {
			b.WriteString("\t\"")
			b.WriteString(k.Name)
			b.WriteString("\" \"")
			b.WriteString(k.Default)
			b.WriteString("\"\n")
		}
		b.WriteString("}\n\n")
	}
	writeBlock("palette", palette)
	writeBlock("accents", accents)
	writeBlock("glyphs", glyphs)
	b.WriteString("states {\n")
	for _, name := range stateNames {
		ks, ok := states[name]
		if !ok {
			panic("theme keys are missing state " + name)
		}
		b.WriteString("\t\"")
		b.WriteString(name)
		b.WriteString("\" {\n")
		for _, k := range ks {
			b.WriteString("\t\t\"")
			b.WriteString(k.Name)
			b.WriteString("\" \"")
			b.WriteString(k.Default)
			b.WriteString("\"\n")
		}
		b.WriteString("\t}\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// sampleKeyNames scans a theme file and returns "block.key" for every key. A
// state key is "states.<state>.<key>".
func sampleKeyNames(t *testing.T, src string) map[string]bool {
	t.Helper()
	toks, err := kdl.Scan(src)
	if err != nil {
		t.Fatalf("kdl.Scan: %v", err)
	}
	out := map[string]bool{}
	i := 0
	for i < len(toks) && toks[i].Kind != kdl.EOF {
		block := toks[i].Value
		i++
		i = skip(t, toks, i, kdl.LBrace)
		for i < len(toks) && toks[i].Kind != kdl.RBrace {
			if block == "states" {
				state := toks[i].Value
				i++
				i = skip(t, toks, i, kdl.LBrace)
				for i < len(toks) && toks[i].Kind != kdl.RBrace {
					key := toks[i].Value
					i += 2
					out["states."+state+"."+key] = true
				}
				i = skip(t, toks, i, kdl.RBrace)
				continue
			}
			key := toks[i].Value
			i += 2
			out[block+"."+key] = true
		}
		i = skip(t, toks, i, kdl.RBrace)
	}
	return out
}

// skip proves toks[i] has kind want and returns i+1.
func skip(t *testing.T, toks []kdl.Token, i int, want kdl.Kind) int {
	t.Helper()
	if i >= len(toks) || toks[i].Kind != want {
		t.Fatalf("token %d is not the expected kind %v", i, want)
	}
	return i + 1
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}
