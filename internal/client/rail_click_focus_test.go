package client

import (
	"testing"

	"dm/internal/testutil"
)

// TestRailWorkspaceClickFocusesFirstPane checks that a left press on a
// workspace row focuses the first available pane in that workspace. The row
// order in RailTestSnapshot(10) is workspace, w1p1, w1p2, so row 0 is the
// workspace header and its first pane is w1p1.
func TestRailWorkspaceClickFocusesFirstPane(t *testing.T) {
	tui, got := clientTUI(t, nil)
	tui.snap = testutil.RailTestSnapshot(10)
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 1})
	if m := testutil.NextMsg(t, got); m.Action != "focus" || m.Target != "w1p1" {
		t.Fatalf("workspace click sent %+v, want focus w1p1", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 1})
}

// TestRailPaneClickFocusesPane checks that a left press on a pane row focuses
// that pane. Row 2 is w1p2's row in RailTestSnapshot(10), so the one-based
// mouse row is 3.
func TestRailPaneClickFocusesPane(t *testing.T) {
	tui, got := clientTUI(t, nil)
	tui.snap = testutil.RailTestSnapshot(10)
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 3})
	if m := testutil.NextMsg(t, got); m.Action != "focus" || m.Target != "w1p2" {
		t.Fatalf("pane click sent %+v, want focus w1p2", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 3})
}
