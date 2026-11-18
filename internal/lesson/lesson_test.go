package lesson

import (
	"testing"
	"time"
)

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

func TestTheLadderClimbsOnTimeAndNeverFalls(t *testing.T) {
	l := Start(Track{Steps: []Step{{Title: "one", Done: Rule{Action: "split-right"}, Keys: []Key{{Label: "|", Bytes: "|"}}}}})
	now := l.startedAt
	if got := l.Ladder(now.Add(3 * time.Second)); got != NoHelp {
		t.Fatalf("after 3s the ladder offered %v", got)
	}
	if got := l.Ladder(now.Add(HintAfter + time.Second)); got != Hint {
		t.Fatalf("after the hint delay it offered %v", got)
	}
	if got := l.Ladder(now.Add(ShowMeAfter + time.Second)); got != OfferShow {
		t.Fatalf("after the show me delay it offered %v", got)
	}
	// The clock resetting is not a reason to take help away.
	if got := l.Ladder(now); got != OfferShow {
		t.Fatalf("the ladder fell back to %v", got)
	}
	// A helped step is not reported as clean, and the next step starts fresh.
	l.Feed(Event{Type: "action", Name: "split-right"})
	if got := l.Results(); len(got) != 1 || got[0] != Shown {
		t.Fatalf("results = %v", got)
	}
	if got := l.Ladder(time.Now()); got != NoHelp {
		t.Fatalf("the next step started at %v", got)
	}
}

func TestHintedStepsRecordHinted(t *testing.T) {
	l := Start(Track{Steps: []Step{{Done: Rule{Action: "split-right"}}}})
	l.Ladder(l.startedAt.Add(HintAfter + time.Second))
	l.Feed(Event{Type: "action", Name: "split-right"})
	if got := l.Results(); len(got) != 1 || got[0] != Hinted {
		t.Fatalf("results = %v", got)
	}
}

func TestShowMeHasTheBytesForEveryStep(t *testing.T) {
	for _, tr := range Tracks {
		for _, step := range tr.Steps {
			if len(step.Keys) == 0 {
				t.Fatalf("%s: step %q names no keys, so show me has nothing to play", tr.ID, step.Title)
			}
			for _, k := range step.Keys {
				if k.Bytes == "" {
					t.Fatalf("%s: step %q has a cap %q with no bytes", tr.ID, step.Title, k.Label)
				}
			}
		}
	}
}
