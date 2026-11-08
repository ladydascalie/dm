package vt

// registerEdit registers the insert and delete family: ICH, DCH, IL and DL.
func registerEdit(t map[csiKey]csiHandler) {
	t[csiKey{Final: '@'}] = csiInsertChars
	t[csiKey{Final: 'P'}] = csiDeleteChars
	t[csiKey{Final: 'L'}] = csiInsertLines
	t[csiKey{Final: 'M'}] = csiDeleteLines
}

func csiInsertChars(term *Term, seq CSI) {
	g := term.grid
	cur := g.Cursor()
	g.InsertCells(cur.X, cur.Y, csiParam(seq, 0, 1))
}

func csiDeleteChars(term *Term, seq CSI) {
	g := term.grid
	cur := g.Cursor()
	g.DeleteCells(cur.X, cur.Y, csiParam(seq, 0, 1))
}

func csiInsertLines(term *Term, seq CSI) {
	g := term.grid
	g.InsertLines(g.Cursor().Y, csiParam(seq, 0, 1))
}

func csiDeleteLines(term *Term, seq CSI) {
	g := term.grid
	g.DeleteLines(g.Cursor().Y, csiParam(seq, 0, 1))
}
