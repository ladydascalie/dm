package layoutdoc

import (
	"strconv"

	"dm/internal/layout"
	"dm/internal/session"
)

// BuildInto replaces st's workspaces and panes with the document's shape, in
// doc order.
//
// Pane ids inside a file are local to it, so every id is assigned from fresh
// counters: workspaces s1.. in doc order, windows w1.. across the whole doc in
// declaration order, and panes w<w>p<n> in tree order per window. Every root
// leaf and every focus is rewritten through that map, so a document can never
// collide with a live id and a second build over the same state leaves no old
// pane behind.
//
// st.Box, st.Secrets, st.Rail, st.ProjectFold and st.ProjectAccent are left
// untouched: a layout carries a shape, never a log, a secret or a width.
func BuildInto(st *session.State, d *Doc) error {
	if err := d.Validate(); err != nil {
		return err
	}
	// remap maps a doc-local pane id to the id it gets in st. Validate proves
	// every id is unique across the whole document, so one map serves it all.
	remap := make(map[string]string)
	workspaces := make([]*session.Workspace, len(d.Workspaces))
	windows := 0
	for i, ws := range d.Workspaces {
		sws := &session.Workspace{
			ID:       "s" + strconv.Itoa(i+1),
			Name:     ws.Name,
			Project:  ws.Project,
			Active:   ws.Active,
			Unfolded: true,
		}
		if sws.Active < 0 || sws.Active >= len(ws.Windows) {
			sws.Active = 0
		}
		sws.Windows = make([]*session.Window, len(ws.Windows))
		for j, w := range ws.Windows {
			windows++
			id := "w" + strconv.Itoa(windows)
			next := 0
			root := cloneTree(w.Root, func(local string) string {
				next++
				nid := id + "p" + strconv.Itoa(next)
				remap[local] = nid
				return nid
			})
			sws.Windows[j] = &session.Window{
				ID: id,
				// A doc always carries a window label, so the window shows it
				// instead of the w<N> the unnamed default renders.
				Name:     w.Name,
				Named:    true,
				Root:     root,
				Focus:    remap[w.Focus],
				NextPane: next,
			}
		}
		workspaces[i] = sws
	}
	st.Workspaces = workspaces
	st.Active = 0
	st.NextWorkspace = len(workspaces)
	st.NextWindow = windows
	st.Panes = make(map[string]*session.PaneState, len(remap))
	for local, nid := range remap {
		p := d.Panes[local]
		st.Panes[nid] = &session.PaneState{
			Cwd:  p.Cwd,
			Argv: append([]string(nil), p.Argv...),
		}
	}
	return nil
}

// cloneTree copies the tree, rewriting every leaf id through next in tree
// order, so pane ids come out left to right and top to bottom the way
// layout.Walk reads them. Validate has already refused a node that holds both
// a pane id and kids, so a leaf's kids are empty here.
func cloneTree(n *layout.Node, next func(string) string) *layout.Node {
	if n == nil {
		return nil
	}
	if n.Pane != "" {
		return &layout.Node{Pane: next(n.Pane)}
	}
	cp := &layout.Node{
		Dir:   n.Dir,
		Sizes: append([]float64(nil), n.Sizes...),
	}
	if len(n.Kids) > 0 {
		cp.Kids = make([]*layout.Node, len(n.Kids))
		for i, k := range n.Kids {
			cp.Kids[i] = cloneTree(k, next)
		}
	}
	return cp
}
