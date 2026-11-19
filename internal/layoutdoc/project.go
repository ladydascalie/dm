package layoutdoc

import (
	"dm/internal/layout"
	"dm/internal/session"
)

// Project lays a session's shape out as a document. It is the inverse of
// BuildInto, and the one projection both a live capture and a stopped
// session's state file go through, so the two cannot drift apart.
//
// Pane ids stay the source ids: BuildInto renumbers them from the target
// session's own counters on the way back in, so a document never has to invent
// an id scheme of its own. Only panes a window's tree references travel,
// because a state can carry a pane no window holds and a layout would have
// nothing to replay it at. The tree and the argv are copies, so the caller may
// marshal the document after it drops its own lock.
//
// The document carries the shape and the launch data only: no box entry, no
// secret, no agent status, no rail width, no fold and no accent. Those belong
// to the session that had them, not to the layout.
func Project(st *session.State) *Doc {
	doc := &Doc{
		Version: Version,
		Name:    st.Name,
		Panes:   map[string]Pane{},
	}
	for _, ws := range st.Workspaces {
		dws := Workspace{
			Name:    ws.Name,
			Project: ws.Project,
			Active:  ws.Active,
		}
		for _, w := range ws.Windows {
			dws.Windows = append(dws.Windows, Window{
				Name:  w.Name,
				Focus: w.Focus,
				Root:  cloneTree(w.Root, nil),
			})
			// Only panes the tree references travel: a state can carry a pane
			// no window holds, and a layout would have nothing to replay it at.
			layout.Walk(w.Root, func(id string) {
				p := Pane{}
				if ps := st.Panes[id]; ps != nil {
					p.Cwd = ps.Cwd
					p.Argv = append([]string(nil), ps.Argv...)
				}
				doc.Panes[id] = p
			})
		}
		doc.Workspaces = append(doc.Workspaces, dws)
	}
	return doc
}
