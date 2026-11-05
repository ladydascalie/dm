package segment

import (
	"bytes"
	"dm/internal/cells"
	"dm/internal/theme"
	"strings"
	"testing"
)

// segTexts is the text of each run, in paint order.
func segTexts(runs []Seg) []string {
	out := make([]string, 0, len(runs))
	for _, s := range runs {
		out = append(out, s.Text)
	}
	return out
}

// TestSegCells counts painted cells, not runes: a wide rune is two cells and a
// joined emoji is the width of its cluster.
func TestSegCells(t *testing.T) {
	cases := []struct {
		name string
		runs []Seg
		want int
	}{
		{"no runs", nil, 0},
		{"plain", []Seg{{Text: "abc"}}, 3},
		{"two runs", []Seg{{Text: "ab"}, {Text: "cde"}}, 5},
		{"wide rune", []Seg{{Text: "日本"}}, 4},
		{"joined runes", []Seg{{Text: "👨‍👩‍👧"}}, 2},
	}
	for _, c := range cases {
		if got := SegCells(c.runs); got != c.want {
			t.Fatalf("%s: SegCells = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestSegClip cuts a run set to a cell budget: whole clusters, in order, and
// nothing after the run the budget can no longer start.
func TestSegClip(t *testing.T) {
	cases := []struct {
		name   string
		runs   []Seg
		budget int
		want   string
	}{
		{"fits whole", []Seg{{Text: "abc"}}, 3, "abc"},
		{"room to spare", []Seg{{Text: "abc"}}, 5, "abc"},
		{"cut inside one run", []Seg{{Text: "abc"}}, 2, "ab"},
		{"cut inside the second run", []Seg{{Text: "ab"}, {Text: "cd"}}, 3, "ab|c"},
		{"wide rune dropped whole", []Seg{{Text: "日本"}}, 3, "日"},
		{"budget short of a wide rune", []Seg{{Text: "日本"}}, 1, ""},
		{"later runs stop with the run that did not fit", []Seg{{Text: "日"}, {Text: "ab"}}, 1, ""},
		{"no budget", []Seg{{Text: "abc"}}, 0, ""},
	}
	for _, c := range cases {
		got := strings.Join(segTexts(ClipSegs(c.runs, c.budget)), "|")
		if got != c.want {
			t.Fatalf("%s: ClipSegs = %q, want %q", c.name, got, c.want)
		}
	}

	// A cut run keeps the style it carried: only the text shrinks.
	styled := []Seg{{Text: "abcd", FG: theme.DefaultTheme().Palette.RailText, BG: theme.DefaultTheme().Palette.RailFocusBG, Bold: true, Pin: true}}
	if got := ClipSegs(styled, 3); len(got) != 1 || got[0] != (Seg{
		Text: "abc", FG: theme.DefaultTheme().Palette.RailText, BG: theme.DefaultTheme().Palette.RailFocusBG, Bold: true, Pin: true,
	}) {
		t.Fatalf("ClipSegs dropped the style: %+v", got)
	}
}

// TestSegEmptyFirstRun pins a run set whose first run is empty: clip skips the
// empty head and keeps the runs after it, join keeps the head in place and still
// lays the gap between groups, and write paints nothing for the empty run.
func TestSegEmptyFirstRun(t *testing.T) {
	runs := []Seg{{}, {Text: "abc", FG: theme.DefaultTheme().Palette.RailText}}
	if n := SegCells(runs); n != 3 {
		t.Fatalf("SegCells = %d, want 3", n)
	}

	// The empty head spends no budget and does not end the set.
	if got := ClipSegs(runs, 2); strings.Join(segTexts(got), "|") != "ab" {
		t.Fatalf("ClipSegs = %q, want %q", segTexts(got), "ab")
	}
	if got := ClipSegs(runs, 3); len(got) != 1 || got[0].Text != "abc" {
		t.Fatalf("ClipSegs at full budget = %q, want the text run alone", segTexts(got))
	}

	// Join leaves the empty head where the caller put it and still gaps groups.
	gap := Seg{Text: " ", FG: theme.DefaultTheme().Palette.RailDim, BG: theme.DefaultTheme().Palette.RailBG}
	joined := Join([][]Seg{{{}, {Text: "a", FG: theme.DefaultTheme().Palette.RailText}}, {{Text: "b", FG: theme.DefaultTheme().Palette.RailText}}}, gap)
	if want := "|a| |b"; strings.Join(segTexts(joined), "|") != want {
		t.Fatalf("Join = %q, want %q", segTexts(joined), want)
	}

	// Write paints nothing for the empty run and leaves the next run alone.
	var buf bytes.Buffer
	Write(&buf, joined[0])
	if buf.Len() != 0 {
		t.Fatalf("empty run wrote %q", buf.String())
	}
	Write(&buf, joined[1])
	if want := "\x1b[0m" + (theme.RGB{}).BG() + theme.DefaultTheme().Palette.RailText.FG() + "a"; buf.String() != want {
		t.Fatalf("run after the empty head = %q, want %q", buf.String(), want)
	}
}

// TestSegElide ends a cut run set with the ellipsis in the dim colour, and
// leaves a set that fits alone.
func TestSegElide(t *testing.T) {
	runs := []Seg{{Text: "abcdef", FG: theme.DefaultTheme().Palette.RailText}}
	got := ElideSegs(theme.DefaultTheme(), runs, 3)
	if want := "ab|…"; strings.Join(segTexts(got), "|") != want {
		t.Fatalf("ElideSegs = %q, want %q", segTexts(got), want)
	}
	if n := len(got); n != 2 || got[1].FG != theme.DefaultTheme().Palette.RailDim {
		t.Fatalf("ellipsis run = %+v, want the dim run", got)
	}
	if got := ElideSegs(theme.DefaultTheme(), runs, 1); len(got) != 1 || got[0].Text != cells.Ellipsis {
		t.Fatalf("ElideSegs at one cell = %q, want the ellipsis alone", segTexts(got))
	}
	if got := ElideSegs(theme.DefaultTheme(), runs, 0); got != nil {
		t.Fatalf("ElideSegs at no budget = %q, want nothing", segTexts(got))
	}
}

// TestSegJoin lays groups left to right with one gap run between them.
func TestSegJoin(t *testing.T) {
	gap := Seg{Text: " ", FG: theme.DefaultTheme().Palette.RailDim, BG: theme.DefaultTheme().Palette.RailBG}
	groups := [][]Seg{{{Text: "a"}}, {{Text: "b"}, {Text: "c"}}}
	got := Join(groups, gap)
	if want := "a| |b|c"; strings.Join(segTexts(got), "|") != want {
		t.Fatalf("Join = %q, want %q", segTexts(got), want)
	}
	if got[1] != gap {
		t.Fatalf("gap run = %+v, want the caller's gap", got[1])
	}
	if got := Join([][]Seg{{{Text: "a"}}}, gap); len(got) != 1 {
		t.Fatalf("one group joined to %q, want no gap", segTexts(got))
	}
}

// TestSegWrite pins the bytes of one run: reset, ground, weight, foreground and
// text, in that order, and nothing at all for a run with no text.
func TestSegWrite(t *testing.T) {
	zero := theme.RGB{}
	cases := []struct {
		name string
		run  Seg
		want string
	}{
		{"empty run writes nothing", Seg{FG: theme.DefaultTheme().Palette.RailText}, ""},
		{
			"styled run",
			Seg{Text: "x", FG: theme.RGB{R: 1, G: 2, B: 3}, BG: theme.RGB{R: 4, G: 5, B: 6}, Bold: true},
			"\x1b[0m" + (theme.RGB{R: 4, G: 5, B: 6}).BG() + "\x1b[1m" + (theme.RGB{R: 1, G: 2, B: 3}).FG() + "x",
		},
		{
			"unstyled run keeps its ground",
			Seg{Text: "x"},
			"\x1b[0m" + zero.BG() + zero.FG() + "x",
		},
		{
			"bold without colours",
			Seg{Text: "x", Bold: true},
			"\x1b[0m" + zero.BG() + "\x1b[1m" + zero.FG() + "x",
		},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		Write(&buf, c.run)
		if got := buf.String(); got != c.want {
			t.Fatalf("%s: Write = %q, want %q", c.name, got, c.want)
		}
	}
}
