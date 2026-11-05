package main

import (
	"bytes"
	"fmt"
	"strings"

	"dm/internal/cells"
	"dm/internal/layout"

	"github.com/charmbracelet/x/ansi"
)

// The two border-row label styles: a title reads bright, a hint dim.
const (
	panelTitleStyle = "\x1b[97m"
	panelHintStyle  = "\x1b[90m"
)

// panelTitle is a panel's top border row. text is the row's label and fill is
// the glyph the row continues with after it, so a caller whose title row is not
// plain border asks for the whole row in one write. A fill row has its corners
// written by the panel and is left out of the box pass, so no cell of it lands
// twice. When fill is empty the top row stays plain border and text is a label
// painted over it, one cell clear of the left corner, which is the old float
// look down to the byte. style is written before the row's first glyph: for a
// fill row it is the whole row's style, reset included; for a label it is the
// label's colour alone.
type panelTitle struct {
	text  string
	style string
	fill  string
}

// paintPanel paints one bordered box and nothing outside it. rect is the
// interior: the border sits one cell around it, the title on the row above and
// the hint on the row below, both one cell clear of a corner and clipped to the
// interior, so a label never eats a corner. paint, when set, writes the inside
// with absolute moves; the panel clips it to rect, so an interior painter that
// writes past its box cannot reach the border or the cells beside it.
//
// The painter holds no state and keeps no buffer between frames: the clip needs
// to see a whole run before it can cut it, so it collects the inside in a
// scratch buffer and throws it away. Only buf reaches the frame.
func paintPanel(buf *bytes.Buffer, th Theme, rect layout.Rect, title panelTitle, hint string, active bool, paint func(*bytes.Buffer, layout.Rect)) {
	if rect.W <= 0 || rect.H <= 0 || rect.X < 1 || rect.Y < 1 {
		return
	}
	titled := title.fill != ""
	if titled {
		paintTitleRow(buf, th, rect, title)
	}
	if paint != nil {
		var inside bytes.Buffer
		paint(&inside, rect)
		panelClip(buf, rect, inside.Bytes())
	}
	if titled {
		drawPaneBoxRows(buf, th, rect, active, true)
	} else {
		drawPaneBox(buf, th, rect, active)
		paintBorderText(buf, rect, rect.Y-1, title.text, title.style)
	}
	paintBorderText(buf, rect, rect.Y+rect.H, hint, panelHintStyle)
}

// paintTitleRow writes the top border row as one run: the left corner, the
// label clipped to the interior and filled to its width, the right corner. It
// writes both corners, so the box pass leaves this row alone.
func paintTitleRow(buf *bytes.Buffer, th Theme, rect layout.Rect, t panelTitle) {
	row := cells.Clip(t.text, rect.W)
	if n := rect.W - cells.Cells(row); n > 0 {
		row += strings.Repeat(t.fill, n)
	}
	fmt.Fprintf(buf, "%s\x1b[%d;%dH%s%s%s", t.style, rect.Y, rect.X,
		th.Glyphs.BorderTopLeft, row, th.Glyphs.BorderTopRight)
}

// paintBorderText writes a label into row y of the border around r, one cell
// clear of the left corner and clipped to the interior, so a label never eats a
// corner and a box too narrow for it drops it.
func paintBorderText(buf *bytes.Buffer, r layout.Rect, y int, text, style string) {
	if text == "" || r.W < 3 || y < 0 {
		return
	}
	text = cells.Clip(text, r.W-2)
	if text == "" {
		return
	}
	fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m%s%s\x1b[0m", y+1, r.X+2, style, text)
}

// panelClip copies an interior painter's bytes into buf, keeping only the cells
// inside rect. Text placed outside the rect is dropped, text that runs past the
// right edge is cut at the edge, and a move that lands outside the rect is
// dropped with the text that follows it.
func panelClip(buf *bytes.Buffer, rect layout.Rect, in []byte) {
	c := panelClipWriter{buf: buf, rect: rect, row: -1, col: -1}
	for len(in) > 0 {
		if in[0] == 0x1b {
			n := panelEscape(in)
			c.escape(in[:n])
			in = in[n:]
			continue
		}
		n := bytes.IndexByte(in, 0x1b)
		if n < 0 {
			n = len(in)
		}
		c.text(in[:n])
		in = in[n:]
	}
}

// panelClipWriter is where an interior painter's bytes land while the panel
// clips them. row and col hold the pen the painter's own moves set; a painter
// that writes text before any move writes outside the box, so it paints nothing.
type panelClipWriter struct {
	buf  *bytes.Buffer
	rect layout.Rect
	row  int
	col  int
}

// escape keeps a move that lands inside the rect and passes a colour through. It
// drops everything else, because the inside of a panel is placed with absolute
// moves and styled with SGR, never with a mode change or a clear.
func (c *panelClipWriter) escape(seq []byte) {
	n := len(seq)
	if n < 3 || seq[1] != '[' {
		return
	}
	switch seq[n-1] {
	case 'H', 'f':
		c.row, c.col = panelPen(seq[2 : n-1])
		if c.inside(c.row, c.col) {
			c.buf.Write(seq)
		}
	case 'm':
		c.buf.Write(seq)
	}
}

// text places a run of printable text and keeps only the clusters inside the
// rect. A cluster wider than the cells left on its row is dropped whole, so a
// clip never paints half a glyph. A control byte moves the pen and paints
// nothing: the panel's rows are placed, not wrapped.
func (c *panelClipWriter) text(s []byte) {
	for len(s) > 0 {
		if s[0] < 0x20 || s[0] == 0x7f {
			switch s[0] {
			case '\n':
				c.row++
			case '\r':
				c.col = 0
			}
			s = s[1:]
			continue
		}
		cluster, w := ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth)
		if len(cluster) == 0 {
			return
		}
		if c.inside(c.row, c.col) && c.col+w <= c.rect.X+c.rect.W {
			c.buf.Write(cluster)
		}
		c.col += w
		s = s[len(cluster):]
	}
}

// inside reports whether row, col is a cell of the rect.
func (c *panelClipWriter) inside(row, col int) bool {
	return row >= c.rect.Y && row < c.rect.Y+c.rect.H &&
		col >= c.rect.X && col < c.rect.X+c.rect.W
}

// panelEscape is the length of the escape sequence at the head of in. It reads a
// CSI up to its final byte and any other escape as the escape and the byte after
// it.
func panelEscape(in []byte) int {
	if len(in) < 2 {
		return len(in)
	}
	if in[1] != '[' {
		return 2
	}
	for i := 2; i < len(in); i++ {
		if in[i] >= 0x40 && in[i] <= 0x7e {
			return i + 1
		}
	}
	return len(in)
}

// panelPen reads a cursor position from CSI parameters: row then column, both
// one based and either omitted, as zero based cells.
func panelPen(param []byte) (row, col int) {
	at := bytes.IndexByte(param, ';')
	if at < 0 {
		at = len(param)
	}
	row = panelNumber(param[:at])
	if at < len(param) {
		col = panelNumber(param[at+1:])
	} else {
		col = 1
	}
	return row - 1, col - 1
}

// panelNumber reads one decimal parameter. A missing, zero or malformed one is
// the first row or column.
func panelNumber(p []byte) int {
	n := 0
	for _, b := range p {
		if b < '0' || b > '9' {
			return 1
		}
		n = n*10 + int(b-'0')
	}
	if n < 1 {
		return 1
	}
	return n
}
