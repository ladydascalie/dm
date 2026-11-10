package switcher

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"dm/internal/cells"
	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/rail"
	"dm/internal/segment"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

// PanePainter paints rows [row0, row0+r.H) of a pane's terminal into r. The
// switcher owns the float, not the pane painter, so the client supplies it.
type PanePainter func(buf *bytes.Buffer, term *vt.Term, r layout.Rect, row0 int)

// PaletteRect centres the float at 80% by 70%, clamped so its box always fits
// on screen. It moved here from pal.go with the same rule; a caller that does
// not own the whole screen asks for the rect it opens the switcher with.
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

// switcherLayout is the cell geometry of one float: the box, the query row, the
// column header, the body, the footer hint row and, when the float is wide
// enough, the columns the preview takes on the right of the split.
type switcherLayout struct {
	r      layout.Rect // the float's outer rect, the box border included
	inner  layout.Rect // inside the border
	queryY int         // the query row
	headY  int         // the column names
	bodyY  int         // the first table row
	bodyH  int         // how many table rows fit
	footY  int         // the footer hint row, -1 when the float has no room
	listW  int         // the table's width

	preview bool
	ruleX   int // the vertical rule between the table and the preview
	prevX   int // the preview band's first column
	prevW   int // the preview band's width
}

// hasRow reports whether a row falls inside the inner rect.
func (l switcherLayout) hasRow(y int) bool {
	return y >= l.inner.Y && y < l.inner.Y+l.inner.H
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
		headY:  r.Y + 2,
		bodyY:  r.Y + 3,
		footY:  r.Y + r.H - 2,
		listW:  r.W - 2,
	}
	// A float too short for the footer gives its last inner row to the body.
	if l.inner.H < 3 || l.footY >= l.inner.Y+l.inner.H {
		l.footY = -1
	}
	if l.footY >= 0 {
		l.bodyH = l.footY - l.bodyY
		if l.bodyH < 0 {
			l.bodyH = 0
		}
	}
	if s.PreviewOn() {
		prevW := l.inner.W * s.ratio / 100
		listW := l.inner.W - prevW - 1
		if listW < 12 {
			// A table narrower than this cannot name its columns, so the
			// preview gives way instead of eating it.
			listW = 12
			prevW = l.inner.W - listW - 1
		}
		if prevW >= 4 && listW >= 4 {
			l.preview = true
			l.listW = listW
			l.prevW = prevW
			l.ruleX = l.inner.X + listW
			l.prevX = l.ruleX + 1
		}
	}
	return l
}

// switcherHead names the columns, in the table's order.
var switcherHead = [7]string{"#", "proj", "agent", "git", "status", "time", "title"}

// switcherColumnFloor is the least width a column keeps while the table shrinks.
// switcherColumnFloor keeps the status word whole: a table that elides the
// state has lost the one thing the dashboard exists to show.
var switcherColumnFloor = [6]int{1, 4, 4, 4, 9, 4}

// switcherTitleFloor is the least room the title column keeps while the others
// shrink, so a narrow table still shows part of a name.
const switcherTitleFloor = 8

// switcherColumns returns the cell width of each column for a table w cells
// wide: the jump digit, project, agent, git, status, time and title. The title
// takes whatever the others leave; when they do not fit, the widest column
// gives a cell up until they do, and the title never drops under one mark.
func switcherColumns(w int) [7]int {
	out := [6]int{1, 10, 14, 8, 10, 4}
	for sum6(out)+len(out)+switcherTitleFloor > w {
		idx := -1
		for i := 0; i < len(out); i++ {
			if out[i] <= switcherColumnFloor[i] {
				continue
			}
			if idx < 0 || out[i]-switcherColumnFloor[i] >= out[idx]-switcherColumnFloor[idx] {
				idx = i
			}
		}
		if idx < 0 {
			for i := 0; i < len(out); i++ {
				if out[i] > 1 && (idx < 0 || out[i] > out[idx]) {
					idx = i
				}
			}
		}
		if idx < 0 {
			break
		}
		out[idx]--
	}
	title := w - sum6(out) - len(out)
	if title < 1 {
		title = 1
	}
	return [7]int{out[0], out[1], out[2], out[3], out[4], out[5], title}
}

// sum6 is the cell width of the six fixed columns.
func sum6(c [6]int) int {
	n := 0
	for _, v := range c {
		n += v
	}
	return n
}

// DrawSwitcher writes the whole float into buf and returns the cell the query
// cursor belongs on. The cell is zero based and always inside s.Rect. Every row
// the float owns is painted, blanks included, and every row starts with an
// absolute cursor move so bandRows can address it. Nothing is written outside
// the rect and the screen is never cleared.
func DrawSwitcher(buf *bytes.Buffer, th theme.Theme, s *Switcher, snap *wire.Snapshot, vts map[string]*vt.Term, paint PanePainter, set theme.GlyphSet) (cx, cy int) {
	if buf == nil || s == nil || snap == nil || snap.Cols < 1 || snap.Rows < 1 {
		return 0, 0
	}
	th = th.For(set)
	l := switcherLayoutOf(s, snap)
	if l.r.W < 3 || l.r.H < 3 {
		return l.r.X, l.r.Y
	}
	cols := switcherColumns(l.listW)
	panel.PaintPanel(buf, th, l.inner, panel.PanelTitle{}, "", true, func(b *bytes.Buffer, _ layout.Rect) {
		cx, cy = paintSwitcherQuery(b, th, s, l)
		paintSwitcherHead(b, th, l, cols)
		paintSwitcherBody(b, th, s, vts, l, cols, paint, set)
		paintSwitcherFoot(b, th, s, l)
	})
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
	buf.WriteString("\x1b[0m")
	buf.WriteString(th.Palette.RailBG.BG())
	buf.WriteString(th.Palette.RailText.FG())
	buf.WriteString("\x1b[1m")
	buf.WriteString(title)
	buf.WriteString("\x1b[0m")
}

// switcherIdleHint is the query row's text outside filter mode. The row keeps
// its place so the table never moves, and the hint says how to start typing.
const switcherIdleHint = "press / to filter"

// paintSwitcherQuery paints the query row: "> " then the query while filter mode
// is on, a prompt otherwise, both clipped to the table. It returns one past the
// query's last cell.
func paintSwitcherQuery(buf *bytes.Buffer, th theme.Theme, s *Switcher, l switcherLayout) (cx, cy int) {
	if !l.hasRow(l.queryY) {
		return l.inner.X, l.inner.Y
	}
	ground, dim, text := th.Palette.RailBG, th.Palette.RailDim, th.Palette.RailText
	x, y, w := l.inner.X, l.queryY, l.listW
	cy = y
	cx = x
	if w <= 0 {
		fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, x+1)
		buf.WriteString("\x1b[0m")
		buf.WriteString(ground.BG())
	} else {
		prompt := "> "
		if !s.Filtering() {
			hint := cells.Clip(switcherIdleHint, w)
			fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, x+1)
			buf.WriteString("\x1b[0m")
			buf.WriteString(ground.BG())
			buf.WriteString(dim.FG())
			buf.WriteString(hint)
			if n := w - cells.Cells(hint); n > 0 {
				buf.WriteString(strings.Repeat(" ", n))
			}
			buf.WriteString("\x1b[0m")
			if l.preview {
				paintRuleCell(buf, th, l.ruleX, y)
				paintSwitcherBlankRow(buf, th, y, l.prevX, l.prevW)
			}
			return x, cy
		}
		q := cells.Clip(string(s.query), w-len(prompt))
		fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, x+1)
		buf.WriteString("\x1b[0m")
		buf.WriteString(ground.BG())
		buf.WriteString(dim.FG())
		buf.WriteString(prompt)
		if q != "" {
			buf.WriteString("\x1b[0m")
			buf.WriteString(ground.BG())
			buf.WriteString(text.FG())
			buf.WriteString(q)
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
	// The columns right of the table still belong to this row, so paint them:
	// the rule keeps the split visible and the preview stays blank.
	if l.preview {
		paintRuleCell(buf, th, l.ruleX, y)
		fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, l.prevX+1)
		buf.WriteString("\x1b[0m")
		buf.WriteString(ground.BG())
		buf.WriteString(strings.Repeat(" ", l.prevW))
		buf.WriteString("\x1b[0m")
	}
	return cx, cy
}

// paintSwitcherHead paints the column names under the query row.
func paintSwitcherHead(buf *bytes.Buffer, th theme.Theme, l switcherLayout, cols [7]int) {
	if !l.hasRow(l.headY) {
		return
	}
	ground := th.Palette.RailBG
	fmt.Fprintf(buf, "\x1b[%d;%dH", l.headY+1, l.inner.X+1)
	buf.WriteString("\x1b[0m")
	buf.WriteString(ground.BG())
	used := 0
	for i, name := range switcherHead {
		if i > 0 {
			buf.WriteString(" ")
			used++
		}
		runs := switcherFit(th, []segment.Seg{{Text: name, FG: th.Palette.RailDim}}, cols[i])
		writeSwitcherRuns(buf, runs, ground, true)
		used += segment.SegCells(runs)
		if n := cols[i] - segment.SegCells(runs); n > 0 {
			buf.WriteString(strings.Repeat(" ", n))
			used += n
		}
	}
	if n := l.listW - used; n > 0 {
		buf.WriteString(strings.Repeat(" ", n))
	}
	buf.WriteString("\x1b[0m")
	if l.preview {
		paintRuleCell(buf, th, l.ruleX, l.headY)
		paintSwitcherBlankRow(buf, th, l.headY, l.prevX, l.prevW)
	}
}

// paintSwitcherBody paints the table rows and, on the right of the split, the
// selected pane's screen.
func paintSwitcherBody(buf *bytes.Buffer, th theme.Theme, s *Switcher, vts map[string]*vt.Term, l switcherLayout, cols [7]int, paint PanePainter, set theme.GlyphSet) {
	if l.bodyH > 0 {
		s.scrollTo(l.bodyH)
	}
	now := switcherNow().Unix()
	for i := 0; i < l.bodyH; i++ {
		y := l.bodyY + i
		idx := s.top + i
		if idx >= 0 && idx < len(s.rows) {
			selected := idx == s.sel
			paintSwitcherRow(buf, th, l, cols, s.rows[idx], idx+1, selected, set, now, y)
		} else {
			paintSwitcherBlankRow(buf, th, y, l.inner.X, l.listW)
		}
		if l.preview {
			paintRuleCell(buf, th, l.ruleX, y)
		}
	}
	if l.preview {
		paintSwitcherPreview(buf, th, s, vts, layout.Rect{X: l.prevX, Y: l.bodyY, W: l.prevW, H: l.bodyH}, paint)
	}
}

// paintSwitcherRow paints one pane row: the jump key, project, agent label, git
// state, status, age and title, each elided to its column. The selected row
// wears the focus ground across the whole table.
func paintSwitcherRow(buf *bytes.Buffer, th theme.Theme, l switcherLayout, cols [7]int, p wire.AllPane, jump int, selected bool, set theme.GlyphSet, now int64, y int) {
	ground := th.Palette.RailBG
	if selected {
		ground = th.Palette.RailFocusBG
	}
	cellsOf := switcherRowCells(th, set, p, jump, now)
	fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, l.inner.X+1)
	buf.WriteString("\x1b[0m")
	buf.WriteString(ground.BG())
	used := 0
	for i := 0; i < len(cols); i++ {
		if i > 0 {
			buf.WriteString(" ")
			used++
		}
		runs := switcherFit(th, cellsOf[i], cols[i])
		writeSwitcherRuns(buf, runs, ground, selected)
		used += segment.SegCells(runs)
		if n := cols[i] - segment.SegCells(runs); n > 0 {
			buf.WriteString(strings.Repeat(" ", n))
			used += n
		}
	}
	if n := l.listW - used; n > 0 {
		buf.WriteString(strings.Repeat(" ", n))
	}
	buf.WriteString("\x1b[0m")
}

// switcherRowCells composes the seven column runs of one pane row.
func switcherRowCells(th theme.Theme, set theme.GlyphSet, p wire.AllPane, jump int, now int64) [7][]segment.Seg {
	pal := th.Palette
	var out [7][]segment.Seg
	jumpText := ""
	if jump >= 1 && jump <= 9 {
		jumpText = strconv.Itoa(jump)
	}
	out[0] = []segment.Seg{{Text: jumpText, FG: pal.RailDim}}
	project, projectFG := "-", pal.RailDim
	if p.Project != "" {
		project, projectFG = p.Project, th.Accents[rail.RailAccentSlot(p.Project)]
	}
	out[1] = []segment.Seg{{Text: project, FG: projectFG}}
	out[2] = []segment.Seg{{Text: paneLabel(p), FG: pal.RailText}}
	out[3] = paneGit(th, p)
	state := paneState(p)
	style := rail.StateStyle(th, state).For(set)
	out[4] = []segment.Seg{{Text: style.Glyph + " " + state, FG: style.Colour}}
	out[5] = []segment.Seg{{Text: switcherClock(p.StatusChangedAt, now), FG: pal.RailDim}}
	out[6] = []segment.Seg{{Text: p.Title, FG: pal.RailDim}}
	return out
}

// paneGit is the git cell: the lines the tree gains and loses, "clean" when it
// has none, and a dash outside a repository.
func paneGit(th theme.Theme, p wire.AllPane) []segment.Seg {
	pal := th.Palette
	if !p.InRepo {
		return []segment.Seg{{Text: "-", FG: pal.RailDim}}
	}
	var out []segment.Seg
	if p.Added != 0 {
		out = append(out, segment.Seg{Text: "+" + strconv.Itoa(p.Added), FG: pal.RailDiffAdd})
	}
	if p.Deleted != 0 {
		if len(out) > 0 {
			out = append(out, segment.Seg{Text: " ", FG: pal.RailDim})
		}
		out = append(out, segment.Seg{Text: "-" + strconv.Itoa(p.Deleted), FG: pal.RailDiffDel})
	}
	if len(out) == 0 {
		return []segment.Seg{{Text: "clean", FG: pal.RailDim}}
	}
	return out
}

// switcherFit cuts a column's runs to its width, with the elision mark only
// when they do not fit.
func switcherFit(th theme.Theme, runs []segment.Seg, w int) []segment.Seg {
	if segment.SegCells(runs) <= w {
		return runs
	}
	return segment.ElideSegs(th, runs, w)
}

// writeSwitcherRuns paints a row's runs on its ground, in the row's weight.
func writeSwitcherRuns(buf *bytes.Buffer, runs []segment.Seg, ground theme.RGB, bold bool) {
	for _, seg := range runs {
		seg.BG = ground
		seg.Bold = seg.Bold || bold
		segment.Write(buf, seg)
	}
}

// paintSwitcherBlankRow paints one table row on the rail ground.
func paintSwitcherBlankRow(buf *bytes.Buffer, th theme.Theme, y, x, w int) {
	if w <= 0 {
		return
	}
	fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, x+1)
	buf.WriteString("\x1b[0m")
	buf.WriteString(th.Palette.RailBG.BG())
	buf.WriteString(strings.Repeat(" ", w))
	buf.WriteString("\x1b[0m")
}

// paintRuleCell paints one cell of the vertical rule between the table and the
// preview.
func paintRuleCell(buf *bytes.Buffer, th theme.Theme, x, y int) {
	fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, x+1)
	buf.WriteString("\x1b[0m")
	buf.WriteString(th.Palette.RailBG.BG())
	buf.WriteString(th.Palette.RailDim.FG())
	buf.WriteString(th.Glyphs.BorderVertical)
	buf.WriteString("\x1b[0m")
}

// switcherHints is the footer's key hints in the order they are worth cells.
// A narrow float keeps the head of the list and drops the rest whole, so it
// loses the scroll reminder before it loses the jump keys.
var switcherHints = []string{
	"enter pick", "esc close", "1-9 jump", "i copy id", "d diff",
	"/ filter", "s sort", "p peek", "+/- size", "^u/^d scroll",
}

// switcherHint fits the hints into room cells and drops the ones that do not
// fit. It never ends on half a hint: a key that does not fit is not named.
func switcherHint(room int) string {
	kept := make([]string, 0, len(switcherHints))
	used := 0
	for _, hint := range switcherHints {
		need := cells.Cells(hint)
		if len(kept) > 0 {
			need += 2
		}
		if used+need > room {
			break
		}
		kept = append(kept, hint)
		used += need
	}
	return strings.Join(kept, "  ")
}

// paintSwitcherFoot paints the hint row at the bottom of the box: the keys on
// the left and the ordering with the preview's share on the right.
func paintSwitcherFoot(buf *bytes.Buffer, th theme.Theme, s *Switcher, l switcherLayout) {
	if l.footY <= l.queryY || !l.hasRow(l.footY) || l.inner.W <= 0 {
		return
	}
	right := s.mode.String()
	if s.PreviewOn() {
		right += " " + strconv.Itoa(s.ratio) + "%"
	}
	room := l.inner.W - cells.Cells(right) - 2
	if room < 0 {
		right, room = "", l.inner.W
	}
	left := switcherHint(room)
	pad := l.inner.W - cells.Cells(left) - cells.Cells(right)
	if pad < 0 {
		pad = 0
	}
	fmt.Fprintf(buf, "\x1b[%d;%dH", l.footY+1, l.inner.X+1)
	buf.WriteString("\x1b[0m")
	buf.WriteString(th.Palette.RailBG.BG())
	buf.WriteString(th.Palette.RailDim.FG())
	buf.WriteString(left)
	buf.WriteString(strings.Repeat(" ", pad))
	if right != "" {
		buf.WriteString("\x1b[0m")
		buf.WriteString(th.Palette.RailBG.BG())
		buf.WriteString(th.Palette.RailDim.FG())
		buf.WriteString(right)
	}
	buf.WriteString("\x1b[0m")
}

// scrollTo moves the table's first visible row so the selection stays in the
// body, and never scrolls past the last row.
func (s *Switcher) scrollTo(h int) {
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

// previewRow0 is the first content row to paint for a band of h rows over n rows
// of content, scrolled up by the model's offset and clamped to the content.
func (s *Switcher) previewRow0(n, h int) int {
	if n <= 0 || h <= 0 {
		return 0
	}
	max := n - h
	if max < 0 {
		max = 0
	}
	row0 := n - h - s.offset
	if row0 > max {
		row0 = max
	}
	if row0 < 0 {
		row0 = 0
	}
	return row0
}

// paintSwitcherPreview paints the selected pane's screen into the band, bottom
// aligned and scrolled by the model's offset. A pane with a live terminal paints
// from it; otherwise the lines of the model's read reply are used. No data at
// all leaves the band blank.
func paintSwitcherPreview(buf *bytes.Buffer, th theme.Theme, s *Switcher, vts map[string]*vt.Term, band layout.Rect, paint PanePainter) {
	if band.W <= 0 || band.H <= 0 {
		return
	}
	var term *vt.Term
	if id := s.Target(); id != "" {
		term = vts[id]
	}
	if term != nil {
		_, n := term.Grid().Size()
		h := band.H
		if n < h {
			h = n
		}
		row0 := s.previewRow0(n, h)
		start := band.Y + band.H - h
		paintBlankRows(buf, th, band, band.Y, start)
		if h > 0 && paint != nil {
			paint(buf, term, layout.Rect{X: band.X, Y: start, W: band.W, H: h}, row0)
		}
		return
	}
	n := len(s.lines)
	h := band.H
	if n < h {
		h = n
	}
	row0 := s.previewRow0(n, h)
	start := band.Y + band.H - h
	paintBlankRows(buf, th, band, band.Y, start)
	for i := 0; i < h; i++ {
		fmt.Fprintf(buf, "\x1b[%d;%dH", start+i+1, band.X+1)
		buf.WriteString("\x1b[0m")
		buf.WriteString(th.Palette.RailBG.BG())
		buf.WriteString(th.Palette.RailText.FG())
		text := cells.Clip(s.lines[row0+i], band.W)
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
		if y < band.Y {
			continue
		}
		fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, band.X+1)
		buf.WriteString("\x1b[0m")
		buf.WriteString(th.Palette.RailBG.BG())
		buf.WriteString(strings.Repeat(" ", band.W))
		buf.WriteString("\x1b[0m")
	}
}
