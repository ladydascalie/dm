package layout

import (
	"fmt"
	"testing"
)

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

// sumInts returns the total of a size list.
func sumInts(v []int) int {
	n := 0
	for _, x := range v {
		n += x
	}
	return n
}

// within reports whether inner lies inside outer.
func within(inner, outer Rect) bool {
	return inner.X >= outer.X && inner.Y >= outer.Y &&
		inner.X+inner.W <= outer.X+outer.W &&
		inner.Y+inner.H <= outer.Y+outer.H
}

// checkSizes asserts the SplitSizes contract: an exact sum and a one-cell
// floor whenever the space can hold every child.
func checkSizes(t *testing.T, name string, got []int, k, space int) {
	t.Helper()
	if len(got) != k {
		t.Fatalf("%s: %d sizes for %d kids", name, len(got), k)
	}
	if sumInts(got) != space {
		t.Fatalf("%s: sizes %v sum to %d, want %d", name, got, sumInts(got), space)
	}
	if space < k {
		return
	}
	for i, v := range got {
		if v < 1 {
			t.Fatalf("%s: size %d = %d, want at least 1", name, i, v)
		}
	}
}

// splitNode builds a split with the given weights.
func splitNode(weights []float64) *Node {
	n := &Node{Dir: "row"}
	for range weights {
		n.Kids = append(n.Kids, &Node{Pane: "p"})
	}
	n.Sizes = weights
	return n
}

// TestSplitSizesClamp pins the reconciliation: the sizes sum to space, every
// child keeps at least one cell, and the excess comes off the largest child.
func TestSplitSizesClamp(t *testing.T) {
	cases := []struct {
		name    string
		weights []float64
		space   int
		want    []int
	}{
		{"dominant over ten", []float64{100, 1, 1}, 10, []int{8, 1, 1}},
		{"k2 equal", []float64{1, 1}, 9, []int{5, 4}},
		{"k2 dominant", []float64{100, 1}, 10, []int{9, 1}},
		{"k3 equal", []float64{1, 1, 1}, 10, []int{4, 3, 3}},
		{"k4 equal", []float64{1, 1, 1, 1}, 10, []int{3, 3, 2, 2}},
		{"k5 equal", []float64{1, 1, 1, 1, 1}, 10, []int{2, 2, 2, 2, 2}},
		{"k6 equal", []float64{1, 1, 1, 1, 1, 1}, 10, []int{2, 2, 2, 2, 1, 1}},
		{"one dominant weight", []float64{10, 1, 1}, 12, []int{10, 1, 1}},
		{"space equals k equal", []float64{1, 1, 1}, 3, []int{1, 1, 1}},
		{"space equals k dominant", []float64{100, 1, 1}, 3, []int{1, 1, 1}},
		{"space far above weights", []float64{1, 1, 1}, 60, []int{20, 20, 20}},
		{"space far above dominant", []float64{100, 1, 1}, 60, []int{58, 1, 1}},
		{"no weights", []float64{1, 1, 1}, 7, []int{3, 2, 2}},
	}
	for _, c := range cases {
		got := SplitSizes(splitNode(c.weights), c.space)
		checkSizes(t, c.name, got, len(c.weights), c.space)
		if c.want == nil {
			continue
		}
		if len(got) != len(c.want) {
			t.Fatalf("%s: sizes %v, want %v", c.name, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: sizes %v, want %v", c.name, got, c.want)
			}
		}
	}
	// A split without weights divides equally.
	none := &Node{Dir: "row", Kids: []*Node{{Pane: "a"}, {Pane: "b"}, {Pane: "c"}}}
	checkSizes(t, "absent sizes", SplitSizes(none, 7), 3, 7)
}

// TestSplitSizesClampSweep proves the contract holds for every child count,
// weight shape and space in the range a terminal can produce.
func TestSplitSizesClampSweep(t *testing.T) {
	for k := 2; k <= 6; k++ {
		weights := map[string][]float64{
			"equal":     make([]float64, k),
			"dominant":  make([]float64, k),
			"ramp up":   make([]float64, k),
			"ramp down": make([]float64, k),
			"alternate": make([]float64, k),
		}
		for i := 0; i < k; i++ {
			weights["equal"][i] = 1
			weights["dominant"][i] = 1
			weights["ramp up"][i] = float64(i + 1)
			weights["ramp down"][i] = float64(k - i)
			weights["alternate"][i] = 1 + float64(i%2)*9
		}
		weights["dominant"][0] = 1000
		for name, w := range weights {
			n := splitNode(w)
			for space := k; space <= 60; space++ {
				got := SplitSizes(n, space)
				checkSizes(t, name, got, k, space)
			}
		}
	}
}

// TestLayoutChildrenInsideParent proves every child box stays inside its
// parent's area, for the shapes the tree can take and for a split that needs
// the clamp.
func TestLayoutChildrenInsideParent(t *testing.T) {
	leaf := func(id string) *Node { return &Node{Pane: id} }
	shapes := append([]struct {
		name string
		root *Node
	}{}, boxShapes()...)
	shapes = append(shapes,
		struct {
			name string
			root *Node
		}{"row3 dominant", &Node{Dir: "row", Sizes: []float64{100, 1, 1},
			Kids: []*Node{leaf("a"), leaf("b"), leaf("c")}}},
		struct {
			name string
			root *Node
		}{"col3 dominant", &Node{Dir: "col", Sizes: []float64{100, 1, 1},
			Kids: []*Node{leaf("a"), leaf("b"), leaf("c")}}},
		struct {
			name string
			root *Node
		}{"mixed dominant", &Node{Dir: "row", Sizes: []float64{100, 1},
			Kids: []*Node{leaf("a"), &Node{Dir: "col", Sizes: []float64{100, 1},
				Kids: []*Node{leaf("b"), leaf("c")}}}}},
	)
	areas := []Rect{
		{X: 0, Y: 0, W: 80, H: 24},
		{X: 3, Y: 1, W: 12, H: 6},
	}
	for _, shape := range shapes {
		for _, area := range areas {
			rects := map[string]Rect{}
			Layout(shape.root, area, 0, rects)
			walkNodes(shape.root, func(n *Node) {
				if n.Pane != "" {
					if r := rects[n.Pane]; !within(r, area) {
						t.Fatalf("%s: pane %s %v escapes %v", shape.name, n.Pane, r, area)
					}
					return
				}
				parent, ok := NodeRect(shape.root, n, area, 0)
				if !ok {
					t.Fatalf("%s: parent rect missing", shape.name)
				}
				for _, kid := range n.Kids {
					got, ok := NodeRect(shape.root, kid, area, 0)
					if !ok {
						t.Fatalf("%s: child rect missing", shape.name)
					}
					if !within(got, parent) {
						t.Fatalf("%s: child %v escapes parent %v", shape.name, got, parent)
					}
				}
				boxes := ChildBoxRects(n, parent)
				sum := 0
				for i, b := range boxes {
					if !within(b, parent) {
						t.Fatalf("%s: box %v escapes parent %v", shape.name, b, parent)
					}
					if n.Dir == "row" {
						sum += b.W
					} else {
						sum += b.H
					}
					if got := rects[n.Kids[i].Pane]; n.Kids[i].Pane != "" && !within(got, b) {
						t.Fatalf("%s: pane %s %v escapes box %v", shape.name, n.Kids[i].Pane, got, b)
					}
				}
				want := parent.W
				if n.Dir != "row" {
					want = parent.H
				}
				if sum != want {
					t.Fatalf("%s: boxes use %d of %d", shape.name, sum, want)
				}
			})
		}
	}
}

// noSingleKidNode reports whether every split in the tree holds at least two
// kids, so no node ever holds exactly one.
func noSingleKidNode(n *Node) bool {
	if n == nil {
		return true
	}
	if n.Pane != "" {
		return true
	}
	if len(n.Kids) < 2 {
		return false
	}
	for _, k := range n.Kids {
		if !noSingleKidNode(k) {
			return false
		}
	}
	return true
}

// sameSet reports whether two id lists hold exactly the same multisets.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, id := range a {
		m[id]++
	}
	for _, id := range b {
		m[id]--
		if m[id] < 0 {
			return false
		}
	}
	return true
}

// parentSplit returns the split node whose Kids directly hold the leaf id.
func parentSplit(n *Node, id string) *Node {
	if n == nil || n.Pane != "" {
		return nil
	}
	for _, k := range n.Kids {
		if k.Pane == id {
			return n
		}
	}
	for _, k := range n.Kids {
		if p := parentSplit(k, id); p != nil {
			return p
		}
	}
	return nil
}

// TestSwapKeepsRects proves a swap trades the two ids in place: every pane
// keeps its rectangle and the tree shape is untouched.
func TestSwapKeepsRects(t *testing.T) {
	root := &Node{Dir: "row", Sizes: []float64{0.2, 0.8}, Kids: []*Node{
		{Pane: "a"},
		{Dir: "col", Sizes: []float64{0.25, 0.75}, Kids: []*Node{
			{Pane: "b"}, {Pane: "c"},
		}},
	}}
	area := Rect{X: 0, Y: 0, W: 100, H: 24}
	before := map[string]Rect{}
	Layout(root, area, 0, before)
	sorted := AllPanes(root)

	if !Swap(root, "b", "c") {
		t.Fatal("swap failed")
	}
	if !noSingleKidNode(root) {
		t.Fatal("a swap created a single-kid split")
	}
	if !sameSet(AllPanes(root), sorted) {
		t.Fatalf("a swap changed the pane set: %v", AllPanes(root))
	}
	afterRects := map[string]Rect{}
	Layout(root, area, 0, afterRects)
	for id, r := range before {
		got := afterRects[id]
		switch id {
		case "b":
			if got != before["c"] {
				t.Fatalf("after the swap b took rect %v, want c's %v", got, before["c"])
			}
		case "c":
			if got != before["b"] {
				t.Fatalf("after the swap c took rect %v, want b's %v", got, before["b"])
			}
		default:
			if got != r {
				t.Fatalf("%s rect moved: %v -> %v", id, r, got)
			}
		}
	}
}

// TestSwapSelfAndBack pins the no-op edges: swapping a leaf with itself
// leaves the tree alone, and two swaps restore the original ids.
func TestSwapSelfAndBack(t *testing.T) {
	root := &Node{Dir: "row", Kids: []*Node{{Pane: "a"}, {Pane: "b"}}}
	if !Swap(root, "a", "a") {
		t.Fatal("swapping a leaf with itself returned false")
	}
	if got := AllPanes(root); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("a self-swap changed the tree: %v", got)
	}
	if !Swap(root, "a", "b") || !Swap(root, "a", "b") {
		t.Fatal("a round-trip swap failed")
	}
	if got := AllPanes(root); got[0] != "a" || got[1] != "b" {
		t.Fatalf("a round-trip swap changed the tree: %v", got)
	}
}

// TestSwapUnknown pins the false cases: an id that is not a leaf in the tree
// refuses the swap and leaves the tree alone.
func TestSwapUnknown(t *testing.T) {
	root := &Node{Dir: "row", Kids: []*Node{{Pane: "a"}, {Pane: "b"}}}
	if Swap(root, "a", "ghost") || Swap(root, "ghost", "b") || Swap(root, "ghost", "ghost2") {
		t.Fatal("a swap naming a missing pane returned true")
	}
	if got := AllPanes(root); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("a refused swap changed the tree: %v", got)
	}
	if Swap(nil, "a", "b") {
		t.Fatal("a swap on a nil tree returned true")
	}
}

// TestMoveFromDeepLeaf moves a top-level leaf to the edge of a pane deep in
// the tree, in all four directions, and pins where the moved pane lands.
func TestMoveFromDeepLeaf(t *testing.T) {
	mk := func() *Node {
		return &Node{Dir: "row", Kids: []*Node{
			{Pane: "a"},
			{Dir: "col", Kids: []*Node{
				{Pane: "b"},
				{Dir: "col", Kids: []*Node{{Pane: "c"}, {Pane: "t"}}},
			}},
		}}
	}
	cases := []struct {
		dir     string
		wantDir string
		side    int // 1 = moved pane after target, -1 = before
	}{
		{"right", "row", 1},
		{"left", "row", -1},
		{"down", "col", 1},
		{"up", "col", -1},
	}
	for _, c := range cases {
		root := mk()
		if !Move(root, "a", "t", c.dir) {
			t.Fatalf("move %s failed", c.dir)
		}
		if !noSingleKidNode(root) {
			t.Fatalf("move %s left a single-kid split", c.dir)
		}
		if !sameSet(AllPanes(root), []string{"a", "b", "c", "t"}) {
			t.Fatalf("move %s changed the pane set: %v", c.dir, AllPanes(root))
		}
		p := parentSplit(root, "t")
		if p == nil || p.Dir != c.wantDir {
			t.Fatalf("move %s: target's parent = %+v, want a %s split", c.dir, p, c.wantDir)
		}
		at := -1
		for i, k := range p.Kids {
			if k.Pane == "t" {
				at = i
			}
		}
		if at < 0 || at+c.side < 0 || at+c.side >= len(p.Kids) || p.Kids[at+c.side].Pane != "a" {
			t.Fatalf("move %s: %q is not %s of %q in %+v", c.dir, "a", c.dir, "t", p.Kids)
		}
	}
}

// TestMoveCollapse proves the collapse rule after every move: moving a pane
// never leaves a node holding exactly one kid, for the shapes the prune
// creates.
func TestMoveCollapse(t *testing.T) {
	leaf := func(id string) *Node { return &Node{Pane: id} }
	row := func(kids ...*Node) *Node { return &Node{Dir: "row", Kids: kids} }
	col := func(kids ...*Node) *Node { return &Node{Dir: "col", Kids: kids} }
	cases := []struct {
		name string
		root *Node
		id   string
		tgt  string
		dir  string
		want []string
	}{
		{"siblings collapse the root", col(leaf("a"), leaf("t")), "a", "t", "right", []string{"t", "a"}},
		{"row sibling", row(leaf("a"), leaf("t")), "a", "t", "down", []string{"t", "a"}},
		{"target under the chain", row(leaf("a"), col(leaf("b"), leaf("t"))), "a", "t", "right", []string{"b", "t", "a"}},
		{"moved pane's parent collapses", row(col(leaf("a"), leaf("b")), leaf("t")), "b", "t", "right", []string{"a", "t", "b"}},
		{"deep target keeps its split", col(row(leaf("a"), leaf("b")), col(leaf("t"), leaf("c"))), "a", "t", "left", []string{"b", "a", "t", "c"}},
	}
	for _, c := range cases {
		if !Move(c.root, c.id, c.tgt, c.dir) {
			t.Fatalf("%s: move failed", c.name)
		}
		if !noSingleKidNode(c.root) {
			t.Fatalf("%s: a move left a single-kid split", c.name)
		}
		if got := AllPanes(c.root); !sameSet(got, c.want) || len(got) != len(c.want) {
			t.Fatalf("%s: panes %v, want %v", c.name, got, c.want)
		}
	}
}

// TestMoveUnknown pins the false cases: a missing id, a target outside the
// tree and a nil tree all refuse the move without touching it. Moving a pane
// to its own edge is a no-op.
func TestMoveUnknown(t *testing.T) {
	root := &Node{Dir: "row", Kids: []*Node{{Pane: "a"}, {Pane: "b"}}}
	if Move(root, "a", "ghost", "right") {
		t.Fatal("a move to a missing target returned true")
	}
	if Move(root, "ghost", "b", "right") {
		t.Fatal("a move of a missing pane returned true")
	}
	if Move(nil, "a", "b", "right") {
		t.Fatal("a move on a nil tree returned true")
	}
	if got := AllPanes(root); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("a refused move changed the tree: %v", got)
	}
	if !Move(root, "a", "a", "right") {
		t.Fatal("a move to its own edge returned false")
	}
	if got := AllPanes(root); got[0] != "a" || got[1] != "b" {
		t.Fatalf("a no-op move changed the tree: %v", got)
	}
}

// presetNames are the five layouts ApplyPreset builds.
var presetNames = []string{
	"even-horizontal", "even-vertical",
	"main-horizontal", "main-vertical", "tiled",
}

// TestApplyPreset runs every preset on 2, 3, 5 and 7 panes and asserts the
// contract: the tree returns the ids in the exact order given, the rectangles
// tile the area without an overlap or an empty pane, and the pane set never
// gains or loses an id.
func TestApplyPreset(t *testing.T) {
	for _, n := range []int{2, 3, 5, 7} {
		ids := make([]string, n)
		for i := range ids {
			ids[i] = fmt.Sprintf("p%d", i)
		}
		kids := make([]*Node, n)
		for i, id := range ids {
			kids[i] = &Node{Pane: id}
		}
		old := &Node{Dir: "col", Kids: kids}
		for _, name := range presetNames {
			root, ok := ApplyPreset(old, name, ids, 80, 24)
			if !ok {
				t.Fatalf("%s on %d panes failed", name, n)
			}
			got := AllPanes(root)
			if len(got) != len(ids) {
				t.Fatalf("%s on %d panes holds %v, want exactly %v", name, n, got, ids)
			}
			for i := range got {
				if got[i] != ids[i] {
					t.Fatalf("%s on %d panes order = %v, want %v", name, n, got, ids)
				}
			}
			area := Rect{X: 0, Y: 0, W: 80, H: 24}
			rects := map[string]Rect{}
			Layout(root, area, 0, rects)
			if len(rects) != n {
				t.Fatalf("%s on %d panes laid out %d rects", name, n, len(rects))
			}
			seen := map[[2]int]string{}
			for id, r := range rects {
				if r.W <= 0 || r.H <= 0 {
					t.Fatalf("%s on %d panes: %s has empty rect %+v", name, n, id, r)
				}
				if !within(r, area) {
					t.Fatalf("%s on %d panes: %s rect %+v escapes %+v", name, n, id, r, area)
				}
				for y := r.Y; y < r.Y+r.H; y++ {
					for x := r.X; x < r.X+r.W; x++ {
						if prev, ok := seen[[2]int{x, y}]; ok {
							t.Fatalf("%s on %d panes: %s and %s overlap at %d,%d", name, n, prev, id, x, y)
						}
						seen[[2]int{x, y}] = id
					}
				}
			}
		}
	}
}

// TestApplyPresetUnknownName pins the one false case the brief names: a name
// outside the five is refused without touching anything.
func TestApplyPresetUnknownName(t *testing.T) {
	old := &Node{Dir: "row", Kids: []*Node{{Pane: "a"}, {Pane: "b"}}}
	if root, ok := ApplyPreset(old, "diagonal", []string{"a", "b"}, 80, 24); ok || root != nil {
		t.Fatalf("unknown name = %+v, %v, want nil, false", root, ok)
	}
}

// TestApplyPresetPaneSet pins the preservation rule: a set that drops a pane,
// duplicates one, or names a pane the tree does not hold is refused, so
// ApplyPreset can never lose or gain a pane.
func TestApplyPresetPaneSet(t *testing.T) {
	old := &Node{Dir: "col", Kids: []*Node{{Pane: "a"}, {Pane: "b"}, {Pane: "c"}}}
	for _, name := range presetNames {
		if root, ok := ApplyPreset(old, name, []string{"a", "b"}, 80, 24); ok || root != nil {
			t.Fatalf("%s dropped a pane = %+v, %v, want nil, false", name, root, ok)
		}
		if root, ok := ApplyPreset(old, name, []string{"a", "b", "b"}, 80, 24); ok || root != nil {
			t.Fatalf("%s duplicated a pane = %+v, %v, want nil, false", name, root, ok)
		}
		if root, ok := ApplyPreset(old, name, []string{"a", "b", "ghost"}, 80, 24); ok || root != nil {
			t.Fatalf("%s added a pane = %+v, %v, want nil, false", name, root, ok)
		}
	}
}

// TestApplyPresetMainShape pins the main geometries: the main pane holds half
// the split's space and the rest stack in the perpendicular direction, so the
// main pane is strictly larger than any stacked sibling once the stack has
// more than one pane.
func TestApplyPresetMainShape(t *testing.T) {
	ids := []string{"m", "a", "b", "c"}
	old := &Node{Dir: "row", Kids: []*Node{{Pane: "m"}, {Pane: "a"}, {Pane: "b"}, {Pane: "c"}}}
	vertical, ok := ApplyPreset(old, "main-vertical", ids, 80, 24)
	if !ok {
		t.Fatal("main-vertical failed")
	}
	if vertical.Dir != "row" || len(vertical.Kids) != 2 || vertical.Kids[0].Pane != "m" {
		t.Fatalf("main-vertical root %+v, want row {main, rest}", vertical)
	}
	if rest := vertical.Kids[1]; rest.Dir != "col" || len(rest.Kids) != 3 {
		t.Fatalf("main-vertical rest %+v, want a column of three", rest)
	}
	horizontal, ok := ApplyPreset(old, "main-horizontal", ids, 80, 24)
	if !ok {
		t.Fatal("main-horizontal failed")
	}
	if horizontal.Dir != "col" || len(horizontal.Kids) != 2 || horizontal.Kids[0].Pane != "m" {
		t.Fatalf("main-horizontal root %+v, want col {main, rest}", horizontal)
	}
	if rest := horizontal.Kids[1]; rest.Dir != "row" || len(rest.Kids) != 3 {
		t.Fatalf("main-horizontal rest %+v, want a row of three", rest)
	}
	rects := map[string]Rect{}
	Layout(vertical, Rect{X: 0, Y: 0, W: 80, H: 24}, 0, rects)
	// The main pane owns the whole split's height; every stacked pane shares
	// the right column, so the main pane is strictly the larger rectangle.
	if rects["m"].H <= rects["a"].H || rects["m"].W*rects["m"].H <= rects["a"].W*rects["a"].H {
		t.Fatalf("main pane %+v is not larger than a stacked pane %+v", rects["m"], rects["a"])
	}
}

// TestApplyPresetTiledGrid pins the balanced grid on an unfactorable count:
// the panes fill row-major, the last row keeps full-width cells, and every
// cell holds a pane.
func TestApplyPresetTiledGrid(t *testing.T) {
	ids := []string{"a", "b", "c", "d", "e"}
	old := &Node{Dir: "col", Kids: []*Node{{Pane: "a"}, {Pane: "b"}, {Pane: "c"}, {Pane: "d"}, {Pane: "e"}}}
	root, ok := ApplyPreset(old, "tiled", ids, 80, 24)
	if !ok {
		t.Fatal("tiled failed")
	}
	// 80x24 is 3.3:1, so five panes want two rows of three and two.
	if len(root.Kids) != 2 || root.Kids[0].Dir != "row" || len(root.Kids[0].Kids) != 3 || len(root.Kids[1].Kids) != 2 {
		t.Fatalf("tiled root %+v, want a column of a 3-row and a 2-row", root)
	}
	if got := AllPanes(root); len(got) != 5 || got[0] != "a" || got[4] != "e" {
		t.Fatalf("tiled order = %v, want row-major a..e", got)
	}
}

// TestMiniRectsMatchesApplyPreset pins the miniature preview to the real
// geometry: at a known size, the rects two named presets produce are the rects
// ApplyPreset walked to over the same ids, at the same area and no padding.
func TestMiniRectsMatchesApplyPreset(t *testing.T) {
	ids := []string{"a", "b", "c"}
	root := &Node{Dir: "col", Kids: []*Node{{Pane: "a"}, {Pane: "b"}, {Pane: "c"}}}
	area := Rect{X: 0, Y: 0, W: 40, H: 14}
	for _, name := range []string{"even-horizontal", "tiled"} {
		got, ok := MiniRects(root, area, ids, name)
		if !ok {
			t.Fatalf("MiniRects %s failed", name)
		}
		tree, ok := ApplyPreset(root, name, ids, area.W, area.H)
		if !ok {
			t.Fatalf("ApplyPreset %s failed", name)
		}
		want := map[string]Rect{}
		Layout(tree, area, 0, want)
		for id, r := range want {
			if got[id] != r {
				t.Fatalf("%s pane %s = %+v, want %+v", name, id, got[id], r)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("%s placed %d panes, want %d", name, len(got), len(want))
		}
	}
}

// TestMiniRectsCustomWalksTheTree proves an empty name previews the tree as it
// stands, and that a pane the tree does not hold refuses.
func TestMiniRectsCustomWalksTheTree(t *testing.T) {
	root := &Node{Dir: "row", Kids: []*Node{
		{Pane: "a"},
		{Dir: "col", Sizes: []float64{2, 1}, Kids: []*Node{{Pane: "b"}, {Pane: "c"}}},
	}}
	got, ok := MiniRects(root, Rect{W: 30, H: 10}, []string{"a", "b", "c"}, "")
	if !ok {
		t.Fatal("custom MiniRects failed")
	}
	want := map[string]Rect{}
	Layout(root, Rect{W: 30, H: 10}, 0, want)
	if got["a"] != want["a"] || got["c"] != want["c"] {
		t.Fatalf("custom rects = %+v, want %+v", got, want)
	}
	// The caller names the ids; a stale id refuses rather than previewing a
	// pane the window no longer holds.
	if _, ok := MiniRects(root, Rect{W: 30, H: 10}, []string{"a", "b", "z"}, ""); ok {
		t.Fatal("a pane outside the tree did not refuse")
	}
	if _, ok := MiniRects(root, Rect{W: 30, H: 10}, []string{"a"}, ""); ok {
		t.Fatal("a short pane list did not refuse")
	}
}
