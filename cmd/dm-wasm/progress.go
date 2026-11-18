package main

// The tour's saved progress: the track a person plays and how many of its steps
// are done. The wasm side is the only writer of it; the page draws what it is
// told and holds no rule.
//
// This file has no build tag, so the host test proves the storage decode, the
// track lookup, the step clamp and the keys a fast forward presses without a
// browser.

import (
	"encoding/json"

	"dm/internal/lesson"
)

// progressKey is the one key the tour writes. It is versioned, so a later layout
// can be told from this one.
const progressKey = "dm.tour.v1"

// progress is what the tour saved: the chosen track and how many of its steps
// are done, which is also the index of the step a person is on.
type progress struct {
	Track string `json:"track"`
	Step  int    `json:"step"`
}

// encodeProgress writes a record for storage.
func encodeProgress(p progress) string {
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return string(b)
}

// decodeProgress reads a saved record. Anything it cannot trust comes back as
// the zero record, which plays the first track from its first step.
func decodeProgress(raw string) progress {
	if raw == "" {
		return progress{}
	}
	var p progress
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return progress{}
	}
	return p
}

// lookupTrack finds a track by id. The bool reports whether the tour has it.
func lookupTrack(id string) (lesson.Track, bool) {
	for _, tr := range lesson.Tracks {
		if tr.ID == id {
			return tr, true
		}
	}
	return lesson.Track{}, false
}

// resumePlan is where a saved record puts a person. The stored record is hostile
// input: malformed JSON, an unknown track id or a step the track does not have
// plays the first track from its first step.
func resumePlan(raw string) (lesson.Track, int) {
	first := lesson.Tracks[0]
	p := decodeProgress(raw)
	tr, ok := lookupTrack(p.Track)
	if !ok {
		return first, 0
	}
	return tr, clampStep(tr, p.Step)
}

// clampStep holds a saved step count to a step the track really has: zero,
// meaning the first step, up to the last one. Anything else is zero, so a step
// past the end never fast forwards past the track.
func clampStep(tr lesson.Track, step int) int {
	if step < 0 || step >= len(tr.Steps) {
		return 0
	}
	return step
}

// trackList is every track as the page draws it, with the one being played
// marked. dm.tracks() and the boot's lesson.tracks event both come from here, so
// the hub and the API cannot disagree.
func trackList(current string) []any {
	out := make([]any, 0, len(lesson.Tracks))
	for _, tr := range lesson.Tracks {
		out = append(out, map[string]any{"id": tr.ID, "title": tr.Title, "current": tr.ID == current})
	}
	return out
}

// stepKeys is one step's keystrokes, as the key channel carries them.
func stepKeys(tr lesson.Track, step int) []string {
	if step < 0 || step >= len(tr.Steps) {
		return nil
	}
	out := make([]string, 0, len(tr.Steps[step].Keys))
	for _, k := range tr.Steps[step].Keys {
		out = append(out, k.Bytes)
	}
	return out
}

// fastForwardKeys is every keystroke a fast forward presses to replay the first
// n steps: each done step's keys, in order. They are the bytes show me presses,
// so the session cannot tell a resume from a person.
func fastForwardKeys(tr lesson.Track, n int) []string {
	if n > len(tr.Steps) {
		n = len(tr.Steps)
	}
	out := []string{}
	for step := 0; step < n; step++ {
		out = append(out, stepKeys(tr, step)...)
	}
	return out
}
