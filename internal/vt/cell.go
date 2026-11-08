package vt

import "github.com/charmbracelet/x/ansi"

// ColorKind says how a Color names its colour.
type ColorKind uint8

const (
	// ColorDefault is the terminal's default foreground or background.
	ColorDefault ColorKind = iota
	// ColorPalette is an index into the 256-colour palette.
	ColorPalette
	// ColorRGB is a 24-bit colour.
	ColorRGB
)

// Color is one colour of a cell. Kind decides which fields carry meaning:
// Index for ColorPalette, R, G and B for ColorRGB, none for ColorDefault.
type Color struct {
	Kind    ColorKind
	Index   uint8
	R, G, B uint8
}

// DefaultColor returns the terminal default colour.
func DefaultColor() Color {
	return Color{Kind: ColorDefault}
}

// PaletteColor returns palette colour i.
func PaletteColor(i uint8) Color {
	return Color{Kind: ColorPalette, Index: i}
}

// RGBColor returns the 24-bit colour r, g, b.
func RGBColor(r, g, b uint8) Color {
	return Color{Kind: ColorRGB, R: r, G: g, B: b}
}

// Attr bits for Style.Attrs, one bit per attribute.
const (
	Bold uint8 = 1 << iota
	Faint
	Italic
	Blink
	RapidBlink
	Reverse
	Conceal
	Strikethrough
)

// UnderlineStyle is the shape of a cell's underline.
type UnderlineStyle uint8

const (
	UnderlineNone UnderlineStyle = iota
	UnderlineSingle
	UnderlineDouble
	UnderlineCurly
	UnderlineDotted
	UnderlineDashed
)

// Style is the colour and attribute of one cell. Reverse is an attribute here,
// not a swap of Fg and Bg, so a painter can tell reverse from a palette swap.
type Style struct {
	Fg, Bg, UnderlineColor Color
	Underline              UnderlineStyle
	Attrs                  uint8
}

// DefaultStyle returns a style with default colours and no attributes.
func DefaultStyle() Style {
	return Style{Fg: DefaultColor(), Bg: DefaultColor(), UnderlineColor: DefaultColor()}
}

// Cell widths. A continuation slot always follows a cell of width 2.
const (
	// WidthContinuation marks the second slot of a wide cell. Its Content is
	// empty and every accessor resolves it to the wide cell it belongs to.
	WidthContinuation int8 = 0
	// WidthNormal is one slot.
	WidthNormal int8 = 1
	// WidthWide is a cell that occupies two slots.
	WidthWide int8 = 2
)

// Cell is one slot of the grid. Content is one grapheme cluster, so a base
// rune and its combining marks, or a joined emoji sequence, stay together.
type Cell struct {
	Content string
	Width   int8
	Style   Style
	Link    string
	// Protected is the DECSCA protection state the cell was written with. A
	// selective erase (DECSERA) leaves a protected cell alone.
	Protected bool
}

// NewCell builds a cell for content and measures its width with x/ansi, the
// same table the chrome uses. Empty content is a blank cell of width 1; a
// cluster that measures zero columns is widened to 1 so it still occupies a
// slot.
func NewCell(content string, st Style) Cell {
	w := ClusterWidth(content)
	switch {
	case content == "":
		content, w = " ", 1
	case w <= 0:
		w = 1
	case w > 2:
		w = 2
	}
	return Cell{Content: content, Width: int8(w), Style: st}
}

// Continuation returns the second slot of a wide cell.
func Continuation() Cell {
	return Cell{Width: WidthContinuation}
}

// IsContinuation reports whether c is the second slot of a wide cell.
func (c Cell) IsContinuation() bool {
	return c.Width == WidthContinuation
}

// IsWide reports whether c occupies two slots.
func (c Cell) IsWide() bool {
	return c.Width == WidthWide
}

// IsBlank reports whether c paints nothing: a space, or an empty continuation
// slot that a caller has not skipped.
func (c Cell) IsBlank() bool {
	return c.Content == "" || c.Content == " "
}

// FirstCluster splits the first grapheme cluster from s and returns it with its
// width in cells. The width comes from x/ansi, so the grid and the chrome agree
// on how much room a cluster takes.
func FirstCluster(s string) (string, int) {
	cluster, w := ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth)
	return cluster, w
}

// ClusterWidth returns how many cells s paints, counting grapheme clusters.
func ClusterWidth(s string) int {
	return ansi.StringWidth(s)
}

// blankCell is the cell an erase or a disconnected continuation leaves behind.
func blankCell(st Style) Cell {
	return Cell{Content: " ", Width: WidthNormal, Style: st}
}
