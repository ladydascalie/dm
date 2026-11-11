package client

import (
	"testing"
	"time"
)

// TestPacerBurstDrawsOnce proves a burst of dirty frames inside one floor
// interval coalesces into a single draw.
func TestPacerBurstDrawsOnce(t *testing.T) {
	t.Parallel()
	var p pacer
	now := time.Unix(1000, 0)

	if !p.shouldDraw(now, true) {
		t.Fatal("first dirty frame should draw")
	}
	p.drew(now)

	for i := 0; i < 5; i++ {
		now = now.Add(time.Millisecond)
		if p.shouldDraw(now, true) {
			t.Fatalf("frame %d drew inside one interval", i)
		}
	}
	if p.shouldDraw(now.Add(paceFloor), false) {
		t.Fatal("a clean frame must not draw")
	}
	if !p.shouldDraw(now.Add(paceFloor), true) {
		t.Fatal("a dirty frame a full interval later should draw")
	}
}

// TestPacerShouldDrawBoundary pins the boundary: exactly one floor interval
// after the last frame is due, one nanosecond less is not.
func TestPacerShouldDrawBoundary(t *testing.T) {
	t.Parallel()
	var p pacer
	now := time.Unix(1000, 0)
	p.drew(now)

	if p.shouldDraw(now.Add(paceFloor-time.Nanosecond), true) {
		t.Fatal("a frame just inside one floor interval drew")
	}
	if !p.shouldDraw(now.Add(paceFloor), true) {
		t.Fatal("a frame exactly one floor interval later did not draw")
	}
}

// TestPacerIdleDecay proves an idle run doubles the interval up to the ceiling
// and stops there.
func TestPacerIdleDecay(t *testing.T) {
	t.Parallel()
	var p pacer
	now := time.Unix(1000, 0)

	want := []time.Duration{
		paceFloor,
		2 * paceFloor,
		4 * paceFloor,
		8 * paceFloor,
		paceCeiling,
		paceCeiling,
		paceCeiling,
	}
	for i, w := range want {
		if got := p.interval(); got != w {
			t.Fatalf("interval %d = %v, want %v", i, got, w)
		}
		p.decay(now)
		now = now.Add(p.interval())
	}
}

// TestPacerDecayCountsNoFrame proves an empty frame advances the decay without
// counting a frame.
func TestPacerDecayCountsNoFrame(t *testing.T) {
	t.Parallel()
	var p pacer
	now := time.Unix(1000, 0)

	p.decay(now)
	p.decay(now)
	if got := p.interval(); got != 4*paceFloor {
		t.Fatalf("interval after two empty frames = %v, want %v", got, 4*paceFloor)
	}
	if got := p.stats().Frames; got != 0 {
		t.Fatalf("empty frames counted %d frames, want 0", got)
	}
}

// TestPacerWriteResets proves a frame that wrote bytes returns the interval to
// the floor.
func TestPacerWriteResets(t *testing.T) {
	t.Parallel()
	var p pacer
	now := time.Unix(1000, 0)

	for i := 0; i < 3; i++ {
		p.decay(now)
	}
	if got := p.interval(); got != 8*paceFloor {
		t.Fatalf("interval after idle = %v, want %v", got, 8*paceFloor)
	}
	p.drew(now)
	if got := p.interval(); got != paceFloor {
		t.Fatalf("interval after a write = %v, want %v", got, paceFloor)
	}
	if got := p.stats().Frames; got != 1 {
		t.Fatalf("frames = %d, want 1", got)
	}
}

// TestPacerResetDrawsAtFloor proves a wake after an idle run draws at the
// floor again.
func TestPacerResetDrawsAtFloor(t *testing.T) {
	t.Parallel()
	var p pacer
	now := time.Unix(1000, 0)

	if !p.shouldDraw(now, true) {
		t.Fatal("first dirty frame should draw")
	}
	p.drew(now)
	for i := 0; i < 4; i++ {
		now = now.Add(p.interval())
		p.decay(now)
	}
	if got := p.interval(); got != paceCeiling {
		t.Fatalf("interval after idle = %v, want %v", got, paceCeiling)
	}

	now = now.Add(10 * time.Millisecond)
	p.reset()
	if got := p.interval(); got != paceFloor {
		t.Fatalf("interval after reset = %v, want %v", got, paceFloor)
	}
	now = now.Add(paceFloor)
	if !p.shouldDraw(now, true) {
		t.Fatal("a dirty frame one floor interval after a reset should draw")
	}
}

// TestPacerRearmPullsADecayedTickerToTheFloor proves the loop's re-arm rule:
// a ticker the idle decay armed out at the ceiling is pulled back after a wake,
// and a ticker already at the floor is left alone so a message stream cannot
// push the frame out forever.
func TestPacerRearmPullsADecayedTickerToTheFloor(t *testing.T) {
	t.Parallel()
	var p pacer
	now := time.Unix(1000, 0)

	for i := 0; i < 6; i++ {
		p.decay(now)
	}
	if got := p.interval(); got != paceCeiling {
		t.Fatalf("interval after idle = %v, want %v", got, paceCeiling)
	}
	if every, ok := p.rearm(paceCeiling); ok || every != paceCeiling {
		t.Fatalf("rearm an idle ticker = %v, %v, want %v, false", every, ok, paceCeiling)
	}

	p.reset()
	if every, ok := p.rearm(paceCeiling); !ok || every != paceFloor {
		t.Fatalf("rearm after a wake = %v, %v, want %v, true", every, ok, paceFloor)
	}
	if every, ok := p.rearm(paceFloor); ok || every != paceFloor {
		t.Fatalf("rearm a floor ticker = %v, %v, want %v, false", every, ok, paceFloor)
	}
}

// TestPacerStatsCount proves the tally reports what the test did.
func TestPacerStatsCount(t *testing.T) {
	t.Parallel()
	var p pacer
	now := time.Unix(1000, 0)

	p.drew(now)
	p.decay(now)
	p.reset()
	p.reset()
	p.reset()

	s := p.stats()
	if s.Frames != 1 {
		t.Fatalf("frames = %d, want 1", s.Frames)
	}
	if s.Wakes != 3 {
		t.Fatalf("wakes = %d, want 3", s.Wakes)
	}
	if s.Interval != paceFloor {
		t.Fatalf("interval = %v, want %v", s.Interval, paceFloor)
	}
	if got, want := s.Millis(), int64(16); got != want {
		t.Fatalf("millis = %d, want %d", got, want)
	}
	if got, want := s.String(), "1 frames written, 3 wakes, 16ms interval"; got != want {
		t.Fatalf("String = %q, want %q", got, want)
	}
}
