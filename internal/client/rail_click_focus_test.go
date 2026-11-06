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
// that pane. Row 3 is w1p2's row in RailTestSnapshot(10), so the one-based
// mouse row is 4.
func TestRailPaneClickFocusesPane(t *testing.T) {
	tui, got := clientTUI(t, nil)
	tui.snap = testutil.RailTestSnapshot(10)
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 4})
	if m := testutil.NextMsg(t, got); m.Action != "focus" || m.Target != "w1p2" {
		t.Fatalf("pane click sent %+v, want focus w1p2", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 4})
}

// TestRailWindowClickFocusesFirstPane checks that a left press on a window row
// focuses the first available pane in that window, which also makes the window
// active. Row 1 is window one in railRowSnap, so the mouse row is 2. The window
// caret is zero-based column 2, so the name starts at column 4 and the mouse
// column is 5.
func TestRailWindowClickFocusesFirstPane(t *testing.T) {
	tui, got := railMenuTUI(t)
	tui.handleMouse(mouseEvent{btn: 0, x: 5, y: 2})
	if m := testutil.NextMsg(t, got); m.Action != "focus" || m.Target != "w1p1" {
		t.Fatalf("window click sent %+v, want focus w1p1", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 5, y: 2})
	testutil.NoMsg(t, got)
}

// TestRailWindowClickSwitchesWindow checks that a left press on a window row in
// another window focuses that window's first pane, so the active window
// switches. Row 4 is window two, whose first pane is w1p2.
func TestRailWindowClickSwitchesWindow(t *testing.T) {
	tui, got := railMenuTUI(t)
	tui.handleMouse(mouseEvent{btn: 0, x: 5, y: 5})
	if m := testutil.NextMsg(t, got); m.Action != "focus" || m.Target != "w1p2" {
		t.Fatalf("window click sent %+v, want focus w1p2", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 5, y: 5})
	testutil.NoMsg(t, got)
}
