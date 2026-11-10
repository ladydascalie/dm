package server

import (
	"dm/internal/box"
)

// operatorSeesLocked reports whether a client is attached and looking at the
// pane. The guards keep a session that is being torn down from panicking on an
// empty layout. Callers hold s.mu.
func (s *server) operatorSeesLocked(id string) bool {
	if len(s.clients) == 0 || len(s.st.Workspaces) == 0 {
		return false
	}
	return s.st.ActiveWindow().Focus == id
}

// notifyPaneState posts a pane state change onto the pane's notify topic.
// watchPane calls it only for a transition into blocked, done or error, so the
// state is always a legal box state. The renderer hooked into boxPost owns
// the desktop notification and its suppression; this is the producer only.
func (s *server) notifyPaneState(id, state, message string) {
	// A failed save keeps the entry in memory and the renderer still pops; the
	// producer has no caller to tell.
	_, _ = s.boxPost(&box.PostReq{
		Key:   box.NotifyPrefix + id,
		State: box.State(state),
		Msg:   message,
	})
}
