package websession

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"dm/internal/layout"
	"dm/internal/paths"
	"dm/internal/session"
)

// sessionName is the session the fake plays, the name a real dm would write in
// its state file and use for its directory.
const sessionName = "spike"

// The kinds a file row can carry: a file a real session writes, a unix socket,
// or a file a real session has and this one does not.
const (
	kindFile   = "file"
	kindSocket = "socket"
	kindAbsent = "absent"
)

// The tags a row wears, in the panel's own words. A real tag means a real
// session writes that file; nothing the panel draws exists in the tab.
const (
	tagReal   = "real"
	tagMemory = "memory"
	tagSocket = "socket"
	tagAbsent = "absent"
)

// The one sentence each row carries: what the file is and whether this tab has
// it. The panel draws the note and holds no copy of its own.
const (
	noteState    = "A real session writes this file: the session's workspaces, windows, panes and counters."
	noteSnapshot = "A real session never writes this file: it is the last snapshot frame the client received."
	noteSocket   = "A unix socket has no contents; a real session reaches the pane's mux through it."
)

// absentFile is a file a real session writes and this one does not, with the
// sentence the panel shows for it.
type absentFile struct {
	path string
	note string
}

// absentFiles are the files a real session leaves directly under its session
// directory. This one has none of them: the fake has no server process, so it
// has no socket, no pid, no lock and no log.
var absentFiles = []absentFile{
	{"server.sock", "A real session writes this server socket; this tab has none."},
	{"server.pid", "A real session writes this file with its server's pid; this tab has none."},
	{"server.lock", "A real session writes this lock beside its server socket; this tab has none."},
	{"server.log", "A real session writes this server log; this tab has none."},
}

// paneLogNote and panePidNote are the sentences a pane's absent files carry.
const (
	paneLogNote = "A real session writes this pane's log beside its socket; this tab has none."
	panePidNote = "A real session writes this pane socket's pid beside it; this tab has none."
)

// file is one row of the session files panel: where it sits under the session
// directory, what it is, its tag, the sentence the viewer shows, and what it
// holds. Only a file has a body.
type file struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Tag  string `json:"tag"`
	Note string `json:"note"`
	Body string `json:"body"`
}

// dir is the session directory the panel prints: internal/paths' layout, with
// the home directory written as a tilde. The page holds no copy of this string,
// and neither does the producer: the layout lives in paths alone.
func (s *Session) dir() string {
	full := paths.SessionDirAt(paths.StateDir(), sessionName)
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		// A tab has no home directory to resolve against, so paths builds a
		// relative tree there and the panel prints it from the tilde down.
		if filepath.IsAbs(full) {
			return full
		}
		return "~/" + full
	}
	if rel, ok := strings.CutPrefix(full, home+string(filepath.Separator)); ok {
		return "~/" + rel
	}
	return full
}

// files is what the session would leave on disk, built from the live model. It
// is a view, not a store: nothing here is read from a file or written to one.
func (s *Session) files() []file {
	out := []file{
		{Path: "state.json", Kind: kindFile, Tag: tagReal, Note: noteState, Body: s.stateBody()},
		{Path: "snapshot.json", Kind: kindFile, Tag: tagMemory, Note: noteSnapshot, Body: s.snapshotBody()},
	}
	for _, id := range s.paneIDs() {
		out = append(out,
			file{Path: "panes/" + id + ".sock", Kind: kindSocket, Tag: tagSocket, Note: noteSocket},
			file{Path: "panes/" + id + ".log", Kind: kindAbsent, Tag: tagAbsent, Note: paneLogNote},
			file{Path: "panes/" + id + ".sock.pid", Kind: kindAbsent, Tag: tagAbsent, Note: panePidNote},
		)
	}
	for _, f := range absentFiles {
		out = append(out, file{Path: f.path, Kind: kindAbsent, Tag: tagAbsent, Note: f.note})
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
	// The rows are built once: every document in the view is marshalled once
	// per change, not once per read of the view.
	//
	// They are []any of maps, the only shapes syscall/js converts: a page that
	// draws them reads plain objects and nothing else.
	files := s.files()
	rows := make([]any, 0, len(files))
	for _, f := range files {
		rows = append(rows, map[string]any{
			"path": f.Path, "kind": f.Kind, "tag": f.Tag, "note": f.Note, "body": f.Body,
		})
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
		Name:   sessionName,
		Active: s.active,
		Cols:   s.cols,
		Rows:   s.rows,
		Panes:  map[string]*session.PaneState{},
		// A state file stores the last id handed out, not the next one: the
		// fake's counters are the next to hand out, so the document carries
		// them one back. A window's NextPane is already the last handed out,
		// so all three counters in this document mean one thing.
		NextWorkspace: s.nextWorkspace - 1,
		NextWindow:    s.nextWindow - 1,
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
