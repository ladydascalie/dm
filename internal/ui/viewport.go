// Package ui holds the primitives a view shares: a scrolling window over a list
// of rows and the text of one key hint. It is a pure library: it paints nothing,
// writes no escape sequence, and depends on no client. The panel styles the row
// it draws, and the caller owns every value here between frames.
package ui

// Viewport is one scrolling window over N rows: the selected row and the first
// visible row. The caller owns the value and sets N, H and Sel before each
// frame; the viewport keeps no rows of its own and paints nothing.
//
// The value is kept fit to paint after every method: Sel is either -1 or a row
// of the list, and Off is an offset the list can be scrolled to. Sel is -1 when
// nothing is selected, which is what an empty list shows and what a caller sets
// to clear the selection.
type Viewport struct {
	N   int // N is the row count.
	H   int // H is the window height in rows.
	Off int // Off is the index of the first visible row.
	Sel int // Sel is the selected row, -1 when nothing is selected.
}

// Ensure folds Sel into the list and then keeps the window on it. A selection
// above the window pulls the top of the window up to it; a selection below the
// window pulls the window down until the selection is its last row. The window
// is clamped last, so no move can leave a gap under it. An empty list has no
// selection.
func (v *Viewport) Ensure() {
	if v.N <= 0 || v.Sel < 0 {
		v.Sel = -1
	} else if v.Sel >= v.N {
		v.Sel = v.N - 1
	}
	if v.Sel >= 0 && v.H > 0 {
		if v.Off > v.Sel {
			v.Off = v.Sel
		}
		if v.Sel >= v.Off+v.H {
			v.Off = v.Sel - v.H + 1
		}
	}
	v.Clamp()
}

// Clamp keeps the window inside the list: Off lands in [0, max(0, N-H)]. A
// window too tall for the list or too short to hold a row starts at the top,
// so there is never a scroll position the list cannot show.
func (v *Viewport) Clamp() {
	maxOff := 0
	if v.N > 0 && v.H > 0 {
		maxOff = v.N - v.H
		if maxOff < 0 {
			maxOff = 0
		}
	}
	if v.Off < 0 {
		v.Off = 0
	}
	if v.Off > maxOff {
		v.Off = maxOff
	}
}

// Move steps the selection d rows, stops at either end of the list, and then
// ensures the window. A step of zero leaves the selection alone, so a view that
// sees no key keeps the row it had. A step from an empty selection lands on the
// row the arithmetic reaches, clamped: down from -1 is the first row.
func (v *Viewport) Move(d int) {
	if d != 0 {
		v.Sel = v.span(v.Sel + d)
	}
	v.Ensure()
}

// Page steps the selection d windows, so one page down moves H rows. It stops
// at either end of the list exactly as Move does. A window with no height has
// nothing to page by.
func (v *Viewport) Page(d int) {
	if v.H <= 0 {
		v.Ensure()
		return
	}
	v.Move(d * v.H)
}

// Top selects the first row, which also scrolls the window to the top. An empty
// list has no row to select.
func (v *Viewport) Top() {
	v.Sel = 0
	v.Ensure()
}

// Bottom selects the last row, which also scrolls the window to the end. An
// empty list has no row to select.
func (v *Viewport) Bottom() {
	v.Sel = v.N - 1
	v.Ensure()
}

// Visible reports whether row i is on screen. A row below the end of the list
// is never visible, however far the window reaches.
func (v *Viewport) Visible(i int) bool {
	return i >= v.Off && i < v.Off+v.H && i < v.N
}

// RowAt maps a screen row y in [0, H) to the list row it shows. It returns -1
// when y is outside the window or the row the window would show is past the end
// of the list.
func (v *Viewport) RowAt(y int) int {
	if y < 0 || y >= v.H {
		return -1
	}
	i := v.Off + y
	if i >= v.N {
		return -1
	}
	return i
}

// span clamps a row into the list. An empty list has no row to point at, so it
// answers with the empty selection.
func (v *Viewport) span(i int) int {
	if v.N <= 0 {
		return -1
	}
	if i < 0 {
		return 0
	}
	if i >= v.N {
		return v.N - 1
	}
	return i
}
