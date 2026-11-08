package panel

import (
	"bytes"

	"dm/internal/layout"
	"dm/internal/theme"
	"dm/internal/vt"
)

// FloatingPane is a pane rendered at an arbitrary position over the main
// layout. It provides the primitive for scratch terminals, toasts, and other
// ephemeral UI elements without entering the session's split tree.
type FloatingPane struct {
	ID     string
	Rect   layout.Rect
	VT     *vt.Term
	Active bool
	// Title and Hint paint into the top and bottom border rows, clear of the
	// corners. A float that is not a terminal uses them to say what it is and
	// which keys it takes.
	Title string
	Hint  string
	// Paint, when set, writes the float's content itself. It receives the
	// content Rect, so a float with no terminal can still paint rows inside
	// the box. The caller owns the content: the panel never reads VT.
	Paint func(buf *bytes.Buffer, inner layout.Rect)
}

// Draw writes the pane's content and border into buf. The content comes from
// Paint alone; a float with no Paint paints its border and nothing inside.
func (f *FloatingPane) Draw(buf *bytes.Buffer, th theme.Theme) {
	PaintPanel(buf, th, f.Rect, PanelTitle{Text: f.Title, Style: PanelTitleStyle}, f.Hint, f.Active, f.Paint)
}
