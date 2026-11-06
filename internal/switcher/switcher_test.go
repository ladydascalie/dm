package switcher

import (
	"reflect"
	"testing"

	"dm/internal/layout"
	"dm/internal/rail"
	"dm/internal/wire"
)

// switcherSnap is a session for the switcher: two workspaces, three windows and
// five panes. Every window is folded and w1's pane carries a status message, so
// a rail note row would appear if the switcher kept one. w1 sits in the folder
// api with the branch dm-api, w2 in the folder web, and w2p2 works in a
// subfolder, so a query can match a window without matching one of its panes.
func switcherSnap() *wire.Snapshot {
	snap := &wire.Snapshot{
		Cols: 100, Rows: 20, Rail: layout.DefaultRail,
		Root: &layout.Node{Pane: "w1p1"}, Focus: "w1p1",
	}
	snap.Panes = []wire.PaneInfo{{ID: "w1p1", Status: "working"}}
	snap.All = []wire.AllPane{
		{
			ID: "w1p1", Name: "serve", Status: "working", Agent: true,
			Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one",
			Focus: true, WorkspaceActive: true, WindowActive: true,
			Cwd: "/work/dm/api", Root: "/work/dm", Branch: "dm-api", InRepo: true,
			Dirty: true, Added: 3, Message: "building",
		},
		{
			ID: "w1p2", Name: "build", Workspace: "s1", WorkspaceName: "main",
			Window: "w1", WindowName: "one", Cwd: "/work/dm/api2", Root: "/work/dm",
		},
		{
			ID: "w2p1", Name: "footer", Workspace: "s1", WorkspaceName: "main",
			Window: "w2", WindowName: "two", Cwd: "/work/dm/web", Root: "/work/dm",
			Branch: "web-work",
		},
		{
			ID: "w2p2", Name: "shell", Workspace: "s1", WorkspaceName: "main",
			Window: "w2", WindowName: "two", Cwd: "/work/dm/tools/capped",
			Root: "/work/dm",
		},
		{
			ID: "w3p1", Name: "notes", Workspace: "s2", WorkspaceName: "docs",
			Window: "w3", WindowName: "three", Cwd: "/work/dm/docs",
			Root: "/work/dm",
		},
	}
	return snap
}

// switcherSnapWithout is the fixture with panes removed.
func switcherSnapWithout(snap *wire.Snapshot, ids ...string) *wire.Snapshot {
	drop := map[string]bool{}
	for _, id := range ids {
		drop[id] = true
	}
	cp := *snap
	cp.All = nil
	for _, p := range snap.All {
		if !drop[p.ID] {
			cp.All = append(cp.All, p)
		}
	}
	return &cp
}

// switcherKindName names a row kind for an expectation.
func switcherKindName(k rail.RailKind) string {
	switch k {
	case rail.Workspace:
		return "ws"
	case rail.Window:
		return "win"
	case rail.Pane:
		return "pane"
	case rail.Gap:
		return "gap"
	}
	return "note"
}

// switcherRowNames is one "kind id" label per surviving row, in tree order.
func switcherRowNames(s *Switcher) []string {
	out := make([]string, len(s.rows))
	for i, row := range s.rows {
		it := s.tree[row]
		name := switcherKindName(it.Kind)
		if id := switcherTarget(it); id != "" {
			name += " " + id
		}
		out[i] = name
	}
	return out
}

// switcherType types a whole query.
func switcherType(s *Switcher, q string) {
	for _, r := range q {
		s.AppendText(r)
	}
}

// TestSwitcherTree checks the tree the float shows: workspaces, windows and
// panes in the rail's order, a gap between the blocks, no note row, and every
// pane of a folded window listed.
func TestSwitcherTree(t *testing.T) {
	snap := switcherSnap()
	s := NewSwitcher(layout.Rect{X: 10, Y: 4, W: 80, H: 24}, snap)
	if s.Rect.W != 80 || s.Rect.H != 24 {
		t.Fatalf("rect = %+v, want the one it opened with", s.Rect)
	}
	want := []string{
		"ws s1", "win w1", "pane w1p1", "pane w1p2",
		"win w2", "pane w2p1", "pane w2p2",
		"gap", "ws s2", "win w3", "pane w3p1",
	}
	if got := switcherRowNames(s); !reflect.DeepEqual(got, want) {
		t.Fatalf("tree = %v, want %v", got, want)
	}
	gaps := 0
	for _, it := range s.tree {
		if it.Kind == rail.WindowNote {
			t.Fatalf("tree kept a note row: %+v", it)
		}
		if it.Kind == rail.Gap {
			gaps++
		}
	}
	if gaps != 1 {
		t.Fatalf("tree has %d gap rows, want one", gaps)
	}
	// Every window of the fixture is folded, and its panes are still there.
	for _, p := range snap.All {
		if p.WindowUnfolded {
			t.Fatalf("pane %s is not folded in the fixture", p.ID)
		}
	}
	for _, id := range []string{"w1p1", "w1p2", "w2p1", "w2p2", "w3p1"} {
		if !s.hasPane(id) {
			t.Fatalf("folded window hid pane %s", id)
		}
	}
	// An empty query selects the first pane row.
	it, ok := s.Selected()
	if !ok || it.Kind != rail.Pane || it.Pane != "w1p1" {
		t.Fatalf("empty query selects %+v, %v, want pane w1p1", it, ok)
	}
}

// TestSwitcherFilter holds the query to the tree: rows survive by their own
// match, headers survive what is under them, and order is the tree's order.
func TestSwitcherFilter(t *testing.T) {
	cases := []struct {
		query string
		want  []string
		pick  string
	}{
		{
			// The folder api is a prefix of api and of api2, and a substring of
			// tools/capped, so the prefix wins the selection.
			query: "ap",
			want:  []string{"ws s1", "win w1", "pane w1p1", "pane w1p2", "win w2", "pane w2p2"},
			pick:  "w1p1",
		},
		{
			// One pane matches: its window and workspace stay, every other pane
			// goes, and the gap goes with the block it separated.
			query: "footer",
			want:  []string{"ws s1", "win w2", "pane w2p1"},
			pick:  "w2p1",
		},
		{
			// The window line matches on its folder, so the pane that works in
			// another folder stays under it.
			query: "web",
			want:  []string{"ws s1", "win w2", "pane w2p1", "pane w2p2"},
			pick:  "w2p1",
		},
		{
			// A workspace id keeps its whole subtree.
			query: "s2",
			want:  []string{"ws s2", "win w3", "pane w3p1"},
			pick:  "w3p1",
		},
		{
			// A window branch keeps every pane under that window.
			query: "dm-api",
			want:  []string{"ws s1", "win w1", "pane w1p1", "pane w1p2"},
			pick:  "w1p1",
		},
		{
			// A workspace name keeps its whole subtree.
			query: "docs",
			want:  []string{"ws s2", "win w3", "pane w3p1"},
			pick:  "w3p1",
		},
		{
			// A window name keeps every pane under that window.
			query: "two",
			want:  []string{"ws s1", "win w2", "pane w2p1", "pane w2p2"},
			pick:  "w2p1",
		},
	}
	for _, c := range cases {
		s := NewSwitcher(layout.Rect{W: 80, H: 24}, switcherSnap())
		switcherType(s, c.query)
		if got := switcherRowNames(s); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("query %q keeps %v, want %v", c.query, got, c.want)
		}
		it, ok := s.Selected()
		if !ok || it.Pane != c.pick {
			t.Fatalf("query %q selects %+v, %v, want pane %s", c.query, it, ok, c.pick)
		}
	}
}

// TestSwitcherBackspace drops rows with the query and gets them back when the
// query shrinks to nothing.
func TestSwitcherBackspace(t *testing.T) {
	s := NewSwitcher(layout.Rect{W: 80, H: 24}, switcherSnap())
	all := switcherRowNames(s)
	switcherType(s, "s2")
	if got := switcherRowNames(s); !reflect.DeepEqual(got, []string{"ws s2", "win w3", "pane w3p1"}) {
		t.Fatalf("query s2 keeps %v", got)
	}
	s.Backspace()
	if n := len(s.rows); n <= 3 {
		t.Fatalf("query s keeps %d rows, want more than 3: %v", n, switcherRowNames(s))
	}
	s.Backspace()
	if got := switcherRowNames(s); !reflect.DeepEqual(got, all) {
		t.Fatalf("backspace to empty keeps %v, want %v", got, all)
	}
	if it, ok := s.Selected(); !ok || it.Kind != rail.Pane {
		t.Fatalf("empty query selects %+v, %v, want a pane row", it, ok)
	}
	// A backspace on an empty query changes nothing.
	s.Backspace()
	if got := switcherRowNames(s); !reflect.DeepEqual(got, all) {
		t.Fatalf("backspace on an empty query keeps %v, want %v", got, all)
	}
	switcherType(s, "s2")
	s.ClearQuery()
	if got := switcherRowNames(s); !reflect.DeepEqual(got, all) {
		t.Fatalf("clearQuery keeps %v, want %v", got, all)
	}
}

// TestSwitcherMove walks the selection and never lands on a gap.
func TestSwitcherMove(t *testing.T) {
	s := NewSwitcher(layout.Rect{W: 80, H: 24}, switcherSnap())
	var walked []string
	for i := 0; i < len(s.rows)+2; i++ {
		it, ok := s.Selected()
		if !ok {
			t.Fatalf("step %d has no selection", i)
		}
		if it.Kind == rail.Gap {
			t.Fatalf("step %d selected a gap row", i)
		}
		walked = append(walked, switcherTarget(it))
		s.Move(1)
	}
	want := []string{
		"w1p1", "w1p2", "w2", "w2p1", "w2p2", "s2", "w3", "w3p1",
		"w3p1", "w3p1", "w3p1", "w3p1", "w3p1",
	}
	if !reflect.DeepEqual(walked, want) {
		t.Fatalf("walked %v, want %v", walked, want)
	}
	// Up again skips the gap from the other side.
	s.Move(-1)
	if it, _ := s.Selected(); switcherTarget(it) != "w3" {
		t.Fatalf("up from w3p1 = %q, want w3", switcherTarget(it))
	}
	s.Move(-1)
	if it, _ := s.Selected(); switcherTarget(it) != "s2" {
		t.Fatalf("up from w3 = %q, want s2", switcherTarget(it))
	}
	s.Move(-1)
	if it, _ := s.Selected(); switcherTarget(it) != "w2p2" {
		t.Fatalf("up over the gap = %q, want w2p2", switcherTarget(it))
	}
	// The top of the list holds.
	s.Move(-30)
	if it, _ := s.Selected(); switcherTarget(it) != "s1" {
		t.Fatalf("up past the top = %q, want s1", switcherTarget(it))
	}
}

// TestSwitcherSync keeps the selection on its row across a snapshot and falls
// back when that row is gone.
func TestSwitcherSync(t *testing.T) {
	snap := switcherSnap()
	s := NewSwitcher(layout.Rect{W: 80, H: 24}, snap)
	switcherType(s, "footer")
	if it, _ := s.Selected(); it.Pane != "w2p1" {
		t.Fatalf("query footer selects %+v, want w2p1", it)
	}
	// The pane survives: so does the selection, query and all.
	s.Sync(switcherSnap())
	if it, ok := s.Selected(); !ok || it.Pane != "w2p1" {
		t.Fatalf("sync moved the selection to %+v, %v", it, ok)
	}
	// An empty query keeps the nearest pane row, which is the one selected.
	s.ClearQuery()
	if it, _ := s.Selected(); it.Pane != "w2p1" {
		t.Fatalf("clearQuery selects %+v, want w2p1", it)
	}
	// The pane is gone: the first pane row takes over.
	s.Sync(switcherSnapWithout(snap, "w2p1", "w2p2"))
	if it, ok := s.Selected(); !ok || it.Pane != "w1p1" {
		t.Fatalf("sync without the selected pane selects %+v, %v, want w1p1", it, ok)
	}
	// Nothing selectable: no selection at all.
	switcherType(s, "footer")
	s.Sync(switcherSnapWithout(snap, "w2p1", "w2p2"))
	if got := switcherRowNames(s); len(got) != 0 {
		t.Fatalf("query footer keeps %v after the pane went", got)
	}
	if _, ok := s.Selected(); ok {
		t.Fatal("selected reports a row while nothing survives")
	}
	if s.sel != -1 || s.Target() != "" {
		t.Fatalf("sel = %d, target = %q, want -1 and no target", s.sel, s.Target())
	}
}

// TestSwitcherPreview takes only the reply to the newest request and asks again
// when the selection moves to another pane.
func TestSwitcherPreview(t *testing.T) {
	s := NewSwitcher(layout.Rect{W: 80, H: 24}, switcherSnap())
	if !s.PreviewDue() {
		t.Fatal("the preview is not due before anything was asked for")
	}
	s.SetPreview(7, "w1p1")
	if s.PreviewDue() {
		t.Fatal("the preview is due for the pane already asked for")
	}
	if s.AcceptPreview(6, []string{"stale"}) {
		t.Fatal("a reply to an older request was accepted")
	}
	if s.lines != nil {
		t.Fatalf("stale reply left lines: %v", s.lines)
	}
	if !s.AcceptPreview(7, []string{"live", "screen"}) {
		t.Fatal("the reply to the newest request was refused")
	}
	if got := s.lines; !reflect.DeepEqual(got, []string{"live", "screen"}) {
		t.Fatalf("lines = %v, want the accepted reply", got)
	}
	// Another pane is selected: the preview is due, and asking again drops the
	// lines that belonged to the pane before.
	switcherType(s, "footer")
	if !s.PreviewDue() {
		t.Fatal("the preview is not due for a new pane")
	}
	s.SetPreview(8, "w2p1")
	if s.lines != nil {
		t.Fatalf("a new request kept the old lines: %v", s.lines)
	}
	if s.AcceptPreview(7, []string{"late"}) {
		t.Fatal("a reply to the replaced request was accepted")
	}
	if s.PreviewDue() {
		t.Fatal("the preview is due for the pane just asked for")
	}
	// Only a pane row has a screen to preview.
	s.Move(-1)
	if it, _ := s.Selected(); it.Kind != rail.Window {
		t.Fatalf("row above the pane = kind %d, want the window line", it.Kind)
	}
	if s.PreviewDue() {
		t.Fatal("the preview is due on a window line")
	}
}

// TestSwitcherTarget maps a row to the id a pick dispatches.
func TestSwitcherTarget(t *testing.T) {
	s := NewSwitcher(layout.Rect{W: 80, H: 24}, switcherSnap())
	if got := s.Target(); got != "w1p1" {
		t.Fatalf("target on the first pane row = %q, want w1p1", got)
	}
	s.Move(-1)
	if it, _ := s.Selected(); it.Kind != rail.Window || s.Target() != "w1" {
		t.Fatalf("target on the window line = %q (kind %d), want w1", s.Target(), it.Kind)
	}
	s.Move(-1)
	if it, _ := s.Selected(); it.Kind != rail.Workspace || s.Target() != "s1" {
		t.Fatalf("target on the workspace header = %q (kind %d), want s1", s.Target(), it.Kind)
	}
}
