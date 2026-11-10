package client

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"dm/internal/vt"
)

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
