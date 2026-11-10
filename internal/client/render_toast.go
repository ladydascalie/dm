package client

import (
	"bytes"
	"dm/internal/cells"
	"dm/internal/theme"
	"dm/internal/wire"
	"fmt"
	"strings"
)

// toastMaxWidth caps a toast's interior in cells, so one long message cannot
// stretch the box across the screen.
const toastMaxWidth = 40

// drawToasts paints the toast stack in the bottom-right corner, just above the
// status bar: newest at the bottom, each toast a small closed box with a
// border on all four sides and its text row inside. The box is tight, as wide
// as its own text plus the two border cells, clamped first to toastMaxWidth
// and then to the screen; a longer text is truncated with an ellipsis on a
// rune boundary. Boxes sit a one-row gap apart so the stack reads as separate
// popups. Only rows above the bar are used, and a stack that does not fit
// above the bar drops its oldest boxes rather than overflowing, so the bar and
// the pointer gestures over the panes are untouched.
func drawToasts(buf *bytes.Buffer, th theme.Theme, snap *wire.Snapshot, toasts []toast) {
	if snap == nil || len(toasts) == 0 || snap.Cols < 3 || snap.Rows < 2 {
		return
	}
	// Each box takes its three rows plus the one-row gap above it, and the
	// newest box rests on the row just above the bar. Walking the stack from
	// the newest toast down and stopping when the next top border would climb
	// past row 1 drops the oldest boxes instead of painting over the bar.
	g := th.Glyphs
	bottom := snap.Rows - 1
	for i := len(toasts) - 1; i >= 0; i-- {
		top := bottom - 2
		if top < 1 {
			return
		}
		inner := cells.Cells(toasts[i].text)
		if inner > toastMaxWidth {
			inner = toastMaxWidth
		}
		if inner > snap.Cols-2 {
			inner = snap.Cols - 2
		}
		text := cells.Pad(cells.Elide(toasts[i].text, inner), inner)
		col := snap.Cols - inner - 2 + 1
		bar := strings.Repeat(g.BorderHorizontal, inner)
		fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m\x1b[90m%s%s%s\x1b[0m",
			top, col, g.BorderTopLeft, bar, g.BorderTopRight)
		fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m\x1b[90m%s\x1b[97m%s\x1b[0m\x1b[90m%s\x1b[0m",
			top+1, col, g.BorderVertical, text, g.BorderVertical)
		fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m\x1b[90m%s%s%s\x1b[0m",
			bottom, col, g.BorderBottomLeft, bar, g.BorderBottomRight)
		bottom = top - 2
	}
}
