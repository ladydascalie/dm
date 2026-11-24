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

// toastMaxWidth caps a toast's interior in cells, so one long message cannot
// stretch the box across the screen.
const toastMaxWidth = 40

// toastTint is how far a state's colour mixes into the rail background for the
// toast's body: enough that the state reads as a panel, small enough that the
// dim topic row keeps WCAG AA on it. TestToastContrast guards the ratio.
const toastTint = 0.12

// blend mixes over into base by t, 0 leaving base and 1 leaving over. Every
// channel rounds to the nearest 8-bit value.
func blend(base, over theme.RGB, t float64) theme.RGB {
	mix := func(a, b int) int {
		return int(math.Round(float64(a) + (float64(b)-float64(a))*t))
	}
	return theme.RGB{R: mix(base.R, over.R), G: mix(base.G, over.G), B: mix(base.B, over.B)}
}

// toastLine is one content row of a toast: its text and the colour it paints in.
type toastLine struct {
	text string
	fg   string
}

// toastBox is one toast laid out for the screen: the title the top border
// carries, the content rows, the colour the border and the title share, the
// background the body and the drained part of the band paint, the colour the
// band fills with, and the interior width in cells. A drain band row sits above
// the top border, the borders bracket one row per content row, and the left
// fraction says how much of the band is still coloured.
type toastBox struct {
	title  string
	border string // border foreground escape
	bg     string // body background escape
	band   string // drain band background escape
	rows   []toastLine
	width  int     // interior width in cells
	left   float64 // fraction of the toast's life still to run, 0..1
}

// newToastBox lays one toast out for a screen maxCols wide. A notify toast names
// its state in the title bar in the state's colour, tints its body with the same
// colour and drains a band in it; the box draws the entry's topic dim above the
// bright message. A plain toast carries its message alone on the neutral float
// background and a dim band, so a client message keeps a quiet shape.
func newToastBox(th theme.Theme, x toast, maxCols int) toastBox {
	dim := th.Palette.RailDim.FG()
	b := toastBox{
		border: dim,
		bg:     th.Palette.RailFocusBG.BG(),
		band:   th.Palette.RailDim.BG(),
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
		b.border = st.Colour.FG()
		b.bg = blend(th.Palette.RailBG, st.Colour, toastTint).BG()
		b.band = st.Colour.BG()
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

// filled is the coloured cells the drain band paints: the whole box while the
// toast is fresh, one cell at the end of its life, none once it has run out. A
// toast that reaches none is dropped by the expiry, so the band never empties on
// screen.
func (b toastBox) filled() int {
	full := b.width + 2
	if b.left <= 0 {
		return 0
	}
	n := int(math.Round(b.left * float64(full)))
	if n > full {
		return full
	}
	if n < 1 {
		return 1
	}
	return n
}

// draw paints the box with its drain band on 1-based row top-1, its top border
// on row top, its bottom border on row bottom and its left border in column
// col. Each row is placed absolutely, so a box never depends on the cursor the
// previous one left. Every cell of the box carries the body background, the
// band included, so a toast reads as one panel over the panes and no cell of
// the picture under it shows through.
func (b toastBox) draw(buf *bytes.Buffer, th theme.Theme, col, top, bottom int) {
	g := th.Glyphs
	title := b.borderTitle()
	bar := strings.Repeat(g.BorderHorizontal, b.width)
	// The band spans the whole box: the coloured part drains from the right, so
	// the cell count the fraction buys is the leftmost fill cells.
	fill := b.filled()
	fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m%s%s\x1b[0m%s%s\x1b[0m", top-1, col, b.band,
		strings.Repeat(" ", fill), b.bg, strings.Repeat(" ", b.width+2-fill))
	// The body background rides every row, so the border sits on the panel too.
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
	fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m%s%s%s%s%s\x1b[0m", bottom, col, b.bg, b.border,
		g.BorderBottomLeft, bar, g.BorderBottomRight)
}

// drawToasts paints the toast stack in the bottom-right corner, just above the
// status bar: newest at the bottom, each toast a drained colour band over a
// closed box, with the state's mark and name in the top border and the topic and
// the message inside. A box is as wide as its widest row, clamped first to
// toastMaxWidth and then to the screen; a longer row is truncated with an
// ellipsis on a cluster boundary. Boxes sit a one-row gap apart so the stack
// reads as separate popups. Only rows above the bar are used, and a stack that
// does not fit above the bar drops its oldest boxes rather than overflowing, so
// the bar and the pointer gestures over the panes are untouched.
func drawToasts(buf *bytes.Buffer, th theme.Theme, snap *wire.Snapshot, toasts []toast) {
	if snap == nil || len(toasts) == 0 || snap.Cols < 3 || snap.Rows < 3 {
		return
	}
	// Each box takes its band row, its two borders and one row per content row,
	// plus the one-row gap above its band. The newest box rests on the row just
	// above the bar; walking the stack from the newest toast down and stopping
	// when the next band would climb past row 1 drops the oldest boxes instead
	// of painting over the bar.
	bottom := snap.Rows - 1
	for i := len(toasts) - 1; i >= 0; i-- {
		b := newToastBox(th, toasts[i], snap.Cols)
		if b.width < 1 {
			continue
		}
		top := bottom - len(b.rows) - 1
		if top-1 < 1 {
			return
		}
		b.draw(buf, th, snap.Cols-b.width-1, top, bottom)
		bottom = top - 3
	}
}
