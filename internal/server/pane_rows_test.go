package server

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"dm/internal/layout"
	"dm/internal/session"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// rowsFixture is one in-process server, one pane wired to a pipe the test
// writes into, and one client whose snapshots the test reads. The pane's
// socket stays open for the life of the test: closing it would end the
// session, which exits the process.
type rowsFixture struct {
	s     *server
	id    string
	pane  net.Conn
	snaps chan wire.Snapshot
}

func newRowsFixture(t *testing.T, name string) *rowsFixture {
	t.Helper()
	st := session.NewStateAt(t.TempDir(), name)
	id := testutil.RootPane(st)
	s := railServer(st)
	srv, pane := net.Pipe()
	s.panes[id] = &paneConn{id: id, c: srv, st: wire.Status{State: "unknown"}}
	csrv, cli := net.Pipe()
	s.clients[newClient(csrv)] = true
	s.watchPane(id)
	t.Cleanup(func() { _ = csrv.Close(); _ = cli.Close() })
	f := &rowsFixture{s: s, id: id, pane: pane, snaps: make(chan wire.Snapshot, 128)}
	// The client would rather drop a frame the test is not reading than stall
	// the server's broadcast.
	go func() {
		for {
			m, err := wire.ReadMsg(cli)
			if err != nil {
				return
			}
			if m.Type != "snapshot" || m.Snap == nil {
				continue
			}
			select {
			case f.snaps <- *m.Snap:
			default:
			}
		}
	}()
	return f
}

// newRowsExitFixture is the rows fixture with a neighbour pane in the same
// window. An exit is the one event these tests cannot take on a lone pane: the
// last pane ends the session, which exits the process. The neighbour also gets
// a pane connection, drained so a broadcast cannot block.
func newRowsExitFixture(t *testing.T, name string) *rowsFixture {
	t.Helper()
	// An exit notification belongs to the operator's desktop, not to a test.
	// Serial: the notify gate reads DM_NOTIFY, which t.Setenv cannot
	// parallelise.
	t.Setenv("DM_NOTIFY", "0")
	f := newRowsFixture(t, name)
	w := f.s.st.ActiveWindow()
	other := w.NewPaneID()
	w.Root = &layout.Node{Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*layout.Node{{Pane: f.id}, {Pane: other}}}
	w.Focus = f.id
	f.s.st.Panes[other] = &session.PaneState{}
	srv, cli := net.Pipe()
	f.s.panes[other] = &paneConn{id: other, c: srv, st: wire.Status{State: "unknown"}}
	t.Cleanup(func() { _ = srv.Close(); _ = cli.Close() })
	go func() {
		for {
			if _, err := wire.ReadMsg(cli); err != nil {
				return
			}
		}
	}()
	return f
}

// send writes one frame into the pane's socket, the way a pane daemon would.
func (f *rowsFixture) send(t *testing.T, m wire.Msg) {
	t.Helper()
	if err := wire.WriteMsg(f.pane, m); err != nil {
		t.Fatal(err)
	}
}

// waitSnapshot returns the first snapshot the client was sent that ok accepts.
func (f *rowsFixture) waitSnapshot(t *testing.T, what string, ok func(wire.Snapshot) bool) wire.Snapshot {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case snap := <-f.snaps:
			if ok(snap) {
				return snap
			}
		case <-deadline:
			t.Fatalf("no snapshot carried %s", what)
			return wire.Snapshot{}
		}
	}
}

// TestAllPaneTitle proves a title the pane reports reaches the row the rail
// reads, through the server's own snapshot.
func TestAllPaneTitle(t *testing.T) {
	t.Parallel()
	f := newRowsFixture(t, "title")
	f.send(t, wire.Msg{Type: "title", Res: json.RawMessage(`{"title":"build log"}`)})
	snap := f.waitSnapshot(t, "the title", func(s wire.Snapshot) bool {
		return len(s.All) == 1 && s.All[0].Title == "build log"
	})
	if snap.All[0].ID != f.id {
		t.Fatalf("row = %q, want %q", snap.All[0].ID, f.id)
	}
}

// TestAllPaneStatusChangedAt proves a state change stamps the row and a repeat
// of the same state does not. The second half uses a sentinel stamp, so it
// cannot pass by two stamps landing in the same second.
func TestAllPaneStatusChangedAt(t *testing.T) {
	t.Parallel()
	f := newRowsFixture(t, "changed")
	f.send(t, wire.Msg{Type: "status", St: &wire.Status{State: "working"}})
	snap := f.waitSnapshot(t, "the first state", func(s wire.Snapshot) bool {
		return len(s.All) == 1 && s.All[0].Status == "working"
	})
	stamp := snap.All[0].StatusChangedAt
	if stamp <= 0 {
		t.Fatalf("status change stamp = %d, want a unix second", stamp)
	}

	f.s.mu.Lock()
	f.s.statusChangedAt[f.id] = 12345
	f.s.mu.Unlock()

	// The pipe is ordered, so the title frame lands after the repeated status.
	// The snapshot that carries the title was built after the repeat ran.
	f.send(t, wire.Msg{Type: "status", St: &wire.Status{State: "working"}})
	f.send(t, wire.Msg{Type: "title", Res: json.RawMessage(`{"title":"still working"}`)})
	snap = f.waitSnapshot(t, "the repeated state", func(s wire.Snapshot) bool {
		return len(s.All) == 1 && s.All[0].Title == "still working"
	})
	if snap.All[0].StatusChangedAt != 12345 {
		t.Fatalf("repeated state stamp = %d, want the sentinel 12345", snap.All[0].StatusChangedAt)
	}
}

// TestAllPaneTitleCap proves two title changes in a row cost one repaint, and
// the trailing change still lands once the cap allows it.
func TestAllPaneTitleCap(t *testing.T) {
	t.Parallel()
	f := newRowsFixture(t, "cap")
	f.send(t, wire.Msg{Type: "title", Res: json.RawMessage(`{"title":"one"}`)})
	f.waitSnapshot(t, "the first title", func(s wire.Snapshot) bool {
		return len(s.All) == 1 && s.All[0].Title == "one"
	})

	f.send(t, wire.Msg{Type: "title", Res: json.RawMessage(`{"title":"two"}`)})
	select {
	case snap := <-f.snaps:
		if len(snap.All) == 1 && snap.All[0].Title == "two" {
			t.Fatal("second title repainted inside the cap window")
		}
	case <-time.After(200 * time.Millisecond):
	}
	f.waitSnapshot(t, "the trailing title", func(s wire.Snapshot) bool {
		return len(s.All) == 1 && s.All[0].Title == "two"
	})
}

// TestAllPaneStatusStampDroppedOnExit proves the server forgets a pane's status
// stamp when the pane exits, so nothing keeps a stamp for an id that is gone.
func TestAllPaneStatusStampDroppedOnExit(t *testing.T) {
	// Serial: the notify gate reads DM_NOTIFY, env t.Setenv cannot
	// parallelise.
	f := newRowsExitFixture(t, "exit")
	f.send(t, wire.Msg{Type: "status", St: &wire.Status{State: "working"}})
	f.waitSnapshot(t, "the state", func(s wire.Snapshot) bool {
		return len(s.All) == 2 && s.All[0].ID == f.id && s.All[0].Status == "working"
	})
	f.s.mu.Lock()
	stamp, ok := f.s.statusChangedAt[f.id]
	f.s.mu.Unlock()
	if !ok || stamp <= 0 {
		t.Fatalf("stamp for %s = %d, %v, want a unix second", f.id, stamp, ok)
	}

	f.send(t, wire.Msg{Type: "exit"})

	// The pane's watch goroutine removes the pane, saves and broadcasts. The
	// broadcast follows the save, so this snapshot proves the save landed
	// before the test leaves and its temp dir goes away.
	f.waitSnapshot(t, "the exited pane dropped", func(s wire.Snapshot) bool {
		return len(s.All) == 1
	})

	// The map is what this test is about: the server stops reporting a pane
	// it dropped.
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.s.mu.Lock()
		_, stampLeft := f.s.statusChangedAt[f.id]
		_, paneLeft := f.s.panes[f.id]
		f.s.mu.Unlock()
		if !stampLeft && !paneLeft {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after exit: stamp %v, pane %v, want both gone", stampLeft, paneLeft)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
