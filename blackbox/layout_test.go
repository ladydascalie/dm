package blackbox

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// layoutNode is the shape of the split tree the list reply carries. Field
// names match the wire value, so it decodes straight from the JSON.
type layoutNode struct {
	Pane string        `json:"pane,omitempty"`
	Dir  string        `json:"dir,omitempty"`
	Kids []*layoutNode `json:"kids,omitempty"`
}

// decodeRoot is the reply's tree, decoded into the local node shape.
func decodeRoot(t *testing.T, tree any) *layoutNode {
	t.Helper()
	b, err := json.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	var n layoutNode
	if err := json.Unmarshal(b, &n); err != nil {
		t.Fatalf("decode tree: %v", err)
	}
	return &n
}

// walkTree returns the pane ids in tree order and whether any split holds
// exactly one kid, the collapse invariant the layout actions must keep.
func walkTree(n *layoutNode) (ids []string, singleKid bool) {
	if n == nil {
		return nil, false
	}
	if n.Pane != "" {
		return []string{n.Pane}, false
	}
	if len(n.Kids) < 2 {
		return nil, true
	}
	for _, k := range n.Kids {
		sub, bad := walkTree(k)
		ids = append(ids, sub...)
		if bad {
			singleKid = true
		}
	}
	return ids, singleKid
}

// peekTree is the active window's split tree from a list call.
func peekTree(t *testing.T, h *harness, session string) *layoutNode {
	t.Helper()
	tree := h.rpc(session, map[string]any{"action": "list"})
	return decodeRoot(t, tree["tree"])
}

// waitAdopted polls until the server answers a read on every pane. A restart
// re-adopts the surviving panes asynchronously; a broadcast before every pane
// attached would miss the ones still pending.
func waitAdopted(t *testing.T, h *harness, session string, panes []string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		all := true
		for _, p := range panes {
			if _, err := h.try("read", p, "-s", session); err != nil {
				all = false
				break
			}
		}
		if all {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the server never adopted every pane")
}

// waitMarkers polls every pane's screen until needle shows in each one. Sync
// broadcasts inside the server; this is the client's proof the marker reached
// every pane of the window.
func waitMarkers(t *testing.T, h *harness, session string, panes []string, needle string) {
	t.Helper()
	deadline := time.Now().Add(waitBudget)
	for {
		var missing []string
		for _, p := range panes {
			out, err := h.try("read", p, "-s", session)
			if err != nil || !strings.Contains(out, needle) {
				missing = append(missing, p)
			}
		}
		if len(missing) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("marker %q never reached panes %v within the %s wait budget",
				needle, missing, waitBudget)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestLayoutFeaturesEndToEnd proves the three layout features through the
// binary: select-layout tiles the window, swap-panes trades ids without
// moving the rectangles, move-pane collapses a leaf's row, synchronize-panes
// broadcasts a write to every pane and the flag survives a server restart.
func TestLayoutFeaturesEndToEnd(t *testing.T) {
	h := newHarness(t)
	s := "layout"
	h.run("new", "-s", s)
	p1 := h.focus(s)
	p2 := h.split(s, p1, "right")
	p3 := h.split(s, p1, "down")
	p4 := h.split(s, p2, "right")
	h.waitPanes(s, 4)
	ids := h.panes(s)
	if len(ids) != 4 || ids[0] != p1 || ids[4-1] != p4 {
		t.Fatalf("setup panes = %v, want four leaves %s..%s", ids, p1, p4)
	}
	waitAdopted(t, h, s, ids)

	// select-layout tiled: the four panes fill a 2x2 grid, every pane in its
	// own cell of equal size, no overlaps, all inside the pane area.
	if res := h.rpc(s, map[string]any{"action": "select-layout", "layout": "tiled"}); res["error"] != nil {
		t.Fatalf("select-layout tiled: %v", res["error"])
	}
	area := h.paneAreaOf(s)
	rects := h.rects(s)
	if len(rects) != 4 {
		t.Fatalf("tiled rects = %d, want 4", len(rects))
	}
	cols := map[int]bool{}
	rows := map[int]bool{}
	seen := map[[2]int]string{}
	var cellW, cellH int
	for id, r := range rects {
		cols[r.X] = true
		rows[r.Y] = true
		if prev, ok := seen[[2]int{r.X, r.Y}]; ok {
			t.Fatalf("tiled collision at %d,%d between %s and %s", r.X, r.Y, prev, id)
		}
		seen[[2]int{r.X, r.Y}] = id
		if r.W <= 0 || r.H <= 0 {
			t.Fatalf("%s has an empty tiled rect %+v", id, r)
		}
		if r.X < area.X || r.Y < area.Y || r.X+r.W > area.X+area.W || r.Y+r.H > area.Y+area.H {
			t.Fatalf("%s rect %+v escapes the pane area %+v", id, r, area)
		}
		if cellW == 0 {
			cellW, cellH = r.W, r.H
		}
		if r.W != cellW || r.H != cellH {
			t.Fatalf("tiled cells differ in size: %+v vs %dx%d", r, cellW, cellH)
		}
	}
	if len(cols) != 2 || len(rows) != 2 || len(seen) != 4 {
		t.Fatalf("tiled grid = %d columns x %d rows across %d cells, want 2x2x4",
			len(cols), len(rows), len(seen))
	}

	// swap-panes: the two ids trade places, so their rectangles swap identity
	// and the other panes keep theirs exactly.
	before := h.rects(s)
	want := [...]string{p4, p3, p2, p1}
	if res := h.rpc(s, map[string]any{"action": "swap-panes", "target": p1, "other": p4}); res["error"] != nil {
		t.Fatalf("swap-panes: %v", res["error"])
	}
	after := h.rects(s)
	if got := h.panes(s); len(got) != 4 || !sameOrder(got, want[:]) {
		t.Fatalf("after the swap tree order = %v, want %v", got, want)
	}
	if after[p1] != before[p4] || after[p4] != before[p1] {
		t.Fatalf("swap did not trade the rects: p1 %v<-want %v, p4 %v<-want %v",
			after[p1], before[p4], after[p4], before[p1])
	}
	for _, id := range []string{p2, p3} {
		if after[id] != before[id] {
			t.Fatalf("swap moved %s's rect: %v -> %v", id, before[id], after[id])
		}
	}

	// move-pane: pull p2 out of its row onto the edge of p1, so its old row
	// collapses; no split may be left holding exactly one kid and the moved
	// pane takes the focus.
	if res := h.rpc(s, map[string]any{"action": "move-pane", "target": p2, "other": p1, "dir": "down"}); res["error"] != nil {
		t.Fatalf("move-pane: %v", res["error"])
	}
	root := peekTree(t, h, s)
	gotIDs, singleKid := walkTree(root)
	if singleKid {
		t.Fatalf("the move left a split with exactly one kid: %+v", root)
	}
	if len(gotIDs) != 4 {
		t.Fatalf("the move changed the pane count: %v", gotIDs)
	}
	set := map[string]bool{}
	for _, id := range gotIDs {
		set[id] = true
	}
	for _, id := range []string{p1, p2, p3, p4} {
		if !set[id] {
			t.Fatalf("the move dropped %s: %v", id, gotIDs)
		}
	}
	if got := h.focus(s); got != p2 {
		t.Fatalf("the move left the focus on %q, want the moved pane %q", got, p2)
	}

	// synchronize-panes: turn it on, write one marker, and every pane prints
	// it. The snapshot carries the flag.
	res := h.rpc(s, map[string]any{"action": "synchronize-panes", "on": true})
	if res["sync"] != true {
		t.Fatalf("synchronize-panes on = %v, want true", res["sync"])
	}
	tree := h.rpc(s, map[string]any{"action": "list"})
	if sync, _ := tree["snapshot"].(map[string]any)["sync"].(bool); !sync {
		t.Fatal("the snapshot does not carry the sync flag")
	}
	marker := "SYNC-MARK-1"
	h.run("write-to", p1, "-s", s, "--msg", "echo "+marker, "--enter")
	waitMarkers(t, h, s, ids, marker)

	// Restart the server: the panes survive, the sync flag rides the state
	// file, and a second marker still reaches every pane.
	boxKillServer(t, h, s)
	h.run("new", "-s", s)
	if got := h.panes(s); len(got) != 4 {
		t.Fatalf("after the restart the window has %d panes, want 4", len(got))
	}
	waitAdopted(t, h, s, ids)
	tree = h.rpc(s, map[string]any{"action": "list"})
	if sync, _ := tree["snapshot"].(map[string]any)["sync"].(bool); !sync {
		t.Fatal("sync was lost across the server restart")
	}
	marker2 := "SYNC-MARK-2"
	h.run("write-to", p3, "-s", s, "--msg", "echo "+marker2, "--enter")
	waitMarkers(t, h, s, ids, marker2)
}

// sameOrder reports whether two id lists are identical element for element.
func sameOrder(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
