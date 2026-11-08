package vt

// registerScroll registers the scroll family: SU and SD.
func registerScroll(t map[csiKey]csiHandler) {
	t[csiKey{Final: 'S'}] = csiScrollUp
	t[csiKey{Final: 'T'}] = csiScrollDown
}

func csiScrollUp(term *Term, seq CSI) {
	term.grid.ScrollUp(csiParam(seq, 0, 1))
}

func csiScrollDown(term *Term, seq CSI) {
	term.grid.ScrollDown(csiParam(seq, 0, 1))
}
