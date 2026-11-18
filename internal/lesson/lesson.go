// Package lesson runs a guided track against what the session did. A track is
// data, a step says what proves it happened, and the engine reports the step to
// draw. The page holds no step logic, so a track can be tested without a
// browser, and the cards can be redesigned without touching a rule.
package lesson

import (
	"sync"
	"time"
)

// The ladder: how long a step waits before it offers its hint, and before it
// offers to play the keys itself.
const (
	HintAfter   = 8 * time.Second
	ShowMeAfter = 20 * time.Second
)

// Level is how much help a stalled step offers: none, its hint, or show me.
type Level int

// The rungs of the ladder.
const (
	NoHelp    Level = 0
	Hint      Level = 1
	OfferShow Level = 2
)

// Rule says what proves a step happened. A step whose rule has more than one
// field set accepts any of them, which is how a step can be passed by the key
// or by the command.
type Rule struct {
	// Action is a keybinding action name, such as "split-right".
	Action string
	// Command is a line the shell answered, such as "git status".
	Command string
	// Panes is a pane count the session must reach.
	Panes int
}

// matches reports whether one event satisfies the rule.
func (r Rule) matches(ev Event) bool {
	switch {
	case r.Action != "" && ev.Type == "action" && ev.Name == r.Action:
		return true
	case r.Command != "" && ev.Type == "command" && ev.Line == r.Command:
		return true
	case r.Panes > 0 && ev.Type == "state" && ev.Panes >= r.Panes:
		return true
	}
	return false
}

// Key is one keystroke a step names: what its cap says, and the bytes that
// press it. Show me feeds the bytes through the same path a person's keys take,
// so the session cannot tell the difference and the caps light up.
type Key struct {
	Label string
	Bytes string
}

// Step is one card: what it teaches, the keys it names, and what proves it.
type Step struct {
	Title string
	Note  string
	// Keys are the caps the card shows, in the words a person would say.
	Keys []Key
	Done Rule
	// Hint is the line shown when the step stalls.
	Hint string
}

// Track is one ordered set of steps.
type Track struct {
	ID    string
	Title string
	Steps []Step
}

// Event is one thing the session did, as the engine sees it.
type Event struct {
	// Type is "action", "command" or "state".
	Type string
	// Name is the action name.
	Name string
	// Line is the command line.
	Line string
	// Panes is the pane count on a state event.
	Panes int
}

// Lesson is a person's place in a track. It is safe for concurrent use: the
// tour feeds it from two goroutines, the client's actions on one and the
// pretend session's commands on another, while the ladder and show me read the
// step. No caller needs a lock of its own.
type Lesson struct {
	mu      sync.Mutex
	track   Track
	at      int
	results []Result
	// startedAt is when the current step started, and level is what the ladder
	// has already offered for it, so a step never climbs twice.
	startedAt time.Time
	level     Level
}

// Result is how a step went. Only Clean exists until the ladder lands: a track
// that has no hints yet cannot be hinted.
type Result string

// Clean is a step the person finished themselves. Hinted is one the ladder
// helped with, and Shown one show me played.
const (
	Clean  Result = "clean"
	Hinted Result = "hinted"
	Shown  Result = "shown"
)

// Start puts a person at the first step of a track.
func Start(t Track) *Lesson {
	return &Lesson{track: t, startedAt: time.Now()}
}

// Level is what the ladder offers for the step as it stands at now. The step's
// clock restarts on every step, so time spent reading the previous card does
// not count against this one.
func (l *Lesson) Level(now time.Time) Level {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.levelAt(now)
}

// levelAt is Level with the lock held.
func (l *Lesson) levelAt(now time.Time) Level {
	if l.at >= len(l.track.Steps) {
		return NoHelp
	}
	waited := now.Sub(l.startedAt)
	switch {
	case waited >= ShowMeAfter:
		return OfferShow
	case waited >= HintAfter:
		return Hint
	}
	return NoHelp
}

// Ladder reports what the ladder now offers, and records it, so a step that was
// helped is not reported as clean.
func (l *Lesson) Ladder(now time.Time) Level {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.at >= len(l.track.Steps) {
		return NoHelp
	}
	if next := l.levelAt(now); next > l.level {
		l.level = next
	}
	return l.level
}

// Keys is the step's keys, for show me to play.
func (l *Lesson) Keys() []Key {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.at >= len(l.track.Steps) {
		return nil
	}
	return l.track.Steps[l.at].Keys
}

// Track is the track this lesson runs.
func (l *Lesson) Track() Track {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.track
}

// Current is the step to draw, and false once the track is finished.
func (l *Lesson) Current() (int, Step, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.at >= len(l.track.Steps) {
		return l.at, Step{}, false
	}
	return l.at, l.track.Steps[l.at], true
}

// Finished reports whether every step is done.
func (l *Lesson) Finished() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.at >= len(l.track.Steps)
}

// Feed takes one event. It reports whether that event finished the current
// step, after which Current is the next one.
func (l *Lesson) Feed(ev Event) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.at >= len(l.track.Steps) {
		return false
	}
	if !l.track.Steps[l.at].Done.matches(ev) {
		return false
	}
	result := Clean
	switch l.level {
	case OfferShow:
		result = Shown
	case Hint:
		result = Hinted
	}
	l.results = append(l.results, result)
	l.at++
	l.level, l.startedAt = NoHelp, time.Now()
	return true
}

// Results is how each finished step went, oldest first.
func (l *Lesson) Results() []Result {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Result(nil), l.results...)
}
