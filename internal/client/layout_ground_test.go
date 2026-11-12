package client

import (
	"bytes"
	"strings"
	"testing"

	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/vt"

	"dm/internal/layout"
	"dm/internal/wire"
)

// layoutGroundTUI is a two-pane frame client over the palette's real draw
// path, so the test asserts the frame's bytes.
func layoutGroundTUI(t *testing.T) *frameTUI {
	t.Helper()
	root := &layout.Node{Dir: "row", Kids: []*layout.Node{{Pane: "w1p1"}, {Pane: "w1p2"}}}
	snap := &wire.Snapshot{Cols: 100, Rows: 30, Rail: 34, Root: root, Focus: "w1p1"}
	rects := map[string]layout.Rect{}
	layout.Layout(root, layout.PaneAreaForWithTabBar(100, 30, 34), testutil.Pad(), rects)
	for _, id := range []string{"w1p1", "w1p2"} {
		snap.Panes = append(snap.Panes, wire.PaneInfo{ID: id, Rect: rects[id]})
	}
	snap.Windows = []wire.WindowInfo{{ID: "w1", Name: "one"}}
	snap.All = []wire.AllPane{
		{ID: "w1p1", Workspace: "s1"},
		{ID: "w1p2", Workspace: "s1"},
	}
	out := &bytes.Buffer{}
	tui := &tui{snap: snap, vts: map[string]*vt.Term{}, out: out, glyphs: theme.GlyphNerd}
	for _, id := range []string{"w1p1", "w1p2"} {
		r := rects[id]
		term := vt.NewTerm(r.W, r.H)
		tui.vts[id] = term
		term.Write([]byte("\x1b[?25l\x1b[1;1HPANE-" + id))
	}
	return &frameTUI{tui: tui, snap: snap, out: out}
}

// TestLayoutPaletteCarriesAGround proves the palette paints opaque cells: the
// interior fill, the list rows and the preview boxes all carry a ground, so
// the panes under the palette cannot bleed through.
func TestLayoutPaletteCarriesAGround(t *testing.T) {
	t.Parallel()
	f := layoutGroundTUI(t)
	openLayout(t, f.tui)
	frame := f.frame()
	if frame == "" {
		t.Fatal("the palette wrote no frame")
	}
	th := theme.DefaultTheme()
	if n := strings.Count(frame, groundSGR(th.Palette.RailBG)); n < 6 {
		t.Fatalf("frame carries the rail ground %d times, want the fill and the rows", n)
	}
	// Two boxes: the unfocused one on the hover ground, the focused one on
	// the focus ground. A box without its ground is transparent.
	for name, rgb := range map[string]theme.RGB{
		"hover": th.Palette.RailHoverBG,
		"focus": th.Palette.RailFocusBG,
	} {
		if !strings.Contains(frame, groundSGR(rgb)) {
			t.Fatalf("frame carries no %s ground: the %s box is transparent", name, name)
		}
	}
	// A list row's padded cells carry the ground too: a row written without
	// one would erase the fill under it.
	if !strings.Contains(frame, "\x1b[0m"+groundSGR(th.Palette.RailBG)) {
		t.Fatal("list rows carry no ground")
	}
}
