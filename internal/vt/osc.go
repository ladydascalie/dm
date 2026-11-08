package vt

import (
	"strconv"
	"strings"
)

// This file owns the OSC family: 0/1/2 title, 4/10/11/12 palette, 8 links,
// 52 clipboard and 7501 status. It is dispatched from Term.osc. A command the
// terminal does not know is ignored, as a terminal ignores an unknown string.

// oscCommand applies one OSC body. The command number is the text before the
// first ';'.
func (t *Term) oscCommand(body string) {
	cmd, rest, ok := strings.Cut(body, ";")
	if !ok {
		return
	}
	n, err := strconv.Atoi(cmd)
	if err != nil {
		return
	}
	switch n {
	case 0:
		t.iconTitle, t.windowTitle = rest, rest
	case 1:
		t.iconTitle = rest
	case 2:
		t.windowTitle = rest
	case 4:
		t.oscPalette(rest)
	case 10, 11, 12:
		t.oscDynamicColor(n, rest)
	case 8:
		t.oscLink(rest)
	case 52:
		t.oscClipboard(rest)
	case 7501:
		t.status = rest
	}
}

// oscPalette handles OSC 4: index;colour pairs.
func (t *Term) oscPalette(rest string) {
	fields := strings.Split(rest, ";")
	for i := 0; i+1 < len(fields); i += 2 {
		idx, err := strconv.Atoi(fields[i])
		if err != nil || idx < 0 || idx > 255 {
			continue
		}
		if c, ok := parseOSCColor(fields[i+1]); ok {
			t.setColor(idx, c)
		}
	}
}

// oscDynamicColor handles OSC 10, 11 and 12: the default foreground,
// background and cursor colour.
func (t *Term) oscDynamicColor(n int, rest string) {
	if c, ok := parseOSCColor(rest); ok {
		t.setColor(n, c)
	}
}

// oscLink handles OSC 8: params;uri. An empty uri ends the link.
func (t *Term) oscLink(rest string) {
	_, uri, ok := strings.Cut(rest, ";")
	if !ok {
		t.link = ""
		return
	}
	t.link = uri
}

// oscClipboard handles OSC 52: selection;data. A query, "?", is left to the
// host and does not change the store.
func (t *Term) oscClipboard(rest string) {
	_, data, ok := strings.Cut(rest, ";")
	if !ok || data == "?" {
		return
	}
	t.clipboard = data
}

// setColor records one colour override.
func (t *Term) setColor(n int, c Color) {
	if t.colors == nil {
		t.colors = map[int]Color{}
	}
	t.colors[n] = c
}

// Color returns the override for a palette entry, or OSC 10, 11 and 12 for the
// default colours.
func (t *Term) Color(n int) (Color, bool) {
	c, ok := t.colors[n]
	return c, ok
}

// Title returns the window title set by OSC 0 or 2.
func (t *Term) Title() string { return t.windowTitle }

// IconTitle returns the icon title set by OSC 0 or 1.
func (t *Term) IconTitle() string { return t.iconTitle }

// Link returns the hyperlink the next cells carry, or empty.
func (t *Term) Link() string { return t.link }

// Clipboard returns the last OSC 52 data.
func (t *Term) Clipboard() string { return t.clipboard }

// Status returns the last OSC 7501 status.
func (t *Term) Status() string { return t.status }

// parseOSCColor reads an X11 colour spec: rgb:R/G/B with one to four hex
// digits per component, or #RGB / #RRGGBB.
func parseOSCColor(s string) (Color, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "?" {
		return Color{}, false
	}
	if strings.HasPrefix(s, "rgb:") {
		parts := strings.Split(strings.TrimPrefix(s, "rgb:"), "/")
		if len(parts) != 3 {
			return Color{}, false
		}
		var rgb [3]uint8
		for i, p := range parts {
			v, ok := parseHex(p)
			if !ok || len(p) == 0 || len(p) > 4 {
				return Color{}, false
			}
			rgb[i] = scaleHex(v, len(p))
		}
		return RGBColor(rgb[0], rgb[1], rgb[2]), true
	}
	if strings.HasPrefix(s, "#") {
		h := strings.TrimPrefix(s, "#")
		switch len(h) {
		case 3:
			v, ok := parseHex(h)
			if !ok {
				return Color{}, false
			}
			r := uint8((v>>8)&0xf) * 17
			g := uint8((v>>4)&0xf) * 17
			b := uint8(v&0xf) * 17
			return RGBColor(r, g, b), true
		case 6:
			v, ok := parseHex(h)
			if !ok {
				return Color{}, false
			}
			return RGBColor(uint8(v>>16), uint8(v>>8), uint8(v)), true
		}
	}
	return Color{}, false
}

// parseHex reads up to four hex digits.
func parseHex(s string) (uint32, bool) {
	var v uint32
	for i := 0; i < len(s); i++ {
		c := s[i]
		var d uint32
		switch {
		case c >= '0' && c <= '9':
			d = uint32(c - '0')
		case c >= 'a' && c <= 'f':
			d = uint32(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = uint32(c-'A') + 10
		default:
			return 0, false
		}
		v = v<<4 | d
	}
	return v, true
}

// scaleHex widens a hex value of digits digits to 8 bits. One digit repeats,
// two are taken as is, and three or four keep the high eight bits, the way
// xterm reads rgb: colours.
func scaleHex(v uint32, digits int) uint8 {
	switch digits {
	case 1:
		return uint8(v * 17)
	case 2:
		return uint8(v)
	case 3, 4:
		return uint8(v >> (4 * uint(digits-2)))
	}
	return 0
}
