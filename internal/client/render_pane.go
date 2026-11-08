package client

import (
	"bytes"
	"fmt"
	"strings"

	"dm/internal/layout"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

// selRange is a row-major cell range inside one pane's content: lo and hi are
// cell offsets from the pane's top-left, lo <= hi.
type selRange struct{ lo, hi int }

func (s *selRange) has(off int) bool {
	return s != nil && off >= s.lo && off <= s.hi
}

// drawPane paints one pane's cells inside r.
func drawPane(buf *bytes.Buffer, term *vt.Term, r layout.Rect) {
	drawPaneFrom(buf, term, r, 0, nil, nil)
}

// drawPaneFrom paints rows [row0, row0+r.H) of term into r. row0 > 0 shows
// scrollback kept in a taller terminal. Cells inside sel get reverse video on
// top of the pane's own colours; cells inside hover get an underline. A wide
// cluster is written once and the cursor advances over its continuation slot.
func drawPaneFrom(buf *bytes.Buffer, term *vt.Term, r layout.Rect, row0 int, sel, hover *selRange) {
	if term == nil || r.W <= 0 || r.H <= 0 {
		return
	}
	g := term.Grid()
	cols, rows := g.Size()
	blank := vt.NewCell(" ", vt.DefaultStyle())
	for y := 0; y < r.H; y++ {
		fmt.Fprintf(buf, "\x1b[%d;%dH", r.Y+y+1, r.X+1)
		var cur *vt.Style
		for x := 0; x < r.W; {
			c := blank
			row := row0 + y
			next := x + 1
			if x < cols && row >= 0 && row < rows {
				c = g.Cell(x, row)
				if n := g.Next(x, row); n > next {
					next = n
				}
			}
			style := c.Style
			hovered := hover.has(y*r.W + x)
			selected := sel.has(y*r.W + x)
			if hovered && style.Underline == vt.UnderlineNone {
				style.Underline = vt.UnderlineSingle
			}
			buf.WriteString(cellSGR(style, cur))
			curStyle := style
			cur = &curStyle
			if selected {
				buf.WriteString("\x1b[7m")
			}
			content := c.Content
			if content == "" || c.Width == vt.WidthContinuation {
				content = " "
			}
			// Clip the row to the content rect. A cluster whose last cell would
			// fall outside is dropped and the slot it leaves is blanked, so a
			// row never paints past the rect and a wide glyph is never split.
			span := clusterSpan(c)
			if x+span > r.W {
				content = " "
			}
			buf.WriteString(content)
			if selected {
				buf.WriteString("\x1b[27m")
			}
			// A cluster wider than the grid says it is still paints its own
			// cells, so the cursor must step over all of them.
			if adv := x + span; adv > next {
				next = adv
			}
			x = next
		}
	}
}

// clusterSpan returns how many cells c paints on a row: one for a blank or a
// continuation slot, two for a wide cluster. It measures the content itself
// and keeps the larger of that and the stored width, so a width table that
// disagrees with the grid cannot paint a cluster past the rect the caller
// clipped the row to.
func clusterSpan(c vt.Cell) int {
	if c.Content == "" || c.Width == vt.WidthContinuation {
		return 1
	}
	w := int(c.Width)
	if m := vt.ClusterWidth(c.Content); m > w {
		w = m
	}
	if w < 1 {
		return 1
	}
	if w > 2 {
		return 2
	}
	return w
}

// drawPaneRow paints one row of a pane's content: the same bytes a full pane
// paint writes for that row.
func drawPaneRow(buf *bytes.Buffer, term *vt.Term, r layout.Rect, row, row0 int, sel, hover *selRange) {
	drawPaneFrom(buf, term, layout.Rect{X: r.X, Y: r.Y + row, W: r.W, H: 1}, row0+row, selRow(sel, row, r.W), selRow(hover, row, r.W))
}

// cellSGR returns the escape sequence that moves a terminal from cur to st.
// An empty string means st needs no change. The style cache carries the rest.
func cellSGR(st vt.Style, cur *vt.Style) string {
	if cur != nil && *cur == st {
		return ""
	}
	return renderStyle(st).SGR()
}

// renderStyle maps a cell style onto the theme's render style. The attribute
// bits, the underline shapes and the colour kinds share their values, which
// TestCellStyleBitsMatchTheme pins.
func renderStyle(st vt.Style) theme.Style {
	return theme.Style{
		Fg:             renderColor(st.Fg),
		Bg:             renderColor(st.Bg),
		UnderlineColor: renderColor(st.UnderlineColor),
		Underline:      uint8(st.Underline),
		Attrs:          st.Attrs,
	}
}

// renderColor maps a cell colour onto the theme's colour.
func renderColor(c vt.Color) theme.Color {
	return theme.Color{Kind: theme.ColorKind(c.Kind), Index: c.Index, R: c.R, G: c.G, B: c.B}
}

// selRow moves a pane-wide cell range onto one row, nil when the row holds
// none of it.
func selRow(s *selRange, row, w int) *selRange {
	if s == nil {
		return nil
	}
	lo, hi := s.lo-row*w, s.hi-row*w
	if lo < 0 {
		lo = 0
	}
	if hi > w-1 {
		hi = w - 1
	}
	if lo > hi {
		return nil
	}
	return &selRange{lo: lo, hi: hi}
}

// blankRect paints default blanks over a rectangle, so a band that left the
// screen does not keep its glyphs.
func blankRect(buf *bytes.Buffer, r layout.Rect) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	line := "\x1b[0m" + strings.Repeat(" ", r.W)
	for y := r.Y; y < r.Y+r.H; y++ {
		fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, r.X+1)
		buf.WriteString(line)
	}
}

// selOrder returns the two cells in row-major order.
func selOrder(a, b cell) (cell, cell) {
	if a.y > b.y || (a.y == b.y && a.x > b.x) {
		return b, a
	}
	return a, b
}

// extractSelection reads the selected cells from term row by row. Trailing
// spaces go, lines join with a newline, and no newline follows the last line.
func extractSelection(term *vt.Term, w int, a, b cell) string {
	if term == nil || w < 1 {
		return ""
	}
	g := term.Grid()
	cols, rows := g.Size()
	start, end := selOrder(a, b)
	lines := make([]string, 0, end.y-start.y+1)
	for y := start.y; y <= end.y; y++ {
		x0, x1 := 0, w-1
		if y == start.y {
			x0 = start.x
		}
		if y == end.y {
			x1 = end.x
		}
		var line strings.Builder
		for x := x0; x <= x1; {
			var c vt.Cell
			if x >= 0 && x < cols && y >= 0 && y < rows {
				c = g.Cell(x, y)
			}
			next := x + 1
			if x >= 0 && x < cols && y >= 0 && y < rows {
				if n := g.Next(x, y); n > next {
					next = n
				}
			}
			content := c.Content
			if content == "" {
				content = " "
			}
			line.WriteString(content)
			x = next
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
	}
	return strings.Join(lines, "\n")
}

// drawGrips marks every divider at its midpoint, so a handle is visible before
// the pointer finds it. The hover paints over the whole divider. A zoomed pane
// covers every divider, so a grip would be a lie over a hidden split.
func drawGrips(buf *bytes.Buffer, th theme.Theme, snap *wire.Snapshot) {
	if snap == nil || snap.Root == nil || snap.Rail < 0 || snap.Zoom != "" {
		return
	}
	buf.WriteString("\x1b[0m\x1b[90m")
	// A grip is one cell and comes from a pad rect, so clip the move itself: a
	// pad that only overlaps the edge still keeps the glyph that is on screen.
	put := func(x, y int) {
		if x < 0 || x >= snap.Cols || y < 0 || y >= snap.Rows {
			return
		}
		fmt.Fprintf(buf, "\x1b[%d;%dH%s", y+1, x+1, th.Glyphs.Grip)
	}
	var walk func(n *layout.Node, a layout.Rect)
	walk = func(n *layout.Node, a layout.Rect) {
		if n == nil || n.Pane != "" {
			return
		}
		boxes := layout.ChildBoxRects(n, a)
		for i := 0; i+1 < len(boxes); i++ {
			p := layout.PadRect(n, boxes, i)
			if n.Dir == "row" {
				y := p.Y + p.H/2
				put(p.X, y)
				if p.W > 1 {
					put(p.X+1, y)
				}
			} else {
				x := p.X + p.W/2
				put(x, p.Y)
				if p.H > 1 {
					put(x, p.Y+1)
				}
			}
		}
		for i, b := range boxes {
			walk(n.Kids[i], b)
		}
	}
	walk(snap.Root, layout.PaneAreaForWithTabBar(snap.Cols, snap.Rows, snap.Rail))
	buf.WriteString("\x1b[0m")
}

// drawDivider paints the hovered divider: heavy line glyphs over both border
// cells, so the grab target is visible before the press.
func drawDivider(buf *bytes.Buffer, th theme.Theme, z layout.Rect, axis string) {
	if z.W <= 0 || z.H <= 0 {
		return
	}
	glyph := th.Glyphs.DividerCol
	if axis == "col" {
		glyph = th.Glyphs.DividerRow
	}
	buf.WriteString("\x1b[0m\x1b[1;96m")
	line := strings.Repeat(glyph, z.W)
	for y := z.Y; y < z.Y+z.H; y++ {
		fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, z.X+1)
		buf.WriteString(line)
	}
	buf.WriteString("\x1b[0m")
}

// contentRows returns the first row and the row after the last that hold any
// glyph, using the cursor when the screen is blank below the output.
func contentRows(term *vt.Term) (top, bottom int) {
	if term == nil {
		return 0, 0
	}
	g := term.Grid()
	cols, rows := g.Size()
	found := false
	for y := 0; y < rows; y++ {
		blank := true
		for x := 0; x < cols; {
			c := g.Cell(x, y)
			if !c.IsBlank() {
				blank = false
				break
			}
			next := g.Next(x, y)
			if next <= x {
				next = x + 1
			}
			x = next
		}
		if !blank {
			if !found {
				top = y
				found = true
			}
			bottom = y + 1
		}
	}
	if cur := g.Cursor(); cur.Y+1 > bottom {
		bottom = cur.Y + 1
	}
	if !found && bottom == 0 {
		return 0, 0
	}
	return top, bottom
}

// scrollStart maps a scrollback offset onto the first row to draw and clamps
// the offset to the content that actually exists.
func scrollStart(term *vt.Term, rectH, offset int) (start, max int) {
	top, bottom := contentRows(term)
	if bottom == 0 {
		return 0, 0
	}
	if bottom < rectH {
		bottom = rectH
	}
	max = bottom - rectH - top
	if max < 0 {
		max = 0
	}
	if offset > max {
		offset = max
	}
	start = bottom - rectH - offset
	if start < 0 {
		start = 0
	}
	return start, max
}

// padHit reports the divider under x,y: the owning split, the panes on each
// side, the axis, the split's rect and the two-cell divider rect.
func padHit(n *layout.Node, area layout.Rect, x, y int) (split *layout.Node, first, second, axis string, sr, pr layout.Rect, ok bool) {
	if n == nil || n.Pane != "" || len(n.Kids) == 0 {
		return nil, "", "", "", layout.Rect{}, layout.Rect{}, false
	}
	boxes := layout.ChildBoxRects(n, area)
	horizontal := n.Dir == "row"
	for i := range n.Kids {
		if i < len(boxes)-1 {
			p := layout.PadRect(n, boxes, i)
			if x >= p.X && x < p.X+p.W && y >= p.Y && y < p.Y+p.H {
				// Probe one cell inside each box, next to the shared border.
				if horizontal {
					first = leafNear(n.Kids[i], boxes[i], boxes[i].X+boxes[i].W-2, y)
					second = leafNear(n.Kids[i+1], boxes[i+1], boxes[i+1].X+1, y)
				} else {
					first = leafNear(n.Kids[i], boxes[i], x, boxes[i].Y+boxes[i].H-2)
					second = leafNear(n.Kids[i+1], boxes[i+1], x, boxes[i+1].Y+1)
				}
				if first == "" || second == "" {
					return nil, "", "", "", layout.Rect{}, layout.Rect{}, false
				}
				return n, first, second, n.Dir, area, p, true
			}
		}
		if sn, f, sec, ax, sr, pr, hit := padHit(n.Kids[i], boxes[i], x, y); hit {
			return sn, f, sec, ax, sr, pr, hit
		}
	}
	return nil, "", "", "", layout.Rect{}, layout.Rect{}, false
}

// leafAt returns the leaf pane whose box contains x,y. It tests the box, not
// the content rect: its one caller resolves which pane sits on each side of a
// divider, and a divider probe lands on a border cell, which is inside the box
// and outside the content at any padding.
func leafAt(n *layout.Node, area layout.Rect, x, y int) string {
	if n == nil {
		return ""
	}
	if n.Pane != "" {
		if x >= area.X && x < area.X+area.W && y >= area.Y && y < area.Y+area.H {
			return n.Pane
		}
		return ""
	}
	for i, b := range layout.ChildBoxRects(n, area) {
		if x >= b.X && x < b.X+b.W && y >= b.Y && y < b.Y+b.H {
			if id := leafAt(n.Kids[i], b, x, y); id != "" {
				return id
			}
		}
	}
	return ""
}

// leafNear returns the leaf at x,y, or the nearest leaf when the point lands
// on a border inside the subtree. A divider spans the whole split, so its
// pointer can sit exactly over a border of the child's own split; probing the
// exact cell would then find no pane and the divider would be dead.
func leafNear(n *layout.Node, area layout.Rect, x, y int) string {
	offsets := [][2]int{
		{0, 0},
		{-1, 0},
		{1, 0},
		{0, -1},
		{0, 1},
		{-2, 0},
		{2, 0},
		{0, -2},
		{0, 2},
	}
	for _, d := range offsets {
		if id := leafAt(n, area, x+d[0], y+d[1]); id != "" {
			return id
		}
	}
	return ""
}
