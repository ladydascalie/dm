package client

import (
	"strconv"
	"strings"
	"testing"

	"dm/internal/theme"
	"dm/internal/vt"
)

// setDepth sets the render depth for one test and restores it after.
func setDepth(t *testing.T, d theme.Depth) {
	t.Helper()
	old := theme.CurrentDepth()
	theme.SetDepth(d)
	t.Cleanup(func() { theme.SetDepth(old) })
}

// TestCellColourStepsDown proves a pane cell's colour follows the terminal's
// depth: 24-bit stays 24-bit, 256 steps to the nearest palette entry, and 16
// steps to the nearest ANSI colour.
func TestCellColourStepsDown(t *testing.T) {
	red := vt.RGBColor(255, 0, 0)

	setDepth(t, theme.TrueColor)
	if got := cellColorSGR(red, false); got != "38;2;255;0;0" {
		t.Fatalf("truecolor fg = %q", got)
	}
	if got := cellColorSGR(red, true); got != "48;2;255;0;0" {
		t.Fatalf("truecolor bg = %q", got)
	}

	setDepth(t, theme.Color256)
	if got := cellColorSGR(red, false); got != "38;5;9" {
		t.Fatalf("256 fg = %q, want the bright red slot", got)
	}
	if got := cellColorSGR(red, true); got != "48;5;9" {
		t.Fatalf("256 bg = %q", got)
	}

	setDepth(t, theme.Color16)
	if got := cellColorSGR(red, false); got != "91" {
		t.Fatalf("16 fg = %q, want bright red", got)
	}
	if got := cellColorSGR(red, true); got != "101" {
		t.Fatalf("16 bg = %q", got)
	}
}

// TestPaletteCellStepsDown proves a palette index above 15 steps down to the
// nearest ANSI colour at 16, and keeps its index at 256.
func TestPaletteCellStepsDown(t *testing.T) {
	c := vt.PaletteColor(200)

	setDepth(t, theme.Color256)
	if got := cellColorSGR(c, false); got != "38;5;200" {
		t.Fatalf("256 palette = %q, want the index kept", got)
	}

	setDepth(t, theme.Color16)
	want := theme.Nearest16(theme.PaletteRGB(200))
	got := cellColorSGR(c, false)
	if got != ansiParam(want, false) {
		t.Fatalf("16 palette = %q, want %q for ANSI %d", got, ansiParam(want, false), want)
	}
	if got := cellColorSGR(vt.PaletteColor(1), false); got != "31" {
		t.Fatalf("16 palette 1 = %q, want 31", got)
	}
}

// TestDefaultColourUnchanged proves the default colour keeps its reset form at
// every depth.
func TestDefaultColourUnchanged(t *testing.T) {
	for _, d := range []theme.Depth{theme.TrueColor, theme.Color256, theme.Color16} {
		setDepth(t, d)
		if got := cellColorSGR(vt.DefaultColor(), false); got != "39" {
			t.Fatalf("depth %v fg = %q, want 39", d, got)
		}
		if got := cellColorSGR(vt.DefaultColor(), true); got != "49" {
			t.Fatalf("depth %v bg = %q, want 49", d, got)
		}
	}
}

// TestUnderlineColourStepsDown proves the underline colour keeps 24-bit at
// truecolor and steps to a palette index below it.
func TestUnderlineColourStepsDown(t *testing.T) {
	c := vt.RGBColor(255, 0, 0)

	setDepth(t, theme.TrueColor)
	if got := underlineColorSGR(c); got != "58;2;255;0;0" {
		t.Fatalf("truecolor underline = %q", got)
	}
	setDepth(t, theme.Color256)
	if got := underlineColorSGR(c); got != "58;5;9" {
		t.Fatalf("256 underline = %q", got)
	}
	setDepth(t, theme.Color16)
	if got := underlineColorSGR(c); got != "58;5;9" {
		t.Fatalf("16 underline = %q", got)
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

// TestFrameEmitsNoDeepColour proves the whole frame follows the depth: at 256
// there is no 24-bit escape, and at 16 there is no 256-colour one either.
func TestFrameEmitsNoDeepColour(t *testing.T) {
	cases := []struct {
		depth theme.Depth
		bad   []string
	}{
		{theme.Color256, []string{"38;2;", "48;2;"}},
		{theme.Color16, []string{"38;2;", "48;2;", "38;5;", "48;5;"}},
	}
	// The control: at truecolor the frame does carry 24-bit escapes, so the
	// checks below are not passing on an empty frame.
	setDepth(t, theme.TrueColor)
	if frame := newFrameTUI(t, 100, 20, 34).frame(); !strings.Contains(frame, "38;2;") && !strings.Contains(frame, "48;2;") {
		t.Fatal("truecolor frame carries no 24-bit escape: the depth check would be vacuous")
	}
	for _, c := range cases {
		setDepth(t, c.depth)
		f := newFrameTUI(t, 100, 20, 34)
		frame := f.frame()
		if frame == "" {
			t.Fatalf("depth %v wrote no frame", c.depth)
		}
		for _, bad := range c.bad {
			if strings.Contains(frame, bad) {
				t.Fatalf("depth %v frame carries %q", c.depth, bad)
			}
		}
	}
}
