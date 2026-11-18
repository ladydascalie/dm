// Package lesson runs a guided track against what the session did. A track is
// data, a step says what proves it happened, and the engine reports the step to
// draw. The page holds no step logic, so a track can be tested without a
// browser, and the cards can be redesigned without touching a rule.
package lesson

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

// Step is one card: what it teaches, the keys it names, and what proves it.
type Step struct {
	Title string
	Note  string
	// Keys are the caps the card shows, in the words a person would say.
	Keys []string
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

// Lesson is a person's place in a track.
type Lesson struct {
	track   Track
	at      int
	results []Result
}

// Result is how a step went. Only Clean exists until the ladder lands: a track
// that has no hints yet cannot be hinted.
type Result string

// Clean is a step the person finished themselves.
const Clean Result = "clean"

// Start puts a person at the first step of a track.
func Start(t Track) *Lesson {
	return &Lesson{track: t}
}

// Track is the track this lesson runs.
func (l *Lesson) Track() Track { return l.track }

// Current is the step to draw, and false once the track is finished.
func (l *Lesson) Current() (int, Step, bool) {
	if l.at >= len(l.track.Steps) {
		return l.at, Step{}, false
	}
	return l.at, l.track.Steps[l.at], true
}

// Finished reports whether every step is done.
func (l *Lesson) Finished() bool { return l.at >= len(l.track.Steps) }

// Feed takes one event. It reports whether that event finished the current
// step, after which Current is the next one.
func (l *Lesson) Feed(ev Event) bool {
	if l.Finished() {
		return false
	}
	if !l.track.Steps[l.at].Done.matches(ev) {
		return false
	}
	l.results = append(l.results, Clean)
	l.at++
	return true
}

// Results is how each finished step went, oldest first.
func (l *Lesson) Results() []Result { return append([]Result(nil), l.results...) }
