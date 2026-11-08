package theme

import (
	"strconv"
	"testing"
)

// setDepth sets the render depth for one test and restores it after.
func setDepth(t *testing.T, d Depth) {
	t.Helper()
	old := CurrentDepth()
	SetDepth(d)
	t.Cleanup(func() { SetDepth(old); ResetSGRCache() })
	ResetSGRCache()
}

// TestStyleSGR pins one escape carrying every field: the attributes, the
// underline shape and all three colours.
func TestStyleSGR(t *testing.T) {
	setDepth(t, TrueColor)
	st := Style{
		Fg:             RGBColor(255, 0, 0),
		Bg:             PaletteColor(17),
		UnderlineColor: RGBColor(1, 2, 3),
		Underline:      UnderlineCurly,
		Attrs:          AttrBold | AttrItalic | AttrReverse,
	}
	want := "\x1b[0;1;3;4:3;7;38;2;255;0;0;48;5;17;58;2;1;2;3m"
	if got := st.SGR(); got != want {
		t.Fatalf("SGR = %q, want %q", got, want)
	}
}

// TestStyleSGRAttributes pins one escape per attribute bit, so a bit moved to
// another SGR number fails here.
func TestStyleSGRAttributes(t *testing.T) {
	setDepth(t, TrueColor)
	cases := []struct {
		attrs uint8
		want  string
	}{
		{AttrBold, "\x1b[0;1;39;49m"},
		{AttrFaint, "\x1b[0;2;39;49m"},
		{AttrItalic, "\x1b[0;3;39;49m"},
		{AttrBlink, "\x1b[0;5;39;49m"},
		{AttrRapidBlink, "\x1b[0;6;39;49m"},
		{AttrReverse, "\x1b[0;7;39;49m"},
		{AttrConceal, "\x1b[0;8;39;49m"},
		{AttrStrikethrough, "\x1b[0;9;39;49m"},
	}
	for _, c := range cases {
		if got := (Style{Attrs: c.attrs}).SGR(); got != c.want {
			t.Fatalf("attrs %08b = %q, want %q", c.attrs, got, c.want)
		}
	}
}

// TestStyleSGRUnderline pins the five underline shapes and the plain reset.
func TestStyleSGRUnderline(t *testing.T) {
	setDepth(t, TrueColor)
	cases := []struct {
		u    uint8
		want string
	}{
		{UnderlineNone, "\x1b[0;39;49m"},
		{UnderlineSingle, "\x1b[0;4;39;49m"},
		{UnderlineDouble, "\x1b[0;4:2;39;49m"},
		{UnderlineCurly, "\x1b[0;4:3;39;49m"},
		{UnderlineDotted, "\x1b[0;4:4;39;49m"},
		{UnderlineDashed, "\x1b[0;4:5;39;49m"},
	}
	for _, c := range cases {
		if got := (Style{Underline: c.u}).SGR(); got != c.want {
			t.Fatalf("underline %d = %q, want %q", c.u, got, c.want)
		}
	}
}

// TestColourStepsDown proves a 24-bit colour follows the terminal's depth:
// 24-bit stays 24-bit, 256 steps to the nearest palette entry, and 16 steps to
// the nearest ANSI colour.
func TestColourStepsDown(t *testing.T) {
	red := RGBColor(255, 0, 0)

	setDepth(t, TrueColor)
	if got := (Style{Fg: red}).SGR(); got != "\x1b[0;38;2;255;0;0;49m" {
		t.Fatalf("truecolor fg = %q", got)
	}
	if got := (Style{Bg: red}).SGR(); got != "\x1b[0;39;48;2;255;0;0m" {
		t.Fatalf("truecolor bg = %q", got)
	}

	setDepth(t, Color256)
	if got := (Style{Fg: red}).SGR(); got != "\x1b[0;38;5;9;49m" {
		t.Fatalf("256 fg = %q, want the bright red slot", got)
	}
	if got := (Style{Bg: red}).SGR(); got != "\x1b[0;39;48;5;9m" {
		t.Fatalf("256 bg = %q", got)
	}

	setDepth(t, Color16)
	if got := (Style{Fg: red}).SGR(); got != "\x1b[0;91;49m" {
		t.Fatalf("16 fg = %q, want bright red", got)
	}
	if got := (Style{Bg: red}).SGR(); got != "\x1b[0;39;101m" {
		t.Fatalf("16 bg = %q", got)
	}
}

// TestPaletteStepsDown proves a palette index above 15 steps down to the
// nearest ANSI colour at 16, and keeps its index at 256.
func TestPaletteStepsDown(t *testing.T) {
	c := PaletteColor(200)

	setDepth(t, Color256)
	if got := (Style{Fg: c}).SGR(); got != "\x1b[0;38;5;200;49m" {
		t.Fatalf("256 palette = %q, want the index kept", got)
	}

	setDepth(t, Color16)
	want := ansiParam(Nearest16(PaletteRGB(200)), false)
	if got := (Style{Fg: c}).SGR(); got != "\x1b[0;"+want+";49m" {
		t.Fatalf("16 palette = %q, want %q", got, want)
	}
	if got := (Style{Fg: PaletteColor(1)}).SGR(); got != "\x1b[0;31;49m" {
		t.Fatalf("16 palette 1 = %q, want 31", got)
	}
}

// TestDefaultColourUnchanged proves the default colour keeps its reset form at
// every depth.
func TestDefaultColourUnchanged(t *testing.T) {
	for _, d := range []Depth{TrueColor, Color256, Color16} {
		setDepth(t, d)
		if got := (Style{}).SGR(); got != "\x1b[0;39;49m" {
			t.Fatalf("depth %v = %q, want the default pair", d, got)
		}
	}
}

// TestUnderlineColourStepsDown proves the underline colour keeps 24-bit at
// truecolor and steps to a palette index below it.
func TestUnderlineColourStepsDown(t *testing.T) {
	c := RGBColor(255, 0, 0)
	st := Style{UnderlineColor: c}

	setDepth(t, TrueColor)
	if got := st.SGR(); got != "\x1b[0;39;49;58;2;255;0;0m" {
		t.Fatalf("truecolor underline = %q", got)
	}
	setDepth(t, Color256)
	if got := st.SGR(); got != "\x1b[0;39;49;58;5;9m" {
		t.Fatalf("256 underline = %q", got)
	}
	setDepth(t, Color16)
	if got := st.SGR(); got != "\x1b[0;39;49;58;5;9m" {
		t.Fatalf("16 underline = %q", got)
	}
}

// TestSGRCacheMatchesBuilder proves a cached escape is the escape the builder
// writes, at every depth and on a second ask.
func TestSGRCacheMatchesBuilder(t *testing.T) {
	for _, d := range []Depth{TrueColor, Color256, Color16} {
		setDepth(t, d)
		for _, st := range styleFixtures() {
			want := buildSGR(st)
			if got := st.SGR(); got != want {
				t.Fatalf("depth %v style %+v: cached %q, built %q", d, st, got, want)
			}
			if got := st.SGR(); got != want {
				t.Fatalf("depth %v style %+v: warm %q, built %q", d, st, got, want)
			}
		}
	}
}

// TestSGRCacheKeepsDepthSeparate proves a cached truecolour escape is not
// served at a shorter depth without a reset in between.
func TestSGRCacheKeepsDepthSeparate(t *testing.T) {
	setDepth(t, TrueColor)
	st := Style{Fg: RGBColor(255, 0, 0)}
	if got := st.SGR(); got != "\x1b[0;38;2;255;0;0;49m" {
		t.Fatalf("truecolor = %q", got)
	}
	SetDepth(Color16)
	if got := st.SGR(); got != "\x1b[0;91;49m" {
		t.Fatalf("16 after truecolor = %q, want the stepped escape", got)
	}
}

// TestSGRCacheEvicts proves a table smaller than the distinct styles a caller
// asks about still answers each one correctly.
func TestSGRCacheEvicts(t *testing.T) {
	setDepth(t, TrueColor)
	// 600 distinct styles are more than the 512 slots, so the last 88 keys
	// reuse a slot that already holds another key.
	for i := 0; i < 600; i++ {
		st := Style{
			Fg: RGBColor(uint8(i%256), uint8(i/256), 0),
			Bg: RGBColor(uint8(i/4), uint8(i/3), uint8(i/5)),
		}
		if got, want := st.SGR(), buildSGR(st); got != want {
			t.Fatalf("style %d: cached %q, built %q", i, got, want)
		}
	}
	if s := SGRCache(); s.Hits != 0 || s.Misses != 600 {
		t.Fatalf("tally = %v, want 600 misses and no hits", s)
	}
}

// TestSGRCacheCounts proves the tally and the rate describe the asks.
func TestSGRCacheCounts(t *testing.T) {
	setDepth(t, TrueColor)
	st := Style{}
	st.SGR() // miss
	st.SGR() // hit
	st.SGR() // hit
	s := SGRCache()
	if s.Hits != 2 || s.Misses != 1 || s.Lookups() != 3 {
		t.Fatalf("tally = %v, want 2 hits of 3", s)
	}
	if want := 2.0 / 3; s.Rate() < want-1e-9 || s.Rate() > want+1e-9 {
		t.Fatalf("rate = %v, want %v", s.Rate(), want)
	}
	if got := s.String(); got != "2 hits, 1 misses, 66.7% hit rate" {
		t.Fatalf("String = %q", got)
	}
}

// TestSGRCacheRateIsZeroWhenUnasked proves an empty tally reports no rate
// rather than a division by zero.
func TestSGRCacheRateIsZeroWhenUnasked(t *testing.T) {
	var s SGRCacheStats
	if s.Rate() != 0 || s.Lookups() != 0 {
		t.Fatalf("empty tally = %v", s)
	}
}

// styleFixtures is one distinct style per attribute and colour form, so the
// cache is asked about every field it keys on.
func styleFixtures() []Style {
	return []Style{
		{},
		{Fg: PaletteColor(1)},
		{Fg: RGBColor(255, 0, 0), Bg: RGBColor(0, 0, 0)},
		{Bg: PaletteColor(236), UnderlineColor: PaletteColor(4), Attrs: AttrBold},
		{UnderlineColor: RGBColor(10, 20, 30), Underline: UnderlineCurly, Attrs: AttrFaint | AttrItalic},
		{Fg: PaletteColor(200), Bg: PaletteColor(17), Underline: UnderlineDouble, Attrs: AttrReverse | AttrStrikethrough},
		{Fg: RGBColor(1, 2, 3), Bg: RGBColor(4, 5, 6), Underline: UnderlineDotted, Attrs: AttrBlink | AttrRapidBlink | AttrConceal},
	}
}

// ansiParam is the SGR parameter for an ANSI index, written out here so the
// test does not trust the code under test to describe itself.
func ansiParam(n int, bg bool) string {
	base := 30
	if bg {
		base = 40
	}
	if n < 8 {
		return strconv.Itoa(base + n)
	}
	return strconv.Itoa(base + 60 + n - 8)
}
