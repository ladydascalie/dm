package main

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/hinshun/vt10x"
)

// railSeg is the name the rail gave a styled run before seg.go made the rail and
// the bar one type. No production file names it now. rail_test.go still spells it
// and is outside this unit's fence, so the name lives here until that file is in
// a fence; the one edit it needs is railSeg -> seg.
type railSeg = seg

// panelChar is one cell of a replayed screen; 0 means nothing was painted there.
func panelChar(screen vt10x.Terminal, x, y int) rune {
	screen.Lock()
	defer screen.Unlock()
	return screen.Cell(x, y).Char
}

// panelRow is one row of a replayed screen as a string, trailing blanks dropped.
func panelRow(screen vt10x.Terminal, y, x, w int) string {
	var b strings.Builder
	for i := 0; i < w; i++ {
		c := panelChar(screen, x+i, y)
		if c == 0 {
			c = ' '
		}
		b.WriteRune(c)
	}
	return strings.TrimRight(b.String(), " ")
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

// panelScreen replays what draw paints into a fresh screen.
func panelScreen(t *testing.T, cols, rows int, draw func(*bytes.Buffer)) vt10x.Terminal {
	t.Helper()
	var b bytes.Buffer
	draw(&b)
	screen := vt10x.New(vt10x.WithSize(cols, rows))
	if _, err := screen.Write(b.Bytes()); err != nil {
		t.Fatalf("replay the panel: %v", err)
	}
	return screen
}

// TestPanelWithoutTitle paints a box that carries no label: the whole top and
// bottom rows are border, and the interior painter's text sits inside.
func TestPanelWithoutTitle(t *testing.T) {
	r := Rect{X: 3, Y: 2, W: 6, H: 3}
	screen := panelScreen(t, 20, 10, func(b *bytes.Buffer) {
		paintPanel(b, r, "", "", true, func(w *bytes.Buffer, inner Rect) {
			fmt.Fprintf(w, "\x1b[%d;%dHAB", inner.Y+1, inner.X+1)
		})
	})
	if got, want := panelRow(screen, r.Y-1, r.X-1, r.W+2), glyphBorderTopLeft+strings.Repeat(glyphBorderHorizontal, r.W)+glyphBorderTopRight; got != want {
		t.Fatalf("top row = %q, want %q", got, want)
	}
	if got, want := panelRow(screen, r.Y+r.H, r.X-1, r.W+2), glyphBorderBottomLeft+strings.Repeat(glyphBorderHorizontal, r.W)+glyphBorderBottomRight; got != want {
		t.Fatalf("bottom row = %q, want %q", got, want)
	}
	if got := panelRow(screen, r.Y, r.X, r.W); got != "AB" {
		t.Fatalf("first interior row = %q, want the painted text", got)
	}
	if got := panelChar(screen, r.X-1, r.Y); got != []rune(glyphBorderVertical)[0] {
		t.Fatalf("left border = %q, want the vertical glyph", got)
	}
	if got := panelChar(screen, r.X+r.W, r.Y); got != []rune(glyphBorderVertical)[0] {
		t.Fatalf("right border = %q, want the vertical glyph", got)
	}
}

// TestPanelTitleAndHint paints both labels on the border rows, one cell clear of
// each corner, so neither label eats a corner.
func TestPanelTitleAndHint(t *testing.T) {
	r := Rect{X: 3, Y: 2, W: 8, H: 3}
	screen := panelScreen(t, 20, 10, func(b *bytes.Buffer) {
		paintPanel(b, r, "title", "hint", false, nil)
	})
	if got, want := panelRow(screen, r.Y-1, r.X-1, r.W+2), "┌─title──┐"; got != want {
		t.Fatalf("title row = %q, want %q", got, want)
	}
	if got, want := panelRow(screen, r.Y+r.H, r.X-1, r.W+2), "└─hint───┘"; got != want {
		t.Fatalf("hint row = %q, want %q", got, want)
	}
}

// TestPanelDropsTitleWhenNarrow drops a label that cannot fit between the
// corners, rather than letting it cover one.
func TestPanelDropsTitleWhenNarrow(t *testing.T) {
	r := Rect{X: 3, Y: 2, W: 2, H: 2}
	screen := panelScreen(t, 20, 10, func(b *bytes.Buffer) {
		paintPanel(b, r, "title", "hint", true, nil)
	})
	if got, want := panelRow(screen, r.Y-1, r.X-1, r.W+2), "┌──┐"; got != want {
		t.Fatalf("top row = %q, want the plain border %q", got, want)
	}
	if got, want := panelRow(screen, r.Y+r.H, r.X-1, r.W+2), "└──┘"; got != want {
		t.Fatalf("bottom row = %q, want the plain border %q", got, want)
	}
}

// TestPanelTruncatesTitleAtThreeWide pins what a three-wide interior does with a
// title longer than its one cell. The base float painter cut the title to fit,
// so the panel keeps that behaviour as intended: the title is truncated to the
// single cell between the border runes, never dropped and never over a corner.
// Dropping it instead would move bytes for every three-wide float with a title.
func TestPanelTruncatesTitleAtThreeWide(t *testing.T) {
	r := Rect{X: 4, Y: 3, W: 3, H: 2}
	var b bytes.Buffer
	paintPanel(&b, r, "title", "", false, nil)
	screen := panelFilled(t, 20, 10, b.Bytes())
	if got, want := panelRow(screen, r.Y-1, r.X-1, r.W+2), "┌─t─┐"; got != want {
		t.Fatalf("title row = %q, want the title cut to its one cell %q", got, want)
	}
	if got, want := panelRow(screen, r.Y+r.H, r.X-1, r.W+2), "└───┘"; got != want {
		t.Fatalf("bottom row = %q, want the plain border %q", got, want)
	}
	for y := 0; y < 10; y++ {
		for x := 0; x < 20; x++ {
			in := x >= r.X-1 && x <= r.X+r.W && y >= r.Y-1 && y <= r.Y+r.H
			if !in {
				if got := panelChar(screen, x, y); got != 'X' {
					t.Fatalf("cell %d,%d outside the box = %q", x, y, got)
				}
			}
		}
	}
}

// TestPanelClipsInterior runs an interior painter that writes past its rect on
// every side. The panel keeps the border, the cells beside it, and the moves the
// painter aimed outside the box.
func TestPanelClipsInterior(t *testing.T) {
	r := Rect{X: 4, Y: 3, W: 4, H: 2}
	var b bytes.Buffer
	paintPanel(&b, r, "T", "", true, func(w *bytes.Buffer, inner Rect) {
		// Past the right edge, the row would run over the border.
		fmt.Fprintf(w, "\x1b[%d;%dHABCDEFG", inner.Y+1, inner.X+1)
		// Above, below and left of the box.
		fmt.Fprintf(w, "\x1b[%d;%dHXXXX", inner.Y, inner.X-1)
		fmt.Fprintf(w, "\x1b[%d;%dHYYYY", inner.Y+inner.H+1, inner.X)
		fmt.Fprintf(w, "\x1b[%d;%dHZZ", inner.Y+1, inner.X-1)
	})
	screen := panelFilled(t, 20, 10, b.Bytes())
	if got, want := panelRow(screen, r.Y-1, r.X-1, r.W+2), "┌─T──┐"; got != want {
		t.Fatalf("title row = %q, want the border intact %q", got, want)
	}
	if got, want := panelRow(screen, r.Y+r.H, r.X-1, r.W+2), "└────┘"; got != want {
		t.Fatalf("bottom row = %q, want the border intact %q", got, want)
	}
	if got := panelRow(screen, r.Y, r.X-1, r.W+2); got != "│ABCD│" {
		t.Fatalf("interior row = %q, want the text cut at the edge", got)
	}
	for y := 0; y < 10; y++ {
		for x := 0; x < 20; x++ {
			in := x >= r.X-1 && x <= r.X+r.W && y >= r.Y-1 && y <= r.Y+r.H
			got := panelChar(screen, x, y)
			if !in && got != 'X' {
				t.Fatalf("cell %d,%d outside the box = %q", x, y, got)
			}
		}
	}
	// No move reaches outside the box: the painter's out-of-box moves are gone
	// with the text that followed them.
	for _, m := range panelMoveRe.FindAllSubmatch(b.Bytes(), -1) {
		y, _ := strconv.Atoi(string(m[1]))
		x, _ := strconv.Atoi(string(m[2]))
		if x-1 < r.X-1 || x-1 > r.X+r.W || y-1 < r.Y-1 || y-1 > r.Y+r.H {
			t.Fatalf("a move lands at %d,%d outside the box %+v", x-1, y-1, r)
		}
	}
}

// panelMoveRe finds every absolute cursor move a panel writes.
var panelMoveRe = regexp.MustCompile("\x1b\\[([0-9]+);([0-9]+)H")
