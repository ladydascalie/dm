package vt

// csiKey identifies one control sequence. Priv is the private marker ('?', '>',
// '<', '=') or 0; Inter is the intermediate bytes as a string; Final is the
// last byte.
type csiKey struct {
	Priv  byte
	Inter string
	Final byte
}

// csiHandler applies one control sequence to a terminal.
type csiHandler func(*Term, CSI)

// csiTable is the CSI dispatch. A family file registers its own keys, so two
// families merge without touching the same file. The parser does one map
// lookup, not a switch.
var csiTable = map[csiKey]csiHandler{}

// escKey identifies a non-CSI escape sequence by its intermediate bytes and its
// final byte.
type escKey struct {
	Inter string
	Final byte
}

// escHandler applies one escape sequence.
type escHandler func(*Term)

// escTable is the non-CSI escape dispatch, kept small the same way.
var escTable = map[escKey]escHandler{}

func init() {
	registerFamilies()
}

// registerFamilies is the one visible list of families. A stream adds its file
// and one line here.
func registerFamilies() {
	registerCursor(csiTable)
	registerErase(csiTable)
	registerEdit(csiTable)
	registerScroll(csiTable)
	registerTab(csiTable, escTable)
	registerSGR(csiTable)
}

// csiParam returns parameter i, or def when it is missing or zero. Movement and
// count sequences read zero as their default.
func csiParam(seq CSI, i, def int) int {
	if i >= len(seq.Params) || seq.Params[i] == 0 {
		return def
	}
	return seq.Params[i]
}

// csiParamOr returns parameter i, or def only when it is missing. Sequences
// that use zero as a real value read it this way.
func csiParamOr(seq CSI, i, def int) int {
	if i >= len(seq.Params) {
		return def
	}
	return seq.Params[i]
}
