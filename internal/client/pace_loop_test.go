package client

import (
	"bytes"
	"testing"
	"time"
)

// newPaceTUI builds a one-pane client whose frames land in a buffer through a
// frameSink, so the tick body can be driven with a synthetic clock. No
// terminal, no socket, no goroutine.
func newPaceTUI(t *testing.T) (*frameTUI, *pacer, *frameSink, *bytes.Buffer) {
	t.Helper()
	f := newFrameTUI(t, 100, 20, 34)
	out := &bytes.Buffer{}
	sink := &frameSink{w: out}
	f.out = out
	f.tui.out = sink
	for _, term := range f.tui.vts {
		term.Flush()
	}
	return f, &pacer{}, sink, out
}

// TestTickDirtyDrawsOneFrame proves a dirty tick draws exactly one frame and
// counts it.
func TestTickDirtyDrawsOneFrame(t *testing.T) {
	f, pace, sink, out := newPaceTUI(t)
	now := time.Unix(1000, 0)

	f.tui.dirty = true
	f.tui.tick(now, pace, sink)
	if out.Len() == 0 {
		t.Fatal("a dirty tick drew nothing")
	}
	if f.tui.dirty {
		t.Fatal("draw left the dirty flag set")
	}
	if got := pace.stats().Frames; got != 1 {
		t.Fatalf("frames = %d, want 1", got)
	}

	// A second dirty tick inside the same interval draws nothing.
	out.Reset()
	f.tui.dirty = true
	every := f.tui.tick(now, pace, sink)
	if out.Len() != 0 {
		t.Fatalf("second tick inside the interval wrote %d bytes: %q", out.Len(), out.String())
	}
	if got := pace.stats().Frames; got != 1 {
		t.Fatalf("frames after a refused draw = %d, want 1", got)
	}
	if every != 2*paceFloor {
		t.Fatalf("interval after an empty frame = %v, want %v", every, 2*paceFloor)
	}
}

// TestTickCleanCountsNoFrame proves a clean tick draws nothing, counts no
// frame and decays the interval.
func TestTickCleanCountsNoFrame(t *testing.T) {
	f, pace, sink, out := newPaceTUI(t)
	now := time.Unix(1000, 0)

	f.tui.dirty = true
	f.tui.tick(now, pace, sink)
	if got := pace.stats().Frames; got != 1 {
		t.Fatalf("frames after the first draw = %d, want 1", got)
	}

	for i := 0; i < 3; i++ {
		out.Reset()
		now = now.Add(pace.interval())
		every := f.tui.tick(now, pace, sink)
		if out.Len() != 0 {
			t.Fatalf("clean tick %d wrote %d bytes: %q", i, out.Len(), out.String())
		}
		if every != pace.interval() {
			t.Fatalf("tick %d returned %v, pacer interval is %v", i, every, pace.interval())
		}
	}
	if got := pace.stats().Frames; got != 1 {
		t.Fatalf("frames after clean ticks = %d, want 1", got)
	}
	if got := pace.interval(); got != 8*paceFloor {
		t.Fatalf("interval after three empty frames = %v, want %v", got, 8*paceFloor)
	}
}

// TestTickWakeAfterIdleDrawsAtFloor proves a wake after an idle run draws on
// the next floor tick and counts the frame.
func TestTickWakeAfterIdleDrawsAtFloor(t *testing.T) {
	f, pace, sink, out := newPaceTUI(t)
	now := time.Unix(1000, 0)

	// Idle out to the ceiling.
	for i := 0; i < 6; i++ {
		now = now.Add(pace.interval())
		f.tui.tick(now, pace, sink)
	}
	if got := pace.interval(); got != paceCeiling {
		t.Fatalf("interval after idle = %v, want %v", got, paceCeiling)
	}
	if got := pace.stats().Frames; got != 0 {
		t.Fatalf("idle ticks counted %d frames, want 0", got)
	}

	// A key or a message arrives and marks the frame dirty.
	now = now.Add(3 * time.Millisecond)
	pace.reset()
	f.tui.dirty = true
	pace.arm(now)

	out.Reset()
	every := f.tui.tick(now.Add(paceFloor), pace, sink)
	if out.Len() == 0 {
		t.Fatal("a dirty tick one floor interval after a wake drew nothing")
	}
	if got := pace.stats().Frames; got != 1 {
		t.Fatalf("frames = %d, want 1", got)
	}
	if every != paceFloor {
		t.Fatalf("interval after a written frame = %v, want %v", every, paceFloor)
	}
}

// TestTickStaleAfterResetDoesNotDouble proves the tick Go leaves buffered
// across Ticker.Reset is not treated as an empty frame, so a reset does not
// immediately cost an interval step.
func TestTickStaleAfterResetDoesNotDouble(t *testing.T) {
	f, pace, sink, _ := newPaceTUI(t)
	now := time.Unix(1000, 0)

	// Idle out to the ceiling, the interval the reset has to undo.
	for i := 0; i < 6; i++ {
		now = now.Add(pace.interval())
		f.tui.tick(now, pace, sink)
	}
	if got := pace.interval(); got != paceCeiling {
		t.Fatalf("interval after idle = %v, want %v", got, paceCeiling)
	}

	now = now.Add(time.Millisecond)
	pace.reset()
	pace.arm(now)
	// The stale tick arrives right after the ticker reset.
	if every := f.tui.tick(now.Add(time.Millisecond), pace, sink); every != paceFloor {
		t.Fatalf("stale tick set the interval to %v, want %v", every, paceFloor)
	}
	// The next real tick is one floor after the reset and decays one step.
	now = now.Add(paceFloor)
	if every := f.tui.tick(now, pace, sink); every != 2*paceFloor {
		t.Fatalf("first real tick set the interval to %v, want %v", every, 2*paceFloor)
	}
	if got := pace.stats().Frames; got != 0 {
		t.Fatalf("stale and clean ticks counted %d frames, want 0", got)
	}
}

// TestTickStaleStillRunsTimers pins the stale branch itself. A tick buffered
// across a Ticker.Reset draws nothing, yet the expiry timers must still run:
// a due toast expires, a pending cluster is flushed, and the interval the reset
// just floored does not double.
func TestTickStaleStillRunsTimers(t *testing.T) {
	f, pace, sink, out := newPaceTUI(t)
	base := time.Now()
	term := f.tui.vts["w1p1"]

	// A toast is due to expire, and a cluster has been quiet long enough for
	// the idle flush. Both are owed on the tick.
	f.tui.toasts = []toast{{text: "saved", at: base.Add(-toastLifetime)}}
	term.Write([]byte("こ"))
	f.tui.noteBytes("w1p1", base.Add(-idleFlush))

	pace.arm(base)
	f.tui.dirty = true

	out.Reset()
	every := f.tui.tick(base.Add(time.Millisecond), pace, sink)
	if out.Len() != 0 {
		t.Fatalf("stale tick wrote %d bytes: %q", out.Len(), out.String())
	}
	if got := pace.stats().Frames; got != 0 {
		t.Fatalf("stale tick counted %d frames, want 0", got)
	}
	if len(f.tui.toasts) != 0 {
		t.Fatalf("stale tick skipped toast expiry: %d toasts left", len(f.tui.toasts))
	}
	if got := termCell(term, 9, 0).Content; got != "こ" {
		t.Fatalf("cell col 9, row 0 = %q after the stale tick, want こ: the idle flush did not run", got)
	}
	if every != paceFloor || pace.interval() != paceFloor {
		t.Fatalf("stale tick set the interval to %v (pacer %v), want %v", every, pace.interval(), paceFloor)
	}
}

// TestTickMessageStreamStillDraws proves the production shape: a pane printing
// faster than the floor resets the pacer before every tick, and the tick still
// draws, so the renderer is never starved. The expiry timers run on the same
// tick: a toast expires and a pending cluster is flushed.
func TestTickMessageStreamStillDraws(t *testing.T) {
	f, pace, sink, out := newPaceTUI(t)
	base := time.Now()
	term := f.tui.vts["w1p1"]

	// A partial cluster waits for the idle flush, and a toast is due to
	// expire on the tick.
	term.Write([]byte("こ"))
	f.tui.noteBytes("w1p1", base)
	f.tui.toasts = []toast{{text: "saved", at: base.Add(-toastLifetime)}}

	pace.arm(base)
	now := base
	for i := 0; i < 16; i++ {
		now = now.Add(time.Millisecond)
		pace.reset()
		f.tui.dirty = true
	}
	if now.Sub(base) != paceFloor {
		t.Fatalf("stream covers %v, want one floor interval", now.Sub(base))
	}

	out.Reset()
	every := f.tui.tick(now, pace, sink)
	if out.Len() == 0 {
		t.Fatal("a message stream starved the draw: the tick wrote nothing")
	}
	if got := pace.stats().Frames; got != 1 {
		t.Fatalf("frames = %d, want 1", got)
	}
	if every != paceFloor {
		t.Fatalf("interval after a written frame = %v, want %v", every, paceFloor)
	}
	if len(f.tui.toasts) != 0 {
		t.Fatalf("the tick skipped toast expiry: %d toasts left", len(f.tui.toasts))
	}
	if got := termCell(term, 9, 0).Content; got != "こ" {
		t.Fatalf("cell col 9, row 0 = %q after the tick, want こ: the idle flush did not run", got)
	}
}
