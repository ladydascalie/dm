// Package websession is the pretend session the browser tour talks to.
//
// It answers the real dm wire messages over a connection, with no pty, no pane
// daemon and no server behind it: each pane is a vt.Term and an
// internal/webshell.Shell, and the model of workspaces, windows and panes is
// the session's. The package has no build tags, so a native test drives it with
// wire.Msg values and reads the frames back through wire.ReadMsg.
package websession

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"

	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/vt"
	"dm/internal/webshell"
	"dm/internal/wire"
)

// SizeFunc returns the renderer's size in cells.
type SizeFunc func() (int, int)

// window is one tiling of panes. Its flag and its preset belong to it, as on a
// machine, so a window switch carries both.
type window struct {
	ID   string
	Name string
	// Named records a user-set name. The fake names nothing, so the rail draws
	// the default label.
	Named bool
	Root  *layout.Node
	Focus string
	// NextPane is the highest pane number handed out in this window. Pane ids
	// only move forward, so a closed pane's number is never reused.
	NextPane int
	// Unfolded is the rail's fold state. A window starts unfolded.
	Unfolded bool
	// Sync is the window's synchronize-panes flag: keys reach every pane of
	// this window.
	Sync bool
	// Layout is the preset select-layout last applied, empty for a hand-made
	// tree. "next" walks the order from it.
	Layout string
}

// workspace is a group of windows with one active. A workspace starts
// unfolded, as the real state does.
type workspace struct {
	ID       string
	Name     string
	Windows  []*window
	Active   int
	Unfolded bool
}

// pane is one pretend pane: the terminal it drew, the shell behind it and the
// raw bytes it printed.
type pane struct {
	term *vt.Term
	sh   *webshell.Shell
	ring *ring
}

// write puts bytes on the pane's screen, into its scrollback ring, and returns
// them, so a read travels to the client in one frame. A resize repaint goes
// through here too: those bytes really printed.
func (p *pane) write(b []byte) []byte {
	p.term.Write(b)
	p.ring.Write(b)
	return b
}

// resize fits the pane's screen to a new rect. The client does the same to its
// copy, so both sides stay on the same grid. A narrower terminal cannot rewrap
// what it already printed, so the shell draws its screen again at the new
// width: without this a split clips the right half of every line.
func (p *pane) resize(cols, rows int) {
	g := p.term.Grid()
	oc, or := g.Size()
	if oc == cols && or == rows {
		return
	}
	r := g.ScrollRegion()
	g.Resize(cols, rows)
	if r.Top == 0 && r.Bottom == or-1 && r.Left == 0 && r.Right == oc-1 {
		g.SetScrollRegion(vt.ScrollRegion{Top: 0, Bottom: rows - 1, Left: 0, Right: cols - 1})
	}
	p.write(p.sh.Repaint(rows))
}

// Session is one pretend session: workspaces of windows of panes, the
// connection it answers on, and the renderer's size.
type Session struct {
	conn    net.Conn
	forward func(map[string]any)

	cols, rows int
	// rail is the width the page asked for: 0 hidden, CollapsedRail thin,
	// DefaultRail full. EffectiveRail clamps it to what the screen can spare.
	rail int
	// zoom is the pane drawn over the whole area, empty for none. It is one
	// view of one focus, so a window, workspace, layout or focus change ends
	// it, as the server ends a zoom.
	zoom string

	workspaces    []*workspace
	active        int
	nextWorkspace int
	nextWindow    int

	panes map[string]*pane
	// sent is the rect each pane's screen was last sent at. The client drops
	// every terminal that leaves the snapshot's Panes, so an entry is dropped
	// with it and the next visit sends the screen again.
	sent map[string]layout.Rect

	// reqMu guards last, which the page reads from its own callback.
	reqMu sync.Mutex
	last  string
}

// New returns a session with one workspace, one window and one pane. size seeds
// the first layout; a resize frame then carries the renderer's live size.
func New(size SizeFunc, forward func(map[string]any), conn net.Conn) *Session {
	s := &Session{
		conn: conn, forward: forward,
		rail:          layout.DefaultRail,
		nextWorkspace: 2,
		nextWindow:    2,
		panes:         map[string]*pane{},
		sent:          map[string]layout.Rect{},
	}
	s.cols, s.rows = 100, 30
	if size != nil {
		if cols, rows := size(); SizeOK(cols, rows) {
			s.cols, s.rows = cols, rows
		}
	}
	w := &window{
		ID: "w1", Name: "one",
		Root: &layout.Node{Pane: "w1p1"}, Focus: "w1p1",
		NextPane: 1, Unfolded: true,
	}
	s.workspaces = []*workspace{{
		ID: "s1", Name: "main",
		Windows: []*window{w}, Active: 0, Unfolded: true,
	}}
	s.fit()
	return s
}

// LastRequest is the request the session handled last, as the page's debug
// hook prints it.
func (s *Session) LastRequest() string {
	s.reqMu.Lock()
	defer s.reqMu.Unlock()
	return s.last
}

// note records one request for LastRequest.
func (s *Session) note(m wire.Msg) {
	s.reqMu.Lock()
	s.last = m.Action + " " + m.Target + " " + m.Layout
	s.reqMu.Unlock()
}

// Serve answers frames from the connection until it closes.
func (s *Session) Serve() {
	for {
		m, err := wire.ReadMsg(s.conn)
		if err != nil {
			return
		}
		s.serve(m)
	}
}

// serve runs one frame. A request always gets a reply; a change also pushes the
// snapshot and the screens that changed with it.
func (s *Session) serve(m wire.Msg) {
	switch m.Type {
	case "attach":
		// A new client holds no terminal yet, so every screen is sent again.
		s.sent = map[string]layout.Rect{}
		if s.applySize(m.Cols, m.Rows) {
			s.fit()
		}
		s.push()
	case "resize":
		if s.applySize(m.Cols, m.Rows) {
			s.fit()
			s.push()
		}
	case "keys":
		s.keys(m)
	case "req":
		s.note(m)
		a := s.request(m)
		if a.changed {
			s.fit()
			s.push()
		}
		s.reply(m, a)
	}
}

// minCols and minRows are the smallest size a session lays out.
const (
	minCols = 20
	minRows = 6
)

// SizeOK reports whether a renderer size is large enough to lay out. The
// session and the page both call it, so the two gates hold one comparison and
// cannot drift apart.
func SizeOK(cols, rows int) bool {
	return cols >= minCols && rows >= minRows
}

// applySize records a renderer size too small to lay out as a no-op.
func (s *Session) applySize(cols, rows int) bool {
	if !SizeOK(cols, rows) {
		return false
	}
	if cols == s.cols && rows == s.rows {
		return false
	}
	s.cols, s.rows = cols, rows
	return true
}

// keys runs one read of keys. A synced window takes them in every pane of the
// window, as on a machine; nothing else does.
func (s *Session) keys(m wire.Msg) {
	id := m.Target
	if id == "" {
		if w := s.activeWindow(); w != nil {
			id = w.Focus
		}
	}
	for _, pid := range s.targets(id) {
		pn := s.panes[pid]
		if pn == nil {
			continue
		}
		_ = wire.WriteMsg(s.conn, wire.Msg{Type: "bytes", Target: pid, Data: pn.write(pn.sh.Feed(m.Keys))})
	}
}

// targets is the panes one read of keys reaches.
func (s *Session) targets(id string) []string {
	_, w, ok := s.locatePane(id)
	if !ok || !w.Sync {
		return []string{id}
	}
	out := []string{}
	for _, p := range layout.AllPanes(w.Root) {
		if p != id {
			out = append(out, p)
		}
	}
	return append([]string{id}, out...)
}

// answer is what one request did: whether the picture changed, the reply body,
// and the refusal that keeps the reply honest.
type answer struct {
	changed bool
	res     any
	err     error
}

// request runs one req action. An action the fake does not serve returns the
// zero answer: no change, an empty reply.
func (s *Session) request(m wire.Msg) answer {
	switch m.Action {
	case "split":
		return s.split(m)
	case "close-pane":
		return s.closePane(m)
	case "set-rail":
		// The page asks for a width; the screen decides how much of it fits.
		s.rail = m.Index
		return answer{changed: true}
	case "focus":
		return s.focus(m)
	case "toggle-fold":
		return s.toggleFold(m)
	case "zoom-pane":
		return s.zoomPane()
	case "swap-panes":
		return s.swapPanes(m)
	case "synchronize-panes":
		if w := s.activeWindow(); w != nil {
			if m.On != nil {
				w.Sync = *m.On
			} else {
				w.Sync = !w.Sync
			}
			return answer{changed: true}
		}
		return answer{err: errNoWindow}
	case "select-layout":
		return s.selectLayout(m)
	case "focus-dir":
		return s.focusDir(m)
	case "next-pane", "prev-pane":
		return s.cyclePane(m)
	case "new-window":
		return s.newWindow(m)
	case "new-workspace":
		return s.newWorkspace(m)
	case "select-window":
		return s.selectWindow(m)
	case "next-window", "prev-window":
		return s.cycleWindow(m)
	case "next-workspace", "prev-workspace":
		return s.cycleWorkspace(m)
	case "close-window":
		return s.closeWindow(m)
	case "close-workspace":
		return s.closeWorkspace(m)
	case "replay":
		return s.replay(m)
	}
	return answer{}
}

// reply sends the frame every request gets, so a client waiting on one is never
// left hanging.
func (s *Session) reply(m wire.Msg, a answer) {
	out := wire.Msg{Type: "resp", Action: m.Action, ID: m.ID, OK: a.err == nil}
	if a.err != nil {
		out.Error = a.err.Error()
	}
	if a.res != nil {
		if b, err := json.Marshal(a.res); err == nil {
			out.Res = b
		}
	}
	_ = wire.WriteMsg(s.conn, out)
}

var errNoWindow = errors.New("no active window")

// split adds a pane beside target in its own window and focuses it.
func (s *Session) split(m wire.Msg) answer {
	switch m.Dir {
	case "left", "right", "up", "down":
	default:
		return answer{err: fmt.Errorf("dir must be left, right, up or down")}
	}
	target := m.Target
	if target == "" {
		// The server resolves an empty target to the focus before it looks at
		// the tree. A split node also has an empty Pane field, so locating ""
		// would find the root split and replace the whole tree with it.
		w := s.activeWindow()
		if w == nil {
			return answer{err: errNoWindow}
		}
		target = w.Focus
	}
	ws, w, ok := s.locatePane(target)
	if !ok {
		return answer{err: fmt.Errorf("no pane %q", target)}
	}
	w.NextPane++
	id := w.ID + "p" + strconv.Itoa(w.NextPane)
	if !layout.SplitLeaf(w.Root, target, m.Dir, id, 0.5) {
		w.NextPane--
		return answer{err: fmt.Errorf("no pane %q", target)}
	}
	w.Layout = ""
	w.Focus = id
	s.activate(ws, w)
	s.zoom = ""
	return answer{changed: true, res: map[string]any{"pane": id}}
}

// closePane removes one pane. The last pane of a window takes the window with
// it, the way a shell exit does. A close that would empty the session is
// refused: a tour must not end the session it teaches.
func (s *Session) closePane(m wire.Msg) answer {
	id := m.Target
	if id == "" {
		w := s.activeWindow()
		if w == nil {
			return answer{err: errNoWindow}
		}
		id = w.Focus
	}
	_, w, ok := s.locatePane(id)
	if !ok {
		return answer{err: fmt.Errorf("no pane %q", id)}
	}
	ids := layout.AllPanes(w.Root)
	if len(ids) <= 1 {
		return s.closeWindow(wire.Msg{Target: w.ID})
	}
	at := 0
	for i, pid := range ids {
		if pid == id {
			at = i
		}
	}
	root, ok := layout.PruneLeaf(w.Root, id)
	if !ok {
		return answer{err: fmt.Errorf("no pane %q", id)}
	}
	w.Root = root
	w.Layout = ""
	if w.Focus == id {
		rest := layout.AllPanes(root)
		if at >= len(rest) {
			at = len(rest) - 1
		}
		w.Focus = rest[at]
	}
	s.zoom = ""
	return answer{changed: true}
}

// focus puts the focus on a pane anywhere in the session, which makes its
// window and its workspace active. A rail click, a tab click and a key
// therefore cannot disagree about what is in front.
func (s *Session) focus(m wire.Msg) answer {
	if m.Target == "" {
		return answer{}
	}
	ws, w, ok := s.locatePane(m.Target)
	if !ok {
		return answer{err: fmt.Errorf("no pane %q", m.Target)}
	}
	w.Focus = m.Target
	s.activate(ws, w)
	s.zoom = ""
	return answer{changed: true}
}

// toggleFold flips a workspace or a window anywhere in the session, so a rail
// caret reaches a window in a workspace that is not active.
func (s *Session) toggleFold(m wire.Msg) answer {
	id := m.Target
	if id == "" {
		// The window in front is the most specific thing an empty target can
		// name.
		w := s.activeWindow()
		if w == nil {
			return answer{err: errNoWindow}
		}
		id = w.ID
	}
	if ws, _ := s.findWorkspace(id); ws != nil {
		ws.Unfolded = !ws.Unfolded
		return answer{changed: true, res: map[string]any{"target": id, "unfolded": ws.Unfolded}}
	}
	if _, w, ok := s.locateWindow(id); ok {
		w.Unfolded = !w.Unfolded
		return answer{changed: true, res: map[string]any{"target": id, "unfolded": w.Unfolded}}
	}
	return answer{err: fmt.Errorf("no window or workspace %q", id)}
}

// zoomPane makes the focused pane cover its window, or puts the tiling back
// when it already does. One pane already fills the area, so the server answers
// that case with an empty zoom and changes nothing.
func (s *Session) zoomPane() answer {
	w := s.activeWindow()
	if w == nil {
		return answer{err: errNoWindow}
	}
	if len(layout.AllPanes(w.Root)) < 2 {
		s.zoom = ""
		return answer{res: map[string]any{"zoom": ""}}
	}
	if s.zoom == w.Focus {
		s.zoom = ""
	} else {
		s.zoom = w.Focus
	}
	return answer{changed: true, res: map[string]any{"zoom": s.zoom}}
}

// swapPanes trades the focused pane with its neighbour in dir. The ids travel
// with their content, so every rectangle keeps its shape.
func (s *Session) swapPanes(m wire.Msg) answer {
	w := s.activeWindow()
	if w == nil {
		return answer{err: errNoWindow}
	}
	other := layout.Neighbor(s.activeRects(), w.Focus, m.Dir)
	if other == "" || !layout.Swap(w.Root, w.Focus, other) {
		return answer{err: fmt.Errorf("no pane %s of %s", m.Dir, w.Focus)}
	}
	w.Layout = ""
	s.zoom = ""
	return answer{changed: true}
}

// selectLayout rebuilds the active window's tree as a preset over the same
// panes.
func (s *Session) selectLayout(m wire.Msg) answer {
	w := s.activeWindow()
	if w == nil {
		return answer{err: errNoWindow}
	}
	ids := layout.AllPanes(w.Root)
	if len(ids) < 2 {
		return answer{err: fmt.Errorf("select-layout needs at least two panes")}
	}
	name := m.Layout
	if name == "next" {
		name = presetOrder[0]
		for i, p := range presetOrder {
			if p == w.Layout {
				name = presetOrder[(i+1)%len(presetOrder)]
				break
			}
		}
	}
	// The client draws the window tab bar on row 0 and hit-tests with the
	// same helper, so the fake must keep that row free.
	area := layout.PaneAreaForWithTabBar(s.cols, s.rows, s.railWidth())
	root, ok := layout.ApplyPreset(w.Root, name, ids, area.W, area.H)
	if !ok {
		return answer{err: fmt.Errorf("unknown layout %q", name)}
	}
	w.Root = root
	w.Layout = name
	s.zoom = ""
	return answer{changed: true, res: map[string]any{"layout": name}}
}

// focusDir moves focus to the nearest pane in that direction, using the same
// geometry a click does.
func (s *Session) focusDir(m wire.Msg) answer {
	w := s.activeWindow()
	if w == nil {
		return answer{err: errNoWindow}
	}
	id := layout.Neighbor(s.activeRects(), w.Focus, m.Dir)
	if id == "" {
		return answer{err: fmt.Errorf("no pane %s of %s", m.Dir, w.Focus)}
	}
	w.Focus = id
	s.zoom = ""
	return answer{changed: true}
}

// cyclePane moves focus to the next or previous pane in tree order.
func (s *Session) cyclePane(m wire.Msg) answer {
	w := s.activeWindow()
	if w == nil {
		return answer{err: errNoWindow}
	}
	ids := layout.AllPanes(w.Root)
	if len(ids) == 0 {
		return answer{err: errors.New("no panes")}
	}
	at := 0
	for i, id := range ids {
		if id == w.Focus {
			at = i
		}
	}
	step := 1
	if m.Action == "prev-pane" {
		step = -1
	}
	w.Focus = ids[(at+step+len(ids))%len(ids)]
	s.zoom = ""
	return answer{changed: true}
}

// newWindow adds a window to the workspace holding target, or to the active
// one, and makes it active. The client sends a focused pane from a key and a
// workspace id from the rail's workspace menu, as the server accepts both.
func (s *Session) newWindow(m wire.Msg) answer {
	ws := s.activeWorkspace()
	if m.Target != "" {
		if tws, _, ok := s.locatePane(m.Target); ok {
			ws = tws
		} else if tws, _ := s.findWorkspace(m.Target); tws != nil {
			ws = tws
		} else {
			return answer{err: fmt.Errorf("no workspace or pane %q", m.Target)}
		}
	}
	if ws == nil {
		return answer{err: errors.New("no active workspace")}
	}
	w := s.addWindow(ws, "")
	s.active = s.indexOfWorkspace(ws)
	ws.Active = s.indexOfWindow(ws, w)
	s.zoom = ""
	return answer{changed: true, res: map[string]any{"window": w.ID, "pane": w.Focus}}
}

// newWorkspace adds a workspace with one window and one pane, and makes it
// active.
func (s *Session) newWorkspace(m wire.Msg) answer {
	n := s.nextWorkspace
	s.nextWorkspace++
	ws := &workspace{ID: "s" + strconv.Itoa(n), Name: "ws" + strconv.Itoa(n), Unfolded: true}
	w := s.addWindow(ws, "")
	ws.Active = 0
	s.workspaces = append(s.workspaces, ws)
	s.active = len(s.workspaces) - 1
	s.zoom = ""
	return answer{changed: true, res: map[string]any{
		"workspace": ws.ID, "window": w.ID, "pane": w.Focus,
	}}
}

// addWindow appends a window with one pane. The window and pane ids move
// forward from the session counters, as on a machine.
func (s *Session) addWindow(ws *workspace, name string) *window {
	wid := "w" + strconv.Itoa(s.nextWindow)
	s.nextWindow++
	if name == "" {
		name = wid
	}
	w := &window{
		ID: wid, Name: name,
		Root: &layout.Node{Pane: wid + "p1"}, Focus: wid + "p1",
		NextPane: 1, Unfolded: true,
	}
	ws.Windows = append(ws.Windows, w)
	return w
}

// selectWindow makes window index active in the active workspace. The index is
// zero-based, as the window-N keys and the tab bar send it.
func (s *Session) selectWindow(m wire.Msg) answer {
	ws := s.activeWorkspace()
	if ws == nil {
		return answer{err: errors.New("no active workspace")}
	}
	i := m.Index
	if i < 0 {
		i = 0
	}
	if i >= len(ws.Windows) {
		return answer{err: fmt.Errorf("window %d does not exist", m.Index)}
	}
	ws.Active = i
	s.zoom = ""
	return answer{changed: true}
}

// cycleWindow walks the active workspace's windows, wrapping at both ends.
func (s *Session) cycleWindow(m wire.Msg) answer {
	ws := s.activeWorkspace()
	if ws == nil || len(ws.Windows) == 0 {
		return answer{err: errors.New("no windows")}
	}
	step := 1
	if m.Action == "prev-window" {
		step = -1
	}
	n := len(ws.Windows)
	ws.Active = (ws.Active + step + n) % n
	s.zoom = ""
	return answer{changed: true}
}

// cycleWorkspace walks the session's workspaces, wrapping at both ends.
func (s *Session) cycleWorkspace(m wire.Msg) answer {
	n := len(s.workspaces)
	if n == 0 {
		return answer{err: errors.New("no workspaces")}
	}
	step := 1
	if m.Action == "prev-workspace" {
		step = -1
	}
	s.active = (s.active + step + n) % n
	s.zoom = ""
	return answer{changed: true}
}

// closeWindow removes a window and its panes. A window that is its workspace's
// only one takes the workspace with it. The last window of the last workspace
// is refused, so a close never empties the session a tour is teaching.
func (s *Session) closeWindow(m wire.Msg) answer {
	id := m.Target
	if id == "" {
		w := s.activeWindow()
		if w == nil {
			return answer{err: errNoWindow}
		}
		id = w.ID
	}
	ws, w, ok := s.locateWindow(id)
	if !ok {
		return answer{err: fmt.Errorf("no window %q", id)}
	}
	if len(ws.Windows) == 1 {
		if len(s.workspaces) == 1 {
			return answer{err: errors.New("cannot close the last window")}
		}
		s.removeWorkspace(s.indexOfWorkspace(ws))
		return answer{changed: true}
	}
	idx := s.indexOfWindow(ws, w)
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
	s.zoom = ""
	return answer{changed: true}
}

// closeWorkspace removes a workspace. The last workspace is refused, so a close
// never leaves the session with nothing to show.
func (s *Session) closeWorkspace(m wire.Msg) answer {
	id := m.Target
	if id == "" {
		ws := s.activeWorkspace()
		if ws == nil {
			return answer{err: errors.New("no active workspace")}
		}
		id = ws.ID
	}
	ws, idx := s.findWorkspace(id)
	if ws == nil {
		return answer{err: fmt.Errorf("no workspace %q", id)}
	}
	if len(s.workspaces) == 1 {
		return answer{err: errors.New("cannot close the last workspace")}
	}
	s.removeWorkspace(idx)
	return answer{changed: true}
}

// replay answers with the pane's raw output ring. The client replays it through
// vt.ReplayRows at the pane's own rect to build scrollback.
func (s *Session) replay(m wire.Msg) answer {
	id := m.Target
	if id == "" {
		w := s.activeWindow()
		if w == nil {
			return answer{err: errNoWindow}
		}
		id = w.Focus
	}
	pn := s.panes[id]
	if pn == nil {
		return answer{err: fmt.Errorf("no pane %q", id)}
	}
	_ = wire.WriteMsg(s.conn, wire.Msg{Type: "replay", Target: id, Data: pn.ring.Bytes()})
	return answer{res: map[string]any{"pane": id}}
}

// presetOrder is the cycle a "next" layout request walks, in the order the
// server walks it.
var presetOrder = []string{"even-horizontal", "even-vertical", "main-horizontal", "main-vertical", "tiled"}

// removeWorkspace drops workspace index idx and keeps the active index on a
// real workspace.
func (s *Session) removeWorkspace(idx int) {
	s.workspaces = append(s.workspaces[:idx], s.workspaces[idx+1:]...)
	switch {
	case idx < s.active:
		s.active--
	case idx == s.active:
		if s.active >= len(s.workspaces) {
			s.active = len(s.workspaces) - 1
		}
	}
	if s.active < 0 {
		s.active = 0
	}
	s.zoom = ""
}

// activate makes ws and w the active pair.
func (s *Session) activate(ws *workspace, w *window) {
	if ws == nil || w == nil {
		return
	}
	s.active = s.indexOfWorkspace(ws)
	ws.Active = s.indexOfWindow(ws, w)
}

// activeWorkspace is the workspace in front.
func (s *Session) activeWorkspace() *workspace {
	if len(s.workspaces) == 0 {
		return nil
	}
	if s.active < 0 || s.active >= len(s.workspaces) {
		s.active = 0
	}
	return s.workspaces[s.active]
}

// activeWindow is the window in front.
func (s *Session) activeWindow() *window {
	ws := s.activeWorkspace()
	if ws == nil || len(ws.Windows) == 0 {
		return nil
	}
	if ws.Active < 0 || ws.Active >= len(ws.Windows) {
		ws.Active = 0
	}
	return ws.Windows[ws.Active]
}

// findWorkspace finds a workspace by id.
func (s *Session) findWorkspace(id string) (*workspace, int) {
	for i, ws := range s.workspaces {
		if ws.ID == id {
			return ws, i
		}
	}
	return nil, -1
}

// locateWindow finds a window by id anywhere in the session.
func (s *Session) locateWindow(id string) (*workspace, *window, bool) {
	for _, ws := range s.workspaces {
		for _, w := range ws.Windows {
			if w.ID == id {
				return ws, w, true
			}
		}
	}
	return nil, nil, false
}

// locatePane finds the workspace and window holding a pane anywhere in the
// session.
func (s *Session) locatePane(id string) (*workspace, *window, bool) {
	for _, ws := range s.workspaces {
		for _, w := range ws.Windows {
			if layout.FindLeaf(w.Root, id) {
				return ws, w, true
			}
		}
	}
	return nil, nil, false
}

// indexOfWorkspace is ws's index, or -1.
func (s *Session) indexOfWorkspace(ws *workspace) int {
	for i, cand := range s.workspaces {
		if cand == ws {
			return i
		}
	}
	return -1
}

// indexOfWindow is w's index in ws, or -1.
func (s *Session) indexOfWindow(ws *workspace, w *window) int {
	for i, cand := range ws.Windows {
		if cand == w {
			return i
		}
	}
	return -1
}

// fit gives every pane in every window a terminal and a shell at its rect, and
// drops the panes that left. Every window is laid out at the pane area, so a
// switch finds its panes already at the right size and their shells untouched.
func (s *Session) fit() {
	live := map[string]bool{}
	for _, ws := range s.workspaces {
		for _, w := range ws.Windows {
			r := s.windowRects(w)
			for _, id := range layout.AllPanes(w.Root) {
				rect, ok := r[id]
				if !ok {
					continue
				}
				live[id] = true
				if pn := s.panes[id]; pn != nil {
					pn.resize(rect.W, rect.H)
					continue
				}
				s.panes[id] = s.newPane(rect)
			}
		}
	}
	for id := range s.panes {
		if !live[id] {
			delete(s.panes, id)
		}
	}
	for id := range s.sent {
		if !live[id] {
			delete(s.sent, id)
		}
	}
}

// newPane makes a pane's screen and its pretend shell, and draws the banner.
func (s *Session) newPane(rect layout.Rect) *pane {
	sh := webshell.New()
	sh.OnRun = func(line string) {
		if s.forward != nil {
			s.forward(map[string]any{"type": "command", "line": line})
		}
	}
	pn := &pane{term: vt.NewTerm(rect.W, rect.H), sh: sh, ring: newRing(ringBudget)}
	pn.write(sh.Greet())
	return pn
}

// windowRects lays one window out at the pane area. A zoom applies only to the
// window in front, and the zoomed pane covers the rest, which keep their places
// underneath.
func (s *Session) windowRects(w *window) map[string]layout.Rect {
	// The client draws the window tab bar on row 0 and hit-tests with the
	// same helper, so the fake must keep that row free.
	area := layout.PaneAreaForWithTabBar(s.cols, s.rows, s.railWidth())
	pad := keymap.LoadDefault().Padding()
	out := map[string]layout.Rect{}
	layout.Layout(w.Root, area, pad, out)
	if w == s.activeWindow() && s.zoom != "" {
		if _, ok := out[s.zoom]; ok {
			out[s.zoom] = layout.InsetBox(area, pad)
		}
	}
	return out
}

// activeRects is the active window's geometry: the same map a click and a key
// walk.
func (s *Session) activeRects() map[string]layout.Rect {
	if w := s.activeWindow(); w != nil {
		return s.windowRects(w)
	}
	return map[string]layout.Rect{}
}

// railWidth is the rail the screen can show: what the page asked for, clamped
// to this many columns. Zero hides it.
func (s *Session) railWidth() int {
	return layout.EffectiveRail(s.cols, s.rail)
}
