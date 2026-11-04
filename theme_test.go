package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/hinshun/vt10x"
)

// applyTestTheme installs a theme for one test and restores the built-in theme
// afterwards, so no test leaves the renderer's globals changed.
func applyTestTheme(t *testing.T, th Theme) {
	t.Helper()
	applyTheme(th)
	t.Cleanup(func() { applyTheme(DefaultTheme()) })
}

// TestThemeDefaults pins the built-in theme: the palette, the six accents, every
// structural glyph, both Nerd fold forms, and every state style.
func TestThemeDefaults(t *testing.T) {
	th := DefaultTheme()

	wantPalette := Palette{
		RailBG:      rgb{20, 19, 26},
		RailFocusBG: rgb{31, 30, 39},
		RailText:    rgb{233, 234, 241},
		RailDim:     rgb{143, 147, 168},
		RailHeader:  rgb{166, 169, 189},
		RailWindow:  rgb{149, 153, 174},
		RailDiffAdd: rgb{120, 220, 130},
		RailDiffDel: rgb{255, 125, 125},
		RailWorking: rgb{245, 190, 80},
		BarAccent:   rgb{122, 192, 235},
	}
	if th.Palette != wantPalette {
		t.Fatalf("default palette = %+v, want %+v", th.Palette, wantPalette)
	}
	wantAccents := [6]rgb{
		{187, 154, 247}, {110, 220, 235}, {240, 140, 215},
		{126, 169, 255}, {240, 160, 90}, {140, 215, 155},
	}
	if th.Accents != wantAccents {
		t.Fatalf("default accents = %+v, want %+v", th.Accents, wantAccents)
	}
	wantGlyphs := Glyphs{
		Grip:              "•",
		DividerRow:        "━",
		DividerCol:        "┃",
		BorderTopLeft:     "┌",
		BorderTopRight:    "┐",
		BorderBottomLeft:  "└",
		BorderBottomRight: "┘",
		BorderHorizontal:  "─",
		BorderVertical:    "│",
		BorderTeeLeft:     "├",
		BorderTeeRight:    "┤",
		FoldOpen:          "▾",
		FoldClosed:        "▸",
		NerdFoldOpen:      "\uf0d7",
		NerdFoldClosed:    "\uf0da",
		ActiveMark:        "▸",
		WorkspaceBar:      "▌",
		Dirty:             "●",
	}
	if th.Glyphs != wantGlyphs {
		t.Fatalf("default glyphs = %+v, want %+v", th.Glyphs, wantGlyphs)
	}
	wantStates := map[string]StateStyle{
		"working": {Glyph: "●", NerdGlyph: "\uf013", Colour: rgb{245, 190, 80}},
		"idle":    {Glyph: "○", NerdGlyph: "\uf111", Colour: rgb{120, 200, 225}},
		"blocked": {Glyph: "▲", NerdGlyph: "\uf071", Colour: rgb{255, 110, 110}},
		"done":    {Glyph: "✓", NerdGlyph: "\uf00c", Colour: rgb{110, 225, 145}},
		"error":   {Glyph: "✕", NerdGlyph: "\uf057", Colour: rgb{255, 130, 160}},
		"unknown": {Glyph: "?", NerdGlyph: "\uf128", Colour: rgb{143, 147, 168}},
		"shell":   {Glyph: "▶", Colour: rgb{143, 147, 168}},
	}
	if !reflect.DeepEqual(th.States, wantStates) {
		t.Fatalf("default states = %+v, want %+v", th.States, wantStates)
	}
}

// TestThemeDefaultNerdGlyphs pins every built-in Nerd Font glyph and keeps the
// error and unknown marks distinct, so Nerd Font users keep a readable error.
func TestThemeDefaultNerdGlyphs(t *testing.T) {
	applyTestTheme(t, DefaultTheme())
	want := map[string]string{
		"working": "\uf013",
		"idle":    "\uf111",
		"blocked": "\uf071",
		"done":    "\uf00c",
		"error":   "\uf057",
		"unknown": "\uf128",
	}
	for name, glyph := range want {
		if got := DefaultTheme().States[name].NerdGlyph; got != glyph {
			t.Fatalf("state %q NerdGlyph = %q, want %q", name, got, glyph)
		}
		if got, _ := railGlyph(name, true); got != glyph {
			t.Fatalf("railGlyph(%q, nerd) = %q, want %q", name, got, glyph)
		}
	}
	if DefaultTheme().States["error"].NerdGlyph == DefaultTheme().States["unknown"].NerdGlyph {
		t.Fatalf("error and unknown share the Nerd glyph %q",
			DefaultTheme().States["error"].NerdGlyph)
	}
}

// TestThemePalette proves a palette block overrides the named slots and leaves
// every other slot at its built-in value.
func TestThemePalette(t *testing.T) {
	th, err := parseTheme(`palette {
	"rail-bg"       "#000000"
	"rail-text"     "#abcdef"
	"bar-accent"    "#123456"
}`)
	if err != nil {
		t.Fatalf("parseTheme: %v", err)
	}
	want := DefaultTheme()
	want.Palette.RailBG = rgb{0, 0, 0}
	want.Palette.RailText = rgb{0xab, 0xcd, 0xef}
	want.Palette.BarAccent = rgb{0x12, 0x34, 0x56}
	if !reflect.DeepEqual(th, want) {
		t.Fatalf("theme = %+v, want %+v", th, want)
	}
}

// TestThemeAccents proves an accents block replaces one hue and keeps the rest.
func TestThemeAccents(t *testing.T) {
	th, err := parseTheme(`accents {
	"accent-1" "#010203"
	"accent-6" "#f0f0f0"
}`)
	if err != nil {
		t.Fatalf("parseTheme: %v", err)
	}
	want := DefaultTheme()
	want.Accents[0] = rgb{1, 2, 3}
	want.Accents[5] = rgb{240, 240, 240}
	if !reflect.DeepEqual(th, want) {
		t.Fatalf("theme = %+v, want %+v", th, want)
	}
}

// TestThemeGlyphs proves a glyphs block overrides the named glyphs. An explicit
// fold caret also replaces its Nerd Font form.
func TestThemeGlyphs(t *testing.T) {
	th, err := parseTheme(`glyphs {
	"grip"        "@"
	"fold-open"   "+"
	"active-mark" ">"
}`)
	if err != nil {
		t.Fatalf("parseTheme: %v", err)
	}
	want := DefaultTheme()
	want.Glyphs.Grip = "@"
	want.Glyphs.FoldOpen = "+"
	want.Glyphs.NerdFoldOpen = "+"
	want.Glyphs.ActiveMark = ">"
	if !reflect.DeepEqual(th, want) {
		t.Fatalf("theme = %+v, want %+v", th, want)
	}
}

// TestThemeStates proves a state block overrides one field and keeps the other,
// and that a theme glyph replaces both the Unicode and the Nerd form.
func TestThemeStates(t *testing.T) {
	th, err := parseTheme(`states {
	"working" {
		"glyph"  "W"
	}
	"idle" {
		"colour" "#010203"
	}
}`)
	if err != nil {
		t.Fatalf("parseTheme: %v", err)
	}
	working := th.States["working"]
	if working.Glyph != "W" || working.NerdGlyph != "W" {
		t.Fatalf("working glyph = %q/%q, want W/W", working.Glyph, working.NerdGlyph)
	}
	if working.Colour != DefaultTheme().States["working"].Colour {
		t.Fatalf("working colour = %v, want the built-in colour", working.Colour)
	}
	idle := th.States["idle"]
	if idle.Colour != (rgb{1, 2, 3}) {
		t.Fatalf("idle colour = %v, want 1,2,3", idle.Colour)
	}
	if idle.Glyph != "○" || idle.NerdGlyph != "\uf111" {
		t.Fatalf("idle glyph = %q/%q, want the built-in pair", idle.Glyph, idle.NerdGlyph)
	}
	applyTestTheme(t, th)
	if got, _ := railGlyph("working", true); got != "W" {
		t.Fatalf("railGlyph(working, nerd) = %q, want W", got)
	}
}

// TestThemePartialOverride proves a theme that names one glyph keeps every other
// value, including the whole palette, accents and states.
func TestThemePartialOverride(t *testing.T) {
	th, err := parseTheme(`glyphs { "dirty" "o" }`)
	if err != nil {
		t.Fatalf("parseTheme: %v", err)
	}
	want := DefaultTheme()
	want.Glyphs.Dirty = "o"
	if !reflect.DeepEqual(th, want) {
		t.Fatalf("theme = %+v, want %+v", th, want)
	}
}

// TestThemeErrors holds the parser to its rejections: every failure names the
// line.
func TestThemeErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			"unknown block",
			`bogus { }`,
			[]string{"line 1", "unknown block", "bogus"},
		},
		{
			"duplicate block",
			"glyphs {\n\t\"grip\" \"@\"\n}\nglyphs { }",
			[]string{"line 4", "duplicate block", "glyphs"},
		},
		{
			"unknown palette key",
			`palette { "nope" "#000000" }`,
			[]string{"line 1", "unknown palette key", "nope"},
		},
		{
			"duplicate key",
			`palette {
	"rail-bg" "#000000"
	"rail-bg" "#111111"
}`,
			[]string{"line 3", "duplicate key", "rail-bg"},
		},
		{
			"malformed colour",
			`palette {
	"rail-bg" "#12345"
}`,
			[]string{"line 2", "#rrggbb", "#12345"},
		},
		{
			"bad hex digit",
			`palette { "rail-bg" "#gg0000" }`,
			[]string{"line 1", "#rrggbb"},
		},
		{
			"unknown accents key",
			`accents { "accent-7" "#000000" }`,
			[]string{"line 1", "unknown accents key", "accent-7"},
		},
		{
			"multi rune glyph",
			`glyphs { "grip" "ab" }`,
			[]string{"line 1", "exactly one rune"},
		},
		{
			"empty glyph",
			`glyphs { "grip" "" }`,
			[]string{"line 1", "exactly one rune"},
		},
		{
			"wide glyph",
			`glyphs { "grip" "世" }`,
			[]string{"line 1", "one cell wide"},
		},
		{
			"unknown glyphs key",
			`glyphs { "nope" "x" }`,
			[]string{"line 1", "unknown glyphs key", "nope"},
		},
		{
			"unknown state",
			`states { "bogus" { } }`,
			[]string{"line 1", "unknown state", "bogus"},
		},
		{
			"duplicate state",
			`states {
	"idle" { "glyph" "i" }
	"idle" { "glyph" "j" }
}`,
			[]string{"line 3", "duplicate state", "idle"},
		},
		{
			"unknown state key",
			`states { "idle" { "nope" "x" } }`,
			[]string{"line 1", "unknown state key", "nope"},
		},
		{
			"duplicate state key",
			`states {
	"idle" {
		"colour" "#000000"
		"colour" "#111111"
	}
}`,
			[]string{"line 4", "duplicate key", "colour"},
		},
		{
			"unexpected end of file",
			`palette {`,
			[]string{"line 1", "expected '}'"},
		},
		{
			"missing value",
			`palette { "rail-bg" }`,
			[]string{"line 1", "expected quoted value", "rail-bg"},
		},
		{
			"unterminated string",
			`palette { "rail-bg" "#000000 }`,
			[]string{"line 1", "unterminated string"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseTheme(c.src)
			if err == nil {
				t.Fatalf("parseTheme accepted %q", c.src)
			}
			for _, want := range c.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %q, want %q", err, want)
				}
			}
		})
	}
}

// TestThemeApply proves applyTheme installs every part of a theme, that it is
// idempotent, and that a second call with the default restores the built-ins.
func TestThemeApply(t *testing.T) {
	th, err := parseTheme(`palette {
	"rail-bg"       "#010203"
	"rail-focus-bg" "#040506"
	"bar-accent"    "#070809"
}
accents {
	"accent-1" "#101112"
	"accent-6" "#131415"
}
glyphs {
	"grip"    "@"
	"dirty"   "o"
	"fold-open" "+"
}
states {
	"working" {
		"glyph"  "W"
		"colour" "#161718"
	}
}`)
	if err != nil {
		t.Fatalf("parseTheme: %v", err)
	}
	applyTestTheme(t, th)
	assertApplied := func() {
		t.Helper()
		if railBG != (rgb{1, 2, 3}) || railFocusBG != (rgb{4, 5, 6}) || barAccent != (rgb{7, 8, 9}) {
			t.Fatalf("palette not applied: bg=%v focus=%v accent=%v", railBG, railFocusBG, barAccent)
		}
		if barGutter != railBG {
			t.Fatalf("barGutter = %v, want railBG %v", barGutter, railBG)
		}
		if len(railAccents) != 6 || railAccents[0] != (rgb{16, 17, 18}) || railAccents[5] != (rgb{19, 20, 21}) {
			t.Fatalf("accents not applied: %v", railAccents)
		}
		if glyphGrip != "@" || glyphDirty != "o" || glyphFoldOpen != "+" || glyphNerdFoldOpen != "+" {
			t.Fatalf("glyphs not applied: %q %q %q %q", glyphGrip, glyphDirty, glyphFoldOpen, glyphNerdFoldOpen)
		}
		if glyph("working") != "W" {
			t.Fatalf("state glyph = %q, want W", glyph("working"))
		}
	}
	assertApplied()
	applyTheme(th)
	assertApplied()

	applyTheme(DefaultTheme())
	def := DefaultTheme()
	if railBG != def.Palette.RailBG || railFocusBG != def.Palette.RailFocusBG ||
		railText != def.Palette.RailText || railDim != def.Palette.RailDim ||
		railHeader != def.Palette.RailHeader || railWindowFg != def.Palette.RailWindow ||
		railDiffAdd != def.Palette.RailDiffAdd || railDiffDel != def.Palette.RailDiffDel ||
		railWorking != def.Palette.RailWorking || barAccent != def.Palette.BarAccent {
		t.Fatal("default palette did not restore")
	}
	if barGutter != railBG {
		t.Fatalf("barGutter = %v, want railBG %v", barGutter, railBG)
	}
	if !reflect.DeepEqual(railAccents, def.Accents[:]) {
		t.Fatalf("accents = %v, want %v", railAccents, def.Accents)
	}
	restored := Glyphs{
		Grip:              glyphGrip,
		DividerRow:        glyphDividerRow,
		DividerCol:        glyphDividerCol,
		BorderTopLeft:     glyphBorderTopLeft,
		BorderTopRight:    glyphBorderTopRight,
		BorderBottomLeft:  glyphBorderBottomLeft,
		BorderBottomRight: glyphBorderBottomRight,
		BorderHorizontal:  glyphBorderHorizontal,
		BorderVertical:    glyphBorderVertical,
		BorderTeeLeft:     glyphBorderTeeLeft,
		BorderTeeRight:    glyphBorderTeeRight,
		FoldOpen:          glyphFoldOpen,
		FoldClosed:        glyphFoldClosed,
		NerdFoldOpen:      glyphNerdFoldOpen,
		NerdFoldClosed:    glyphNerdFoldClosed,
		ActiveMark:        glyphActiveMark,
		WorkspaceBar:      glyphWorkspaceBar,
		Dirty:             glyphDirty,
	}
	if restored != def.Glyphs {
		t.Fatalf("glyphs = %+v, want %+v", restored, def.Glyphs)
	}
	if !reflect.DeepEqual(stateStyles, def.States) {
		t.Fatalf("states = %+v, want %+v", stateStyles, def.States)
	}
}

// glyph reads the Unicode state glyph from the installed state styles.
func glyph(status string) string {
	return stateStyle(status).Glyph
}

// TestThemeDrawGrips proves drawGrips paints the themed grip, not the built-in
// dot.
func TestThemeDrawGrips(t *testing.T) {
	th := DefaultTheme()
	th.Glyphs.Grip = "@"
	applyTestTheme(t, th)

	root := &Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*Node{{Pane: "a"}, {Pane: "b"}},
	}
	snap := &Snapshot{Cols: 40, Rows: 10, Rail: 0, Root: root}
	var buf bytes.Buffer
	drawGrips(&buf, snap)
	out := buf.String()
	if !strings.Contains(out, "@") {
		t.Fatalf("themed grip not drawn: %q", out)
	}
	if strings.Contains(out, "•") {
		t.Fatalf("built-in grip still drawn: %q", out)
	}
	vt := vt10x.New(vt10x.WithSize(40, 10))
	if _, err := vt.Write(buf.Bytes()); err != nil {
		t.Fatalf("draw grips: %v", err)
	}
	vt.Lock()
	defer vt.Unlock()
	grips := 0
	for y := 0; y < snap.Rows; y++ {
		for x := 0; x < snap.Cols; x++ {
			if vt.Cell(x, y).Char == '@' {
				grips++
			}
		}
	}
	if grips != 2 {
		t.Fatalf("themed cells = %d, want 2", grips)
	}
}

// TestThemeRailRowGlyph proves railRow composes the themed state glyph and
// colour for a pane row, in both the Unicode and the Nerd form.
func TestThemeRailRowGlyph(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th, err := parseTheme(`states {
	"working" {
		"glyph"  "W"
		"colour" "#010203"
	}
}`)
	if err != nil {
		t.Fatalf("parseTheme: %v", err)
	}
	applyTestTheme(t, th)

	items := railItems(agentRail("working"))
	pane := railRowFind(t, items, railPane, "w1p1")
	left, _, _ := railRow(pane, defaultRail, false, false)
	found := false
	for _, seg := range left {
		if seg.text == "W" {
			found = true
			if seg.colour != (rgb{1, 2, 3}) {
				t.Fatalf("themed glyph colour = %v, want 1,2,3", seg.colour)
			}
		}
		if seg.text == "●" {
			t.Fatalf("built-in glyph still composed: %+v", left)
		}
	}
	if !found {
		t.Fatalf("themed state glyph not composed: %+v", left)
	}
	if got, colour := railGlyph("working", true); got != "W" || colour != (rgb{1, 2, 3}) {
		t.Fatalf("nerd glyph = %q/%v, want W/1,2,3", got, colour)
	}

	var b bytes.Buffer
	drawRail(&b, agentRail("working"))
	if !strings.Contains(b.String(), "W") || !strings.Contains(b.String(), (rgb{1, 2, 3}).fg()) {
		t.Fatalf("themed state glyph not drawn: %q", b.String())
	}
}
