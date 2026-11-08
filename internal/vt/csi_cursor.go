package vt

// registerCursor registers the cursor movement family: CUU, CUD, CUF, CUB,
// CNL, CPL, CHA, HPA, CUP, HVP, VPA, HPR, VPR, CHT and CBT.
func registerCursor(t map[csiKey]csiHandler) {
	t[csiKey{Final: 'A'}] = csiCursorUp
	t[csiKey{Final: 'B'}] = csiCursorDown
	t[csiKey{Final: 'C'}] = csiCursorForward
	t[csiKey{Final: 'D'}] = csiCursorBack
	t[csiKey{Final: 'E'}] = csiCursorNextLine
	t[csiKey{Final: 'F'}] = csiCursorPrevLine
	t[csiKey{Final: 'G'}] = csiCursorColumn
	t[csiKey{Final: '`'}] = csiCursorColumnAbsolute
	t[csiKey{Final: 'H'}] = csiCursorPosition
	t[csiKey{Final: 'f'}] = csiCursorPosition
	t[csiKey{Final: 'd'}] = csiCursorRow
	t[csiKey{Final: 'a'}] = csiCursorForward
	t[csiKey{Final: 'e'}] = csiCursorDown
	t[csiKey{Final: 'I'}] = csiCursorForwardTab
	t[csiKey{Final: 'Z'}] = csiCursorBackTab
}

func csiCursorUp(term *Term, seq CSI) {
	moveCursor(term, 0, -csiParam(seq, 0, 1))
}

func csiCursorDown(term *Term, seq CSI) {
	moveCursor(term, 0, csiParam(seq, 0, 1))
}

func csiCursorForward(term *Term, seq CSI) {
	moveCursor(term, csiParam(seq, 0, 1), 0)
}

func csiCursorBack(term *Term, seq CSI) {
	moveCursor(term, -csiParam(seq, 0, 1), 0)
}

// moveCursor moves the cursor by dx columns and dy rows. The move stops at the
// scroll region's margins when the cursor starts inside them, and at the screen
// edge otherwise. That is xterm's rule, and esctest2 pins it for every axis.
func moveCursor(term *Term, dx, dy int) {
	g := term.grid
	cur := g.Cursor()
	loX, hiX := columnBounds(g)
	loY, hiY := rowBounds(g)
	g.SetCursor(clamp(cur.X+dx, loX, hiX), clamp(cur.Y+dy, loY, hiY))
}

// rowBounds returns the inclusive row range a vertical move may reach from the
// current cursor position. A cursor inside the scroll region stops at its top
// and bottom margins; one outside stops at the screen edge.
func rowBounds(g *Grid) (lo, hi int) {
	cur := g.Cursor()
	r := g.ScrollRegion()
	lo, hi = 0, g.rows-1
	if cur.Y >= r.Top {
		lo = r.Top
	}
	if cur.Y <= r.Bottom {
		hi = r.Bottom
	}
	return lo, hi
}

// columnBounds returns the inclusive column range a horizontal move may reach
// from the current cursor position, with the same inside-the-margins rule as
// rowBounds.
func columnBounds(g *Grid) (lo, hi int) {
	cur := g.Cursor()
	r := g.ScrollRegion()
	lo, hi = 0, g.cols-1
	if cur.X >= r.Left {
		lo = r.Left
	}
	if cur.X <= r.Right {
		hi = r.Right
	}
	return lo, hi
}

// homeColumn is the column CR, CNL, CPL and NEL move to: the left margin. With
// no left margin designated it is column 0.
func homeColumn(g *Grid) int {
	return g.ScrollRegion().Left
}

func csiCursorNextLine(term *Term, seq CSI) {
	moveCursor(term, 0, csiParam(seq, 0, 1))
	g := term.grid
	g.SetCursor(homeColumn(g), g.Cursor().Y)
}

func csiCursorPrevLine(term *Term, seq CSI) {
	moveCursor(term, 0, -csiParam(seq, 0, 1))
	g := term.grid
	g.SetCursor(homeColumn(g), g.Cursor().Y)
}

// csiCursorColumn handles CHA, which moves to a column of the active line. In
// origin mode the column is relative to the left margin, and the move stops at
// both margins.
func csiCursorColumn(term *Term, seq CSI) {
	g := term.grid
	x := csiParam(seq, 0, 1) - 1
	if g.Modes().Origin {
		r := g.ScrollRegion()
		x = clamp(r.Left+x, r.Left, r.Right)
	} else {
		x = clamp(x, 0, g.cols-1)
	}
	g.SetCursor(x, g.Cursor().Y)
}

// csiCursorColumnAbsolute handles HPA. HPA looks like CHA but ignores origin
// mode, so its column is always absolute.
func csiCursorColumnAbsolute(term *Term, seq CSI) {
	g := term.grid
	g.SetCursor(clamp(csiParam(seq, 0, 1)-1, 0, g.cols-1), g.Cursor().Y)
}

// csiCursorPosition handles CUP and HVP. In origin mode both axes are relative
// to the margins and the move stops at them.
func csiCursorPosition(term *Term, seq CSI) {
	g := term.grid
	x := csiParam(seq, 1, 1) - 1
	y := csiParam(seq, 0, 1) - 1
	if g.Modes().Origin {
		r := g.ScrollRegion()
		x = clamp(r.Left+x, r.Left, r.Right)
		y = clamp(r.Top+y, r.Top, r.Bottom)
	} else {
		x = clamp(x, 0, g.cols-1)
		y = clamp(y, 0, g.rows-1)
	}
	g.SetCursor(x, y)
}

// csiCursorRow handles VPA, which is absolute and ignores origin mode.
func csiCursorRow(term *Term, seq CSI) {
	g := term.grid
	g.SetCursor(g.Cursor().X, clamp(csiParam(seq, 0, 1)-1, 0, g.rows-1))
}

// csiCursorForwardTab handles CHT. A tab stops at the right margin, as a DEC
// terminal does; a tab that starts right of the margin stops at the screen
// edge.
func csiCursorForwardTab(term *Term, seq CSI) {
	g := term.grid
	cur := g.Cursor()
	limit := g.cols - 1
	if r := g.ScrollRegion(); cur.X <= r.Right {
		limit = r.Right
	}
	x := cur.X
	for i, n := 0, csiParam(seq, 0, 1); i < n; i++ {
		next := g.NextTabStop(x)
		if next > limit {
			next = limit
		}
		if next == x {
			break
		}
		x = next
	}
	g.SetCursor(x, cur.Y)
}

// csiCursorBackTab handles CBT, which ignores the margins and stops at the
// left edge of the screen.
func csiCursorBackTab(term *Term, seq CSI) {
	g := term.grid
	cur := g.Cursor()
	x := cur.X
	for i, n := 0, csiParam(seq, 0, 1); i < n; i++ {
		prev := g.PrevTabStop(x)
		if prev == x {
			break
		}
		x = prev
	}
	g.SetCursor(x, cur.Y)
}
