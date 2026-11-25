package server

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"dm/internal/layout"
	"dm/internal/session"
	"dm/internal/vt"
	"dm/internal/wire"
)

// The sizer tests run the real serve loop over pipes, so attach, resize and
// detach take the wiring a socket client takes. Each pane is a fake daemon on
// its own socket: it records every resize the server sends, which is the
// winsize its pty would take, and answers the screen and reflow requests the
// attach path asks for.

// sizerDaemon is one fake pane daemon: its current size and the resize frames
// it received, in order.
type sizerDaemon struct {
	id string

	mu         sync.Mutex
	cols       int
	rows       int
	sizes      [][2]int
	reflowReqs [][2]int
}

func newSizerDaemon(t *testing.T, st *session.State, id string) *sizerDaemon {
	t.Helper()
	d := &sizerDaemon{id: id}
	sock := st.PaneSock(id)
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen %s: %v", sock, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go d.serve(c)
		}
	}()
	return d
}

// serve answers one conn the way a pane daemon does: a resize sets the size, a
// screen answers with a grid of that size, and anything else gets a good reply.
func (d *sizerDaemon) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	for {
		m, err := wire.ReadMsg(c)
		if err != nil {
			return
		}
		switch m.Action {
		case "resize":
			d.mu.Lock()
			d.cols, d.rows = m.Cols, m.Rows
			d.sizes = append(d.sizes, [2]int{m.Cols, m.Rows})
			d.mu.Unlock()
		case "reflow":
			// The picture handshake: the server asks once per settled size
			// and the answer is the screen the "screen" case already sends.
			d.mu.Lock()
			d.reflowReqs = append(d.reflowReqs, [2]int{m.Cols, m.Rows})
			d.mu.Unlock()
			fallthrough
		case "screen":
			d.mu.Lock()
			cols, rows := d.cols, d.rows
			d.mu.Unlock()
			if cols < 1 {
				cols = 80
			}
			if rows < 1 {
				rows = 24
			}
			term := vt.NewTerm(cols, rows)
			term.Write([]byte("pane " + d.id))
			term.Flush()
			_ = wire.WriteMsg(c, wire.Msg{Type: "screen", ID: m.ID, Target: d.id, VTSnap: term.Snapshot()})
		case "replay":
			st := wire.Status{State: "idle"}
			_ = wire.WriteMsg(c, wire.Msg{Type: "replay", ID: m.ID, Target: d.id, Data: []byte("ring"), St: &st})
		default:
			_ = wire.WriteMsg(c, wire.Msg{Type: "resp", ID: m.ID, OK: true, Cwd: os.TempDir()})
		}
	}
}

func (d *sizerDaemon) size() (int, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cols, d.rows
}

func (d *sizerDaemon) resizes() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.sizes)
}

// reflows is the number of picture handshakes the server asked for, and the
// size each one named.
func (d *sizerDaemon) reflows() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.reflowReqs)
}

// sizerFixture is one server with two layout panes side by side, each behind a
// fake daemon, and the real serve loop for every client.
type sizerFixture struct {
	s       *server
	a, b    string
	demons  map[string]*sizerDaemon
	clients []*sizerClient
}

func newSizerFixture(t *testing.T, name string) *sizerFixture {
	t.Helper()
	st := session.NewStateAt(t.TempDir(), name)
	w := st.ActiveWindow()
	a := w.NewPaneID()
	b := w.NewPaneID()
	w.Root = &layout.Node{Dir: "row", Kids: []*layout.Node{{Pane: a}, {Pane: b}}}
	w.Focus = a
	st.Panes[a] = &session.PaneState{}
	st.Panes[b] = &session.PaneState{}
	if err := os.MkdirAll(st.SessionDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	f := &sizerFixture{s: railServer(st), a: a, b: b, demons: map[string]*sizerDaemon{}}
	f.s.reflowSettle = 10 * time.Millisecond
	for _, id := range []string{a, b} {
		f.demons[id] = newSizerDaemon(t, st, id)
		if err := f.s.ensurePane(id); err != nil {
			t.Fatalf("ensurePane %s: %v", id, err)
		}
	}
	t.Cleanup(func() {
		// The client cleanups have closed their conns by now. Let each serve
		// loop finish its detach bookkeeping before the temp dir goes: a
		// detach that reverts a claim saves state, and a save that lands after
		// the removal fails the test. The serve loop closes its own side of
		// the conn as its last act, so that close is the signal that no write
		// to the state directory is left.
		for _, c := range f.clients {
			select {
			case <-c.served:
			case <-time.After(3 * time.Second):
				t.Errorf("a client serve loop never finished its detach")
			}
		}
		f.s.mu.Lock()
		for _, pc := range f.s.panes {
			_ = pc.c.Close()
		}
		f.s.mu.Unlock()
	})
	return f
}

// servedConn is the server side of a client conn. The serve loop closes it as
// its last act, after the detach bookkeeping and the state save that goes with
// it, so a fixture that waits for the close cannot remove the state directory
// under a write.
type servedConn struct {
	net.Conn
	served chan struct{}
	once   sync.Once
}

func (c *servedConn) Close() error {
	c.once.Do(func() { close(c.served) })
	return c.Conn.Close()
}

// client attaches a client to the fixture's serve loop and collects the frames
// the server sends it.
type sizerClient struct {
	conn   net.Conn
	frames chan wire.Msg
	// served closes when the serve loop returns.
	served chan struct{}
}

func (f *sizerFixture) client(t *testing.T, cols, rows int) *sizerClient {
	t.Helper()
	srv, cli := net.Pipe()
	served := &servedConn{Conn: srv, served: make(chan struct{})}
	go f.s.serve(served)
	t.Cleanup(func() { _ = cli.Close() })
	c := &sizerClient{conn: cli, frames: make(chan wire.Msg, 256), served: served.served}
	f.clients = append(f.clients, c)
	go func() {
		for {
			m, err := wire.ReadMsg(cli)
			if err != nil {
				close(c.frames)
				return
			}
			c.frames <- m
		}
	}()
	if err := wire.WriteMsg(cli, wire.Msg{Type: "attach", Cols: cols, Rows: rows}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	return c
}

func (c *sizerClient) send(t *testing.T, m wire.Msg) {
	t.Helper()
	if err := wire.WriteMsg(c.conn, m); err != nil {
		t.Fatalf("send %+v: %v", m, err)
	}
}

// waitSizer reads frames until the server says whether this client sizes the
// panes and reports that verdict.
func (c *sizerClient) waitSizer(t *testing.T, want bool) bool {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case m, ok := <-c.frames:
			if !ok {
				t.Fatal("the client conn closed before a sizer frame")
			}
			if m.Type != "sizer" || m.Sizing == nil {
				continue
			}
			return *m.Sizing
		case <-deadline:
			t.Fatalf("no sizer frame arrived; wanted sizing=%v", want)
		}
	}
}

// waitClients blocks until the server has n clients attached, so a detach has
// finished its bookkeeping before the test reads the outcome.
func (f *sizerFixture) waitClients(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f.s.mu.Lock()
		got := len(f.s.clients)
		f.s.mu.Unlock()
		if got == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the server never held %d clients", n)
}

// screen waits for the next screen frame for a pane and proves it carries cols
// x rows. It reports how many screen frames for that pane it read on the way,
// and fails on one at another size: a client that is not the sizer never
// resizes a terminal, so this frame is the only way it learns a pane's size.
func (c *sizerClient) screen(t *testing.T, id string, cols, rows int) int {
	t.Helper()
	seen := 0
	deadline := time.After(3 * time.Second)
	for {
		select {
		case m, ok := <-c.frames:
			if !ok {
				t.Fatalf("the client conn closed before a %dx%d screen frame for %s", cols, rows, id)
			}
			if m.Type != "screen" || m.Target != id {
				continue
			}
			seen++
			if m.VTSnap == nil {
				t.Fatalf("a screen frame for %s carried no snapshot", id)
			}
			if m.VTSnap.Cols != cols || m.VTSnap.Rows != rows {
				t.Fatalf("%s screen is %dx%d, want %dx%d", id, m.VTSnap.Cols, m.VTSnap.Rows, cols, rows)
			}
			return seen
		case <-deadline:
			t.Fatalf("no %dx%d screen frame for %s arrived: the client keeps its old grid", cols, rows, id)
		}
	}
}

// expectNoScreen fails if another screen frame for id arrives within wait.
func (c *sizerClient) expectNoScreen(t *testing.T, id string, wait time.Duration) {
	t.Helper()
	deadline := time.After(wait)
	for {
		select {
		case m, ok := <-c.frames:
			if !ok {
				return
			}
			if m.Type == "screen" && m.Target == id {
				t.Fatalf("a second screen frame for %s arrived for one claim", id)
			}
		case <-deadline:
			return
		}
	}
}

// readScreen reads the next screen frame for a pane, whatever size it carries.
// The attach asks for its picture on a fresh connection, so that request can
// beat the resize that follows it, and the answer then carries the size the
// pane held before the attach. Only the picture handshake is about the size the
// pane holds now, so an attach picture is read by target alone.
func (c *sizerClient) readScreen(t *testing.T, id string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case m, ok := <-c.frames:
			if !ok {
				t.Fatalf("the client conn closed before a screen frame for %s", id)
			}
			if m.Type != "screen" || m.Target != id {
				continue
			}
			if m.VTSnap == nil {
				t.Fatalf("a screen frame for %s carried no snapshot", id)
			}
			return
		case <-deadline:
			t.Fatalf("no screen frame for %s arrived", id)
		}
	}
}

// waitScreens reads frames for a pane until it has taken n screen frames for it
// and proves each carries cols x rows. It is how a test consumes the pictures
// an attach already owed the client, so the next frame it reads is the one the
// step under test costs.
func (c *sizerClient) waitScreens(t *testing.T, id string, n, cols, rows int) {
	t.Helper()
	got := 0
	deadline := time.After(3 * time.Second)
	for got < n {
		select {
		case m, ok := <-c.frames:
			if !ok {
				t.Fatalf("the client conn closed after %d of %d screen frames for %s", got, n, id)
			}
			if m.Type != "screen" || m.Target != id {
				continue
			}
			if m.VTSnap == nil {
				t.Fatalf("a screen frame for %s carried no snapshot", id)
			}
			if m.VTSnap.Cols != cols || m.VTSnap.Rows != rows {
				t.Fatalf("%s screen is %dx%d, want %dx%d", id, m.VTSnap.Cols, m.VTSnap.Rows, cols, rows)
			}
			got++
		case <-deadline:
			t.Fatalf("%d of %d %dx%d screen frames for %s arrived", got, n, cols, rows, id)
		}
	}
}

// settlePicture runs at once the picture handshake the server has armed for a
// pane, and reports whether one was armed. Every size change arms one; running
// it here rather than waiting for the timer keeps the frames a step costs in
// the test's hands. A timer also races the resize it belongs to: the resize
// rides the pane's own conn while the handshake dials a fresh one, so on a
// loaded machine the pane can answer the handshake before it has taken the
// resize, and the picture then carries the size the pane is about to leave.
func (f *sizerFixture) settlePicture(t *testing.T, id string) bool {
	t.Helper()
	f.s.mu.Lock()
	armed := f.s.reflowWait[id]
	f.s.mu.Unlock()
	if !armed {
		return false
	}
	f.s.settleReflow()
	return true
}

// waitSize waits until the daemon holds cols x rows and reports the size it had
// before that resize.
func (f *sizerFixture) waitSize(t *testing.T, id string, cols, rows int) (int, int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, r := f.demons[id].size(); c == cols && r == rows {
			return c, r
		}
		time.Sleep(time.Millisecond)
	}
	c, r := f.demons[id].size()
	t.Fatalf("pane %s is %dx%d, want %dx%d", id, c, r, cols, rows)
	return 0, 0
}

// expectNoResize proves a frame changed nothing: the pane's daemon receives no
// resize and keeps the size it holds. It waits past the reflow settle, so a
// resize that only lands late still fails the test.
func (f *sizerFixture) expectNoResize(t *testing.T, id string, cols, rows int) {
	t.Helper()
	before := f.demons[id].resizes()
	time.Sleep(60 * time.Millisecond)
	if got := f.demons[id].resizes(); got != before {
		t.Fatalf("pane %s received a resize, want none", id)
	}
	if c, r := f.demons[id].size(); c != cols || r != rows {
		t.Fatalf("pane %s is %dx%d, want the unchanged %dx%d", id, c, r, cols, rows)
	}
}

// rectOf is one pane's layout rectangle in the server's current snapshot.
func rectOf(t *testing.T, s *server, id string) layout.Rect {
	t.Helper()
	for _, p := range snapshot(s).Panes {
		if p.ID == id {
			return p.Rect
		}
	}
	t.Fatalf("no pane %s in the snapshot", id)
	return layout.Rect{}
}

// TestClaimResizesOnePane: the sizing client's claim sizes that pane's pty and
// leaves its sibling's alone.
func TestClaimResizesOnePane(t *testing.T) {
	t.Parallel()
	f := newSizerFixture(t, "sizerclaim")
	c := f.client(t, 100, 30)
	if !c.waitSizer(t, true) {
		t.Fatal("the first client to attach was not made the sizing client")
	}
	f.waitClients(t, 1)

	ra := rectOf(t, f.s, f.a)
	rb := rectOf(t, f.s, f.b)
	f.waitSize(t, f.a, ra.W, ra.H)
	f.waitSize(t, f.b, rb.W, rb.H)
	sibling := f.demons[f.b].resizes()
	t.Logf("before: %s %dx%d, %s %dx%d", f.a, ra.W, ra.H, f.b, rb.W, rb.H)

	c.send(t, wire.Msg{Type: "resize", Target: f.a, Cols: 94, Rows: 24})
	f.waitSize(t, f.a, 94, 24)
	if c, r := f.demons[f.b].size(); c != rb.W || r != rb.H {
		t.Fatalf("the sibling pty moved to %dx%d", c, r)
	}
	if got := f.demons[f.b].resizes(); got != sibling {
		t.Fatalf("the sibling received %d resize frames, want the %d it had", got-sibling, 0)
	}
	f.s.mu.Lock()
	cols, rows := f.s.st.Panes[f.a].Cols, f.s.st.Panes[f.a].Rows
	f.s.mu.Unlock()
	if cols != 94 || rows != 24 {
		t.Fatalf("the pane state is %dx%d, want 94x24", cols, rows)
	}
	t.Logf("after: %s 94x24, %s %dx%d", f.a, f.b, rb.W, rb.H)
	if got := rectOf(t, f.s, f.a); got != ra {
		t.Fatalf("the claim moved the layout rectangle to %+v, want %+v", got, ra)
	}
}

// TestSecondAttachTakesOver: the newest client sizes the panes, the older one is
// told it no longer does, its claims are dropped and its later claim is refused.
func TestSecondAttachTakesOver(t *testing.T) {
	t.Parallel()
	f := newSizerFixture(t, "sizertakeover")
	first := f.client(t, 100, 30)
	if !first.waitSizer(t, true) {
		t.Fatal("the first client was not made the sizing client")
	}
	f.waitClients(t, 1)
	ra := rectOf(t, f.s, f.a)
	f.waitSize(t, f.a, ra.W, ra.H)

	first.send(t, wire.Msg{Type: "resize", Target: f.a, Cols: 94, Rows: 24})
	f.waitSize(t, f.a, 94, 24)
	t.Logf("before the second attach: %s 94x24", f.a)

	// The same screen size, so the layout rectangles do not move and the claim
	// is the only thing that changes.
	second := f.client(t, 100, 30)
	if !second.waitSizer(t, true) {
		t.Fatal("the second client was not made the sizing client")
	}
	if first.waitSizer(t, true) {
		t.Fatal("the first client was still told it sizes the panes")
	}
	f.waitClients(t, 2)
	f.waitSize(t, f.a, ra.W, ra.H)
	t.Logf("after the second attach: %s %dx%d", f.a, ra.W, ra.H)

	// The old sizing client's claim is refused, and nothing moves.
	first.send(t, wire.Msg{Type: "resize", Target: f.a, Cols: 70, Rows: 20})
	f.expectNoResize(t, f.a, ra.W, ra.H)

	second.send(t, wire.Msg{Type: "resize", Target: f.a, Cols: 94, Rows: 24})
	f.waitSize(t, f.a, 94, 24)
	t.Logf("after the new client's claim: %s 94x24", f.a)
}

// TestDetachSizerRevertsToRemainingClient: when the sizing client goes, the most
// recently attached remaining client takes over, and the panes fall back to the
// layout until it claims.
func TestDetachSizerRevertsToRemainingClient(t *testing.T) {
	t.Parallel()
	f := newSizerFixture(t, "sizerdetach")
	first := f.client(t, 100, 30)
	if !first.waitSizer(t, true) {
		t.Fatal("the first client was not made the sizing client")
	}
	second := f.client(t, 100, 30)
	if !second.waitSizer(t, true) {
		t.Fatal("the second client was not made the sizing client")
	}
	if first.waitSizer(t, true) {
		t.Fatal("the first client was still told it sizes the panes")
	}
	f.waitClients(t, 2)
	ra := rectOf(t, f.s, f.a)
	f.waitSize(t, f.a, ra.W, ra.H)

	second.send(t, wire.Msg{Type: "resize", Target: f.a, Cols: 94, Rows: 24})
	f.waitSize(t, f.a, 94, 24)
	t.Logf("before the detach: %s 94x24", f.a)

	_ = second.conn.Close()
	f.waitClients(t, 1)
	if !first.waitSizer(t, true) {
		t.Fatal("the remaining client was not handed the sizing role")
	}
	f.waitSize(t, f.a, ra.W, ra.H)
	t.Logf("after the detach: %s %dx%d", f.a, ra.W, ra.H)

	// The new sizing client's claim is honoured.
	first.send(t, wire.Msg{Type: "resize", Target: f.a, Cols: 90, Rows: 20})
	f.waitSize(t, f.a, 90, 20)
	t.Logf("after the remaining client's claim: %s 90x20", f.a)
}

// TestNonSizerClaimIsRefused: only the sizing client's claim reaches a pty.
func TestNonSizerClaimIsRefused(t *testing.T) {
	t.Parallel()
	f := newSizerFixture(t, "sizernonsizer")
	first := f.client(t, 100, 30)
	if !first.waitSizer(t, true) {
		t.Fatal("the first client was not made the sizing client")
	}
	second := f.client(t, 100, 30)
	if !second.waitSizer(t, true) {
		t.Fatal("the second client was not made the sizing client")
	}
	if first.waitSizer(t, true) {
		t.Fatal("the first client was still told it sizes the panes")
	}
	f.waitClients(t, 2)
	ra := rectOf(t, f.s, f.a)
	f.waitSize(t, f.a, ra.W, ra.H)

	first.send(t, wire.Msg{Type: "resize", Target: f.a, Cols: 94, Rows: 24})
	f.expectNoResize(t, f.a, ra.W, ra.H)

	f.s.mu.Lock()
	claims := len(f.s.claims)
	f.s.mu.Unlock()
	if claims != 0 {
		t.Fatalf("%d claims were recorded from a client that does not size the panes", claims)
	}
	// The client's own screen size stays what its attach said: a claim is
	// never read as the client's size.
	f.s.mu.Lock()
	screen := [2]int{f.s.cols, f.s.rows}
	f.s.mu.Unlock()
	if screen != [2]int{100, 30} {
		t.Fatalf("the refused claim moved the screen to %dx%d", screen[0], screen[1])
	}
	t.Logf("refused claim: %s stayed %dx%d, no claim recorded", f.a, ra.W, ra.H)
}

// TestClaimHeightReachesNonSizer is the two-client repro from the attach
// review's F3. The sizing client changes only the row count, and the client
// that is not the sizer must take the new grid from a screen frame: it never
// resizes a terminal. Before the fix dm sent the pane a resize and no screen,
// so the non-sizer kept the old row count and a full-screen program scrolled
// rows out of a grid that was too short.
func TestClaimHeightReachesNonSizer(t *testing.T) {
	t.Parallel()
	f := newSizerFixture(t, "sizerheight")
	// This test runs every picture handshake itself, once the pane holds the
	// size the handshake is about, so it parks the settle timer. The other
	// sizer tests read sizes and resize counts, not pictures, and keep the
	// fixture's short settle.
	f.s.reflowSettle = time.Hour
	first := f.client(t, 100, 30)
	if !first.waitSizer(t, true) {
		t.Fatal("the first client to attach was not made the sizing client")
	}
	f.waitClients(t, 1)
	rect := rectOf(t, f.s, f.a)
	f.waitSize(t, f.a, rect.W, rect.H)
	// The attach owed this client one picture for the pane, and the size
	// change from the pane's empty state to its rectangle armed one more
	// handshake. Read both here: a frame still in flight when a claim is
	// measured reads as the claim's own.
	first.readScreen(t, f.a)
	if !f.settlePicture(t, f.a) {
		t.Fatal("the attach's size change armed no picture handshake")
	}
	first.waitScreens(t, f.a, 1, rect.W, rect.H)

	second := f.client(t, 100, 30)
	if !second.waitSizer(t, true) {
		t.Fatal("the second client was not made the sizing client")
	}
	if first.waitSizer(t, false) {
		t.Fatal("the first client was still told it sizes the panes")
	}
	f.waitClients(t, 2)
	f.waitSize(t, f.a, rect.W, rect.H)
	// The second attach arms a handshake only if it moved a size. Run it if it
	// did, so its picture is read here rather than as a claim's.
	if f.settlePicture(t, f.a) {
		first.waitScreens(t, f.a, 1, rect.W, rect.H)
	}
	t.Logf("no claim yet: %s %dx%d, every attach picture read", f.a, rect.W, rect.H)

	// oneClaim sends one claim and proves the frames it costs: one resize to
	// the pane, one picture handshake, and one screen for the non-sizer.
	oneClaim := func(cols, rows int, kind string) {
		t.Helper()
		resizes := f.demons[f.a].resizes()
		reflows := f.demons[f.a].reflows()
		second.send(t, wire.Msg{Type: "resize", Target: f.a, Cols: cols, Rows: rows})
		f.waitSize(t, f.a, cols, rows)
		// The pane holds the claimed size, so the picture the claim armed is
		// the claimed grid. Run it now and read the one frame it owes: a
		// second frame for this pane before it is the previous claim's, and
		// the next claim's screen() call counts it.
		if !f.settlePicture(t, f.a) {
			t.Fatalf("the %dx%d claim armed no picture handshake", cols, rows)
		}
		if n := first.screen(t, f.a, cols, rows); n != 1 {
			t.Fatalf("%s owed one %dx%d screen frame per claim, read %d", f.a, cols, rows, n)
		}
		if got := f.demons[f.a].resizes(); got != resizes+1 {
			t.Fatalf("the %dx%d claim sent the pane %d resize frames, want 1", cols, rows, got-resizes)
		}
		if got := f.demons[f.a].reflows(); got != reflows+1 {
			t.Fatalf("the %dx%d claim sent the pane %d reflow requests, want 1", cols, rows, got-reflows)
		}
		t.Logf("%s claim %dx%d: 1 resize to the pty, 1 reflow, 1 screen to the non-sizer", kind, cols, rows)
	}

	// The control, unchanged by the fix: a width change pushes a screen.
	oneClaim(60, 20, "width")
	// Height only, smaller then larger: each owes the non-sizer one frame.
	oneClaim(60, 16, "height-only")
	oneClaim(60, 30, "height-only")

	// Every handshake the test ran has been answered and every frame it owes
	// has been read. A frame that arrives now is a second picture for one
	// handshake, which is the claim the old fixed window could not tell apart
	// from a slow one.
	first.expectNoScreen(t, f.a, 100*time.Millisecond)
}
