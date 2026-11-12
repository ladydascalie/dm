package panel

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/theme"
	"dm/internal/vt"
)

// panelChar is one cell of a replayed screen; an empty string means nothing was
// painted there.
func panelChar(screen *vt.Term, x, y int) string {
	return screen.Grid().Cell(x, y).Content
}

// panelRow is one row of a replayed screen as a string, trailing blanks dropped.
func panelRow(screen *vt.Term, y, x, w int) string {
	var b strings.Builder
	for i := 0; i < w; i++ {
		c := panelChar(screen, x+i, y)
		if c == "" {
			c = " "
		}
		b.WriteString(c)
	}
	return strings.TrimRight(b.String(), " ")
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

// panelScreen replays what draw paints into a fresh screen.
func panelScreen(t *testing.T, cols, rows int, draw func(*bytes.Buffer)) *vt.Term {
	t.Helper()
	var b bytes.Buffer
	draw(&b)
	screen := vt.NewTerm(cols, rows)
	screen.Write(b.Bytes())
	screen.Flush()
	return screen
}

// TestPanelWithoutTitle paints a box that carries no label: the whole top and
// bottom rows are border, and the interior painter's text sits inside.
func TestPanelWithoutTitle(t *testing.T) {
	r := layout.Rect{X: 3, Y: 2, W: 6, H: 3}
	screen := panelScreen(t, 20, 10, func(b *bytes.Buffer) {
		PaintPanel(b, theme.DefaultTheme(), r, PanelTitle{}, "", true, func(w *bytes.Buffer, inner layout.Rect) {
			fmt.Fprintf(w, "\x1b[%d;%dHAB", inner.Y+1, inner.X+1)
		})
	})
	if got, want := panelRow(screen, r.Y-1, r.X-1, r.W+2), theme.DefaultTheme().Glyphs.BorderTopLeft+strings.Repeat(theme.DefaultTheme().Glyphs.BorderHorizontal, r.W)+theme.DefaultTheme().Glyphs.BorderTopRight; got != want {
		t.Fatalf("top row = %q, want %q", got, want)
	}
	if got, want := panelRow(screen, r.Y+r.H, r.X-1, r.W+2), theme.DefaultTheme().Glyphs.BorderBottomLeft+strings.Repeat(theme.DefaultTheme().Glyphs.BorderHorizontal, r.W)+theme.DefaultTheme().Glyphs.BorderBottomRight; got != want {
		t.Fatalf("bottom row = %q, want %q", got, want)
	}
	if got := panelRow(screen, r.Y, r.X, r.W); got != "AB" {
		t.Fatalf("first interior row = %q, want the painted text", got)
	}
	if got := panelChar(screen, r.X-1, r.Y); got != theme.DefaultTheme().Glyphs.BorderVertical {
		t.Fatalf("left border = %q, want the vertical glyph", got)
	}
	if got := panelChar(screen, r.X+r.W, r.Y); got != theme.DefaultTheme().Glyphs.BorderVertical {
		t.Fatalf("right border = %q, want the vertical glyph", got)
	}
}

// TestPanelTitleAndHint paints both labels on the border rows, one cell clear of
// each corner, so neither label eats a corner.
func TestPanelTitleAndHint(t *testing.T) {
	r := layout.Rect{X: 3, Y: 2, W: 8, H: 3}
	screen := panelScreen(t, 20, 10, func(b *bytes.Buffer) {
		PaintPanel(b, theme.DefaultTheme(), r, PanelTitle{Text: "title", Style: PanelTitleStyle}, "hint", false, nil)
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
	r := layout.Rect{X: 3, Y: 2, W: 2, H: 2}
	screen := panelScreen(t, 20, 10, func(b *bytes.Buffer) {
		PaintPanel(b, theme.DefaultTheme(), r, PanelTitle{Text: "title", Style: PanelTitleStyle}, "hint", true, nil)
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
	r := layout.Rect{X: 4, Y: 3, W: 3, H: 2}
	var b bytes.Buffer
	PaintPanel(&b, theme.DefaultTheme(), r, PanelTitle{Text: "title", Style: PanelTitleStyle}, "", false, nil)
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
				if got := panelChar(screen, x, y); got != "X" {
					t.Fatalf("cell %d,%d outside the box = %q", x, y, got)
				}
			}
		}
	}
}

// TestPanelFillTitleRowWritesOnce paints the menu look: a dim row continued
// with the border rune. The whole row is one write, corners included, and the
// box pass leaves it alone, so its cells and its bytes are the caller's.
func TestPanelFillTitleRowWritesOnce(t *testing.T) {
	r := layout.Rect{X: 4, Y: 3, W: 10, H: 2}
	var b bytes.Buffer
	PaintPanel(&b, theme.DefaultTheme(), r, PanelTitle{
		Text:  " title ",
		Style: "\x1b[0m\x1b[90m",
		Fill:  theme.DefaultTheme().Glyphs.BorderHorizontal,
	}, "", false, nil)
	out := b.String()
	// The style carries the panel ground, so the fill glyphs are opaque too.
	if want := "\x1b[0m\x1b[90m" + theme.DefaultTheme().Palette.RailBG.BG() + "\x1b[3;4H┌ title ───┐"; !strings.HasPrefix(out, want) {
		t.Fatalf("band starts %q, want the row in one write %q", out, want)
	}
	if n := strings.Count(out, "\x1b[3;"); n != 1 {
		t.Fatalf("the top row takes %d moves, want the row in one write", n)
	}
	if n := strings.Count(out, "┌ title ───┐"); n != 1 {
		t.Fatalf("the row appears %d times, want 1", n)
	}
	if n := strings.Count(out, " title "); n != 1 {
		t.Fatalf("label appears %d times in the band, want 1", n)
	}
	screen := panelFilled(t, 20, 10, b.Bytes())
	if got, want := panelRow(screen, r.Y-1, r.X-1, r.W+2), "┌ title ───┐"; got != want {
		t.Fatalf("title row = %q, want %q", got, want)
	}
	if got, want := panelRow(screen, r.Y+r.H, r.X-1, r.W+2), "└──────────┘"; got != want {
		t.Fatalf("bottom row = %q, want %q", got, want)
	}
}

// TestPanelClipsInterior runs an interior painter that writes past its rect on
// every side. The panel keeps the border, the cells beside it, and the moves the
// painter aimed outside the box.
func TestPanelClipsInterior(t *testing.T) {
	r := layout.Rect{X: 4, Y: 3, W: 4, H: 2}
	var b bytes.Buffer
	PaintPanel(&b, theme.DefaultTheme(), r, PanelTitle{Text: "T", Style: PanelTitleStyle}, "", true, func(w *bytes.Buffer, inner layout.Rect) {
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
			if !in && got != "X" {
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

// panelMovesInGrid returns the one-based moves a buffer writes, failing on any
// that lands outside a cols x rows grid.
func panelMovesInGrid(t *testing.T, b []byte, cols, rows int) [][2]int {
	t.Helper()
	var out [][2]int
	for _, m := range panelMoveRe.FindAllSubmatch(b, -1) {
		y, _ := strconv.Atoi(string(m[1]))
		x, _ := strconv.Atoi(string(m[2]))
		if x < 1 || x > cols || y < 1 || y > rows {
			t.Fatalf("a move lands at row %d, col %d outside the %dx%d grid", y, x, cols, rows)
		}
		out = append(out, [2]int{y, x})
	}
	return out
}

// panelHas reports whether moves holds the one-based address (row, col).
func panelHas(moves [][2]int, row, col int) bool {
	for _, m := range moves {
		if m[0] == row && m[1] == col {
			return true
		}
	}
	return false
}

// TestDrawPaneBoxOnGridClips keeps the border cells that land on the grid and
// drops the rest. The box around content at Y=15, H=10 runs to row 26 and
// column 13; pre-fix the bottom border wrote row 26, col 2. The on-screen
// vertical at row 20, col 2 must survive.
func TestDrawPaneBoxOnGridClips(t *testing.T) {
	content := layout.Rect{X: 2, Y: 15, W: 10, H: 10}
	var b bytes.Buffer
	DrawPaneBoxOnGrid(&b, theme.DefaultTheme(), content, false, 100, 20)
	if b.Len() == 0 {
		t.Fatal("a partially visible box wrote nothing")
	}
	moves := panelMovesInGrid(t, b.Bytes(), 100, 20)
	if !panelHas(moves, 20, 2) {
		t.Fatalf("the on-screen border cell at row 20, col 2 was dropped: %v", moves)
	}
}

// TestDrawPanePaddingOnGridClips keeps the ring cells that land on the grid.
// The ring around content at Y=15, H=10 runs to row 26; pre-fix it wrote row
// 21, col 2, the first row past a 20-row grid. The on-screen ring at row 20,
// col 2 must survive.
func TestDrawPanePaddingOnGridClips(t *testing.T) {
	content := layout.Rect{X: 2, Y: 15, W: 10, H: 10}
	box := layout.BoxRect(content, 1)
	var b bytes.Buffer
	DrawPanePaddingOnGrid(&b, box, content, 100, 20)
	if b.Len() == 0 {
		t.Fatal("a partially visible ring wrote nothing")
	}
	moves := panelMovesInGrid(t, b.Bytes(), 100, 20)
	if !panelHas(moves, 20, 2) {
		t.Fatalf("the on-screen ring cell at row 20, col 2 was dropped: %v", moves)
	}
}
