package vt

// Term is the new terminal: a grid, a parser, and the state the parser needs to
// place cells. Write feeds bytes to the parser; the parser calls back here to
// move the cursor and store cells.
type Term struct {
	grid   *Grid
	parser parser
	style  Style
}

// NewTerm returns a terminal with a blank grid of cols by rows and the default
// style.
func NewTerm(cols, rows int) *Term {
	return &Term{grid: NewGrid(cols, rows), style: DefaultStyle()}
}

// Write parses b and places what it holds. Pending parser state survives the
// call, so a sequence or a cluster split across chunks parses the same as one
// chunk.
func (t *Term) Write(b []byte) {
	t.parser.write(t, b)
}

// Flush places the cluster under construction. A cluster is complete only when
// the next rune proves it cannot extend it, so the last cluster of a Write
// stays pending, exactly as a snapshot must carry it.
func (t *Term) Flush() {
	t.parser.flush(t)
}

// place puts one grapheme cluster on the grid, wraps when the row is full, and
// advances the cursor. A wide cell at the right edge moves to the next row
// first when wrap is on; with wrap off the grid stores it as a normal cell.
func (t *Term) place(content string) {
	g := t.grid
	cols, _ := g.Size()
	c := NewCell(content, t.style)

	cur := g.Cursor()
	if cur.WrapNext {
		g.SetCursor(0, cur.Y)
		t.lineFeed()
		cur = g.Cursor()
	}
	if c.Width == WidthWide && cur.X == cols-1 && g.Modes().Wrap {
		t.carriageReturn()
		t.lineFeed()
		cur = g.Cursor()
	}

	g.Write(cur.X, cur.Y, c)
	placed := g.Cell(cur.X, cur.Y)

	if next := cur.X + int(placed.Width); next < cols {
		g.SetCursor(next, cur.Y)
		return
	}
	g.SetCursor(cols-1, cur.Y)
	if g.Modes().Wrap {
		g.SetWrapNext(true)
	}
}

// control executes a C0 control.
func (t *Term) control(b byte) {
	switch b {
	case 0x08: // BS
		t.backspace()
	case 0x09: // HT
		t.tab()
	case 0x0a, 0x0b, 0x0c: // LF, VT, FF
		t.lineFeed()
	case 0x0d: // CR
		t.carriageReturn()
	case 0x0e: // SO
		t.shiftCharset(1)
	case 0x0f: // SI
		t.shiftCharset(0)
	}
}

// lineFeed moves down one row, scrolling the scroll region when the cursor is
// on its bottom row. The column does not change.
func (t *Term) lineFeed() {
	g := t.grid
	cur := g.Cursor()
	r := g.ScrollRegion()
	switch {
	case cur.Y == r.Bottom:
		g.ScrollUp(1)
	case cur.Y < g.rows-1:
		cur.Y++
	}
	g.SetCursor(cur.X, cur.Y)
}

// carriageReturn moves to column 0.
func (t *Term) carriageReturn() {
	g := t.grid
	g.SetCursor(0, g.Cursor().Y)
}

// backspace moves left one cluster. A wide cell is skipped from either slot.
func (t *Term) backspace() {
	g := t.grid
	cur := g.Cursor()
	x := cur.X
	if x > 0 {
		x--
	}
	if x > 0 && g.lines[cur.Y][x].IsContinuation() {
		x--
	}
	g.SetCursor(x, cur.Y)
}

// tab moves to the next tab stop, or to the last column when there is none.
func (t *Term) tab() {
	g := t.grid
	cur := g.Cursor()
	g.SetCursor(g.NextTabStop(cur.X), cur.Y)
}

// shiftCharset selects G0 or G1 as GL, as SI and SO do.
func (t *Term) shiftCharset(gl int) {
	cs := t.grid.Charset()
	cs.GL = gl
	t.grid.SetCharset(cs)
}

// escape handles a non-CSI escape sequence. The ESC control family lands here
// with the dispatch table; until then a parsed sequence is ignored, as a
// terminal ignores one it does not know.
func (t *Term) escape(inter string, final byte) {
}

// csi handles one parsed control sequence. The CSI families land here with the
// dispatch table; until then a parsed sequence is ignored.
func (t *Term) csi(seq CSI) {
}

// osc handles one OSC body. The OSC families land here with the dispatch table;
// until then a parsed string is ignored.
func (t *Term) osc(body string) {
}
