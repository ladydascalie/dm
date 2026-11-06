package client

import (
	"fmt"
	"strings"
	"testing"

	"dm/internal/segment"

	"github.com/hinshun/vt10x"
)

// railSeg is the name the rail gave a styled run before seg.go made the rail and
// the bar one type. No production file names it now. rail_test.go still spells it,
// so the alias lives here; the one edit it needs is railSeg -> segment.Seg.
type railSeg = segment.Seg

// panelChar is one cell of a replayed screen; 0 means nothing was painted there.
func panelChar(screen vt10x.Terminal, x, y int) rune {
	screen.Lock()
	defer screen.Unlock()
	return screen.Cell(x, y).Char
}

// panelFilled is a screen with every cell set to X, so a test can tell a painted
// cell from an untouched one.
func panelFilled(t *testing.T, cols, rows int, raw []byte) vt10x.Terminal {
	t.Helper()
	screen := vt10x.New(vt10x.WithSize(cols, rows))
	var fill strings.Builder
	for y := 0; y < rows; y++ {
		fmt.Fprintf(&fill, "\x1b[%d;1H%s", y+1, strings.Repeat("X", cols))
	}
	if _, err := screen.Write([]byte(fill.String())); err != nil {
		t.Fatalf("fill the screen: %v", err)
	}
	if _, err := screen.Write(raw); err != nil {
		t.Fatalf("replay the panel: %v", err)
	}
	return screen
}
