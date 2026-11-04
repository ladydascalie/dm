package main

import (
	"bytes"
	"fmt"

	"github.com/hinshun/vt10x"
)

// FloatingPane is a pane rendered at an arbitrary position over the main
// layout. It provides the primitive for scratch terminals, toasts, and other
// ephemeral UI elements without entering the session's split tree.
type FloatingPane struct {
	ID     string
	Rect   Rect
	VT     vt10x.Terminal
	Active bool
	// Title and Hint paint into the top and bottom border rows, clear of the
	// corners. A float that is not a terminal uses them to say what it is and
	// which keys it takes.
	Title string
	Hint  string
	// Paint, when set, writes the float's content itself and Draw skips the
	// VT path. It receives the content Rect, so a float with no terminal can
	// still paint rows inside the box.
	Paint func(buf *bytes.Buffer, inner Rect)
	last  paint
}

// Draw writes the pane's content and border into buf.
func (f *FloatingPane) Draw(buf *bytes.Buffer, hover *selRange) {
	if f.Paint != nil {
		f.Paint(buf, f.Rect)
	} else {
		drawPaneFrom(buf, f.VT, f.Rect, 0, nil, hover)
	}
	drawPaneBox(buf, f.Rect, f.Active)
	paintBorderText(buf, f.Rect, f.Rect.Y-1, f.Title, "\x1b[97m")
	paintBorderText(buf, f.Rect, f.Rect.Y+f.Rect.H, f.Hint, "\x1b[90m")
}

// paintBorderText writes text into row y of the box around r, one cell clear of
// the left corner and clipped to the interior so a label never eats a corner.
func paintBorderText(buf *bytes.Buffer, r Rect, y int, text, style string) {
	if text == "" || r.W < 3 || y < 0 {
		return
	}
	text = clipCells(text, r.W-2)
	if text == "" {
		return
	}
	fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m%s%s\x1b[0m", y+1, r.X+2, style, text)
}
