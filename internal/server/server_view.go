package server

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"dm/internal/layout"
	"dm/internal/notify"
	"dm/internal/paths"
	"dm/internal/session"
	"dm/internal/vt"
	"dm/internal/wire"
)

// fitToClientsLocked takes the shared session size from the largest attached
// client. Every client draws the same layout, but a small one, such as a dm
// nested in one of this session's own panes, must not shrink the display of
// the others. The caller holds s.mu and reports whether the size changed.
func (s *server) fitToClientsLocked() bool {
	cols, rows := 0, 0
	for cl := range s.clients {
		if cl.cols > cols {
			cols = cl.cols
		}
		if cl.rows > rows {
			rows = cl.rows
		}
	}
	if cols < 20 || rows < 5 {
		return false
	}
	if cols == s.cols && rows == s.rows {
		return false
	}
	s.cols, s.rows = cols, rows
	return true
}

// requestedRailLocked is the stored width, floored at zero, before the screen
// clamp. The caller holds s.mu.
func (s *server) requestedRailLocked() int {
	if !s.st.RailSet {
		return layout.DefaultRail
	}
	if s.st.Rail < 0 {
		return 0
	}
	return s.st.Rail
}

// railLocked is the effective sidebar width for the current screen. The caller
// holds s.mu.
func (s *server) railLocked() int {
	return layout.EffectiveRail(s.cols, s.requestedRailLocked())
}

// setRail records the requested width and returns the effective one. A
// requested 0 hides the rail. The value is clamped when it is used, not when
// it is stored, so a wider terminal restores a wider rail.
func (s *server) setRail(requested int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Rail = requested
	s.st.RailSet = true
	rail := s.railLocked()
	_ = s.st.Save()
	return rail
}

// paneRectsLocked is the active window's layout with the zoom applied: the
// zoomed pane takes the box a single unsplit pane gets, and every sibling keeps
// its layout rectangle. A zoom that no longer names a pane of the window is
// dropped here, so a dead or switched-away pane cannot hold it. The caller
// holds s.mu.
func (s *server) paneRectsLocked() map[string]layout.Rect {
	win := s.st.ActiveWindow()
	area := layout.PaneAreaForWithTabBar(s.cols, s.rows, s.railLocked())
	pad := s.cfg.Padding()
	rects := map[string]layout.Rect{}
	layout.Layout(win.Root, area, pad, rects)
	if _, ok := rects[s.zoom]; !ok {
		s.zoom = ""
		return rects
	}
	rects[s.zoom] = layout.InsetBox(area, pad)
	return rects
}

// clearZoom drops the zoom. It is one view of one focus, so a focus or layout
// change ends it.
func (s *server) clearZoom() {
	s.mu.Lock()
	s.zoom = ""
	s.mu.Unlock()
}

func (s *server) snapshotLocked() *wire.Snapshot {
	win := s.st.ActiveWindow()
	rects := s.paneRectsLocked()
	snap := &wire.Snapshot{
		Session:      s.st.Name,
		Workspace:    s.st.ActiveWorkspace().Name,
		Windows:      []wire.WindowInfo{},
		ActiveWindow: s.st.ActiveWorkspace().Active,
		Focus:        win.Focus,
		Cols:         s.cols,
		Rows:         s.rows,
		Rail:         s.railLocked(),
		RailWant:     s.requestedRailLocked(),
		Zoom:         s.zoom,
		Pad:          s.cfg.Padding(),
		Root:         win.Root,
	}
	for _, w := range s.st.ActiveWorkspace().Windows {
		snap.Windows = append(snap.Windows, wire.WindowInfo{ID: w.ID, Name: w.Name, Named: w.Named})
	}
	for _, id := range layout.AllPanes(win.Root) {
		pc := s.panes[id]
		info := wire.PaneInfo{ID: id, Rect: rects[id]}
		if pc != nil {
			info.Status = pc.st.State
			info.Message = pc.st.Message
			info.Alt = pc.alt
			info.Mouse = pc.mouse
		}
		if ps := s.st.Panes[id]; ps != nil {
			info.Name = ps.Name
		}
		ps := s.st.Panes[id]
		if ps != nil && ps.Restarted {
			info.Restart = true
		}
		snap.Panes = append(snap.Panes, info)
	}
	snap.All = s.allPanesLocked()
	return snap
}

// noteStatus persists the agent mark and the reporting app. Both keep the
// sidebar calling a pane an agent after state=clear, and both survive a server
// or pane restart because they live in the state file.
func (s *server) noteStatus(id string, st wire.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps := s.st.Panes[id]
	if ps == nil {
		return
	}
	changed := false
	if !ps.Reported {
		ps.Reported = true
		changed = true
	}
	if st.App != "" && ps.App != st.App {
		ps.App = st.App
		changed = true
	}
	if changed {
		_ = s.st.Save()
	}
}

// allPanesLocked lists every pane in the session in workspace, window, tree
// order. A pane is an agent when it has a recorded name, reported the status
// protocol at least once, or has a status other than empty or unknown;
// otherwise it is a shell. The caller holds s.mu.
func (s *server) allPanesLocked() []wire.AllPane {
	out := make([]wire.AllPane, 0, len(s.st.Panes))
	for i, ws := range s.st.Workspaces {
		wsActive := i == s.st.Active
		for j, w := range ws.Windows {
			winActive := wsActive && j == ws.Active
			for _, id := range layout.AllPanes(w.Root) {
				info := wire.AllPane{
					ID: id, Workspace: ws.ID, WorkspaceName: ws.Name, Project: ws.Project,
					Window: w.ID, WindowName: w.Name, WindowNamed: w.Named,
					WorkspaceActive: wsActive, WindowActive: winActive,
					Focus: winActive && id == w.Focus,
				}
				info.WindowUnfolded = w.Unfolded
				info.WorkspaceUnfolded = ws.Unfolded
				info.ProjectFolded = s.st.ProjectFold[ws.Project]
				ps := s.st.Panes[id]
				if ps != nil {
					info.Name = ps.Name
					info.App = ps.App
					gi := s.gitFor(ps.Cwd)
					info.Cwd = ps.Cwd
					info.Root = gi.root
					info.Branch = gi.branch
					info.Worktree = gi.worktree
					info.InRepo = gi.inRepo
					info.Dirty = gi.dirty
					info.Added = gi.added
					info.Deleted = gi.deleted
				}
				if pc := s.panes[id]; pc != nil {
					info.Status = pc.st.State
					info.Message = pc.st.Message
				}
				info.Agent = info.Name != "" || (ps != nil && ps.Reported) ||
					(info.Status != "" && info.Status != "unknown")
				out = append(out, info)
			}
		}
	}
	return out
}

// paneExited removes a dead pane from the layout. A split collapses, focus
// moves to a live neighbour, an empty window closes. When the session has no
// panes left the server ends the session and every client detaches.
func (s *server) paneExited(id string) {
	s.mu.Lock()
	pc := s.panes[id]
	// An exit interrupts the operator unless a client is attached and the pane
	// is the focus it is looking at. The title is built before the pane leaves
	// the state, because its recorded name goes with it.
	notifyExit := !s.operatorSeesLocked(id)
	exitTitle := ""
	if notifyExit {
		exitTitle = s.notifyTitleLocked(id, "exited")
	}
	exitBody := ""
	if pc != nil {
		exitBody = pc.st.Message
	}
	delete(s.panes, id)
	delete(s.st.Panes, id)

	found, emptyWindow := false, false
	for _, ws := range s.st.Workspaces {
		for _, w := range ws.Windows {
			if !layout.FindLeaf(w.Root, id) {
				continue
			}
			found = true
			ids := layout.AllPanes(w.Root)
			at := 0
			for i, pid := range ids {
				if pid == id {
					at = i
				}
			}
			if root, ok := layout.PruneLeaf(w.Root, id); ok {
				w.Root = root
			}
			rest := layout.AllPanes(w.Root)
			if len(rest) > 0 {
				if w.Focus == id {
					if at >= len(rest) {
						at = len(rest) - 1
					}
					w.Focus = rest[at]
				}
				continue
			}
			w.Root = nil
			emptyWindow = true
		}
	}
	if !found && pc == nil {
		s.mu.Unlock()
		return
	}
	if emptyWindow {
		s.dropEmptyWindows()
	}
	if s.countPanes() == 0 {
		s.mu.Unlock()
		if found {
			notify.SystemNotify(exitTitle, exitBody)
		}
		if pc != nil {
			_ = pc.c.Close()
		}
		s.endSession()
		return
	}
	s.mu.Unlock()
	if found {
		notify.SystemNotify(exitTitle, exitBody)
	}

	if pc != nil {
		_ = pc.c.Close()
	}
	old := paths.PaneSock(s.st.Name, id)
	_ = os.Remove(old)
	_ = os.Remove(old + ".pid")

	s.syncSizes()
	s.mu.Lock()
	_ = s.st.Save()
	s.broadcastLocked(wire.Msg{Type: "exit", Target: id})
	s.broadcastLocked(wire.Msg{Type: "snapshot", Snap: s.snapshotLocked()})
	ids := layout.AllPanes(s.st.ActiveWindow().Root)
	s.mu.Unlock()

	s.broadcastScreens(ids)
	for _, pid := range ids {
		rp := readPaneReplay(s.st.Name, pid)
		if !rp.ok {
			continue
		}
		s.mu.Lock()
		if pc := s.panes[pid]; pc != nil {
			pc.alt, pc.mouse = rp.alt, rp.mouse
		}
		s.broadcastLocked(wire.Msg{Type: "replay", Target: pid, Data: rp.data, St: &rp.st})
		s.mu.Unlock()
	}
}

// dropEmptyWindows removes windows with no panes and workspaces left with no
// windows, keeping the active window and workspace pointing at something real.
func (s *server) dropEmptyWindows() {
	var activeWin *session.Window
	if ws := s.st.ActiveWorkspace(); ws.Active >= 0 && ws.Active < len(ws.Windows) {
		activeWin = ws.Windows[ws.Active]
	}
	var keep []*session.Workspace
	for _, ws := range s.st.Workspaces {
		var wins []*session.Window
		for _, w := range ws.Windows {
			if w.Root != nil && len(layout.AllPanes(w.Root)) > 0 {
				wins = append(wins, w)
			}
		}
		if len(wins) == 0 {
			continue
		}
		ws.Windows = wins
		ws.Active = 0
		for i, w := range wins {
			if w == activeWin {
				ws.Active = i
			}
		}
		keep = append(keep, ws)
	}
	if len(keep) == 0 {
		s.st.Workspaces = nil
		s.st.Active = 0
		return
	}
	s.st.Workspaces = keep
	if s.st.Active >= len(keep) {
		s.st.Active = len(keep) - 1
	}
}

func (s *server) countPanes() int {
	n := 0
	for _, ws := range s.st.Workspaces {
		for _, w := range ws.Windows {
			n += len(layout.AllPanes(w.Root))
		}
	}
	return n
}

// endSession closes every client, removes the session directory and exits. A
// session with no panes is gone, the same way tmux ends one.
func (s *server) endSession() {
	s.mu.Lock()
	s.ending = true
	s.stopReflowLocked()
	clients := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.broadcastLocked(wire.Msg{Type: "close"})
	s.mu.Unlock()

	// Remove the session before closing the listener: accepting goroutine wakes
	// on the closed listener and may exit the process immediately.
	name := s.st.Name
	_ = os.Remove(paths.ServerSock(name))
	_ = os.Remove(filepath.Join(paths.SessionDir(name), "server.pid"))
	_ = os.RemoveAll(paths.SessionDir(name))
	for _, c := range clients {
		_ = c.c.Close()
	}
	if s.ln != nil {
		_ = s.ln.Close()
	}
	os.Exit(0)
}

func (s *server) pushSnapshot() {
	s.mu.Lock()
	snap := s.snapshotLocked()
	s.broadcastLocked(wire.Msg{Type: "snapshot", Snap: snap})
	s.mu.Unlock()
}

// armReflowLocked records panes whose width changed and (re)starts the settle
// timer. Merging the ids and resetting the timer make one drag produce one
// settle at its final size. The caller holds s.mu.
func (s *server) armReflowLocked(ids []string) {
	if len(ids) == 0 {
		return
	}
	if s.reflowWait == nil {
		s.reflowWait = make(map[string]bool, len(ids))
	}
	for _, id := range ids {
		s.reflowWait[id] = true
	}
	if s.reflowTimer == nil {
		s.reflowTimer = time.AfterFunc(reflowSettle, s.settleReflow)
		return
	}
	s.reflowTimer.Reset(reflowSettle)
}

// stopReflowLocked cancels a pending settle and forgets the panes waiting on
// it. The caller holds s.mu.
func (s *server) stopReflowLocked() {
	if s.reflowTimer != nil {
		s.reflowTimer.Stop()
		s.reflowTimer = nil
	}
	s.reflowWait = nil
}

func (s *server) stopReflow() {
	s.mu.Lock()
	s.stopReflowLocked()
	s.mu.Unlock()
}

// settleReflow runs reflowSettle after the last width change. It asks every
// affected pane to rebuild its screen from its ring at the recorded size and
// broadcasts each answer, the same screen frame sendScreens sends. The
// requests run concurrently, so one pane that does not answer cannot hold the
// rest; a pane that fails is skipped.
func (s *server) settleReflow() {
	s.mu.Lock()
	wait := s.reflowWait
	s.stopReflowLocked()
	sizes := make(map[string][2]int, len(wait))
	for id := range wait {
		if ps := s.st.Panes[id]; ps != nil {
			sizes[id] = [2]int{ps.Cols, ps.Rows}
		}
	}
	name := s.st.Name
	s.mu.Unlock()

	type answer struct {
		id string
		ps paneScreen
	}
	answers := make(chan answer, len(sizes))
	var wg sync.WaitGroup
	for id, sz := range sizes {
		wg.Add(1)
		go func(id string, cols, rows int) {
			defer wg.Done()
			m := wire.Msg{Type: "req", Action: "reflow", Cols: cols, Rows: rows}
			answers <- answer{id: id, ps: paneScreenRequest(name, id, m)}
		}(id, sz[0], sz[1])
	}
	go func() {
		wg.Wait()
		close(answers)
	}()
	for a := range answers {
		if !a.ps.ok {
			continue
		}
		s.mu.Lock()
		if pc := s.panes[a.id]; pc != nil {
			pc.alt, pc.mouse = a.ps.alt, a.ps.mouse
		}
		s.broadcastLocked(wire.Msg{Type: "screen", Target: a.id, VTSnap: a.ps.snap})
		s.mu.Unlock()
	}
}

// syncSizes pushes each active-window pane's rectangle into its PTY and VT.
func (s *server) syncSizes() {
	s.mu.Lock()
	rects := s.paneRectsLocked()
	s.st.Cols, s.st.Rows = s.cols, s.rows
	var calls []func()
	var widened []string
	for id, r := range rects {
		pc := s.panes[id]
		ps := s.st.Panes[id]
		if ps == nil {
			continue
		}
		if ps.Cols == r.W && ps.Rows == r.H {
			continue
		}
		// Only a width change re-wraps the line model, so only those panes
		// wait for a reflow. A height change gets the cheap resize alone.
		if ps.Cols != r.W {
			widened = append(widened, id)
		}
		ps.Cols, ps.Rows = r.W, r.H
		calls = append(calls, func() {
			if pc != nil {
				_ = pc.send(wire.Msg{Action: "resize", Cols: r.W, Rows: r.H})
			}
		})
	}
	s.armReflowLocked(widened)
	s.mu.Unlock()
	for _, f := range calls {
		f()
	}
	_ = s.st.Save()
}

func (s *server) serve(c net.Conn) {
	cl := newClient(c)
	registered := false
	defer func() {
		if registered {
			s.mu.Lock()
			delete(s.clients, cl)
			resized := s.fitToClientsLocked()
			s.mu.Unlock()
			if resized {
				s.syncSizes()
				s.pushSnapshot()
			}
		}
		cl.shutdown()
		c.Close()
	}()

	for {
		m, err := wire.ReadMsg(c)
		if err != nil {
			return
		}
		switch m.Type {
		case "attach":
			registered = true
			s.mu.Lock()
			s.clients[cl] = true
			s.mu.Unlock()
			s.attach(cl, m)
		case "keys":
			s.mu.Lock()
			pc := s.panes[s.st.ActiveWindow().Focus]
			s.mu.Unlock()
			if pc != nil {
				_ = pc.send(wire.Msg{Action: "write", Keys: m.Keys})
			}
		case "resize":
			s.mu.Lock()
			if m.Cols > 20 && m.Rows > 5 {
				cl.cols, cl.rows = m.Cols, m.Rows
			}
			s.fitToClientsLocked()
			s.mu.Unlock()
			s.syncSizes()
			s.pushSnapshot()
		case "req":
			s.handleReq(cl, m)
		}
	}
}

func (s *server) attach(cl *client, m wire.Msg) {
	s.mu.Lock()
	if m.Cols > 20 && m.Rows > 5 {
		cl.cols, cl.rows = m.Cols, m.Rows
	}
	s.fitToClientsLocked()
	s.mu.Unlock()
	// A client that overflows later resyncs from a picture instead of a
	// backlog: one snapshot, then one screen per pane.
	cl.setResync(func() { s.resyncClient(cl) })
	cl.pause()
	s.syncSizes()
	s.mu.Lock()
	snap := s.snapshotLocked()
	ids := layout.AllPanes(s.st.ActiveWindow().Root)
	s.mu.Unlock()
	cl.deliver(wire.Msg{Type: "snapshot", Snap: snap})
	if s.sendScreens(ids, func(m wire.Msg) { cl.deliver(m) }) {
		// The pane's alt or mouse mode changed since the snapshot was built.
		// Send a fresh one so the client's pane info is not one event stale.
		s.mu.Lock()
		snap = s.snapshotLocked()
		s.mu.Unlock()
		cl.deliver(wire.Msg{Type: "snapshot", Snap: snap})
	}
	// History is not pushed at attach: the ring is 512 KiB per pane, and a
	// nested client would carry it through the outer pty. The client asks for
	// it with a "replay" request when it wants history. Its alternate-screen
	// and mouse facts came back with sendScreens above, and later mode
	// changes still arrive from the pane.
	cl.resume()
}

// paneScreen is the pane's visible screen as the VT snapshot. It is the
// picture; the ring below is history.
type paneScreen struct {
	snap  *vt.Snapshot
	alt   bool
	mouse bool
	ok    bool
}

// paneScreenRequest dials the pane, sends one request and reads the screen
// reply. A fresh connection is a subscriber, so a busy pane can deliver bytes,
// status or cwd before the screen. Take the screen, not the first frame.
func paneScreenRequest(session, id string, m wire.Msg) paneScreen {
	var ps paneScreen
	c, err := net.DialTimeout("unix", paths.PaneSock(session, id), time.Second)
	if err != nil {
		return ps
	}
	defer c.Close()
	m.ID = 1
	if err := wire.WriteMsg(c, m); err != nil {
		return ps
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		m, err := wire.ReadMsg(c)
		if err != nil {
			return ps
		}
		if m.Type != "screen" {
			continue
		}
		if m.VTSnap == nil {
			return ps
		}
		var extra struct {
			Alt   bool `json:"alt"`
			Mouse bool `json:"mouse"`
		}
		_ = json.Unmarshal(m.Res, &extra)
		ps.snap, ps.alt, ps.mouse, ps.ok = m.VTSnap, extra.Alt, extra.Mouse, true
		return ps
	}
}

// readPaneScreen asks a pane for its visible screen.
func readPaneScreen(session, id string) paneScreen {
	return paneScreenRequest(session, id, wire.Msg{Type: "req", Action: "screen"})
}

// sendScreens asks each pane for its visible screen and hands it to send. It
// runs after a resize so the pane serialises at its new size.
func (s *server) sendScreens(ids []string, send func(wire.Msg)) (changed bool) {
	for _, id := range ids {
		ps := readPaneScreen(s.st.Name, id)
		if !ps.ok {
			continue
		}
		s.mu.Lock()
		if pc := s.panes[id]; pc != nil {
			if pc.alt != ps.alt || pc.mouse != ps.mouse {
				changed = true
			}
			pc.alt, pc.mouse = ps.alt, ps.mouse
		}
		s.mu.Unlock()
		send(wire.Msg{Type: "screen", Target: id, VTSnap: ps.snap})
	}
	return changed
}

func (s *server) broadcastScreens(ids []string) {
	s.sendScreens(ids, func(m wire.Msg) {
		s.mu.Lock()
		s.broadcastLocked(m)
		s.mu.Unlock()
	})
}

// resyncClient rebuilds one client from a picture: a fresh snapshot, then one
// screen per pane through readPaneScreen. The client's writer calls it after an
// overflow drains, so a lagging client never replays a backlog.
func (s *server) resyncClient(cl *client) {
	s.mu.Lock()
	snap := s.snapshotLocked()
	ids := layout.AllPanes(s.st.ActiveWindow().Root)
	s.mu.Unlock()
	cl.deliver(wire.Msg{Type: "snapshot", Snap: snap})
	s.sendScreens(ids, func(m wire.Msg) { cl.deliver(m) })
}

type paneReplay struct {
	data  []byte
	st    wire.Status
	alt   bool
	mouse bool
	ok    bool
}

func readPaneReplay(session, id string) paneReplay {
	var rp paneReplay
	c, err := net.DialTimeout("unix", paths.PaneSock(session, id), time.Second)
	if err != nil {
		return rp
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := wire.WriteMsg(c, wire.Msg{Type: "req", Action: "replay", ID: 1}); err != nil {
		return rp
	}
	// A pane broadcasts bytes, status and cwd while the request is in flight.
	// The replay reply is the only frame this call wants: taking the first
	// frame made a busy pane's ring a fragment.
	for {
		m, err := wire.ReadMsg(c)
		if err != nil {
			return rp
		}
		if m.Type != "replay" {
			continue
		}
		if m.St != nil {
			rp.st = *m.St
		}
		var extra struct {
			Alt   bool `json:"alt"`
			Mouse bool `json:"mouse"`
		}
		_ = json.Unmarshal(m.Res, &extra)
		rp.data, rp.alt, rp.mouse, rp.ok = m.Data, extra.Alt, extra.Mouse, true
		return rp
	}
}

func paneRequest(session, id string, m wire.Msg) (wire.Msg, error) {
	var out wire.Msg
	c, err := net.DialTimeout("unix", paths.PaneSock(session, id), time.Second)
	if err != nil {
		return out, err
	}
	defer c.Close()
	m.ID = 1
	if err := wire.WriteMsg(c, m); err != nil {
		return out, err
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		got, err := wire.ReadMsg(c)
		if err != nil {
			return out, err
		}
		if got.Type == "resp" || got.Type == "replay" || got.Type == "screen" {
			return got, nil
		}
	}
}

func (s *server) reply(cl *client, m wire.Msg, res any, err error) {
	// The action rides back, so a client can tell the replies it asked for
	// apart from each other. ID stays the request tag it already was.
	out := wire.Msg{Type: "resp", Action: m.Action, ID: m.ID, Res: jsonRaw(res)}
	if err != nil {
		out.Error = err.Error()
	} else {
		out.OK = true
	}
	cl.deliver(out)
}

func jsonRaw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
