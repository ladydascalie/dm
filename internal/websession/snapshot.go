package websession

import (
	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/wire"
)

// snapshot describes the session the way a server does: the active workspace's
// windows, the active window's tree and panes, and every pane in the session
// for the rail. A zoomed pane is drawn last, because the client paints in this
// order and the zoom has to cover the tiling under it.
func (s *Session) snapshot() *wire.Snapshot {
	ws := s.activeWorkspace()
	w := s.activeWindow()
	snap := &wire.Snapshot{
		Session:      sessionName,
		Windows:      []wire.WindowInfo{},
		ActiveWindow: 0,
		Cols:         s.cols,
		Rows:         s.rows,
		Pad:          keymap.LoadDefault().Padding(),
		Rail:         s.railWidth(),
		RailWant:     s.rail,
		Zoom:         s.zoom,
	}
	if ws != nil {
		snap.Workspace = ws.Name
		snap.ActiveWindow = ws.Active
		for _, win := range ws.Windows {
			snap.Windows = append(snap.Windows, wire.WindowInfo{
				ID: win.ID, Name: win.Name, Named: win.Named, Layout: win.Layout,
			})
		}
	}
	if w != nil {
		snap.Focus = w.Focus
		snap.Sync = w.Sync
		snap.Root = w.Root
		ids := layout.AllPanes(w.Root)
		ids = zoomLast(ids, s.zoom)
		r := s.windowRects(w)
		for _, id := range ids {
			rect, ok := r[id]
			if !ok {
				continue
			}
			snap.Panes = append(snap.Panes, wire.PaneInfo{ID: id, Rect: rect})
		}
	}
	snap.All = s.allPanes()
	return snap
}

// zoomLast moves the zoomed pane to the end of the draw order, where it covers
// the panes it is drawn over.
func zoomLast(ids []string, zoom string) []string {
	if zoom == "" {
		return ids
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != zoom {
			out = append(out, id)
		}
	}
	for _, id := range ids {
		if id == zoom {
			out = append(out, id)
		}
	}
	return out
}

// allPanes lists every pane in the session ordered by workspace, then window,
// then tree order. The rail reads the same picture the tabs do, so a workspace
// or window row and the pane rows under it always agree.
func (s *Session) allPanes() []wire.AllPane {
	out := []wire.AllPane{}
	for i, ws := range s.workspaces {
		wsActive := i == s.active
		for j, w := range ws.Windows {
			winActive := wsActive && j == ws.Active
			for _, id := range layout.AllPanes(w.Root) {
				info := wire.AllPane{
					ID: id, Workspace: ws.ID, WorkspaceName: ws.Name,
					Window: w.ID, WindowName: w.Name, WindowNamed: w.Named,
					WorkspaceActive: wsActive, WindowActive: winActive,
					WorkspaceUnfolded: ws.Unfolded, WindowUnfolded: w.Unfolded,
					Focus:  winActive && id == w.Focus,
					Status: "idle", Title: "sh",
				}
				if pn := s.panes[id]; pn != nil {
					info.Cwd = pn.sh.Cwd()
				}
				out = append(out, info)
			}
		}
	}
	return out
}

// push sends the picture: the snapshot, the session event that counts it, then
// the screen of every pane the client does not already hold at that rect. A
// snapshot drops every terminal outside the active window, so those entries
// leave sent with it and the next visit sends their screens again.
func (s *Session) push() {
	snap := s.snapshot()
	_ = wire.WriteMsg(s.conn, wire.Msg{Type: "snapshot", Snap: snap})
	// One session event per snapshot, for the page's coach. It is not a frame:
	// the wire the client reads is unchanged.
	s.lastSnap = snap
	s.reportSession()
	s.reportFiles()
	live := make(map[string]bool, len(snap.Panes))
	for _, p := range snap.Panes {
		live[p.ID] = true
	}
	for id := range s.sent {
		if !live[id] {
			delete(s.sent, id)
		}
	}
	for _, p := range snap.Panes {
		if rect, ok := s.sent[p.ID]; ok && rect == p.Rect {
			continue
		}
		pn := s.panes[p.ID]
		if pn == nil {
			continue
		}
		_ = wire.WriteMsg(s.conn, wire.Msg{Type: "screen", Target: p.ID, VTSnap: pn.term.Snapshot()})
		s.sent[p.ID] = p.Rect
	}
}
