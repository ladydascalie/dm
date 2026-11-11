package client

import (
	"bytes"
	"strings"
	"testing"

	"dm/internal/theme"
	"dm/internal/wire"
)

// elidedBarSnapshot is a status bar narrow enough that the session label has to
// shrink. The label is long, so every set elides it rather than dropping it.
func elidedBarSnapshot() *wire.Snapshot {
	return &wire.Snapshot{
		Session:      strings.Repeat("session", 8),
		Workspace:    "main",
		Windows:      []wire.WindowInfo{{ID: "w1", Name: "one", Named: true}},
		ActiveWindow: 0,
		Cols:         24, Rows: 5,
		Focus: "w1p1",
		Panes: []wire.PaneInfo{{ID: "w1p1", Name: "alpha", Status: "working"}},
	}
}

// barSessionLabel is the elided session run of the left block.
func barSessionLabel(th theme.Theme, snap *wire.Snapshot, set theme.GlyphSet) (string, bool) {
	for _, s := range barSegments(th, snap, false, "", set) {
		if s.FG == th.Palette.RailText && s.BG == th.Palette.RailBG && !s.Bold {
			return s.Text, true
		}
	}
	return "", false
}

// TestBarElisionFollowsTheGlyphSet proves the status bar's elision mark is the
// set's own: ascii paints only ASCII bytes and ends the label with `>`, unicode
// ends it with `…`. The row is resolved before drawing, as the live client
// resolves its theme once at load.
func TestBarElisionFollowsTheGlyphSet(t *testing.T) {
	t.Parallel()
	snap := elidedBarSnapshot()

	t.Run("ascii", func(t *testing.T) {
		th := theme.DefaultTheme().For(theme.GlyphASCII)
		var buf bytes.Buffer
		drawBar(&buf, th, snap, false, "", theme.GlyphASCII)
		out := buf.String()
		for i := 0; i < len(out); i++ {
			if out[i] > 0x7f {
				t.Fatalf("ascii bar carries byte %#x at %d: %q", out[i], i, out)
			}
		}
		label, ok := barSessionLabel(th, snap, theme.GlyphASCII)
		if !ok {
			t.Fatal("bar has no session label")
		}
		if !strings.HasSuffix(label, ">") {
			t.Fatalf("ascii session label %q does not end with the ascii mark", label)
		}
	})

	t.Run("unicode", func(t *testing.T) {
		th := theme.DefaultTheme().For(theme.GlyphUnicode)
		var buf bytes.Buffer
		drawBar(&buf, th, snap, false, "", theme.GlyphUnicode)
		label, ok := barSessionLabel(th, snap, theme.GlyphUnicode)
		if !ok {
			t.Fatal("bar has no session label")
		}
		if !strings.HasSuffix(label, "…") {
			t.Fatalf("unicode session label %q does not end with the ellipsis", label)
		}
	})
}
