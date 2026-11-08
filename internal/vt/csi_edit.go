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
	_, right, ok := editBounds(term)
	if !ok {
		return
	}
	g.InsertCells(cur.X, cur.Y, right, csiParam(seq, 0, 1))
}

func csiDeleteChars(term *Term, seq CSI) {
	g := term.grid
	cur := g.Cursor()
	_, right, ok := editBounds(term)
	if !ok {
		return
	}
	g.DeleteCells(cur.X, cur.Y, right, csiParam(seq, 0, 1))
}

func csiInsertLines(term *Term, seq CSI) {
	g := term.grid
	if _, _, ok := editBounds(term); !ok {
		return
	}
	g.InsertLines(g.Cursor().Y, csiParam(seq, 0, 1))
}

func csiDeleteLines(term *Term, seq CSI) {
	g := term.grid
	if _, _, ok := editBounds(term); !ok {
		return
	}
	g.DeleteLines(g.Cursor().Y, csiParam(seq, 0, 1))
}

// editBounds returns the inclusive column range an insert or delete sequence
// may touch. When the left and right margins are designated it is the margin
// range, and the sequence does nothing when the cursor is outside it;
// otherwise it is the screen.
func editBounds(term *Term) (left, right int, ok bool) {
	g := term.grid
	cols, _ := g.Size()
	r := g.ScrollRegion()
	if r.Left > 0 || r.Right < cols-1 {
		cur := g.Cursor()
		if cur.X < r.Left || cur.X > r.Right {
			return 0, 0, false
		}
		return r.Left, r.Right, true
	}
	return 0, cols - 1, true
}
