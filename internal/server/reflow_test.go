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

// fakeReflowPane answers a pane socket the way a pane daemon does: a "screen"
// request gets the current picture, a "reflow" request gets a picture at the
// requested size and is recorded. A silent pane records the request and never
// answers, standing in for one that hung.
type fakeReflowPane struct {
	id     string
	silent bool

	mu    sync.Mutex
	reqs  []wire.Msg
	conns []net.Conn
}

func newFakeReflowPane(t *testing.T, st *session.State, id string, silent bool) *fakeReflowPane {
	t.Helper()
	f := &fakeReflowPane{id: id, silent: silent}
	sock := st.PaneSock(id)
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ln.Close()
		f.mu.Lock()
		for _, c := range f.conns {
			_ = c.Close()
		}
		f.mu.Unlock()
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.conns = append(f.conns, c)
			f.mu.Unlock()
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeReflowPane) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	for {
		m, err := wire.ReadMsg(c)
		if err != nil {
			return
		}
		switch m.Action {
		case "screen":
			_ = wire.WriteMsg(c, wire.Msg{Type: "screen", ID: m.ID, Target: f.id,
				VTSnap: f.snapshot(20, 5), Res: jsonRaw(modeReply{Alt: true})})
		case "reflow":
			f.mu.Lock()
			f.reqs = append(f.reqs, m)
			silent := f.silent
			f.mu.Unlock()
			if silent {
				continue
			}
			_ = wire.WriteMsg(c, wire.Msg{Type: "screen", ID: m.ID, Target: f.id,
				VTSnap: f.snapshot(m.Cols, m.Rows), Res: jsonRaw(modeReply{Mouse: true})})
		}
	}
}

// modeReply is the pane's alt and mouse modes, the shape readPaneScreen parses.
type modeReply struct {
	Alt   bool `json:"alt"`
	Mouse bool `json:"mouse"`
}

func (f *fakeReflowPane) snapshot(cols, rows int) *vt.Snapshot {
	term := vt.NewTerm(cols, rows)
	term.Write([]byte("reflow " + f.id))
	return term.Snapshot()
}

// reflows returns the reflow requests the pane received so far.
func (f *fakeReflowPane) reflows() []wire.Msg {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]wire.Msg(nil), f.reqs...)
}

// waitReflows waits for want reflow requests, so a test never asserts against a
// dial that is still in flight.
func (f *fakeReflowPane) waitReflows(t *testing.T, want int, wait time.Duration) []wire.Msg {
	t.Helper()
	deadline := time.Now().Add(wait)
	for {
		got := f.reflows()
		if len(got) >= want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s got %d reflow requests, want %d", f.id, len(got), want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// reflowFixture is an in-process server whose active window holds panes backed
// by fake pane sockets, with one client socket that receives the broadcasts.
type reflowFixture struct {
	s     *server
	name  string
	ids   []string
	panes map[string]*fakeReflowPane
	cli   net.Conn
}

func newReflowFixture(t *testing.T, name string, ids ...string) *reflowFixture {
	t.Helper()
	st := session.NewStateAt(t.TempDir(), name)
	w := st.ActiveWindow()
	kids := make([]*layout.Node, 0, len(ids))
	for _, id := range ids {
		st.Panes[id] = &session.PaneState{}
		kids = append(kids, &layout.Node{Pane: id})
	}
	w.Root = &layout.Node{Dir: "row", Kids: kids}
	w.Focus = ids[0]

	f := &reflowFixture{name: name, ids: ids, panes: map[string]*fakeReflowPane{}}
	for _, id := range ids {
		f.panes[id] = newFakeReflowPane(t, st, id, false)
	}
	f.s = railServer(st)
	// A short settle keeps the fixture's waits quick. The server carries its
	// own settle, so parallel tests never share the package default.
	f.s.reflowSettle = 20 * time.Millisecond
	for _, id := range ids {
		srv, cli := net.Pipe()
		f.s.panes[id] = &paneConn{id: id, c: srv}
		go func(c net.Conn) {
			for {
				if _, err := wire.ReadMsg(c); err != nil {
					return
				}
			}
		}(cli)
		t.Cleanup(func() { _ = srv.Close(); _ = cli.Close() })
	}

	// Record the current rectangles, so the first syncSizes arms nothing and a
	// test's own resize is the only width change.
	f.s.mu.Lock()
	for id, r := range f.s.paneRectsLocked() {
		if ps := f.s.st.Panes[id]; ps != nil {
			ps.Cols, ps.Rows = r.W, r.H
		}
	}
	f.s.mu.Unlock()

	srv, cli := net.Pipe()
	cl := newClient(srv)
	f.s.mu.Lock()
	f.s.clients[cl] = true
	f.s.mu.Unlock()
	f.cli = cli
	t.Cleanup(func() {
		cl.shutdown()
		_ = srv.Close()
		_ = cli.Close()
	})
	return f
}

// resize sets the session size and syncs the panes, as the resize case in
// serve does.
func (f *reflowFixture) resize(cols, rows int) {
	f.s.mu.Lock()
	f.s.cols, f.s.rows = cols, rows
	f.s.mu.Unlock()
	f.s.syncSizes()
}

// sizeOf is the pane's recorded size, what a reflow must be asked for.
func (f *reflowFixture) sizeOf(id string) (int, int) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	ps := f.s.st.Panes[id]
	return ps.Cols, ps.Rows
}

// screens reads want screen broadcasts and fails on the deadline. Any other
// frame is ignored.
func (f *reflowFixture) screens(t *testing.T, want int) []wire.Msg {
	t.Helper()
	var got []wire.Msg
	deadline := time.Now().Add(time.Second)
	for len(got) < want {
		_ = f.cli.SetReadDeadline(deadline)
		m, err := wire.ReadMsg(f.cli)
		if err != nil {
			t.Fatalf("got %d of %d screen broadcasts: %v", len(got), want, err)
		}
		if m.Type == "screen" {
			got = append(got, m)
		}
	}
	return got
}

// expectNoScreen fails if a screen broadcast arrives within wait.
func (f *reflowFixture) expectNoScreen(t *testing.T, wait time.Duration) {
	t.Helper()
	_ = f.cli.SetReadDeadline(time.Now().Add(wait))
	for {
		m, err := wire.ReadMsg(f.cli)
		if err != nil {
			return
		}
		if m.Type == "screen" {
			t.Fatalf("unexpected screen broadcast for %q", m.Target)
		}
	}
}

// TestReflowOnWidthChange proves a width change arms the settle and one settle
// asks each affected pane once, at its recorded size, and broadcasts one screen
// per answer.
func TestReflowOnWidthChange(t *testing.T) {
	t.Parallel()
	f := newReflowFixture(t, "reflowsize", "w1p1", "w1p2")
	f.resize(120, 40)

	got := f.screens(t, 2)
	if len(got) != 2 {
		t.Fatalf("got %d screen broadcasts, want 2: %+v", len(got), got)
	}
	seen := map[string]*vt.Snapshot{}
	for _, m := range got {
		if m.VTSnap == nil {
			t.Fatalf("screen broadcast without a snapshot: %+v", m)
		}
		if seen[m.Target] != nil {
			t.Fatalf("pane %s got two screen broadcasts", m.Target)
		}
		seen[m.Target] = m.VTSnap
	}
	for _, id := range f.ids {
		reqs := f.panes[id].waitReflows(t, 1, time.Second)
		if len(reqs) != 1 {
			t.Fatalf("%s got %d reflow requests, want 1: %+v", id, len(reqs), reqs)
		}
		cols, rows := f.sizeOf(id)
		if reqs[0].Type != "req" || reqs[0].Action != "reflow" ||
			reqs[0].Cols != cols || reqs[0].Rows != rows {
			t.Fatalf("%s reflow = %+v, want %dx%d", id, reqs[0], cols, rows)
		}
		snap := seen[id]
		if snap == nil {
			t.Fatalf("no screen broadcast for %s: %+v", id, got)
		}
		if snap.Cols != cols || snap.Rows != rows {
			t.Fatalf("%s screen is %dx%d, want %dx%d", id, snap.Cols, snap.Rows, cols, rows)
		}
		// The reply carries the modes, and the server adopts them.
		f.s.mu.Lock()
		alt, mouse := f.s.panes[id].alt, f.s.panes[id].mouse
		f.s.mu.Unlock()
		if alt || !mouse {
			t.Fatalf("%s modes = alt %v mouse %v, want alt false mouse true", id, alt, mouse)
		}
	}
	f.expectNoScreen(t, 3*reflowSettle)
}

// TestReflowHeightOnlyDoesNotArm proves a height change never re-wraps, so it
// arms nothing.
func TestReflowHeightOnlyDoesNotArm(t *testing.T) {
	t.Parallel()
	f := newReflowFixture(t, "reflowheight", "w1p1")
	f.s.mu.Lock()
	cols := f.s.cols
	f.s.mu.Unlock()
	f.resize(cols, 40)

	f.expectNoScreen(t, 5*reflowSettle)
	for _, id := range f.ids {
		if reqs := f.panes[id].reflows(); len(reqs) != 0 {
			t.Fatalf("%s got %d reflow requests after a height change: %+v", id, len(reqs), reqs)
		}
	}
}

// TestReflowDragCoalesces proves two width changes inside one settle produce
// exactly one request, at the final size.
func TestReflowDragCoalesces(t *testing.T) {
	t.Parallel()
	f := newReflowFixture(t, "reflowdrag", "w1p1")
	f.resize(100, 40)
	f.resize(130, 40)

	got := f.screens(t, 1)
	if got[0].Target != "w1p1" {
		t.Fatalf("screen broadcast for %q, want w1p1", got[0].Target)
	}
	cols, rows := f.sizeOf("w1p1")
	reqs := f.panes["w1p1"].waitReflows(t, 1, time.Second)
	if len(reqs) != 1 {
		t.Fatalf("got %d reflow requests for one drag, want 1: %+v", len(reqs), reqs)
	}
	if reqs[0].Cols != cols || reqs[0].Rows != rows {
		t.Fatalf("reflow = %dx%d, want the final %dx%d", reqs[0].Cols, reqs[0].Rows, cols, rows)
	}
	f.expectNoScreen(t, 3*reflowSettle)
	if reqs := f.panes["w1p1"].reflows(); len(reqs) != 1 {
		t.Fatalf("a second settle sent %d requests, want 1: %+v", len(reqs), reqs)
	}
}

// TestReflowSilentPaneDoesNotBlockOthers proves a pane that does not answer is
// skipped while the rest still get their picture.
func TestReflowSilentPaneDoesNotBlockOthers(t *testing.T) {
	t.Parallel()
	f := newReflowFixture(t, "reflowsilent", "w1p1", "w1p2")
	f.panes["w1p2"].mu.Lock()
	f.panes["w1p2"].silent = true
	f.panes["w1p2"].mu.Unlock()
	f.resize(120, 40)

	got := f.screens(t, 1)
	if len(got) != 1 || got[0].Target != "w1p1" {
		t.Fatalf("screens = %+v, want one for w1p1", got)
	}
	f.panes["w1p1"].waitReflows(t, 1, time.Second)
	f.panes["w1p2"].waitReflows(t, 1, time.Second)
	f.expectNoScreen(t, 100*time.Millisecond)
}
