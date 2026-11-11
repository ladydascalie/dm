package server

import (
	"dm/internal/session"
	"dm/internal/wire"
)

// railServer is an in-process server with no panes and no clients. Layout and
// snapshots do not need a spawned process.
func railServer(st *session.State) *server {
	return &server{
		st: st, panes: map[string]*paneConn{},
		clients: map[*client]bool{}, cols: st.Cols, rows: st.Rows,
		detect: detectGit,
	}
}

// snapshot takes the server's snapshot under the lock, so a background
// goroutine pushing its own snapshot cannot race the read.
func snapshot(s *server) *wire.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}
