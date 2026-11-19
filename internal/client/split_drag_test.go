package client

import (
	"math"
	"testing"

	"dm/internal/layout"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// splitDragTUI is a client whose window holds two panes in a row, so the pad
// between them is a drag target, and a reader for every message it sends.
func splitDragTUI(t *testing.T) (*tui, chan wire.Msg) {
	t.Helper()
	tui, got := clientTUI(t, nil)
	row := &layout.Node{
		Dir: "row", Kids: []*layout.Node{{Pane: "a"}, {Pane: "b"}},
		Sizes: []float64{0.5, 0.5},
	}
	tui.snap = testutil.TestSnapshot(row, 100, 30, nil)
	tui.snap.Focus = "a"
	return tui, got
}

// TestSplitDragSendsTheRatio drives one divider drag: the press sends nothing,
// the first motion sends the share the pointer asks for, and the release forces
// the final one, so the session on the other end has a ratio to apply.
func TestSplitDragSendsTheRatio(t *testing.T) {
	t.Parallel()
	tui, got := splitDragTUI(t)
	ds, ok := dragPress(tui.snap, 49, 5)
	if !ok {
		t.Fatal("the pad between the two panes is not a drag target")
	}
	// The pad is zero-based column 49, so the one-based mouse cell is 50.
	tui.handleMouse(mouseEvent{btn: 0, x: 50, y: 6})
	if !tui.dragging {
		t.Fatal("a press on the pad did not start a drag")
	}
	testutil.NoMsg(t, got)

	// A motion eight cells right asks for the share the pointer names.
	tui.handleMouse(mouseEvent{btn: 32, x: 58, y: 6})
	m := testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != "resize-split" || m.Target != "a" || m.Other != "b" {
		t.Fatalf("motion sent %+v, want resize-split of a and b", m)
	}
	if want := float64(58-1-ds.rect.X) / float64(ds.rect.W); math.Abs(m.Ratio-want) > 1e-9 {
		t.Fatalf("motion ratio = %v, want %v", m.Ratio, want)
	}

	// The release forces the final share through, once.
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 70, y: 6})
	m = testutil.NextMsg(t, got)
	want := float64(70-1-ds.rect.X) / float64(ds.rect.W)
	if m.Action != "resize-split" || math.Abs(m.Ratio-want) > 1e-9 {
		t.Fatalf("release sent %+v, want resize-split at %v", m, want)
	}
	if tui.dragging {
		t.Fatal("the release did not end the drag")
	}
	testutil.NoMsg(t, got)
}
