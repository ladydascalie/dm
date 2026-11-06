package client

import (
	"bytes"

	"dm/internal/layout"
	"dm/internal/panel"

	"github.com/hinshun/vt10x"
)

// AddFloatingPane appends a floating pane to the client's render stack.
func (t *tui) AddFloatingPane(f *panel.FloatingPane) {
	t.floats = append(t.floats, f)
	t.dirty = true
}

// RemoveFloatingPane removes a floating pane by ID.
func (t *tui) RemoveFloatingPane(id string) {
	for i, f := range t.floats {
		if f.ID == id {
			t.floats = append(t.floats[:i], t.floats[i+1:]...)
			t.dirty = true
			return
		}
	}
}

// floatVTPaint is the content painter for a float backed by a terminal. The
// panel owns the box; this closure owns the cells, so the panel never imports
// the terminal or the pane painter.
func floatVTPaint(vt vt10x.Terminal, hover *selRange) func(*bytes.Buffer, layout.Rect) {
	return func(buf *bytes.Buffer, inner layout.Rect) {
		drawPaneFrom(buf, vt, inner, 0, nil, hover)
	}
}
