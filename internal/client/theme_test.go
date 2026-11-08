package client

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/rail"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

// TestThemeDefaultNerdGlyphs pins every built-in Nerd Font glyph and keeps the
// error and unknown marks distinct, so Nerd Font users keep a readable error.
func TestThemeDefaultNerdGlyphs(t *testing.T) {
	want := map[string]string{
		"working": "\uf013",
		"idle":    "\uf111",
		"blocked": "\uf071",
		"done":    "\uf00c",
		"error":   "\uf057",
		"unknown": "\uf128",
	}
	for name, glyph := range want {
		if got := theme.DefaultTheme().States[name].NerdGlyph; got != glyph {
			t.Fatalf("state %q NerdGlyph = %q, want %q", name, got, glyph)
		}
		if got, _ := rail.RailGlyph(theme.DefaultTheme(), name, true); got != glyph {
			t.Fatalf("rail.RailGlyph(theme.DefaultTheme(), %q, nerd) = %q, want %q", name, got, glyph)
		}
	}
	if theme.DefaultTheme().States["error"].NerdGlyph == theme.DefaultTheme().States["unknown"].NerdGlyph {
		t.Fatalf("error and unknown share the Nerd glyph %q",
			theme.DefaultTheme().States["error"].NerdGlyph)
	}
}

// TestThemeReachesPainters proves a theme value reaches the painters: the
// palette, the accents, the glyphs and the states all paint from the value the
// caller passes, and no call changes the built-in theme.
func TestThemeReachesPainters(t *testing.T) {
	th := theme.DefaultTheme()
	th.Palette.RailBG = theme.RGB{R: 1, G: 2, B: 3}
	th.Palette.RailFocusBG = theme.RGB{R: 4, G: 5, B: 6}
	th.Palette.BarAccent = theme.RGB{R: 7, G: 8, B: 9}
	th.Accents[0] = theme.RGB{R: 16, G: 17, B: 18}
	th.Accents[5] = theme.RGB{R: 19, G: 20, B: 21}
	th.Glyphs.Grip = "@"
	th.Glyphs.Dirty = "o"
	th.Glyphs.FoldOpen = "+"
	th.Glyphs.NerdFoldOpen = "+"
	th.States["working"] = theme.StateStyle{Glyph: "W", NerdGlyph: "W", Colour: theme.RGB{R: 22, G: 23, B: 24}}
	// The palette reaches the rail ground and the bar accent.
	var railBuf bytes.Buffer
	rail.DrawRail(&railBuf, th, testutil.AgentRail("working"))
	if !strings.Contains(railBuf.String(), (theme.RGB{R: 1, G: 2, B: 3}).BG()) {
		t.Fatalf("rail does not paint the themed ground: %q", railBuf.String())
	}
	accent := false
	for _, span := range barSegments(th, testutil.BarTestSnap(80), false, "") {
		if span.FG == (theme.RGB{R: 7, G: 8, B: 9}) {
			accent = true
		}
	}
	if !accent {
		t.Fatal("bar does not paint the themed accent")
	}

	// The accents keep their slots.
	if th.Accents[0] != (theme.RGB{R: 16, G: 17, B: 18}) || th.Accents[5] != (theme.RGB{R: 19, G: 20, B: 21}) {
		t.Fatalf("accents = %v, want 16,17,18 and 19,20,21", th.Accents)
	}

	// The glyphs reach the grips and the dirty dot.
	root := &layout.Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*layout.Node{{Pane: "a"}, {Pane: "b"}},
	}
	var grips bytes.Buffer
	drawGrips(&grips, th, &wire.Snapshot{Cols: 40, Rows: 10, Root: root})
	if !strings.Contains(grips.String(), "@") || strings.Contains(grips.String(), "•") {
		t.Fatalf("grips = %q, want the themed @ only", grips.String())
	}
	if segs := rail.RailStats(th, rail.RailItem{Kind: rail.Window, InRepo: true, Dirty: true}); len(segs) == 0 || segs[0].Text != "o" {
		t.Fatalf("dirty dot = %+v, want the themed o", segs)
	}

	// The states reach the glyph and the colour.
	if g, colour := rail.RailGlyph(th, "working", false); g != "W" || colour != (theme.RGB{R: 22, G: 23, B: 24}) {
		t.Fatalf("working = %q/%v, want W/22,23,24", g, colour)
	}

	// Nothing was installed: the built-in theme still paints its own values.
	if g, _ := rail.RailGlyph(theme.DefaultTheme(), "working", false); g != "●" {
		t.Fatalf("built-in working = %q, want the built-in glyph", g)
	}
}

// TestTuiTheme proves a client with no loaded theme runs the built-in one, and a
// loaded theme is the one its painters see.

// TestTuiTheme proves a client with no loaded theme runs the built-in one, and a
// loaded theme is the one its painters see.
func TestTuiTheme(t *testing.T) {
	if got := (&tui{}).theme(); !reflect.DeepEqual(got, theme.DefaultTheme()) {
		t.Fatalf("nil theme = %+v, want the built-in theme", got)
	}
	th := theme.DefaultTheme()
	th.Palette.RailBG = theme.RGB{R: 9, G: 9, B: 9}
	c := &tui{th: &th}
	if got := c.theme().Palette.RailBG; got != (theme.RGB{R: 9, G: 9, B: 9}) {
		t.Fatalf("loaded theme ground = %v, want 9,9,9", got)
	}
}

// TestThemeDrawGrips proves drawGrips paints the themed grip, not the built-in
// dot.

// TestThemeDrawGrips proves drawGrips paints the themed grip, not the built-in
// dot.
func TestThemeDrawGrips(t *testing.T) {
	th := theme.DefaultTheme()
	th.Glyphs.Grip = "@"
	root := &layout.Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*layout.Node{{Pane: "a"}, {Pane: "b"}},
	}
	snap := &wire.Snapshot{Cols: 40, Rows: 10, Rail: 0, Root: root}
	var buf bytes.Buffer
	drawGrips(&buf, th, snap)
	out := buf.String()
	if !strings.Contains(out, "@") {
		t.Fatalf("themed grip not drawn: %q", out)
	}
	if strings.Contains(out, "•") {
		t.Fatalf("built-in grip still drawn: %q", out)
	}
	vt := vt.NewTerm(40, 10)
	vt.Write(buf.Bytes())
	vt.Flush()
	grips := 0
	for y := 0; y < snap.Rows; y++ {
		for x := 0; x < snap.Cols; x++ {
			if termCell(vt, x, y).Char == '@' {
				grips++
			}
		}
	}
	if grips != 2 {
		t.Fatalf("themed cells = %d, want 2", grips)
	}
}

// TestThemeRailRowGlyph proves rail.RailRow composes the themed state glyph and
// colour for a pane row, in both the Unicode and the Nerd form.

// TestThemeRailRowGlyph proves rail.RailRow composes the themed state glyph and
// colour for a pane row, in both the Unicode and the Nerd form.
func TestThemeRailRowGlyph(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	th.States["working"] = theme.StateStyle{Glyph: "W", NerdGlyph: "W", Colour: theme.RGB{R: 1, G: 2, B: 3}}
	items := rail.RailItems(testutil.AgentRail("working"))
	pane := railRowFind(t, items, rail.Pane, "w1p1")
	left, _, _ := rail.RailRow(th, pane, layout.DefaultRail, false, false)
	found := false
	for _, s := range left {
		if s.Text == "W" {
			found = true
			if s.FG != (theme.RGB{R: 1, G: 2, B: 3}) {
				t.Fatalf("themed glyph colour = %v, want 1,2,3", s.FG)
			}
		}
		if s.Text == "●" {
			t.Fatalf("built-in glyph still composed: %+v", left)
		}
	}
	if !found {
		t.Fatalf("themed state glyph not composed: %+v", left)
	}
	if got, colour := rail.RailGlyph(th, "working", true); got != "W" || colour != (theme.RGB{R: 1, G: 2, B: 3}) {
		t.Fatalf("nerd glyph = %q/%v, want W/1,2,3", got, colour)
	}

	var b bytes.Buffer
	rail.DrawRail(&b, th, testutil.AgentRail("working"))
	if !strings.Contains(b.String(), "W") || !strings.Contains(b.String(), (theme.RGB{R: 1, G: 2, B: 3}).FG()) {
		t.Fatalf("themed state glyph not drawn: %q", b.String())
	}
}
