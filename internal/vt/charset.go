package vt

import "unicode/utf8"

// decSpecialGraphics is the DEC special graphics set, designated with ESC ( 0
// and ESC ) 0. The right-hand rune is what a VT100 draws for the left-hand
// ASCII byte. The set is fixed by the VT100 manual and reproduced by xterm, so
// it is a table, not a rule.
var decSpecialGraphics = map[rune]rune{
	'`': '◆', // diamond
	'a': '▒', // checkerboard
	'b': '␉', // HT
	'c': '␌', // FF
	'd': '␍', // CR
	'e': '␊', // LF
	'f': '°', // degree
	'g': '±', // plus/minus
	'h': '␤', // NL
	'i': '␋', // VT
	'j': '┘', // lower right corner
	'k': '┐', // upper right corner
	'l': '┌', // upper left corner
	'm': '└', // lower left corner
	'n': '┼', // crossing lines
	'o': '⎺', // scan line 1
	'p': '⎻', // scan line 3
	'q': '─', // horizontal line
	'r': '⎼', // scan line 7
	's': '⎽', // scan line 9
	't': '├', // left tee
	'u': '┤', // right tee
	'v': '┴', // bottom tee
	'w': '┬', // top tee
	'x': '│', // vertical line
	'y': '≤', // less than or equal
	'z': '≥', // greater than or equal
	'{': 'π', // pi
	'|': '≠', // not equal
	'}': '£', // pound
	'~': '·', // middle dot
}

// ukGraphics is the UK set, designated with ESC ( A. It differs from ASCII in
// one byte.
var ukGraphics = map[rune]rune{
	'#': '£',
}

// TranslateCharset maps one grapheme cluster through the GL set. A cluster is a
// base rune plus whatever extends it, so only the base rune is translated and
// the rest of the cluster is kept. A set with no mapping leaves the cluster
// alone.
func TranslateCharset(cs Charset, content string) string {
	r, size := utf8.DecodeRuneInString(content)
	if size == 0 || (r == utf8.RuneError && size == 1) {
		return content
	}
	set := cs.G0
	if cs.GL == 1 {
		set = cs.G1
	}
	var table map[rune]rune
	switch set {
	case CharsetDECSpecial:
		table = decSpecialGraphics
	case CharsetUK:
		table = ukGraphics
	}
	if mapped, ok := table[r]; ok {
		return string(mapped) + content[size:]
	}
	return content
}

func init() {
	// IND, NEL and RI are C1 controls whose 7-bit forms are escape sequences.
	// The 8-bit forms (0x84, 0x85, 0x8d) need a parser hook; see the stream A
	// report.
	escTable[escKey{Final: 'D'}] = escIndexDown
	escTable[escKey{Final: 'E'}] = escNextLine
	escTable[escKey{Final: 'M'}] = escReverseIndexUp
}

// escIndexDown handles IND: down one row, scrolling at the bottom margin.
func escIndexDown(term *Term) {
	indexDown(term)
}

// escNextLine handles NEL: down one row, then to the left margin.
func escNextLine(term *Term) {
	indexDown(term)
	g := term.grid
	cur := g.Cursor()
	if cur.X < g.ScrollRegion().Left {
		// A cursor left of the left margin keeps its column, which is what
		// xterm does and what esctest2 asserts.
		return
	}
	g.SetCursor(homeColumn(g), cur.Y)
}

// escReverseIndexUp handles RI: up one row, scrolling at the top margin.
func escReverseIndexUp(term *Term) {
	reverseIndexUp(term)
}

// indexDown moves the cursor down one row. At the bottom margin it scrolls the
// region instead, but only when the cursor is inside the left and right
// margins. Outside them the cursor does not move.
func indexDown(term *Term) {
	g := term.grid
	cur := g.Cursor()
	r := g.ScrollRegion()
	switch {
	case cur.Y == r.Bottom && insideMargins(g):
		g.ScrollUp(1)
	case cur.Y == r.Bottom:
	case cur.Y < g.rows-1:
		cur.Y++
	}
	g.SetCursor(cur.X, cur.Y)
}

// reverseIndexUp moves the cursor up one row. At the top margin it scrolls the
// region instead, by the same left and right margin rule as indexDown.
func reverseIndexUp(term *Term) {
	g := term.grid
	cur := g.Cursor()
	r := g.ScrollRegion()
	switch {
	case cur.Y == r.Top && insideMargins(g):
		g.ScrollDown(1)
	case cur.Y == r.Top:
	case cur.Y > 0:
		cur.Y--
	}
	g.SetCursor(cur.X, cur.Y)
}

// insideMargins reports whether the cursor is inside the left and right
// margins.
func insideMargins(g *Grid) bool {
	cur := g.Cursor()
	r := g.ScrollRegion()
	return cur.X >= r.Left && cur.X <= r.Right
}
