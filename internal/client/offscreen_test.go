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

// TestFrameOffscreenPaneWritesNothing pins the cull: a pane whose rect misses
// the grid is not painted and carries no row fingerprint, so a change in its
// scrollback never reaches the frame.
func TestFrameOffscreenPaneWritesNothing(t *testing.T) {
	t.Parallel()
	f := newFrameTUI(t, 100, 20, 34)
	p := &f.snap.Panes[0]
	p.Rect = layout.Rect{X: 140, Y: 4, W: 10, H: 5}
	term := vt.NewTerm(p.Rect.W, p.Rect.H)
	term.Write([]byte("\x1b[?25l\x1b[1;1HOFFSCREEN-MARK"))
	f.tui.vts[p.ID] = term

	f.frame() // the first frame settles the picture
	if sig, ok := f.tui.rowsig[p.ID]; ok {
		t.Fatalf("offscreen pane kept a row fingerprint: %v", sig)
	}
	term.Write([]byte("\x1b[1;1HSTILL-OFF-SCREEN"))
	if got := f.frame(); got != "" {
		t.Fatalf("offscreen pane wrote %d bytes: %q", len(got), got)
	}
}

// TestFramePartialPanePaintsOnScreenRowsOnly pins that a pane that overlaps
// the grid paint its on-screen rows and nothing else. The y clip in the row
// loop is unchanged; the cull only drops a pane that misses the grid whole.
func TestFramePartialPanePaintsOnScreenRowsOnly(t *testing.T) {
	t.Parallel()
	f := newFrameTUI(t, 100, 20, 34)
	p := &f.snap.Panes[0]
	// Rows 15..24: the last five rows of the rect fall below a 20-row grid.
	p.Rect = layout.Rect{X: 2, Y: 15, W: 10, H: 10}
	term := vt.NewTerm(p.Rect.W, p.Rect.H)
	term.Write([]byte("\x1b[?25l\x1b[1;1HPANE-MARK"))
	f.tui.vts[p.ID] = term

	f.frame()

	// A row below the grid moved: no bytes, because no on-screen cell changed.
	term.Write([]byte("\x1b[8;1HBELOW-GRID"))
	if got := f.frame(); got != "" {
		t.Fatalf("pane row below the grid wrote %d bytes: %q", len(got), got)
	}

	// An on-screen row moved: exactly that row is repainted.
	term.Write([]byte("\x1b[2;1HON-SCREEN"))
	got := f.frame()
	if !strings.Contains(got, "ON-SCREEN") {
		t.Fatalf("on-screen row missing: %q", got)
	}
	addrs := frameAddrs(got)
	if len(addrs) != 1 {
		t.Fatalf("frame wrote %d rows, want 1: %v", len(addrs), addrs)
	}
	if want := [2]int{p.Rect.Y + 1 + 1, p.Rect.X + 1}; addrs[0] != want {
		t.Fatalf("on-screen row written at %v, want %v", addrs[0], want)
	}
}

// TestFrameCursorHiddenWhenFocusPaneOffscreen pins that an offscreen focus
// pane writes no cursor position, so the cursor stays hidden instead of being
// placed at a row outside the grid.
func TestFrameCursorHiddenWhenFocusPaneOffscreen(t *testing.T) {
	t.Parallel()
	f := newFrameTUI(t, 100, 20, 34)
	p := &f.snap.Panes[0]
	p.Rect = layout.Rect{X: 120, Y: 2, W: 10, H: 6}
	term := f.tui.vts[p.ID]
	term.Write([]byte("\x1b[?25h\x1b[1;1H")) // a visible cursor on the pane

	got := f.frame()
	if strings.Contains(got, "\x1b[?25h") {
		t.Fatalf("offscreen focus pane placed the cursor: %q", got)
	}
	if !strings.Contains(got, "\x1b[?25l") {
		t.Fatalf("offscreen focus pane did not hide the cursor: %q", got)
	}
}

// TestFrameOffscreenPaneWritesNoOffGridMove pins the bands a pane owns. A
// layout with a pane wholly outside the grid must write no cursor move past
// win.Cols or win.Rows for it, on a full repaint.
func TestFrameOffscreenPaneWritesNoOffGridMove(t *testing.T) {
	t.Parallel()
	f := newFrameTUI(t, 100, 20, 34)
	p := &f.snap.Panes[0]
	p.Rect = layout.Rect{X: 140, Y: 4, W: 10, H: 5}

	got := f.frame() // a full repaint
	if !strings.Contains(got, "\x1b[2J") {
		t.Fatalf("first frame is not a whole repaint: %q", got)
	}
	for _, a := range frameAddrs(got) {
		if a[0] < 1 || a[0] > f.snap.Rows || a[1] < 1 || a[1] > f.snap.Cols {
			t.Fatalf("frame wrote a cursor move outside the %dx%d grid: row %d, col %d",
				f.snap.Cols, f.snap.Rows, a[0], a[1])
		}
	}
}

// quadSession builds the split the recon names: four panes in a row, laid out
// in a pane area narrower than the child count, so the last children run past
// the right edge. With three columns the last pane starts at X=3.
func quadSession(t *testing.T, cols, rows, rail int) *wire.Snapshot {
	t.Helper()
	ids := []string{"p1", "p2", "p3", "p4"}
	root := &layout.Node{Dir: "row"}
	for _, id := range ids {
		root.Kids = append(root.Kids, &layout.Node{Pane: id})
	}
	snap := &wire.Snapshot{Cols: cols, Rows: rows, Rail: rail, Root: root, Focus: "p1"}
	rects := map[string]layout.Rect{}
	layout.Layout(root, layout.PaneAreaForWithTabBar(cols, rows, rail), testutil.Pad(), rects)
	for _, id := range ids {
		snap.Panes = append(snap.Panes, wire.PaneInfo{ID: id, Rect: rects[id]})
		snap.All = append(snap.All, wire.AllPane{
			ID: id, Name: id, Workspace: "s1", WorkspaceName: "main",
			Window: "w1", WindowName: "one", Focus: id == "p1",
			WorkspaceActive: true, WindowActive: true,
			WindowUnfolded: true, WorkspaceUnfolded: true,
		})
	}
	return snap
}

// newQuadTUI is a four-pane frame sink, for the layouts where a split area is
// narrower than its child count.
func newQuadTUI(t *testing.T, cols, rows, rail int) *frameTUI {
	t.Helper()
	snap := quadSession(t, cols, rows, rail)
	out := &bytes.Buffer{}
	tui := &tui{snap: snap, vts: map[string]*vt.Term{}, out: out, glyphs: theme.GlyphNerd}
	for _, p := range snap.Panes {
		term := vt.NewTerm(p.Rect.W, p.Rect.H)
		term.Write([]byte("\x1b[?25l" + p.ID))
		tui.vts[p.ID] = term
	}
	return &frameTUI{tui: tui, snap: snap, out: out}
}

// TestGripsStayOnGrid pins the grip band. The four-pane, 3-column split puts
// the last pad at X=2 with a width of 2, so its second grip is at X=3, past the
// grid. Pre-fix this test fails at row 6, column 4. The pad itself overlaps the
// grid, so the fix clips the cell, it does not drop the pad.
func TestGripsStayOnGrid(t *testing.T) {
	t.Parallel()
	snap := quadSession(t, 3, 10, 0)
	var grips bytes.Buffer
	drawGrips(&grips, theme.DefaultTheme(), snap)
	if grips.Len() == 0 {
		t.Fatal("no grips drawn: the fixture is not the split it claims")
	}
	for _, a := range frameAddrs(grips.String()) {
		if a[0] < 1 || a[0] > snap.Rows || a[1] < 1 || a[1] > snap.Cols {
			t.Fatalf("grip wrote a cursor move outside the %dx%d grid: row %d, col %d",
				snap.Cols, snap.Rows, a[0], a[1])
		}
	}

	// The whole frame of the same layout must stay on the grid too.
	f := newQuadTUI(t, 3, 10, 0)
	got := f.frame()
	if !strings.Contains(got, "\x1b[2J") {
		t.Fatalf("first frame is not a whole repaint: %q", got)
	}
	for _, a := range frameAddrs(got) {
		if a[0] < 1 || a[0] > f.snap.Rows || a[1] < 1 || a[1] > f.snap.Cols {
			t.Fatalf("frame wrote a cursor move outside the %dx%d grid: row %d, col %d",
				f.snap.Cols, f.snap.Rows, a[0], a[1])
		}
	}
}

// TestFramePartialPaneWritesNoOffGridMove pins the reviewer's probe: a pane at
// Y=15 with H=10 on a 20-row grid. Its box runs past the last row, so pre-fix
// the border wrote off the grid. The box cells that are on screen must survive:
// the right border still paints on the last visible row, at the border column
// the configured padding puts it at.
func TestFramePartialPaneWritesNoOffGridMove(t *testing.T) {
	t.Parallel()
	f := newFrameTUI(t, 100, 20, 34)
	p := &f.snap.Panes[0]
	p.Rect = layout.Rect{X: 2, Y: 15, W: 10, H: 10}

	got := f.frame()
	for _, a := range frameAddrs(got) {
		if a[0] < 1 || a[0] > f.snap.Rows || a[1] < 1 || a[1] > f.snap.Cols {
			t.Fatalf("frame wrote a cursor move outside the %dx%d grid: row %d, col %d",
				f.snap.Cols, f.snap.Rows, a[0], a[1])
		}
	}
	box := layout.BoxRect(p.Rect, f.tui.km().Padding())
	// drawPaneBoxRows puts the right border one cell past the box rect, in
	// zero-based cells, and frameAddrs addresses are one-based.
	wantRow, wantCol := f.snap.Rows, box.X+box.W+1
	onScreen := false
	for _, a := range frameAddrs(got) {
		if a[0] == wantRow && a[1] == wantCol {
			onScreen = true
		}
	}
	if !onScreen {
		t.Fatalf("the pane's on-screen border cell at row %d, col %d was dropped: %q",
			wantRow, wantCol, got)
	}
}
