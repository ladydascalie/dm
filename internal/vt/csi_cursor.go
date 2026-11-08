package vt

// registerCursor registers the cursor movement family: CUU, CUD, CUF, CUB,
// CNL, CPL, CHA, HPA, CUP, HVP, VPA, CHT and CBT.
func registerCursor(t map[csiKey]csiHandler) {
	t[csiKey{Final: 'A'}] = csiCursorUp
	t[csiKey{Final: 'B'}] = csiCursorDown
	t[csiKey{Final: 'C'}] = csiCursorForward
	t[csiKey{Final: 'D'}] = csiCursorBack
	t[csiKey{Final: 'E'}] = csiCursorNextLine
	t[csiKey{Final: 'F'}] = csiCursorPrevLine
	t[csiKey{Final: 'G'}] = csiCursorColumn
	t[csiKey{Final: '`'}] = csiCursorColumn
	t[csiKey{Final: 'H'}] = csiCursorPosition
	t[csiKey{Final: 'f'}] = csiCursorPosition
	t[csiKey{Final: 'd'}] = csiCursorRow
	t[csiKey{Final: 'I'}] = csiCursorForwardTab
	t[csiKey{Final: 'Z'}] = csiCursorBackTab
}

func csiCursorUp(term *Term, seq CSI) {
	moveVertically(term, -csiParam(seq, 0, 1))
}

func csiCursorDown(term *Term, seq CSI) {
	moveVertically(term, csiParam(seq, 0, 1))
}

// moveVertically moves the cursor by delta rows, stopping at the scroll
// region's top or bottom margin when the cursor starts inside it.
func moveVertically(term *Term, delta int) {
	g := term.grid
	cur := g.Cursor()
	lo, hi := 0, g.rows-1
	r := g.ScrollRegion()
	if cur.Y >= r.Top {
		lo = r.Top
	}
	if cur.Y <= r.Bottom {
		hi = r.Bottom
	}
	g.SetCursor(cur.X, clamp(cur.Y+delta, lo, hi))
}

func csiCursorForward(term *Term, seq CSI) {
	g := term.grid
	cur := g.Cursor()
	g.SetCursor(cur.X+csiParam(seq, 0, 1), cur.Y)
}

func csiCursorBack(term *Term, seq CSI) {
	g := term.grid
	cur := g.Cursor()
	g.SetCursor(cur.X-csiParam(seq, 0, 1), cur.Y)
}

func csiCursorNextLine(term *Term, seq CSI) {
	moveVertically(term, csiParam(seq, 0, 1))
	g := term.grid
	g.SetCursor(0, g.Cursor().Y)
}

func csiCursorPrevLine(term *Term, seq CSI) {
	moveVertically(term, -csiParam(seq, 0, 1))
	g := term.grid
	g.SetCursor(0, g.Cursor().Y)
}

func csiCursorColumn(term *Term, seq CSI) {
	g := term.grid
	g.SetCursor(csiParam(seq, 0, 1)-1, g.Cursor().Y)
}

func csiCursorPosition(term *Term, seq CSI) {
	g := term.grid
	g.SetCursor(csiParam(seq, 1, 1)-1, csiParam(seq, 0, 1)-1)
}

func csiCursorRow(term *Term, seq CSI) {
	g := term.grid
	g.SetCursor(g.Cursor().X, csiParam(seq, 0, 1)-1)
}

func csiCursorForwardTab(term *Term, seq CSI) {
	g := term.grid
	x := g.Cursor().X
	for i, n := 0, csiParam(seq, 0, 1); i < n; i++ {
		next := g.NextTabStop(x)
		if next == x {
			break
		}
		x = next
	}
	g.SetCursor(x, g.Cursor().Y)
}

func csiCursorBackTab(term *Term, seq CSI) {
	g := term.grid
	x := g.Cursor().X
	for i, n := 0, csiParam(seq, 0, 1); i < n; i++ {
		prev := g.PrevTabStop(x)
		if prev == x {
			break
		}
		x = prev
	}
	g.SetCursor(x, g.Cursor().Y)
}
