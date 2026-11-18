package main

import (
	"testing"

	"dm/internal/lesson"
)

// TestProgressRoundTripsThroughStorage proves a record the tour writes is the
// record it reads back.
func TestProgressRoundTripsThroughStorage(t *testing.T) {
	for _, want := range []progress{{Track: "windows", Step: 3}, {Track: "basics"}, {}} {
		if got := decodeProgress(encodeProgress(want)); got != want {
			t.Fatalf("a %+v record read back as %+v", want, got)
		}
	}
}

// TestResumePlanDecodesTheRecord proves a saved record decides the track and the
// step a person is on, including a track that is not the first one.
func TestResumePlanDecodesTheRecord(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
		step int
	}{
		{"a valid record", `{"track":"windows","step":4}`, "windows", 4},
		{"the first step", `{"track":"basics","step":0}`, "basics", 0},
		{"the last step", `{"track":"basics","step":4}`, "basics", 4},
		{"a step in the middle", `{"track":"basics","step":2}`, "basics", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr, at := resumePlan(c.raw)
			if tr.ID != c.want || at != c.step {
				t.Fatalf("resumePlan(%q) = %q at step %d, want %q at step %d",
					c.raw, tr.ID, at, c.want, c.step)
			}
		})
	}
}

// TestResumePlanRefusesAHostileRecord proves the stored record is treated as
// hostile: every record the tour cannot trust plays the first track from its
// first step.
func TestResumePlanRefusesAHostileRecord(t *testing.T) {
	first := lesson.Tracks[0]
	cases := []struct {
		name string
		raw  string
	}{
		{"no record", ""},
		{"malformed JSON", `{"track":`},
		{"not an object", `"windows"`},
		{"an unknown track", `{"track":"nope","step":2}`},
		{"no track", `{"step":2}`},
		{"a step past the end", `{"track":"basics","step":5}`},
		{"a negative step", `{"track":"basics","step":-3}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr, at := resumePlan(c.raw)
			if tr.ID != first.ID || at != 0 {
				t.Fatalf("resumePlan(%q) = %q at step %d, want %q at step 0",
					c.raw, tr.ID, at, first.ID)
			}
		})
	}
}

// TestClampStep proves the clamp itself: a real step survives, and the count of
// every step, one past it and a negative number become the first step.
func TestClampStep(t *testing.T) {
	tr, ok := lookupTrack("basics")
	if !ok {
		t.Fatal("the basics track is gone")
	}
	last := len(tr.Steps) - 1
	for _, step := range []int{0, 1, last} {
		if got := clampStep(tr, step); got != step {
			t.Fatalf("clampStep(%d) = %d, want it unchanged", step, got)
		}
	}
	for _, step := range []int{-1, last + 1, last + 7, 99} {
		if got := clampStep(tr, step); got != 0 {
			t.Fatalf("clampStep(%d) = %d, want the first step", step, got)
		}
	}
}

// TestLookupTrack proves the lookup finds every track the tour offers and
// refuses a name it does not have.
func TestLookupTrack(t *testing.T) {
	if len(lesson.Tracks) < 2 {
		t.Fatalf("the tour offers %d tracks, want at least two", len(lesson.Tracks))
	}
	for _, tr := range lesson.Tracks {
		got, ok := lookupTrack(tr.ID)
		if !ok || got.ID != tr.ID || got.Title != tr.Title || len(got.Steps) != len(tr.Steps) {
			t.Fatalf("lookupTrack(%q) = %+v, %v", tr.ID, got, ok)
		}
	}
	if _, ok := lookupTrack("nope"); ok {
		t.Fatal("lookupTrack found a track the tour does not have")
	}
	if _, ok := lookupTrack(""); ok {
		t.Fatal("lookupTrack found a track for an empty id")
	}
}

// TestTrackListMarksTheCurrentOne proves the hub's list names every track, in
// order, and marks only the one being played.
func TestTrackListMarksTheCurrentOne(t *testing.T) {
	for _, current := range lesson.Tracks {
		list := trackList(current.ID)
		if len(list) != len(lesson.Tracks) {
			t.Fatalf("trackList(%q) has %d entries, want %d", current.ID, len(list), len(lesson.Tracks))
		}
		for i, entry := range list {
			got, _ := entry.(map[string]any)
			if got["id"] != lesson.Tracks[i].ID || got["title"] != lesson.Tracks[i].Title {
				t.Fatalf("trackList(%q)[%d] = %v, want %q", current.ID, i, got, lesson.Tracks[i].ID)
			}
			if got["current"] != (lesson.Tracks[i].ID == current.ID) {
				t.Fatalf("trackList(%q)[%d] current = %v", current.ID, i, got["current"])
			}
		}
	}
}

// TestFastForwardKeysAreTheDoneStepsKeys proves the exact keystrokes a fast
// forward presses: the keys of the done steps, in order, as the key channel
// carries them. They are the bytes show me presses, so a resume and a person
// reach the session the same way.
func TestFastForwardKeysAreTheDoneStepsKeys(t *testing.T) {
	tr, ok := lookupTrack("basics")
	if !ok {
		t.Fatal("the basics track is gone")
	}
	// The first two steps: git status and enter, then ctrl+b and the split key.
	only(t, "two steps of keys", fastForwardKeys(tr, 2), "git status", "\r", "\x02", "|")
	// The card's labels are not the bytes: the split key is "|", its cap says
	// "|", and the prefix is ctrl+b's raw 0x02.
	only(t, "the prefix of one step", fastForwardKeys(tr, 1), "git status", "\r")
	only(t, "no steps", fastForwardKeys(tr, 0))
	only(t, "a negative count", fastForwardKeys(tr, -1))
	// A count past the track is the whole track, and every key of it is there.
	whole := fastForwardKeys(tr, len(tr.Steps)+3)
	want := 0
	for _, step := range tr.Steps {
		want += len(step.Keys)
	}
	if len(whole) != want {
		t.Fatalf("a count past the track gave %d keys, want %d", len(whole), want)
	}
	// Each step's own keys are that step's keys, in order.
	for step, st := range tr.Steps {
		keys := stepKeys(tr, step)
		if len(keys) != len(st.Keys) {
			t.Fatalf("step %d's keys = %q, want %d of them", step, keys, len(st.Keys))
		}
		for i, k := range st.Keys {
			if keys[i] != k.Bytes {
				t.Fatalf("step %d key %d = %q, want %q", step, i, keys[i], k.Bytes)
			}
		}
	}
	only(t, "the first step of the wider track", fastForwardKeys(widerTrack(t), 2),
		"\x02", "c", "\x02", "w")
	if got := stepKeys(tr, len(tr.Steps)); len(got) != 0 {
		t.Fatalf("stepKeys past the end gave %v, want nothing", got)
	}
}

// widerTrack is the windows track, which starts with a key that opens a window
// rather than a command.
func widerTrack(t *testing.T) lesson.Track {
	t.Helper()
	tr, ok := lookupTrack("windows")
	if !ok {
		t.Fatal("the windows track is gone")
	}
	return tr
}

// only is the small assertion helper the wider suite uses: the slice must hold
// exactly want, in order.
func only(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %q, want %q", what, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %q, want %q", what, got, want)
		}
	}
}
