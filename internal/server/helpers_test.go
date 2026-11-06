package server

import (
	"dm/internal/layout"
	"dm/internal/session"
)

// railServer is an in-process server with no panes and no clients. Layout and
// snapshots do not need a spawned process.
func railServer(st *session.State) *server {
	return &server{
		st: st, panes: map[string]*paneConn{},
		clients: map[*client]bool{}, cols: st.Cols, rows: st.Rows,
	}
}

// rootPane gives a state a single focused pane in its first window.
func rootPane(st *session.State) string {
	w := st.ActiveWindow()
	id := w.NewPaneID()
	w.Root = &layout.Node{Pane: id}
	w.Focus = id
	st.Panes[id] = &session.PaneState{}
	return id
}
