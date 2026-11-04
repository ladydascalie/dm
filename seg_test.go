package main

import (
	"bytes"
	"strings"
	"testing"
)

// segTexts is the text of each run, in paint order.
func segTexts(runs []seg) []string {
	out := make([]string, 0, len(runs))
	for _, s := range runs {
		out = append(out, s.text)
	}
	return out
}

// TestSegCells counts painted cells, not runes: a wide rune is two cells and a
// joined emoji is the width of its cluster.
func TestSegCells(t *testing.T) {
	cases := []struct {
		name string
		runs []seg
		want int
	}{
		{"no runs", nil, 0},
		{"plain", []seg{{text: "abc"}}, 3},
		{"two runs", []seg{{text: "ab"}, {text: "cde"}}, 5},
		{"wide rune", []seg{{text: "日本"}}, 4},
		{"joined runes", []seg{{text: "👨‍👩‍👧"}}, 2},
	}
	for _, c := range cases {
		if got := segCells(c.runs); got != c.want {
			t.Fatalf("%s: segCells = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestSegClip cuts a run set to a cell budget: whole clusters, in order, and
// nothing after the run the budget can no longer start.
func TestSegClip(t *testing.T) {
	cases := []struct {
		name   string
		runs   []seg
		budget int
		want   string
	}{
		{"fits whole", []seg{{text: "abc"}}, 3, "abc"},
		{"room to spare", []seg{{text: "abc"}}, 5, "abc"},
		{"cut inside one run", []seg{{text: "abc"}}, 2, "ab"},
		{"cut inside the second run", []seg{{text: "ab"}, {text: "cd"}}, 3, "ab|c"},
		{"wide rune dropped whole", []seg{{text: "日本"}}, 3, "日"},
		{"budget short of a wide rune", []seg{{text: "日本"}}, 1, ""},
		{"later runs stop with the run that did not fit", []seg{{text: "日"}, {text: "ab"}}, 1, ""},
		{"no budget", []seg{{text: "abc"}}, 0, ""},
	}
	for _, c := range cases {
		got := strings.Join(segTexts(clipSegs(c.runs, c.budget)), "|")
		if got != c.want {
			t.Fatalf("%s: clipSegs = %q, want %q", c.name, got, c.want)
		}
	}

	// A cut run keeps the style it carried: only the text shrinks.
	styled := []seg{{text: "abcd", fg: railText, bg: railFocusBG, bold: true, pin: true}}
	if got := clipSegs(styled, 3); len(got) != 1 || got[0] != (seg{
		text: "abc", fg: railText, bg: railFocusBG, bold: true, pin: true,
	}) {
		t.Fatalf("clipSegs dropped the style: %+v", got)
	}
}

// TestSegEmptyFirstRun pins a run set whose first run is empty: clip skips the
// empty head and keeps the runs after it, join keeps the head in place and still
// lays the gap between groups, and write paints nothing for the empty run.
func TestSegEmptyFirstRun(t *testing.T) {
	runs := []seg{{}, {text: "abc", fg: railText}}
	if n := segCells(runs); n != 3 {
		t.Fatalf("segCells = %d, want 3", n)
	}

	// The empty head spends no budget and does not end the set.
	if got := clipSegs(runs, 2); strings.Join(segTexts(got), "|") != "ab" {
		t.Fatalf("clipSegs = %q, want %q", segTexts(got), "ab")
	}
	if got := clipSegs(runs, 3); len(got) != 1 || got[0].text != "abc" {
		t.Fatalf("clipSegs at full budget = %q, want the text run alone", segTexts(got))
	}

	// join leaves the empty head where the caller put it and still gaps groups.
	gap := seg{text: " ", fg: railDim, bg: railBG}
	joined := join([][]seg{{{}, {text: "a", fg: railText}}, {{text: "b", fg: railText}}}, gap)
	if want := "|a| |b"; strings.Join(segTexts(joined), "|") != want {
		t.Fatalf("join = %q, want %q", segTexts(joined), want)
	}

	// write paints nothing for the empty run and leaves the next run alone.
	var buf bytes.Buffer
	write(&buf, joined[0])
	if buf.Len() != 0 {
		t.Fatalf("empty run wrote %q", buf.String())
	}
	write(&buf, joined[1])
	if want := "\x1b[0m" + (rgb{}).bg() + railText.fg() + "a"; buf.String() != want {
		t.Fatalf("run after the empty head = %q, want %q", buf.String(), want)
	}
}

// TestSegElide ends a cut run set with the ellipsis in the dim colour, and
// leaves a set that fits alone.
func TestSegElide(t *testing.T) {
	runs := []seg{{text: "abcdef", fg: railText}}
	got := elideSegs(runs, 3)
	if want := "ab|…"; strings.Join(segTexts(got), "|") != want {
		t.Fatalf("elideSegs = %q, want %q", segTexts(got), want)
	}
	if n := len(got); n != 2 || got[1].fg != railDim {
		t.Fatalf("ellipsis run = %+v, want the dim run", got)
	}
	if got := elideSegs(runs, 1); len(got) != 1 || got[0].text != ellipsis {
		t.Fatalf("elideSegs at one cell = %q, want the ellipsis alone", segTexts(got))
	}
	if got := elideSegs(runs, 0); got != nil {
		t.Fatalf("elideSegs at no budget = %q, want nothing", segTexts(got))
	}
}

// TestSegJoin lays groups left to right with one gap run between them.
func TestSegJoin(t *testing.T) {
	gap := seg{text: " ", fg: railDim, bg: railBG}
	groups := [][]seg{{{text: "a"}}, {{text: "b"}, {text: "c"}}}
	got := join(groups, gap)
	if want := "a| |b|c"; strings.Join(segTexts(got), "|") != want {
		t.Fatalf("join = %q, want %q", segTexts(got), want)
	}
	if got[1] != gap {
		t.Fatalf("gap run = %+v, want the caller's gap", got[1])
	}
	if got := join([][]seg{{{text: "a"}}}, gap); len(got) != 1 {
		t.Fatalf("one group joined to %q, want no gap", segTexts(got))
	}
}

// TestSegWrite pins the bytes of one run: reset, ground, weight, foreground and
// text, in that order, and nothing at all for a run with no text.
func TestSegWrite(t *testing.T) {
	zero := rgb{}
	cases := []struct {
		name string
		run  seg
		want string
	}{
		{"empty run writes nothing", seg{fg: railText}, ""},
		{
			"styled run",
			seg{text: "x", fg: rgb{1, 2, 3}, bg: rgb{4, 5, 6}, bold: true},
			"\x1b[0m" + (rgb{4, 5, 6}).bg() + "\x1b[1m" + (rgb{1, 2, 3}).fg() + "x",
		},
		{
			"unstyled run keeps its ground",
			seg{text: "x"},
			"\x1b[0m" + zero.bg() + zero.fg() + "x",
		},
		{
			"bold without colours",
			seg{text: "x", bold: true},
			"\x1b[0m" + zero.bg() + "\x1b[1m" + zero.fg() + "x",
		},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		write(&buf, c.run)
		if got := buf.String(); got != c.want {
			t.Fatalf("%s: write = %q, want %q", c.name, got, c.want)
		}
	}
}
