package main

import (
	"bytes"

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
	paint := func(b *bytes.Buffer, inner Rect) {
		drawPaneFrom(b, f.VT, f.Rect, 0, nil, hover)
	}
	if f.Paint != nil {
		paint = f.Paint
	}
	paintPanel(buf, f.Rect, panelTitle{text: f.Title, style: panelTitleStyle}, f.Hint, f.Active, paint)
}
