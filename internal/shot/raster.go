package shot

import (
	"image"
	"image/color"
	"image/draw"
	"sync"

	"dm/internal/cells"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Default cell size in pixels. The font size follows the cell height.
const (
	DefaultCellW = 8
	DefaultCellH = 16
)

// Options sets the cell size and the font size. The zero value is valid: it
// takes the defaults.
type Options struct {
	CellW    int
	CellH    int
	FontSize float64
}

func (o Options) withDefaults() Options {
	if o.CellW <= 0 {
		o.CellW = DefaultCellW
	}
	if o.CellH <= 0 {
		o.CellH = DefaultCellH
	}
	if o.FontSize <= 0 {
		o.FontSize = float64(o.CellH) * 0.75
	}
	return o
}

// Row is the pen position after one grid row. Pen is in cells, counted from
// the left edge. A row that lands exactly on the grid width is correct; a
// larger Pen is an overshoot.
type Row struct {
	Y   int
	Pen int
}

// Raster is a rendered grid: the pixels and the per-row pen positions.
type Raster struct {
	Image *image.RGBA
	Rows  []Row
}

// Overshoot returns the rows whose pen passed the grid width.
func (r *Raster) Overshoot(w int) []Row {
	var out []Row
	for _, row := range r.Rows {
		if row.Pen != w {
			out = append(out, row)
		}
	}
	return out
}

var (
	fontOnce sync.Once
	fontData *opentype.Font
	fontErr  error
)

func gomonoFont() (*opentype.Font, error) {
	fontOnce.Do(func() {
		fontData, fontErr = opentype.Parse(gomono.TTF)
	})
	return fontData, fontErr
}

func faceFor(size float64) (font.Face, error) {
	f, err := gomonoFont()
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(f, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
}

// Rasterise draws every cell at the pen, then advances the pen by the glyph's
// real width. It reports the pen position after every row.
func Rasterise(g *Grid, opts Options) (*Raster, error) {
	opts = opts.withDefaults()
	face, err := faceFor(opts.FontSize)
	if err != nil {
		return nil, err
	}
	defer func() { _ = face.Close() }()

	img := image.NewRGBA(image.Rect(0, 0, g.W*opts.CellW, g.H*opts.CellH))
	base := baseline(face, opts.CellH)
	rows := make([]Row, g.H)
	for y := 0; y < g.H; y++ {
		pen := 0
		for x := 0; x < g.W; x++ {
			c := g.Cell(x, y)
			if c.Cont {
				continue
			}
			drawCell(img, face, opts, base, pen, y, c)
			pen += cellWidth(c.Content)
		}
		rows[y] = Row{Y: y, Pen: pen}
	}
	return &Raster{Image: img, Rows: rows}, nil
}

// cellWidth is the number of cells a glyph paints. An empty cell paints one
// space, so a blank row still lands on the grid width.
func cellWidth(s string) int {
	if s == "" {
		return 1
	}
	return cells.Cells(s)
}

func baseline(face font.Face, cellH int) int {
	m := face.Metrics()
	h := (m.Ascent + m.Descent).Ceil()
	return (cellH-h)/2 + m.Ascent.Ceil()
}

func drawCell(img *image.RGBA, face font.Face, o Options, base, pen, y int, c Cell) {
	fg, bg := c.Fg, c.Bg
	if c.Attr&Reverse != 0 {
		fg, bg = bg, fg
	}
	x0 := pen * o.CellW
	y0 := y * o.CellH
	draw.Draw(img, image.Rect(x0, y0, x0+o.CellW, y0+o.CellH), &image.Uniform{bg.color()}, image.Point{}, draw.Src)

	s := c.Content
	if s == "" {
		s = " "
	}
	d := font.Drawer{
		Dst:  img,
		Src:  &image.Uniform{fg.color()},
		Face: face,
		Dot:  fixed.P(x0, y0+base),
	}
	d.DrawString(s)
	if c.Attr&Bold != 0 {
		d.Dot = fixed.P(x0+1, y0+base)
		d.DrawString(s)
	}
	if c.Attr&Underline != 0 {
		uy := y0 + base + 2
		draw.Draw(img, image.Rect(x0, uy, x0+o.CellW, uy+1), &image.Uniform{fg.color()}, image.Point{}, draw.Src)
	}
}

func (c RGB) color() color.RGBA {
	return color.RGBA{R: c.R, G: c.G, B: c.B, A: 0xff}
}
