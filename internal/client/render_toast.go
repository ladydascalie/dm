package client

import (
	"bytes"
	"dm/internal/cells"
	"dm/internal/theme"
	"dm/internal/wire"
	"fmt"
)

// toastMaxWidth caps a toast's interior in cells, so one long message cannot
// stretch the box across the screen.
const toastMaxWidth = 40

// drawToasts paints the toast stack in the bottom-right corner, just above the
// status bar: newest at the bottom, each toast one row inside a dim border. The
// box is as wide as the longest text plus its two border cells, clamped first
// to toastMaxWidth and then to the screen; a longer text is truncated with an
// ellipsis. Only rows above the bar are used, so the bar and the pointer
// gestures over the panes are untouched.
func drawToasts(buf *bytes.Buffer, th theme.Theme, snap *wire.Snapshot, toasts []toast) {
	if snap == nil || len(toasts) == 0 || snap.Cols < 3 || snap.Rows < 2 {
		return
	}
	longest := 0
	for _, x := range toasts {
		if n := cells.Cells(x.text); n > longest {
			longest = n
		}
	}
	w := longest + 2
	if w > toastMaxWidth+2 {
		w = toastMaxWidth + 2
	}
	if w > snap.Cols {
		w = snap.Cols
	}
	inner := w - 2
	for i, x := range toasts {
		row := snap.Rows - 1 - (len(toasts) - 1 - i)
		if row < 1 {
			continue
		}
		fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m\x1b[90m%s\x1b[97m%s\x1b[0m\x1b[90m%s\x1b[0m",
			row, snap.Cols-w+1, th.Glyphs.BorderVertical, cells.Pad(cells.Elide(x.text, inner), inner), th.Glyphs.BorderVertical)
	}
}
