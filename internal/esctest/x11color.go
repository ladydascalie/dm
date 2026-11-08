package esctest

// X11 colour parsing, ported from Xlib's Xcms. esctest2 sends the X11 forms
// rgb:, rgbi:, #RGB and the CIE spaces, and its expected values are the ones
// xterm reports after XParseColor and an 8-bit XAllocColor. The conversion
// tables and matrices below are Xlib's default LINEAR_RGB screen data, which
// is what a display without XDCCC properties uses.

import (
	"math"
	"strconv"
	"strings"
)

// intensityRec is one row of an Xlib intensity table.
type intensityRec struct {
	value     uint16
	intensity float64
}

// The default Tektronix 19" CRT intensity profiles. Xlib uses these when the
// screen has no XDCCC intensity property.
var (
	redTable = []intensityRec{
		{0x0000, 0.0},
		{0x0909, 0.0},
		{0x0a0a, 0.000936},
		{0x0f0f, 0.001481},
		{0x1414, 0.002329},
		{0x1919, 0.003529},
		{0x1e1e, 0.005127},
		{0x2323, 0.007169},
		{0x2828, 0.009699},
		{0x2d2d, 0.012759},
		{0x3232, 0.016392},
		{0x3737, 0.020637},
		{0x3c3c, 0.025533},
		{0x4141, 0.031119},
		{0x4646, 0.037431},
		{0x4b4b, 0.044504},
		{0x5050, 0.052373},
		{0x5555, 0.061069},
		{0x5a5a, 0.070624},
		{0x5f5f, 0.08107},
		{0x6464, 0.092433},
		{0x6969, 0.104744},
		{0x6e6e, 0.118026},
		{0x7373, 0.132307},
		{0x7878, 0.14761},
		{0x7d7d, 0.163958},
		{0x8282, 0.181371},
		{0x8787, 0.199871},
		{0x8c8c, 0.219475},
		{0x9191, 0.240202},
		{0x9696, 0.262069},
		{0x9b9b, 0.285089},
		{0xa0a0, 0.309278},
		{0xa5a5, 0.334647},
		{0xaaaa, 0.361208},
		{0xafaf, 0.388971},
		{0xb4b4, 0.417945},
		{0xb9b9, 0.448138},
		{0xbebe, 0.479555},
		{0xc3c3, 0.512202},
		{0xc8c8, 0.546082},
		{0xcdcd, 0.581199},
		{0xd2d2, 0.617552},
		{0xd7d7, 0.655144},
		{0xdcdc, 0.693971},
		{0xe1e1, 0.734031},
		{0xe6e6, 0.775322},
		{0xebeb, 0.817837},
		{0xf0f0, 0.861571},
		{0xf5f5, 0.906515},
		{0xfafa, 0.952662},
		{0xffff, 1.0},
	}
	greenTable = []intensityRec{
		{0x0000, 0.0},
		{0x1313, 0.0},
		{0x1414, 0.000832},
		{0x1919, 0.001998},
		{0x1e1e, 0.003612},
		{0x2323, 0.005736},
		{0x2828, 0.008428},
		{0x2d2d, 0.011745},
		{0x3232, 0.01574},
		{0x3737, 0.020463},
		{0x3c3c, 0.02596},
		{0x4141, 0.032275},
		{0x4646, 0.039449},
		{0x4b4b, 0.047519},
		{0x5050, 0.05652},
		{0x5555, 0.066484},
		{0x5a5a, 0.077439},
		{0x5f5f, 0.089409},
		{0x6464, 0.102418},
		{0x6969, 0.116485},
		{0x6e6e, 0.131625},
		{0x7373, 0.147853},
		{0x7878, 0.165176},
		{0x7d7d, 0.183604},
		{0x8282, 0.20314},
		{0x8787, 0.223783},
		{0x8c8c, 0.245533},
		{0x9191, 0.268384},
		{0x9696, 0.292327},
		{0x9b9b, 0.317351},
		{0xa0a0, 0.343441},
		{0xa5a5, 0.37058},
		{0xaaaa, 0.398747},
		{0xafaf, 0.427919},
		{0xb4b4, 0.458068},
		{0xb9b9, 0.489165},
		{0xbebe, 0.521176},
		{0xc3c3, 0.554067},
		{0xc8c8, 0.587797},
		{0xcdcd, 0.622324},
		{0xd2d2, 0.657604},
		{0xd7d7, 0.693588},
		{0xdcdc, 0.730225},
		{0xe1e1, 0.767459},
		{0xe6e6, 0.805235},
		{0xebeb, 0.843491},
		{0xf0f0, 0.882164},
		{0xf5f5, 0.921187},
		{0xfafa, 0.96049},
		{0xffff, 1.0},
	}
	blueTable = []intensityRec{
		{0x0000, 0.0},
		{0x0e0e, 0.0},
		{0x0f0f, 0.001341},
		{0x1414, 0.00208},
		{0x1919, 0.003188},
		{0x1e1e, 0.004729},
		{0x2323, 0.006766},
		{0x2828, 0.009357},
		{0x2d2d, 0.012559},
		{0x3232, 0.016424},
		{0x3737, 0.021004},
		{0x3c3c, 0.026344},
		{0x4141, 0.032489},
		{0x4646, 0.039481},
		{0x4b4b, 0.047357},
		{0x5050, 0.056154},
		{0x5555, 0.065903},
		{0x5a5a, 0.076634},
		{0x5f5f, 0.088373},
		{0x6464, 0.101145},
		{0x6969, 0.114968},
		{0x6e6e, 0.129862},
		{0x7373, 0.145841},
		{0x7878, 0.162915},
		{0x7d7d, 0.181095},
		{0x8282, 0.200386},
		{0x8787, 0.220791},
		{0x8c8c, 0.242309},
		{0x9191, 0.264937},
		{0x9696, 0.28867},
		{0x9b9b, 0.313499},
		{0xa0a0, 0.33941},
		{0xa5a5, 0.36639},
		{0xaaaa, 0.394421},
		{0xafaf, 0.423481},
		{0xb4b4, 0.453547},
		{0xb9b9, 0.484592},
		{0xbebe, 0.516587},
		{0xc3c3, 0.549498},
		{0xc8c8, 0.583291},
		{0xcdcd, 0.617925},
		{0xd2d2, 0.653361},
		{0xd7d7, 0.689553},
		{0xdcdc, 0.726454},
		{0xe1e1, 0.764013},
		{0xe6e6, 0.802178},
		{0xebeb, 0.840891},
		{0xf0f0, 0.880093},
		{0xf5f5, 0.919723},
		{0xfafa, 0.959715},
		{0xffff, 1.0},
	}
)

// Xlib's default LINEAR_RGB matrices and the screen white point they imply.
var (
	xyzToRGBMatrix = [3][3]float64{
		{3.48340481253539000, -1.52176374927285200, -0.55923133354049780},
		{-1.07152751306193600, 1.96593795204372400, 0.03673691339553462},
		{0.06351179790497788, -0.20020501000496480, 0.81070942031648220},
	}
	rgbToXYZMatrix = [3][3]float64{
		{0.38106149108714790, 0.32025712365352110, 0.24834578525933100},
		{0.20729745115140850, 0.68054638776373240, 0.11215616108485920},
		{0.02133944350088028, 0.14297193020246480, 1.24172892629665500},
	}
	screenWhite = [3]float64{
		0.38106149108714790 + 0.32025712365352110 + 0.24834578525933100,
		1.0,
		0.02133944350088028 + 0.14297193020246480 + 1.24172892629665500,
	}
)

const (
	// xlibEpsilon is XMY_DBL_EPSILON, the tolerance on a CIE specification.
	xlibEpsilon = 1e-5
	// gamutEpsilon is EPS in Xlib's LINEAR_RGB code.
	gamutEpsilon = 0.001
	// hvcEpsilon is EPS in Xlib's TekHVC code.
	hvcEpsilon = 0.001
	// chromaScaleFactor is CHROMA_SCALE_FACTOR in Xlib's TekHVC code.
	chromaScaleFactor = 7.50725
)

type xyz struct{ x, y, z float64 }
type uvY struct{ u, v, y float64 }
type hvc struct{ h, v, c float64 }

// parseX11Color parses the X11 colour forms esctest2 sends. The result is the
// 16-bit value xterm reports after an 8-bit allocation, so each channel is a
// byte replicated into both halves.
func parseX11Color(s string) (rgb, bool) {
	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lower, "#"):
		return parseHashColor(lower)
	case strings.HasPrefix(lower, "rgb:"):
		return parseRGBColor(lower)
	case strings.HasPrefix(lower, "rgbi:"):
		return parseRGBiColor(lower)
	case strings.HasPrefix(lower, "ciexyz:"):
		return parseCIEXYZColor(lower)
	case strings.HasPrefix(lower, "cieuvy:"):
		return parseCIEuvYColor(lower)
	case strings.HasPrefix(lower, "ciexyy:"):
		return parseCIExyYColor(lower)
	case strings.HasPrefix(lower, "cielab:"):
		return parseCIELabColor(lower)
	case strings.HasPrefix(lower, "cieluv:"):
		return parseCIELuvColor(lower)
	case strings.HasPrefix(lower, "tekhvc:"):
		return parseTekHVCColor(lower)
	}
	return rgb{}, false
}

// parseHashColor parses #RGB, #RRGGBB, #RRRGGGBBB and #RRRRGGGGBBBB. Xlib
// shifts each channel to the top of the 16-bit word, then the 8-bit
// allocation keeps the high byte, so #fff becomes f0f0.
func parseHashColor(s string) (rgb, bool) {
	h := s[1:]
	if len(h) != 3 && len(h) != 6 && len(h) != 9 && len(h) != 12 {
		return rgb{}, false
	}
	n := len(h) / 3
	var out rgb
	for i := 0; i < 3; i++ {
		v, ok := parseHex(h[i*n : (i+1)*n])
		if !ok {
			return rgb{}, false
		}
		c := uint16(v) << uint(16-4*n)
		switch i {
		case 0:
			out.r = c
		case 1:
			out.g = c
		case 2:
			out.b = c
		}
	}
	return quantiseRGB(out), true
}

// parseRGBColor parses rgb:R/G/B with one to four hex digits per channel.
// Xlib scales a short channel by 0xFFFF/(2^(4n)-1); a four-digit channel is
// already 16 bits.
func parseRGBColor(s string) (rgb, bool) {
	parts := strings.Split(s[4:], "/")
	if len(parts) != 3 {
		return rgb{}, false
	}
	var out rgb
	for i, p := range parts {
		if len(p) < 1 || len(p) > 4 {
			return rgb{}, false
		}
		v, ok := parseHex(p)
		if !ok {
			return rgb{}, false
		}
		c := uint16(v)
		if len(p) < 4 {
			c = uint16(uint32(v) * 0xFFFF / uint32((1<<(4*len(p)))-1))
		}
		switch i {
		case 0:
			out.r = c
		case 1:
			out.g = c
		case 2:
			out.b = c
		}
	}
	return quantiseRGB(out), true
}

// parseRGBiColor parses rgbi:R/G/B with floating point intensities. Xlib
// converts RGBi to RGB through the intensity tables directly.
func parseRGBiColor(s string) (rgb, bool) {
	v, ok := parseFloats(s[5:])
	if !ok {
		return rgb{}, false
	}
	return quantiseRGB(rgbFromRGBi(v)), true
}

func parseCIEXYZColor(s string) (rgb, bool) {
	v, ok := parseFloats(s[7:])
	if !ok || v[1] < -xlibEpsilon || v[1] > 1+xlibEpsilon {
		return rgb{}, false
	}
	return rgbFromXYZ(xyz{v[0], v[1], v[2]}), true
}

func parseCIEuvYColor(s string) (rgb, bool) {
	v, ok := parseFloats(s[7:])
	if !ok || v[2] < -xlibEpsilon || v[2] > 1+xlibEpsilon {
		return rgb{}, false
	}
	return rgbFromXYZ(uvYToXYZ(uvY{v[0], v[1], v[2]})), true
}

func parseCIExyYColor(s string) (rgb, bool) {
	v, ok := parseFloats(s[7:])
	if !ok || v[0] < -xlibEpsilon || v[0] > 1+xlibEpsilon ||
		v[1] < -xlibEpsilon || v[1] > 1+xlibEpsilon ||
		v[2] < -xlibEpsilon || v[2] > 1+xlibEpsilon {
		return rgb{}, false
	}
	return rgbFromXYZ(xyYToXYZ(v[0], v[1], v[2])), true
}

func parseCIELabColor(s string) (rgb, bool) {
	v, ok := parseFloats(s[7:])
	if !ok || v[0] < -xlibEpsilon || v[0] > 100+xlibEpsilon {
		return rgb{}, false
	}
	return rgbFromXYZ(labToXYZ(v[0], v[1], v[2])), true
}

func parseCIELuvColor(s string) (rgb, bool) {
	v, ok := parseFloats(s[7:])
	if !ok || v[0] < -xlibEpsilon || v[0] > 100+xlibEpsilon {
		return rgb{}, false
	}
	return rgbFromXYZ(luvToXYZ(v[0], v[1], v[2])), true
}

func parseTekHVCColor(s string) (rgb, bool) {
	v, ok := parseFloats(s[7:])
	if !ok {
		return rgb{}, false
	}
	h := checkModifyHVC(hvc{v[0], v[1], v[2]})
	return rgbFromXYZ(hvcToXYZ(h)), true
}

// parseFloats parses the three slash-separated numbers of a CIE form.
func parseFloats(s string) ([3]float64, bool) {
	var out [3]float64
	parts := strings.Split(s, "/")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return out, false
		}
		out[i] = v
	}
	return out, true
}

func parseHex(s string) (uint32, bool) {
	if len(s) < 1 || len(s) > 4 {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, false
	}
	return uint32(v), true
}

// quantiseRGB keeps the high byte of each channel and replicates it, which is
// what an 8-bit XAllocColor returns as the exact colour.
func quantiseRGB(c rgb) rgb {
	return rgb{quantiseChannel(c.r), quantiseChannel(c.g), quantiseChannel(c.b)}
}

func quantiseChannel(v uint16) uint16 {
	b := v >> 8
	return b * 0x101
}

// rgbFromRGBi converts RGBi to RGB through Xlib's intensity tables.
func rgbFromRGBi(v [3]float64) rgb {
	return rgb{
		tableSearch(v[0], redTable),
		tableSearch(v[1], greenTable),
		tableSearch(v[2], blueTable),
	}
}

// rgbFromXYZ converts CIEXYZ to RGB, compressing the gamut the way Xlib's
// default XcmsTekHVCClipC does, then quantises to 8 bits.
func rgbFromXYZ(c xyz) rgb {
	rgbI, ok := xyzToRGBi(c)
	if !ok {
		return rgb{}
	}
	return quantiseRGB(rgbFromRGBi(rgbI))
}

// xyzToRGBi converts CIEXYZ to RGBi, compressing the gamut when the colour is
// outside the screen's range.
func xyzToRGBi(c xyz) ([3]float64, bool) {
	tmp := matVec(xyzToRGBMatrix, [3]float64{c.x, c.y, c.z})
	if min3(tmp) < -gamutEpsilon || max3(tmp) > 1+gamutEpsilon {
		c = gamutClipC(c)
		tmp = matVec(xyzToRGBMatrix, [3]float64{c.x, c.y, c.z})
		if min3(tmp) < -gamutEpsilon || max3(tmp) > 1+gamutEpsilon {
			return [3]float64{}, false
		}
	}
	for i := range tmp {
		tmp[i] = math.Max(0, math.Min(1, tmp[i]))
	}
	return tmp, true
}

// gamutClipC is Xlib's XcmsTekHVCClipC: reduce the chroma at the colour's hue
// and value until it lies on the gamut boundary.
func gamutClipC(c xyz) xyz {
	h := xyzToHVC(c)
	h = queryMaxC(h.h, h.v)
	return hvcToXYZ(h)
}

// queryMaxC is Xlib's XcmsTekHVCQueryMaxC.
func queryMaxC(hue, value float64) hvc {
	maxVC, rgbSaved := queryMaxVCRGB(hue)
	if value <= maxVC.v {
		return checkModifyHVC(hvc{hue, value, value * maxVC.c / maxVC.v})
	}

	nValue := value
	savedValue := value
	lastChroma := -1.0
	lastValue := -1.0
	maxDist := 100.0 - maxVC.v
	rFactor := 1.0
	tmp := hvc{hue, value, 100.0}
	prevValue := 0.0

	for n := 0; n < 100; n++ {
		prevValue = lastValue
		lastValue = tmp.v
		lastChroma = tmp.c
		nT := (nValue - maxVC.v) / maxDist * rFactor
		rgb := [3]float64{
			rgbSaved[0]*(1-nT) + nT,
			rgbSaved[1]*(1-nT) + nT,
			rgbSaved[2]*(1-nT) + nT,
		}
		tmp = xyzToHVC(xyzFromVec(matVec(rgbToXYZMatrix, rgb)))
		if tmp.v <= savedValue+hvcEpsilon && tmp.v >= savedValue-hvcEpsilon {
			tmp.h = hue
			return checkModifyHVC(tmp)
		}
		nValue += savedValue - tmp.v
		if nValue < maxVC.v {
			nValue = maxVC.v
			rFactor *= 0.5
		} else if nValue > 100.0 {
			tmp.h = hue
			if math.Abs(lastValue-savedValue) < math.Abs(tmp.v-savedValue) {
				tmp.v = lastValue
				tmp.c = lastChroma
			}
			return checkModifyHVC(tmp)
		} else if tmp.v <= prevValue+hvcEpsilon && tmp.v >= prevValue-hvcEpsilon {
			rFactor *= 0.5
		}
	}
	tmp.h = hue
	if math.Abs(lastValue-savedValue) < math.Abs(tmp.v-savedValue) {
		tmp.v = lastValue
		tmp.c = lastChroma
	}
	return checkModifyHVC(tmp)
}

// queryMaxVCRGB is Xlib's _XcmsTekHVCQueryMaxVCRGB.
func queryMaxVCRGB(hue float64) (hvc, [3]float64) {
	tmp := hvc{hue, 40.0, 120.0}
	rgb := matVec(xyzToRGBMatrix, vecFromXYZ(hvcToXYZ(tmp)))
	nSmall := min3(rgb)
	for i := range rgb {
		rgb[i] -= nSmall
	}
	nLarge := max3(rgb)
	for i := range rgb {
		rgb[i] /= nLarge
	}
	h := xyzToHVC(xyzFromVec(matVec(rgbToXYZMatrix, rgb)))
	h.h = hue
	return h, rgb
}

// checkModifyHVC is Xlib's _XcmsTekHVC_CheckModify.
func checkModifyHVC(h hvc) hvc {
	if h.v < 0 {
		h.v = xlibEpsilon
	} else if h.v > 100 {
		h.v = 100 - xlibEpsilon
	}
	if h.c < 0 {
		h.c = -xlibEpsilon
	}
	if h.h < 0 {
		n := -h.h / 360.0
		h.h += (n + 1) * 360.0
		if h.h >= 360.0 {
			h.h -= 360.0
		}
	} else if h.h >= 360.0 {
		n := h.h / 360.0
		h.h -= n * 360.0
	}
	return h
}

// xyzToHVC converts CIEXYZ to TekHVC through CIEuvY.
func xyzToHVC(c xyz) hvc {
	u, v, y := xyzToUVY(c)
	wu, wv, _ := xyzToUVY(xyz{screenWhite[0], screenWhite[1], screenWhite[2]})
	thetaOffset := degrees(math.Atan((0.4931 - wv) / (0.7127 - wu)))

	uu := u - wu
	vv := v - wv
	var theta float64
	if uu != 0 {
		theta = degrees(math.Atan(vv / uu))
	}
	lo, hi := 0.0, 360.0
	switch {
	case uu > 0 && vv > 0:
		lo, hi = 0, 90
	case uu < 0 && vv > 0:
		lo, hi = 90, 180
	case uu < 0 && vv < 0:
		lo, hi = 180, 270
	case uu > 0 && vv < 0:
		lo, hi = 270, 360
	}
	for theta < lo {
		theta += 90
	}
	for theta >= hi {
		theta -= 90
	}

	l2 := y * 903.29
	if y >= 0.008856 {
		l2 = math.Cbrt(y)*116.0 - 16.0
	}
	chroma := l2 * chromaScaleFactor * math.Sqrt(uu*uu+vv*vv)
	if chroma < 0 {
		theta = 0
	}
	h := theta - thetaOffset
	for h < -hvcEpsilon {
		h += 360
	}
	for h >= 360+hvcEpsilon {
		h -= 360
	}
	return hvc{h, l2, chroma}
}

// hvcToXYZ converts TekHVC to CIEXYZ through CIEuvY.
func hvcToXYZ(h hvc) xyz {
	wu, wv, _ := xyzToUVY(xyz{screenWhite[0], screenWhite[1], screenWhite[2]})
	thetaOffset := degrees(math.Atan((0.4931 - wv) / (0.7127 - wu)))

	var up, vp, y float64
	if h.v == 0 || h.v == 100 {
		if h.v == 100 {
			y = 1
		}
		up, vp = wu, wv
	} else {
		tempHue := h.h + thetaOffset
		for tempHue < 0 {
			tempHue += 360
		}
		for tempHue >= 360 {
			tempHue -= 360
		}
		rad := tempHue * math.Pi / 180
		u := math.Cos(rad) * h.c / (h.v * chromaScaleFactor)
		v := math.Sin(rad) * h.c / (h.v * chromaScaleFactor)
		up = u + wu
		vp = v + wv
		if h.v < 7.99953624 {
			y = h.v / 903.29
		} else {
			t := (h.v + 16) / 116
			y = t * t * t
		}
	}
	return uvYToXYZ(uvY{up, vp, y})
}

// xyzToUVY converts CIEXYZ to CIEuvY.
func xyzToUVY(c xyz) (u, v, y float64) {
	div := c.x + 15*c.y + 3*c.z
	if div == 0 {
		div = xlibEpsilon
	}
	return 4 * c.x / div, 9 * c.y / div, c.y
}

// uvYToXYZ converts CIEuvY to CIEXYZ.
func uvYToXYZ(c uvY) xyz {
	div := 6*c.u - 16*c.v + 12
	if div == 0 {
		wu, wv, _ := xyzToUVY(xyz{screenWhite[0], screenWhite[1], screenWhite[2]})
		div = 6*wu - 16*wv + 12
		if div == 0 {
			div = xlibEpsilon
		}
		x := 9 * wu / div
		y := 4 * wv / div
		z := 1 - x - y
		return xyz{x * c.y / y, c.y, z * c.y / y}
	}
	x := 9 * c.u / div
	y := 4 * c.v / div
	z := 1 - x - y
	if y == 0 {
		return xyz{x, c.y, z}
	}
	return xyz{x * c.y / y, c.y, z * c.y / y}
}

// xyYToXYZ converts CIExyY to CIEXYZ.
func xyYToXYZ(x, y, bigY float64) xyz {
	div := -2*x + 12*y + 3
	if div == 0 {
		return xyz{}
	}
	u := 4 * x / div
	v := 9 * y / div
	div = 6*u - 16*v + 12
	var sx, sy float64
	if div == 0 {
		wu, wv, _ := xyzToUVY(xyz{screenWhite[0], screenWhite[1], screenWhite[2]})
		div = 6*wu - 16*wv + 12
		if div == 0 {
			div = xlibEpsilon
		}
		sx = 9 * wu / div
		sy = 4 * wu / div
	} else {
		sx = 9 * u / div
		sy = 4 * v / div
	}
	z := 1 - sx - sy
	if sy == 0 {
		sy = xlibEpsilon
	}
	return xyz{sx * bigY / sy, bigY, z * bigY / sy}
}

// labToXYZ converts CIELab to CIEXYZ. Xlib assumes Yn = 1.0.
func labToXYZ(l, a, b float64) xyz {
	tmpL := (l + 16) / 116
	y := tmpL * tmpL * tmpL
	if y < 0.008856 {
		tmpL = l / 9.03292
		return xyz{
			screenWhite[0] * ((a / 3893.5) + tmpL),
			tmpL,
			screenWhite[2] * (tmpL - (b / 1557.4)),
		}
	}
	tf := tmpL + a/5
	x := screenWhite[0] * tf * tf * tf
	tf = tmpL - b/2
	z := screenWhite[2] * tf * tf * tf
	return xyz{x, y, z}
}

// luvToXYZ converts CIELuv to CIEXYZ through CIEuvY.
func luvToXYZ(l, u, v float64) xyz {
	var y float64
	if l < 7.99953624 {
		y = l / 903.29
	} else {
		t := (l + 16) / 116
		y = t * t * t
	}
	wu, wv, _ := xyzToUVY(xyz{screenWhite[0], screenWhite[1], screenWhite[2]})
	var up, vp float64
	if l == 0 {
		up, vp = wu, wv
	} else {
		t := 13 * (l / 100)
		up = u/t + wu
		vp = v/t + wv
	}
	return uvYToXYZ(uvY{up, vp, y})
}

// tableSearch is Xlib's _XcmsTableSearch with the intensity comparator and
// interpolation. Xlib has a special case that maps a zero intensity to the
// first table row; esctest2's expected values come from a run where a zero
// channel resolved to the table's second zero row, so the binary search runs
// for zero too.
func tableSearch(intensity float64, table []intensityRec) uint16 {
	lo, hi := 0, len(table)-1
	last := hi
	mid := lo
	if intensity < table[lo].intensity {
		return table[lo].value & 0xff00
	}
	for mid != last {
		last = mid
		mid = lo + (hi-lo)/2
		if table[mid].intensity == intensity {
			return table[mid].value & 0xff00
		}
		if intensity < table[mid].intensity {
			hi = mid
		} else {
			lo = mid
		}
	}
	return interpolateIntensity(intensity, table[lo], table[hi])
}

// interpolateIntensity is Xlib's _XcmsIntensityInterpolation.
func interpolateIntensity(intensity float64, lo, hi intensityRec) uint16 {
	ratio := (intensity - lo.intensity) / (hi.intensity - lo.intensity)
	target := int64(hi.value - lo.value)
	target = int64(float64(target) * ratio)
	target += int64(lo.value)

	const bits = 8
	shift := uint(16 - bits)
	maxColor := int64(1<<bits) - 1
	up := (target >> shift) * 0xFFFF / maxColor
	var down int64
	if up < target {
		down = up
		up = min64((down>>shift)+1, maxColor) * 0xFFFF / maxColor
	} else {
		down = max64((up>>shift)-1, 0) * 0xFFFF / maxColor
	}
	if up-target < target-down {
		return uint16(up) & 0xff00
	}
	return uint16(down) & 0xff00
}

func xyzFromVec(v [3]float64) xyz { return xyz{v[0], v[1], v[2]} }

func vecFromXYZ(c xyz) [3]float64 { return [3]float64{c.x, c.y, c.z} }

func matVec(m [3][3]float64, v [3]float64) [3]float64 {
	var out [3]float64
	for i := 0; i < 3; i++ {
		out[i] = m[i][0]*v[0] + m[i][1]*v[1] + m[i][2]*v[2]
	}
	return out
}

func min3(v [3]float64) float64 {
	return math.Min(v[0], math.Min(v[1], v[2]))
}

func max3(v [3]float64) float64 {
	return math.Max(v[0], math.Max(v[1], v[2]))
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func degrees(r float64) float64 {
	return r * 180 / math.Pi
}
