package vt

// This file owns the erase family: ED, EL, ECH and the selective erase pair
// DECSED and DECSEL. None of them respects the scroll region; the selective
// pair also leaves protected cells alone. The family registers itself.

// registerErase registers the erase family.
func registerErase(t map[csiKey]csiHandler) {
	t[csiKey{Final: 'J'}] = csiEraseDisplay
	t[csiKey{Final: 'K'}] = csiEraseLine
	t[csiKey{Final: 'X'}] = csiEraseChars
	t[csiKey{Priv: '?', Final: 'J'}] = csiSelectiveEraseDisplay // DECSED
	t[csiKey{Priv: '?', Final: 'K'}] = csiSelectiveEraseLine    // DECSEL
}

func csiEraseDisplay(term *Term, seq CSI) {
	g := term.grid
	cur := g.Cursor()
	switch csiParamOr(seq, 0, 0) {
	case 0:
		eraseCells(g, cur.X, g.cols-1, cur.Y)
		for y := cur.Y + 1; y < g.rows; y++ {
			eraseCells(g, 0, g.cols-1, y)
		}
	case 1:
		for y := 0; y < cur.Y; y++ {
			eraseCells(g, 0, g.cols-1, y)
		}
		eraseCells(g, 0, cur.X, cur.Y)
	case 2:
		g.Clear()
	}
}

func csiEraseLine(term *Term, seq CSI) {
	g := term.grid
	cur := g.Cursor()
	switch csiParamOr(seq, 0, 0) {
	case 0:
		eraseCells(g, cur.X, g.cols-1, cur.Y)
	case 1:
		eraseCells(g, 0, cur.X, cur.Y)
	case 2:
		eraseCells(g, 0, g.cols-1, cur.Y)
	}
}

func csiEraseChars(term *Term, seq CSI) {
	g := term.grid
	cur := g.Cursor()
	eraseCells(g, cur.X, cur.X+csiParam(seq, 0, 1)-1, cur.Y)
}

// csiSelectiveEraseDisplay handles DECSED. It erases like ED but leaves
// protected cells alone, and it erases across the whole screen, ignoring the
// scroll region and its margins. Parameter 3 erases scrollback and leaves the
// screen as it is.
func csiSelectiveEraseDisplay(term *Term, seq CSI) {
	g := term.grid
	cur := g.Cursor()
	switch csiParamOr(seq, 0, 0) {
	case 0:
		eraseCellsSelective(g, cur.X, g.cols-1, cur.Y)
		for y := cur.Y + 1; y < g.rows; y++ {
			eraseCellsSelective(g, 0, g.cols-1, y)
		}
	case 1:
		for y := 0; y < cur.Y; y++ {
			eraseCellsSelective(g, 0, g.cols-1, y)
		}
		eraseCellsSelective(g, 0, cur.X, cur.Y)
	case 2:
		for y := 0; y < g.rows; y++ {
			eraseCellsSelective(g, 0, g.cols-1, y)
		}
	}
}

// csiSelectiveEraseLine handles DECSEL. It erases like EL but leaves protected
// cells alone, and it erases across the whole line, ignoring the scroll region
// and its margins.
func csiSelectiveEraseLine(term *Term, seq CSI) {
	g := term.grid
	cur := g.Cursor()
	switch csiParamOr(seq, 0, 0) {
	case 0:
		eraseCellsSelective(g, cur.X, g.cols-1, cur.Y)
	case 1:
		eraseCellsSelective(g, 0, cur.X, cur.Y)
	case 2:
		eraseCellsSelective(g, 0, g.cols-1, cur.Y)
	}
}

// eraseCells blanks the inclusive range x0 to x1 on row y. It leaves a cell
// written inside an ISO protected area alone, and it ignores DECSCA
// protection, as ED, EL and ECH do.
func eraseCells(g *Grid, x0, x1, y int) {
	eraseRange(g, x0, x1, y, false)
}

// eraseCellsSelective blanks the inclusive range x0 to x1 on row y and leaves
// every protected cell alone, DECSCA and ISO alike.
func eraseCellsSelective(g *Grid, x0, x1, y int) {
	eraseRange(g, x0, x1, y, true)
}

// eraseRange blanks the inclusive range x0 to x1 on row y. A cell its
// protection rule protects is skipped with the whole cluster, so half of a
// protected wide cell never goes.
func eraseRange(g *Grid, x0, x1, y int, selective bool) {
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	for x := x0; x <= x1; x++ {
		c := g.Cell(x, y)
		if c.ISOProtected || (selective && c.Protected) {
			continue
		}
		g.Erase(x, y)
	}
}
