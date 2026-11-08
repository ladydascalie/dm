package vt

// registerTab registers the tab stop family: HTS is an escape sequence and TBC
// is a CSI. HT and CBT read the stops; HTS and TBC set them.
func registerTab(csi map[csiKey]csiHandler, esc map[escKey]escHandler) {
	csi[csiKey{Final: 'g'}] = csiTabClear
	esc[escKey{Final: 'H'}] = escTabSet
}

// csiTabClear handles TBC: 0 clears the stop at the cursor, 3 clears all.
func csiTabClear(term *Term, seq CSI) {
	g := term.grid
	switch csiParamOr(seq, 0, 0) {
	case 0:
		g.ClearTabStop(g.Cursor().X)
	case 3:
		g.ClearTabStops()
	}
}

// escTabSet handles HTS.
func escTabSet(term *Term) {
	term.grid.SetTabStop(term.grid.Cursor().X)
}
