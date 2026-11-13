package panel

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/theme"
)

// shadowCell is one glyph PaintShadow placed, read back from the escape stream.
type shadowCell struct {
	row, col int
	glyph    string
}

// shadowCells reads every cursor move and the run that follows it into cells.
// PaintShadow only places one-cell glyphs, so a run is that glyph repeated.
var shadowCells = regexp.MustCompile(`\x1b\[(\d+);(\d+)H([^\x1b]+)`)

func readShadowCells(out string) []shadowCell {
	var cells []shadowCell
	for _, m := range shadowCells.FindAllStringSubmatch(out, -1) {
		row, col := atoi(m[1]), atoi(m[2])
		n := 0
		for _, r := range m[3] {
			cells = append(cells, shadowCell{row, col + n, string(r)})
			n++
		}
	}
	return cells
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

// shadowAt finds the glyph painted at row, col, one based both ways.
func shadowAt(cells []shadowCell, row, col int) (string, bool) {
	for _, c := range cells {
		if c.row == row && c.col == col {
			return c.glyph, true
		}
	}
	return "", false
}

// TestPaintShadow paints the shadow: a mark beside every border row and a row
// of marks under the box that reaches the right shadow column, so the L
// connects, while the cell under the bottom-left border corner stays clear.
func TestPaintShadow(t *testing.T) {
	th := theme.DefaultTheme()
	rect := layout.Rect{X: 2, Y: 2, W: 4, H: 2}
	var buf bytes.Buffer
	PaintShadow(&buf, th, rect, 80, 24)
	if !strings.HasPrefix(buf.String(), "\x1b[0m\x1b[90m") {
		t.Fatalf("shadow is not dim: %q", buf.String())
	}
	if !strings.HasSuffix(buf.String(), "\x1b[0m") {
		t.Fatalf("shadow does not reset its style: %q", buf.String())
	}
	cells := readShadowCells(buf.String())
	// The border rows run 1-based rows 2..5 and the border columns 2..7.
	for y := 2; y <= 5; y++ {
		if g, ok := shadowAt(cells, y, 8); !ok || g != "░" {
			t.Fatalf("shadow beside the right border at row %d = %q, %v", y, g, ok)
		}
	}
	for x := 3; x <= 8; x++ {
		if g, ok := shadowAt(cells, 6, x); !ok || g != "░" {
			t.Fatalf("shadow under the box at col %d = %q, %v", x, g, ok)
		}
	}
	// The run reaches the right shadow column, so the L connects, but the
	// cell under the bottom-left border corner stays clear.
	if _, ok := shadowAt(cells, 6, 2); ok {
		t.Fatal("shadow painted the cell under the bottom-left corner")
	}
	if _, ok := shadowAt(cells, 2, 2); ok {
		t.Fatal("shadow painted a border cell")
	}
}

// TestPaintShadowClips proves a box that touches the screen edge loses the
// shadow cells that fall off it and paints nothing outside the screen.
func TestPaintShadowClips(t *testing.T) {
	th := theme.DefaultTheme()
	rect := layout.Rect{X: 2, Y: 2, W: 4, H: 2}
	var buf bytes.Buffer
	// The shadow column is x 7 and the shadow row is y 5: a screen that ends
	// before either keeps no shadow at all.
	PaintShadow(&buf, th, rect, 7, 4)
	if cells := readShadowCells(buf.String()); len(cells) != 0 {
		t.Fatalf("shadow escaped a 7x4 screen: %+v", cells)
	}
	// One wider and one taller keeps the shadow whole.
	buf.Reset()
	PaintShadow(&buf, th, rect, 8, 5)
	if cells := readShadowCells(buf.String()); len(cells) == 0 {
		t.Fatal("shadow clipped on a screen that fits it")
	}
}

// TestPaintShadowNoGlyph proves a theme without a shadow glyph paints nothing.
func TestPaintShadowNoGlyph(t *testing.T) {
	th := theme.DefaultTheme()
	th.Glyphs.Shadow = ""
	var buf bytes.Buffer
	PaintShadow(&buf, th, layout.Rect{X: 2, Y: 2, W: 4, H: 2}, 80, 24)
	if buf.Len() != 0 {
		t.Fatalf("no glyph painted %q", buf.String())
	}
}
