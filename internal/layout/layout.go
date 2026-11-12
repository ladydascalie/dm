// Package layout holds the split tree and all pane geometry. It is pure: it
// reads the tree and returns rectangles, and it imports nothing but stdlib.
package layout

// Node is a pane leaf or a split. Dir "row" means children side by side.
// Sizes gives each child its share of the split, so a caller can keep the
// existing pane at a ratio instead of always halving. Absent means equal.
type Node struct {
	Pane  string    `json:"pane,omitempty"`
	Dir   string    `json:"dir,omitempty"`
	Kids  []*Node   `json:"kids,omitempty"`
	Sizes []float64 `json:"sizes,omitempty"`
}

// Rect is a cell rectangle on screen.
type Rect struct {
	X, Y, W, H int
}

// MaxInt returns the larger of a and b.
func MaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Walk calls f for every pane id in the tree.
func Walk(n *Node, f func(string)) {
	if n == nil {
		return
	}
	if n.Pane != "" {
		f(n.Pane)
		return
	}
	for _, k := range n.Kids {
		Walk(k, f)
	}
}

// AllPanes returns every pane id in the tree, in tree order.
func AllPanes(n *Node) []string {
	var out []string
	Walk(n, func(id string) { out = append(out, id) })
	return out
}

// SplitLeaf replaces the leaf holding target with a split, keeping z-order.
// ratio is the share the existing pane keeps; 0 or 1 falls back to half.
func SplitLeaf(root *Node, target, dir, newID string, ratio float64) bool {
	if root == nil {
		return false
	}
	if root.Pane == target {
		if ratio <= 0 || ratio >= 1 {
			ratio = 0.5
		}
		old := &Node{Pane: target}
		kid := &Node{Pane: newID}
		root.Pane = ""
		switch dir {
		case "left", "up":
			root.Dir = "row"
			if dir == "up" {
				root.Dir = "col"
			}
			root.Kids = []*Node{kid, old}
			root.Sizes = []float64{1 - ratio, ratio}
		default: // right, down
			root.Dir = "row"
			if dir == "down" {
				root.Dir = "col"
			}
			root.Kids = []*Node{old, kid}
			root.Sizes = []float64{ratio, 1 - ratio}
		}
		return true
	}
	for _, k := range root.Kids {
		if SplitLeaf(k, target, dir, newID, ratio) {
			return true
		}
	}
	return false
}

// Swap exchanges the two leaf panes' ids in place: the panes trade ids and
// nothing else, so every rectangle keeps its shape. It returns false when
// either id is not a leaf in the tree. Swapping a leaf with itself is a
// no-op that returns true.
func Swap(root *Node, a, b string) bool {
	if root == nil {
		return false
	}
	pa := AncestorPath(root, a, nil)
	pb := AncestorPath(root, b, nil)
	if pa == nil || pb == nil {
		return false
	}
	na, nb := pa[len(pa)-1], pb[len(pb)-1]
	if na.Pane != a || nb.Pane != b {
		return false
	}
	na.Pane, nb.Pane = b, a
	return true
}

// Move moves the leaf pane id to the edge of target in dir: it prunes id out
// of the tree, then splits target with a fresh sibling in dir holding id, so
// the tree re-balances the way SplitLeaf does. A parent left with one kid by
// the prune collapses, and the split never has one, so no node ends up
// holding exactly one kid. Both ids must be leaves of the same tree; a target
// outside the tree, a missing id, or a nil tree returns false. Moving a pane
// to its own edge is a no-op that returns true.
func Move(root *Node, id, target, dir string) bool {
	if root == nil {
		return false
	}
	if id == target {
		return FindLeaf(root, id)
	}
	if !FindLeaf(root, target) {
		return false
	}
	nr, ok := PruneLeaf(root, id)
	if !ok || nr == nil {
		return false
	}
	if !SplitLeaf(nr, target, dir, id, 0) {
		return false
	}
	// PruneLeaf may return a different node than root when the root collapses;
	// the final tree hangs off nr, so copy it back onto the caller's root.
	*root = *nr
	return true
}

// PruneLeaf removes a leaf and collapses its parent split.
func PruneLeaf(root *Node, target string) (*Node, bool) {
	if root == nil {
		return nil, false
	}
	if root.Pane == target {
		return nil, true
	}
	for i, k := range root.Kids {
		nk, removed := PruneLeaf(k, target)
		if !removed {
			continue
		}
		if nk == nil {
			root.Kids = append(root.Kids[:i], root.Kids[i+1:]...)
		} else {
			root.Kids[i] = nk
		}
		if len(root.Sizes) == len(root.Kids)+1 {
			root.Sizes = append(root.Sizes[:i], root.Sizes[i+1:]...)
		}
		if len(root.Kids) == 1 {
			return root.Kids[0], true
		}
		return root, true
	}
	return root, false
}

// ChildBoxRects divides a split's area into its children's boxes. The boxes
// are adjacent, so the two borders between neighbours form the divider; there
// is no empty cell. This keeps the visual gap between panes to one cell in
// both axes, the way a conventional multiplexer spaces them.
func ChildBoxRects(n *Node, r Rect) []Rect {
	if n == nil || n.Pane != "" || len(n.Kids) == 0 {
		return nil
	}
	k := len(n.Kids)
	horizontal := n.Dir == "row"
	space := r.W
	if !horizontal {
		space = r.H
	}
	if space < k {
		space = k
	}
	sizes := SplitSizes(n, space)
	rects := make([]Rect, k)
	off := 0
	for i := range n.Kids {
		if horizontal {
			rects[i] = Rect{X: r.X + off, Y: r.Y, W: sizes[i], H: r.H}
		} else {
			rects[i] = Rect{X: r.X, Y: r.Y + off, W: r.W, H: sizes[i]}
		}
		off += sizes[i]
	}
	return rects
}

// PadRect is the divider between child i's box and child i+1's box: the two
// adjacent border cells, one from each box.
func PadRect(n *Node, boxes []Rect, i int) Rect {
	b := boxes[i]
	if n.Dir == "row" {
		return Rect{X: b.X + b.W - 1, Y: b.Y, W: 2, H: b.H}
	}
	return Rect{X: b.X, Y: b.Y + b.H - 1, W: b.W, H: 2}
}

// Layout assigns a rectangle to every pane. Each split burns one column or row
// for the divider, so panes never share a cell. pad is the blank cells between
// a pane's border and its content.
func Layout(n *Node, area Rect, pad int, out map[string]Rect) {
	if n == nil {
		return
	}
	if n.Pane != "" {
		out[n.Pane] = InsetBox(area, pad)
		return
	}
	boxes := ChildBoxRects(n, area)
	for i, kid := range n.Kids {
		Layout(kid, boxes[i], pad, out)
	}
}

// NodeRect returns the rectangle Layout assigns to a node in the tree.
func NodeRect(n, target *Node, area Rect, pad int) (Rect, bool) {
	if n == nil {
		return Rect{}, false
	}
	if n == target {
		if n.Pane != "" {
			return InsetBox(area, pad), true
		}
		return area, true
	}
	if n.Pane != "" {
		return Rect{}, false
	}
	for i, b := range ChildBoxRects(n, area) {
		if out, ok := NodeRect(n.Kids[i], target, b, pad); ok {
			return out, ok
		}
	}
	return Rect{}, false
}

// AncestorPath returns the nodes from root to id, inclusive.
func AncestorPath(n *Node, id string, path []*Node) []*Node {
	if n == nil {
		return nil
	}
	path = append(path, n)
	if n.Pane == id {
		return path
	}
	for _, k := range n.Kids {
		if p := AncestorPath(k, id, path); p != nil {
			return p
		}
	}
	return nil
}

// LowestCommonAncestor returns the deepest node on both paths.
func LowestCommonAncestor(a, b []*Node) *Node {
	var lca *Node
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			break
		}
		lca = a[i]
	}
	return lca
}

// DefaultRail is the width a session asks for when it has none yet. It leaves
// room for a window label and its right-aligned git stats.
const DefaultRail = 34

// CollapsedRail is the width of the thin rail: an agent mark, the project
// bar, the state glyph, and the grip strip. The glyph keeps the column it
// has in the expanded rail, so collapsing does not move the mark.
const CollapsedRail = 4

// RailCompact reports whether a rail width is the thin, glyph-only form.
func RailCompact(rail int) bool { return rail > 0 && rail <= CollapsedRail }

// MaxRail is the widest rail cols allows: a third of the screen, capped at 46.
func MaxRail(cols int) int {
	m := cols / 3
	if m > 46 {
		m = 46
	}
	if m < 0 {
		m = 0
	}
	return m
}

// EffectiveRail clamps a requested width to what the screen can give.
func EffectiveRail(cols, rail int) int {
	if rail < 0 {
		rail = 0
	}
	if m := MaxRail(cols); rail > m {
		rail = m
	}
	return rail
}

// PaneAreaFor is the rectangle the pane boxes live in. Each leaf is a box: a
// border, the content, and a border. Adjacent boxes keep one empty cell between
// them, the pad a resize control will live on. The last row is the status bar.
// The rail takes the left columns before the first box.
func PaneAreaFor(cols, rows, rail int) Rect {
	rail = EffectiveRail(cols, rail)
	w := cols - rail
	if w < 1 {
		w = 1
	}
	return Rect{rail, 0, w, rows - 1}
}

// PaneArea is the pane area with no rail.
func PaneArea(cols, rows int) Rect {
	return PaneAreaFor(cols, rows, 0)
}

// TabBarRows is the height of the window tab bar: one row above the pane area.
// The server keeps the row clear when it lays out panes, and the client draws
// the window tabs there.
const TabBarRows = 1

// PaneAreaForWithTabBar is PaneAreaFor with the tab bar row reserved at the
// top. Pane boxes start one row below the screen top. The last row stays the
// status bar. A screen too short for both gets an empty area, never a negative
// one.
func PaneAreaForWithTabBar(cols, rows, rail int) Rect {
	a := PaneAreaFor(cols, rows, rail)
	a.Y += TabBarRows
	a.H -= TabBarRows
	if a.H < 0 {
		a.H = 0
	}
	return a
}

// BoxRect is the interior of a pane's box: the content rect grown by pad, so
// the border sits one cell outside it. It is the inverse of InsetBox.
func BoxRect(content Rect, pad int) Rect {
	pad = MaxInt(0, pad)
	return Rect{
		X: content.X - pad, Y: content.Y - pad,
		W: content.W + 2*pad, H: content.H + 2*pad,
	}
}

// InsetBox is the content rect of a boxed pane: one cell for the border, then
// pad cells of padding. A box too small for the padding keeps its border and
// drops the padding, so a tiny pane still shows content.
func InsetBox(b Rect, pad int) Rect {
	if b.W < 3 || b.H < 3 {
		return Rect{X: b.X, Y: b.Y, W: MaxInt(1, b.W), H: MaxInt(1, b.H)}
	}
	inset := 1 + MaxInt(0, pad)
	if b.W < 2*inset+1 || b.H < 2*inset+1 {
		inset = 1
	}
	return Rect{X: b.X + inset, Y: b.Y + inset, W: b.W - 2*inset, H: b.H - 2*inset}
}

// SplitSizes turns the node's weights into integer cell counts. When space
// holds at least one cell per child, every count is at least 1 and the counts
// sum to exactly space. A split without weights divides equally. Each child
// takes the floor of its proportional share, the largest remainders take the
// spare cells, and any overshoot left by the floor of 1 is reclaimed from the
// largest counts, lowest index first on a tie. A space too small for one cell
// per child returns 1 for each child.
func SplitSizes(n *Node, space int) []int {
	k := len(n.Kids)
	out := make([]int, k)
	if k == 0 {
		return out
	}
	if space < k {
		for i := range out {
			out[i] = 1
		}
		return out
	}
	weights := make([]float64, k)
	total := 0.0
	for i := range weights {
		w := 1.0
		if i < len(n.Sizes) && n.Sizes[i] > 0 {
			w = n.Sizes[i]
		}
		weights[i] = w
		total += w
	}
	// rem holds each child's fractional share, so the spare cells go to the
	// children that lost the most to rounding. A child floored up to 1 has no
	// remainder to claim: it already sits above its share.
	rem := make([]float64, k)
	used := 0
	for i := 0; i < k; i++ {
		exact := weights[i] / total * float64(space)
		s := int(exact)
		if s < 1 {
			s = 1
			rem[i] = 0
		} else {
			rem[i] = exact - float64(s)
		}
		out[i] = s
		used += s
	}
	for used < space {
		best := -1
		for i := range out {
			if rem[i] <= 0 {
				continue
			}
			if best < 0 || rem[i] > rem[best] {
				best = i
			}
		}
		if best < 0 {
			// Float drift only: give the cell to the largest count.
			for i := range out {
				if best < 0 || out[i] > out[best] {
					best = i
				}
			}
			out[best]++
			used++
			continue
		}
		out[best]++
		rem[best] = 0
		used++
	}
	// Reclaim the overshoot from the largest counts, so a small child keeps
	// its floor and no child grows past its share to cover the rounding.
	for used > space {
		best := -1
		for i := range out {
			if out[i] <= 1 {
				continue
			}
			if best < 0 || out[i] > out[best] {
				best = i
			}
		}
		if best < 0 {
			break
		}
		out[best]--
		used--
	}
	return out
}

// FindLeaf reports whether the tree holds the pane id.
func FindLeaf(n *Node, id string) bool {
	if n == nil {
		return false
	}
	if n.Pane == id {
		return true
	}
	for _, k := range n.Kids {
		if FindLeaf(k, id) {
			return true
		}
	}
	return false
}

// Perpendicular is the gap between the two rectangles' centres along the axis
// they must overlap on. It breaks ties between panes in the same column or row.
func Perpendicular(from, r Rect, dir string) int {
	switch dir {
	case "left", "right":
		d := (r.Y + r.H/2) - (from.Y + from.H/2)
		if d < 0 {
			return -d
		}
		return d
	default:
		d := (r.X + r.W/2) - (from.X + from.W/2)
		if d < 0 {
			return -d
		}
		return d
	}
}

// Neighbor returns the pane whose rectangle lies in dir from target.
func Neighbor(rects map[string]Rect, target, dir string) string {
	from, ok := rects[target]
	if !ok {
		return ""
	}
	best, bestScore := "", 1<<30
	var bestRect Rect
	for id, r := range rects {
		if id == target {
			continue
		}
		var primary int
		var overlap bool
		switch dir {
		case "left":
			primary = from.X - (r.X + r.W)
			overlap = r.Y < from.Y+from.H && from.Y < r.Y+r.H
		case "right":
			primary = r.X - (from.X + from.W)
			overlap = r.Y < from.Y+from.H && from.Y < r.Y+r.H
		case "up":
			primary = from.Y - (r.Y + r.H)
			overlap = r.X < from.X+from.W && from.X < r.X+r.W
		case "down":
			primary = r.Y - (from.Y + from.H)
			overlap = r.X < from.X+from.W && from.X < r.X+r.W
		}
		if primary < 0 || !overlap {
			continue
		}
		val := primary*10000 + Perpendicular(from, r, dir)
		if val < bestScore || (val == bestScore && TieLess(r, bestRect, dir)) {
			best, bestScore, bestRect = id, val, r
		}
	}
	return best
}

// TieLess breaks a neighbour tie between panes equidistant from the target.
// It picks the upper pane for a sideways move and the leftmost for a vertical
// one, so a target spanning a whole column or row resolves the same way every
// time instead of following map order.
func TieLess(a, b Rect, dir string) bool {
	if dir == "left" || dir == "right" {
		if a.Y != b.Y {
			return a.Y < b.Y
		}
		return a.X < b.X
	}
	if a.X != b.X {
		return a.X < b.X
	}
	return a.Y < b.Y
}
