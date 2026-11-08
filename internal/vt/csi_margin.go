package vt

// This file owns the margins family: DECSTBM, DECSLRM, DECCOLM's conformance
// gate, and the column delete that is only defined inside the margins. It
// registers itself so the family list in dispatch.go does not grow per stream.
// DECCOLM and DECLRMM themselves are DEC private modes and live in csi_mode.go.

func init() {
	csiTable[csiKey{Final: 'r'}] = csiSetTopBottomMargins           // DECSTBM
	csiTable[csiKey{Final: 's'}] = csiSaveCursorOrMargins           // SCOSC / DECSLRM
	csiTable[csiKey{Inter: "\"", Final: 'p'}] = csiConformanceLevel // DECSCL
	csiTable[csiKey{Inter: "'", Final: '~'}] = csiDeleteColumns     // DECDC
}

// csiSetTopBottomMargins handles DECSTBM. The top and bottom defaults are the
// screen edges, and a top that is not above the bottom is ignored. The cursor
// moves home, as the DEC terminal does.
func csiSetTopBottomMargins(term *Term, seq CSI) {
	g := term.grid
	_, rows := g.Size()
	top := csiParam(seq, 0, 1) - 1
	bottom := rows - 1
	if len(seq.Params) > 1 && seq.Params[1] != 0 {
		bottom = seq.Params[1] - 1
	}
	top = clamp(top, 0, rows-1)
	bottom = clamp(bottom, 0, rows-1)
	if top >= bottom {
		return
	}
	r := g.ScrollRegion()
	r.Top, r.Bottom = top, bottom
	g.SetScrollRegion(r)
	homeCursor(g)
}

// csiSaveCursorOrMargins handles CSI s. In left-right margin mode it is
// DECSLRM; otherwise it is SCOSC, the ANSI save cursor. The two share a final
// byte in the standard, and the mode decides, as it does in xterm.
func csiSaveCursorOrMargins(term *Term, seq CSI) {
	if term.level >= 4 && term.grid.Modes().LeftRightMargin {
		csiSetLeftRightMargins(term, seq)
		return
	}
	escSaveCursor(term)
}

// csiSetLeftRightMargins handles DECSLRM. It has no effect unless DECLRMM is
// on. The right default is the screen edge, and a left that is not left of the
// right is ignored. The cursor moves home.
func csiSetLeftRightMargins(term *Term, seq CSI) {
	g := term.grid
	if term.level < 4 || !g.Modes().LeftRightMargin {
		return
	}
	cols, _ := g.Size()
	left := csiParam(seq, 0, 1) - 1
	right := cols - 1
	if len(seq.Params) > 1 && seq.Params[1] != 0 {
		right = seq.Params[1] - 1
	}
	left = clamp(left, 0, cols-1)
	right = clamp(right, 0, cols-1)
	if left >= right {
		return
	}
	r := g.ScrollRegion()
	r.Left, r.Right = left, right
	g.SetScrollRegion(r)
	homeCursor(g)
}

// csiConformanceLevel handles DECSCL. Only the level matters: DECSLRM needs
// level 4 and DECNCSM is a level 5 feature.
func csiConformanceLevel(term *Term, seq CSI) {
	level := csiParamOr(seq, 0, 0)
	if level >= 61 && level <= 65 {
		term.level = level - 60
	}
	if len(seq.Params) > 1 && seq.Params[1] >= 0 && seq.Params[1] <= 2 {
		term.sevenBit = seq.Params[1]
	}
}

// csiDeleteColumns handles DECDC. It deletes columns at the cursor on every row
// of the scroll region, between the left and right margins, and it does nothing
// when the cursor is outside the scroll area.
func csiDeleteColumns(term *Term, seq CSI) {
	g := term.grid
	r := g.ScrollRegion()
	cur := g.Cursor()
	if cur.Y < r.Top || cur.Y > r.Bottom || cur.X < r.Left || cur.X > r.Right {
		return
	}
	g.DeleteColumns(cur.X, r.Top, r.Bottom, r.Left, r.Right, csiParam(seq, 0, 1))
}

// homeCursor moves the cursor to the home position: the margins' top left in
// origin mode, and the screen's top left otherwise.
func homeCursor(g *Grid) {
	r := g.ScrollRegion()
	if g.Modes().Origin {
		g.SetCursor(r.Left, r.Top)
		return
	}
	g.SetCursor(0, 0)
}
