package pane

import (
	"bytes"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"dm/internal/vt"
	"dm/internal/wire"
)

// broadcastPane is a pane with no pty: the test publishes to it the way the
// read loop does, through publishLocked.
func broadcastPane(t *testing.T) *paneProc {
	t.Helper()
	return &paneProc{
		vt: vt.NewTerm(20, 5), ring: NewRing(ringDefault),
		subs: map[*subConn]bool{}, st: wire.Status{State: "idle"},
	}
}

// subPipe attaches one subscriber to the pane over a pipe and returns the
// client end. The caller owns the client end: nothing reads it unless the
// test says so, which is how a stuck subscriber is built.
func subPipe(t *testing.T, p *paneProc, sendTimeout time.Duration) (*subConn, net.Conn) {
	t.Helper()
	serve, cli := net.Pipe()
	sc := &subConn{p: p, c: serve, sendTimeout: sendTimeout}
	p.mu.Lock()
	p.subs[sc] = true
	p.mu.Unlock()
	t.Cleanup(func() { _ = cli.Close() })
	return sc, cli
}

// pump reads everything one conn sends into a channel, the way a live client
// does.
func pump(cli net.Conn) chan wire.Msg {
	msgs := make(chan wire.Msg, 4096)
	var once sync.Once
	go func() {
		for {
			m, err := wire.ReadMsg(cli)
			if err != nil {
				once.Do(func() { close(msgs) })
				return
			}
			msgs <- m
		}
	}()
	return msgs
}

// collectBytes gathers every bytes frame's payload from a channel until it
// closes, so a test can compare what one subscriber received against the
// stream the pane published.
func collectBytes(msgs chan wire.Msg) []byte {
	var got bytes.Buffer
	for m := range msgs {
		if m.Type == "bytes" {
			got.Write(m.Data)
		}
	}
	return got.Bytes()
}

// closedSoon proves a conn was closed by the other end within the window.
func closedSoon(t *testing.T, cli net.Conn, window time.Duration, what string) {
	t.Helper()
	_ = cli.SetReadDeadline(time.Now().Add(window))
	if _, err := cli.Read(make([]byte, 1)); err == nil {
		t.Fatalf("%s was not closed", what)
	}
}

// broadcastBurst publishes more than one subscriber's queue bound, chunked
// the way the read loop publishes, so a subscriber that keeps up sees it all
// and one that does not hits its bound.
func broadcastBurst(p *paneProc, total int) []byte {
	const chunk = 32 << 10
	full := bytes.Repeat([]byte("output line for the burst\n"), total/26+1)[:total]
	for from := 0; from < total; from += chunk {
		to := min(from+chunk, total)
		p.mu.Lock()
		p.publishLocked(full[from:to])
		p.mu.Unlock()
	}
	return full
}

// TestBroadcastEvictsAStuckSubscriber proves the wedge cure: one subscriber
// that stops reading while output flows never holds the pane. The serve loop
// keeps draining, a second subscriber receives every byte in order, the stuck
// one is evicted once and counted, the ring is untouched, and an evicted
// client may reconnect and start fresh.
func TestBroadcastEvictsAStuckSubscriber(t *testing.T) {
	t.Parallel()
	p := broadcastPane(t)
	healthy, healthyEnd := subPipe(t, p, 0)
	stuck, stuckEnd := subPipe(t, p, time.Second)
	healthyMsgs := pump(healthyEnd)

	// The serve loop's own conn, pumped, so the test can prove the loop kept
	// draining while the burst queued up against the stuck subscriber.
	serveConn, serveCli := net.Pipe()
	t.Cleanup(func() { _ = serveCli.Close() })
	go p.serve(serveConn)
	serveMsgs := pump(serveCli)

	const total = 2 << 20 // 2 MiB, twice the subscriber queue bound
	want := broadcastBurst(p, total)

	// The serve loop never waited on the stuck subscriber: the ping answers
	// far inside the default send deadline, on a burst larger than any
	// subscriber's buffers.
	if err := wire.WriteMsg(serveCli, wire.Msg{Type: "req", Action: "ping", ID: 77}); err != nil {
		t.Fatalf("send the ping: %v", err)
	}
	for answered := false; !answered; {
		select {
		case m, ok := <-serveMsgs:
			if !ok {
				t.Fatal("the serve conn closed before the ping reply")
			}
			if m.Type == "resp" && m.ID == 77 && m.OK {
				answered = true
			}
		case <-time.After(time.Second):
			t.Fatal("no ping reply within a second, the serve loop stalled")
		}
	}

	waitFor(t, 5*time.Second, func() bool { return p.subEvictions() == 1 }, "the eviction")
	p.mu.Lock()
	_, stillThere := p.subs[stuck]
	p.mu.Unlock()
	if stillThere {
		t.Fatal("the stuck subscriber is still in the pane's map")
	}
	p.mu.Lock()
	_, healthyThere := p.subs[healthy]
	p.mu.Unlock()
	if !healthyThere {
		t.Fatal("the healthy subscriber was evicted with the stuck one")
	}
	closedSoon(t, stuckEnd, time.Second, "the stuck subscriber")

	if got := p.produced; got != int64(total) {
		t.Fatalf("produced = %d, want %d", got, total)
	}
	if p.ring.Len() != min(total, ringDefault) {
		t.Fatalf("ring holds %d bytes, want %d: the eviction disturbed it", p.ring.Len(), min(total, ringDefault))
	}

	_ = healthyEnd.Close()
	if got := collectBytes(healthyMsgs); !bytes.Equal(got, want) {
		t.Fatalf("the healthy subscriber received %d bytes, want %d in order", len(got), len(want))
	}

	// The evicted client reconnects and starts fresh: a new conn works.
	_, freshEnd := subPipe(t, p, 0)
	freshMsgs := pump(freshEnd)
	p.mu.Lock()
	p.broadcastLocked(wire.Msg{Type: "status", St: &p.st})
	p.mu.Unlock()
	select {
	case m, ok := <-freshMsgs:
		if !ok || m.Type != "status" {
			t.Fatalf("fresh subscriber got %+v, want the status broadcast", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the fresh subscriber never received a broadcast")
	}
}

// TestBroadcastEvictsOnSendDeadline proves the deadline path: a subscriber
// that reads part of a frame and stops holds the writer inside its write, the
// deadline ends it, and the subscriber is evicted once while a subscriber
// that keeps reading loses nothing.
func TestBroadcastEvictsOnSendDeadline(t *testing.T) {
	t.Parallel()
	p := broadcastPane(t)
	healthy, healthyEnd := subPipe(t, p, 0)
	_, slowEnd := subPipe(t, p, 250*time.Millisecond)
	healthyMsgs := pump(healthyEnd)

	const total = 64 << 10 // inside the queue bound, so only the deadline can evict
	want := broadcastBurst(p, total)

	// The slow subscriber consumes part of the first frame, then stops
	// reading: the writer stays inside its write until the deadline.
	deadline := time.Now().Add(5 * time.Second)
	_ = slowEnd.SetReadDeadline(deadline)
	if _, err := io.ReadFull(slowEnd, make([]byte, 100)); err != nil {
		t.Fatalf("partial read: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool { return p.subEvictions() == 1 }, "the eviction")
	p.mu.Lock()
	_, stillThere := p.subs[healthy]
	p.mu.Unlock()
	if !stillThere {
		t.Fatal("the healthy subscriber was evicted with the slow one")
	}
	closedSoon(t, slowEnd, time.Second, "the timed-out subscriber")

	_ = healthyEnd.Close()
	if got := collectBytes(healthyMsgs); !bytes.Equal(got, want) {
		t.Fatalf("the healthy subscriber received %d bytes, want %d in order", len(got), len(want))
	}
}

// TestSubscriberDisconnectIsNotAnEviction proves a subscriber that leaves the
// ordinary way is not counted, even when the writer is inside a write to it
// when it goes: its serve loop's cleanup stops the writer, and the pane never
// records an eviction.
func TestSubscriberDisconnectIsNotAnEviction(t *testing.T) {
	t.Parallel()
	p := broadcastPane(t)
	serveConn, cli := net.Pipe()
	t.Cleanup(func() { _ = cli.Close() })
	go p.serve(serveConn)
	msgs := pump(cli)
	waitFor(t, 5*time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.subs) == 1
	}, "the serve loop's registration")

	p.mu.Lock()
	p.broadcastLocked(wire.Msg{Type: "status", St: &p.st})
	p.mu.Unlock()
	select {
	case m, ok := <-msgs:
		if !ok || m.Type != "status" {
			t.Fatalf("got %+v, want the status broadcast", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no broadcast arrived")
	}

	// A write in flight when the client vanishes: the writer's failure must
	// read as the shutdown it races, not as an eviction.
	p.mu.Lock()
	p.broadcastLocked(wire.Msg{Type: "bytes", Data: bytes.Repeat([]byte("x"), 512<<10)})
	p.mu.Unlock()
	_ = cli.Close()

	waitFor(t, 5*time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.subs) == 0
	}, "the serve loop's cleanup")
	if got := p.subEvictions(); got != 0 {
		t.Fatalf("subEvictions = %d, want 0: a disconnect is not an eviction", got)
	}
}
