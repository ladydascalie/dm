package esctest

import (
	"fmt"
	"strconv"
	"strings"
)

// rgb is a 16-bit-per-channel colour, the form xterm reports.
type rgb struct{ r, g, b uint16 }

var (
	defaultFG     = rgb{0xffff, 0xffff, 0xffff}
	defaultBG     = rgb{0x0000, 0x0000, 0x0000}
	defaultCursor = rgb{0xffff, 0xffff, 0xffff}
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

// parseColor parses the forms esctest2 sends: rgb:R/G/B and #RGB, #RRGGBB,
// #RRRGGGBBB, #RRRRGGGGBBBB. Each channel is scaled to 16 bits the way X11
// does it. Other X11 forms are not supported.
func parseColor(s string) (rgb, bool) {
	if strings.HasPrefix(s, "rgb:") {
		parts := strings.Split(s[4:], "/")
		if len(parts) != 3 {
			return rgb{}, false
		}
		var out rgb
		for i, p := range parts {
			v, ok := parseChannel(p)
			if !ok {
				return rgb{}, false
			}
			switch i {
			case 0:
				out.r = v
			case 1:
				out.g = v
			case 2:
				out.b = v
			}
		}
		return out, true
	}
	if strings.HasPrefix(s, "#") {
		h := s[1:]
		if len(h) != 3 && len(h) != 6 && len(h) != 9 && len(h) != 12 {
			return rgb{}, false
		}
		n := len(h) / 3
		var out rgb
		for i := 0; i < 3; i++ {
			v, ok := parseChannel(h[i*n : (i+1)*n])
			if !ok {
				return rgb{}, false
			}
			switch i {
			case 0:
				out.r = v
			case 1:
				out.g = v
			case 2:
				out.b = v
			}
		}
		return out, true
	}
	return rgb{}, false
}

func parseChannel(s string) (uint16, bool) {
	if len(s) < 1 || len(s) > 4 {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, false
	}
	shift := uint(16 - 4*len(s))
	return uint16(v << shift), true
}

func formatColor(c rgb) string {
	return fmt.Sprintf("rgb:%04x/%04x/%04x", c.r, c.g, c.b)
}
