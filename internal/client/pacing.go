package client

import (
	"strconv"
	"time"
)

// The pacer's bounds. The floor is the fastest the client draws, the ceiling
// the slowest it wakes when nothing happens.
const (
	paceFloor   = 16 * time.Millisecond
	paceCeiling = 250 * time.Millisecond
)

// pacer decides when the client may draw and how fast it should wake. It is a
// value, not a goroutine: the loop owns the time and asks the pacer about it.
//
// The zero value is ready: the interval starts at the floor and the first
// shouldDraw call sees no previous frame.
type pacer struct {
	gap    time.Duration // ticker interval to use next
	last   time.Time     // when the pacer last handled a frame: a drawn one or an empty one
	armed  time.Time     // when the ticker was last created or reset
	frames uint64        // frames that wrote bytes since the process started
	wakes  uint64        // wakes reset since the process started
}

// interval is the ticker interval to use next. An idle run doubles it up to
// the ceiling; a frame that wrote bytes or a wake resets it to the floor.
func (p *pacer) interval() time.Duration {
	if p.gap < paceFloor {
		return paceFloor
	}
	return p.gap
}

// shouldDraw reports whether a frame is due. A dirty frame inside one floor
// interval of the last frame waits, so a burst of changes coalesces into a
// single frame.
func (p *pacer) shouldDraw(now time.Time, dirty bool) bool {
	return dirty && now.Sub(p.last) >= paceFloor
}

// arm records when the loop created or reset the ticker. Staleness hangs off
// the ticker's own schedule, not the wakes: a message stream resets the pacer
// before every tick, and must still be drawn.
func (p *pacer) arm(now time.Time) {
	p.armed = now
}

// stale reports whether now is too soon after the ticker was armed to be a
// real tick. Go's Ticker.Reset does not drain a tick already delivered, so the
// tick after a reset may be the buffered one from before it. It carries no new
// time and must not advance the decay.
func (p *pacer) stale(now time.Time) bool {
	return !p.armed.IsZero() && now.Sub(p.armed) < paceFloor
}

// drew records a frame that wrote bytes at now. It counts the frame and
// returns the interval to the floor.
func (p *pacer) drew(now time.Time) {
	p.last = now
	p.frames++
	p.gap = paceFloor
}

// decay records an empty frame at now: a tick that drew nothing. It advances
// the interval one doubling step toward the ceiling without counting a frame.
func (p *pacer) decay(now time.Time) {
	p.last = now
	if p.gap = p.interval() * 2; p.gap > paceCeiling {
		p.gap = paceCeiling
	}
}

// reset returns the interval to the floor after a wake: input, a message, a
// resize or an idle flush.
func (p *pacer) reset() {
	p.wakes++
	p.gap = paceFloor
}

// PaceStats is the pacer's tally since the process started. It is the shape
// DM_STATS prints at client exit.
type PaceStats struct {
	Frames   uint64
	Wakes    uint64
	Interval time.Duration
}

// Millis is the current interval in whole milliseconds.
func (s PaceStats) Millis() int64 {
	return s.Interval.Milliseconds()
}

// String is the one-line diagnostic the client prints for DM_STATS. Frames
// counts frames that wrote bytes, not ticks.
func (s PaceStats) String() string {
	return strconv.FormatUint(s.Frames, 10) + " frames written, " +
		strconv.FormatUint(s.Wakes, 10) + " wakes, " +
		strconv.FormatInt(s.Millis(), 10) + "ms interval"
}

// stats returns the pacer's tally.
func (p *pacer) stats() PaceStats {
	return PaceStats{Frames: p.frames, Wakes: p.wakes, Interval: p.interval()}
}
