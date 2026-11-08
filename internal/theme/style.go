package theme

import (
	"strconv"
	"strings"
	"sync"
)

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

// Color is one colour of a Style. Kind decides which fields carry meaning:
// Index for ColorPalette, R, G and B for ColorRGB, none for ColorDefault.
type Color struct {
	Kind    ColorKind
	Index   uint8
	R, G, B uint8
}

// DefaultColor returns the terminal default colour.
func DefaultColor() Color { return Color{Kind: ColorDefault} }

// PaletteColor returns palette colour i.
func PaletteColor(i uint8) Color { return Color{Kind: ColorPalette, Index: i} }

// RGBColor returns the 24-bit colour r, g, b.
func RGBColor(r, g, b uint8) Color { return Color{Kind: ColorRGB, R: r, G: g, B: b} }

// Attribute bits for Style.Attrs, one bit per attribute, in SGR order. A cell's
// attribute bits use the same values, so a painter passes them straight
// through.
const (
	AttrBold uint8 = 1 << iota
	AttrFaint
	AttrItalic
	AttrBlink
	AttrRapidBlink
	AttrReverse
	AttrConceal
	AttrStrikethrough
)

// Underline shapes, in SGR order. A cell's underline uses the same values.
const (
	UnderlineNone uint8 = iota
	UnderlineSingle
	UnderlineDouble
	UnderlineCurly
	UnderlineDotted
	UnderlineDashed
)

// Style is one run's colours and attributes: everything its escape sequence
// depends on.
type Style struct {
	Fg, Bg, UnderlineColor Color
	Underline              uint8
	Attrs                  uint8
}

// SGR returns the escape sequence that turns s on from the reset state. The
// sequences are cached by colour and attribute, so a frame that asks for the
// same handful of styles hundreds of times builds each one once. SGRCache
// reports the hit rate.
func (s Style) SGR() string {
	k := styleKey{style: s, depth: depth}
	i := styleHash(k) & (styleCacheSize - 1)

	styleMu.Lock()
	defer styleMu.Unlock()
	if slot := &styleCache[i]; slot.used && slot.key == k {
		styleHits++
		return slot.sgr
	}
	styleMisses++
	sgr := buildSGR(s)
	styleCache[i] = styleSlot{key: k, sgr: sgr, used: true}
	return sgr
}

// SGRCacheStats is the style cache's tally since the process started.
type SGRCacheStats struct{ Hits, Misses uint64 }

// Lookups is how many times the cache was asked, hit or miss.
func (s SGRCacheStats) Lookups() uint64 { return s.Hits + s.Misses }

// Rate is the hit rate between 0 and 1, and 0 when the cache was not asked.
func (s SGRCacheStats) Rate() float64 {
	if n := s.Lookups(); n > 0 {
		return float64(s.Hits) / float64(n)
	}
	return 0
}

// String is the one-line diagnostic the client prints for DM_STATS.
func (s SGRCacheStats) String() string {
	return strconv.FormatUint(s.Hits, 10) + " hits, " +
		strconv.FormatUint(s.Misses, 10) + " misses, " +
		strconv.FormatFloat(100*s.Rate(), 'f', 1, 64) + "% hit rate"
}

// SGRCache returns the style cache's tally.
func SGRCache() SGRCacheStats {
	styleMu.Lock()
	defer styleMu.Unlock()
	return SGRCacheStats{Hits: styleHits, Misses: styleMisses}
}

// ResetSGRCache empties the table and the tally. A test uses it to measure one
// frame; the live client never resets.
func ResetSGRCache() {
	styleMu.Lock()
	defer styleMu.Unlock()
	styleCache = [styleCacheSize]styleSlot{}
	styleHits, styleMisses = 0, 0
}

// styleCacheSize is the number of slots in the style cache. It is a power of
// two, so the hash masks instead of dividing.
const styleCacheSize = 512

// styleKey is everything an escape depends on. The colour depth is part of the
// key, so a test that changes the depth reads the right answer.
type styleKey struct {
	style Style
	depth Depth
}

// styleSlot is one direct-mapped entry. used tells an empty slot from a stored
// default style, whose key is the zero value.
type styleSlot struct {
	key  styleKey
	sgr  string
	used bool
}

var (
	styleMu     sync.Mutex
	styleCache  [styleCacheSize]styleSlot
	styleHits   uint64
	styleMisses uint64
)

// styleHash mixes every byte of the key. The table is small, so a multiply-xor
// hash spreads the handful of styles a frame uses across enough slots.
func styleHash(k styleKey) uint32 {
	const (
		offset = 2166136261
		prime  = 16777619
	)
	h := uint32(offset)
	add := func(v uint32) { h = (h ^ v) * prime }
	colour := func(c Color) {
		add(uint32(c.Kind) | uint32(c.Index)<<8 | uint32(c.R)<<16 | uint32(c.G)<<24)
		add(uint32(c.B))
	}
	colour(k.style.Fg)
	colour(k.style.Bg)
	colour(k.style.UnderlineColor)
	add(uint32(k.style.Underline) | uint32(k.style.Attrs)<<8 | uint32(k.depth)<<16)
	return h
}

// buildSGR is the uncached escape: the reset, the attribute set, the
// foreground, the background and the underline colour.
func buildSGR(s Style) string {
	p := []string{"0"}
	if s.Attrs&AttrBold != 0 {
		p = append(p, "1")
	}
	if s.Attrs&AttrFaint != 0 {
		p = append(p, "2")
	}
	if s.Attrs&AttrItalic != 0 {
		p = append(p, "3")
	}
	switch s.Underline {
	case UnderlineSingle:
		p = append(p, "4")
	case UnderlineDouble:
		p = append(p, "4:2")
	case UnderlineCurly:
		p = append(p, "4:3")
	case UnderlineDotted:
		p = append(p, "4:4")
	case UnderlineDashed:
		p = append(p, "4:5")
	}
	if s.Attrs&AttrBlink != 0 {
		p = append(p, "5")
	}
	if s.Attrs&AttrRapidBlink != 0 {
		p = append(p, "6")
	}
	if s.Attrs&AttrReverse != 0 {
		p = append(p, "7")
	}
	if s.Attrs&AttrConceal != 0 {
		p = append(p, "8")
	}
	if s.Attrs&AttrStrikethrough != 0 {
		p = append(p, "9")
	}
	p = append(p, colorParam(s.Fg, false), colorParam(s.Bg, true))
	if s.UnderlineColor.Kind != ColorDefault {
		p = append(p, underlineParam(s.UnderlineColor))
	}
	return "\x1b[" + strings.Join(p, ";") + "m"
}

// colorParam maps a colour to an SGR parameter at the terminal's colour depth.
// A 24-bit colour steps down to the nearest palette entry, and a palette entry
// above 15 steps down to the nearest ANSI colour.
func colorParam(c Color, bg bool) string {
	switch c.Kind {
	case ColorPalette:
		return PaletteSGR(int(c.Index), bg)
	case ColorRGB:
		return RGB{int(c.R), int(c.G), int(c.B)}.SGR(bg)
	}
	if bg {
		return "49"
	}
	return "39"
}

// underlineParam maps an underline colour to an SGR parameter. SGR 58 has no
// 16-colour form, so a stepped-down colour keeps the 256-colour form.
func underlineParam(c Color) string {
	switch c.Kind {
	case ColorPalette:
		return "58;5;" + strconv.Itoa(int(c.Index))
	case ColorRGB:
		rgb := RGB{int(c.R), int(c.G), int(c.B)}
		switch depth {
		case Color16:
			return "58;5;" + strconv.Itoa(Nearest16(rgb))
		case Color256:
			return "58;5;" + strconv.Itoa(Nearest256(rgb))
		}
		return "58;2;" + strconv.Itoa(int(c.R)) + ";" + strconv.Itoa(int(c.G)) + ";" + strconv.Itoa(int(c.B))
	}
	return "59"
}
