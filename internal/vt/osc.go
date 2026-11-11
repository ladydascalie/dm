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
	case 133:
		t.oscPrompt(rest)
	case 7501:
		t.status = rest
	}
}

// oscPrompt handles OSC 133: the shell integration prompt marks. A is the
// prompt start, B the end of the prompt, C the command start and D the command
// end. The mark records the stream byte offset where its sequence began, the
// same coordinate space the yank anchor uses. The D body may carry the exit
// status, either as a bare number or prefixed with 0 for the unsigned format,
// as in "D;0;130". Unknown or malformed bodies are ignored, as every other
// unknown OSC command is.
func (t *Term) oscPrompt(rest string) {
	kind := rest
	params := ""
	if i := strings.IndexByte(kind, ';'); i >= 0 {
		params = kind[i+1:]
		kind = kind[:i]
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return
	}
	switch kind {
	case "A":
		t.mark = "A"
		t.promptPos, t.promptSet = t.markStart, true
	case "C":
		t.mark = "C"
		t.startPos, t.startSet = t.markStart, true
	case "D":
		t.mark = "D"
		t.endPos, t.endSet = t.markStart, true
		code, ok := parseExitCode(params)
		t.endCode, t.endCodeSet = code, ok
	}
}

// parseExitCode reads the exit status of a D body. A bare D has no status, so
// the empty body reports false.
func parseExitCode(params string) (int, bool) {
	fields := strings.Split(params, ";")
	code := fields[0]
	// The canonical shell integration form prefixes a 0 for the unsigned
	// format, as in "0;130"; the 0 is padding, not the status.
	if code == "0" && len(fields) > 1 {
		code = fields[1]
	}
	n, err := strconv.Atoi(code)
	if err != nil {
		return 0, false
	}
	return n, true
}

// AtPrompt reports whether the shell's most recent OSC 133 mark is the prompt
// start, which is where the shell sits once a command has returned. A wait
// that arrives there belongs to the next command, not the one already done.
func (t *Term) AtPrompt() bool { return t.mark == "A" }

// PromptStart returns the most recent OSC 133 prompt start as a stream byte
// offset, and whether one has been seen.
func (t *Term) PromptStart() (pos int64, ok bool) {
	return t.promptPos, t.promptSet
}

// CommandStart returns the most recent OSC 133 C mark as a stream byte offset,
// and whether one has been seen. It is where the command's output begins.
func (t *Term) CommandStart() (pos int64, ok bool) {
	return t.startPos, t.startSet
}

// CommandEnd returns the most recent OSC 133 D mark as a stream byte offset,
// and whether one has been seen. It is where the command's output ends.
func (t *Term) CommandEnd() (pos int64, ok bool) {
	return t.endPos, t.endSet
}

// CommandExit returns the exit status the most recent OSC 133 D mark carried,
// and whether that mark carried one. A bare D reports false.
func (t *Term) CommandExit() (code int, ok bool) {
	return t.endCode, t.endCodeSet
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
