package theme

import (
	"math"
	"strconv"
	"strings"
	"sync"
)

// Depth is the colour depth the outer terminal supports.
type Depth uint8

const (
	// TrueColor is 24-bit colour.
	TrueColor Depth = iota
	// Color256 is the xterm 256-colour palette.
	Color256
	// Color16 is the 16 ANSI colours.
	Color16
)

// depth is what the renderer emits. It is set once at startup, before the first
// frame, and read by every FG, BG and SGR call.
var depth = TrueColor

// SetDepth sets the colour depth the renderer emits.
func SetDepth(d Depth) { depth = d }

// CurrentDepth returns the colour depth the renderer emits.
func CurrentDepth() Depth { return depth }

// DetectDepth reads the environment for the outer terminal's colour depth.
// DM_COLOR overrides everything, so an operator can force a depth when the
// environment lies. COLORTERM wins over TERM, because a terminal that supports
// 24-bit colour says so there and its TERM often does not.
func DetectDepth(getenv func(string) string) Depth {
	switch strings.ToLower(getenv("DM_COLOR")) {
	case "truecolor", "24bit":
		return TrueColor
	case "256":
		return Color256
	case "16":
		return Color16
	}
	switch strings.ToLower(getenv("COLORTERM")) {
	case "truecolor", "24bit":
		return TrueColor
	}
	term := strings.ToLower(getenv("TERM"))
	switch {
	case strings.Contains(term, "256color"):
		return Color256
	case term == "" || term == "dumb":
		return Color16
	}
	return Color16
}

// SGR returns the SGR parameter for the colour at the current depth, without
// the escape wrapper. bg selects the background form.
func (c RGB) SGR(bg bool) string {
	switch depth {
	case Color256:
		return paletteSGR(Nearest256(c), bg)
	case Color16:
		return ansiSGR(Nearest16(c), bg)
	}
	if bg {
		return "48;2;" + strconv.Itoa(c.R) + ";" + strconv.Itoa(c.G) + ";" + strconv.Itoa(c.B)
	}
	return "38;2;" + strconv.Itoa(c.R) + ";" + strconv.Itoa(c.G) + ";" + strconv.Itoa(c.B)
}

// paletteSGR is the 256-colour form of a palette index.
func paletteSGR(n int, bg bool) string {
	if bg {
		return "48;5;" + strconv.Itoa(n)
	}
	return "38;5;" + strconv.Itoa(n)
}

// ansiSGR is the 16-colour form of an ANSI index: 30-37 and 40-47 for the
// normal colours, 90-97 and 100-107 for the bright ones.
func ansiSGR(n int, bg bool) string {
	base := 30
	if bg {
		base = 40
	}
	if n < 8 {
		return strconv.Itoa(base + n)
	}
	return strconv.Itoa(base + 60 + n - 8)
}

// PaletteSGR is the SGR parameter for xterm palette index n at the current
// depth. Above 15 the index steps down to the nearest ANSI colour at 16.
func PaletteSGR(n int, bg bool) string {
	if depth == Color16 {
		if n > 15 {
			n = Nearest16(PaletteRGB(n))
		}
		return ansiSGR(n, bg)
	}
	return paletteSGR(n, bg)
}

// PaletteRGB is the xterm 256-colour palette entry n.
func PaletteRGB(n int) RGB {
	if n < 0 || n > 255 {
		return RGB{}
	}
	return xterm256[n]
}

// Nearest256 is the xterm 256-colour palette index closest to c in OKLab.
func Nearest256(c RGB) int { return cachedNearest(c, Color256) }

// Nearest16 is the ANSI colour closest to c in OKLab.
func Nearest16(c RGB) int { return cachedNearest(c, Color16) }

// nearest is the index of the palette entry closest to c in OKLab. The palette
// arrives already converted, so a lookup costs one conversion and a scan.
func nearest(c RGB, pal []oklab) int {
	target := toOKLab(c)
	best, bestD := 0, math.Inf(1)
	for i, p := range pal {
		if d := target.dist2(p); d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// nearestCache memoises the search. A frame asks for the same handful of
// colours thousands of times, and the search is 256 OKLab distances.
var (
	nearestMu    sync.Mutex
	nearestCache = map[nearestKey]int{}
)

type nearestKey struct {
	c     RGB
	depth Depth
}

// cachedNearest is nearest with the depth in the key, so a test that changes
// the depth does not read a stale answer.
func cachedNearest(c RGB, d Depth) int {
	nearestMu.Lock()
	defer nearestMu.Unlock()
	k := nearestKey{c: c, depth: d}
	if n, ok := nearestCache[k]; ok {
		return n
	}
	var n int
	if d == Color16 {
		n = nearest(c, xterm256Lab[:16])
	} else {
		n = nearest(c, xterm256Lab[:])
	}
	nearestCache[k] = n
	return n
}

// oklab is the OKLab form of an sRGB colour.
type oklab struct{ l, a, b float64 }

// dist2 is the squared distance between two OKLab colours. Squared is enough
// to pick a nearest neighbour, and it skips a square root per candidate.
func (a oklab) dist2(b oklab) float64 {
	dl, da, db := a.l-b.l, a.a-b.a, a.b-b.b
	return dl*dl + da*da + db*db
}

// toOKLab converts an sRGB colour to OKLab, the perceptually uniform space
// where a nearest-neighbour step-down looks closest to the original.
func toOKLab(c RGB) oklab {
	r, g, b := srgbToLinear(c.R), srgbToLinear(c.G), srgbToLinear(c.B)
	l := 0.4122214708*r + 0.5363325363*g + 0.0514459929*b
	m := 0.2119034982*r + 0.6806995451*g + 0.1073969566*b
	s := 0.0883024619*r + 0.2817188376*g + 0.6299787005*b
	l, m, s = math.Cbrt(l), math.Cbrt(m), math.Cbrt(s)
	return oklab{
		l: 0.2104542553*l + 0.7936177850*m - 0.0040720468*s,
		a: 1.9779984951*l - 2.4285922050*m + 0.4505937099*s,
		b: 0.0259040371*l + 0.7827717662*m - 0.8086757660*s,
	}
}

// srgbToLinear undoes the sRGB transfer function for one channel.
func srgbToLinear(v int) float64 {
	f := float64(v) / 255
	if f <= 0.04045 {
		return f / 12.92
	}
	return math.Pow((f+0.055)/1.055, 2.4)
}

// xterm256 is the xterm 256-colour palette: the 16 system colours, a 6x6x6
// cube, then 24 greys.
var xterm256 = func() [256]RGB {
	var p [256]RGB
	system := [16]RGB{
		{0, 0, 0}, {128, 0, 0}, {0, 128, 0}, {128, 128, 0},
		{0, 0, 128}, {128, 0, 128}, {0, 128, 128}, {192, 192, 192},
		{128, 128, 128}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0},
		{0, 0, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
	}
	copy(p[:16], system[:])
	levels := [6]int{0, 95, 135, 175, 215, 255}
	for i := 0; i < 216; i++ {
		p[16+i] = RGB{levels[i/36], levels[(i/6)%6], levels[i%6]}
	}
	for i := 0; i < 24; i++ {
		v := 8 + 10*i
		p[232+i] = RGB{v, v, v}
	}
	return p
}()

// xterm256Lab is xterm256 in OKLab, converted once.
var xterm256Lab = func() [256]oklab {
	var p [256]oklab
	for i, c := range xterm256 {
		p[i] = toOKLab(c)
	}
	return p
}()
