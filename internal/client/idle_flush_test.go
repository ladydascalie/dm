package client

import (
	"testing"
	"time"

	"dm/internal/vt"
	"dm/internal/wire"
)

// TestFlushIdlePlacesLastCluster proves a pane that writes once and waits shows
// its last grapheme cluster, and that a pane still receiving bytes is left
// alone.
func TestFlushIdlePlacesLastCluster(t *testing.T) {
	t.Parallel()
	term := vt.NewTerm(20, 3)
	tui := &tui{vts: map[string]*vt.Term{"a": term}}
	now := time.Now()

	term.Write([]byte("こ"))
	tui.noteBytes("a", now)
	if got := termCell(term, 0, 0).Content; got != " " {
		t.Fatalf("cell 0,0 = %q before the flush, want a blank cell: the parser holds the cluster", got)
	}
	if tui.flushIdle(now.Add(idleFlush / 2)) {
		t.Fatal("flushed a pane that is still receiving bytes")
	}
	if !tui.flushIdle(now.Add(idleFlush)) {
		t.Fatal("no flush after the idle tick")
	}
	if got := termCell(term, 0, 0).Content; got != "こ" {
		t.Fatalf("cell 0,0 = %q after the flush, want こ", got)
	}
	if tui.flushIdle(now.Add(2 * idleFlush)) {
		t.Fatal("flushed a pane with no pending bytes")
	}
}

// TestFlushIdleKeepsSplitCluster proves the idle rule does not break a cluster
// whose chunks arrive inside one tick.
func TestFlushIdleKeepsSplitCluster(t *testing.T) {
	t.Parallel()
	term := vt.NewTerm(20, 3)
	tui := &tui{vts: map[string]*vt.Term{"a": term}}
	now := time.Now()

	term.Write([]byte("e"))
	tui.noteBytes("a", now)
	term.Write([]byte("\u0301"))
	tui.noteBytes("a", now.Add(5*time.Millisecond))
	if tui.flushIdle(now.Add(idleFlush)) {
		t.Fatal("flushed a cluster whose second chunk arrived inside the tick")
	}
	tui.flushIdle(now.Add(5*time.Millisecond + idleFlush))
	if got := termCell(term, 0, 0).Content; got != "e\u0301" {
		t.Fatalf("cell 0,0 = %q, want the combined cluster", got)
	}
}

// TestApplyNotesBytes proves the bytes case records the pane's last output, so
// the ticker's flushIdle can tell a quiet pane from a busy one.
func TestApplyNotesBytes(t *testing.T) {
	t.Parallel()
	term := vt.NewTerm(20, 3)
	tui := &tui{vts: map[string]*vt.Term{"a": term}}
	tui.apply(wire.Msg{Type: "bytes", Target: "a", Data: []byte("x")})
	if _, ok := tui.lastByte["a"]; !ok {
		t.Fatal("apply did not record the pane's last bytes")
	}
}
