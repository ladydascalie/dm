package rail

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/theme"
)

// TestRailDropMarkerBackground paints the drop boundary's row with the focus
// fill, and a drop row past the last item changes nothing.
func TestRailDropMarkerBackground(t *testing.T) {
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
	DrawRail(&b, th, snap, theme.GlyphUnicode, -1, dropAt)
	rowStart := fmt.Sprintf("\x1b[%d;1H\x1b[0m", dropAt+1) + th.Palette.RailFocusBG.BG()
	if out := b.String(); !strings.Contains(out, rowStart) {
		t.Fatalf("drop row is not on the focus fill: %q", out)
	}

	var plain bytes.Buffer
	DrawRail(&plain, th, snap, theme.GlyphUnicode, -1, -1)
	if plain.String() == b.String() {
		t.Fatal("the drop row left no mark")
	}

	var far bytes.Buffer
	DrawRail(&far, th, snap, theme.GlyphUnicode, -1, len(items)+3)
	if far.String() != plain.String() {
		t.Fatal("an out-of-range drop row changed the paint")
	}
}
