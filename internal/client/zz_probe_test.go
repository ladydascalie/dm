package client

import (
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/testutil"
	"dm/internal/vt"
	"dm/internal/wire"
)

func probeWheel(t *testing.T, snap *wire.Snapshot, id string) {
	t.Helper()
	tui := &tui{snap: snap, vts: map[string]*vt.Term{}}
	for _, p := range snap.Panes {
		t.Logf("pane %s rect %+v mouse=%v", p.ID, p.Rect, p.Mouse)
	}
	p := tui.paneByID(id)
	if p == nil {
		t.Fatalf("no pane %s", id)
	}
	tui.handleMouse(mouseEvent{btn: 64, x: p.Rect.X + 2, y: p.Rect.Y + 2})
	t.Logf("wheel over %s: scroll=%q mode=%q", id, tui.scroll, tui.mode)
	if tui.scroll != id {
		t.Fatalf("wheel over %s scrolled %q", id, tui.scroll)
	}
	tui.apply(wire.Msg{Type: "replay", Target: id, Data: []byte(strings.Repeat("ROW\r\n", 200))})
	t.Logf("after replay: scrollVT=%v maxOff=%d offset=%d", tui.scrollVT != nil, tui.maxOff, tui.offset)
	tui.handleMouse(mouseEvent{btn: 64, x: p.Rect.X + 2, y: p.Rect.Y + 2})
	t.Logf("after second wheel: offset=%d", tui.offset)
	if tui.offset <= 1 {
		t.Fatalf("second wheel did not move the offset: %d", tui.offset)
	}
}

func TestProbeWheelRail(t *testing.T) {
	snap := testutil.RailTestSnapshot(20)
	probeWheel(t, snap, "w1p2")
}

func TestProbeWheelFour(t *testing.T) {
	root := &layout.Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*layout.Node{
			{Dir: "col", Sizes: []float64{0.5, 0.5}, Kids: []*layout.Node{{Pane: "a"}, {Pane: "c"}}},
			{Dir: "col", Sizes: []float64{0.5, 0.5}, Kids: []*layout.Node{{Pane: "b"}, {Pane: "d"}}},
		},
	}
	snap := testutil.TestSnapshot(root, 100, 30, map[string]bool{})
	snap.Focus = "a"
	probeWheel(t, snap, "b")
}
