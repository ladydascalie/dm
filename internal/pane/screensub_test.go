package pane

import (
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"dm/internal/vt"
	"dm/internal/wire"
)

// picPane is a pane with no pty: a picture subscriber only needs the VT and the
// subscriber set.
func picPane(t *testing.T) *paneProc {
	t.Helper()
	return &paneProc{
		id: "w1p1", vt: vt.NewTerm(40, 6), ring: NewRing(ringDefault),
		subs: map[*subConn]bool{}, st: wire.Status{State: "idle"},
	}
}

// dialPic gives the pane one more connection and asks it for pictures. It
// returns the frames the conn sends and the pane's screen as the request
// answered it. The subscription is registered under the pane's lock with its
// first picture, so the caller knows nothing is still in flight when it
// publishes.
func dialPic(t *testing.T, p *paneProc) (chan wire.Msg, *vt.Snapshot) {
	t.Helper()
	serve, cli := net.Pipe()
	t.Cleanup(func() { _ = cli.Close() })
	go p.serve(serve)
	msgs := pump(cli)
	p.mu.Lock()
	subs := len(p.picSubsLocked())
	p.mu.Unlock()
	if err := wire.WriteMsg(cli, wire.Msg{Type: "req", Action: "screen-sub", ID: 1}); err != nil {
		t.Fatalf("send screen-sub: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.picSubsLocked()) > subs
	}, "the pane to register a picture subscriber")
	return msgs, nextScreen(t, msgs, 2*time.Second)
}

// nextScreen returns the next screen frame within the wait and fails on a bytes
// frame, which a picture subscriber must never be handed.
func nextScreen(t *testing.T, msgs chan wire.Msg, wait time.Duration) *vt.Snapshot {
	t.Helper()
	deadline := time.After(wait)
	for {
		select {
		case m, ok := <-msgs:
			if !ok {
				t.Fatal("the connection closed before a screen")
			}
			if m.Type == "bytes" {
				t.Fatalf("picture subscriber received a %d byte frame", len(m.Data))
			}
			if m.Type == "screen" {
				if m.VTSnap == nil {
					t.Fatal("screen frame carried no snapshot")
				}
				return m.VTSnap
			}
		case <-deadline:
			t.Fatalf("no screen frame within %s", wait)
		}
	}
}

// screensWithin gathers the screen frames that arrive in the window, so a test
// can count what one burst of output cost.
func screensWithin(t *testing.T, msgs chan wire.Msg, window time.Duration) []*vt.Snapshot {
	t.Helper()
	deadline := time.After(window)
	var out []*vt.Snapshot
	for {
		select {
		case m, ok := <-msgs:
			if !ok {
				return out
			}
			if m.Type == "bytes" {
				t.Fatalf("picture subscriber received a %d byte frame", len(m.Data))
			}
			if m.Type == "screen" {
				out = append(out, m.VTSnap)
			}
		case <-deadline:
			return out
		}
	}
}

// picText joins a snapshot's rows into the text a renderer would paint.
func picText(snap *vt.Snapshot) string {
	var b strings.Builder
	for _, row := range snap.Lines {
		for _, c := range row.Cells {
			b.WriteString(c.Text)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// publish feeds one chunk through the path the read loop uses.
func publish(p *paneProc, s string) {
	p.mu.Lock()
	p.publishLocked([]byte(s))
	p.mu.Unlock()
}

// TestScreenSubPaintsAfterWrite proves a subscriber takes the pane's screen when
// it asks, then a fresh one once the pane writes.
func TestScreenSubPaintsAfterWrite(t *testing.T) {
	t.Parallel()
	p := picPane(t)
	msgs, first := dialPic(t, p)
	if first.Cols != 40 || first.Rows != 6 {
		t.Fatalf("first screen is %dx%d, want 40x6", first.Cols, first.Rows)
	}

	publish(p, "ready")
	snap := nextScreen(t, msgs, 2*time.Second)
	if got := picText(snap); !strings.Contains(got, "ready") {
		t.Fatalf("pushed screen %q does not carry the write", got)
	}
}

// TestScreenSubCoalescesWindow proves two writes inside one window cost one
// screen, not two, and the screen carries the final content.
func TestScreenSubCoalescesWindow(t *testing.T) {
	t.Parallel()
	p := picPane(t)
	msgs, _ := dialPic(t, p)

	publish(p, "ab")
	publish(p, "cd")
	screens := screensWithin(t, msgs, 3*picWindow)
	if len(screens) != 1 {
		t.Fatalf("%d screens for two writes inside one window, want 1", len(screens))
	}
	if got := picText(screens[0]); !strings.Contains(got, "abcd") {
		t.Fatalf("coalesced screen %q", got)
	}
}

// TestScreenSubSharedByTwo proves a second subscriber rides the one push: each
// sees one screen for the burst, and the pane takes one snapshot for both.
func TestScreenSubSharedByTwo(t *testing.T) {
	t.Parallel()
	p := picPane(t)
	one, _ := dialPic(t, p)
	two, _ := dialPic(t, p)

	publish(p, "twice")
	first := screensWithin(t, one, 3*picWindow)
	second := screensWithin(t, two, 3*picWindow)
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("%d and %d screens for one write, want 1 each", len(first), len(second))
	}
	if !strings.Contains(picText(first[0]), "twice") || !strings.Contains(picText(second[0]), "twice") {
		t.Fatalf("screens %q and %q", picText(first[0]), picText(second[0]))
	}
}

// TestScreenSubClosedConnLeavesNothing proves a closed subscriber is forgotten,
// is sent nothing, and leaves no goroutine behind: the pane's serve loop, its
// subscriber writer and the test's own reader all end.
//
// Serial, not parallel: it counts the process's goroutines, and a concurrent
// test's would pollute the count.
func TestScreenSubClosedConnLeavesNothing(t *testing.T) {
	base := runtime.NumGoroutine()
	p := picPane(t)
	serve, cli := net.Pipe()
	go p.serve(serve)
	msgs := pump(cli)
	if err := wire.WriteMsg(cli, wire.Msg{Type: "req", Action: "screen-sub", ID: 1}); err != nil {
		t.Fatalf("send screen-sub: %v", err)
	}
	nextScreen(t, msgs, 2*time.Second)

	if err := cli.Close(); err != nil {
		t.Fatalf("close the subscriber: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.subs) == 0
	}, "the pane to forget the closed subscriber")

	// A publish with no picture subscriber arms no window, and the conn that
	// left is handed nothing.
	publish(p, "nobody")
	for m := range msgs {
		if m.Type == "screen" {
			t.Fatal("a closed subscriber was sent a screen")
		}
	}
	waitFor(t, 3*time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.picTimer == nil
	}, "the push window to pass")
	waitFor(t, 3*time.Second, func() bool {
		return runtime.NumGoroutine() <= base
	}, "the pane's goroutines to end")
}
