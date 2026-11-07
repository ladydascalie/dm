// Package shot rasterises a cell grid to a PNG.
//
// It is test-only. No non-test file may import it, so the embedded font never
// reaches the dm binary. A test in this package enforces that fence.
//
// The rasteriser is a terminal, not a cell printer: it advances the pen by the
// glyph's real width, so a wide glyph in a one-cell slot overlaps the next
// cell. That overlap is the width bug made visible, not a defect here.
package shot

// RGB is a 24-bit colour.
type RGB struct {
	R, G, B uint8
}

// Attr is a set of cell attributes. It holds only what dm emits today.
type Attr uint8

const (
	// Bold draws the glyph twice, one pixel apart.
	Bold Attr = 1 << iota
	// Underline draws a one-pixel rule under the glyph.
	Underline
	// Reverse swaps the cell's foreground and background.
	Reverse
)

// Cell is one grid slot. Content is the glyph text; an empty Content paints a
// space. Fg and Bg are the colours before any Reverse swap.
type Cell struct {
	Content string
	Fg      RGB
	Bg      RGB
	Attr    Attr
}

// Grid is a width, a height and a row-major cell lookup.
type Grid struct {
	W, H  int
	Cells []Cell
}

// NewGrid returns a W by H grid of empty cells.
func NewGrid(w, h int) *Grid {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	return &Grid{W: w, H: h, Cells: make([]Cell, w*h)}
}

// Cell returns the cell at x, y. A coordinate outside the grid returns the
// zero Cell.
func (g *Grid) Cell(x, y int) Cell {
	if x < 0 || y < 0 || x >= g.W || y >= g.H {
		return Cell{}
	}
	return g.Cells[y*g.W+x]
}

// Set stores c at x, y. A coordinate outside the grid is ignored.
func (g *Grid) Set(x, y int, c Cell) {
	if x < 0 || y < 0 || x >= g.W || y >= g.H {
		return
	}
	g.Cells[y*g.W+x] = c
}
