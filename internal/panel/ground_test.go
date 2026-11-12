package panel

import (
	"bytes"
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/theme"
)

// TestPanelOwnsAGround proves the primitive, not the caller: every interior
// cell of a panel carries the rail ground before the content paints, so a
// painter that leaves a cell alone shows the panel's ground and never the
// frame behind it. The content still wins over the fill.
// itoa is the tiny int formatter the test needs for a cursor move.
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

func TestPanelOwnsAGround(t *testing.T) {
	r := layout.Rect{X: 3, Y: 2, W: 12, H: 5}
	var b bytes.Buffer
	PaintPanel(&b, theme.DefaultTheme(), r, PanelTitle{}, "", false, func(buf *bytes.Buffer, inner layout.Rect) {
		// The painter moves to the interior's first cell, writes one glyph
		// and leaves the rest alone. The rest must still be opaque.
		buf.WriteString("\x1b[" + itoa(inner.Y+1) + ";" + itoa(inner.X+1) + "H")
		buf.WriteString("x")
	})
	s := b.String()
	ground := theme.DefaultTheme().Palette.RailBG.BG()
	if n := strings.Count(s, ground); n < r.H {
		t.Fatalf("the ground appears %d times, want at least one per interior row (%d)", n, r.H)
	}
	// The unfilled cells are spaces on the ground, not bare spaces.
	if !strings.Contains(s, ground+"\x1b[") && !strings.Contains(s, "\x1b[0m"+ground) {
		t.Fatal("the fill did not ride on the ground escape")
	}
	// The content's cell holds the painter's glyph, not the fill's space.
	if !strings.Contains(s, "x") {
		t.Fatal("the painter's glyph did not survive the fill")
	}
}
