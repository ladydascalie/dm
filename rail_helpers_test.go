package main

import (
	"testing"

	"dm/internal/layout"
	"dm/internal/rail"
	"dm/internal/wire"
)

// railRowSnap is a rail with every row kind on it: a workspace header, an
// unfolded window line with a folder, a branch and its stats, that window's
// note, the focused pane, a folded window line, the gap, and a second
// workspace. Rows 8 and up are past the last item.
func railRowSnap(railWidth int) *wire.Snapshot {
	snap := &wire.Snapshot{
		Cols: 100, Rows: 10, Rail: railWidth,
		Root: &layout.Node{Pane: "w1p1"}, Focus: "w1p1",
	}
	snap.Panes = []wire.PaneInfo{{ID: "w1p1", Status: "working"}}
	snap.All = []wire.AllPane{
		{
			ID: "w1p1", Name: "pi", Status: "working", Agent: true,
			Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one",
			Focus: true, WorkspaceActive: true, WindowActive: true, WindowUnfolded: true,
			Cwd: "/work/dm/rail", Root: "/work/dm", Branch: "dm-rail", InRepo: true,
			Dirty: true, Added: 12, Deleted: 4, Message: "working hard",
		},
		{
			ID: "w1p2", Name: "beta", Status: "idle", Workspace: "s1",
			WorkspaceName: "main", Window: "w2", WindowName: "two",
			WindowUnfolded: false, Cwd: "/work/dm/other", Root: "/work/dm",
		},
		{
			ID: "w2p1", Name: "gamma", Status: "done", Workspace: "s2",
			WorkspaceName: "work", Window: "w3", WindowName: "three",
			WindowUnfolded: false,
		},
	}
	return snap
}

// railRowFind returns the only row of a kind that carries text.
func railRowFind(t *testing.T, items []rail.RailItem, kind rail.RailKind, text string) rail.RailItem {
	t.Helper()
	for _, it := range items {
		if it.Kind == kind && it.Text == text {
			return it
		}
	}
	t.Fatalf("rail has no kind %d row %q: %+v", kind, text, items)
	return rail.RailItem{}
}
