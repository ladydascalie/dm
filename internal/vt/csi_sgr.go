package vt

// registerSGR reserves the SGR family's place in the dispatch. The stream that
// implements SGR registers its keys here; this unit only opens the file so the
// table has one entry per family.
func registerSGR(t map[csiKey]csiHandler) {
}
