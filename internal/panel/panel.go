// Package panel paints a bordered box: its border, its two border-row labels
// and a clipped interior. It owns no state between frames.
package panel

import (
	"bytes"
	"fmt"
	"strings"

	"dm/internal/cells"
	"dm/internal/layout"
	"dm/internal/theme"
)

// The two border-row label styles: a title reads bright, a hint dim.
const (
	PanelTitleStyle = "\x1b[97m"
	PanelHintStyle  = "\x1b[90m"
)

// PanelTitle is a panel's top border row. Text is the row's label and Fill is
// the glyph the row continues with after it, so a caller whose title row is not
// plain border asks for the whole row in one write. A fill row has its corners
// written by the panel and is left out of the box pass, so no cell of it lands
// twice. When Fill is empty the top row stays plain border and Text is a label
// painted over it, one cell clear of the left corner, which is the old float
// look down to the byte. Style is written before the row's first glyph: for a
// fill row it is the whole row's style, reset included; for a label it is the
// label's colour alone.
type PanelTitle struct {
	Text  string
	Style string
	Fill  string
}

// PaintPanel paints one bordered box and nothing outside it. rect is the
// interior: the border sits one cell around it, the title on the row above and
// the hint on the row below, both one cell clear of a corner and clipped to the
// interior, so a label never eats a corner. paint, when set, writes the inside
// with absolute moves; the panel clips it to rect, so an interior painter that
// writes past its box cannot reach the border or the cells beside it.
//
// The painter holds no state and keeps no buffer between frames: the clip needs
// to see a whole run before it can cut it, so it collects the inside in a
// scratch buffer and throws it away. Only buf reaches the frame.
func PaintPanel(buf *bytes.Buffer, th theme.Theme, rect layout.Rect, title PanelTitle, hint string, active bool, paint func(*bytes.Buffer, layout.Rect)) {
	if rect.W <= 0 || rect.H <= 0 || rect.X < 1 || rect.Y < 1 {
		return
	}
	titled := title.Fill != ""
	if titled {
		paintTitleRow(buf, th, rect, title)
	}
	// The panel owns a solid ground: the interior fills before the content
	// paints, so a cell the painter leaves alone shows the panel's ground
	// and never the frame behind it. A panel that reads through is a bug in
	// the panel, never in the caller's painter.
	fillRect(buf, rect, groundSGR(th))
	if paint != nil {
		var inside bytes.Buffer
		paint(&inside, rect)
		panelClip(buf, rect, inside.Bytes())
	}
	if titled {
		DrawPaneBoxRows(buf, th, rect, active, true)
	} else {
		DrawPaneBox(buf, th, rect, active)
		paintBorderText(buf, rect, rect.Y-1, title.Text, title.Style)
	}
	paintBorderText(buf, rect, rect.Y+rect.H, hint, PanelHintStyle)
}

// paintTitleRow writes the top border row as one run: the left corner, the
// label clipped to the interior and filled to its width, the right corner. It
// writes both corners, so the box pass leaves this row alone.
func paintTitleRow(buf *bytes.Buffer, th theme.Theme, rect layout.Rect, t PanelTitle) {
	row := cells.Clip(t.Text, rect.W)
	if n := rect.W - cells.Cells(row); n > 0 {
		row += strings.Repeat(t.Fill, n)
	}
	fmt.Fprintf(buf, "%s%s\x1b[%d;%dH%s%s%s", t.Style, groundSGR(th), rect.Y, rect.X,
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
		cluster, w := cells.FirstClusterBytes(s)
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

// DrawPaneBox draws the border of one pane. The active pane is bright cyan;
// the rest are dim, so the active one reads at a glance.
func DrawPaneBox(buf *bytes.Buffer, th theme.Theme, content layout.Rect, active bool) {
	DrawPaneBoxRows(buf, th, content, active, false)
}

// DrawPaneBoxOnGrid draws the border of one pane and keeps only the cells that
// land inside a cols x rows grid. A pane that overlaps the grid paints its
// on-screen border cells and nothing past the edge.
func DrawPaneBoxOnGrid(buf *bytes.Buffer, th theme.Theme, content layout.Rect, active bool, cols, rows int) {
	drawPaneBoxRows(buf, th, content, active, false, gridPut(buf, cols, rows))
}

// DrawPanePadding blanks the ring between a pane's border and its content. The
// border band owns the ring, so a cell the content does not own never keeps an
// older frame's glyph. box is the box interior and content the content rect.
func DrawPanePadding(buf *bytes.Buffer, box, content layout.Rect) {
	drawPanePadding(buf, box, content, gridPut(buf, 0, 0))
}

// DrawPanePaddingOnGrid is DrawPanePadding clipped to a cols x rows grid: the
// ring of a pane that overlaps the edge keeps its on-screen cells only.
func DrawPanePaddingOnGrid(buf *bytes.Buffer, box, content layout.Rect, cols, rows int) {
	drawPanePadding(buf, box, content, gridPut(buf, cols, rows))
}

// drawPanePadding writes the ring through put. See DrawPanePadding.
func drawPanePadding(buf *bytes.Buffer, box, content layout.Rect, put func(x, y int, s string)) {
	if box.W <= 0 || box.H <= 0 {
		return
	}
	buf.WriteString("\x1b[0m")
	spaces := func(n int) string {
		if n <= 0 {
			return ""
		}
		return strings.Repeat(" ", n)
	}
	for y := box.Y; y < box.Y+box.H; y++ {
		if y < content.Y || y >= content.Y+content.H {
			put(box.X, y, spaces(box.W))
			continue
		}
		put(box.X, y, spaces(content.X-box.X))
		put(content.X+content.W, y, spaces(box.X+box.W-(content.X+content.W)))
	}
}

// DrawPaneBoxRows draws the border of one pane. skipTop leaves the top row
// alone, for a caller that writes it itself as one row, so no cell of it lands
// twice.
func DrawPaneBoxRows(buf *bytes.Buffer, th theme.Theme, content layout.Rect, active bool, skipTop bool) {
	drawPaneBoxRows(buf, th, content, active, skipTop, gridPut(buf, 0, 0))
}

// drawPaneBoxRows writes the border through put. See DrawPaneBoxRows.
func drawPaneBoxRows(buf *bytes.Buffer, th theme.Theme, content layout.Rect, active bool, skipTop bool, put func(x, y int, s string)) {
	g := th.Glyphs
	x0, y0 := content.X-1, content.Y-1
	x1, y1 := content.X+content.W, content.Y+content.H
	if content.W <= 0 || content.H <= 0 || x0 < 0 || y0 < 0 {
		return
	}
	if active {
		buf.WriteString("\x1b[0m\x1b[1;96m")
	} else {
		buf.WriteString("\x1b[0m\x1b[90m")
	}
	if !skipTop {
		put(x0, y0, g.BorderTopLeft)
		put(x1, y0, g.BorderTopRight)
	}
	put(x0, y1, g.BorderBottomLeft)
	put(x1, y1, g.BorderBottomRight)
	for x := x0 + 1; x < x1; x++ {
		if !skipTop {
			put(x, y0, g.BorderHorizontal)
		}
		put(x, y1, g.BorderHorizontal)
	}
	for y := y0 + 1; y < y1; y++ {
		put(x0, y, g.BorderVertical)
		put(x1, y, g.BorderVertical)
	}
}

// gridPut returns the writer a band places its cells through. cols or rows
// below 1 means no clip.
func gridPut(buf *bytes.Buffer, cols, rows int) func(x, y int, s string) {
	if cols < 1 || rows < 1 {
		return func(x, y int, s string) {
			fmt.Fprintf(buf, "\x1b[%d;%dH%s", y+1, x+1, s)
		}
	}
	return func(x, y int, s string) {
		putClipped(buf, x, y, cols, rows, s)
	}
}

// putClipped places a run of cells at x, y and keeps only the part inside a
// cols x rows grid. The runs here are single-cell glyphs or spaces, so the head
// and tail are cut by cell count. A run wholly outside the grid writes nothing.
func putClipped(buf *bytes.Buffer, x, y, cols, rows int, s string) {
	if s == "" || y < 0 || y >= rows {
		return
	}
	n := cells.Cells(s)
	lo, hi := x, x+n
	if lo < 0 {
		lo = 0
	}
	if hi > cols {
		hi = cols
	}
	if lo >= hi {
		return
	}
	if lo > x {
		s = cells.ClipTail(s, n-(lo-x))
	}
	if hi < x+n {
		s = cells.Clip(s, hi-lo)
	}
	if s == "" {
		return
	}
	fmt.Fprintf(buf, "\x1b[%d;%dH%s", y+1, lo+1, s)
}

// groundSGR is the panel's ground: the rail background every float shares.
func groundSGR(th theme.Theme) string {
	return th.Palette.RailBG.BG()
}

// fillRect writes every cell of r as a space on the ground, one row at a
// time. Content painted after it replaces those cells, so the fill is the
// floor a painter builds on, never a ceiling.
func fillRect(buf *bytes.Buffer, r layout.Rect, ground string) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	for y := 0; y < r.H; y++ {
		fmt.Fprintf(buf, "%s\x1b[%d;%dH%s\x1b[0m", ground, r.Y+y+1, r.X+1, strings.Repeat(" ", r.W))
	}
}
