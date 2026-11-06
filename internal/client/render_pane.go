package client

import (
	"bytes"
	"fmt"
	"strings"

	"dm/internal/layout"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"

	"github.com/hinshun/vt10x"
)

// selRange is a row-major cell range inside one pane's content: lo and hi are
// cell offsets from the pane's top-left, lo <= hi.
type selRange struct{ lo, hi int }

func (s *selRange) has(off int) bool {
	return s != nil && off >= s.lo && off <= s.hi
}

// drawPane paints one pane's cells inside r.
func drawPane(buf *bytes.Buffer, vt vt10x.View, r layout.Rect) {
	drawPaneFrom(buf, vt, r, 0, nil, nil)
}

// drawPaneFrom paints rows [row0, row0+r.H) of vt into r. row0 > 0 shows
// scrollback kept in a taller terminal. Cells inside sel get reverse video on
// top of the pane's own colours; cells inside hover get an underline.
func drawPaneFrom(buf *bytes.Buffer, v vt10x.View, r layout.Rect, row0 int, sel, hover *selRange) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	cols, rows := v.Size()
	v.Lock()
	defer v.Unlock()
	for y := 0; y < r.H; y++ {
		fmt.Fprintf(buf, "\x1b[%d;%dH", r.Y+y+1, r.X+1)
		var cur *vt10x.Glyph
		for x := 0; x < r.W; x++ {
			var g vt10x.Glyph
			if x < cols && row0+y < rows {
				g = vt.NormCell(v.Cell(x, row0+y))
			} else {
				g = vt.NormCell(g)
			}
			hovered := hover.has(y*r.W + x)
			selected := sel.has(y*r.W + x)
			if hovered {
				g.Mode |= vt.AttrUnderline
			}
			buf.WriteString(vt.SGR(g, cur))
			if selected {
				buf.WriteString("\x1b[7m")
			}
			gg := g
			cur = &gg
			if g.Char < 0x20 {
				buf.WriteByte(' ')
			} else {
				buf.WriteRune(g.Char)
			}
			if selected {
				buf.WriteString("\x1b[27m")
			}
		}
	}
}

// drawPaneRow paints one row of a pane's content: the same bytes a full pane
// paint writes for that row.
func drawPaneRow(buf *bytes.Buffer, vt vt10x.View, r layout.Rect, row, row0 int, sel, hover *selRange) {
	drawPaneFrom(buf, vt, layout.Rect{X: r.X, Y: r.Y + row, W: r.W, H: 1}, row0+row, selRow(sel, row, r.W), selRow(hover, row, r.W))
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

// extractSelection reads the selected cells from vt row by row. Trailing spaces
// go, lines join with a newline, and no newline follows the last line.
func extractSelection(v vt10x.View, w int, a, b cell) string {
	if v == nil || w < 1 {
		return ""
	}
	start, end := selOrder(a, b)
	cols, rows := v.Size()
	v.Lock()
	defer v.Unlock()
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
		for x := x0; x <= x1; x++ {
			var g vt10x.Glyph
			if x >= 0 && x < cols && y >= 0 && y < rows {
				g = vt.NormCell(v.Cell(x, y))
			} else {
				g = vt.NormCell(g)
			}
			if g.Char < 0x20 {
				line.WriteByte(' ')
			} else {
				line.WriteRune(g.Char)
			}
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
				fmt.Fprintf(buf, "\x1b[%d;%dH%s", y+1, p.X+1, th.Glyphs.Grip)
				if p.W > 1 {
					fmt.Fprintf(buf, "\x1b[%d;%dH%s", y+1, p.X+2, th.Glyphs.Grip)
				}
			} else {
				x := p.X + p.W/2
				fmt.Fprintf(buf, "\x1b[%d;%dH%s", p.Y+1, x+1, th.Glyphs.Grip)
				if p.H > 1 {
					fmt.Fprintf(buf, "\x1b[%d;%dH%s", p.Y+2, x+1, th.Glyphs.Grip)
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
func contentRows(v vt10x.View) (top, bottom int) {
	cols, rows := v.Size()
	found := false
	for y := 0; y < rows; y++ {
		blank := true
		for x := 0; x < cols; x++ {
			g := v.Cell(x, y)
			if g.Char != 0 && g.Char != ' ' {
				blank = false
				break
			}
		}
		if !blank {
			if !found {
				top = y
				found = true
			}
			bottom = y + 1
		}
	}
	if cur := v.Cursor(); cur.Y+1 > bottom {
		bottom = cur.Y + 1
	}
	if !found && bottom == 0 {
		return 0, 0
	}
	return top, bottom
}

// scrollStart maps a scrollback offset onto the first row to draw and clamps
// the offset to the content that actually exists.
func scrollStart(v vt10x.View, rectH, offset int) (start, max int) {
	top, bottom := contentRows(v)
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

// leafAt returns the leaf pane whose content rect contains x,y.
func leafAt(n *layout.Node, area layout.Rect, x, y int) string {
	if n == nil {
		return ""
	}
	if n.Pane != "" {
		r := layout.InsetBox(area)
		if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
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
