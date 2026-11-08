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
	last   time.Time     // when the pacer last drew a frame
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

// rearm reports the interval the ticker must be armed with, given it is
// currently armed with armed, and whether that differs. A wake floors the
// interval, so a ticker the idle decay armed out at the ceiling is pulled back
// and the next frame lands within one floor. A ticker already at the floor is
// left alone: re-arming on every wake would push the frame out forever while a
// pane streams.
func (p *pacer) rearm(armed time.Duration) (time.Duration, bool) {
	want := p.interval()
	return want, want != armed
}

// drew records a frame that wrote bytes at now. It counts the frame and
// returns the interval to the floor.
func (p *pacer) drew(now time.Time) {
	p.last = now
	p.frames++
	p.gap = paceFloor
}

// decay records an empty frame at now: a tick that drew nothing. It advances
// the interval one doubling step toward the ceiling without counting a frame,
// and leaves the frame floor's reference alone: an empty tick is not a draw, so
// a key arriving right after one still draws at once.
func (p *pacer) decay(now time.Time) {
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
