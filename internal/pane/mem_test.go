package pane

import (
	"bytes"
	"dm/internal/vt"
	"dm/internal/wire"
	"fmt"
	"net"
	"runtime"
	"testing"
	"time"
)

// memBound is what a pane may keep after a burst: the fixed ring, one screen
// and the small allocations the VT parser keeps. Anything near the burst size
// means history is being retained rather than wrapped over.
const memBound = 2 << 20

// ringStream is a byte stream wider than the ring, so every test that fills
// the ring wraps it several times. Records are fixed width, so a tail slice is
// an exact expectation.
func ringStream(t *testing.T, size int) []byte {
	t.Helper()
	var b bytes.Buffer
	for i := 0; b.Len() < size; i++ {
		fmt.Fprintf(&b, "%08d\n", i)
	}
	return b.Bytes()
}

// writeChunks feeds a stream through a write path in PTY-sized reads.
func writeChunks(b []byte, write func([]byte)) {
	const chunk = 32 << 10
	for len(b) > 0 {
		n := min(len(b), chunk)
		write(b[:n])
		b = b[n:]
	}
}

// TestScrollRingFixed fills the ring past its bound in small writes and holds
// it to three properties: no write below the bound allocates, the length never
// passes ringMax, and the bytes are exactly the newest tail of the stream.
func TestScrollRingFixed(t *testing.T) {
	r := &scrollRing{}
	chunk := bytes.Repeat([]byte("0123456789abcdef"), 2<<10)
	if allocs := testing.AllocsPerRun(64, func() { r.Write(chunk) }); allocs != 0 {
		t.Fatalf("a write below the ring bound allocated %.1f objects, want 0", allocs)
	}

	// Below the bound the ring is a plain buffer: the bytes come back in order
	// and the write index alone does not decide where they start.
	r = &scrollRing{}
	r.Write([]byte("hello"))
	if got := r.Bytes(); string(got) != "hello" {
		t.Fatalf("one short write came back as %q", got)
	}

	stream := ringStream(t, 4*ringMax+12345)
	r = &scrollRing{}
	writeChunks(stream, func(b []byte) {
		r.Write(b)
		if r.Len() > ringMax {
			t.Fatalf("ring holds %d bytes, over the %d bound", r.Len(), ringMax)
		}
	})
	if r.Len() != ringMax {
		t.Fatalf("ring holds %d bytes after the burst, want %d", r.Len(), ringMax)
	}
	want := stream[len(stream)-ringMax:]
	if got := r.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("ring content differs from the newest tail: %d bytes, first difference at %d",
			len(got), firstDiff(got, want))
	}

	// A write straddling the buffer end keeps both parts in order.
	edge := &scrollRing{}
	edge.Write(stream[:ringMax-3])
	edge.Write(stream[ringMax-3 : ringMax+7])
	edgeWant := stream[:ringMax+7]
	edgeWant = edgeWant[len(edgeWant)-ringMax:]
	if got := edge.Bytes(); edge.Len() != ringMax || !bytes.Equal(got, edgeWant) {
		t.Fatalf("write across the buffer end left %d bytes, first difference at %d",
			len(got), firstDiff(got, edgeWant))
	}

	// One write larger than the ring keeps only its own tail.
	huge := bytes.Repeat([]byte("z"), ringMax+4096)
	big := &scrollRing{}
	big.Write(huge)
	if big.Len() != ringMax || !bytes.Equal(big.Bytes(), huge[len(huge)-ringMax:]) {
		t.Fatalf("oversized write left %d bytes, want the last %d of the write",
			big.Len(), ringMax)
	}
}

func firstDiff(a, b []byte) int {
	for i := range a {
		if i >= len(b) || a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

// TestReplayAfterWrap asks a real pane connection for the replay once the ring
// has wrapped, which is the path a client uses when it attaches late.
func TestReplayAfterWrap(t *testing.T) {
	stream := ringStream(t, 3*ringMax)
	r := &scrollRing{}
	writeChunks(stream, r.Write)

	p := &paneProc{
		id:   "w1p1",
		vt:   vt.NewTerm(80, 24),
		ring: r,
		subs: map[*subConn]bool{},
		st:   wire.Status{State: "unknown"},
	}
	serve, cli := net.Pipe()
	defer cli.Close()
	go p.serve(serve)

	_ = cli.SetDeadline(time.Now().Add(10 * time.Second))
	if err := wire.WriteMsg(cli, wire.Msg{Type: "req", Action: "replay"}); err != nil {
		t.Fatalf("send replay request: %v", err)
	}
	reply, err := wire.ReadMsg(cli)
	if err != nil {
		t.Fatalf("read replay reply: %v", err)
	}
	if reply.Type != "replay" {
		t.Fatalf("reply type %q, want replay", reply.Type)
	}
	want := stream[len(stream)-ringMax:]
	if !bytes.Equal(reply.Data, want) {
		t.Fatalf("replay carried %d bytes, want the %d-byte tail; first difference at %d",
			len(reply.Data), len(want), firstDiff(reply.Data, want))
	}
}

// TestPaneHeapAfterBurst runs a few MiB through the pane's ring and VT and
// requires the retained heap to stay under memBound once garbage is collected.
func TestPaneHeapAfterBurst(t *testing.T) {
	const burstSize = 4 << 20
	burst := ringStream(t, burstSize)

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	p := &paneProc{
		id:   "w1p1",
		vt:   vt.NewTerm(80, 24),
		ring: &scrollRing{},
		st:   wire.Status{State: "unknown"},
	}
	writeChunks(burst, func(b []byte) {
		p.vt.Write(b)
		p.ring.Write(b)
	})

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("retained heap after a %d MiB burst: %d KiB", burstSize>>20, retained>>10)
	if retained > memBound {
		t.Fatalf("retained %d KiB after a %d MiB burst, want at most %d KiB",
			retained>>10, burstSize>>20, memBound>>10)
	}
	if p.ring.Len() != ringMax {
		t.Fatalf("ring holds %d bytes after the burst, want %d", p.ring.Len(), ringMax)
	}
	want := burst[len(burst)-ringMax:]
	if got := p.ring.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("ring after the burst does not hold the newest tail")
	}
}
