package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dm/internal/paths"
)

// The queue tests use net.Pipe for the client socket. A write on a pipe blocks
// until the other end reads, so a pipe the test never reads is a stalled
// client: exactly the pane that used to freeze a lane.

// screenPane answers the pane socket with a screen and a replay, so the
// server's resync path runs without a real pane daemon.
func screenPane(t *testing.T, session, id string) {
	t.Helper()
	sock := paths.PaneSock(session, id)
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				for {
					m, err := readMsg(c)
					if err != nil {
						return
					}
					switch m.Action {
					case "screen":
						_ = writeMsg(c, Msg{Type: "screen", ID: m.ID,
							Target: id, Data: []byte("screen " + id)})
					case "replay":
						st := Status{State: "idle"}
						_ = writeMsg(c, Msg{Type: "replay", ID: m.ID,
							Target: id, Data: []byte("replay " + id), St: &st})
					default:
						_ = writeMsg(c, Msg{Type: "resp", ID: m.ID, OK: true})
					}
				}
			}(c)
		}
	}()
}

// queueServer builds a server with two panes side by side in one window. The
// panes are answered by fake pane sockets; nothing spawns a process.
func queueServer(t *testing.T, name string) (*server, string, string) {
	t.Helper()
	t.Setenv("DM_STATE", t.TempDir())
	st := newState(name)
	w := st.win()
	a := w.newPaneID()
	b := w.newPaneID()
	w.Root = &Node{Dir: "row", Kids: []*Node{{Pane: a}, {Pane: b}}}
	w.Focus = a
	st.Panes[a] = &PaneState{}
	st.Panes[b] = &PaneState{}
	if err := os.MkdirAll(paths.SessionDir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	screenPane(t, name, a)
	screenPane(t, name, b)
	s := railServer(st)
	return s, a, b
}

// stalledClient registers a client whose socket nobody reads and returns the
// end of the pipe the test can read later.
func stalledClient(t *testing.T, s *server) (*client, net.Conn) {
	t.Helper()
	srv, cli := net.Pipe()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = cli.Close()
	})
	cl := newClient(srv)
	cl.setResync(func() { s.resyncClient(cl) })
	s.mu.Lock()
	s.clients[cl] = true
	s.mu.Unlock()
	t.Cleanup(func() {
		s.mu.Lock()
		delete(s.clients, cl)
		s.mu.Unlock()
		cl.shutdown()
	})
	return cl, cli
}

// readFrame reads one frame, or reports why it could not.
func readFrame(c net.Conn, wait time.Duration) (Msg, error) {
	_ = c.SetReadDeadline(time.Now().Add(wait))
	return readMsg(c)
}

// TestBroadcastWhileClientStalled proves the write left the caller's goroutine:
// a broadcast to a stalled client returns at once and the queue stays bounded.
// An overflowed client then owes a picture, not a backlog: it refuses output,
// still takes every other frame, and resyncs from a snapshot once it drains.
func TestBroadcastWhileClientStalled(t *testing.T) {
	s, a, b := queueServer(t, "stall")
	cl, cli := stalledClient(t, s)
	data := make([]byte, 8<<10)

	start := time.Now()
	for i := 0; i < 300; i++ {
		id := a
		if i%2 == 1 {
			id = b
		}
		s.mu.Lock()
		s.broadcastLocked(Msg{Type: "bytes", Target: id, Data: data})
		s.mu.Unlock()
		if q := cl.queuedBytes(); q > outboundBytes {
			t.Fatalf("queued %d bytes, bound is %d", q, outboundBytes)
		}
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("broadcast to a stalled client took %v", elapsed)
	}

	cl.mu.Lock()
	stale := cl.stale
	cl.mu.Unlock()
	if !stale {
		t.Fatal("an overflowed client was not marked stale")
	}

	// Hold the queue still: the counts below belong to enqueueLocked, not to
	// the writer popping a frame. An overflowed queue may be empty, because
	// the writer already handed the last frame to the socket, so nothing here
	// counts frames as a proof of the backlog.
	cl.pause()
	q0, f0 := cl.queuedBytes(), cl.queuedFrames()

	// A stale client owes a picture, so output does not queue again.
	cl.deliver(Msg{Type: "bytes", Target: a, Data: data})
	if q, f := cl.queuedBytes(), cl.queuedFrames(); q != q0 || f != f0 {
		t.Fatalf("a bytes frame queued for a stale client: %d frames / %d bytes, was %d / %d", f, q, f0, q0)
	}
	// Any other frame still queues: only output is replaced by the resync.
	cl.deliver(Msg{Type: "resp", ID: 7, OK: true})
	q1, f1 := cl.queuedBytes(), cl.queuedFrames()
	if f1 != f0+1 || q1 <= q0 {
		t.Fatalf("a resp frame did not queue for a stale client: %d frames / %d bytes, was %d / %d", f1, q1, f0, q0)
	}
	if q1 > outboundBytes {
		t.Fatalf("queued %d bytes, bound is %d", q1, outboundBytes)
	}
	cl.resume()

	// The stale client owes a picture, not a backlog: once it drains, the
	// writer resyncs from the server's snapshot.
	var snap Msg
	for i := 0; ; i++ {
		m, err := readFrame(cli, 5*time.Second)
		if err != nil {
			t.Fatalf("no resync snapshot after %d frames: %v", i, err)
		}
		if m.Type == "snapshot" {
			snap = m
			break
		}
		if i > 4 {
			t.Fatalf("frame %d is %q, want the resync snapshot", i, m.Type)
		}
	}
	if snap.Snap == nil || snap.Snap.Focus != a {
		t.Fatalf("resync snapshot %+v", snap.Snap)
	}
}

// TestStalledClientResyncsFromScreens proves a stalled client gets a picture,
// not a backlog: once it drains, a snapshot then one screen per pane arrives.
func TestStalledClientResyncsFromScreens(t *testing.T) {
	s, a, b := queueServer(t, "resync")
	cl, cli := stalledClient(t, s)
	data := make([]byte, 8<<10)

	for i := 0; i < 300; i++ {
		id := a
		if i%2 == 1 {
			id = b
		}
		cl.deliver(Msg{Type: "bytes", Target: id, Data: data})
	}
	if q := cl.queuedBytes(); q > outboundBytes {
		t.Fatalf("queued %d bytes, bound is %d", q, outboundBytes)
	}

	// Drain until the resync snapshot. Everything before it is the one frame
	// the writer already had in flight.
	var snap Msg
	for i := 0; ; i++ {
		m, err := readFrame(cli, 5*time.Second)
		if err != nil {
			t.Fatalf("no resync snapshot after %d frames: %v", i, err)
		}
		if m.Type == "snapshot" {
			snap = m
			break
		}
		if i > 4 {
			t.Fatalf("frame %d is %q, want the resync snapshot", i, m.Type)
		}
	}
	if snap.Snap == nil || snap.Snap.Focus != a {
		t.Fatalf("resync snapshot %+v", snap.Snap)
	}

	screens := map[string]bool{}
	for i := 0; i < 2; i++ {
		m, err := readFrame(cli, 2*time.Second)
		if err != nil {
			t.Fatalf("screen %d after the snapshot: %v", i, err)
		}
		if m.Type != "screen" {
			t.Fatalf("frame %d after the snapshot is %q, want a screen", i, m.Type)
		}
		screens[m.Target] = true
	}
	if !screens[a] || !screens[b] || len(screens) != 2 {
		t.Fatalf("screens after the snapshot = %v, want one per pane", screens)
	}
	if _, err := readFrame(cli, 200*time.Millisecond); err == nil {
		t.Fatal("a frame followed the resync: a bytes backlog survived")
	}
	if q := cl.queuedBytes(); q != 0 {
		t.Fatalf("queued %d bytes after the drain", q)
	}
}

// TestMutationRepliesWhileClientStalled proves a layout change still completes
// and answers its request while a client is not reading its socket.
func TestMutationRepliesWhileClientStalled(t *testing.T) {
	s, a, b := queueServer(t, "mutate")
	cl, cli := stalledClient(t, s)

	// Fill the queue first, so the mutation runs against a client that is
	// already behind and the reply has to share the bound with the backlog.
	data := make([]byte, 8<<10)
	for i := 0; i < 300; i++ {
		cl.deliver(Msg{Type: "bytes", Target: a, Data: data})
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleReq(cl, Msg{Type: "req", Action: "focus", Target: b, ID: 5})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleReq blocked on a stalled client")
	}

	var reply *Msg
	for i := 0; i < 64; i++ {
		m, err := readFrame(cli, 2*time.Second)
		if err != nil {
			t.Fatalf("no reply after %d frames: %v", i, err)
		}
		if m.Type == "resp" && m.ID == 5 {
			got := m
			reply = &got
			break
		}
	}
	if reply == nil || !reply.OK {
		t.Fatalf("focus reply = %+v, want an ok response", reply)
	}
	if q := cl.queuedBytes(); q > outboundBytes {
		t.Fatalf("queued %d bytes, bound is %d", q, outboundBytes)
	}
}
