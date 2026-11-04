package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// family is one glyph: man, joiner, woman, joiner, girl. It paints two cells and
// holds five runes, so any helper that counts runes or walks them one by one
// gets it wrong.
const family = "\U0001F468\u200D\U0001F469\u200D\U0001F467"

func textCases() []struct {
	name   string
	s      string
	cells  int
	budget int
	clip   string
	elide  string
} {
	return []struct {
		name   string
		s      string
		cells  int
		budget int
		clip   string
		elide  string
	}{
		{"ascii", "abc", 3, 2, "ab", "a…"},
		{"ascii fits", "abc", 3, 3, "abc", "abc"},
		{"two wide runes", "日日", 4, 3, "日", "日…"},
		{"joined family", family, 2, 1, "", "…"},
		{"joined family fits", family, 2, 2, family, family},
		{"base plus acute", "e\u0301", 1, 1, "e\u0301", "e\u0301"},
		{"cut inside a cluster", "a" + family, 3, 2, "a", "a…"},
		{"cut at the join", "a" + family, 3, 1, "a", "…"},
	}
}

func TestTextCells(t *testing.T) {
	for _, c := range textCases() {
		if got := cells(c.s); got != c.cells {
			t.Errorf("%s: cells(%q) = %d, want %d", c.name, c.s, got, c.cells)
		}
	}
}

func TestTextClipKeepsWholeClusters(t *testing.T) {
	for _, c := range textCases() {
		got := clip(c.s, c.budget)
		if got != c.clip {
			t.Errorf("%s: clip(%q, %d) = %q, want %q", c.name, c.s, c.budget, got, c.clip)
		}
		if n := cells(got); n > c.budget {
			t.Errorf("%s: clip(%q, %d) paints %d cells", c.name, c.s, c.budget, n)
		}
		if !strings.HasPrefix(c.s, got) {
			t.Errorf("%s: clip(%q, %d) = %q is not a prefix", c.name, c.s, c.budget, got)
		}
	}
}

func TestTextElideEndsAtTheBudget(t *testing.T) {
	for _, c := range textCases() {
		got := elide(c.s, c.budget)
		if got != c.elide {
			t.Errorf("%s: elide(%q, %d) = %q, want %q", c.name, c.s, c.budget, got, c.elide)
		}
		if n := cells(got); n > c.budget {
			t.Errorf("%s: elide(%q, %d) paints %d cells", c.name, c.s, c.budget, n)
		}
	}
}

func TestTextPadCountsCells(t *testing.T) {
	for _, c := range textCases() {
		for budget := 0; budget <= c.cells+2; budget++ {
			got := pad(c.s, budget)
			if n := cells(got); n > budget {
				t.Errorf("%s: pad(%q, %d) paints %d cells", c.name, c.s, budget, n)
			}
		}
	}
	// Two wide runes and one space: three cells, but only two runes.
	got := pad("日日", 3)
	if n := cells(got); n != 3 {
		t.Errorf("pad(%q, 3) paints %d cells, want 3", "日日", n)
	}
	if n := utf8.RuneCountInString(got); n == 3 {
		t.Errorf("pad(%q, 3) = %q holds %d runes, so it counted runes", "日日", got, n)
	}
}

func TestTextClipTailKeepsTheEnd(t *testing.T) {
	cases := []struct {
		s      string
		budget int
		want   string
	}{
		{"abc", 2, "bc"},
		{"日日", 3, "日"},
		{family, 2, family},
		{"a" + family, 2, family},
		{"a" + family, 1, ""},
	}
	for _, c := range cases {
		got := clipTail(c.s, c.budget)
		if got != c.want {
			t.Errorf("clipTail(%q, %d) = %q, want %q", c.s, c.budget, got, c.want)
		}
		if n := cells(got); n > c.budget {
			t.Errorf("clipTail(%q, %d) paints %d cells", c.s, c.budget, n)
		}
		if !strings.HasSuffix(c.s, got) {
			t.Errorf("clipTail(%q, %d) = %q is not a suffix", c.s, c.budget, got)
		}
	}
}
