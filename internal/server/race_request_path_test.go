package server

import (
	"net"
	"testing"

	"dm/internal/session"
	"dm/internal/wire"
)

// The probes in .dungeon/adversarial-review/reports/review-concurrency.md
// fired the race detector on the request paths that read the pane and state
// maps outside s.mu. These tests keep that shape: each starts a writer that
// takes s.mu and mutates the maps the way adoptPane, closePane and syncSizes
// do, then hammers the request path. The read must sit under the same lock as
// the write, or the detector reports the race and the server would abort on a
// real session.

// raceWriter takes s.mu in a tight loop and runs write, the way every pane
// map writer does. It stops when the test ends.
func raceWriter(t *testing.T, s *server, write func()) {
	t.Helper()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			s.mu.Lock()
			write()
			s.mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		close(stop)
		<-done
	})
}

// drainConn is a live pane connection whose socket is read and dropped, so a
// send from the request path never blocks the test.
func drainConn(t *testing.T, id string) *paneConn {
	t.Helper()
	srv, cli := net.Pipe()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = cli.Close()
	})
	go func() {
		for {
			if _, err := wire.ReadMsg(cli); err != nil {
				return
			}
		}
	}()
	return &paneConn{id: id, c: srv}
}

// TestSendKeysReadsPaneMapUnderTheLock proves a synced keystroke resolves its
// destinations under s.mu: adopt and redial rewrite the pane map under it, and
// an unlocked read aborts the server with "concurrent map read and map write".
func TestSendKeysReadsPaneMapUnderTheLock(t *testing.T) {
	t.Parallel()
	s, a, b := queueServer(t, "sendkeys-race")

	pa := drainConn(t, a)
	pb := drainConn(t, b)
	other := drainConn(t, b)
	s.mu.Lock()
	s.panes[a] = pa
	s.panes[b] = pb
	s.st.ActiveWindow().Sync = true
	s.mu.Unlock()

	// The writer swaps the second destination, the way a redial installs a
	// fresh conn while a synced keystroke is in flight.
	raceWriter(t, s, func() {
		if s.panes[b] == pb {
			s.panes[b] = other
			return
		}
		s.panes[b] = pb
	})

	for i := 0; i < 500; i++ {
		if err := s.sendKeys(a, []byte("x")); err != nil {
			t.Fatalf("sendKeys: %v", err)
		}
	}
}

// TestPaneActionReadsStateMapUnderTheLock proves the pane action copies what
// it needs from the state map under s.mu. A reply built from a pointer kept
// past the unlock races adopt and close, the P1/P2 defect R-03 names.
func TestPaneActionReadsStateMapUnderTheLock(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, _ := rpcServer(t, "pane-race")

	s.mu.Lock()
	s.st.Panes[a].Name = "start"
	s.st.Panes[a].Cwd = "/tmp"
	s.mu.Unlock()

	raceWriter(t, s, func() {
		s.st.Panes[a] = &session.PaneState{Name: "race", Cwd: "/tmp/race"}
	})

	for i := 0; i < 300; i++ {
		m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "pane", Target: a})
		if !m.OK {
			t.Fatalf("pane action: %s", m.Error)
		}
	}
}

// TestPaneHistoryReadsSizeUnderTheLock proves the history path copies the
// pane's PTY size under s.mu. syncSizes writes Cols and Rows there, and a
// pointer kept past the unlock reads a torn pair and replays at the wrong
// geometry.
func TestPaneHistoryReadsSizeUnderTheLock(t *testing.T) {
	t.Parallel()
	s, a, _ := queueServer(t, "history-race")

	s.mu.Lock()
	s.st.Panes[a].Cols = 80
	s.st.Panes[a].Rows = 24
	s.mu.Unlock()

	raceWriter(t, s, func() {
		ps := s.st.Panes[a]
		if ps.Cols == 80 {
			ps.Cols, ps.Rows = 120, 40
			return
		}
		ps.Cols, ps.Rows = 80, 24
	})

	for i := 0; i < 300; i++ {
		if _, err := s.paneHistory(a, 10); err != nil {
			t.Fatalf("paneHistory: %v", err)
		}
	}
}
