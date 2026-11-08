package vt

// This file owns the rectangular family: DECCRA, DECERA, DECFRA and the
// selective erase DECSERA. Every rectangle is inclusive, one-based on the wire,
// relative to the margins in origin mode, clipped to the screen, and independent
// of the scroll region. None of them moves the cursor. The family registers
// itself.

func init() {
	csiTable[csiKey{Inter: "$", Final: 'v'}] = csiCopyRect      // DECCRA
	csiTable[csiKey{Inter: "$", Final: 'z'}] = csiEraseRect     // DECERA
	csiTable[csiKey{Inter: "$", Final: 'x'}] = csiFillRect      // DECFRA
	csiTable[csiKey{Inter: "$", Final: '{'}] = csiSelectiveRect // DECSERA
}

// rectParam returns one-based rectangle parameter i, or def when it is missing
// or zero.
func rectParam(seq CSI, i, def int) int {
	if i >= len(seq.Params) || seq.Params[i] <= 0 {
		return def
	}
	return seq.Params[i]
}

// rectCoords reads the four rectangle parameters starting at base and returns
// inclusive zero-based screen coordinates. It applies the origin-mode offset,
// clips to the screen and reports ok false for an inverted rectangle.
func rectCoords(term *Term, seq CSI, base int) (top, left, bottom, right int, ok bool) {
	g := term.grid
	cols, rows := g.Size()
	top = rectParam(seq, base, 1) - 1
	left = rectParam(seq, base+1, 1) - 1
	bottom = rectParam(seq, base+2, rows) - 1
	right = rectParam(seq, base+3, cols) - 1
	if g.Modes().Origin {
		r := g.ScrollRegion()
		top += r.Top
		bottom += r.Top
		left += r.Left
		right += r.Left
	}
	top = clamp(top, 0, rows-1)
	bottom = clamp(bottom, 0, rows-1)
	left = clamp(left, 0, cols-1)
	right = clamp(right, 0, cols-1)
	if top > bottom || left > right {
		return 0, 0, 0, 0, false
	}
	return top, left, bottom, right, true
}

// csiCopyRect handles DECCRA: copy the source rectangle to the destination
// corner, clipping both to the screen. The copy is taken first, so an
// overlapping source and destination read the original cells.
func csiCopyRect(term *Term, seq CSI) {
	top, left, bottom, right, ok := rectCoords(term, seq, 0)
	if !ok {
		return
	}
	g := term.grid
	cols, rows := g.Size()
	destTop := rectParam(seq, 5, 1) - 1
	destLeft := rectParam(seq, 6, 1) - 1
	if g.Modes().Origin {
		r := g.ScrollRegion()
		destTop += r.Top
		destLeft += r.Left
	}
	src := make([][]Cell, bottom-top+1)
	for y := top; y <= bottom; y++ {
		row := make([]Cell, right-left+1)
		for x := left; x <= right; x++ {
			row[x-left] = g.Cell(x, y)
		}
		src[y-top] = row
	}
	for dy, row := range src {
		for dx, c := range row {
			x, y := destLeft+dx, destTop+dy
			if x < 0 || x >= cols || y < 0 || y >= rows {
				continue
			}
			g.Write(x, y, c)
		}
	}
}

// csiEraseRect handles DECERA: blank every cell in the rectangle.
func csiEraseRect(term *Term, seq CSI) {
	top, left, bottom, right, ok := rectCoords(term, seq, 0)
	if !ok {
		return
	}
	for y := top; y <= bottom; y++ {
		for x := left; x <= right; x++ {
			term.grid.Erase(x, y)
		}
	}
}

// csiFillRect handles DECFRA: fill the rectangle with one character.
func csiFillRect(term *Term, seq CSI) {
	ch := csiParamOr(seq, 0, ' ')
	if ch <= 0 || ch > 0x10ffff {
		ch = ' '
	}
	cell := NewCell(string(rune(ch)), DefaultStyle())
	top, left, bottom, right, ok := rectCoords(term, seq, 1)
	if !ok {
		return
	}
	for y := top; y <= bottom; y++ {
		for x := left; x <= right; x++ {
			term.grid.Write(x, y, cell)
		}
	}
}

// csiSelectiveRect handles DECSERA: blank the unprotected cells in the
// rectangle and leave the protected ones alone.
func csiSelectiveRect(term *Term, seq CSI) {
	top, left, bottom, right, ok := rectCoords(term, seq, 0)
	if !ok {
		return
	}
	g := term.grid
	for y := top; y <= bottom; y++ {
		for x := left; x <= right; x++ {
			if g.Cell(x, y).Protected {
				continue
			}
			g.Erase(x, y)
		}
	}
}
