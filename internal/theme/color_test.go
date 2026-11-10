package theme

import (
	"math"
	"path/filepath"
	"strings"
	"testing"
)

// TestDetectDepth pins the environment rules: DM_COLOR overrides, COLORTERM
// beats TERM, and an unknown terminal gets 16 colours rather than 24-bit.
func TestDetectDepth(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want Depth
	}{
		{"truecolor", map[string]string{"COLORTERM": "truecolor", "TERM": "xterm-256color"}, TrueColor},
		{"24bit", map[string]string{"COLORTERM": "24bit"}, TrueColor},
		{"256", map[string]string{"TERM": "xterm-256color"}, Color256},
		{"screen", map[string]string{"TERM": "screen-256color"}, Color256},
		{"plain xterm", map[string]string{"TERM": "xterm"}, Color16},
		{"dumb", map[string]string{"TERM": "dumb"}, Color16},
		{"empty", map[string]string{}, Color16},
		{"override 256", map[string]string{"DM_COLOR": "256", "COLORTERM": "truecolor"}, Color256},
		{"override 16", map[string]string{"DM_COLOR": "16", "TERM": "xterm-256color"}, Color16},
		{"override truecolor", map[string]string{"DM_COLOR": "truecolor", "TERM": "xterm"}, TrueColor},
		{"override junk", map[string]string{"DM_COLOR": "nope", "TERM": "xterm-256color"}, Color256},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DetectDepth(func(k string) string { return c.env[k] })
			if got != c.want {
				t.Fatalf("DetectDepth(%v) = %v, want %v", c.env, got, c.want)
			}
		})
	}
}

// TestDepthEscapes pins the three forms of one colour.
func TestDepthEscapes(t *testing.T) {
	c := RGB{255, 0, 0}
	defer SetDepth(TrueColor)

	SetDepth(TrueColor)
	if got := c.FG(); got != "\x1b[38;2;255;0;0m" {
		t.Fatalf("truecolor FG = %q", got)
	}
	if got := c.BG(); got != "\x1b[48;2;255;0;0m" {
		t.Fatalf("truecolor BG = %q", got)
	}

	SetDepth(Color256)
	if got := c.FG(); got != "\x1b[38;5;9m" {
		t.Fatalf("256 FG = %q, want the bright red slot", got)
	}
	if got := c.BG(); got != "\x1b[48;5;9m" {
		t.Fatalf("256 BG = %q", got)
	}

	SetDepth(Color16)
	if got := c.FG(); got != "\x1b[91m" {
		t.Fatalf("16 FG = %q, want bright red", got)
	}
	if got := c.BG(); got != "\x1b[101m" {
		t.Fatalf("16 BG = %q", got)
	}
}

// TestAnsiAnchors pins the corners of the 16-colour palette, so a broken
// distance function cannot pass by mapping everything to one slot.
func TestAnsiAnchors(t *testing.T) {
	cases := []struct {
		c    RGB
		want int
	}{
		{RGB{0, 0, 0}, 0},
		{RGB{255, 255, 255}, 15},
		{RGB{255, 0, 0}, 9},
		{RGB{0, 255, 0}, 10},
		{RGB{0, 0, 255}, 12},
		{RGB{128, 128, 128}, 8},
	}
	for _, c := range cases {
		if got := Nearest16(c.c); got != c.want {
			t.Fatalf("Nearest16(%v) = %d, want %d", c.c, got, c.want)
		}
	}
}

// TestNearestIsNearest checks the search itself over every bundled theme: the
// index returned is the closest entry in OKLab, and no other entry is closer.
// It also bounds how far a theme colour can move, so a wrong palette table or a
// broken conversion fails here rather than in a screenshot.
func TestNearestIsNearest(t *testing.T) {
	themes := bundledThemes(t)
	if len(themes) < 4 {
		t.Fatalf("found %d bundled themes, want the built-in plus three files", len(themes))
	}
	for name, th := range themes {
		for _, c := range themeColours(th) {
			for _, d := range []Depth{Color256, Color16} {
				pal := xterm256Lab[:]
				if d == Color16 {
					pal = xterm256Lab[:16]
				}
				got := nearest(c, pal)
				target := toOKLab(c)
				best := math.Inf(1)
				for _, p := range pal {
					if dist := target.dist2(p); dist < best {
						best = dist
					}
				}
				if dist := target.dist2(pal[got]); dist > best+1e-12 {
					t.Fatalf("%s %v at depth %v: index %d is %.6f away, best is %.6f", name, c, d, got, dist, best)
				}
				// A 256-colour step-down should be visually indistinguishable;
				// 16 colours are coarse, and a near-black theme colour lands on
				// black. Measured maxima: 0.077 at 256, 0.195 at 16.
				limit := 0.10
				if d == Color16 {
					limit = 0.25
				}
				if dist := math.Sqrt(best); dist > limit {
					t.Fatalf("%s %v at depth %v: nearest is %.4f away in OKLab, over the %.2f bound", name, c, d, dist, limit)
				}
			}
		}
	}
}

// bundledThemes is the built-in theme plus every theme file the oracle ships.
func bundledThemes(t *testing.T) map[string]Theme {
	t.Helper()
	out := map[string]Theme{"built-in": DefaultTheme()}
	paths, err := filepath.Glob(filepath.Join("..", "shot", "testdata", "themes", "*.kdl"))
	if err != nil {
		t.Fatalf("glob themes: %v", err)
	}
	for _, p := range paths {
		name := strings.TrimSuffix(filepath.Base(p), ".kdl")
		th, err := LoadThemeFile(filepath.Join("..", "shot", "testdata"), name)
		if err != nil {
			t.Fatalf("load theme %s: %v", name, err)
		}
		out[name] = th
	}
	return out
}

// themeColours is every colour a theme can paint with.
func themeColours(th Theme) []RGB {
	p := th.Palette
	out := []RGB{
		p.RailBG, p.RailFocusBG, p.RailHoverBG, p.RailText, p.RailDim, p.RailHeader,
		p.RailWindow, p.RailDiffAdd, p.RailDiffDel, p.RailWorking, p.BarAccent,
	}
	out = append(out, th.Accents[:]...)
	for _, st := range th.States {
		out = append(out, st.Colour)
	}
	return out
}
