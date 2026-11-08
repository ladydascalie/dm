package vt

func init() {
	// DECSCA, DECIC, DECBI and DECFI register from this file too. DECDC is
	// the margins family's key and registers there.
	csiTable[csiKey{Inter: "\"", Final: 'q'}] = csiSelectCharacterProtection // DECSCA
	csiTable[csiKey{Inter: "'", Final: '}'}] = csiInsertColumns              // DECIC
	escTable[escKey{Final: '6'}] = escBackIndex                              // DECBI
	escTable[escKey{Final: '9'}] = escForwardIndex                           // DECFI
	escTable[escKey{Final: 'V'}] = escStartProtectedArea                     // SPA
	escTable[escKey{Final: 'W'}] = escEndProtectedArea                       // EPA
}

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

// csiSelectCharacterProtection handles DECSCA. 1 turns protection on for cells
// written afterwards; 0 and 2 turn it off.
func csiSelectCharacterProtection(term *Term, seq CSI) {
	term.protection.DECSCA = csiParamOr(seq, 0, 0) == 1
}

// escStartProtectedArea handles SPA. Cells written from now on are ISO
// protected: every erase leaves them alone, selective or not.
func escStartProtectedArea(term *Term) {
	term.protection.ISO = true
}

// escEndProtectedArea handles EPA: cells written from now on are not ISO
// protected.
func escEndProtectedArea(term *Term) {
	term.protection.ISO = false
}

// csiInsertColumns handles DECIC: insert columns at the cursor on every row of
// the scroll region, between the left and right margins. It does nothing when
// the cursor is outside the scroll area.
func csiInsertColumns(term *Term, seq CSI) {
	g := term.grid
	r := g.ScrollRegion()
	cur := g.Cursor()
	if cur.Y < r.Top || cur.Y > r.Bottom || cur.X < r.Left || cur.X > r.Right {
		return
	}
	g.InsertColumns(cur.X, r.Top, r.Bottom, r.Left, r.Right, csiParam(seq, 0, 1))
}

// escBackIndex handles DECBI: move left one column. At the left margin it
// scrolls the region's columns right instead and leaves the cursor there. At
// the left screen edge, outside the region, it does nothing.
func escBackIndex(term *Term) {
	g := term.grid
	r := g.ScrollRegion()
	cur := g.Cursor()
	if cur.X == r.Left && cur.Y >= r.Top && cur.Y <= r.Bottom {
		g.ScrollColumnsRight(1)
		g.SetCursor(r.Left, cur.Y)
		return
	}
	if cur.X > 0 {
		g.SetCursor(cur.X-1, cur.Y)
	}
}

// escForwardIndex handles DECFI: move right one column. At the right margin it
// scrolls the region's columns left instead and leaves the cursor there. At
// the right screen edge, outside the region, it does nothing.
func escForwardIndex(term *Term) {
	g := term.grid
	r := g.ScrollRegion()
	cur := g.Cursor()
	if cur.X == r.Right && cur.Y >= r.Top && cur.Y <= r.Bottom {
		g.ScrollColumnsLeft(1)
		g.SetCursor(r.Right, cur.Y)
		return
	}
	if cur.X < g.cols-1 {
		g.SetCursor(cur.X+1, cur.Y)
	}
}
