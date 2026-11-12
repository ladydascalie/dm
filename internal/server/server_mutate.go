package server

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"dm/internal/layout"
	"dm/internal/paths"
	"dm/internal/session"
	"dm/internal/vt"
	"dm/internal/wire"
)

// toggleFold flips a workspace's or window's rail fold state and persists it.
func (s *server) toggleFold(id string) (bool, error) {
	if name, ok := strings.CutPrefix(id, wire.ProjectTargetPrefix); ok {
		return s.toggleProjectFold(name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ws := range s.st.Workspaces {
		if ws.ID == id {
			ws.Unfolded = !ws.Unfolded
			_ = s.st.Save()
			return ws.Unfolded, nil
		}
		for _, w := range ws.Windows {
			if w.ID != id {
				continue
			}
			w.Unfolded = !w.Unfolded
			_ = s.st.Save()
			return w.Unfolded, nil
		}
	}
	return false, fmt.Errorf("no window or workspace %q", id)
}

// toggleProjectFold flips a project's rail fold state and persists it. A
// project is a name, so the state is the set of folded names.
func (s *server) toggleProjectFold(name string) (bool, error) {
	if name == "" {
		return false, fmt.Errorf("no project %q", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.ProjectFold == nil {
		s.st.ProjectFold = map[string]bool{}
	}
	folded := !s.st.ProjectFold[name]
	if folded {
		s.st.ProjectFold[name] = true
	} else {
		delete(s.st.ProjectFold, name)
	}
	if err := s.st.Save(); err != nil {
		return false, err
	}
	return !folded, nil
}

// paneInTreeLocked reports whether id is a pane in this session's layout, in
// any window rather than only the active one. The caller holds s.mu.
func (s *server) paneInTreeLocked(id string) bool {
	for _, ws := range s.st.Workspaces {
		for _, w := range ws.Windows {
			if layout.FindLeaf(w.Root, id) {
				return true
			}
		}
	}
	return false
}

func (s *server) statuses() map[string]wire.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]wire.Status{}
	for id, pc := range s.panes {
		out[id] = pc.st
	}
	return out
}

// paneLines reads the pane VT through a short-lived socket connection.
func (s *server) paneLines(id string) ([]string, wire.Status, error) {
	m, err := paneRequest(s.st, id, wire.Msg{Type: "req", Action: "read"})
	if err != nil {
		return nil, wire.Status{}, err
	}
	if !m.OK {
		return nil, wire.Status{}, fmt.Errorf("%s", m.Error)
	}
	st := wire.Status{}
	if m.St != nil {
		st = *m.St
	}
	if m.Lines != nil {
		return m.Lines, st, nil
	}
	// A pane started by the short-lived build that moved rows into Res.
	var res struct {
		Lines []string `json:"lines"`
	}
	_ = json.Unmarshal(m.Res, &res)
	return res.Lines, st, nil
}

// paneHistory replays the pane's ring into a tall terminal and returns the
// rows immediately above the live screen, oldest first.
func (s *server) paneHistory(id string, n int) ([]string, error) {
	if n <= 0 {
		return nil, fmt.Errorf("history needs lines > 0")
	}
	// The tall replay must use the pane's own PTY size, not its layout rect:
	// a pane in an inactive workspace keeps its 80x24 PTY until it is shown.
	s.mu.Lock()
	ps := s.st.Panes[id]
	s.mu.Unlock()
	if ps == nil || ps.Cols <= 0 || ps.Rows <= 0 {
		return nil, fmt.Errorf("no pane %q", id)
	}
	r := layout.Rect{W: ps.Cols, H: ps.Rows}
	rp := readPaneReplay(s.st, id)
	if !rp.ok {
		return nil, fmt.Errorf("no pane %q", id)
	}
	if rp.alt {
		return nil, fmt.Errorf("pane %q is on the alternate screen", id)
	}
	rows := vt.ReplayRows(rp.data, r.W, r.H, n+r.H)
	if len(rows) == 0 {
		return []string{}, nil
	}
	end := len(rows) - r.H
	if end < 0 {
		end = 0
	}
	// Leading blank rows are not reachable, like the client's scrollStart. A
	// history that holds nothing but blanks keeps them: those rows sit
	// directly above the live screen and are a real window, not padding above
	// the oldest content.
	top := 0
	for top < end && vt.BlankRow(rows[top]) {
		top++
	}
	if top == end {
		top = 0
	}
	start := end - n
	if start < top {
		start = top
	}
	if start > end {
		start = end
	}
	out := make([]string, 0, end-start)
	for y := start; y < end; y++ {
		out = append(out, vt.PlainRow(rows[y]))
	}
	return out, nil
}

// paneYank asks a pane for the replay rows of its last Enter, up to the prompt
// when the shell marks one. The reply carries every replayed row, the first row
// with content and the total row count: the client cuts lines at start, drops
// trailing blank rows and owns the clipboard. The anchor flag is false when the
// pane has never been sent an Enter.
func (s *server) paneYank(id string) ([]string, int, int, bool, error) {
	m, err := paneRequest(s.st, id, wire.Msg{Type: "req", Action: "yank"})
	if err != nil {
		return nil, 0, 0, false, err
	}
	if !m.OK {
		return nil, 0, 0, false, fmt.Errorf("%s", m.Error)
	}
	var res struct {
		Lines  []string `json:"lines"`
		Start  int      `json:"start"`
		Rows   int      `json:"rows"`
		Anchor bool     `json:"anchor"`
	}
	if len(m.Res) > 0 {
		if err := json.Unmarshal(m.Res, &res); err != nil {
			return nil, 0, 0, false, err
		}
	}
	return res.Lines, res.Start, res.Rows, res.Anchor, nil
}

func (s *server) writeTo(id, text string, enter bool) error {
	s.mu.Lock()
	pc := s.panes[id]
	known := s.paneInTreeLocked(id)
	s.mu.Unlock()
	if !known {
		return fmt.Errorf("no pane %q", id)
	}
	if pc == nil {
		// A pane can exist in the layout but not be adopted yet.
		if err := s.ensurePane(id); err != nil {
			return err
		}
		s.watchPane(id)
		s.mu.Lock()
		pc = s.panes[id]
		s.mu.Unlock()
	}
	if pc == nil {
		return fmt.Errorf("no pane %q", id)
	}
	s.mu.Lock()
	dsts := s.syncPaneIDsLocked(id)
	s.mu.Unlock()
	if len(dsts) == 0 {
		return pc.send(wire.Msg{Action: "write", Msg: text, Enter: enter})
	}
	for _, pid := range dsts {
		if p := s.panes[pid]; p != nil {
			_ = p.send(wire.Msg{Action: "write", Msg: text, Enter: enter})
		}
	}
	return nil
}

// syncPaneIDsLocked returns the pane ids a synced write must reach: nil when
// id's window is not synced, otherwise every pane of that window in tree
// order. The caller holds s.mu.
func (s *server) syncPaneIDsLocked(id string) []string {
	for _, ws := range s.st.Workspaces {
		for _, win := range ws.Windows {
			if !layout.FindLeaf(win.Root, id) {
				continue
			}
			if !win.Sync {
				return nil
			}
			return layout.AllPanes(win.Root)
		}
	}
	return nil
}

// sendKeys delivers a keys frame to the pane named by id, or to every live
// pane of its window, in tree order, when that window is synced. The target
// pane must be live, exactly as the single-pane path requires.
func (s *server) sendKeys(id string, keys []byte) error {
	s.mu.Lock()
	pc := s.panes[id]
	dsts := s.syncPaneIDsLocked(id)
	s.mu.Unlock()
	if pc == nil {
		return fmt.Errorf("no pane %q", id)
	}
	if len(dsts) == 0 {
		return pc.send(wire.Msg{Action: "write", Keys: keys})
	}
	for _, pid := range dsts {
		if p := s.panes[pid]; p != nil {
			_ = p.send(wire.Msg{Action: "write", Keys: keys})
		}
	}
	return nil
}

// synchronizePanes toggles the active window's sync flag, or sets it when on
// names a value. It returns the new value. Sync makes every write to a pane
// of that window reach every live pane of it; other windows are untouched.
func (s *server) synchronizePanes(on *bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	win := s.st.ActiveWindow()
	if on == nil {
		win.Sync = !win.Sync
	} else {
		win.Sync = *on
	}
	return win.Sync, nil
}

// mutation runs fn and saves state. fn takes the lock itself, because it may
// need to spawn a process, which must not happen under the lock.
func (s *server) mutation(fn func() error) error {
	if err := fn(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Save()
}

// afterMutation clears the zoom, then resizes panes, broadcasts the new layout
// and re-sends replays. A focus, split, close, window or workspace change is a
// new layout, so the zoom ends here once for all of them.
func (s *server) afterMutation() {
	s.clearZoom()
	s.resync()
}

// resync resizes panes, broadcasts the new layout and re-sends the screens and
// replays. A client rebuilds its VT when the layout changes.
func (s *server) resync() {
	s.syncSizes()
	s.mu.Lock()
	snap := s.snapshotLocked()
	s.broadcastLocked(wire.Msg{Type: "snapshot", Snap: snap})
	s.mu.Unlock()
	// Clients rebuild their VT after a layout change; send the screen, then
	// replay the rings as history.
	s.mu.Lock()
	ids := layout.AllPanes(s.st.ActiveWindow().Root)
	s.mu.Unlock()
	s.broadcastScreens(ids)
	for _, id := range ids {
		rp := readPaneReplay(s.st, id)
		if !rp.ok {
			continue
		}
		s.mu.Lock()
		if pc := s.panes[id]; pc != nil {
			pc.alt, pc.mouse = rp.alt, rp.mouse
		}
		s.broadcastLocked(wire.Msg{Type: "replay", Target: id, Data: rp.data, St: &rp.st})
		s.mu.Unlock()
	}
}

type paneListInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	App           string `json:"app,omitempty"`
	Status        string `json:"status,omitempty"`
	Message       string `json:"message,omitempty"`
	Workspace     string `json:"workspace"`
	WorkspaceName string `json:"workspaceName"`
	Window        string `json:"window"`
}

// paneList reports every pane in the session, including inactive workspaces
// and windows. Agents must see the whole fleet, not just the visible window.
func (s *server) paneList() []paneListInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []paneListInfo
	for _, ws := range s.st.Workspaces {
		for _, w := range ws.Windows {
			for _, id := range layout.AllPanes(w.Root) {
				info := paneListInfo{ID: id, Workspace: ws.ID, WorkspaceName: ws.Name, Window: w.ID}
				if ps := s.st.Panes[id]; ps != nil {
					info.Name = ps.Name
					info.App = ps.App
				}
				if pc := s.panes[id]; pc != nil {
					info.Status = pc.st.State
					info.Message = pc.st.Message
				}
				out = append(out, info)
			}
		}
	}
	return out
}

type winInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

type wsInfo struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Project string    `json:"project,omitempty"`
	Active  bool      `json:"active"`
	Windows []winInfo `json:"windows"`
}

func (s *server) workspaceList() []wsInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]wsInfo, 0, len(s.st.Workspaces))
	for i, ws := range s.st.Workspaces {
		info := wsInfo{ID: ws.ID, Name: ws.Name, Project: ws.Project, Active: i == s.st.Active}
		for j, w := range ws.Windows {
			info.Windows = append(info.Windows, winInfo{
				ID: w.ID, Name: w.Name,
				Active: i == s.st.Active && j == ws.Active,
			})
		}
		out = append(out, info)
	}
	return out
}

// findPane locates a pane anywhere in the session. Panes outside the active
// window are still addressable, which is what a background layout needs.
func (s *server) findPane(id string) (*session.Workspace, *session.Window, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ws := range s.st.Workspaces {
		for _, w := range ws.Windows {
			if layout.FindLeaf(w.Root, id) {
				return ws, w, true
			}
		}
	}
	return nil, nil, false
}

func (s *server) focusWindow(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, ws := range s.st.Workspaces {
		for j, w := range ws.Windows {
			if w.ID != id {
				continue
			}
			s.st.Active = i
			ws.Active = j
			if !layout.FindLeaf(w.Root, w.Focus) {
				if ids := layout.AllPanes(w.Root); len(ids) > 0 {
					w.Focus = ids[0]
				}
			}
			return nil
		}
	}
	return fmt.Errorf("no window %q", id)
}

func (s *server) focusWorkspace(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, ws := range s.st.Workspaces {
		if ws.ID == id {
			s.st.Active = i
			return nil
		}
	}
	return fmt.Errorf("no workspace %q", id)
}

// renameWorkspace sets the workspace name and nothing else: the id, the active
// index, the windows and the panes stay. A background workspace is fair game.
func (s *server) renameWorkspace(id, name string) error {
	if name == "" {
		return fmt.Errorf("name required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ws := range s.st.Workspaces {
		if ws.ID == id {
			ws.Name = name
			return nil
		}
	}
	return fmt.Errorf("no workspace %q", id)
}

// setProject tags the workspace with a project name and moves it to the end of
// that project's block. An empty name clears the project. The id, the windows
// and the panes stay; the active workspace stays the same workspace.
func (s *server) setProject(id, project string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, ws := range s.st.Workspaces {
		if ws.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("no workspace %q", id)
	}
	activeID := ""
	if aws := s.st.ActiveWorkspace(); aws != nil {
		activeID = aws.ID
	}
	ws := s.st.Workspaces[idx]
	ws.Project = project
	s.st.Workspaces = append(s.st.Workspaces[:idx], s.st.Workspaces[idx+1:]...)
	s.insertWorkspaceLocked(ws)
	for i, w := range s.st.Workspaces {
		if w.ID == activeID {
			s.st.Active = i
			break
		}
	}
	return nil
}

// insertWorkspaceLocked puts ws at the end of its project's block and regroups
// the slice. An untagged workspace goes to the end of the slice, which is where
// GroupWorkspaces keeps it. The caller holds s.mu.
func (s *server) insertWorkspaceLocked(ws *session.Workspace) {
	at := len(s.st.Workspaces)
	if ws.Project != "" {
		for i, w := range s.st.Workspaces {
			if w.Project == ws.Project {
				at = i + 1
			}
		}
	}
	s.st.Workspaces = append(s.st.Workspaces, nil)
	copy(s.st.Workspaces[at+1:], s.st.Workspaces[at:])
	s.st.Workspaces[at] = ws
	s.st.GroupWorkspaces()
}

// renameWindow sets the named window's name and nothing else: the id, the
// active window, the focus and the panes stay. A background window is fair
// game.
func (s *server) renameWindow(id, name string) error {
	if name == "" {
		return fmt.Errorf("name required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ws := range s.st.Workspaces {
		for _, w := range ws.Windows {
			if w.ID == id {
				w.Name = name
				w.Named = true
				return nil
			}
		}
	}
	return fmt.Errorf("no window %q", id)
}

// removeWorkspaceLocked drops the workspace at idx and kills every pane under
// it. The caller holds s.mu and leaves at least one workspace behind, so the
// active index can be clamped to a real workspace.
func (s *server) removeWorkspaceLocked(idx int) {
	ws := s.st.Workspaces[idx]
	var ids []string
	for _, w := range ws.Windows {
		ids = append(ids, layout.AllPanes(w.Root)...)
	}
	s.st.Workspaces = append(s.st.Workspaces[:idx], s.st.Workspaces[idx+1:]...)
	if s.st.Active >= len(s.st.Workspaces) {
		s.st.Active = len(s.st.Workspaces) - 1
	}
	for _, pid := range ids {
		if pc := s.panes[pid]; pc != nil {
			_ = pc.send(wire.Msg{Action: "kill"})
			_ = pc.c.Close()
			delete(s.panes, pid)
		}
		delete(s.st.Panes, pid)
	}
}

func (s *server) closeWorkspace(id string) error {
	s.mu.Lock()
	idx := -1
	for i, ws := range s.st.Workspaces {
		if ws.ID == id {
			idx = i
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		return fmt.Errorf("no workspace %q", id)
	}
	if len(s.st.Workspaces) == 1 {
		s.mu.Unlock()
		return fmt.Errorf("cannot close the last workspace")
	}
	s.removeWorkspaceLocked(idx)
	s.mu.Unlock()
	return nil
}

// closeWindow removes a window and kills its panes. A window that is the only
// one in its workspace takes the workspace with it. It refuses only the last
// window of the last workspace, so a close never ends the session. Any window
// left behind stays active with its active index on a real window.
func (s *server) closeWindow(id string) error {
	s.mu.Lock()
	var ws *session.Workspace
	idx, wsIdx := -1, -1
	for i, w := range s.st.Workspaces {
		for j, win := range w.Windows {
			if win.ID == id {
				ws, idx, wsIdx = w, j, i
			}
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		return fmt.Errorf("no window %q", id)
	}
	if len(ws.Windows) == 1 {
		if len(s.st.Workspaces) == 1 {
			s.mu.Unlock()
			return fmt.Errorf("cannot close the last window")
		}
		s.removeWorkspaceLocked(wsIdx)
		s.mu.Unlock()
		return nil
	}
	win := ws.Windows[idx]
	ids := layout.AllPanes(win.Root)
	ws.Windows = append(ws.Windows[:idx], ws.Windows[idx+1:]...)
	switch {
	case idx < ws.Active:
		ws.Active--
	case idx == ws.Active:
		ws.Active = len(ws.Windows) - 1
	}
	if ws.Active >= len(ws.Windows) {
		ws.Active = len(ws.Windows) - 1
	}
	for _, pid := range ids {
		if pc := s.panes[pid]; pc != nil {
			_ = pc.send(wire.Msg{Action: "kill"})
			_ = pc.c.Close()
			delete(s.panes, pid)
		}
		delete(s.st.Panes, pid)
	}
	s.mu.Unlock()
	return nil
}

// swapPanes exchanges the ids of two leaves of the same tree. The panes trade
// ids in place, so every rectangle keeps its shape and both ids stay leaves.
// Focus is untouched: the focused pane's id survives the swap, so it keeps
// focus wherever the swap leaves it.
func (s *server) swapPanes(a, b string) error {
	if a == "" || b == "" || a == b {
		return fmt.Errorf("swap-panes needs two panes")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ws := range s.st.Workspaces {
		for _, win := range ws.Windows {
			if !layout.FindLeaf(win.Root, a) || !layout.FindLeaf(win.Root, b) {
				continue
			}
			if !layout.Swap(win.Root, a, b) {
				return fmt.Errorf("no pane %q or %q", a, b)
			}
			return nil
		}
	}
	return fmt.Errorf("no pane %q", a)
}

// movePane moves the leaf pane id to the edge of target in dir. Both ids must
// be leaves of the same window; the moved pane takes the focus.
func (s *server) movePane(id, target, dir string) error {
	switch dir {
	case "left", "right", "up", "down":
	default:
		return fmt.Errorf("dir must be left, right, up or down")
	}
	if id == "" || target == "" {
		return fmt.Errorf("move-pane needs a pane and a target")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ws := range s.st.Workspaces {
		for _, win := range ws.Windows {
			if !layout.FindLeaf(win.Root, id) || !layout.FindLeaf(win.Root, target) {
				continue
			}
			if !layout.Move(win.Root, id, target, dir) {
				return fmt.Errorf("no pane %q", id)
			}
			win.Focus = id
			return nil
		}
	}
	return fmt.Errorf("no pane %q", id)
}

func (s *server) split(target, dir, cwd string, ratio float64, noFocus bool) (string, error) {
	// A split opens in the folder its source pane stands in now. Ask before
	// taking the lock, for the same reason a window asks.
	if cwd == "" {
		cwd = s.inheritedCwd(target)
	}
	switch dir {
	case "left", "right", "up", "down":
	default:
		return "", fmt.Errorf("dir must be left, right, up or down")
	}
	s.mu.Lock()
	if target == "" {
		target = s.st.ActiveWindow().Focus
	}
	var win *session.Window
	var ws *session.Workspace
	for _, cand := range s.st.Workspaces {
		for _, w := range cand.Windows {
			if layout.FindLeaf(w.Root, target) {
				ws, win = cand, w
			}
		}
	}
	if win == nil {
		s.mu.Unlock()
		return "", fmt.Errorf("no pane %q", target)
	}
	id := win.NewPaneID()
	layout.SplitLeaf(win.Root, target, dir, id, ratio)
	s.addPaneStateAt(id, cwd, "")
	if !noFocus {
		for i, cand := range s.st.Workspaces {
			if cand == ws {
				s.st.Active = i
			}
		}
		for i, cand := range ws.Windows {
			if cand == win {
				ws.Active = i
			}
		}
		win.Focus = id
	}
	s.mu.Unlock()

	if err := s.ensurePane(id); err != nil {
		return id, err
	}
	s.watchPane(id)
	return id, nil
}

// resizeSplit sets the weights of the split between two adjacent panes. Ratio
// is the share the first child keeps, clamped so both sides keep three cells.
func (s *server) resizeSplit(first, second string, ratio float64) error {
	if math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return fmt.Errorf("bad ratio")
	}
	if first == "" || second == "" || first == second {
		return fmt.Errorf("resize-split needs two panes")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ws := range s.st.Workspaces {
		for _, win := range ws.Windows {
			if !layout.FindLeaf(win.Root, first) || !layout.FindLeaf(win.Root, second) {
				continue
			}
			pa := layout.AncestorPath(win.Root, first, nil)
			pb := layout.AncestorPath(win.Root, second, nil)
			split := layout.LowestCommonAncestor(pa, pb)
			if split == nil || split.Pane != "" {
				return fmt.Errorf("panes %q and %q share no split", first, second)
			}
			if len(split.Kids) != 2 {
				return fmt.Errorf("split has %d panes, want 2", len(split.Kids))
			}
			r, ok := layout.NodeRect(win.Root, split,
				layout.PaneAreaForWithTabBar(s.cols, s.rows, s.railLocked()), s.cfg.Padding())
			if !ok {
				return fmt.Errorf("no rect for split of %q", first)
			}
			horizontal := split.Dir == "row"
			space := r.W
			if !horizontal {
				space = r.H
			}
			// A pane box is border + padding + content + padding + border.
			// The smallest content a drag may leave is 3 cells, so padding
			// does not shrink the floor a pane can be dragged to.
			minBox := 5 + 2*s.cfg.Padding()
			if space < 2 {
				return fmt.Errorf("split between %q and %q is too small", first, second)
			}
			if space < 2*minBox {
				split.Sizes = []float64{0.5, 0.5}
				return nil
			}
			const eps = 1e-9
			lo := float64(minBox)/float64(space) + eps
			hi := 1 - float64(minBox)/float64(space) + eps
			if ratio < lo {
				ratio = lo
			}
			if ratio > hi {
				ratio = hi
			}
			split.Sizes = []float64{ratio, 1 - ratio}
			return nil
		}
	}
	return fmt.Errorf("no pane %q", first)
}

func (s *server) setFocus(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, ws := range s.st.Workspaces {
		for j, w := range ws.Windows {
			if layout.FindLeaf(w.Root, id) {
				s.st.Active = i
				ws.Active = j
				w.Focus = id
				return nil
			}
		}
	}
	return fmt.Errorf("no pane %q", id)
}

func (s *server) focusDir(dir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	win := s.st.ActiveWindow()
	rects := map[string]layout.Rect{}
	layout.Layout(win.Root, layout.PaneAreaForWithTabBar(s.cols, s.rows, s.railLocked()), s.cfg.Padding(), rects)
	n := layout.Neighbor(rects, win.Focus, dir)
	if n == "" {
		return fmt.Errorf("no pane %s of %s", dir, win.Focus)
	}
	win.Focus = n
	return nil
}

func (s *server) cyclePane(step int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	win := s.st.ActiveWindow()
	ids := layout.AllPanes(win.Root)
	if len(ids) == 0 {
		return fmt.Errorf("no panes")
	}
	at := 0
	for i, id := range ids {
		if id == win.Focus {
			at = i
		}
	}
	win.Focus = ids[(at+step+len(ids))%len(ids)]
	return nil
}

// selectLayout rebuilds the active window's tree as one of the preset
// layouts, over the window's pane ids in tree order. The pane set is exactly
// preserved, so the focus survives untouched. A window with fewer than two
// panes refuses: there is nothing to arrange.
func (s *server) selectLayout(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	win := s.st.ActiveWindow()
	ids := layout.AllPanes(win.Root)
	if len(ids) < 2 {
		return name, fmt.Errorf("select-layout needs at least two panes, window %s has %d", win.ID, len(ids))
	}
	area := layout.PaneAreaForWithTabBar(s.cols, s.rows, s.railLocked())
	root, ok := layout.ApplyPreset(win.Root, name, ids, area.W, area.H)
	if !ok {
		return name, fmt.Errorf("no layout %q", name)
	}
	win.Root = root
	return name, nil
}

func (s *server) closePane(target string) error {
	s.mu.Lock()
	win := s.st.ActiveWindow()
	if target == "" {
		target = win.Focus
	}
	ids := layout.AllPanes(win.Root)
	if len(ids) <= 1 {
		// Closing the last pane empties the window. Let the pane's exit drive
		// the normal path, which drops the window and ends the session when
		// nothing is left, exactly like a shell exiting.
		pc := s.panes[target]
		s.mu.Unlock()
		if pc == nil {
			return fmt.Errorf("no pane %q", target)
		}
		_ = pc.send(wire.Msg{Action: "kill"})
		return nil
	}
	defer s.mu.Unlock()
	at := 0
	for i, id := range ids {
		if id == target {
			at = i
		}
	}
	root, ok := layout.PruneLeaf(win.Root, target)
	if !ok {
		return fmt.Errorf("no pane %q", target)
	}
	win.Root = root
	if win.Focus == target {
		rest := layout.AllPanes(root)
		if len(rest) > 0 {
			if at >= len(rest) {
				at = len(rest) - 1
			}
			win.Focus = rest[at]
		}
	}
	if pc := s.panes[target]; pc != nil {
		_ = pc.send(wire.Msg{Action: "kill"})
		_ = pc.c.Close()
		delete(s.panes, target)
	}
	delete(s.st.Panes, target)
	return nil
}

// setPaneCwd records where a pane's shell stands and redraws when it moved. A
// move is the rail's folder, its git labels and the folder the next pane opens
// in, all at once.
func (s *server) setPaneCwd(id, cwd string) {
	if cwd == "" {
		return
	}
	s.mu.Lock()
	ps := s.st.Panes[id]
	if ps == nil || ps.Cwd == cwd {
		s.mu.Unlock()
		return
	}
	ps.Cwd = cwd
	_ = s.st.Save()
	s.mu.Unlock()
	s.pushSnapshot()
}

// setPaneTitle records a pane's OSC window title. A title is the shell's
// name, so it repaints the rail at most once a titlePushEvery.
func (s *server) setPaneTitle(id, title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pc := s.panes[id]
	if pc == nil || pc.title == title {
		return
	}
	pc.title = title
	s.pushTitleLocked()
}

// liveCwd is where a pane's shell stands now. It asks the pane, because the
// recorded cwd is the folder the pane was born in and a cd never rewrites it.
// The recorded cwd answers when the pane daemon cannot: a dead daemon, or one
// spawned by an older binary.
func (s *server) liveCwd(id string) string {
	if id != "" {
		if m, err := paneRequest(s.st, id, wire.Msg{Type: "req", Action: "cwd"}); err == nil && m.Cwd != "" {
			return m.Cwd
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ps := s.st.Panes[id]; ps != nil {
		return ps.Cwd
	}
	return ""
}

// inheritPaneLocked is the pane a new pane takes its folder from: target names
// a pane, or a workspace whose active pane answers, or nothing at all for the
// active workspace. The caller holds s.mu.
func (s *server) inheritPaneLocked(target string) string {
	if target != "" && s.st.Panes[target] != nil {
		return target
	}
	ws := s.st.ActiveWorkspace()
	for _, cand := range s.st.Workspaces {
		if target != "" && cand.ID != target {
			continue
		}
		ws = cand
		break
	}
	if ws == nil || len(ws.Windows) == 0 {
		return ""
	}
	return ws.Windows[ws.Active].Focus
}

// inheritedCwd is where the pane a new pane inherits from stands now, or empty
// when there is nothing to ask. It runs before the caller takes s.mu, because
// asking a pane is a socket round trip.
func (s *server) inheritedCwd(target string) string {
	s.mu.Lock()
	id := s.inheritPaneLocked(target)
	s.mu.Unlock()
	return s.liveCwd(id)
}

// windowCwdLocked is the starting directory of a workspace's active pane, or
// the server's own directory when the workspace has none. The caller holds
// s.mu.
func (s *server) windowCwdLocked(ws *session.Workspace) string {
	if ws != nil && len(ws.Windows) > 0 {
		if ps := s.st.Panes[ws.Windows[ws.Active].Focus]; ps != nil {
			return ps.Cwd
		}
	}
	cwd, _ := os.Getwd()
	return cwd
}

func (s *server) newWindow(wsID, label, cwd string, noFocus bool) (string, string, error) {
	// The new pane opens where its source pane stands now, so the window's
	// folder name and its shell agree. Ask before taking the lock.
	if cwd == "" {
		cwd = s.inheritedCwd(wsID)
	}
	s.mu.Lock()
	ws := s.st.ActiveWorkspace()
	src := ""
	if wsID != "" {
		ws = nil
		for _, cand := range s.st.Workspaces {
			if cand.ID == wsID {
				ws = cand
				break
			}
			for _, w := range cand.Windows {
				if layout.FindLeaf(w.Root, wsID) {
					ws, src = cand, wsID
					break
				}
			}
			if ws != nil {
				break
			}
		}
		if ws == nil {
			s.mu.Unlock()
			return "", "", fmt.Errorf("no workspace or pane %q", wsID)
		}
	}
	// The new pane opens where the source pane is, or where the active pane of
	// the target workspace is, so the window's folder name and its shell agree.
	if cwd == "" {
		if ps := s.st.Panes[src]; src != "" && ps != nil {
			cwd = ps.Cwd
		}
		if cwd == "" {
			cwd = s.windowCwdLocked(ws)
		}
	}
	wid := s.st.NewWindowID()
	w := &session.Window{ID: wid, Name: label}
	w.Unfolded = s.cfg.DefaultFold()
	id := w.NewPaneID()
	w.Root = &layout.Node{Pane: id}
	w.Focus = id
	if label == "" {
		label = paths.FolderName(cwd)
		if label == "" {
			label = strconv.Itoa(len(ws.Windows) + 1)
		}
		w.Name = label
	}
	ws.Windows = append(ws.Windows, w)
	s.addPaneStateAt(id, cwd, "")
	if !noFocus {
		for i, cand := range s.st.Workspaces {
			if cand == ws {
				s.st.Active = i
			}
		}
		ws.Active = len(ws.Windows) - 1
	}
	s.mu.Unlock()
	if err := s.ensurePane(id); err != nil {
		return wid, id, err
	}
	s.watchPane(id)
	return wid, id, nil
}

func (s *server) cycleWindow(step int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.st.ActiveWorkspace()
	n := len(ws.Windows)
	if n == 0 {
		return fmt.Errorf("no windows")
	}
	ws.Active = (ws.Active + step + n) % n
	return nil
}

func (s *server) selectWindow(i int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.st.ActiveWorkspace()
	if i < 0 {
		i = 0
	}
	if i >= len(ws.Windows) {
		return fmt.Errorf("window %d does not exist", i)
	}
	ws.Active = i
	return nil
}

func (s *server) newWorkspace(name, tabLabel, cwd, project string, noFocus bool) (string, string, string, error) {
	// Ask the pane before taking the lock: the folder is where the user is now,
	// and asking is a socket round trip.
	if cwd == "" {
		cwd = s.inheritedCwd("")
	}
	s.mu.Lock()
	activeID := ""
	if aws := s.st.ActiveWorkspace(); aws != nil {
		activeID = aws.ID
		if project == "" {
			project = aws.Project
		}
	}
	if name == "" {
		name = "ws" + strconv.Itoa(len(s.st.Workspaces)+1)
	}
	if cwd == "" {
		cwd = s.windowCwdLocked(s.st.ActiveWorkspace())
	}
	if tabLabel == "" {
		tabLabel = paths.FolderName(cwd)
		if tabLabel == "" {
			tabLabel = "1"
		}
	}
	wsid := s.st.NewWorkspaceID()
	wid := s.st.NewWindowID()
	win := &session.Window{ID: wid, Name: tabLabel}
	id := win.NewPaneID()
	win.Root = &layout.Node{Pane: id}
	win.Focus = id
	ws := &session.Workspace{ID: wsid, Name: name, Project: project, Unfolded: true, Windows: []*session.Window{win}}
	s.insertWorkspaceLocked(ws)
	// The insert can land before the active workspace, so follow the active
	// workspace by id. A focused new workspace becomes the active one.
	if noFocus {
		for i, w := range s.st.Workspaces {
			if w.ID == activeID {
				s.st.Active = i
				break
			}
		}
	} else {
		for i, w := range s.st.Workspaces {
			if w.ID == wsid {
				s.st.Active = i
				break
			}
		}
	}
	s.addPaneStateAt(id, cwd, "")
	s.mu.Unlock()
	if err := s.ensurePane(id); err != nil {
		return wsid, wid, id, err
	}
	s.watchPane(id)
	return wsid, wid, id, nil
}

func (s *server) cycleWorkspace(step int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.st.Workspaces)
	if n == 0 {
		return fmt.Errorf("no workspaces")
	}
	s.st.Active = (s.st.Active + step + n) % n
	return nil
}

// ---- client side helpers ----
