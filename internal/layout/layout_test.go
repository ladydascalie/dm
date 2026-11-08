package layout

import "testing"

func TestLayout(t *testing.T) {
	root := &Node{Dir: "row", Kids: []*Node{
		{Pane: "a"},
		{Dir: "col", Kids: []*Node{{Pane: "b"}, {Pane: "c"}}},
	}}
	rects := map[string]Rect{}
	Layout(root, Rect{X: 0, Y: 0, W: 100, H: 29}, 0, rects)
	if len(rects) != 3 {
		t.Fatalf("rects %+v", rects)
	}
	seen := map[[2]int]string{}
	for id, r := range rects {
		if r.W <= 0 || r.H <= 0 {
			t.Fatalf("%s has empty rect %+v", id, r)
		}
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				if prev, ok := seen[[2]int{x, y}]; ok {
					t.Fatalf("%s and %s overlap at %d,%d", prev, id, x, y)
				}
				seen[[2]int{x, y}] = id
			}
		}
	}
	// Each box carries its two borders; adjacent borders form the divider.
	if rects["a"].W+rects["b"].W+4 != 100 {
		t.Fatalf("widths do not tile: %+v", rects)
	}
	if got := Neighbor(rects, "a", "right"); got != "b" {
		t.Fatalf("neighbor right = %q", got)
	}
	if got := Neighbor(rects, "b", "down"); got != "c" {
		t.Fatalf("neighbor down = %q", got)
	}
}

// walkNodes calls f for every node in the tree, splits included.
func walkNodes(n *Node, f func(*Node)) {
	if n == nil {
		return
	}
	f(n)
	for _, k := range n.Kids {
		walkNodes(k, f)
	}
}

// boxShapes are the tree shapes the geometry must hold for.
func boxShapes() []struct {
	name string
	root *Node
} {
	leaf := func(id string) *Node { return &Node{Pane: id} }
	row := func(kids ...*Node) *Node { return &Node{Dir: "row", Kids: kids} }
	col := func(kids ...*Node) *Node { return &Node{Dir: "col", Kids: kids} }
	return []struct {
		name string
		root *Node
	}{
		{"single", leaf("a")},
		{"row2", row(leaf("a"), leaf("b"))},
		{"col2", col(leaf("a"), leaf("b"))},
		{"row3", row(leaf("a"), leaf("b"), leaf("c"))},
		{"nested", row(col(leaf("a"), leaf("c")), leaf("b"))},
		{"nested2", col(row(leaf("a"), leaf("b")), leaf("c"))},
	}
}

// TestBoxTiling proves the boxes and pads tile each split exactly, and that
// pane interiors never overlap.
func TestBoxTiling(t *testing.T) {
	for _, shape := range boxShapes() {
		area := Rect{X: 0, Y: 0, W: 80, H: 24}
		var walk func(n *Node, a Rect)
		walk = func(n *Node, a Rect) {
			if n.Pane != "" {
				in := InsetBox(a, 0)
				if in.W <= 0 || in.H <= 0 {
					t.Fatalf("%s: %s has an empty interior in %v", shape.name, n.Pane, a)
				}
				return
			}
			boxes := ChildBoxRects(n, a)
			if len(boxes) != len(n.Kids) {
				t.Fatalf("%s: %d boxes for %d kids", shape.name, len(boxes), len(n.Kids))
			}
			used := 0
			for i, b := range boxes {
				if n.Dir == "row" {
					if b.X < a.X || b.X+b.W > a.X+a.W || b.Y != a.Y || b.H != a.H {
						t.Fatalf("%s: box %v escapes %v", shape.name, b, a)
					}
					used += b.W
				} else {
					if b.Y < a.Y || b.Y+b.H > a.Y+a.H || b.X != a.X || b.W != a.W {
						t.Fatalf("%s: box %v escapes %v", shape.name, b, a)
					}
					used += b.H
				}
				walk(n.Kids[i], b)
			}
			want := a.W
			if n.Dir != "row" {
				want = a.H
			}
			if used != want {
				t.Fatalf("%s: boxes and pads use %d of %d", shape.name, used, want)
			}
		}
		walk(shape.root, area)

		rects := map[string]Rect{}
		Layout(shape.root, area, 0, rects)
		ids := AllPanes(shape.root)
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				a, b := rects[ids[i]], rects[ids[j]]
				if a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H {
					t.Fatalf("%s: %s and %s overlap: %v %v", shape.name, ids[i], ids[j], a, b)
				}
			}
		}
	}
}

// TestDividerIsAdjacent pins the pane spacing: boxes touch, so the divider is
// the two adjacent border cells and there is no empty gap cell.
func TestDividerIsAdjacent(t *testing.T) {
	area := Rect{X: 0, Y: 0, W: 80, H: 24}
	row := &Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*Node{{Pane: "a"}, {Pane: "b"}},
	}
	boxes := ChildBoxRects(row, area)
	if boxes[1].X != boxes[0].X+boxes[0].W {
		t.Fatalf("row boxes are not adjacent: %v", boxes)
	}
	if p := PadRect(row, boxes, 0); p.X != boxes[0].X+boxes[0].W-1 || p.W != 2 {
		t.Fatalf("row divider = %v, want the two border cells", p)
	}

	col := &Node{
		Dir: "col", Sizes: []float64{0.5, 0.5},
		Kids: []*Node{{Pane: "a"}, {Pane: "b"}},
	}
	boxes = ChildBoxRects(col, area)
	if boxes[1].Y != boxes[0].Y+boxes[0].H {
		t.Fatalf("column boxes are not adjacent: %v", boxes)
	}
	if p := PadRect(col, boxes, 0); p.Y != boxes[0].Y+boxes[0].H-1 || p.H != 2 {
		t.Fatalf("column divider = %v, want the two border cells", p)
	}

	// The content rects lose only their own borders, so an 80-wide area gives
	// two 38-cell interiors around four border cells.
	rects := map[string]Rect{}
	Layout(row, area, 0, rects)
	if rects["a"].W != 38 || rects["b"].W != 38 {
		t.Fatalf("interiors = %v, want 38 each", rects)
	}
}

func TestNodeRectMatchesLayout(t *testing.T) {
	for _, shape := range boxShapes() {
		area := Rect{X: 0, Y: 0, W: 80, H: 24}
		rects := map[string]Rect{}
		Layout(shape.root, area, 0, rects)
		walkNodes(shape.root, func(n *Node) {
			got, ok := NodeRect(shape.root, n, area, 0)
			if !ok {
				t.Fatalf("%s: nodeRect missed a node", shape.name)
			}
			if n.Pane != "" {
				if got != rects[n.Pane] {
					t.Fatalf("%s: nodeRect(%s) = %v, layout = %v", shape.name, n.Pane, got, rects[n.Pane])
				}
				return
			}
			if got.W <= 0 || got.H <= 0 {
				t.Fatalf("%s: split area %v", shape.name, got)
			}
		})
	}
}

func TestPaneAreaInset(t *testing.T) {
	area := PaneArea(100, 30)
	if area != (Rect{X: 0, Y: 0, W: 100, H: 29}) {
		t.Fatalf("pane area = %+v", area)
	}
	if PaneArea(3, 4) != (Rect{X: 0, Y: 0, W: 3, H: 3}) {
		t.Fatalf("tiny area = %+v", PaneArea(3, 4))
	}
}

// TestPaneAreaWithTabBar pins the reserve: the tab bar takes the first row to
// the right of the rail, the pane area starts directly below it and loses that
// row from its height, and the status bar keeps the last row.
func TestPaneAreaWithTabBar(t *testing.T) {
	area := PaneAreaForWithTabBar(100, 30, 20)
	if area != (Rect{X: 20, Y: TabBarRows, W: 80, H: 28}) {
		t.Fatalf("pane area with tab bar = %+v", area)
	}
	bar := Rect{X: area.X, Y: area.Y - TabBarRows, W: area.W, H: TabBarRows}
	if bar.Y != 0 || bar.Y+bar.H != area.Y {
		t.Fatalf("tab bar row %+v does not sit above pane area %+v", bar, area)
	}
	if area.Y+area.H != 29 {
		t.Fatalf("pane area reaches row %d, want the row above 29", area.Y+area.H)
	}
	// A screen too short for both clamps to an empty area, not a negative one.
	if got := PaneAreaForWithTabBar(10, 1, 0); got.H < 0 {
		t.Fatalf("short screen height = %d", got.H)
	}
}

// TestPaneBoxesStartBelowTabBar proves every pane box shifts down by the tab
// bar rows and loses them from its height.
func TestPaneBoxesStartBelowTabBar(t *testing.T) {
	root := &Node{Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*Node{{Pane: "a"}, {Pane: "b"}}}
	plain := map[string]Rect{}
	Layout(root, PaneAreaFor(100, 30, 20), 0, plain)
	shifted := map[string]Rect{}
	Layout(root, PaneAreaForWithTabBar(100, 30, 20), 0, shifted)
	for id := range plain {
		want := plain[id]
		want.Y += TabBarRows
		want.H -= TabBarRows
		if shifted[id] != want {
			t.Fatalf("%s box %+v, want %+v", id, shifted[id], want)
		}
		if shifted[id].Y <= TabBarRows {
			t.Fatalf("%s starts at row %d, want below the tab bar row %d", id, shifted[id].Y, TabBarRows)
		}
	}
}

func TestSplitRatio(t *testing.T) {
	root := &Node{Pane: "a"}
	SplitLeaf(root, "a", "right", "b", 0.25)
	rects := map[string]Rect{}
	Layout(root, Rect{X: 0, Y: 0, W: 100, H: 10}, 0, rects)
	if got := rects["a"].W; got < 22 || got > 24 {
		t.Fatalf("existing pane width = %d, want about 23", got)
	}
	if got := rects["b"].W; got < 72 || got > 74 {
		t.Fatalf("new pane width = %d, want about 73", got)
	}
	if rects["a"].W+rects["b"].W+4 != 100 {
		t.Fatalf("sizes do not tile: %+v", rects)
	}
}

func TestSplitAndPrune(t *testing.T) {
	root := &Node{Pane: "a"}
	if !SplitLeaf(root, "a", "right", "b", 0) {
		t.Fatal("split failed")
	}
	if root.Dir != "row" || len(root.Kids) != 2 || root.Kids[1].Pane != "b" {
		t.Fatalf("root %+v", root)
	}
	SplitLeaf(root.Kids[1], "b", "down", "c", 0)
	n, ok := PruneLeaf(root, "b")
	if !ok || len(n.Kids) != 2 || n.Kids[1].Pane != "c" {
		t.Fatalf("prune left %+v", n)
	}
}

// TestInsetBoxPadding proves the content rect clears the border and then the
// padding, and that a box too small for the padding keeps its border and drops
// the padding rather than going empty.
func TestInsetBoxPadding(t *testing.T) {
	box := Rect{X: 2, Y: 3, W: 20, H: 10}
	if got, want := InsetBox(box, 0), (Rect{X: 3, Y: 4, W: 18, H: 8}); got != want {
		t.Fatalf("pad 0 = %+v, want %+v", got, want)
	}
	if got, want := InsetBox(box, 1), (Rect{X: 4, Y: 5, W: 16, H: 6}); got != want {
		t.Fatalf("pad 1 = %+v, want %+v", got, want)
	}
	if got, want := InsetBox(box, 2), (Rect{X: 5, Y: 6, W: 14, H: 4}); got != want {
		t.Fatalf("pad 2 = %+v, want %+v", got, want)
	}
	// A 5-cell box fits a border and one cell of padding, not two.
	if got, want := InsetBox(Rect{X: 0, Y: 0, W: 5, H: 5}, 2), (Rect{X: 1, Y: 1, W: 3, H: 3}); got != want {
		t.Fatalf("pad 2 in a 5-cell box = %+v, want %+v", got, want)
	}
	// A 2-cell box has no room for a border at all.
	if got, want := InsetBox(Rect{X: 0, Y: 0, W: 2, H: 2}, 1), (Rect{X: 0, Y: 0, W: 2, H: 2}); got != want {
		t.Fatalf("pad 1 in a 2-cell box = %+v, want %+v", got, want)
	}
}

// TestLayoutPadding proves the padding reaches every leaf through the tree.
func TestLayoutPadding(t *testing.T) {
	root := &Node{Dir: "row", Kids: []*Node{{Pane: "a"}, {Pane: "b"}}}
	area := Rect{X: 0, Y: 0, W: 40, H: 10}
	plain := map[string]Rect{}
	Layout(root, area, 0, plain)
	padded := map[string]Rect{}
	Layout(root, area, 1, padded)
	for id := range plain {
		want := plain[id]
		want.X++
		want.Y++
		want.W -= 2
		want.H -= 2
		if padded[id] != want {
			t.Fatalf("%s padded = %+v, want %+v", id, padded[id], want)
		}
	}
}
