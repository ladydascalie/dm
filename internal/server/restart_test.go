package server

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dm/internal/layout"
	"dm/internal/session"
	"dm/internal/wire"
)

// restartFixture is an in-process server with one pane and one client socket,
// so a restart has something to tell and something to leave alone. No pane
// daemon is spawned: the pane record is all a restart must not touch.
type restartFixture struct {
	s    *server
	cl   *client
	peer net.Conn
	ex   chan int
}

func newRestartFixture(t *testing.T) *restartFixture {
	t.Helper()
	st := session.NewStateAt(t.TempDir(), "rs")
	w := st.ActiveWindow()
	w.Root = &layout.Node{Pane: "w1p1"}
	w.Focus = "w1p1"
	st.Panes["w1p1"] = &session.PaneState{Cwd: "/tmp"}

	s := railServer(st)
	s.conns = map[*client]bool{}
	ex := make(chan int, 1)
	s.exit = func(code int) { ex <- code }

	here, there := net.Pipe()
	t.Cleanup(func() {
		_ = here.Close()
		_ = there.Close()
	})
	cl := newClient(here)
	s.mu.Lock()
	s.clients[cl] = true
	s.conns[cl] = true
	s.mu.Unlock()
	cl.startWriter()

	// The files a restart removes before it tells anyone: the socket a client
	// would dial, and the pid file a kill would signal.
	if err := os.MkdirAll(st.SessionDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.SessionDir(), "server.pid"), []byte("4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.ServerSock(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return &restartFixture{s: s, cl: cl, peer: there, ex: ex}
}

// TestRestartDrainsTheCloseFrame proves an orderly restart: the requesting
// client gets its reply and then a close frame naming the restart, before the
// server exits, so the client can tell a restart from a detach and reconnect
// instead of quitting.
func TestRestartDrainsTheCloseFrame(t *testing.T) {
	t.Parallel()
	f := newRestartFixture(t)

	go f.s.handleReq(f.cl, wire.Msg{Type: "req", Action: "restart", ID: 3})

	_ = f.peer.SetReadDeadline(time.Now().Add(5 * time.Second))
	first, err := wire.ReadMsg(f.peer)
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if first.Type != "resp" || first.Action != "restart" || !first.OK {
		t.Fatalf("first frame = %+v, want the restart reply", first)
	}
	last, err := wire.ReadMsg(f.peer)
	if err != nil {
		t.Fatalf("read close: %v", err)
	}
	if last.Type != "close" || last.Msg != wire.CloseRestart {
		t.Fatalf("last frame = %+v, want a close naming the restart", last)
	}

	select {
	case code := <-f.ex:
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not exit after the drain")
	}

	// The socket and the pid file go before the process does, so nothing can
	// dial the server that is leaving or signal it after it is gone.
	if _, err := os.Stat(f.s.st.ServerSock()); !os.IsNotExist(err) {
		t.Fatalf("the socket survived the restart: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.s.st.SessionDir(), "server.pid")); !os.IsNotExist(err) {
		t.Fatalf("the pid file survived the restart: %v", err)
	}
	// The pane record is the restart's whole point: it must still be there.
	if f.s.st.Panes["w1p1"] == nil {
		t.Fatal("the restart dropped the pane record")
	}
}

// TestRestartDrainsAStalledClient proves a client that stopped reading costs
// the restart its drain window and never the session: the close frame is
// queued, the writer blocks on the socket, and the server still leaves.
func TestRestartDrainsAStalledClient(t *testing.T) {
	t.Parallel()
	f := newRestartFixture(t)
	// Shorten the wait under test, so the proof does not cost the constant.
	f.s.mu.Lock()
	f.s.stopReflowLocked()
	f.s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		f.s.restartServer()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(restartDrain + 5*time.Second):
		t.Fatal("a stalled client held the restart past its drain window")
	}
}
