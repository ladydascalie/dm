package vt

// Term is the new terminal: a grid, a parser, and the state the parser needs to
// place cells. Write feeds bytes to the parser; the parser calls back here to
// move the cursor and store cells.
type Term struct {
	grid   *Grid
	parser parser
	style  Style
	// protected is the DECSCA state applied to cells written from now on.
	protected bool
	// level is the DECSCL conformance level. Level 4 and above support
	// DECSLRM; DECNCSM is a level 5 feature. The default is 5, as xterm's is.
	level int

	// OSC state. The title, the palette overrides, the hyperlink the next
	// cells carry, the clipboard and the 7501 status.
	iconTitle   string
	windowTitle string
	link        string
	clipboard   string
	status      string
	colors      map[int]Color

	// Settings the report family answers but the grid does not model.
	cursorStyle  int
	screenLines  int
	statusType   int
	activeStatus int
	changeExtent int
	localFuncs   int
	localKeys    int
	modifierKeys int
	linesPerPage int
	sevenBit     int

	// decModeStates and ansiModeStates record every mode set and reset, so a
	// DECRQM can report a mode the grid does not model.
	decModeStates  map[int]bool
	ansiModeStates map[int]bool
}

// NewTerm returns a terminal with a blank grid of cols by rows and the default
// style.
func NewTerm(cols, rows int) *Term {
	return &Term{
		grid: NewGrid(cols, rows), style: DefaultStyle(), level: 5,
		screenLines: defaultScreenLines, sevenBit: 1,
	}
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
// When the cursor is inside the left and right margins, the row is full at the
// right margin and a wrapped line starts at the left margin.
func (t *Term) place(content string) {
	g := t.grid
	c := NewCell(content, t.style)
	c.Protected = t.protected
	c.Link = t.link

	cur := g.Cursor()
	left, right := t.wrapBounds(cur.X)
	if cur.WrapNext {
		g.SetCursor(left, cur.Y)
		t.lineFeed()
		cur = g.Cursor()
		g.SetLineWrapped(cur.Y, true)
	}
	if c.Width == WidthWide && cur.X == right && g.Modes().Wrap {
		g.SetCursor(left, cur.Y)
		t.lineFeed()
		cur = g.Cursor()
		g.SetLineWrapped(cur.Y, true)
	}

	g.Write(cur.X, cur.Y, c)
	placed := g.Cell(cur.X, cur.Y)

	if next := cur.X + int(placed.Width); next <= right {
		g.SetCursor(next, cur.Y)
		return
	}
	g.SetCursor(right, cur.Y)
	if g.Modes().Wrap {
		g.SetWrapNext(true)
	}
}

// wrapBounds returns the inclusive column range a printable rune may occupy at
// cursor column x. Inside the left and right margins it is the margin range;
// outside them it is the screen.
func (t *Term) wrapBounds(x int) (left, right int) {
	g := t.grid
	cols, _ := g.Size()
	r := g.ScrollRegion()
	if x >= r.Left && x <= r.Right {
		return r.Left, r.Right
	}
	return 0, cols - 1
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
// on its bottom row. At the bottom margin it scrolls only when the cursor is
// inside the left and right margins; outside them it does not move. The column
// does not change.
func (t *Term) lineFeed() {
	indexDown(t)
}

// carriageReturn moves to the home column: the left margin when the cursor is
// at or right of it, or column 0 when it is left of the margin.
func (t *Term) carriageReturn() {
	g := t.grid
	g.SetCursor(homeColumn(g), g.Cursor().Y)
}

// backspace moves left one cluster. A wide cell is skipped from either slot.
// Inside the left and right margins it stops at the left margin, and with
// reverse wrap on it wraps to the right margin of the previous row.
func (t *Term) backspace() {
	g := t.grid
	cur := g.Cursor()
	r := g.ScrollRegion()
	m := g.Modes()
	if cur.WrapNext {
		// The cursor is still on the cell it just wrote, with a wrap pending.
		g.SetWrapNext(false)
		if m.ReverseWrap || m.ReverseWrapExtended {
			return
		}
		cur = g.Cursor()
	}
	wraps := false
	if m.Wrap && cur.X <= r.Left {
		switch {
		case m.ReverseWrapExtended:
			// Mode 1045 wraps anywhere, and around the top and bottom.
			wraps = true
		case m.ReverseWrap && g.LineWrapped(cur.Y):
			// Mode 45 wraps only a line that autowrap began.
			wraps = true
		}
	}
	if wraps {
		y := cur.Y - 1
		if m.ReverseWrapExtended && cur.Y == r.Top {
			y = r.Bottom
		}
		if y >= 0 {
			g.SetCursor(r.Right, y)
			return
		}
	}
	x := cur.X
	if x > 0 {
		x--
	}
	if x > 0 && g.lines[cur.Y][x].IsContinuation() {
		x--
	}
	if x < r.Left && cur.X >= r.Left {
		x = r.Left
	}
	g.SetCursor(x, cur.Y)
}

// tab moves to the next tab stop, or to the last column when there is none. A
// tab at or left of the right margin stops there, as a DEC terminal's does.
func (t *Term) tab() {
	g := t.grid
	cur := g.Cursor()
	r := g.ScrollRegion()
	x := g.NextTabStop(cur.X)
	if cur.X <= r.Right && x > r.Right {
		x = r.Right
	}
	g.SetCursor(x, cur.Y)
}

// shiftCharset selects G0 or G1 as GL, as SI and SO do.
func (t *Term) shiftCharset(gl int) {
	cs := t.grid.Charset()
	cs.GL = gl
	t.grid.SetCharset(cs)
}

// escape dispatches a non-CSI escape sequence. An unregistered sequence is
// ignored, as a terminal ignores one it does not know.
func (t *Term) escape(inter string, final byte) {
	if h, ok := escTable[escKey{Inter: inter, Final: final}]; ok {
		h(t)
	}
}

// csi dispatches one parsed control sequence. An unregistered sequence is
// ignored.
func (t *Term) csi(seq CSI) {
	key := csiKey{Priv: seq.Private, Inter: string(seq.Inter), Final: seq.Final}
	if h, ok := csiTable[key]; ok {
		h(t, seq)
	}
}

// osc handles one OSC body. The OSC family lives in osc.go and dispatches on
// the command number.
func (t *Term) osc(body string) {
	t.oscCommand(body)
}
