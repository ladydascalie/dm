package vt

import "strings"

// registerSGR registers the rendition family: SGR.
func registerSGR(t map[csiKey]csiHandler) {
	t[csiKey{Final: 'm'}] = csiSGR
}

// csiSGR handles SGR. It reads the raw parameters, not the parsed ones,
// because SGR carries colon sub-parameters such as 4:3 and 38:2::r:g:b.
func csiSGR(term *Term, seq CSI) {
	fields := sgrFields(seq.Raw)
	if len(fields) == 0 {
		term.style = DefaultStyle()
		return
	}
	for i := 0; i < len(fields); i++ {
		sub := fields[i]
		v := sub[0]
		switch {
		case v == 0:
			term.style = DefaultStyle()
		case v == 1:
			term.setAttr(Bold, true)
		case v == 2:
			term.setAttr(Faint, true)
		case v == 3:
			term.setAttr(Italic, true)
		case v == 4:
			if len(sub) > 1 {
				term.style.Underline = sgrUnderline(sub[1])
			} else {
				term.style.Underline = UnderlineSingle
			}
		case v == 5:
			term.setAttr(Blink, true)
		case v == 6:
			term.setAttr(RapidBlink, true)
		case v == 7:
			term.setAttr(Reverse, true)
		case v == 8:
			term.setAttr(Conceal, true)
		case v == 9:
			term.setAttr(Strikethrough, true)
		case v == 21:
			term.style.Underline = UnderlineDouble
		case v == 22:
			term.setAttr(Bold, false)
			term.setAttr(Faint, false)
		case v == 23:
			term.setAttr(Italic, false)
		case v == 24:
			term.style.Underline = UnderlineNone
		case v == 25:
			term.setAttr(Blink, false)
			term.setAttr(RapidBlink, false)
		case v == 27:
			term.setAttr(Reverse, false)
		case v == 28:
			term.setAttr(Conceal, false)
		case v == 29:
			term.setAttr(Strikethrough, false)
		case v >= 30 && v <= 37:
			term.style.Fg = PaletteColor(uint8(v - 30))
		case v == 38:
			i = term.sgrExtendedColor(sub, fields, i, &term.style.Fg)
		case v == 39:
			term.style.Fg = DefaultColor()
		case v >= 40 && v <= 47:
			term.style.Bg = PaletteColor(uint8(v - 40))
		case v == 48:
			i = term.sgrExtendedColor(sub, fields, i, &term.style.Bg)
		case v == 49:
			term.style.Bg = DefaultColor()
		case v == 58:
			i = term.sgrExtendedColor(sub, fields, i, &term.style.UnderlineColor)
		case v == 59:
			term.style.UnderlineColor = DefaultColor()
		case v >= 90 && v <= 97:
			term.style.Fg = PaletteColor(uint8(v - 90 + 8))
		case v >= 100 && v <= 107:
			term.style.Bg = PaletteColor(uint8(v - 100 + 8))
		}
	}
}

// sgrFields splits the raw parameters on ';' and then each field on ':'.
// A missing number reads as 0.
func sgrFields(raw []byte) [][]int {
	body := strings.TrimSpace(string(raw))
	if body == "" {
		return nil
	}
	fields := strings.Split(body, ";")
	out := make([][]int, len(fields))
	for i, f := range fields {
		subs := strings.Split(f, ":")
		nums := make([]int, len(subs))
		for j, s := range subs {
			nums[j] = sgrNumber(s)
		}
		out[i] = nums
	}
	return out
}

// sgrNumber reads one decimal parameter. An empty or malformed field is 0.
func sgrNumber(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0
		}
		n = n*10 + int(s[i]-'0')
	}
	return n
}

// sgrUnderline maps the colon sub-parameter of SGR 4.
func sgrUnderline(v int) UnderlineStyle {
	switch v {
	case 0:
		return UnderlineNone
	case 1:
		return UnderlineSingle
	case 2:
		return UnderlineDouble
	case 3:
		return UnderlineCurly
	case 4:
		return UnderlineDotted
	case 5:
		return UnderlineDashed
	}
	return UnderlineNone
}

// setAttr turns one style attribute on or off.
func (t *Term) setAttr(bit uint8, on bool) {
	if on {
		t.style.Attrs |= bit
		return
	}
	t.style.Attrs &^= bit
}

// sgrExtendedColor handles 38, 48 and 58, in both the semicolon and the colon
// form. It returns the index of the last parameter it consumed.
func (t *Term) sgrExtendedColor(sub []int, fields [][]int, i int, dst *Color) int {
	if len(sub) > 1 {
		switch sub[1] {
		case 5:
			if len(sub) > 2 {
				*dst = sgrPaletteColor(sub[2])
			}
		case 2:
			vals := sub[2:]
			if len(vals) >= 4 {
				// 38:2:ColorSpaceId:R:G:B
				vals = vals[1:]
			}
			if len(vals) >= 3 {
				*dst = RGBColor(uint8(vals[0]), uint8(vals[1]), uint8(vals[2]))
			}
		}
		return i
	}
	// Semicolon form: the next field is the colour mode.
	j := i + 1
	if j >= len(fields) {
		return i
	}
	switch fields[j][0] {
	case 5:
		if j+1 < len(fields) {
			*dst = sgrPaletteColor(fields[j+1][0])
			return j + 1
		}
	case 2:
		if j+3 < len(fields) {
			*dst = RGBColor(
				uint8(fields[j+1][0]),
				uint8(fields[j+2][0]),
				uint8(fields[j+3][0]),
			)
			return j + 3
		}
	}
	return j
}

// sgrPaletteColor clamps a palette index to the model's 256 entries.
func sgrPaletteColor(v int) Color {
	if v < 0 || v > 255 {
		return DefaultColor()
	}
	return PaletteColor(uint8(v))
}
