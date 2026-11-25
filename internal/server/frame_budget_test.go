package server

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dm/internal/ask"
	"dm/internal/layout"
	"dm/internal/session"
	"dm/internal/wire"
)

// A picture is one bounded frame a client needs to paint or asked for: the
// snapshot, a screen, the scrub repaint, the replay ring. It is priced at its
// real size and spends its own budget, so the 1 MiB output bound no longer
// drops the snapshot, the replay or the screen.

// TestPictureOverOutputStreamIsDelivered proves a snapshot of about 2 MiB, well
// over the output bound, reaches the client intact.
func TestPictureOverOutputStreamIsDelivered(t *testing.T) {
	t.Parallel()
	cl, conn := boxConn(t)
	title := strings.Repeat("t", 2<<20)
	snap := &wire.Snapshot{Session: "big", Panes: []wire.PaneInfo{{ID: "w1p1", Title: title}}}
	picture := wire.Msg{Type: "snapshot", Snap: snap}
	if n := msgBytes(picture); n <= outboundBytes {
		t.Fatalf("the fixture prices at %d bytes, want more than the %d output bound", n, outboundBytes)
	}
	if n := msgBytes(picture); n > wire.MaxFrame {
		t.Fatalf("the fixture prices at %d bytes, want under the wire limit %d", n, wire.MaxFrame)
	}

	if !cl.deliver(picture) {
		t.Fatalf("a %d byte snapshot was refused", msgBytes(picture))
	}
	got, err := readFrame(conn, 5*time.Second)
	if err != nil {
		t.Fatalf("read the snapshot frame: %v", err)
	}
	if got.Type != "snapshot" || got.Snap == nil {
		t.Fatalf("frame = %q with snap %v, want a snapshot", got.Type, got.Snap)
	}
	if len(got.Snap.Panes) != 1 || got.Snap.Panes[0].Title != title {
		t.Fatalf("snapshot arrived with %d panes and a %d byte title, want the %d byte one intact",
			len(got.Snap.Panes), len(got.Snap.Panes[0].Title), len(title))
	}
}

// bigRingPane answers the pane socket with a replay ring of size bytes, the way
// a real pane daemon answers a replay request.
func bigRingPane(t *testing.T, sock string, size int) []byte {
	t.Helper()
	ring := bytes.Repeat([]byte("r"), size)
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
				defer func() { _ = c.Close() }()
				for {
					m, err := wire.ReadMsg(c)
					if err != nil {
						return
					}
					if m.Action != "replay" {
						_ = wire.WriteMsg(c, wire.Msg{Type: "resp", ID: m.ID, OK: true})
						continue
					}
					st := wire.Status{State: "idle"}
					_ = wire.WriteMsg(c, wire.Msg{Type: "replay", ID: m.ID, Target: m.Target,
						Data: ring, St: &st})
				}
			}(c)
		}
	}()
	return ring
}

// TestReplayOverOutputStreamArrives is the report's own probe: a 2.4 MiB ring
// used to answer only a resp, so `dm history` said nothing and the scrollback
// stayed empty. The ring now arrives whole, then the OK.
func TestReplayOverOutputStreamArrives(t *testing.T) {
	t.Parallel()
	st := session.NewStateAt(t.TempDir(), "ring")
	w := st.ActiveWindow()
	a := w.NewPaneID()
	w.Root = &layout.Node{Kids: []*layout.Node{{Pane: a}}}
	w.Focus = a
	st.Panes[a] = &session.PaneState{}
	if err := os.MkdirAll(st.SessionDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	ring := bigRingPane(t, st.PaneSock(a), 2516582) // 2.4 MiB: over 1 MiB, under the wire limit
	s := railServer(st)
	cl, conn := boxConn(t)

	req := wire.Msg{Type: "req", Action: "replay", Target: a, ID: 3}
	s.handleReq(cl, req)

	got, err := readFrame(conn, 10*time.Second)
	if err != nil {
		t.Fatalf("read the replay frame: %v", err)
	}
	if got.Type != "replay" {
		t.Fatalf("first frame is %q, want the replay ring", got.Type)
	}
	if !bytes.Equal(got.Data, ring) {
		t.Fatalf("replay arrived with %d bytes, want the %d byte ring intact", len(got.Data), len(ring))
	}
	resp, err := readFrame(conn, 5*time.Second)
	if err != nil {
		t.Fatalf("read the replay reply: %v", err)
	}
	if resp.Type != "resp" || resp.ID != 3 {
		t.Fatalf("second frame = %q id %d, want the replay resp", resp.Type, resp.ID)
	}
	if !resp.OK {
		t.Fatalf("replay reply = %q, want OK after the ring", resp.Error)
	}
}

// TestPictureOverWireLimitAnswersAnError proves a picture the wire cannot carry
// is refused to its caller, never answered with a false OK, and that the
// refusal arms no repair: a resync that queued nothing would arm itself again.
func TestPictureOverWireLimitAnswersAnError(t *testing.T) {
	t.Parallel()
	s, a, _ := queueServer(t, "oversize")
	cl, conn := boxConn(t)
	picture := wire.Msg{Type: "replay", Target: a, Data: make([]byte, wire.MaxFrame)}
	req := wire.Msg{Type: "req", Action: "replay", Target: a, ID: 9}

	s.sendPicture(cl, picture, req, map[string]any{"pane": a})

	got, err := readFrame(conn, 5*time.Second)
	if err != nil {
		t.Fatalf("read the reply: %v", err)
	}
	if got.Type != "resp" || got.ID != 9 {
		t.Fatalf("frame = %q id %d, want the replay resp", got.Type, got.ID)
	}
	wantErr(t, "replay", "wire frame limit", got)
	if n := cl.queuedFrames(); n != 0 {
		t.Fatalf("%d frames queued after a refused picture", n)
	}
	cl.mu.Lock()
	stale, armed := cl.stale, cl.armed
	cl.mu.Unlock()
	if stale || armed {
		t.Fatalf("a refused picture armed a repair: stale %v, armed %v", stale, armed)
	}
}

// TestStreamOverflowResyncsOnce proves a dropped output frame still buys exactly
// one resync picture, counted: the overflow cannot arm itself from the repair it
// already owes. The open ask below prices the snapshot the repair carries over
// the output bound, so this is the report's busy loop: the snapshot used to be
// dropped again on every pass, and the writer re-dialled every pane with no end.
func TestStreamOverflowResyncsOnce(t *testing.T) {
	t.Parallel()
	s, a, b := queueServer(t, "once")
	s.st.Ask = []ask.Request{{
		ID: "a1", Pane: a, ToolCallID: "t1", At: time.Now().UTC(),
		Questions: []ask.Question{{Question: "which?", Header: "pick",
			Options: []ask.Option{{Label: "one", Preview: strings.Repeat("p", 2<<20)}}}},
	}}
	s.mu.Lock()
	picture := msgBytes(wire.Msg{Type: "snapshot", Snap: s.snapshotLocked()})
	s.mu.Unlock()
	if picture <= outboundBytes {
		t.Fatalf("the snapshot prices at %d bytes, want more than the %d output bound", picture, outboundBytes)
	}
	cl, conn := stalledClient(t, s)
	var calls atomic.Int32
	cl.setResync(func() {
		calls.Add(1)
		s.resyncClient(cl)
	})

	data := make([]byte, 8<<10)
	for i := 0; i < 300; i++ {
		id := a
		if i%2 == 1 {
			id = b
		}
		cl.deliver(wire.Msg{Type: "bytes", Target: id, Data: data})
	}
	if q := cl.queuedBytes(); q > outboundBytes {
		t.Fatalf("queued %d bytes, bound is %d", q, outboundBytes)
	}

	// Drain the one resync picture: a snapshot, then one screen per pane.
	var snap wire.Msg
	for i := 0; ; i++ {
		m, err := readFrame(conn, 5*time.Second)
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
	for i := 0; i < 2; i++ {
		m, err := readFrame(conn, 2*time.Second)
		if err != nil {
			t.Fatalf("screen %d after the snapshot: %v", i, err)
		}
		if m.Type != "screen" {
			t.Fatalf("frame %d after the snapshot is %q, want a screen", i, m.Type)
		}
	}
	if _, err := readFrame(conn, 200*time.Millisecond); err == nil {
		t.Fatal("a frame followed the resync picture")
	}
	time.Sleep(200 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("resync ran %d times for one overflow, want once", got)
	}
}
