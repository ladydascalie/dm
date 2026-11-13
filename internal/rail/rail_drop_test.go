package rail

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/theme"
)

// fg0 is the escape segment.Write paints for a foreground RGB: the same
// colours RGB.FG spells, prefixed with the reset the rail rows carry.
func fg0(c theme.RGB) string {
	return "\x1b[0;" + strings.TrimPrefix(c.FG(), "\x1b[")
}

// TestRailDropInsertionLine replaces the drop boundary's row with an
// insertion line in the dragged project's accent, and a drop row past the
// last item changes nothing.
func TestRailDropInsertionLine(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	snap := railRowSnap(layout.DefaultRail)
	items := RailItems(snap)
	dropAt := -1
	for y, it := range items {
		if it.Kind == Window && it.Window == "w2" {
			dropAt = y
			break
		}
	}
	if dropAt < 0 {
		t.Fatal("fixture lost w2's window row")
	}
	var b bytes.Buffer
	DrawRail(&b, th, snap, theme.GlyphUnicode, -1, &Drop{Row: dropAt, Accent: 2})
	if out := b.String(); !strings.Contains(out, th.Accents[2].SGR(false)) {
		t.Fatalf("drop row is not on the accent line: %q", out)
	}
	if n := strings.Count(b.String(), th.Glyphs.BorderHorizontal); n == 0 {
		t.Fatal("the insertion line drew no rule")
	}

	var plain bytes.Buffer
	DrawRail(&plain, th, snap, theme.GlyphUnicode, -1, nil)
	if plain.String() == b.String() {
		t.Fatal("the drop row left no mark")
	}

	var far bytes.Buffer
	DrawRail(&far, th, snap, theme.GlyphUnicode, -1, &Drop{Row: len(items) + 3, Accent: 2})
	if far.String() != plain.String() {
		t.Fatal("an out-of-range drop row changed the paint")
	}
}

// TestRailLiftDimsDraggedRows paints the dragged window's rows dim and on the
// plain rail ground, so the window reads as in the air. The focused pane row
// loses its focus fill while it lifts.
func TestRailLiftDimsDraggedRows(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	snap := railRowSnap(layout.DefaultRail)
	items := RailItems(snap)
	windowAt, focusAt := -1, -1
	w3At := -1
	for y, it := range items {
		if it.Kind == Window && it.Window == "w1" && windowAt < 0 {
			windowAt = y
		}
		if it.Kind == Pane && it.Focus && focusAt < 0 {
			focusAt = y
		}
		if it.Kind == Window && it.Window == "w3" {
			w3At = y
		}
	}
	if windowAt < 0 || focusAt < 0 || w3At < 0 {
		t.Fatalf("fixture lost rows: window %d focus %d w3 %d", windowAt, focusAt, w3At)
	}
	var b bytes.Buffer
	DrawRail(&b, th, snap, theme.GlyphUnicode, -1, &Drop{Row: -1, Lift: "w1"})
	out := b.String()
	windowRow := out[strings.Index(out, fmt.Sprintf("\x1b[%d;1H", windowAt+1)):]
	if !strings.Contains(windowRow[:300], th.Palette.RailDim.SGR(false)) {
		t.Fatalf("the lifted window row is not dim: %q", windowRow[:300])
	}
	if strings.Contains(out, th.Palette.RailFocusBG.BG()) {
		t.Fatal("the lifted focused pane kept its focus fill")
	}

	var plain bytes.Buffer
	DrawRail(&plain, th, snap, theme.GlyphUnicode, -1, nil)
	if plain.String() == b.String() {
		t.Fatal("the lift left no mark")
	}

	// Another window's row keeps its paint while w1 lifts.
	lifted := b.String()
	i := strings.Index(lifted, fmt.Sprintf("\x1b[%d;1H", w3At+1))
	j := strings.Index(plain.String(), fmt.Sprintf("\x1b[%d;1H", w3At+1))
	if i < 0 || j < 0 || lifted[i:i+200] != plain.String()[j:j+200] {
		t.Fatal("a window that is not lifted changed the paint")
	}
}
