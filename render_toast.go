package main

import (
	"bytes"
	"dm/internal/cells"
	"dm/internal/wire"
	"fmt"
)

// drawToasts paints the toast stack in the bottom-right corner, just above the
// status bar: newest at the bottom, each toast one row inside a dim border. The
// box is as wide as the longest text plus its two border cells, clamped to the
// screen; a longer text is truncated to fit. Only rows above the bar are used,
// so the bar and the pointer gestures over the panes are untouched.
func drawToasts(buf *bytes.Buffer, th Theme, snap *wire.Snapshot, toasts []toast) {
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
			row, snap.Cols-w+1, th.Glyphs.BorderVertical, cells.Pad(x.text, inner), th.Glyphs.BorderVertical)
	}
}
