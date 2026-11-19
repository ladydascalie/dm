package server

import (
	"errors"
	"log"
	"os"
	"path/filepath"

	"dm/internal/layout"
	"dm/internal/layoutdoc"
	"dm/internal/session"
)

// captureLayoutLocked projects the live state onto a layout document. The
// caller holds s.mu.
//
// Pane ids stay the live ids: layoutdoc.BuildInto renumbers them from the
// target session's own counters on the way back in, so the reply never has to
// invent an id scheme of its own. The document carries the shape and the launch
// data only: no box entry, no secret, no agent status and no rail width. Those
// belong to the session that had them, not to the layout.
func (s *server) captureLayoutLocked() *layoutdoc.Doc {
	doc := &layoutdoc.Doc{
		Version: layoutdoc.Version,
		Name:    s.st.Name,
		Panes:   map[string]layoutdoc.Pane{},
	}
	for _, ws := range s.st.Workspaces {
		dws := layoutdoc.Workspace{
			Name:    ws.Name,
			Project: ws.Project,
			Active:  ws.Active,
		}
		for _, w := range ws.Windows {
			dws.Windows = append(dws.Windows, layoutdoc.Window{
				Name:  w.Name,
				Focus: w.Focus,
				Root:  cloneTree(w.Root),
			})
			// Only panes the tree references travel: a state can carry a pane
			// no window holds, and a layout would have nothing to replay it at.
			layout.Walk(w.Root, func(id string) {
				p := layoutdoc.Pane{}
				if ps := s.st.Panes[id]; ps != nil {
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

// loadState loads a session's state, or seeds a fresh one from the layout file
// at layoutPath. fromDisk reports whether the state came from a state file.
//
// The layout seeds an empty session and never rearranges a live one: when a
// state file exists the flag is ignored and one line says so. A document that
// does not load is an error, so the caller exits before serving: a half-built
// session is worse than a failed start.
func loadState(root, name, layoutPath string) (st *session.State, fromDisk bool, err error) {
	st, err = session.LoadStateAt(root, name)
	if err == nil {
		fromDisk = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	if st != nil && fromDisk {
		if layoutPath != "" {
			log.Printf("layout: %s ignored, session %q keeps its state", layoutPath, name)
		}
		return st, true, nil
	}
	st = session.NewStateAt(root, name)
	if layoutPath == "" {
		return st, false, nil
	}
	doc, err := layoutdoc.Load(layoutPath)
	if err != nil {
		return nil, false, err
	}
	if err := layoutdoc.BuildInto(st, doc); err != nil {
		return nil, false, err
	}
	for _, ps := range st.Panes {
		paneDefaults(ps)
	}
	// A saved folder that is gone must not kill the session: land that pane in
	// the nearest surviving directory before anything spawns.
	fallbackCwd(st, homeDir())
	// Save before any pane spawns, so the first snapshot a client sees is the
	// whole layout and a crash mid-spawn leaves the shape, not a default one.
	if err := st.Save(); err != nil {
		return nil, false, err
	}
	return st, false, nil
}

// fallbackCwd replaces a pane cwd that is not a live directory with the nearest
// existing ancestor, or with home when nothing below the filesystem root
// survives. It logs one line per moved pane, naming the pane, the old cwd and
// the new one, and it leaves every pane whose cwd is fine alone. A pane with no
// cwd keeps it: the pane then runs where the server started, which is the
// honest answer when the environment has no home directory.
func fallbackCwd(st *session.State, home string) {
	for id, ps := range st.Panes {
		if ps.Cwd == "" || liveDir(ps.Cwd) {
			continue
		}
		old := ps.Cwd
		ps.Cwd = nearestCwd(old, home)
		log.Printf("layout: pane %s: cwd %q is gone, using %q", id, old, ps.Cwd)
	}
}

// homeDir is the operator's home directory, empty when the environment has
// none.
func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

// liveDir reports whether path is a directory that exists now.
func liveDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// nearestCwd is the first live ancestor of dir, or home when every ancestor
// below the filesystem root is gone. The root itself is not an answer: a pane
// dropped in / tells the operator nothing, so home is the better last resort.
// It returns "" when home is not a live directory, which leaves the pane to
// start where the server did rather than in an invented place.
func nearestCwd(dir, home string) string {
	for p := filepath.Dir(dir); ; p = filepath.Dir(p) {
		if p == filepath.Dir(p) {
			// The root of an absolute path, or the end of a relative walk.
			break
		}
		if liveDir(p) {
			return p
		}
	}
	if liveDir(home) {
		return home
	}
	return ""
}

// paneDefaults fills the pane fields the caller did not set: the shell and the
// initial terminal size. A layout carries the launch data, never a size, so a
// seeded pane would otherwise start as a 0x0 screen.
func paneDefaults(ps *session.PaneState) {
	if ps.Shell == "" {
		ps.Shell = os.Getenv("SHELL")
	}
	if ps.Cols == 0 {
		ps.Cols = 80
	}
	if ps.Rows == 0 {
		ps.Rows = 24
	}
}
