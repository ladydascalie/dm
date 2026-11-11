package client

import (
	"testing"

	"dm/internal/testutil"
	"dm/internal/wire"
)

// TestRailWorkspaceClickFocusesFirstPane checks that a left press on a
// workspace row focuses the first available pane in that workspace. The row
// order in RailTestSnapshot(10) is workspace, w1p1, w1p2, so row 0 is the
// workspace header and its first pane is w1p1.
func TestRailWorkspaceClickFocusesFirstPane(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	tui, got := railMenuTUI(t)
	tui.handleMouse(mouseEvent{btn: 0, x: 5, y: 5})
	if m := testutil.NextMsg(t, got); m.Action != "focus" || m.Target != "w1p2" {
		t.Fatalf("window click sent %+v, want focus w1p2", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 5, y: 5})
	testutil.NoMsg(t, got)
}

// projectRailSnap is a rail with one project group of two workspaces: a
// project header, then s1 with its window and two panes, a gap, then s2 with
// its window and one pane. Row 0 is the project header.
func projectRailSnap() *wire.Snapshot {
	snap := testutil.RailTestSnapshot(20)
	for i := range snap.All {
		snap.All[i].Project = "dm"
	}
	snap.All = append(snap.All, wire.AllPane{
		ID: "w2p1", Name: "delta", Status: "idle",
		Workspace: "s2", WorkspaceName: "work", Window: "w3", WindowName: "three",
		Project:           "dm",
		WorkspaceUnfolded: true, WindowUnfolded: true,
	})
	return snap
}

// TestRailProjectClickFocusesGroupFirstPane proves a left press on a project
// header focuses the first pane of the group's first workspace, not the row
// that follows it.
func TestRailProjectClickFocusesGroupFirstPane(t *testing.T) {
	t.Parallel()
	tui, got := clientTUI(t, nil)
	tui.snap = projectRailSnap()
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 1})
	if m := testutil.NextMsg(t, got); m.Action != "focus" || m.Target != "w1p1" {
		t.Fatalf("project click sent %+v, want focus w1p1", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 1})
	testutil.NoMsg(t, got)
}

// TestRailProjectDoubleClickDoesNothing proves a second press on the same
// project row sends nothing and opens no prompt or menu.
func TestRailProjectDoubleClickDoesNothing(t *testing.T) {
	t.Parallel()
	tui, got := clientTUI(t, nil)
	tui.snap = projectRailSnap()
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 1})
	if m := testutil.NextMsg(t, got); m.Action != "focus" || m.Target != "w1p1" {
		t.Fatalf("first press sent %+v, want focus w1p1", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 1})
	testutil.NoMsg(t, got)
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 1})
	testutil.NoMsg(t, got)
	if tui.mode != "" {
		t.Fatalf("second press opened mode %q", tui.mode)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 1})
	testutil.NoMsg(t, got)
}

// TestRailProjectRightPressOpensNoMenu proves a project row offers no menu.
func TestRailProjectRightPressOpensNoMenu(t *testing.T) {
	t.Parallel()
	tui, got := clientTUI(t, nil)
	tui.snap = projectRailSnap()
	tui.handleMouse(mouseEvent{btn: 2, x: 3, y: 1})
	if tui.mode != "" || tui.menu != nil {
		t.Fatalf("right press on a project row: mode = %q menu = %+v", tui.mode, tui.menu)
	}
	tui.handleMouse(mouseEvent{btn: 2, release: true, x: 3, y: 1})
	testutil.NoMsg(t, got)
}
