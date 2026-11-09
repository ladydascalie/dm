package client

import (
	"bytes"
	"fmt"
	"testing"

	"dm/internal/layout"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

// padFrame builds a one-pane client whose keymap and snapshot disagree about
// padding. The pane rects are laid out with the snapshot's value, the way the
// server lays them out.
func padFrame(t *testing.T, keypad, snappad int) *frameTUI {
	t.Helper()
	root := &layout.Node{Pane: "a"}
	snap := &wire.Snapshot{Cols: 30, Rows: 12, Root: root, Focus: "a", Pad: snappad}
	rects := map[string]layout.Rect{}
	layout.Layout(root, layout.PaneAreaForWithTabBar(snap.Cols, snap.Rows, snap.Rail), snappad, rects)
	snap.Panes = []wire.PaneInfo{{ID: "a", Rect: rects["a"]}}
	out := &bytes.Buffer{}
	tr := &tui{snap: snap, vts: map[string]*vt.Term{}, out: out, glyphs: theme.GlyphNerd,
		keymap: testutil.KeymapTestLoad(t, fmt.Sprintf("padding %d\n", keypad))}
	term := vt.NewTerm(rects["a"].W, rects["a"].H)
	term.Write([]byte("\x1b[?25l\x1b[1;1HMMARK"))
	tr.vts["a"] = term
	return &frameTUI{tui: tr, snap: snap, out: out}
}

// TestFrameDrawsWithSnapshotPad proves the client draws with the snapshot's
// padding, not its keymap's. A keymap that says 1 and a snapshot that says 0
// paints a box with no padding ring, and the reverse paints one.
func TestFrameDrawsWithSnapshotPad(t *testing.T) {
	// Keymap 1, server 0: the border sits next to the content.
	f := padFrame(t, 1, 0)
	painted := paintFrame(t, f)
	g := f.tui.theme().Glyphs
	p := f.snap.Panes[0].Rect
	box := layout.BoxRect(p, 0)
	if got, want := termCell(painted, box.X-1, box.Y-1).Content, g.BorderTopLeft; got != want {
		t.Fatalf("snap pad 0: top-left border = %q, want %q", got, want)
	}
	if got := termCell(painted, p.X, p.Y).Char; got != 'M' {
		t.Fatalf("snap pad 0: content cell = %q, want the pane marker", got)
	}

	// Keymap 0, server 1: the border moves out and leaves a blank ring.
	f = padFrame(t, 0, 1)
	painted = paintFrame(t, f)
	g = f.tui.theme().Glyphs
	p = f.snap.Panes[0].Rect
	box = layout.BoxRect(p, 1)
	if got, want := termCell(painted, box.X-1, box.Y-1).Content, g.BorderTopLeft; got != want {
		t.Fatalf("snap pad 1: top-left border = %q, want %q", got, want)
	}
	if got := termCell(painted, p.X-1, p.Y-1).Content; got != " " {
		t.Fatalf("snap pad 1: padding ring cell = %q, want a blank", got)
	}
	if got := termCell(painted, p.X, p.Y).Char; got != 'M' {
		t.Fatalf("snap pad 1: content cell = %q, want the pane marker", got)
	}
}

// TestPaneAtBoxUsesSnapshotPad proves the hit zone agrees with the drawing: the
// grab rect of a pane whose border touches the divider moves with snap.Pad. The
// keymap here says 0, so a client reading it would miss the cell.
func TestPaneAtBoxUsesSnapshotPad(t *testing.T) {
	root := &layout.Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*layout.Node{{Pane: "a"}, {Pane: "b"}},
	}
	snap := &wire.Snapshot{Cols: 30, Rows: 12, Root: root, Focus: "a", Pad: 1}
	rects := map[string]layout.Rect{}
	layout.Layout(root, layout.PaneAreaForWithTabBar(snap.Cols, snap.Rows, snap.Rail), 1, rects)
	snap.Panes = []wire.PaneInfo{{ID: "a", Rect: rects["a"]}, {ID: "b", Rect: rects["b"]}}
	tu := &tui{snap: snap, keymap: testutil.KeymapTestLoad(t, "padding 0\n")}

	a := rects["a"]
	x, y := a.X+a.W+1, a.Y+1 // a's right border, on the divider side
	if got := tu.paneAtBox(x, y); got == nil || got.ID != "a" {
		t.Fatalf("snap pad 1: press on a's divider border hit %+v, want a", got)
	}
	snap.Pad = 0
	if got := tu.paneAtBox(x, y); got != nil {
		t.Fatalf("snap pad 0: press on a's divider border hit %q, want no pane", got.ID)
	}
}

// TestPadNilSnapshotIsZero guards the nil snapshot: padding 0 is a real value,
// so a missing snapshot must not silently fall back to 1.
func TestPadNilSnapshotIsZero(t *testing.T) {
	tu := &tui{keymap: testutil.KeymapTestLoad(t, "padding 1\n")}
	if got := tu.pad(); got != 0 {
		t.Fatalf("pad() with no snapshot = %d, want 0", got)
	}
}
