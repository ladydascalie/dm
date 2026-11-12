package client

import (
	"strings"
	"testing"

	"dm/internal/segment"
	"dm/internal/testutil"
	"dm/internal/theme"
)

// TestBarSyncMarkPainting pins the sync mark: it sits right after the pane id
// in the focus chip, painted in barAccent, and one glyph per vocabulary.
func TestBarSyncMarkPainting(t *testing.T) {
	t.Parallel()
	th := theme.DefaultTheme()
	snap := testutil.BarTestSnap(120)
	snap.Sync = true
	mark := " " + th.Glyphs.SyncMark

	segs := barSegments(th, snap, false, "", theme.GlyphUnicode)
	idAt, markAt := -1, -1
	for i, s := range segs {
		switch s.Text {
		case " w1p2":
			idAt = i
		case mark:
			markAt = i
		}
	}
	if markAt < 0 {
		t.Fatalf("no sync mark in %q", testutil.BarTexts(segs))
	}
	if markAt != idAt+1 {
		t.Fatalf("sync mark at %d, want right after the id at %d: %q",
			markAt, idAt, testutil.BarTexts(segs))
	}
	if segs[markAt].FG != th.Palette.BarAccent {
		t.Fatalf("sync mark colour %v, want barAccent %v", segs[markAt].FG, th.Palette.BarAccent)
	}
	if segs[markAt].BG != th.Palette.RailFocusBG {
		t.Fatalf("sync mark background %v, want the chip fill %v", segs[markAt].BG, th.Palette.RailFocusBG)
	}

	// Off means no mark at all, so the bar is unchanged in shape.
	snap.Sync = false
	off := barSegments(th, snap, false, "", theme.GlyphUnicode)
	for _, s := range off {
		if s.Text == mark {
			t.Fatalf("an unsynced window still paints the mark: %q", testutil.BarTexts(off))
		}
	}
}

// TestBarSyncMarkVocabularies proves the mark resolves to a distinct glyph in
// every vocabulary, and each is exactly one cell.
func TestBarSyncMarkVocabularies(t *testing.T) {
	t.Parallel()
	for _, set := range []theme.GlyphSet{theme.GlyphASCII, theme.GlyphUnicode, theme.GlyphNerd} {
		th := theme.DefaultTheme().For(set)
		snap := testutil.BarTestSnap(120)
		snap.Sync = true
		segs := barSegments(th, snap, false, "", set)
		mark := " " + th.Glyphs.SyncMark
		found := false
		for _, s := range segs {
			if s.Text == mark {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("set %v: mark %q missing from %q", set, mark, testutil.BarTexts(segs))
		}
		if n := segment.SegCells([]segment.Seg{{Text: th.Glyphs.SyncMark}}); n != 1 {
			t.Fatalf("set %v: sync mark %q is %d cells, want 1", set, th.Glyphs.SyncMark, n)
		}
		if th.Glyphs.SyncMark == "" {
			t.Fatalf("set %v: sync mark is empty", set)
		}
	}
	ascii := theme.DefaultTheme().For(theme.GlyphASCII).Glyphs.SyncMark
	uni := theme.DefaultTheme().For(theme.GlyphUnicode).Glyphs.SyncMark
	nerd := theme.DefaultTheme().For(theme.GlyphNerd).Glyphs.SyncMark
	if ascii == uni || uni == nerd || ascii == nerd {
		t.Fatalf("vocabularies share a glyph: ascii %q unicode %q nerd %q", ascii, uni, nerd)
	}
}

// TestBarSyncMarkElidesFirst proves the ladder drops the sync mark before the
// focus chip's status word: at widths where the status still shows, the mark
// is already gone.
func TestBarSyncMarkElidesFirst(t *testing.T) {
	t.Parallel()
	th := theme.DefaultTheme()
	mark := " " + th.Glyphs.SyncMark
	snap := testutil.BarTestSnap(120)
	snap.Sync = true

	everPainted := false
	statusWithoutMark := false
	for cols := 8; cols <= 120; cols++ {
		snap.Cols = cols
		segs := barSegments(th, snap, false, "", theme.GlyphUnicode)
		var text strings.Builder
		hasMark := false
		for _, s := range segs {
			if s.Pin {
				continue
			}
			text.WriteString(s.Text)
			if s.Text == mark {
				hasMark = true
			}
		}
		if hasMark {
			everPainted = true
		}
		if strings.Contains(text.String(), " idle") && !hasMark {
			statusWithoutMark = true
		}
		if segment.SegCells(segs) > cols {
			t.Fatalf("cols %d: spans paint %d cells, want <= %d", cols, segment.SegCells(segs), cols)
		}
	}
	if !everPainted {
		t.Fatal("the sync mark never painted at any width")
	}
	if !statusWithoutMark {
		t.Fatal("the sync mark never dropped while the status word still showed")
	}
}
