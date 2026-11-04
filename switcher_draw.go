package main

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/hinshun/vt10x"
)

// paletteRect centres the float at 80% by 70%, clamped so its box always fits
// on screen. It moved here from pal.go with the same rule; the client asks for
// the rect it opens the switcher with.
func paletteRect(cols, rows int) Rect {
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
	return Rect{X: (cols - w) / 2, Y: (rows - h) / 2, W: w, H: h}
}

// switcherLayout is the cell geometry of one float: the box, the query row, the
// band of rows under it and, when the float is wide enough, the columns the
// preview takes.
type switcherLayout struct {
	r      Rect // the float's outer rect, the box border included
	inner  Rect // inside the border
	queryY int  // the query row
	bandY  int  // the first row of the list and preview bands
	bandH  int  // how many band rows fit
	listW  int  // the list band's width

	preview bool
	ruleX   int // the vertical rule between the bands
	prevX   int // the preview band's first column
	prevW   int // the preview band's width
}

// switcherLayoutOf measures the float. A switcher opened without a rect takes
// paletteRect, and the rect is clamped to the screen so no row writes off it.
func switcherLayoutOf(s *switcher, snap *Snapshot) switcherLayout {
	r := s.rect
	if r.W <= 0 || r.H <= 0 {
		r = paletteRect(snap.Cols, snap.Rows)
	}
	r = clampRect(r, snap.Cols, snap.Rows)
	s.rect = r
	l := switcherLayout{
		r:      r,
		inner:  Rect{X: r.X + 1, Y: r.Y + 1, W: r.W - 2, H: r.H - 2},
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

// clampRect keeps r inside a cols by rows screen.
func clampRect(r Rect, cols, rows int) Rect {
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

// drawSwitcher writes the whole float into buf and returns the cell the query
// cursor belongs on. The cell is zero based and always inside s.rect. Every row
// the float owns is painted, blanks included, and every row starts with an
// absolute cursor move so bandRows can address it. Nothing is written outside
// the rect and the screen is never cleared.
func drawSwitcher(buf *bytes.Buffer, s *switcher, snap *Snapshot, vts map[string]vt10x.Terminal) (cx, cy int) {
	if buf == nil || s == nil || snap == nil || snap.Cols < 1 || snap.Rows < 1 {
		return 0, 0
	}
	l := switcherLayoutOf(s, snap)
	if l.r.W < 3 || l.r.H < 3 {
		return l.r.X, l.r.Y
	}
	paintPanel(buf, l.inner, panelTitle{}, "", true, func(b *bytes.Buffer, _ Rect) {
		cx, cy = paintSwitcherQuery(b, s, l)
		paintSwitcherBands(b, s, vts, l)
	})
	// The title wears the rail ground, which the panel's label style cannot
	// express, so the switcher paints it on the border row itself.
	paintSwitcherTitle(buf, l)
	return cx, cy
}

// paintSwitcherTitle names the float on its top border row, on the rail ground
// and in the rail text colour.
func paintSwitcherTitle(buf *bytes.Buffer, l switcherLayout) {
	title := " switch "
	if l.r.W-3 < len(title) {
		return
	}
	fmt.Fprintf(buf, "\x1b[%d;%dH", l.r.Y+1, l.r.X+3)
	buf.WriteString("\x1b[0m" + railBG.bg() + railText.fg() + "\x1b[1m")
	buf.WriteString(title)
	buf.WriteString("\x1b[0m")
}

// paintSwitcherQuery paints the query row: "> " then the query, clipped to the
// list band, and returns one past the query's last cell.
func paintSwitcherQuery(buf *bytes.Buffer, s *switcher, l switcherLayout) (cx, cy int) {
	x, y, w := l.inner.X, l.queryY, l.listW
	cy = y
	cx = x
	if w <= 0 {
		fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, x+1)
		buf.WriteString("\x1b[0m" + railBG.bg())
	} else {
		prompt := "> "
		q := clip(string(s.query), w-len(prompt))
		fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, x+1)
		buf.WriteString("\x1b[0m" + railBG.bg() + railDim.fg() + prompt)
		if q != "" {
			buf.WriteString("\x1b[0m" + railBG.bg() + railText.fg() + q)
		}
		used := len(prompt) + cells(q)
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
		buf.WriteString("\x1b[0m" + railBG.bg() + railDim.fg() + glyphBorderVertical)
		fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, l.prevX+1)
		buf.WriteString("\x1b[0m" + railBG.bg() + strings.Repeat(" ", l.prevW) + "\x1b[0m")
	}
	return cx, cy
}

// paintSwitcherBands paints the rows under the query line: the rail tree on the
// left and, when the float is wide enough, the selected pane's screen on the
// right.
func paintSwitcherBands(buf *bytes.Buffer, s *switcher, vts map[string]vt10x.Terminal, l switcherLayout) {
	if l.bandH <= 0 {
		return
	}
	scrollSwitcher(s, l.bandH)
	nerd := nerdFonts()
	for i := 0; i < l.bandH; i++ {
		y := l.bandY + i
		idx := s.top + i
		var left, right, tail []seg
		bg, bold := railBG, false
		if idx >= 0 && idx < len(s.rows) {
			left, right, tail = railRow(s.tree[s.rows[idx]], l.listW, false, nerd)
			if idx == s.sel {
				bg, bold = railFocusBG, true
			}
		}
		paintRailRow(buf, left, tail, right, l.inner.X, y, l.listW, bg, bold)
		if l.preview {
			fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, l.ruleX+1)
			buf.WriteString("\x1b[0m" + railBG.bg() + railDim.fg() + glyphBorderVertical + "\x1b[0m")
		}
	}
	if l.preview {
		paintSwitcherPreview(buf, s, vts, Rect{X: l.prevX, Y: l.bandY, W: l.prevW, H: l.bandH})
	}
}

// paintRailRow paints one composed rail row on its background, filling the whole
// width so no cell of the previous frame shows through.
func paintRailRow(buf *bytes.Buffer, left, tail, right []seg, x, y, w int, bg rgb, bold bool) {
	fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, x+1)
	buf.WriteString("\x1b[0m" + bg.bg())
	for _, seg := range left {
		writeRailSeg(buf, seg, bg, bold)
	}
	for _, seg := range tail {
		writeRailSeg(buf, seg, bg, bold)
	}
	if n := w - segCells(left) - segCells(tail) - segCells(right); n > 0 {
		buf.WriteString(strings.Repeat(" ", n))
	}
	for _, seg := range right {
		writeRailSeg(buf, seg, bg, bold)
	}
	buf.WriteString("\x1b[0m")
}

// scrollSwitcher moves the list's first visible row so the selection stays in
// the band, and never scrolls past the last row.
func scrollSwitcher(s *switcher, h int) {
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
func paintSwitcherPreview(buf *bytes.Buffer, s *switcher, vts map[string]vt10x.Terminal, band Rect) {
	if band.W <= 0 || band.H <= 0 {
		return
	}
	var vt vt10x.Terminal
	var lines []string
	if it, ok := s.selected(); ok && it.kind == railPane {
		vt = vts[it.pane]
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
		paintBlankRows(buf, band, band.Y, start)
		if h > 0 {
			drawPaneFrom(buf, vt, Rect{X: band.X, Y: start, W: band.W, H: h}, row0, nil, nil)
		}
		return
	}
	h := len(lines)
	if h > band.H {
		lines = lines[len(lines)-band.H:]
		h = band.H
	}
	start := band.Y + band.H - h
	paintBlankRows(buf, band, band.Y, start)
	for i := 0; i < h; i++ {
		fmt.Fprintf(buf, "\x1b[%d;%dH", start+i+1, band.X+1)
		buf.WriteString("\x1b[0m" + railBG.bg() + railText.fg())
		text := clip(lines[i], band.W)
		buf.WriteString(text)
		if n := band.W - cells(text); n > 0 {
			buf.WriteString(strings.Repeat(" ", n))
		}
		buf.WriteString("\x1b[0m")
	}
}

// paintBlankRows paints the rows [from, to) of a band on the rail ground.
func paintBlankRows(buf *bytes.Buffer, band Rect, from, to int) {
	for y := from; y < to; y++ {
		fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, band.X+1)
		buf.WriteString("\x1b[0m" + railBG.bg() + strings.Repeat(" ", band.W) + "\x1b[0m")
	}
}
