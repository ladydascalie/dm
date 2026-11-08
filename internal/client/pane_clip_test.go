package client

import (
	"bytes"
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

// clipFrame builds a two-pane frame with a rail. Pane a's content rect is 10
// cells wide, so a row wider than that is the client's to clip, not the outer
// terminal's. Pane b and the rail are the cells the overflow would reach.
func clipFrame(t *testing.T, aCols int, aRow, bRow string) *frameTUI {
	t.Helper()
	root := &layout.Node{Dir: "row", Kids: []*layout.Node{{Pane: "a"}, {Pane: "b"}}}
	snap := &wire.Snapshot{Cols: 36, Rows: 7, Rail: 8, Root: root, Focus: "b"}
	rects := map[string]layout.Rect{}
	layout.Layout(root, layout.PaneAreaForWithTabBar(snap.Cols, snap.Rows, snap.Rail), testutil.Pad(), rects)
	snap.Panes = []wire.PaneInfo{{ID: "a", Rect: rects["a"]}, {ID: "b", Rect: rects["b"]}}
	snap.All = []wire.AllPane{
		{ID: "a", Name: "one", Workspace: "s1", WorkspaceName: "main", Window: "w1",
			WindowName: "one", WorkspaceActive: true, WindowActive: true,
			WindowUnfolded: true, WorkspaceUnfolded: true},
		{ID: "b", Name: "two", Workspace: "s1", WorkspaceName: "main", Window: "w1",
			WindowName: "one", Focus: true, WorkspaceActive: true, WindowActive: true,
			WindowUnfolded: true, WorkspaceUnfolded: true},
	}
	out := &bytes.Buffer{}
	tr := &tui{snap: snap, vts: map[string]*vt.Term{}, out: out, glyphs: theme.GlyphNerd}
	aTerm := vt.NewTerm(aCols, rects["a"].H)
	aTerm.Write([]byte(aRow))
	tr.vts["a"] = aTerm
	bTerm := vt.NewTerm(rects["b"].W, rects["b"].H)
	bTerm.Write([]byte("\x1b[?25l" + bRow))
	tr.vts["b"] = bTerm
	return &frameTUI{tui: tr, snap: snap, out: out}
}

// paintFrame draws one frame into a live terminal, so the test reads the cells
// the terminal ends up showing rather than the raw bytes.
func paintFrame(t *testing.T, f *frameTUI) *vt.Term {
	t.Helper()
	live := vt.NewTerm(f.snap.Cols, f.snap.Rows)
	live.Write([]byte(f.frame()))
	live.Flush()
	return live
}

// paintRow draws one pane row into a terminal wide enough to show the cells
// past the content rect, so an overflow shows up instead of being masked by the
// padding the frame paints after the row.
func paintRow(t *testing.T, term *vt.Term, r layout.Rect) *vt.Term {
	t.Helper()
	var buf bytes.Buffer
	drawPane(&buf, term, r)
	live := vt.NewTerm(r.W+4, 1)
	live.Write(buf.Bytes())
	live.Flush()
	return live
}

// sameCells fails on the first cell where two painted terminals differ.
func sameCells(t *testing.T, got, want *vt.Term, label string) {
	t.Helper()
	gg, wg := got.Grid(), want.Grid()
	cols, rows := gg.Size()
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			a, b := gg.Cell(x, y), wg.Cell(x, y)
			if a.Content != b.Content || a.Width != b.Width || a.Style != b.Style {
				t.Fatalf("%s: cell %d,%d clipped=%q/%d/{%+v} fitted=%q/%d/{%+v}",
					label, x, y, a.Content, a.Width, a.Style, b.Content, b.Width, b.Style)
			}
		}
	}
}

// TestPaneRowClipsToContentRect pins the clip: a pane in a 10-cell content rect
// prints a 30-cell row, and only the first 10 cells reach the screen. The
// padding, the box borders, the next pane and the rail keep their own cells.
func TestPaneRowClipsToContentRect(t *testing.T) {
	f := clipFrame(t, 30, strings.Repeat("A", 30), "BBBB")
	a := f.snap.Panes[0].Rect
	if a.W != 10 {
		t.Fatalf("fixture pane is %d cells wide, want 10", a.W)
	}
	got := paintFrame(t, f)
	g := got.Grid()
	for x := a.X; x < a.X+a.W; x++ {
		if c := g.Cell(x, a.Y); c.Content != "A" {
			t.Fatalf("content cell %d is %q, want the first 10 cells of the row", x, c.Content)
		}
	}
	// Nothing past the rect carries the row: the pad, the border, the divider,
	// the next box and the rail are untouched.
	for x := a.X + a.W; x < f.snap.Cols; x++ {
		if c := g.Cell(x, a.Y); strings.Contains(c.Content, "A") {
			t.Fatalf("column %d past the content rect holds the row: %q", x, c.Content)
		}
	}
	b := f.snap.Panes[1].Rect
	for i, r := range "BBBB" {
		if c := g.Cell(b.X+i, b.Y); c.Content != string(r) {
			t.Fatalf("next pane cell %d is %q, want %q", i, c.Content, string(r))
		}
	}

	// The clipped frame is the frame a 10-cell row paints, cell for cell.
	fits := clipFrame(t, 10, strings.Repeat("A", 10), "BBBB")
	sameCells(t, got, paintFrame(t, fits), "clipped row")
}

// TestPaneRowDropsWideGlyphAtEdge pins the cell boundary: a wide glyph whose
// second cell would fall outside the rect is not painted, and the cell it
// would have started in is blanked.
func TestPaneRowDropsWideGlyphAtEdge(t *testing.T) {
	term := vt.NewTerm(30, 1)
	term.Write([]byte("abcdefghi世"))
	term.Flush()
	r := layout.Rect{X: 0, Y: 0, W: 10, H: 1}
	live := paintRow(t, term, r)
	for i, want := range "abcdefghi" {
		if c := live.Grid().Cell(i, 0); c.Content != string(want) {
			t.Fatalf("cell %d is %q, want %q", i, c.Content, string(want))
		}
	}
	// The glyph's first cell is blanked and its second cell is never painted.
	for x := r.W - 1; x < r.W+4; x++ {
		c := live.Grid().Cell(x, 0)
		if c.Content != " " || c.Width == vt.WidthContinuation {
			t.Fatalf("cell %d past the rect edge is %q/%d, want a blank cell", x, c.Content, c.Width)
		}
	}

	// The dropped glyph leaves exactly the blanks a row that ends early paints.
	f := clipFrame(t, 30, "abcdefghi世", "BBBB")
	got := paintFrame(t, f)
	fits := clipFrame(t, 10, "abcdefghi ", "BBBB")
	sameCells(t, got, paintFrame(t, fits), "wide glyph at edge")
}

// TestPaneRowDropsClusterWiderThanItsSlot pins the width table the terminal is
// not trusted with: a row that ended on a wide cluster with no room stores it in
// one slot, but it still paints two cells, so the clip measures the content.
func TestPaneRowDropsClusterWiderThanItsSlot(t *testing.T) {
	term := vt.NewTerm(10, 1)
	term.Write([]byte("\x1b[?7labcdefghi世"))
	term.Flush()
	if c := term.Grid().Cell(9, 0); c.Content != "世" || c.Width != vt.WidthNormal {
		t.Fatalf("fixture cell 9 is %q/%d, want a wide cluster in one slot", c.Content, c.Width)
	}
	r := layout.Rect{X: 0, Y: 0, W: 10, H: 1}
	live := paintRow(t, term, r)
	for i, want := range "abcdefghi" {
		if c := live.Grid().Cell(i, 0); c.Content != string(want) {
			t.Fatalf("cell %d is %q, want %q", i, c.Content, string(want))
		}
	}
	for x := r.W - 1; x < r.W+4; x++ {
		c := live.Grid().Cell(x, 0)
		if c.Content != " " || c.Width == vt.WidthContinuation {
			t.Fatalf("cell %d past the rect edge is %q/%d, want a blank cell", x, c.Content, c.Width)
		}
	}

	f := clipFrame(t, 10, "\x1b[?7labcdefghi世", "BBBB")
	fits := clipFrame(t, 10, "\x1b[?7labcdefghi ", "BBBB")
	sameCells(t, paintFrame(t, f), paintFrame(t, fits), "cluster wider than its slot")
}

// TestPaneRowFittingIsUnchanged pins that the clip leaves a row that fits
// alone: it paints the same bytes, and a wider row clips down to them.
func TestPaneRowFittingIsUnchanged(t *testing.T) {
	row := "abcdefgh世"
	fits := vt.NewTerm(10, 1)
	fits.Write([]byte(row))
	fits.Flush()
	var want bytes.Buffer
	drawPane(&want, fits, layout.Rect{X: 0, Y: 0, W: 10, H: 1})
	if got := want.String(); got != "\x1b[1;1H\x1b[0;39;49m"+row {
		t.Fatalf("fitted row painted %q, want the row itself", got)
	}

	wide := vt.NewTerm(30, 1)
	wide.Write([]byte(row + strings.Repeat("Z", 20)))
	wide.Flush()
	var got bytes.Buffer
	drawPane(&got, wide, layout.Rect{X: 0, Y: 0, W: 10, H: 1})
	if got.String() != want.String() {
		t.Fatalf("clipped row %q differs from the fitted row %q", got.String(), want.String())
	}
}
