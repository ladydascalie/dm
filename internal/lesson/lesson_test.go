package lesson

import "testing"

func TestAStepAdvancesOnTheEventItsRuleNames(t *testing.T) {
	track := Track{Steps: []Step{
		{Title: "run", Done: Rule{Command: "git status"}},
		{Title: "split", Done: Rule{Action: "split-right"}},
		{Title: "see it", Done: Rule{Panes: 2}},
	}}
	l := Start(track)

	for _, ev := range [][]Event{
		{{Type: "command", Line: "ls"}}, // not the rule
		{{Type: "command", Line: "git status"}},
		{{Type: "action", Name: "focus-left"}}, // not the rule
		{{Type: "action", Name: "split-right"}},
		{{Type: "state", Panes: 1}}, // not enough
		{{Type: "state", Panes: 2}},
	} {
		l.Feed(ev[0])
	}
	if got := l.Results(); len(got) != 3 {
		t.Fatalf("results = %v, want three clean steps", got)
	}
	if !l.Finished() {
		t.Fatal("the track did not finish")
	}
	if _, _, ok := l.Current(); ok {
		t.Fatal("a finished track still offers a step")
	}
}

func TestARuleAcceptsEitherField(t *testing.T) {
	// A step passed by the key or by the command, whichever comes first.
	step := Step{Done: Rule{Action: "select-layout", Command: "echo tiled"}}
	for _, ev := range []Event{
		{Type: "action", Name: "select-layout"},
		{Type: "command", Line: "echo tiled"},
	} {
		l := Start(Track{Steps: []Step{step}})
		if !l.Feed(ev) {
			t.Fatalf("%v did not finish the step", ev)
		}
	}
}

func TestNoEventAdvancesPastTheEnd(t *testing.T) {
	l := Start(Track{Steps: []Step{{Done: Rule{Action: "split-right"}}}})
	l.Feed(Event{Type: "action", Name: "split-right"})
	if l.Feed(Event{Type: "action", Name: "split-right"}) {
		t.Fatal("a finished track advanced again")
	}
}

// TestTheBasicsTrackPlaysThrough is the track's proof, without a browser: the
// events a person would produce, in order, finish every step.
func TestTheBasicsTrackPlaysThrough(t *testing.T) {
	var track Track
	for _, tr := range Tracks {
		if tr.ID == "basics" {
			track = tr
		}
	}
	if track.ID == "" {
		t.Fatal("the basics track is gone")
	}

	l := Start(track)
	events := []Event{
		{Type: "command", Line: "git status"},
		{Type: "state", Panes: 2},
		{Type: "action", Name: "split-right"},
		{Type: "action", Name: "focus-left"},
		{Type: "action", Name: "zoom-pane"},
		{Type: "action", Name: "zoom-pane"},
	}
	for _, ev := range events {
		l.Feed(ev)
	}
	if !l.Finished() {
		at, step, _ := l.Current()
		t.Fatalf("stuck at step %d %q", at+1, step.Title)
	}
	if len(l.Results()) != len(track.Steps) {
		t.Fatalf("results = %v, want one per step", l.Results())
	}
}
