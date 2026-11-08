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

	// A second dirty tick inside the interval draws nothing and leaves the
	// interval at the floor: a frame is still owed, so nothing decays.
	out.Reset()
	f.tui.dirty = true
	f.tui.tick(now, pace, sink)
	if out.Len() != 0 {
		t.Fatalf("second tick inside the interval wrote %d bytes: %q", out.Len(), out.String())
	}
	if got := pace.stats().Frames; got != 1 {
		t.Fatalf("frames after a refused draw = %d, want 1", got)
	}
	if got := pace.interval(); got != paceFloor {
		t.Fatalf("interval after a refused draw = %v, want %v", got, paceFloor)
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
		f.tui.tick(now, pace, sink)
		if out.Len() != 0 {
			t.Fatalf("clean tick %d wrote %d bytes: %q", i, out.Len(), out.String())
		}
	}
	if got := pace.stats().Frames; got != 1 {
		t.Fatalf("frames after clean ticks = %d, want 1", got)
	}
	if got := pace.interval(); got != 8*paceFloor {
		t.Fatalf("interval after three empty frames = %v, want %v", got, 8*paceFloor)
	}
}

// TestWakeAfterIdleDrawsAtOnce proves a key or a message after an idle run
// draws on the wake itself, not on a tick the idle decay had armed out at the
// ceiling. It is the typing-latency guard: the wake draws, and the loop's
// re-arm rule pulls the ticker back to the floor for the frame after it.
func TestWakeAfterIdleDrawsAtOnce(t *testing.T) {
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

	// A key arrives well before the ticker would have fired.
	now = now.Add(3 * time.Millisecond)
	f.tui.dirty = true
	out.Reset()
	f.tui.wake(now, pace, sink)
	if out.Len() == 0 {
		t.Fatal("a wake after an idle run drew nothing: the frame waited for the idle tick")
	}
	if got := pace.stats().Frames; got != 1 {
		t.Fatalf("frames = %d, want 1", got)
	}
	if got := pace.interval(); got != paceFloor {
		t.Fatalf("interval after a wake draw = %v, want %v", got, paceFloor)
	}
	if every, ok := pace.rearm(paceCeiling); !ok || every != paceFloor {
		t.Fatalf("rearm a ceiling ticker = %v, %v, want %v, true", every, ok, paceFloor)
	}

	// A second wake inside the floor draws nothing, so a burst still
	// coalesces, and does not decay the interval.
	out.Reset()
	f.tui.dirty = true
	f.tui.wake(now.Add(time.Millisecond), pace, sink)
	if out.Len() != 0 {
		t.Fatalf("a wake inside the floor wrote %d bytes: %q", out.Len(), out.String())
	}
	if got := pace.interval(); got != paceFloor {
		t.Fatalf("interval after a refused wake = %v, want %v", got, paceFloor)
	}
	if every, ok := pace.rearm(paceFloor); ok || every != paceFloor {
		t.Fatalf("rearm a floor ticker = %v, %v, want %v, false", every, ok, paceFloor)
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
	f.tui.tick(now, pace, sink)
	if out.Len() == 0 {
		t.Fatal("a message stream starved the draw: the tick wrote nothing")
	}
	if got := pace.stats().Frames; got != 1 {
		t.Fatalf("frames = %d, want 1", got)
	}
	if got := pace.interval(); got != paceFloor {
		t.Fatalf("interval after a written frame = %v, want %v", got, paceFloor)
	}
	if len(f.tui.toasts) != 0 {
		t.Fatalf("the tick skipped toast expiry: %d toasts left", len(f.tui.toasts))
	}
	if got := termCell(term, 9, 0).Content; got != "こ" {
		t.Fatalf("cell col 9, row 0 = %q after the tick, want こ: the idle flush did not run", got)
	}
}
