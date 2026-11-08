package client

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"dm/internal/segment"

	"dm/internal/vt"
)

// railSeg is the name the rail gave a styled run before seg.go made the rail and
// the bar one type. No production file names it now. rail_test.go still spells it,
// so the alias lives here; the one edit it needs is railSeg -> segment.Seg.
type railSeg = segment.Seg

// panelChar is one cell of a replayed screen; 0 means nothing was painted there.
func panelChar(screen *vt.Term, x, y int) rune {
	r, _ := utf8.DecodeRuneInString(screen.Grid().Cell(x, y).Content)
	return r
}

// panelFilled is a screen with every cell set to X, so a test can tell a painted
// cell from an untouched one.
func panelFilled(t *testing.T, cols, rows int, raw []byte) *vt.Term {
	t.Helper()
	screen := vt.NewTerm(cols, rows)
	var fill strings.Builder
	for y := 0; y < rows; y++ {
		fmt.Fprintf(&fill, "\x1b[%d;1H%s", y+1, strings.Repeat("X", cols))
	}
	screen.Write([]byte(fill.String()))
	screen.Write(raw)
	screen.Flush()
	return screen
}
