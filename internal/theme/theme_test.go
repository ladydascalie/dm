package theme

import (
	"reflect"
	"strings"
	"testing"
)

// TestThemeDefaults pins the built-in theme: the palette, the six accents, every
// structural glyph, both Nerd fold forms, and every state style.
func TestThemeDefaults(t *testing.T) {
	th := DefaultTheme()

	wantPalette := Palette{
		RailBG:      RGB{20, 19, 26},
		RailFocusBG: RGB{44, 42, 56},
		RailHoverBG: RGB{33, 32, 45},
		RailText:    RGB{233, 234, 241},
		RailDim:     RGB{143, 147, 168},
		RailHeader:  RGB{166, 169, 189},
		RailWindow:  RGB{149, 153, 174},
		RailDiffAdd: RGB{120, 220, 130},
		RailDiffDel: RGB{255, 125, 125},
		RailWorking: RGB{245, 190, 80},
		BarAccent:   RGB{122, 192, 235},
	}
	if th.Palette != wantPalette {
		t.Fatalf("default palette = %+v, want %+v", th.Palette, wantPalette)
	}
	wantAccents := [6]RGB{
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
		SyncMark:          "⧉",
		NerdSyncMark:      "\uf0e2",
		Ellipsis:          "…",
	}
	if th.Glyphs != wantGlyphs {
		t.Fatalf("default glyphs = %+v, want %+v", th.Glyphs, wantGlyphs)
	}
	wantStates := map[string]StateStyle{
		"working": {Glyph: "●", NerdGlyph: "\uf013", Colour: RGB{245, 190, 80}},
		"idle":    {Glyph: "○", NerdGlyph: "\uf111", Colour: RGB{120, 200, 225}},
		"blocked": {Glyph: "▲", NerdGlyph: "\uf071", Colour: RGB{255, 110, 110}},
		"done":    {Glyph: "✓", NerdGlyph: "\uf00c", Colour: RGB{110, 225, 145}},
		"error":   {Glyph: "✕", NerdGlyph: "\uf057", Colour: RGB{255, 130, 160}},
		"unknown": {Glyph: "?", NerdGlyph: "\uf128", Colour: RGB{143, 147, 168}},
		"shell":   {Glyph: "▶", Colour: RGB{143, 147, 168}},
	}
	if !reflect.DeepEqual(th.States, wantStates) {
		t.Fatalf("default states = %+v, want %+v", th.States, wantStates)
	}
}

// TestThemeDefaultNerdGlyphs pins every built-in Nerd Font glyph and keeps the
// error and unknown marks distinct, so Nerd Font users keep a readable error.

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
	want.Palette.RailBG = RGB{0, 0, 0}
	want.Palette.RailText = RGB{0xab, 0xcd, 0xef}
	want.Palette.BarAccent = RGB{0x12, 0x34, 0x56}
	if !reflect.DeepEqual(th, want) {
		t.Fatalf("theme = %+v, want %+v", th, want)
	}
}

// TestThemeAccents proves an accents block replaces one hue and keeps the rest.

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
	want.Accents[0] = RGB{1, 2, 3}
	want.Accents[5] = RGB{240, 240, 240}
	if !reflect.DeepEqual(th, want) {
		t.Fatalf("theme = %+v, want %+v", th, want)
	}
}

// TestThemeGlyphs proves a glyphs block overrides the named glyphs. An explicit
// fold caret also replaces its Nerd Font form.

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
	if idle.Colour != (RGB{1, 2, 3}) {
		t.Fatalf("idle colour = %v, want 1,2,3", idle.Colour)
	}
	if idle.Glyph != "○" || idle.NerdGlyph != "\uf111" {
		t.Fatalf("idle glyph = %q/%q, want the built-in pair", idle.Glyph, idle.NerdGlyph)
	}
}

// TestThemePartialOverride proves a theme that names one glyph keeps every other
// value, including the whole palette, accents and states.

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

// TestThemeReachesPainters proves a theme value reaches the painters: the
// palette, the accents, the glyphs and the states all paint from the value the
// caller passes, and no call changes the built-in theme.
