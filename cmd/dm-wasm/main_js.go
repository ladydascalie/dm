//go:build js && wasm

// Command dm-wasm runs the real dm client in a browser tab. It is the spike
// for a guided tour: proof that internal/client, which owns a terminal on a
// real machine, runs with none.
//
// The client is the shipped one. The session on the other end of the pipe is a
// fake in this binary: one window, one pane, an echo shell, no pty, no pane
// daemon and no server. See README.md for what that means and what is left.
//
// The page talks to it through one global, set before the program starts:
//
//	dm.onOutput(fn)        fn(Uint8Array) receives the frame bytes
//	dm.input(text)         keys, as a terminal would send them
//	dm.resize(cols, rows)  the renderer's size in cells
package main

import (
	"net"
	"os"
	"strconv"
	"sync"
	"syscall/js"
	"time"

	"dm/internal/client"
	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/lesson"
	"dm/internal/vt"
	"dm/internal/webshell"
	"dm/internal/wire"
)

const (
	// paneID is the fake session's only pane.
	paneID = "w1p1"
	// prompt is what the echo shell prints before every line.
	prompt = "\x1b[32mguest@dm\x1b[0m:\x1b[34m~\x1b[0m$ "
	// banner is the shell's greeting, drawn once on attach.
	banner = "\x1b[H\x1b[2Jdm, in a tab.\r\n" +
		"One window, one pane, a fake shell. There is no pty behind it and no daemon.\r\n" +
		"Type, and ctrl+b for the prefix.\r\n\r\n"
)

// bridge is the page's side of the program: the frame sink the client writes
// to, and the queues the page fills. Output written before the page registers
// a callback is held and delivered to it, so a slow page never loses a frame.
type bridge struct {
	mu      sync.Mutex
	out     js.Value
	events  js.Value
	pending [][]byte
	held    []map[string]any
}

// Write sends one frame to the page.
func (b *bridge) Write(p []byte) (int, error) {
	n := len(p)
	b.mu.Lock()
	fn := b.out
	if fn.IsUndefined() || fn.IsNull() {
		b.pending = append(b.pending, append([]byte(nil), p...))
		b.mu.Unlock()
		return n, nil
	}
	b.mu.Unlock()
	b.send(fn, p)
	return n, nil
}

// send copies b into a Uint8Array and calls fn with it.
func (b *bridge) send(fn js.Value, p []byte) {
	buf := js.Global().Get("Uint8Array").New(len(p))
	js.CopyBytesToJS(buf, p)
	fn.Invoke(buf)
}

// report sends one session event to the page and folds it into the lesson, so a
// step advances on what the session did and the page only draws the result.
func report(events chan map[string]any, l *lesson.Lesson, ev map[string]any) {
	pushEvent(events, ev)
	le := lesson.Event{}
	if v, ok := ev["type"].(string); ok {
		le.Type = v
	}
	if v, ok := ev["name"].(string); ok {
		le.Name = v
	}
	if v, ok := ev["line"].(string); ok {
		le.Line = v
	}
	if v, ok := ev["panes"].(int); ok {
		le.Panes = v
	}
	if !l.Feed(le) {
		return
	}
	if _, step, ok := l.Current(); ok {
		pushEvent(events, lessonEvent("lesson.step", l, step))
		return
	}
	pushEvent(events, map[string]any{"type": "lesson.finish", "title": l.Track().Title})
}

// lessonEvent is one step as the page draws it.
func lessonEvent(kind string, l *lesson.Lesson, step lesson.Step) map[string]any {
	keys := make([]any, 0, len(step.Keys))
	for _, k := range step.Keys {
		keys = append(keys, k.Label)
	}
	at, _, _ := l.Current()
	return map[string]any{
		"type": kind, "track": l.Track().Title, "index": at + 1,
		"total": len(l.Track().Steps), "title": step.Title,
		"note": step.Note, "hint": step.Hint, "keys": keys,
	}
}

// pushEvent queues one event for the page, dropping it when nobody is reading:
// a lesson must never stall the session behind a slow card.
func pushEvent(events chan map[string]any, ev map[string]any) {
	select {
	case events <- ev:
	default:
	}
}

// drainEvents hands events to the page's callback as they arrive. Events that
// came before the page registered are held and delivered in order.
func drainEvents(b *bridge, events chan map[string]any) {
	for ev := range events {
		b.mu.Lock()
		fn := b.events
		if fn.IsUndefined() || fn.IsNull() {
			b.held = append(b.held, ev)
			b.mu.Unlock()
			continue
		}
		b.mu.Unlock()
		fn.Invoke(js.ValueOf(ev))
	}
}

// lastReq is the request the fake handled last, for the page or a test that
// wants to see what a click sent.
var lastReq struct {
	mu sync.Mutex
	s  string
}

// recordLast notes one request.
func recordLast(s string) {
	lastReq.mu.Lock()
	lastReq.s = s
	lastReq.mu.Unlock()
}

// sizer is the renderer's size in cells, written by the page and read by the
// client on the resize signal.
type sizer struct {
	mu   sync.Mutex
	cols int
	rows int
}

// get reads the size.
func (s *sizer) get() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cols, s.rows
}

// set writes the size, refusing one too small to lay out.
func (s *sizer) set(cols, rows int) {
	if cols < 20 || rows < 6 {
		return
	}
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	s.mu.Unlock()
}

// resizeSignal is the winch the page sends. A browser has no SIGWINCH, and the
// client only reads the channel, so the signal is a marker with no meaning.
type resizeSignal struct{}

func (resizeSignal) String() string { return "resize" }
func (resizeSignal) Signal()        {}

func main() {
	size := &sizer{cols: 100, rows: 30}
	b := &bridge{}
	keys := make(chan []byte, 64)
	winch := make(chan os.Signal, 1)

	// The prologue Run writes on a real machine: the alternate screen, a
	// hidden cursor, a clear, and the mouse modes, without which the rail is
	// never sent a click. Attach owns no terminal, so the harness opens the
	// screen it draws on.
	_, _ = b.Write([]byte("\x1b[?1049h\x1b[?25l\x1b[2J\x1b[H"))
	_, _ = b.Write([]byte("\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1006h\x1b[?2004h"))

	clientConn, serverConn := net.Pipe()
	events := make(chan map[string]any, 64)
	track := lesson.Tracks[0]
	l := lesson.Start(track)
	if _, step, ok := l.Current(); ok {
		pushEvent(events, lessonEvent("lesson.step", l, step))
	}
	forward := func(ev map[string]any) { report(events, l, ev) }
	go fakeServer(serverConn, size, events, forward)
	go func() {
		h := client.Harness{
			Conn: clientConn, Out: b, Keys: keys, Winch: winch,
			Size: size.get, Session: "spike",
			OnEvent: func(e client.Event) {
				ev := map[string]any{"type": e.Type}
				if e.Name != "" {
					ev["name"] = e.Name
				}
				if e.Type == "state" {
					ev["panes"] = e.Panes
					ev["focus"] = e.Focus
					ev["rail"] = e.Rail
					ev["zoom"] = e.Zoom
					ev["sync"] = e.Sync
				}
				forward(ev)
			},
		}
		_, _ = client.Attach(h)
	}()
	go drainEvents(b, events)

	// The ladder: one timer for the whole tour, reporting each rung once per
	// step. The page draws what it hears.
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		last := lesson.NoHelp
		for now := range t.C {
			level := l.Ladder(now)
			if level == last {
				continue
			}
			last = level
			if at, step, ok := l.Current(); ok {
				pushEvent(events, map[string]any{
					"type": "lesson.hint", "index": at + 1, "level": int(level),
					"hint": step.Hint,
				})
			}
		}
	}()

	api := map[string]any{
		"showMe": js.FuncOf(func(_ js.Value, args []js.Value) any {
			// Show me presses the keys through the same channel the page types
			// on, one at a time, so the caps light and the step advances the
			// way it does for a person.
			stepKeys := l.Keys()
			go func() {
				for _, k := range stepKeys {
					select {
					case keys <- []byte(k.Bytes):
					case <-time.After(time.Second):
					}
					time.Sleep(250 * time.Millisecond)
				}
			}()
			return nil
		}),
		"onOutput": js.FuncOf(func(_ js.Value, args []js.Value) any {
			b.mu.Lock()
			b.out = args[0]
			held := b.pending
			b.pending = nil
			b.mu.Unlock()
			for _, p := range held {
				b.send(b.out, p)
			}
			return nil
		}),
		"input": js.FuncOf(func(_ js.Value, args []js.Value) any {
			select {
			case keys <- []byte(args[0].String()):
			default: // a page that types faster than the client reads
			}
			return nil
		}),
		"onEvent": js.FuncOf(func(_ js.Value, args []js.Value) any {
			b.mu.Lock()
			b.events = args[0]
			held := b.held
			b.held = nil
			b.mu.Unlock()
			for _, ev := range held {
				b.events.Invoke(js.ValueOf(ev))
			}
			return nil
		}),
		"last": js.FuncOf(func(_ js.Value, args []js.Value) any {
			lastReq.mu.Lock()
			defer lastReq.mu.Unlock()
			return lastReq.s
		}),
		"resize": js.FuncOf(func(_ js.Value, args []js.Value) any {
			size.set(args[0].Int(), args[1].Int())
			select {
			case winch <- resizeSignal{}:
			default:
			}
			return nil
		}),
	}
	js.Global().Set("dm", js.ValueOf(api))
	if ready := js.Global().Get("onDmReady"); ready.Type() == js.TypeFunction {
		ready.Invoke(js.Global().Get("dm"))
	}
	select {}
}

// session is what a server holds for one window: a layout tree, the pretend
// shell behind each pane, and which pane has focus. It is small on purpose. The
// real server is not in this binary yet, so this stands in for the parts a
// lesson presses on.
// pane is one pretend pane: the terminal it drew and the shell behind it.
type pane struct {
	term *vt.Term
	sh   *webshell.Shell
}

// write puts bytes on the pane's screen and returns them, so a read travels to
// the client in one frame.
func (p *pane) write(b []byte) []byte {
	p.term.Write(b)
	return b
}

// resize fits the pane's screen to a new rect. The client does the same to its
// copy in resizeTerm, so both sides stay on the same grid.
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
	// A narrower terminal cannot rewrap what it already printed, so the shell
	// draws its screen again at the new width. Without this a split clips the
	// right half of every line and a zoom never brings it back.
	p.write(p.sh.Repaint(rows))
}

type session struct {
	size  *sizer
	root  *layout.Node
	focus string
	panes map[string]*pane
	seq   int
	// rail is the width the page asked for: 0 hidden, CollapsedRail thin,
	// DefaultRail full. The width the screen can spare is computed from it.
	rail int
	// winFolded and wsFolded are the rail's fold state, which the server owns
	// and a click on a triangle asks it to flip.
	winFolded bool
	wsFolded  bool
	// preset is the layout name last applied, empty for a hand made tree. A
	// "next" request walks the order from it.
	preset string
	// zoom is the pane drawn over the whole area, empty for none.
	zoom string
	// sync is the window's synchronize-panes flag: keys reach every pane.
	sync bool
	// events carries what the shells ran to the page, which is how a lesson
	// sees a command and not just a key.
	events  chan map[string]any
	forward func(map[string]any)
}

// newSession makes the one pane a session starts with.
func newSession(size *sizer, events chan map[string]any, forward func(map[string]any)) *session {
	s := &session{size: size, panes: map[string]*pane{}, seq: 1, rail: layout.DefaultRail, events: events, forward: forward}
	s.root = &layout.Node{Pane: "w1p1"}
	s.focus = "w1p1"
	s.fit()
	return s
}

// addPane makes the next pane id. Ids only move forward, as on a real machine.
func (s *session) addPane() string {
	s.seq++
	return "w1p" + strconv.Itoa(s.seq)
}

// rects lays the tree out for the renderer's size.
func (s *session) rects() map[string]layout.Rect {
	cols, rows := s.size.get()
	pad := keymap.LoadDefault().Padding()
	area := layout.PaneAreaFor(cols, rows, s.railWidth())
	out := map[string]layout.Rect{}
	layout.Layout(s.root, area, pad, out)
	if _, ok := out[s.zoom]; ok {
		// A zoomed pane covers the rest, which keep their places underneath.
		out[s.zoom] = layout.InsetBox(area, pad)
	}
	return out
}

// fit gives every pane a shell at its rect and drops the shells of panes that
// left. A shell that already exists is resized, so a split does not wipe the
// pane next to it.
func (s *session) fit() {
	r := s.rects()
	for id := range s.panes {
		if _, ok := r[id]; !ok {
			delete(s.panes, id)
		}
	}
	for _, id := range layout.AllPanes(s.root) {
		rect, ok := r[id]
		if !ok {
			continue
		}
		if pn := s.panes[id]; pn != nil {
			pn.resize(rect.W, rect.H)
			continue
		}
		sh := webshell.New()
		sh.OnRun = func(line string) {
			s.forward(map[string]any{"type": "command", "line": line})
		}
		pn := &pane{term: vt.NewTerm(rect.W, rect.H), sh: sh}
		pn.write(sh.Greet())
		s.panes[id] = pn
	}
}

// railWidth is the rail the screen can show: what the page asked for, clamped
// to this many columns. Zero hides it.
func (s *session) railWidth() int {
	cols, _ := s.size.get()
	return layout.EffectiveRail(cols, s.rail)
}

// handle runs one request. It reports whether the screen changed, so the caller
// sends a snapshot only when there is something new to see.
func (s *session) handle(m wire.Msg) bool {
	recordLast(m.Action + " " + m.Target + " " + m.Layout)
	switch m.Action {
	case "split":
		id := s.addPane()
		if !layout.SplitLeaf(s.root, m.Target, m.Dir, id, 0.5) {
			return false
		}
		s.focus = id
		s.fit()
		return true
	case "close-pane":
		if len(layout.AllPanes(s.root)) <= 1 {
			// The last pane keeps the window open. A real machine would end
			// the session here, and a tour should never end one.
			return false
		}
		root, ok := layout.PruneLeaf(s.root, m.Target)
		if !ok {
			return false
		}
		s.root = root
		if s.focus == m.Target {
			rest := layout.AllPanes(s.root)
			s.focus = rest[len(rest)-1]
		}
		s.fit()
		return true
	case "set-rail":
		// The page asks for a width; the screen decides how much of it fits.
		// The panes change width with it, so their shells are resized before
		// the next picture is taken.
		s.rail = m.Index
		s.fit()
		return true
	case "focus":
		// A click on a rail row names the pane to focus.
		if _, ok := s.rects()[m.Target]; ok {
			s.focus = m.Target
			return true
		}
		return false
	case "toggle-fold":
		// The rail's triangles ask the server to flip a workspace or a window.
		switch m.Target {
		case "s1":
			s.wsFolded = !s.wsFolded
			return true
		case "w1":
			s.winFolded = !s.winFolded
			return true
		}
		return false
	case "zoom-pane":
		if s.zoom == s.focus {
			s.zoom = ""
		} else {
			s.zoom = s.focus
		}
		s.fit()
		return true
	case "swap-panes":
		other := s.neighbor(m.Dir)
		if other == "" || !layout.Swap(s.root, s.focus, other) {
			return false
		}
		// The ids travel with their content, so the two panes change places
		// and the focus stays on the same pane.
		s.preset = ""
		s.fit()
		return true
	case "synchronize-panes":
		if m.On != nil {
			s.sync = *m.On
		} else {
			s.sync = !s.sync
		}
		return true
	case "select-layout":
		return s.setLayout(m.Layout)
	case "focus-dir":
		s.focusDir(m.Dir)
		return true
	case "next-pane", "prev-pane":
		ids := layout.AllPanes(s.root)
		if len(ids) < 2 {
			return false
		}
		at := 0
		for i, id := range ids {
			if id == s.focus {
				at = i
			}
		}
		if m.Action == "next-pane" {
			at = (at + 1) % len(ids)
		} else {
			at = (at - 1 + len(ids)) % len(ids)
		}
		s.focus = ids[at]
		return true
	}
	return false
}

// presetOrder is the cycle a "next" layout request walks, in the same order
// the server walks it.
var presetOrder = []string{"even-horizontal", "even-vertical", "main-horizontal", "main-vertical", "tiled"}

// setLayout rebuilds the tree as a preset over the same panes, which is what
// the layout picker and the cycle key ask the server for.
func (s *session) setLayout(name string) bool {
	ids := layout.AllPanes(s.root)
	if len(ids) < 2 {
		// Nothing to arrange. A real machine refuses for the same reason.
		return false
	}
	if name == "next" {
		name = presetOrder[0]
		for i, p := range presetOrder {
			if p == s.preset {
				name = presetOrder[(i+1)%len(presetOrder)]
				break
			}
		}
	}
	cols, rows := s.size.get()
	area := layout.PaneAreaFor(cols, rows, s.railWidth())
	root, ok := layout.ApplyPreset(s.root, name, ids, area.W, area.H)
	if !ok {
		return false
	}
	s.root = root
	s.preset = name
	s.fit()
	return true
}

// focusDir moves focus to the nearest pane in that direction.
func (s *session) focusDir(dir string) {
	if id := s.neighbor(dir); id != "" {
		s.focus = id
	}
}

// neighbor is the pane the server would walk to in that direction. The
// geometry is layout.Neighbor, the same call the server makes, so a click and a
// key cannot disagree about which pane is next.
func (s *session) neighbor(dir string) string {
	return layout.Neighbor(s.rects(), s.focus, dir)
}

// targets is the panes one read of keys reaches. A synced window takes them in
// every pane of the window, as on a machine.
func (s *session) targets(id string) []string {
	if !s.sync {
		return []string{id}
	}
	out := []string{}
	for _, p := range layout.AllPanes(s.root) {
		if p != id {
			out = append(out, p)
		}
	}
	return append([]string{id}, out...)
}

// snapshot describes the session the way a server does, with the padding the
// built-in keymap asks for. A zoomed pane is drawn last, because the client
// paints in this order and the zoom has to cover the tiling under it.
func (s *session) snapshot() *wire.Snapshot {
	cols, rows := s.size.get()
	r := s.rects()
	snap := &wire.Snapshot{
		Session:      "spike",
		Workspace:    "main",
		Windows:      []wire.WindowInfo{{ID: "w1", Name: "one"}},
		ActiveWindow: 0,
		Focus:        s.focus,
		Zoom:         s.zoom,
		Sync:         s.sync,
		Cols:         cols,
		Rows:         rows,
		Pad:          keymap.LoadDefault().Padding(),
		Rail:         s.railWidth(),
		RailWant:     s.rail,
		Root:         s.root,
	}
	ids := layout.AllPanes(s.root)
	for i, id := range ids {
		if id == s.zoom {
			ids = append(append(append([]string{}, ids[:i]...), ids[i+1:]...), id)
			break
		}
	}
	for _, id := range ids {
		rect, ok := r[id]
		if !ok {
			continue
		}
		snap.Panes = append(snap.Panes, wire.PaneInfo{ID: id, Rect: rect})
		// The rail folds a workspace or a window that is not unfolded, so a
		// snapshot that leaves these false draws a rail with no rows in it.
		snap.All = append(snap.All, wire.AllPane{
			ID: id, Window: "w1", WindowName: "one",
			Workspace: "s1", WorkspaceName: "main",
			Status: "idle", Title: "sh", Cwd: "/home/guest",
			WindowUnfolded: !s.winFolded, WorkspaceUnfolded: !s.wsFolded,
			WorkspaceActive: true, WindowActive: true,
			Focus: id == s.focus,
		})
	}
	return snap
}

// push sends the picture: the snapshot, then the screen of every pane. Every
// pane, not only the new one, because the client resizes and lends its own
// terminals and the two sides must agree on what is on screen.
func (s *session) push(c net.Conn) {
	_ = wire.WriteMsg(c, wire.Msg{Type: "snapshot", Snap: s.snapshot()})
	for _, id := range layout.AllPanes(s.root) {
		pn := s.panes[id]
		if pn == nil {
			continue
		}
		_ = wire.WriteMsg(c, wire.Msg{Type: "screen", Target: id, VTSnap: pn.term.Snapshot()})
	}
}

// fakeServer is the session the client talks to. It answers the frames a server
// answers, over a pipe instead of a socket.
func fakeServer(c net.Conn, size *sizer, events chan map[string]any, forward func(map[string]any)) {
	s := newSession(size, events, forward)
	s.push(c)

	for {
		m, err := wire.ReadMsg(c)
		if err != nil {
			return
		}
		switch m.Type {
		case "attach", "resize":
			if m.Type == "attach" || (m.Cols > 20 && m.Rows > 5) {
				s.fit()
				s.push(c)
			}
		case "keys":
			id := m.Target
			if id == "" {
				id = s.focus
			}
			for _, id := range s.targets(id) {
				if pn := s.panes[id]; pn != nil {
					_ = wire.WriteMsg(c, wire.Msg{Type: "bytes", Target: id, Data: pn.write(pn.sh.Feed(m.Keys))})
				}
			}
		case "req":
			if s.handle(m) {
				s.push(c)
			} else {
				// A request the spike does not serve gets an empty reply, so a
				// client waiting on one is not left hanging.
				_ = wire.WriteMsg(c, wire.Msg{Type: "resp", Action: m.Action, OK: true})
			}
		}
	}
}
