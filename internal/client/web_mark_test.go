package client

import (
	"testing"

	"dm/internal/segment"
	"dm/internal/theme"
	"dm/internal/wire"
)

// TestBarWebMarkPinned proves the daemon's live web listener paints one mark at
// the head of the pinned block, in the bar's accent, so the mark survives no
// search and no reply: the snapshot carries it.
func TestBarWebMarkPinned(t *testing.T) {
	t.Parallel()
	th := theme.DefaultTheme()
	snap := &wire.Snapshot{Cols: 60, Rows: 24, Web: true}

	segs := barSegments(th, snap, false, "", theme.GlyphUnicode)
	var first *segment.Seg
	for i := range segs {
		if segs[i].Pin {
			first = &segs[i]
			break
		}
	}
	if first == nil {
		t.Fatal("the bar pins nothing, want the web mark")
	}
	if want := th.Glyphs.WebMark + " "; first.Text != want {
		t.Fatalf("first pinned span = %q, want the web mark %q", first.Text, want)
	}
	if first.FG != th.Palette.BarAccent {
		t.Fatalf("web mark colour = %v, want the bar accent %v", first.FG, th.Palette.BarAccent)
	}
	if n := segment.SegCells(segs); n > snap.Cols {
		t.Fatalf("bar paints %d cells, want at most %d", n, snap.Cols)
	}
}

// TestBarWebMarkAbsentOff proves a daemon with no listener paints the bar it
// painted before the mark existed: no span of the bar changes.
func TestBarWebMarkAbsentOff(t *testing.T) {
	t.Parallel()
	th := theme.DefaultTheme()
	snap := &wire.Snapshot{Cols: 60, Rows: 24}
	info := "SCROLL w1p1 +1/9"
	badge := barBadge{n: 2, glyph: "●", colour: th.Palette.RailDiffAdd}

	plain := barSegments(th, snap, true, info, theme.GlyphUnicode, badge)
	for _, s := range plain {
		if s.Text == th.Glyphs.WebMark+" " {
			t.Fatal("a bar with no web listener paints the web mark")
		}
	}

	plainCells := len(plain)
	snap.Web = true
	withMark := barSegments(th, snap, true, info, theme.GlyphUnicode, badge)
	if got := len(withMark); got != plainCells+1 {
		t.Fatalf("span count = %d, want %d: the mark is one span", got, plainCells+1)
	}
}

// TestBarWebMarkFitsNarrow proves a bar too narrow for the whole pinned block
// drops the mark before an unread badge: ambient health is never more urgent
// than an entry the operator has not read.
func TestBarWebMarkFitsNarrow(t *testing.T) {
	t.Parallel()
	th := theme.DefaultTheme()
	snap := &wire.Snapshot{Cols: 5, Rows: 24, Web: true}
	badge := barBadge{n: 3, glyph: "●", colour: th.Palette.RailDiffAdd}

	segs := barSegments(th, snap, false, "", theme.GlyphUnicode, badge)
	for _, s := range segs {
		if s.Text == th.Glyphs.WebMark+" " {
			t.Fatal("a bar too narrow for both kept the web mark over the badge")
		}
	}
	if n := segment.SegCells(segs); n > snap.Cols {
		t.Fatalf("bar paints %d cells, want at most %d", n, snap.Cols)
	}
}

// TestBarWebMarkPerGlyphSet proves the mark resolves in every vocabulary: one
// cell everywhere, and plain ASCII where the set has no box drawing.
func TestBarWebMarkPerGlyphSet(t *testing.T) {
	t.Parallel()
	th := theme.DefaultTheme()
	for _, set := range []theme.GlyphSet{theme.GlyphASCII, theme.GlyphUnicode, theme.GlyphNerd} {
		mark := th.For(set).Glyphs.WebMark
		if n := segment.SegCells([]segment.Seg{{Text: mark}}); n != 1 {
			t.Fatalf("set %v web mark = %q is %d cells, want 1", set, mark, n)
		}
		if set == theme.GlyphASCII {
			for _, r := range mark {
				if r > 0x7f {
					t.Fatalf("ascii web mark carries %q", mark)
				}
			}
		}
	}
}
