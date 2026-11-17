package server

import (
	"bytes"
	"net"
	"testing"
	"time"

	"dm/internal/wire"
)

// TestWatchConnForwardsScrub proves the pane's repaint frame reaches the
// clients. The pane rewrites its own ring and screen when a redaction toggle
// comes back on, and the server's only job is to hand the repaired snapshot to
// every client and let them drop their cached ring.
func TestWatchConnForwardsScrub(t *testing.T) {
	t.Parallel()
	s, ids := histServer(t, "scrubfwd", 1)
	id := ids[0]

	srv, cli := net.Pipe()
	t.Cleanup(func() { _ = srv.Close(); _ = cli.Close() })
	cl := newClient(srv)
	s.mu.Lock()
	s.clients[cl] = true
	s.mu.Unlock()
	t.Cleanup(func() {
		s.mu.Lock()
		delete(s.clients, cl)
		s.mu.Unlock()
		cl.shutdown()
	})

	paneSrv, paneCli := net.Pipe()
	t.Cleanup(func() { _ = paneSrv.Close(); _ = paneCli.Close() })
	pc := &paneConn{id: id, c: paneSrv, st: wire.Status{State: "idle"}}
	s.mu.Lock()
	s.panes[id] = pc
	s.mu.Unlock()
	go func() { s.watchConn(id, pc) }()

	want := newScreenSnapshot(id)
	if err := wire.WriteMsg(paneCli, wire.Msg{Type: "scrub", Target: id, VTSnap: want}); err != nil {
		t.Fatalf("write scrub: %v", err)
	}
	_ = cli.SetReadDeadline(time.Now().Add(2 * time.Second))
	m, err := wire.ReadMsg(cli)
	if err != nil {
		t.Fatalf("client frame: %v", err)
	}
	if m.Type != "scrub" || m.Target != id {
		t.Fatalf("frame %+v, want a scrub for %q", m, id)
	}
	if !bytes.Equal(snapshotBytes(t, m.VTSnap), snapshotBytes(t, want)) {
		t.Fatal("forwarded snapshot is not the one the pane sent")
	}
}
