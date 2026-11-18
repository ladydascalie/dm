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
	"strings"
	"sync"
	"syscall/js"

	"dm/internal/client"
	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/vt"
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
	pending [][]byte
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
	// hidden cursor, a clear. Attach owns no terminal, so the harness opens
	// the screen it draws on.
	_, _ = b.Write([]byte("\x1b[?1049h\x1b[?25l\x1b[2J\x1b[H"))

	clientConn, serverConn := net.Pipe()
	go fakeServer(serverConn, size)
	go func() { _, _ = client.Attach(clientConn, b, keys, winch, size.get, "spike") }()

	api := map[string]any{
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
type session struct {
	size  *sizer
	root  *layout.Node
	focus string
	panes map[string]*shell
	seq   int
	// rail is the width the page asked for: 0 hidden, CollapsedRail thin,
	// DefaultRail full. The width the screen can spare is computed from it.
	rail int
}

// newSession makes the one pane a session starts with.
func newSession(size *sizer) *session {
	s := &session{size: size, panes: map[string]*shell{}, seq: 1, rail: layout.DefaultRail}
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
	out := map[string]layout.Rect{}
	layout.Layout(s.root, layout.PaneAreaFor(cols, rows, s.railWidth()), keymap.LoadDefault().Padding(), out)
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
		if sh := s.panes[id]; sh != nil {
			sh.resize(rect.W, rect.H)
			continue
		}
		sh := newShell(rect.W, rect.H)
		sh.greet()
		s.panes[id] = sh
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

// focusDir moves focus to the nearest pane in that direction, by the distance
// between the two rectangles' centres.
func (s *session) focusDir(dir string) {
	r := s.rects()
	from, ok := r[s.focus]
	if !ok {
		return
	}
	fx, fy := from.X+from.W/2, from.Y+from.H/2
	best, bestAt := "", 0
	for id, rect := range r {
		if id == s.focus {
			continue
		}
		dx := rect.X + rect.W/2 - fx
		dy := rect.Y + rect.H/2 - fy
		var ahead bool
		at := 0
		switch dir {
		case "left":
			ahead, at = dx < 0, -dx
		case "right":
			ahead, at = dx > 0, dx
		case "up":
			ahead, at = dy < 0, -dy
		case "down":
			ahead, at = dy > 0, dy
		}
		if ahead && (best == "" || at < bestAt) {
			best, bestAt = id, at
		}
	}
	if best != "" {
		s.focus = best
	}
}

// snapshot describes the session the way a server does, with the padding the
// built-in keymap asks for.
func (s *session) snapshot() *wire.Snapshot {
	cols, rows := s.size.get()
	r := s.rects()
	snap := &wire.Snapshot{
		Session:      "spike",
		Workspace:    "main",
		Windows:      []wire.WindowInfo{{ID: "w1", Name: "one"}},
		ActiveWindow: 0,
		Focus:        s.focus,
		Cols:         cols,
		Rows:         rows,
		Pad:          keymap.LoadDefault().Padding(),
		Rail:         s.railWidth(),
		RailWant:     s.rail,
		Root:         s.root,
	}
	for _, id := range layout.AllPanes(s.root) {
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
			WindowUnfolded: true, WorkspaceUnfolded: true,
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
		sh := s.panes[id]
		if sh == nil {
			continue
		}
		_ = wire.WriteMsg(c, wire.Msg{Type: "screen", Target: id, VTSnap: sh.term.Snapshot()})
	}
}

// fakeServer is the session the client talks to. It answers the frames a server
// answers, over a pipe instead of a socket.
func fakeServer(c net.Conn, size *sizer) {
	s := newSession(size)
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
			if sh := s.panes[id]; sh != nil {
				_ = wire.WriteMsg(c, wire.Msg{Type: "bytes", Target: id, Data: sh.feed(m.Keys)})
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

// shell is the fake pane program: a prompt, a line buffer and three commands.
// There is no process under it and no pty. It turns keys into the bytes a shell
// would print, which is what a step needs to check and all this spike has.
type shell struct {
	term *vt.Term
	line []rune
	// esc is where a key is in an escape sequence: 0 outside one, 1 after the
	// esc, 2 inside a CSI. A sequence is swallowed, so an arrow key does not
	// leave its tail on the line.
	esc int
}

// newShell makes a shell of the given size.
func newShell(cols, rows int) *shell {
	return &shell{term: vt.NewTerm(cols, rows)}
}

// greet draws the banner and the first prompt.
func (s *shell) greet() {
	s.emit([]byte(banner))
	s.emit([]byte(prompt))
}

// resize fits the shell's own screen to a new rect. The client does the same to
// its copy in resizeTerm, so both sides stay on the same grid.
func (s *shell) resize(cols, rows int) {
	g := s.term.Grid()
	oc, or := g.Size()
	if oc == cols && or == rows {
		return
	}
	r := g.ScrollRegion()
	g.Resize(cols, rows)
	if r.Top == 0 && r.Bottom == or-1 && r.Left == 0 && r.Right == oc-1 {
		g.SetScrollRegion(vt.ScrollRegion{Top: 0, Bottom: rows - 1, Left: 0, Right: cols - 1})
	}
}

// feed runs one read of keys and returns what the terminal should show.
func (s *shell) feed(in []byte) []byte {
	var out []byte
	for _, r := range []rune(string(in)) {
		switch {
		case s.esc == 1:
			// ESC then a byte: a two-byte sequence, and '[' or 'O' starts a
			// longer one.
			if r == '[' || r == 'O' {
				s.esc = 2
			} else {
				s.esc = 0
			}
		case s.esc == 2:
			// A CSI ends at its first final byte.
			if r >= 0x40 && r <= 0x7e {
				s.esc = 0
			}
		case r == 0x1b:
			s.esc = 1
		case r == '\r':
			out = append(out, s.emit([]byte("\r\n"))...)
			out = append(out, s.emit(s.run(string(s.line)))...)
			s.line = s.line[:0]
			out = append(out, s.emit([]byte(prompt))...)
		case r == 0x7f || r == 0x08:
			if n := len(s.line); n > 0 {
				s.line = s.line[:n-1]
				out = append(out, s.emit([]byte("\b \b"))...)
			}
		case r == 0x03:
			s.line = s.line[:0]
			out = append(out, s.emit([]byte("^C\r\n"))...)
			out = append(out, s.emit([]byte(prompt))...)
		case r == 0x04:
			// The pane outlives the shell, so ctrl+d prints what a shell says
			// and hands back a prompt instead of ending anything.
			s.line = s.line[:0]
			out = append(out, s.emit([]byte("exit\r\n"))...)
			out = append(out, s.emit([]byte(prompt))...)
		case r == 0x0c:
			out = append(out, s.emit([]byte("\x1b[2J\x1b[H"))...)
			out = append(out, s.emit([]byte(prompt))...)
			out = append(out, s.emit([]byte(string(s.line)))...)
		case r == 0x15:
			for range s.line {
				out = append(out, s.emit([]byte("\b \b"))...)
			}
			s.line = s.line[:0]
		case r < 0x20:
			// Every other control key does nothing, as at a live prompt.
		default:
			s.line = append(s.line, r)
			out = append(out, s.emit([]byte(string(r)))...)
		}
	}
	return out
}

// emit shows b and returns it, so a whole read travels to the client in one
// frame.
func (s *shell) emit(b []byte) []byte {
	s.term.Write(b)
	return b
}

// run answers one line. An unknown word is refused the way a shell refuses it,
// so a step cannot pass on a typo.
func (s *shell) run(line string) []byte {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return nil
	}
	switch fields[0] {
	case "echo":
		return []byte(strings.Join(fields[1:], " ") + "\r\n")
	case "clear":
		return []byte("\x1b[2J\x1b[H")
	case "help":
		return []byte("echo WORDS  print the words back\r\nclear       clear the screen\r\n")
	default:
		return []byte(fields[0] + ": command not found\r\n")
	}
}
