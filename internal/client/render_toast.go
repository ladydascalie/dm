package client

import (
	"bytes"
	"fmt"
	"strings"

	"dm/internal/cells"
	"dm/internal/theme"
	"dm/internal/wire"
)

// toastMaxWidth caps a toast's interior in cells, so one long message cannot
// stretch the box across the screen.
const toastMaxWidth = 40

// toastLine is one content row of a toast: its text and the colour it paints in.
type toastLine struct {
	text string
	fg   string
}

// toastBox is one toast laid out for the screen: the title the top border
// carries, the content rows, the colour the border and the title share, and the
// interior width in cells. The top and bottom borders bracket one row per
// content row.
type toastBox struct {
	title  string
	border string
	rows   []toastLine
	width  int
}

// newToastBox lays one toast out for a screen maxCols wide. A notify toast names
// its state in the title bar in the state's colour, and the box draws the
// entry's topic dim above the bright message. A plain toast carries its message
// alone on a dim border, so a client message keeps the plain shape it had.
func newToastBox(th theme.Theme, x toast, maxCols int) toastBox {
	dim := th.Palette.RailDim.FG()
	b := toastBox{border: dim}
	if x.state != "" {
		st, ok := th.States[x.state]
		if !ok {
			st = th.States["unknown"]
		}
		b.title = st.Glyph
		if b.title != "" {
			b.title += " "
		}
		b.title += x.state
		b.border = st.Colour.FG()
	}
	if x.topic != "" {
		b.rows = append(b.rows, toastLine{text: x.topic, fg: dim})
	}
	if x.text != "" {
		b.rows = append(b.rows, toastLine{text: x.text, fg: th.Palette.RailText.FG()})
	}
	// The box is as wide as its widest row; a title keeps its one-cell margin on
	// each side, so it asks for two cells more than its text.
	b.width = cells.Cells(b.title)
	if b.title != "" {
		b.width += 2
	}
	for _, r := range b.rows {
		if n := cells.Cells(r.text); n > b.width {
			b.width = n
		}
	}
	if b.width > toastMaxWidth {
		b.width = toastMaxWidth
	}
	if b.width > maxCols-2 {
		b.width = maxCols - 2
	}
	return b
}

// borderTitle fits the title into the interior, elided when the screen is
// narrower than the state the title names. It returns "" when no title fits.
func (b toastBox) borderTitle() string {
	room := b.width - 2
	if room < cells.Cells(cells.Ellipsis) {
		return ""
	}
	return cells.Elide(b.title, room)
}

// draw paints the box with its top border on 1-based row top, its bottom border
// on row bottom and its left border in column col. Each row is placed
// absolutely, so a box never depends on the cursor the previous one left.
func (b toastBox) draw(buf *bytes.Buffer, th theme.Theme, col, top, bottom int) {
	g := th.Glyphs
	title := b.borderTitle()
	bar := strings.Repeat(g.BorderHorizontal, b.width)
	if title == "" {
		fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m%s%s%s%s\x1b[0m", top, col, b.border,
			g.BorderTopLeft, bar, g.BorderTopRight)
	} else {
		// The title takes the middle of the top border, with one cell of
		// horizontal on each side, and the bar fills what is left.
		fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m%s%s %s %s%s\x1b[0m", top, col, b.border,
			g.BorderTopLeft, title,
			strings.Repeat(g.BorderHorizontal, b.width-cells.Cells(title)-2), g.BorderTopRight)
	}
	for i, r := range b.rows {
		text := cells.Pad(cells.Elide(r.text, b.width), b.width)
		fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m%s%s\x1b[0m%s%s\x1b[0m%s%s\x1b[0m",
			top+1+i, col, b.border, g.BorderVertical, r.fg, text, b.border, g.BorderVertical)
	}
	fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m%s%s%s%s\x1b[0m", bottom, col, b.border,
		g.BorderBottomLeft, bar, g.BorderBottomRight)
}

// drawToasts paints the toast stack in the bottom-right corner, just above the
// status bar: newest at the bottom, each toast a small closed box with the
// state's mark and name in the top border, the topic and the message inside. A
// box is as wide as its widest row, clamped first to toastMaxWidth and then to
// the screen; a longer row is truncated with an ellipsis on a cluster boundary.
// Boxes sit a one-row gap apart so the stack reads as separate popups. Only rows
// above the bar are used, and a stack that does not fit above the bar drops its
// oldest boxes rather than overflowing, so the bar and the pointer gestures over
// the panes are untouched.
func drawToasts(buf *bytes.Buffer, th theme.Theme, snap *wire.Snapshot, toasts []toast) {
	if snap == nil || len(toasts) == 0 || snap.Cols < 3 || snap.Rows < 3 {
		return
	}
	// Each box takes its own rows plus the one-row gap above it, and the newest
	// box rests on the row just above the bar. Walking the stack from the newest
	// toast down and stopping when the next top border would climb past row 1
	// drops the oldest boxes instead of painting over the bar.
	bottom := snap.Rows - 1
	for i := len(toasts) - 1; i >= 0; i-- {
		b := newToastBox(th, toasts[i], snap.Cols)
		if b.width < 1 {
			continue
		}
		top := bottom - len(b.rows) - 1
		if top < 1 {
			return
		}
		b.draw(buf, th, snap.Cols-b.width-1, top, bottom)
		bottom = top - 2
	}
}
