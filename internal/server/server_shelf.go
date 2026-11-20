package server

// The shelf: panes set aside. A shelved pane keeps its id, its PaneState and
// its PTY, but it sits in no window's tree, so it has no rect and every walk
// that feeds geometry, reflow or the drawn pane list skips it. The client
// paints it from its own float, sized by the resize frame a float uses.

import (
	"fmt"

	"dm/internal/layout"
	"dm/internal/paths"
	"dm/internal/session"
	"dm/internal/wire"
)

// shelfActivePaneLocked is the shelf member the shelf shows, or "" for an
// empty shelf. The caller holds s.mu.
func (s *server) shelfActivePaneLocked() string {
	if len(s.st.Shelf) == 0 {
		return ""
	}
	i := s.st.ShelfActive
	if i < 0 || i >= len(s.st.Shelf) {
		i = 0
	}
	return s.st.Shelf[i]
}

// shelvePane takes a pane out of its window's tree and puts it on the shelf.
// The tree prune collapses the parent the pane left behind and returns the new
// root, so the window keeps its shape without a one-kid split. The last pane of
// a window refuses: a window with no pane is not a window, and the shelf is no
// place to keep one the session would drop on the next exit.
func (s *server) shelvePane(target string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if target == "" {
		target = s.st.ActiveWindow().Focus
	}
	var win *session.Window
	for _, ws := range s.st.Workspaces {
		for _, w := range ws.Windows {
			if layout.FindLeaf(w.Root, target) {
				win = w
			}
		}
	}
	if win == nil {
		if s.st.Shelved(target) {
			return "", fmt.Errorf("pane %q is already on the shelf", target)
		}
		return "", fmt.Errorf("no pane %q", target)
	}
	ids := layout.AllPanes(win.Root)
	if len(ids) <= 1 {
		return "", fmt.Errorf("cannot shelve %q: it is the last pane in window %s", target, win.ID)
	}
	at := 0
	for i, id := range ids {
		if id == target {
			at = i
		}
	}
	focused := win.Focus == target
	root, ok := layout.PruneLeaf(win.Root, target)
	if !ok {
		return "", fmt.Errorf("no pane %q", target)
	}
	win.Root = root
	win.Layout = ""
	rest := layout.AllPanes(root)
	if len(rest) == 0 {
		// The last-pane guard above makes this unreachable: a prune that took
		// the only pane would have been refused. A tree that somehow empties
		// anyway drops the window, so focus lands in the next one.
		s.dropEmptyWindows()
		s.st.AddShelf(target)
		return target, nil
	}
	// Focus only moves when the shelved pane held it: a pane set aside from
	// the side must not take the operator's focus with it. The survivor is the
	// pane that took the shelved one's place, else the last pane.
	if focused {
		if at >= len(rest) {
			at = len(rest) - 1
		}
		win.Focus = rest[at]
	}
	s.st.AddShelf(target)
	return target, nil
}

// popPane puts a shelf member back into the layout: the focused pane splits to
// the right, and the member takes that side and the focus. It is the split
// path's tree change (SplitLeaf) applied to a pane that already exists, so no
// PaneState is created and no PTY is spawned. A size change lands through
// syncSizes, the same path a float's summon uses.
func (s *server) popPane(target string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if target == "" {
		target = s.shelfActivePaneLocked()
	}
	if target == "" {
		return "", fmt.Errorf("the shelf is empty")
	}
	if !s.st.Shelved(target) {
		return "", fmt.Errorf("pane %q is not on the shelf", target)
	}
	win := s.st.ActiveWindow()
	if len(s.st.Workspaces) == 0 {
		// A session with a live shelf and no workspace has nowhere to pop into:
		// build the workspace and its window, the way new-window does, and let
		// the member fill it below.
		ws := &session.Workspace{ID: s.st.NewWorkspaceID(), Name: s.st.Name, Unfolded: true}
		win = &session.Window{ID: s.st.NewWindowID(), Unfolded: s.cfg.DefaultFold()}
		if ps := s.st.Panes[target]; ps != nil {
			win.Name = paths.FolderName(ps.Cwd)
		}
		ws.Windows = []*session.Window{win}
		s.st.Workspaces = []*session.Workspace{ws}
		s.st.Active = 0
	}
	if len(layout.AllPanes(win.Root)) == 0 {
		// An empty window has no pane to split: the member becomes its root,
		// which is the pane it would have split beside.
		win.Root = &layout.Node{Pane: target}
		win.Focus = target
		win.Layout = ""
		s.st.DropShelf(target)
		return target, nil
	}
	if !layout.FindLeaf(win.Root, win.Focus) {
		win.Focus = layout.AllPanes(win.Root)[0]
	}
	if !layout.SplitLeaf(win.Root, win.Focus, "right", target, 0) {
		return "", fmt.Errorf("could not split %q", win.Focus)
	}
	win.Layout = ""
	win.Focus = target
	s.st.DropShelf(target)
	return target, nil
}

// focusShelf moves the shelf's active member to the pane named, and reports
// whether that pane was a member at all. A shelved pane is in no tree, so there
// is no window focus to move: the shelf's own index is the whole answer.
func (s *server) focusShelf(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := s.st.ShelfIndex(id)
	if at < 0 {
		return false
	}
	s.st.ShelfActive = at
	_ = s.st.Save()
	return true
}

// resizeShelfPane gives a shelved pane the size the client draws it at. A
// shelved pane has no rect, so nothing else would size it: the shelf float
// sends the box it drew and this sets the PTY through the same clamp a float's
// resize uses. It reports whether the target was a shelf member.
func (s *server) resizeShelfPane(id string, cols, rows int) bool {
	s.mu.Lock()
	if !s.st.Shelved(id) {
		s.mu.Unlock()
		return false
	}
	if cols < 1 || rows < 1 {
		s.mu.Unlock()
		return true
	}
	if cols > maxWinCols {
		cols = maxWinCols
	}
	if rows > maxWinRows {
		rows = maxWinRows
	}
	if ps := s.st.Panes[id]; ps != nil {
		ps.Cols, ps.Rows = cols, rows
	}
	pc := s.panes[id]
	s.mu.Unlock()
	if pc != nil {
		if err := pc.send(wire.Msg{Action: "resize", Cols: cols, Rows: rows}); err != nil {
			s.rearmPaneWatcher(id)
		}
	}
	return true
}
