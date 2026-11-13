package panel

import (
	"bytes"
	"strings"

	"dm/internal/layout"
	"dm/internal/theme"
)

// PaintShadow paints a bordered box's drop shadow: a column of shadow marks
// beside the box's right border and a row of them under its bottom border.
// rect is the box interior, the rect PaintPanel takes. The shadow is clipped
// to a cols x rows screen, so a box that touches the screen edge loses the
// cells that fall off it. The caller paints the shadow before the box, so
// where the two meet the box's own cells win.
func PaintShadow(buf *bytes.Buffer, th theme.Theme, rect layout.Rect, cols, rows int) {
	g := th.Glyphs.Shadow
	if g == "" || rect.W <= 0 || rect.H <= 0 {
		return
	}
	x0, y0 := rect.X-1, rect.Y-1
	x1, y1 := rect.X+rect.W, rect.Y+rect.H
	put := gridPut(buf, cols, rows)
	buf.WriteString("\x1b[0m\x1b[90m")
	for y := y0; y <= y1; y++ {
		put(x1+1, y, g)
	}
	put(x0+1, y1+1, strings.Repeat(g, rect.W+1))
	buf.WriteString("\x1b[0m")
}
