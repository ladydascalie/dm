package pane

import (
	"bytes"
	"net"
	"os"
	"testing"
	"time"

	"dm/internal/vt"
	"dm/internal/wire"
)

// stalledPane is a pane daemon whose pty nobody reads: the read end of the
// pipe stays unread, so the pty writer's first big frame blocks for good,
// the way a suspended foreground job blocks a pty write. The socket side is
// wired the way yankPane wires it. The caller owns the read end, so a test
// can bring the reader back.
func stalledPane(t *testing.T) (*paneProc, net.Conn, chan wire.Msg, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// Closing both ends in the cleanup also keeps r alive for the test's
	// body: an os.File the test drops would be finalized and closed early,
	// and the writer would see EPIPE instead of a full pipe.
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	p := &paneProc{
		id: "w1p1", vt: vt.NewTerm(20, 5), ring: NewRing(ringDefault),
		subs: map[*subConn]bool{}, st: wire.Status{State: "idle"}, ptmx: testPty{w},
	}
	serve, cli := net.Pipe()
	t.Cleanup(func() { _ = cli.Close() })
	msgs := make(chan wire.Msg, 64)
	go func() {
		for {
			m, err := wire.ReadMsg(cli)
			if err != nil {
				close(msgs)
				return
			}
			msgs <- m
		}
	}()
	go p.serve(serve)
	return p, cli, msgs, r
}

// stallStopsTheWriter is one frame the pty cannot take whole: it is larger
// than any pipe buffer Linux grants an unprivileged process, so the writer
// goroutine blocks inside its write no matter how the kernel sized the pipe.
const stallStopsTheWriter = 4 << 20

// stallBurst blocks the writer goroutine on a frame the pty cannot take and
// then pushes the queue past its depth with small writes. Every send
// completes inside the deadline paneSend enforces, or the test dies there.
func stallBurst(t *testing.T, cli net.Conn) {
	t.Helper()
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Keys: bytes.Repeat([]byte("a"), stallStopsTheWriter)})
	for i := 0; i < ptyQueueDepth+150; i++ {
		paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Msg: "x"})
	}
}

// waitPtyQueueEmpty blocks until the pty writer has taken every queued frame,
// so the next frame is queued rather than dropped. The queue holds frames only
// while the writer is behind, so an empty queue means the writer has caught up
// with everything sent so far. A fixed sleep cannot stand in for this: on a
// loaded machine the writer stays behind for longer than any sleep, and a frame
// that arrives at a full queue is dropped, which reads exactly like a real
// wedge, the failure this test exists to catch.
func waitPtyQueueEmpty(t *testing.T, p *paneProc, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		n := len(p.writes)
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pty queue still holds %d frames after %v", n, within)
		}
		time.Sleep(time.Millisecond)
	}
}

// drainSocket pings and reads the reply inside window, so a test can prove
// the serve loop still reads its socket. The server bounds one push with
// paneSendTimeout; the window sits far inside it.
func drainSocket(t *testing.T, cli net.Conn, msgs chan wire.Msg, window time.Duration) {
	t.Helper()
	if err := cli.SetDeadline(time.Now().Add(window)); err != nil {
		t.Fatal(err)
	}
	if err := wire.WriteMsg(cli, wire.Msg{Type: "req", Action: "ping", ID: 77}); err != nil {
		t.Fatalf("send the ping: %v", err)
	}
	select {
	case m, ok := <-msgs:
		if !ok {
			t.Fatal("the socket closed before the ping reply")
		}
		if m.Type != "resp" || m.ID != 77 || !m.OK {
			t.Fatalf("reply = %+v, want the ping's resp", m)
		}
	case <-time.After(window):
		t.Fatalf("no ping reply within %v, the socket stopped draining", window)
	}
}

// TestServeDrainsTheSocketWhenNobodyReadsThePty proves the wedge cure: with
// a pty whose reader is gone, a burst of write frames still drains from the
// control socket, every send completes well inside the server's send
// deadline, the ping still answers, the queue stays bounded, and the frames
// the pty cannot take are dropped and counted. The old shape blocked the
// serve loop in the pty write, the socket backed up, the server's send hit
// its deadline mid-frame, the conn was poisoned and three failed redials
// stood the watcher down; none of that is reachable from pty backpressure
// alone here.
func TestServeDrainsTheSocketWhenNobodyReadsThePty(t *testing.T) {
	t.Parallel()
	p, cli, msgs, _ := stalledPane(t)

	stallBurst(t, cli)
	drainSocket(t, cli, msgs, time.Second)

	if got := p.ptyDrops(); got == 0 {
		time.Sleep(500 * time.Millisecond)
		t.Fatalf("no write was dropped (qlen %d, after sleep drops %d, qlen %d)",
			len(p.writes), p.ptyDropped.Load(), len(p.writes))
	}
	if len(p.writes) > ptyQueueDepth {
		t.Fatalf("queue holds %d frames, want at most %d", len(p.writes), ptyQueueDepth)
	}
}

// TestStalledPtyRecoversWithoutLosingTheSocket proves the stall is
// recoverable and the cure never trips the wedge: once the pty's reader
// comes back, the queued backlog flushes and a write sent after it reaches
// the pty in order, while the socket answered all along.
func TestStalledPtyRecoversWithoutLosingTheSocket(t *testing.T) {
	t.Parallel()
	p, cli, msgs, r := stalledPane(t)

	stallBurst(t, cli)
	drainSocket(t, cli, msgs, time.Second)

	// The suspended job resumes: a reader drains the pipe, hunting for the
	// marker write the test sends after the backlog. Every byte before it
	// belongs to a queued frame, so finding the marker proves the order
	// held and the backlog reached the pty.
	found := make(chan bool, 1)
	go func() {
		var acc bytes.Buffer
		buf := make([]byte, 32<<10)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				acc.Write(buf[:n])
				if i := bytes.Index(acc.Bytes(), []byte("late")); i >= 0 {
					found <- true
					return
				}
				if acc.Len() > 1<<20 {
					acc.Reset()
				}
			}
			if err != nil {
				found <- false
				return
			}
		}
	}()
	// Wait for the backlog to leave the queue before sending the marker. A
	// marker that arrives at a full queue is the frame the writer drops, and
	// the test would then blame the backlog for a drop it caused itself.
	waitPtyQueueEmpty(t, p, 5*time.Second)
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Msg: "late"})
	select {
	case ok := <-found:
		if !ok {
			t.Fatal("the pipe closed before the late write reached the pty")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the queued backlog never flushed to the pty")
	}
	if p.ptyDrops() == 0 {
		t.Fatal("no write was dropped, the queue did not overflow")
	}
	// The socket answered under the full backlog and still answers now.
	drainSocket(t, cli, msgs, time.Second)
}
