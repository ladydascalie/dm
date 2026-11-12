package pane

import (
	"bytes"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"dm/internal/vt"
	"dm/internal/wire"
)

// TestRingResizeShrinkKeepsTheTail proves a shrink keeps the newest bytes,
// oldest first, even when the buffer has already wrapped.
func TestRingResizeShrinkKeepsTheTail(t *testing.T) {
	t.Parallel()
	r := NewRing(2 * ringMin)
	stream := ringStream(t, 3*ringMin)
	r.Write(stream)

	r.Resize(ringMin)
	if got := r.Budget(); got != ringMin {
		t.Fatalf("budget after shrink = %d, want %d", got, ringMin)
	}
	if got := r.Len(); got != ringMin {
		t.Fatalf("bytes held after shrink = %d, want %d", got, ringMin)
	}
	want := stream[len(stream)-ringMin:]
	if got := r.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("shrink kept the wrong bytes: first difference at %d", firstDiff(got, want))
	}

	// The resized ring keeps writing and still drops the oldest.
	next := bytes.Repeat([]byte("z"), ringMin+7)
	r.Write(next)
	if got := r.Len(); got != ringMin {
		t.Fatalf("bytes held after a write past the new budget = %d, want %d", got, ringMin)
	}
	want = next[len(next)-ringMin:]
	if got := r.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("write after shrink kept the wrong bytes: first difference at %d", firstDiff(got, want))
	}
}

// TestRingResizeKeepsTheTailAcrossAWrap proves a grow keeps everything held
// and a later shrink after another wrap still keeps the newest bytes.
func TestRingResizeKeepsTheTailAcrossAWrap(t *testing.T) {
	t.Parallel()
	r := NewRing(ringMin)
	first := ringStream(t, 3*ringMin)
	r.Write(first)

	kept := first[len(first)-ringMin:]
	r.Resize(2 * ringMin)
	if got := r.Budget(); got != 2*ringMin {
		t.Fatalf("budget after grow = %d, want %d", got, 2*ringMin)
	}
	if got := r.Bytes(); !bytes.Equal(got, kept) {
		t.Fatalf("grow lost held bytes: first difference at %d", firstDiff(got, kept))
	}

	// New bytes land after the kept tail and the grown ring holds both.
	second := bytes.Repeat([]byte("q"), ringMin)
	r.Write(second)
	if got := r.Len(); got != 2*ringMin {
		t.Fatalf("bytes held after the grow write = %d, want %d", got, 2*ringMin)
	}
	together := append(append([]byte{}, kept...), second...)
	if got := r.Bytes(); !bytes.Equal(got, together) {
		t.Fatalf("grown ring lost order: first difference at %d", firstDiff(got, together))
	}

	// Another wrap, then a shrink: the newest bytes survive.
	third := ringStream(t, 2*ringMin)
	r.Write(third)
	together = append(together, third...)
	want := together[len(together)-ringMin:]
	r.Resize(ringMin)
	if got := r.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("shrink after a wrap kept the wrong bytes: first difference at %d",
			firstDiff(got, want))
	}
}

// TestSetHistoryActionResizesTheRing proves the pane socket action applies a
// valid budget, refuses one outside the range, and leaves the ring alone on a
// refusal.
func TestSetHistoryActionResizesTheRing(t *testing.T) {
	t.Parallel()
	r := NewRing(ringDefault)
	r.Write(ringStream(t, ringDefault+1024))

	p := &paneProc{
		id: "w1p1", vt: vt.NewTerm(20, 5), ring: r,
		subs: map[*subConn]bool{}, st: wire.Status{State: "unknown"},
	}
	serve, cli := net.Pipe()
	defer func() { _ = cli.Close() }()
	go p.serve(serve)
	_ = cli.SetDeadline(time.Now().Add(10 * time.Second))

	ask := func(budget int, id int) wire.Msg {
		t.Helper()
		err := wire.WriteMsg(cli, wire.Msg{Type: "req", Action: "set-history",
			History: budget, ID: id})
		if err != nil {
			t.Fatalf("send set-history: %v", err)
		}
		reply, err := wire.ReadMsg(cli)
		if err != nil {
			t.Fatalf("read set-history reply: %v", err)
		}
		return reply
	}

	reply := ask(2*ringMin, 1)
	if !reply.OK {
		t.Fatalf("set-history refused a valid budget: %s", reply.Error)
	}
	if got := r.Budget(); got != 2*ringMin {
		t.Fatalf("budget after set-history = %d, want %d", got, 2*ringMin)
	}
	if got := r.Len(); got != 2*ringMin {
		t.Fatalf("bytes held after set-history = %d, want %d", got, 2*ringMin)
	}

	for i, bad := range []int{ringMin - 1, ringMax + 1, 0} {
		reply = ask(bad, 2+i)
		if reply.OK || reply.Error == "" {
			t.Fatalf("set-history %d: ok=%v error=%q, want a refusal", bad, reply.OK, reply.Error)
		}
		if got := r.Budget(); got != 2*ringMin {
			t.Fatalf("a refused set-history %d changed the budget to %d", bad, got)
		}
	}
}

// TestParsePaneHistoryFlag pins the flag's default and its bounds.
func TestParsePaneHistoryFlag(t *testing.T) {
	t.Parallel()
	o, err := parsePaneArgs([]string{"--id", "p1", "--session", "s1"})
	if err != nil {
		t.Fatalf("parsePaneArgs with no history: %v", err)
	}
	if o.history != ringDefault {
		t.Fatalf("default history = %d, want %d", o.history, ringDefault)
	}
	if ringDefault != 512<<10 {
		t.Fatalf("ringDefault = %d, want the 512 KiB ring", ringDefault)
	}

	for _, good := range []int{ringMin, ringMax} {
		args := []string{"--id", "p1", "--session", "s1", "--history", strconv.Itoa(good)}
		if _, err := parsePaneArgs(args); err != nil {
			t.Fatalf("parsePaneArgs --history %d: %v", good, err)
		}
	}
	for _, bad := range []int{ringMin - 1, ringMax + 1, 0, -1} {
		args := []string{"--id", "p1", "--session", "s1", "--history", strconv.Itoa(bad)}
		_, err := parsePaneArgs(args)
		if err == nil || !strings.Contains(err.Error(), "history budget") {
			t.Fatalf("parsePaneArgs --history %d = %v, want the budget error", bad, err)
		}
	}
}
