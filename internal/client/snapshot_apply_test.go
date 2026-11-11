package client

import (
	"testing"

	"dm/internal/layout"
	"dm/internal/vt"
	"dm/internal/wire"
)

// TestApplySnapshotReflowsAMismatchedSize pins the reattach fix. syncSizes
// sends a pane's resize and asks for its screen without waiting, so the
// snapshot can be the size the pane had before the resize reached it. The
// client must restore at the snapshot's size and reflow to the rect. Refusing
// the mismatch left the pane blank until the program redrew.
func TestApplySnapshotReflowsAMismatchedSize(t *testing.T) {
	t.Parallel()
	// The pane daemon serialised at 40x10, before the layout's resize.
	src := vt.NewTerm(40, 10)
	src.Write([]byte("REATTACH-MARK"))
	src.Flush()
	snap := src.Snapshot()

	// The layout now says 50x12.
	rect := layout.Rect{X: 0, Y: 0, W: 50, H: 12}
	vts := map[string]*vt.Term{}
	if !applySnapshot(vts, "w1p1", rect, snap) {
		t.Fatal("applySnapshot refused a snapshot of a different size")
	}
	term := vts["w1p1"]
	if term == nil {
		t.Fatal("applySnapshot left no terminal")
	}
	cols, rows := term.Grid().Size()
	if cols != rect.W || rows != rect.H {
		t.Fatalf("grid is %dx%d, want %dx%d", cols, rows, rect.W, rect.H)
	}
	if got := term.Grid().Text(0); got != "REATTACH-MARK" {
		t.Fatalf("row 0 is %q, want %q", got, "REATTACH-MARK")
	}
}

// TestApplySnapshotRefusesNil pins the one case that must still be refused: a
// screen message with no snapshot carries nothing to draw.
func TestApplySnapshotRefusesNil(t *testing.T) {
	t.Parallel()
	vts := map[string]*vt.Term{}
	if applySnapshot(vts, "w1p1", layout.Rect{W: 10, H: 5}, nil) {
		t.Fatal("applySnapshot accepted a nil snapshot")
	}
	if vts["w1p1"] != nil {
		t.Fatal("applySnapshot created a terminal for a nil snapshot")
	}
}

// TestScreenMessageRestoresThePane drives the wire path: a screen message with
// a snapshot of a different size fills the pane and reflows it.
func TestScreenMessageRestoresThePane(t *testing.T) {
	t.Parallel()
	src := vt.NewTerm(40, 10)
	src.Write([]byte("WIRE-MARK"))
	src.Flush()
	rect := layout.Rect{X: 0, Y: 0, W: 50, H: 12}
	tui := &tui{
		snap: &wire.Snapshot{
			Cols:  50,
			Rows:  12,
			Panes: []wire.PaneInfo{{ID: "w1p1", Rect: rect}},
		},
		vts: map[string]*vt.Term{},
	}
	tui.apply(wire.Msg{Type: "screen", Target: "w1p1", VTSnap: src.Snapshot()})
	term := tui.vts["w1p1"]
	if term == nil {
		t.Fatal("the screen message left no terminal")
	}
	if got := term.Grid().Text(0); got != "WIRE-MARK" {
		t.Fatalf("row 0 is %q, want %q", got, "WIRE-MARK")
	}
	if cols, rows := term.Grid().Size(); cols != rect.W || rows != rect.H {
		t.Fatalf("grid is %dx%d, want %dx%d", cols, rows, rect.W, rect.H)
	}
}
