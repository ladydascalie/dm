package theme

import (
	"testing"
	"unicode/utf8"

	"dm/internal/cells"
)

// TestGlyphSetsAreOneCell is the rule the glyph sets exist for: every glyph the
// chrome can paint is exactly one cell wide, in every set. A two-cell glyph
// would shift the row it lands on and break the rail's column arithmetic.
func TestGlyphSetsAreOneCell(t *testing.T) {
	th := DefaultTheme()
	for _, set := range []GlyphSet{GlyphAuto, GlyphASCII, GlyphUnicode, GlyphNerd} {
		g := th.Glyphs.For(set)
		wantMark := cells.Ellipsis
		if set == GlyphASCII {
			wantMark = ">"
		}
		if g.Ellipsis != wantMark {
			t.Fatalf("set %v elision mark = %q, want %q", set, g.Ellipsis, wantMark)
		}
		structural := map[string]string{
			"elide": g.Ellipsis,
			"grip":  g.Grip, "divider-row": g.DividerRow, "divider-col": g.DividerCol,
			"top-left": g.BorderTopLeft, "top-right": g.BorderTopRight,
			"bottom-left": g.BorderBottomLeft, "bottom-right": g.BorderBottomRight,
			"horizontal": g.BorderHorizontal, "vertical": g.BorderVertical,
			"tee-left": g.BorderTeeLeft, "tee-right": g.BorderTeeRight,
			"fold-open": g.FoldOpen, "fold-closed": g.FoldClosed,
			"active-mark": g.ActiveMark, "workspace-bar": g.WorkspaceBar, "dirty": g.Dirty,
		}
		for name, glyph := range structural {
			if glyph == "" {
				continue
			}
			if n := cells.Cells(glyph); n != 1 {
				t.Fatalf("set %v %s = %q is %d cells, want 1", set, name, glyph, n)
			}
		}
		for status, style := range th.States {
			s := style.For(set)
			if s.Glyph == "" {
				continue
			}
			if n := cells.Cells(s.Glyph); n != 1 {
				t.Fatalf("set %v state %s = %q is %d cells, want 1", set, status, s.Glyph, n)
			}
		}
		marks := th.RailMarks.For(set)
		for name, glyph := range map[string]string{"workspace": marks.Workspace, "pane": marks.Pane} {
			if glyph == "" {
				continue
			}
			if n := cells.Cells(glyph); n != 1 {
				t.Fatalf("set %v rail mark %s = %q is %d cells, want 1", set, name, glyph, n)
			}
		}
	}
}

// TestASCIIGlyphSetIsASCII proves the ascii set is plain ASCII, so a terminal
// with no Unicode at all still draws the chrome.
func TestASCIIGlyphSetIsASCII(t *testing.T) {
	th := DefaultTheme()
	g := th.Glyphs.For(GlyphASCII)
	all := []string{
		g.Grip, g.DividerRow, g.DividerCol,
		g.BorderTopLeft, g.BorderTopRight, g.BorderBottomLeft, g.BorderBottomRight,
		g.BorderHorizontal, g.BorderVertical, g.BorderTeeLeft, g.BorderTeeRight,
		g.FoldOpen, g.FoldClosed, g.ActiveMark, g.WorkspaceBar, g.Dirty,
		g.Ellipsis,
	}
	for _, style := range th.States {
		all = append(all, style.For(GlyphASCII).Glyph)
	}
	marks := th.RailMarks.For(GlyphASCII)
	all = append(all, marks.Workspace, marks.Pane)
	for _, glyph := range all {
		for _, r := range glyph {
			if r > 0x7f {
				t.Fatalf("ascii set carries %q (U+%04X)", glyph, r)
			}
		}
	}
}

// TestGlyphSetString pins the config spelling, which the parser and the docs
// both use.
func TestGlyphSetString(t *testing.T) {
	cases := map[GlyphSet]string{
		GlyphAuto:    "auto",
		GlyphASCII:   "ascii",
		GlyphUnicode: "unicode",
		GlyphNerd:    "nerd",
	}
	for set, want := range cases {
		if got := set.String(); got != want {
			t.Fatalf("GlyphSet(%d).String() = %q, want %q", set, got, want)
		}
	}
}

// TestStateStyleFor proves the resolution: ascii substitutes, nerd prefers the
// Nerd glyph, and unicode keeps the theme's glyph.
func TestStateStyleFor(t *testing.T) {
	style := StateStyle{Glyph: "●", NerdGlyph: "\uf013"}
	if got := style.For(GlyphUnicode).Glyph; got != "●" {
		t.Fatalf("unicode = %q", got)
	}
	if got := style.For(GlyphNerd).Glyph; got != "\uf013" {
		t.Fatalf("nerd = %q", got)
	}
	if got := style.For(GlyphASCII).Glyph; got != "*" {
		t.Fatalf("ascii = %q", got)
	}
	// A state with no Nerd form keeps its Unicode glyph under nerd.
	shell := StateStyle{Glyph: "▶"}
	if got := shell.For(GlyphNerd).Glyph; got != "▶" {
		t.Fatalf("nerd shell = %q", got)
	}
	if got := shell.For(GlyphASCII).Glyph; got != ">" {
		t.Fatalf("ascii shell = %q", got)
	}
}

// TestASCIIFallbackKeepsUnknownGlyphs proves a theme's own glyph is used as it
// is: the table covers what dm ships, not what a user invents.
func TestASCIIFallbackKeepsUnknownGlyphs(t *testing.T) {
	if got := ASCII("Z"); got != "Z" {
		t.Fatalf("ASCII(Z) = %q", got)
	}
	if got := ASCII("★"); got != "★" {
		t.Fatalf("ASCII(★) = %q, want it kept", got)
	}
	if r, _ := utf8.DecodeRuneInString(ASCII("★")); r != '★' {
		t.Fatalf("ASCII(★) decoded to %q", r)
	}
}
