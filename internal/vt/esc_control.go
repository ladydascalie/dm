package vt

// This file owns the control sequences outside the CSI mode family: RIS,
// DECSTR, DECALN, DECSC/DECRC, REP, SOS and PM. It registers itself so the
// family list in dispatch.go does not grow per stream.
//
// SOS (ESC X) and PM (ESC ^) need no handler here. The parser already sends
// them to the string state, which swallows their body up to ST and returns to
// ground, so their content never reaches the grid and the next rune prints
// normally. The tests pin that.

func init() {
	escTable[escKey{Final: '7'}] = escSaveCursor                  // DECSC
	escTable[escKey{Final: '8'}] = escRestoreCursor               // DECRC
	escTable[escKey{Final: 'c'}] = escReset                       // RIS
	escTable[escKey{Inter: "#", Final: '8'}] = escScreenAlignment // DECALN
	csiTable[csiKey{Inter: "!", Final: 'p'}] = csiSoftReset       // DECSTR
	csiTable[csiKey{Final: 'b'}] = csiRepeat                      // REP
}

// escSaveCursor handles DECSC.
func escSaveCursor(term *Term) {
	term.grid.SaveCursor(term.style)
}

// escRestoreCursor handles DECRC.
func escRestoreCursor(term *Term) {
	term.style = term.grid.RestoreCursor()
}

// escReset handles RIS: the full reset. It clears the screen and the alternate
// screen, resets the modes, the margins, the tab stops, the charset, the saved
// cursor and the rendition.
func escReset(term *Term) {
	term.grid.Reset()
	term.style = DefaultStyle()
}

// escScreenAlignment handles DECALN: fill the screen with E, home the cursor and
// drop the margins.
func escScreenAlignment(term *Term) {
	term.grid.Fill("E")
}

// csiSoftReset handles DECSTR: the soft reset. It resets the modes, the
// margins, the charset, the saved cursor and the rendition, and it leaves the
// screen contents and the cursor position alone.
func csiSoftReset(term *Term, _ CSI) {
	term.grid.SoftReset()
	term.style = DefaultStyle()
}

// csiRepeat handles REP: repeat the preceding graphic character. The character
// comes from the cell before the cursor, so REP needs no extra state and works
// after a chunk boundary.
func csiRepeat(term *Term, seq CSI) {
	c, ok := term.precedingCell()
	if !ok {
		return
	}
	for i, n := 0, csiParam(seq, 0, 1); i < n; i++ {
		term.place(c.Content)
	}
}

// precedingCell returns the cell the cursor just passed. With the pending wrap
// set the cursor already sits on the last column it wrote, so that cell is the
// preceding one; otherwise the cursor sits on the next column.
func (t *Term) precedingCell() (Cell, bool) {
	g := t.grid
	cur := g.Cursor()
	x := cur.X
	if !cur.WrapNext {
		x--
	}
	if x < 0 || cur.Y < 0 || cur.Y >= g.rows {
		return Cell{}, false
	}
	c := g.Cell(x, cur.Y)
	if c.Content == "" {
		return Cell{}, false
	}
	return c, true
}
