package websession

import (
	"encoding/json"

	"dm/internal/layout"
	"dm/internal/session"
)

// sessionName is the session the fake plays, the name a real dm would write in
// its state file and use for its directory.
const sessionName = "spike"

// stateRoot is the state tree a real dm writes under, as the panel prints it.
// The directory comes from internal/paths on a machine; the panel prints the
// short form, because a browser has no home directory to resolve it against and
// the page must not show this host's paths.
const stateRoot = "~/.local/state/dm"

// The kinds a file row can carry: a file a real session writes, a unix socket,
// or a file a real session has and this one does not.
const (
	kindFile   = "file"
	kindSocket = "socket"
	kindAbsent = "absent"
)

// absentFiles are the files a real session leaves under its directory that this
// one has none of. They carry no body: the fake has no server process, so it
// has no socket, no pid and no log.
var absentFiles = []string{"server.sock", "server.pid", "server.log"}

// file is one row of the session files panel: where it sits under the session
// directory, what it is, and what it holds. Only a file has a body.
type file struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Body string `json:"body"`
}

// dir is the session directory the panel prints.
func (s *Session) dir() string { return stateRoot + "/sessions/" + sessionName }

// files is what the session would leave on disk, built from the live model. It
// is a view, not a store: nothing here is read from a file or written to one.
func (s *Session) files() []file {
	out := []file{
		{Path: "state.json", Kind: kindFile, Body: s.stateBody()},
		{Path: "snapshot.json", Kind: kindFile, Body: s.snapshotBody()},
	}
	for _, id := range s.paneIDs() {
		out = append(out, file{Path: "panes/" + id + ".sock", Kind: kindSocket})
	}
	for _, name := range absentFiles {
		out = append(out, file{Path: name, Kind: kindAbsent})
	}
	return out
}

// reportFiles pushes one session files event: the directory and the rows the
// panel draws. It rides with every snapshot, after the session event, so the
// panel is drawn from the same model the screen was.
func (s *Session) reportFiles() {
	if s.forward == nil {
		return
	}
	// The rows are []any of maps, the only shapes syscall/js converts: a page
	// that draws them reads plain objects and nothing else.
	rows := make([]any, 0, len(s.files()))
	for _, f := range s.files() {
		rows = append(rows, map[string]any{"path": f.Path, "kind": f.Kind, "body": f.Body})
	}
	s.forward(map[string]any{"type": "session.files", "dir": s.dir(), "files": rows})
}

// paneIDs is every pane in the session, ordered by workspace, then window, then
// tree order, which is the order the rail lists them.
func (s *Session) paneIDs() []string {
	out := []string{}
	for _, ws := range s.workspaces {
		for _, w := range ws.Windows {
			out = append(out, layout.AllPanes(w.Root)...)
		}
	}
	return out
}

// stateBody is the state document a real session would write: the shipped
// internal/session types, marshalled the way State.Save marshals them, so the
// panel cannot drift from the file.
func (s *Session) stateBody() string {
	b, err := json.MarshalIndent(s.stateDocument(), "", "  ")
	if err != nil {
		// A session.State holds no value json refuses. A body that cannot be
		// marshalled is not a session file, and the panel shows none.
		return ""
	}
	return string(b)
}

// stateDocument builds the state from the fake's model: every workspace, every
// window of every workspace, and a pane state for every pane.
func (s *Session) stateDocument() *session.State {
	st := &session.State{
		Name:          sessionName,
		Active:        s.active,
		Cols:          s.cols,
		Rows:          s.rows,
		Panes:         map[string]*session.PaneState{},
		NextWorkspace: s.nextWorkspace,
		NextWindow:    s.nextWindow,
		// The fake addresses panes the way a state file written by this dm
		// version does.
		IDVersion: 2,
		// Rail is what the page asked for, as a real state records the
		// requested width rather than the width the screen could spare.
		Rail: s.rail, RailSet: true,
	}
	for _, ws := range s.workspaces {
		out := &session.Workspace{
			ID: ws.ID, Name: ws.Name,
			Active: ws.Active, Unfolded: ws.Unfolded,
		}
		for _, w := range ws.Windows {
			out.Windows = append(out.Windows, &session.Window{
				ID: w.ID, Name: w.Name, Named: w.Named,
				Root: w.Root, Focus: w.Focus, NextPane: w.NextPane,
				Unfolded: w.Unfolded, Sync: w.Sync, Layout: w.Layout,
			})
			for _, id := range layout.AllPanes(w.Root) {
				st.Panes[id] = s.paneState(id)
			}
		}
		st.Workspaces = append(st.Workspaces, out)
	}
	return st
}

// paneState is one pane as a state file records it: the shell it runs, the
// directory it stands in, and the size of its terminal. A pane the last fit has
// not built yet has no cwd and no size.
func (s *Session) paneState(id string) *session.PaneState {
	ps := &session.PaneState{Shell: "/bin/sh"}
	pn := s.panes[id]
	if pn == nil {
		return ps
	}
	ps.Cwd = pn.sh.Cwd()
	if g := pn.term.Grid(); g != nil {
		ps.Cols, ps.Rows = g.Size()
	}
	return ps
}

// snapshotBody is the last snapshot push sent, indented for reading. It is not
// a file a real session writes: it is one frame of the wire, and the panel says
// so.
func (s *Session) snapshotBody() string {
	if s.lastSnap == nil {
		return ""
	}
	b, err := json.MarshalIndent(s.lastSnap, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}
