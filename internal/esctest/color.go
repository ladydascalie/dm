package esctest

import "fmt"

// rgb is a 16-bit-per-channel colour, the form xterm reports.
type rgb struct{ r, g, b uint16 }

// esctest2's reset() sets the dynamic foreground to #000 and the background
// to #ffffff before every test, and the reset tests expect OSC 110/111 to
// restore those values. The harness's defaults match that environment.
var (
	defaultFG     = rgb{0x0000, 0x0000, 0x0000}
	defaultBG     = rgb{0xffff, 0xffff, 0xffff}
	defaultCursor = rgb{0x0000, 0x0000, 0x0000}
)

// ansi16 is xterm's default palette for the first sixteen colours.
var ansi16 = [16]rgb{
	{0x0000, 0x0000, 0x0000},
	{0xcdcd, 0x0000, 0x0000},
	{0x0000, 0xcdcd, 0x0000},
	{0xcdcd, 0xcdcd, 0x0000},
	{0x0000, 0x0000, 0xeeee},
	{0xcdcd, 0x0000, 0xcdcd},
	{0x0000, 0xcdcd, 0xcdcd},
	{0xe5e5, 0xe5e5, 0xe5e5},
	{0x7f7f, 0x7f7f, 0x7f7f},
	{0xffff, 0x0000, 0x0000},
	{0x0000, 0xffff, 0x0000},
	{0xffff, 0xffff, 0x0000},
	{0x5c5c, 0x5c5c, 0xffff},
	{0xffff, 0x0000, 0xffff},
	{0x0000, 0xffff, 0xffff},
	{0xffff, 0xffff, 0xffff},
}

var cubeLevels = [6]uint16{0x00, 0x5f, 0x87, 0xaf, 0xd7, 0xff}

// defaultIndexed returns the default colour for an OSC 4/5 index.
func defaultIndexed(idx int) rgb {
	switch {
	case idx >= 0 && idx < 16:
		return ansi16[idx]
	case idx >= 16 && idx < 232:
		n := idx - 16
		scale := func(v uint16) uint16 { return v * 0x101 }
		return rgb{
			scale(cubeLevels[n/36]),
			scale(cubeLevels[(n/6)%6]),
			scale(cubeLevels[n%6]),
		}
	case idx >= 232 && idx < 256:
		v := uint16(8 + 10*(idx-232))
		v *= 0x101
		return rgb{v, v, v}
	}
	// Special colours start at 256. The tests set them before querying, so
	// the defaults only need to be well formed.
	switch idx {
	case 256:
		return defaultFG
	case 257:
		return defaultBG
	case 258:
		return defaultCursor
	}
	return defaultFG
}

// parseColor parses the X11 colour forms esctest2 sends. The conversion lives
// in x11color.go; the result is the 16-bit value xterm reports.
func parseColor(s string) (rgb, bool) {
	return parseX11Color(s)
}

func formatColor(c rgb) string {
	return fmt.Sprintf("rgb:%04x/%04x/%04x", c.r, c.g, c.b)
}
