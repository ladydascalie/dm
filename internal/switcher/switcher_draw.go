package switcher

import (
	"bytes"
	"fmt"
	"strings"

	"dm/internal/cells"
	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/rail"
	"dm/internal/segment"
	"dm/internal/theme"
	"dm/internal/wire"

	"github.com/hinshun/vt10x"
)

// PanePainter paints rows [row0, row0+r.H) of a pane's terminal into r. The
// switcher owns the float, not the pane painter, so the client supplies it.
type PanePainter func(buf *bytes.Buffer, vt vt10x.Terminal, r layout.Rect, row0 int)

// PaletteRect centres the float at 80% by 70%, clamped so its box always fits
// on screen. It moved here from pal.go with the same rule; the client asks for
// the rect it opens the switcher with.
func PaletteRect(cols, rows int) layout.Rect {
	w, h := cols*8/10, rows*7/10
	if w > 132 {
		w = 132
	}
	if h > 40 {
		h = 40
	}
	if w > cols-4 {
		w = cols - 4
	}
	if h > rows-3 {
		h = rows - 3
	}
	if w < 20 {
		w = 20
	}
	if h < 6 {
		h = 6
	}
	if w > cols {
		w = cols
	}
	if h > rows {
		h = rows
	}
	return layout.Rect{X: (cols - w) / 2, Y: (rows - h) / 2, W: w, H: h}
}

// switcherLayout is the cell geometry of one float: the box, the query row, the
// band of rows under it and, when the float is wide enough, the columns the
// preview takes.
type switcherLayout struct {
	r      layout.Rect // the float's outer rect, the box border included
	inner  layout.Rect // inside the border
	queryY int         // the query row
	bandY  int         // the first row of the list and preview bands
	bandH  int         // how many band rows fit
	listW  int         // the list band's width

	preview bool
	ruleX   int // the vertical rule between the bands
	prevX   int // the preview band's first column
	prevW   int // the preview band's width
}

// switcherLayoutOf measures the float. A switcher opened without a rect takes
// PaletteRect, and the rect is clamped to the screen so no row writes off it.
func switcherLayoutOf(s *Switcher, snap *wire.Snapshot) switcherLayout {
	r := s.Rect
	if r.W <= 0 || r.H <= 0 {
		r = PaletteRect(snap.Cols, snap.Rows)
	}
	r = ClampRect(r, snap.Cols, snap.Rows)
	s.Rect = r
	l := switcherLayout{
		r:      r,
		inner:  layout.Rect{X: r.X + 1, Y: r.Y + 1, W: r.W - 2, H: r.H - 2},
		queryY: r.Y + 1,
		bandY:  r.Y + 2,
		bandH:  r.H - 3,
		listW:  r.W - 2,
	}
	if r.W >= 64 && r.H >= 12 {
		pw := (r.W - 2) / 2
		lw := r.W - 2 - pw - 1
		if pw >= 4 && lw >= 4 {
			l.preview = true
			l.prevW = pw
			l.listW = lw
			l.ruleX = r.X + 1 + lw
			l.prevX = l.ruleX + 1
		}
	}
	return l
}

// ClampRect keeps r inside a cols by rows screen.
func ClampRect(r layout.Rect, cols, rows int) layout.Rect {
	if r.W > cols {
		r.W = cols
	}
	if r.H > rows {
		r.H = rows
	}
	if r.W < 0 {
		r.W = 0
	}
	if r.H < 0 {
		r.H = 0
	}
	if r.X < 0 {
		r.X = 0
	}
	if r.Y < 0 {
		r.Y = 0
	}
	if r.X+r.W > cols {
		r.X = cols - r.W
	}
	if r.Y+r.H > rows {
		r.Y = rows - r.H
	}
	if r.X < 0 {
		r.X = 0
	}
	if r.Y < 0 {
		r.Y = 0
	}
	return r
}

// DrawSwitcher writes the whole float into buf and returns the cell the query
// cursor belongs on. The cell is zero based and always inside s.Rect. Every row
// the float owns is painted, blanks included, and every row starts with an
// absolute cursor move so bandRows can address it. Nothing is written outside
// the rect and the screen is never cleared.
func DrawSwitcher(buf *bytes.Buffer, th theme.Theme, s *Switcher, snap *wire.Snapshot, vts map[string]vt10x.Terminal, paint PanePainter) (cx, cy int) {
	if buf == nil || s == nil || snap == nil || snap.Cols < 1 || snap.Rows < 1 {
		return 0, 0
	}
	l := switcherLayoutOf(s, snap)
	if l.r.W < 3 || l.r.H < 3 {
		return l.r.X, l.r.Y
	}
	panel.PaintPanel(buf, th, l.inner, panel.PanelTitle{}, "", true, func(b *bytes.Buffer, _ layout.Rect) {
		cx, cy = paintSwitcherQuery(b, th, s, l)
		paintSwitcherBands(b, th, s, vts, l, paint)
	})
	// The title wears the rail ground, which the panel's label style cannot
	// express, so the switcher paints it on the border row itself.
	paintSwitcherTitle(buf, th, l)
	return cx, cy
}

// paintSwitcherTitle names the float on its top border row, on the rail ground
// and in the rail text colour.
func paintSwitcherTitle(buf *bytes.Buffer, th theme.Theme, l switcherLayout) {
	title := " switch "
	if l.r.W-3 < len(title) {
		return
	}
	fmt.Fprintf(buf, "\x1b[%d;%dH", l.r.Y+1, l.r.X+3)
	buf.WriteString("\x1b[0m" + th.Palette.RailBG.BG() + th.Palette.RailText.FG() + "\x1b[1m")
	buf.WriteString(title)
	buf.WriteString("\x1b[0m")
}

// paintSwitcherQuery paints the query row: "> " then the query, clipped to the
// list band, and returns one past the query's last cell.
func paintSwitcherQuery(buf *bytes.Buffer, th theme.Theme, s *Switcher, l switcherLayout) (cx, cy int) {
	ground, dim, text := th.Palette.RailBG, th.Palette.RailDim, th.Palette.RailText
	x, y, w := l.inner.X, l.queryY, l.listW
	cy = y
	cx = x
	if w <= 0 {
		fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, x+1)
		buf.WriteString("\x1b[0m" + ground.BG())
	} else {
		prompt := "> "
		q := cells.Clip(string(s.query), w-len(prompt))
		fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, x+1)
		buf.WriteString("\x1b[0m" + ground.BG() + dim.FG() + prompt)
		if q != "" {
			buf.WriteString("\x1b[0m" + ground.BG() + text.FG() + q)
		}
		used := len(prompt) + cells.Cells(q)
		if n := w - used; n > 0 {
			buf.WriteString(strings.Repeat(" ", n))
		}
		buf.WriteString("\x1b[0m")
		cx = x + used
	}
	// The cell is one past the query's last cell, clamped inside the row.
	maxX := x + l.listW - 1
	if cx > maxX {
		cx = maxX
	}
	if cx < x {
		cx = x
	}
	// The columns right of the list still belong to this row, so paint them:
	// the rule keeps the split visible and the preview band stays blank.
	if l.preview {
		fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, l.ruleX+1)
		buf.WriteString("\x1b[0m" + ground.BG() + dim.FG() + th.Glyphs.BorderVertical)
		fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, l.prevX+1)
		buf.WriteString("\x1b[0m" + ground.BG() + strings.Repeat(" ", l.prevW) + "\x1b[0m")
	}
	return cx, cy
}

// paintSwitcherBands paints the rows under the query line: the rail tree on the
// left and, when the float is wide enough, the selected pane's screen on the
// right.
func paintSwitcherBands(buf *bytes.Buffer, th theme.Theme, s *Switcher, vts map[string]vt10x.Terminal, l switcherLayout, paint PanePainter) {
	if l.bandH <= 0 {
		return
	}
	p := th.Palette
	scrollSwitcher(s, l.bandH)
	nerd := rail.NerdFonts()
	for i := 0; i < l.bandH; i++ {
		y := l.bandY + i
		idx := s.top + i
		var left, right, tail []segment.Seg
		bg, bold := p.RailBG, false
		if idx >= 0 && idx < len(s.rows) {
			left, right, tail = rail.RailRow(th, s.tree[s.rows[idx]], l.listW, false, nerd)
			if idx == s.sel {
				bg, bold = p.RailFocusBG, true
			}
		}
		paintRailRow(buf, left, tail, right, l.inner.X, y, l.listW, bg, bold)
		if l.preview {
			fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, l.ruleX+1)
			buf.WriteString("\x1b[0m" + p.RailBG.BG() + p.RailDim.FG() + th.Glyphs.BorderVertical + "\x1b[0m")
		}
	}
	if l.preview {
		paintSwitcherPreview(buf, th, s, vts, layout.Rect{X: l.prevX, Y: l.bandY, W: l.prevW, H: l.bandH}, paint)
	}
}

// paintRailRow paints one composed rail row on its background, filling the whole
// width so no cell of the previous frame shows through.
func paintRailRow(buf *bytes.Buffer, left, tail, right []segment.Seg, x, y, w int, bg theme.RGB, bold bool) {
	fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, x+1)
	buf.WriteString("\x1b[0m" + bg.BG())
	for _, s := range left {
		rail.WriteRailSeg(buf, s, bg, bold)
	}
	for _, s := range tail {
		rail.WriteRailSeg(buf, s, bg, bold)
	}
	if n := w - segment.SegCells(left) - segment.SegCells(tail) - segment.SegCells(right); n > 0 {
		buf.WriteString(strings.Repeat(" ", n))
	}
	for _, s := range right {
		rail.WriteRailSeg(buf, s, bg, bold)
	}
	buf.WriteString("\x1b[0m")
}

// scrollSwitcher moves the list's first visible row so the selection stays in
// the band, and never scrolls past the last row.
func scrollSwitcher(s *Switcher, h int) {
	if h <= 0 || len(s.rows) == 0 {
		s.top = 0
		return
	}
	if s.sel >= 0 {
		if s.sel < s.top {
			s.top = s.sel
		}
		if s.sel >= s.top+h {
			s.top = s.sel - h + 1
		}
	}
	max := len(s.rows) - h
	if max < 0 {
		max = 0
	}
	if s.top > max {
		s.top = max
	}
	if s.top < 0 {
		s.top = 0
	}
}

// paintSwitcherPreview paints the selected pane's screen into the band, bottom
// aligned. A pane with a live terminal paints from it; otherwise the lines of
// the model's read reply are used. No data at all leaves the band blank.
func paintSwitcherPreview(buf *bytes.Buffer, th theme.Theme, s *Switcher, vts map[string]vt10x.Terminal, band layout.Rect, paint PanePainter) {
	if band.W <= 0 || band.H <= 0 {
		return
	}
	var vt vt10x.Terminal
	var lines []string
	if it, ok := s.Selected(); ok && it.Kind == rail.Pane {
		vt = vts[it.Pane]
		lines = s.lines
	}
	if vt != nil {
		_, rows := vt.Size()
		h, row0 := band.H, 0
		if rows < h {
			h = rows
		}
		if rows > band.H {
			row0 = rows - band.H
		}
		start := band.Y + band.H - h
		paintBlankRows(buf, th, band, band.Y, start)
		if h > 0 && paint != nil {
			paint(buf, vt, layout.Rect{X: band.X, Y: start, W: band.W, H: h}, row0)
		}
		return
	}
	h := len(lines)
	if h > band.H {
		lines = lines[len(lines)-band.H:]
		h = band.H
	}
	start := band.Y + band.H - h
	paintBlankRows(buf, th, band, band.Y, start)
	for i := 0; i < h; i++ {
		fmt.Fprintf(buf, "\x1b[%d;%dH", start+i+1, band.X+1)
		buf.WriteString("\x1b[0m" + th.Palette.RailBG.BG() + th.Palette.RailText.FG())
		text := cells.Clip(lines[i], band.W)
		buf.WriteString(text)
		if n := band.W - cells.Cells(text); n > 0 {
			buf.WriteString(strings.Repeat(" ", n))
		}
		buf.WriteString("\x1b[0m")
	}
}

// paintBlankRows paints the rows [from, to) of a band on the rail ground.
func paintBlankRows(buf *bytes.Buffer, th theme.Theme, band layout.Rect, from, to int) {
	for y := from; y < to; y++ {
		fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, band.X+1)
		buf.WriteString("\x1b[0m" + th.Palette.RailBG.BG() + strings.Repeat(" ", band.W) + "\x1b[0m")
	}
}
