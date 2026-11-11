package client

import (
	"strings"
	"testing"

	"dm/internal/theme"
)

// setDepth sets the render depth for one test and restores it after.
func setDepth(t *testing.T, d theme.Depth) {
	t.Helper()
	old := theme.CurrentDepth()
	theme.SetDepth(d)
	t.Cleanup(func() { theme.SetDepth(old) })
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

// TestFrameASCIIGlyphs proves the ascii set reaches a whole frame: no glyph
// from the Unicode set survives in the chrome.
func TestFrameASCIIGlyphs(t *testing.T) {
	t.Parallel()
	f := newFrameTUI(t, 100, 20, 34)
	f.tui.glyphs = theme.GlyphASCII
	frame := f.frame()
	for _, glyph := range []string{"▾", "▸", "•", "·", "●", "○", "▲", "✓", "✕", "▶", "▌", "┌", "─", "│", "└"} {
		if strings.Contains(frame, glyph) {
			t.Fatalf("ascii frame carries %q", glyph)
		}
	}
}
