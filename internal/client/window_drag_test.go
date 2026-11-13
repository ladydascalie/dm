package client

import (
	"testing"

	"dm/internal/testutil"
	"dm/internal/wire"
)

// winDragTUI is a client whose rail shows one workspace with two folded
// windows: row 0 is the workspace, row 1 window w1, row 2 window w2.
func winDragTUI(t *testing.T) (*tui, chan wire.Msg) {
	t.Helper()
	tr, got := clientTUI(t, nil)
	tr.snap.Rail = 30
	tr.snap.All = []wire.AllPane{
		{ID: "a", Workspace: "s1", WorkspaceName: "one", Window: "w1", WindowName: "one", WorkspaceUnfolded: true},
		{ID: "b", Workspace: "s1", WorkspaceName: "one", Window: "w2", WindowName: "two", WorkspaceUnfolded: true},
	}
	return tr, got
}

// winDragFeed drives one rail mouse gesture in rail coordinates.
func winDragFeed(tr *tui, x, y, btn int, release bool) {
	tr.handleMouse(mouseEvent{btn: btn, x: x + 1, y: y + 1, release: release})
}

// A press on a window row, motion to another row, and a release above a
// window row moves the dragged window before that row's window.
func TestWinDragReordersWindow(t *testing.T) {
	t.Parallel()
	tr, got := winDragTUI(t)

	winDragFeed(tr, 10, 2, 0, false) // press on w2
	if msg := testutil.NextMsg(t, got); msg.Action != "focus" || msg.Target != "b" {
		t.Fatalf("press request = %+v, want focus b", msg)
	}
	winDragFeed(tr, 10, 1, 32, false) // motion onto w1's row
	if tr.winDrag == nil || !tr.winDrag.armed || tr.winDrag.dropRow != 1 {
		t.Fatalf("motion did not arm the drag: %+v", tr.winDrag)
	}
	winDragFeed(tr, 10, 1, 0, true) // release
	if msg := testutil.NextMsg(t, got); msg.Action != "move-window" || msg.Target != "w2" || msg.Dir != "up" {
		t.Fatalf("drop request = %+v, want move-window w2 up", msg)
	}
	testutil.NoMsg(t, got)
	if tr.winDrag != nil {
		t.Fatal("the drag survived its release")
	}
}

// A press and a release on the same row is an ordinary click: the focus
// happens, no move goes out, and the drop marker stays off.
func TestWinDragNeedsMotion(t *testing.T) {
	t.Parallel()
	tr, got := winDragTUI(t)

	winDragFeed(tr, 10, 1, 0, false) // press on w1
	if msg := testutil.NextMsg(t, got); msg.Action != "focus" {
		t.Fatalf("press request = %+v, want focus", msg)
	}
	winDragFeed(tr, 10, 1, 0, true) // release on the same row
	testutil.NoMsg(t, got)
	if tr.winDrop() != nil {
		t.Fatal("a click without motion left a drop hint")
	}
}

// A boundary past the home block clamps to its last window: a drag out of
// the block reorders to the end instead of reparenting.
func TestWinDragClampsToWorkspace(t *testing.T) {
	t.Parallel()
	tr, got := winDragTUI(t)
	tr.snap.All = append(tr.snap.All,
		wire.AllPane{ID: "c", Workspace: "s2", WorkspaceName: "two", Window: "w3", WindowName: "three", WorkspaceUnfolded: true})

	winDragFeed(tr, 10, 1, 0, false) // press on w1
	testutil.NextMsg(t, got)         // the focus
	winDragFeed(tr, 10, 5, 32, false)
	winDragFeed(tr, 10, 5, 0, true) // release over w3, a row in another workspace
	if msg := testutil.NextMsg(t, got); msg.Action != "move-window" || msg.Target != "w1" || msg.Dir != "down" {
		t.Fatalf("drop request = %+v, want move-window w1 down", msg)
	}
	testutil.NoMsg(t, got)
}

// A release outside the rail cancels the drag and moves nothing.
func TestWinDragOutsideCancel(t *testing.T) {
	t.Parallel()
	tr, got := winDragTUI(t)

	winDragFeed(tr, 10, 2, 0, false) // press on w2
	testutil.NextMsg(t, got)
	winDragFeed(tr, 10, 1, 32, false)
	tr.handleMouse(mouseEvent{btn: 0, x: 60, y: 2, release: true}) // off the rail
	testutil.NoMsg(t, got)
	if tr.winDrag != nil {
		t.Fatal("the drag survived a release outside the rail")
	}
}
