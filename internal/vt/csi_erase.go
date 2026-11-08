package vt

// registerErase registers the erase family: ED, EL and ECH.
func registerErase(t map[csiKey]csiHandler) {
	t[csiKey{Final: 'J'}] = csiEraseDisplay
	t[csiKey{Final: 'K'}] = csiEraseLine
	t[csiKey{Final: 'X'}] = csiEraseChars
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

// eraseCells blanks the inclusive range x0 to x1 on row y.
func eraseCells(g *Grid, x0, x1, y int) {
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	for x := x0; x <= x1; x++ {
		g.Erase(x, y)
	}
}
