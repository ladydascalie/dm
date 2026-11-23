package client

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/rail"
	"dm/internal/testutil"
	"dm/internal/vt"
	"dm/internal/wire"
)

// railScrollTUI is a client whose rail overflows by five item rows: six
// unfolded windows of one pane each come to thirteen item rows on a rail
// that shows eight.
func railScrollTUI(t *testing.T) (*tui, chan wire.Msg) {
	t.Helper()
	tui, got := clientTUI(t, nil)
	snap := &wire.Snapshot{
		Cols: 100, Rows: 10, Rail: 20,
		Root: &layout.Node{Pane: "w1p1"}, Focus: "w1p1",
	}
	rects := map[string]layout.Rect{}
	layout.Layout(snap.Root, layout.PaneAreaFor(snap.Cols, snap.Rows, snap.Rail), testutil.Pad(), rects)
	snap.Panes = []wire.PaneInfo{{ID: "w1p1", Rect: rects["w1p1"]}}
	for i, name := range []string{"one", "two", "three", "four", "five", "six"} {
		snap.All = append(snap.All, wire.AllPane{
			ID: fmt.Sprintf("w%dp1", i+1), Name: fmt.Sprintf("pane%d", i+1),
			Workspace: "s1", WorkspaceName: "main",
			Window: fmt.Sprintf("w%d", i+1), WindowName: name,
			WorkspaceActive: true, WindowActive: true,
			WorkspaceUnfolded: true, WindowUnfolded: true,
		})
	}
	tui.snap = snap
	tui.out = &bytes.Buffer{}
	tui.vts["w1p1"] = vt.NewTerm(rects["w1p1"].W, rects["w1p1"].H)
	return tui, got
}

// railWheelTurn sends one wheel event over the rail: up walks back up the
// list, down walks down it.
func railWheelTurn(tui *tui, up bool) {
	btn := 65
	if up {
		btn = 64
	}
	tui.handleMouse(mouseEvent{btn: btn, x: 4, y: 2})
}

// TestRailWheelScrolls holds the wheel over the rail to the rail: a turn
// moves the list by three rows, the offset clamps at both ends, the hover
// follows the item it lands on, and the paint shows the new window.
func TestRailWheelScrolls(t *testing.T) {
	t.Parallel()
	tui, _ := railScrollTUI(t)
	items := rail.RailItems(tui.snap)
	max := len(items) - (tui.snap.Rows - 2)
	if max != 5 {
		t.Fatalf("the fixture overflows by %d rows, want 5", max)
	}
	railWheelTurn(tui, false)
	if tui.railOff != railWheelStep {
		t.Fatalf("railOff = %d, want %d after one wheel turn", tui.railOff, railWheelStep)
	}
	// The hover is an item row, so the row under the pointer is the one the
	// offset moved to it.
	if want := 1 + tui.railOff; tui.railHover != want {
		t.Fatalf("railHover = %d, want %d", tui.railHover, want)
	}
	// Row 0 now carries the item the offset names, not the workspace row.
	if it, ok := rail.RailItemAt(tui.snap, 3, 0, tui.railOff); !ok || it.Kind != rail.Window {
		t.Fatalf("row 0 = %+v, %v, want the window row the offset names", it, ok)
	}
	// The wheel clamps at the last page and at the top.
	for i := 0; i < 10; i++ {
		railWheelTurn(tui, false)
	}
	if tui.railOff != max {
		t.Fatalf("railOff = %d, want the last page at %d", tui.railOff, max)
	}
	buf := tui.out.(*bytes.Buffer)
	buf.Reset()
	tui.draw()
	frame := buf.String()
	if !strings.Contains(frame, "pane6") {
		t.Fatal("the last page paints no last pane row")
	}
	if strings.Contains(frame, "pane1") {
		t.Fatal("the last page still paints a pane row the offset moved off")
	}
	for i := 0; i < 10; i++ {
		railWheelTurn(tui, true)
	}
	if tui.railOff != 0 {
		t.Fatalf("railOff = %d, want the first page", tui.railOff)
	}
}

// TestRailWheelOutsideLeavesTheRail alone: a wheel off the rail's columns
// never moves the rail's list.
func TestRailWheelOutsideLeavesTheRail(t *testing.T) {
	t.Parallel()
	tui, _ := railScrollTUI(t)
	tui.handleMouse(mouseEvent{btn: 65, x: 40, y: 2})
	if tui.railOff != 0 {
		t.Fatalf("railOff = %d, want 0 for a wheel off the rail", tui.railOff)
	}
}

// TestRailClickFollowsTheScroll focuses the pane the scrolled rail paints: the
// click is resolved through the same offset the paint uses.
func TestRailClickFollowsTheScroll(t *testing.T) {
	t.Parallel()
	tui, got := railScrollTUI(t)
	for i := 0; i < 10; i++ {
		railWheelTurn(tui, false)
	}
	// The last page paints the last pane on the rail's last item row.
	last := tui.snap.Rows - 3
	if it, ok := rail.RailItemAt(tui.snap, 3, last, tui.railOff); !ok || it.Pane != "w6p1" {
		t.Fatalf("last row = %+v, %v, want the last pane", it, ok)
	}
	tui.handleMouse(mouseEvent{btn: 0, x: 4, y: last + 1})
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "focus" || m.Target != "w6p1" {
		t.Fatalf("a click on the scrolled rail sent %+v, want focus w6p1", m)
	}
}

// TestRailScrollClampsOnSnapshot holds a shrinking list to its first page: a
// fold that leaves fewer rows than the offset must not leave the rail blank.
func TestRailScrollClampsOnSnapshot(t *testing.T) {
	t.Parallel()
	tui, _ := railScrollTUI(t)
	for i := 0; i < 10; i++ {
		railWheelTurn(tui, false)
	}
	short := *tui.snap
	short.All = short.All[:1]
	tui.apply(wire.Msg{Type: "snapshot", Snap: &short})
	if tui.railOff != 0 {
		t.Fatalf("railOff = %d, want 0 after the list shrank to one row", tui.railOff)
	}
}
