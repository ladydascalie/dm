package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

// Node is a pane leaf or a split. Dir "row" means children side by side.
// Sizes gives each child its share of the split, so a caller can keep the
// existing pane at a ratio instead of always halving. Absent means equal.
type Node struct {
	Pane  string    `json:"pane,omitempty"`
	Dir   string    `json:"dir,omitempty"`
	Kids  []*Node   `json:"kids,omitempty"`
	Sizes []float64 `json:"sizes,omitempty"`
}

type Window struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Root     *Node  `json:"root"`
	Focus    string `json:"focus"`
	NextPane int    `json:"nextPane,omitempty"`
}

type Workspace struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Windows []*Window `json:"windows"`
	Active  int       `json:"active"`
}

type PaneState struct {
	Shell     string `json:"shell"`
	Cwd       string `json:"cwd"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
	Restarted bool   `json:"restarted,omitempty"`
	// Name is the agent name recorded inside the mux. Agents survives a
	// restarted client because this is persisted, not held in memory.
	Name string `json:"name,omitempty"`
}

type State struct {
	Name       string                `json:"name"`
	Workspaces []*Workspace          `json:"workspaces"`
	Active     int                   `json:"active"`
	Cols       int                   `json:"cols"`
	Rows       int                   `json:"rows"`
	Panes      map[string]*PaneState `json:"panes"`

	// Counters for the addressable id scheme. A pane id is its window id plus
	// p and a per-window sequence: w1p1 is window 1, pane 1.
	NextWorkspace int `json:"nextWorkspace,omitempty"`
	NextWindow    int `json:"nextWindow,omitempty"`
	IDVersion     int `json:"idVersion,omitempty"`
}

func stateDir() string {
	if v := os.Getenv("DM_STATE"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, "dm")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "dm")
}

func sessionDir(name string) string { return filepath.Join(stateDir(), "sessions", name) }
func serverSock(name string) string { return filepath.Join(sessionDir(name), "server.sock") }
func paneSock(name, id string) string {
	return filepath.Join(sessionDir(name), "panes", id+".sock")
}
func statePath(name string) string { return filepath.Join(sessionDir(name), "state.json") }

func newState(name string) *State {
	st := &State{
		Name:          name,
		Cols:          80,
		Rows:          24,
		Panes:         map[string]*PaneState{},
		NextWorkspace: 1,
		NextWindow:    1,
		IDVersion:     2,
		Workspaces: []*Workspace{{
			ID:   "s1",
			Name: "main",
			Windows: []*Window{{
				ID:   "w1",
				Name: "1",
			}},
		}},
	}
	return st
}

func (st *State) save() error {
	if err := os.MkdirAll(sessionDir(st.Name), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := statePath(st.Name) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, statePath(st.Name))
}

func loadState(name string) (*State, error) {
	b, err := os.ReadFile(statePath(name))
	if err != nil {
		return nil, err
	}
	st := &State{}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, err
	}
	if st.Panes == nil {
		st.Panes = map[string]*PaneState{}
	}
	st.Name = name
	changed := false
	if st.IDVersion < 2 {
		st.migrateIDs()
		st.IDVersion = 2
		changed = true
	}
	if changed {
		_ = st.save()
	}
	return st, nil
}

func (st *State) ws() *Workspace {
	if st.Active < 0 || st.Active >= len(st.Workspaces) {
		st.Active = 0
	}
	return st.Workspaces[st.Active]
}

func (st *State) win() *Window {
	ws := st.ws()
	if ws.Active < 0 || ws.Active >= len(ws.Windows) {
		ws.Active = 0
	}
	return ws.Windows[ws.Active]
}

// Identifiers are addressable. A window is w<N>; a pane in it is w<N>p<M>,
// with M a per-window sequence that only grows. A workspace is s<N>. The id is
// therefore readable and constructible (w1p1), stable while the object lives,
// and never reused after a close because the counters only move forward.
func (st *State) newWorkspaceID() string {
	st.NextWorkspace++
	return "s" + strconv.Itoa(st.NextWorkspace)
}

func (st *State) newWindowID() string {
	st.NextWindow++
	return "w" + strconv.Itoa(st.NextWindow)
}

func (w *Window) newPaneID() string {
	w.NextPane++
	return w.ID + "p" + strconv.Itoa(w.NextPane)
}

// migrateIDs rewrites a state written before the addressable scheme. Panes are
// numbered in tree order per window; every focus and PaneState key follows.
func (st *State) migrateIDs() {
	remap := map[string]string{}
	nws, nw := 0, 0
	for _, ws := range st.Workspaces {
		nws++
		ws.ID = "s" + strconv.Itoa(nws)
		for _, w := range ws.Windows {
			nw++
			w.ID = "w" + strconv.Itoa(nw)
			n := 0
			var walk func(*Node)
			walk = func(nd *Node) {
				if nd == nil {
					return
				}
				if nd.Pane != "" {
					n++
					nid := w.ID + "p" + strconv.Itoa(n)
					remap[nd.Pane] = nid
					nd.Pane = nid
					return
				}
				for _, k := range nd.Kids {
					walk(k)
				}
			}
			walk(w.Root)
			w.NextPane = n
			if nid, ok := remap[w.Focus]; ok {
				w.Focus = nid
			}
		}
	}
	panes := make(map[string]*PaneState, len(st.Panes))
	for old, ps := range st.Panes {
		if nid, ok := remap[old]; ok {
			panes[nid] = ps
		} else {
			panes[old] = ps
		}
	}
	st.Panes = panes
	st.NextWorkspace, st.NextWindow = nws, nw
}

// walk calls f for every pane id in the tree.
func walk(n *Node, f func(string)) {
	if n == nil {
		return
	}
	if n.Pane != "" {
		f(n.Pane)
		return
	}
	for _, k := range n.Kids {
		walk(k, f)
	}
}

func allPanes(n *Node) []string {
	var out []string
	walk(n, func(id string) { out = append(out, id) })
	return out
}

// splitLeaf replaces the leaf holding target with a split, keeping z-order.
// ratio is the share the existing pane keeps; 0 or 1 falls back to half.
func splitLeaf(root *Node, target, dir, newID string, ratio float64) bool {
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
		if splitLeaf(k, target, dir, newID, ratio) {
			return true
		}
	}
	return false
}

// pruneLeaf removes a leaf and collapses its parent split.
func pruneLeaf(root *Node, target string) (*Node, bool) {
	if root == nil {
		return nil, false
	}
	if root.Pane == target {
		return nil, true
	}
	for i, k := range root.Kids {
		nk, removed := pruneLeaf(k, target)
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

// layout assigns a rectangle to every pane. Each split burns one column or row
// for the divider, so panes never share a cell.
func layout(n *Node, r Rect, out map[string]Rect) {
	if n == nil {
		return
	}
	if n.Pane != "" {
		out[n.Pane] = r
		return
	}
	k := len(n.Kids)
	if k == 0 {
		return
	}
	horizontal := n.Dir == "row"
	space := r.W
	if !horizontal {
		space = r.H
	}
	space -= k - 1
	if space < k {
		space = k
	}
	sizes := splitSizes(n, space)
	off := 0
	for i, kid := range n.Kids {
		size := sizes[i]
		if horizontal {
			layout(kid, Rect{X: r.X + off, Y: r.Y, W: size, H: r.H}, out)
		} else {
			layout(kid, Rect{X: r.X, Y: r.Y + off, W: r.W, H: size}, out)
		}
		off += size + 1
	}
}

// nodeRect returns the rectangle layout assigns to a node in the tree.
func nodeRect(n, target *Node, r Rect) (Rect, bool) {
	if n == nil {
		return Rect{}, false
	}
	if n == target {
		return r, true
	}
	if n.Pane != "" || len(n.Kids) == 0 {
		return Rect{}, false
	}
	k := len(n.Kids)
	horizontal := n.Dir == "row"
	space := r.W
	if !horizontal {
		space = r.H
	}
	space -= k - 1
	if space < k {
		space = k
	}
	sizes := splitSizes(n, space)
	off := 0
	for i, kid := range n.Kids {
		size := sizes[i]
		var kr Rect
		if horizontal {
			kr = Rect{X: r.X + off, Y: r.Y, W: size, H: r.H}
		} else {
			kr = Rect{X: r.X, Y: r.Y + off, W: r.W, H: size}
		}
		if out, ok := nodeRect(kid, target, kr); ok {
			return out, ok
		}
		off += size + 1
	}
	return Rect{}, false
}

// ancestorPath returns the nodes from root to id, inclusive.
func ancestorPath(n *Node, id string, path []*Node) []*Node {
	if n == nil {
		return nil
	}
	path = append(path, n)
	if n.Pane == id {
		return path
	}
	for _, k := range n.Kids {
		if p := ancestorPath(k, id, path); p != nil {
			return p
		}
	}
	return nil
}

// lowestCommonAncestor returns the deepest node on both paths.
func lowestCommonAncestor(a, b []*Node) *Node {
	var lca *Node
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			break
		}
		lca = a[i]
	}
	return lca
}

// splitSizes turns the node's weights into integer cell counts that sum to
// space. A split without weights divides equally.
func splitSizes(n *Node, space int) []int {
	k := len(n.Kids)
	out := make([]int, k)
	if k == 0 {
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
	used := 0
	for i := 0; i < k-1; i++ {
		s := int(weights[i] / total * float64(space))
		if s < 1 {
			s = 1
		}
		out[i] = s
		used += s
	}
	out[k-1] = space - used
	if out[k-1] < 1 {
		out[k-1] = 1
	}
	return out
}

func findLeaf(n *Node, id string) bool {
	if n == nil {
		return false
	}
	if n.Pane == id {
		return true
	}
	for _, k := range n.Kids {
		if findLeaf(k, id) {
			return true
		}
	}
	return false
}

// perpendicular is the gap between the two rectangles' centres along the axis
// they must overlap on. It breaks ties between panes in the same column or row.
func perpendicular(from, r Rect, dir string) int {
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

// neighbor returns the pane whose rectangle lies in dir from target.
func neighbor(rects map[string]Rect, target, dir string) string {
	from, ok := rects[target]
	if !ok {
		return ""
	}
	best, bestScore := "", 1<<30
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
		val := primary*10000 + perpendicular(from, r, dir)
		if val < bestScore {
			best, bestScore = id, val
		}
	}
	return best
}
