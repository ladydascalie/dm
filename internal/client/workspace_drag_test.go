package client

import (
	"testing"

	"dm/internal/rail"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// wsDragTUI is a client whose rail shows three same-project workspaces and
// one other-project workspace, each with one folded window.
func wsDragTUI(t *testing.T) (*tui, chan wire.Msg) {
	t.Helper()
	tr, got := clientTUI(t, nil)
	tr.snap.Rail = 30
	tr.snap.All = []wire.AllPane{
		{ID: "a", Workspace: "s1", WorkspaceName: "one", Project: "dm", Window: "w1", WorkspaceUnfolded: true},
		{ID: "b", Workspace: "s2", WorkspaceName: "two", Project: "dm", Window: "w2", WorkspaceUnfolded: true},
		{ID: "c", Workspace: "s3", WorkspaceName: "three", Project: "dm", Window: "w3", WorkspaceUnfolded: true},
		{ID: "d", Workspace: "s4", WorkspaceName: "four", Project: "other", Window: "w4", WorkspaceUnfolded: true},
	}
	return tr, got
}

func wsDragFeed(tr *tui, x, y, btn int, release bool) {
	tr.handleMouse(mouseEvent{btn: btn, x: x + 1, y: y + 1, release: release})
}

// workspaceRank places a workspace inside its project run: neighbouring
// workspaces that share the project count in, separated ones do not.
func TestWorkspaceRankRun(t *testing.T) {
	t.Parallel()
	snap := &wire.Snapshot{All: []wire.AllPane{
		{ID: "a", Workspace: "s1", Project: "dm"},
		{ID: "b", Workspace: "s2", Project: "dm"},
		{ID: "c", Workspace: "s3", Project: ""},
		{ID: "d", Workspace: "s4", Project: ""},
		{ID: "e", Workspace: "s5", Project: "other"},
		{ID: "f", Workspace: "s6", Project: ""},
	}}
	cases := []struct {
		ws           string
		index, count int
	}{
		{"s1", 0, 2},
		{"s2", 1, 2},
		{"s3", 0, 2}, // two untagged neighbours form one run
		{"s4", 1, 2},
		{"s5", 0, 1},
		{"s6", 0, 1}, // separated by a project, an untagged run stays alone
	}
	for _, c := range cases {
		idx, count := workspaceRank(snap, c.ws)
		if idx != c.index || count != c.count {
			t.Fatalf("rank(%s) = %d,%d want %d,%d", c.ws, idx, count, c.index, c.count)
		}
	}
}

// The workspace menu offers the moves the run allows: edge workspaces offer
// one inward move, a lone workspace offers none.
func TestWorkspaceMenuMoveItems(t *testing.T) {
	t.Parallel()
	tr, got := wsDragTUI(t)

	open := func(ws string) []string {
		tr.closeMenu()
		tr.openRailMenu(rail.RailItem{Kind: rail.Workspace, Workspace: ws, Project: "dm"}, 10, 10)
		var out []string
		for _, it := range tr.menu.items {
			out = append(out, it.label)
		}
		return out
	}
	has := func(labels []string, want string) bool {
		for _, l := range labels {
			if l == want {
				return true
			}
		}
		return false
	}
	first := open("s1")
	if !has(first, "Move workspace down") || has(first, "Move workspace up") {
		t.Fatalf("the run's first workspace menu = %v", first)
	}
	last := open("s3")
	if !has(last, "Move workspace up") || has(last, "Move workspace down") {
		t.Fatalf("the run's last workspace menu = %v", last)
	}
	lone := open("s4")
	if has(lone, "Move workspace up") || has(lone, "Move workspace down") {
		t.Fatalf("a lone workspace menu = %v", lone)
	}

	// Running the move sends move-workspace with its direction.
	tr.closeMenu()
	tr.openRailMenu(rail.RailItem{Kind: rail.Workspace, Workspace: "s2", Project: "dm"}, 10, 10)
	for _, it := range tr.menu.items {
		if it.label == "Move workspace up" {
			it.run()
			msg := testutil.NextMsg(t, got)
			if msg.Action != "move-workspace" || msg.Target != "s2" || msg.Dir != "up" {
				t.Fatalf("up request = %+v, want move-workspace s2 up", msg)
			}
			return
		}
	}
	t.Fatal("the middle workspace menu has no move up")
}

// Dragging a workspace row reorders it inside its project run: a release
// above another run member sends move-workspace steps, and a release past
// the run clamps to its end.
func TestWsDragReordersWithinRun(t *testing.T) {
	t.Parallel()
	tr, got := wsDragTUI(t)

	// Rows: 0 project dm, 1 ws s1, 2 w1, 3 gap, 4 ws s2, 5 w2, 6 gap,
	// 7 ws s3, 8 w3, 9 gap, 10 project other, 11 ws s4, 12 w4.
	wsDragFeed(tr, 10, 7, 0, false) // press on ws s3
	if msg := testutil.NextMsg(t, got); msg.Action != "focus" || msg.Target != "c" {
		t.Fatalf("press request = %+v, want focus c", msg)
	}
	wsDragFeed(tr, 10, 1, 32, false) // motion onto ws s1
	if tr.wsDrag == nil || !tr.wsDrag.armed || tr.wsDrag.dropRow != 1 {
		t.Fatalf("motion did not arm the drag: %+v", tr.wsDrag)
	}
	wsDragFeed(tr, 10, 0, 0, true) // release: two swaps carry it to the top
	for i := 0; i < 2; i++ {
		msg := testutil.NextMsg(t, got)
		if msg.Action != "move-workspace" || msg.Target != "s3" || msg.Dir != "up" {
			t.Fatalf("drop request %d = %+v, want move-workspace s3 up", i, msg)
		}
	}
	testutil.NoMsg(t, got)
}

// The run's last member cannot move further: a pointer over another
// project's rows pins the marker to the dragged block and sends nothing.
func TestWsDragNeverLeavesRun(t *testing.T) {
	t.Parallel()
	tr, got := wsDragTUI(t)

	wsDragFeed(tr, 10, 7, 0, false) // press on ws s3, the run's last member
	testutil.NextMsg(t, got)
	wsDragFeed(tr, 10, 11, 32, false) // over ws s4, a different project
	if tr.wsDrag.dropRow != 7 {
		t.Fatalf("marker = %d, want the dragged block's own top", tr.wsDrag.dropRow)
	}
	wsDragFeed(tr, 10, 9, 0, true)
	testutil.NoMsg(t, got)
}

// Asking for the run's end moves the workspace after its run's last block,
// wherever the pointer wandered.
func TestWsDragEndsAtRunEdge(t *testing.T) {
	t.Parallel()
	tr, got := wsDragTUI(t)

	wsDragFeed(tr, 10, 1, 0, false) // press on ws s1
	testutil.NextMsg(t, got)
	wsDragFeed(tr, 10, 7, 32, false) // over ws s3, still in the run
	if tr.wsDrag.dropRow != 7 {
		t.Fatalf("marker = %d, want ws s3's header row", tr.wsDrag.dropRow)
	}
	wsDragFeed(tr, 10, 6, 0, true)
	if msg := testutil.NextMsg(t, got); msg.Action != "move-workspace" || msg.Target != "s1" || msg.Dir != "down" {
		t.Fatalf("drop request = %+v, want move-workspace s1 down", msg)
	}
	testutil.NoMsg(t, got)
}
