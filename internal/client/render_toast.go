package client

import (
	"bytes"
	"fmt"
	"math"
	"strings"

	"dm/internal/cells"
	"dm/internal/theme"
	"dm/internal/wire"
)

// toastWidth is the interior width of every toast in cells. A fixed width keeps
// the box, the stack and the gauge in one place: a short message pads out and a
// long one elides, so a toast never changes size under the operator's eye.
const toastWidth = 40

// toastLine is one content row of a toast: its text and the colour it paints in.
type toastLine struct {
	text string
	fg   string
}

// toastBox is one toast laid out for the screen: the title the top border
// carries, the content rows, the colour the border and the title share, the
// colour the gauge draws the life left in, the colour of the spent track, the
// panel ground every cell of the box carries, and the interior width in cells.
// The top border carries the title, the bottom border is the gauge, and the left
// fraction says how much of it is still coloured.
type toastBox struct {
	title  string
	border string // border foreground escape
	gauge  string // gauge foreground escape, the life still to run
	track  string // the spent part of the gauge
	bg     string // panel background escape
	rows   []toastLine
	width  int     // interior width in cells
	left   float64 // fraction of the toast's life still to run, 0..1
}

// newToastBox lays one toast out for a screen maxCols wide on the float ground,
// so a stack of three never reads as three coloured blocks. Every box is the same
// three rows: the title in the top border, one message row, the gauge in the
// bottom border. A notify toast names its state and the entry's topic in the top
// border, in the state's colour, and draws the gauge in the same colour: the
// bottom border is the track, and the run still to live is the bright part of it.
// A plain toast keeps the dim border and a dim gauge, and carries its message
// alone.
func newToastBox(th theme.Theme, x toast, maxCols int) toastBox {
	dim := th.Palette.RailDim.FG()
	b := toastBox{
		border: dim,
		gauge:  dim,
		track:  dim,
		bg:     th.Palette.RailFocusBG.BG(),
		left:   x.left,
	}
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
		if x.topic != "" {
			b.title += " " + x.topic
		}
		b.border = st.Colour.FG()
		b.gauge = st.Colour.FG()
	}
	// The message always takes the one interior row, empty or not, so every
	// toast is the same three rows and the stack never moves.
	b.rows = []toastLine{{text: x.text, fg: th.Palette.RailText.FG()}}
	b.width = toastWidth
	if b.width > maxCols-2 {
		b.width = maxCols - 2
	}
	return b
}

// borderTitle fits the title into the interior, elided when the screen is
// narrower than the state and topic it names. It returns "" when no title fits.
func (b toastBox) borderTitle() string {
	room := b.width - 2
	if room < cells.Cells(cells.Ellipsis) {
		return ""
	}
	return cells.Elide(b.title, room)
}

// filled is how many cells of the gauge are still coloured: the whole track while
// the toast is fresh, one cell at the end of its life, none once it has run out.
// A toast that reaches none is dropped by the expiry, so the gauge never empties
// on screen.
func (b toastBox) filled() int {
	if b.left <= 0 {
		return 0
	}
	n := int(math.Round(b.left * float64(b.width)))
	if n > b.width {
		return b.width
	}
	if n < 1 {
		return 1
	}
	return n
}

// draw paints the box with its top border on 1-based row top, its bottom border
// on row bottom and its left border in column col. Each row is placed
// absolutely, so a box never depends on the cursor the previous one left. Every
// cell of the box carries the panel ground, so a toast reads as one panel over
// the panes and no cell of the picture under it shows through.
func (b toastBox) draw(buf *bytes.Buffer, th theme.Theme, col, top, bottom int) {
	g := th.Glyphs
	title := b.borderTitle()
	bar := strings.Repeat(g.BorderHorizontal, b.width)
	if title == "" {
		fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m%s%s%s%s%s\x1b[0m", top, col, b.bg, b.border,
			g.BorderTopLeft, bar, g.BorderTopRight)
	} else {
		// The title takes the middle of the top border, with one cell of
		// horizontal on each side, and the bar fills what is left.
		fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m%s%s%s %s %s%s\x1b[0m", top, col, b.bg, b.border,
			g.BorderTopLeft, title,
			strings.Repeat(g.BorderHorizontal, b.width-cells.Cells(title)-2), g.BorderTopRight)
	}
	for i, r := range b.rows {
		text := cells.Pad(cells.Elide(r.text, b.width), b.width)
		fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m%s%s%s\x1b[0m%s%s%s\x1b[0m%s%s%s\x1b[0m",
			top+1+i, col,
			b.bg, b.border, g.BorderVertical,
			b.bg, r.fg, text,
			b.bg, b.border, g.BorderVertical)
	}
	// The bottom border is the gauge: one track as wide as the interior, the run
	// still to live in the state's colour and the rest spent and dim. The track
	// is what makes the fraction readable: a bright run alone reads as a stray
	// mark once it is short.
	fill := b.filled()
	fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m%s%s%s", bottom, col, b.bg, b.border, g.BorderBottomLeft)
	run := func(n int, fg string) {
		if n > 0 {
			fmt.Fprintf(buf, "\x1b[0m%s%s%s", b.bg, fg, strings.Repeat(g.BorderHorizontal, n))
		}
	}
	if b.gauge == b.track {
		run(b.width, b.track)
	} else {
		run(fill, b.gauge)
		run(b.width-fill, b.track)
	}
	fmt.Fprintf(buf, "\x1b[0m%s%s%s\x1b[0m", b.bg, b.border, g.BorderBottomRight)
}

// drawToasts paints the toast stack in the bottom-right corner, just above the
// status bar: newest at the bottom, each toast a fixed-size closed box on the
// float ground, the state's mark, name and topic in the top border, the message
// inside and the life it has left drawn along the bottom border. Every box is
// toastWidth cells wide, clamped to the screen, and three rows tall; a longer
// title or message is truncated with an ellipsis on a cluster boundary. Boxes sit
// a one-row gap apart so the stack reads as separate popups. Only rows above the
// bar are used, and a stack that does not fit above the bar drops its oldest
// boxes rather than overflowing, so the bar and the pointer gestures over the
// panes are untouched.
func drawToasts(buf *bytes.Buffer, th theme.Theme, snap *wire.Snapshot, toasts []toast) {
	if snap == nil || len(toasts) == 0 || snap.Cols < 3 || snap.Rows < 3 {
		return
	}
	// Each box takes its two borders and one row per content row, plus the
	// one-row gap above it. The newest box rests on the row just above the bar;
	// walking the stack from the newest toast down and stopping when the next top
	// border would climb past row 1 drops the oldest boxes instead of painting
	// over the bar.
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
