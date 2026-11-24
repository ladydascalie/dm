package hintbar

import (
	"bytes"
	"strings"
	"testing"

	"dm/internal/segment"
	"dm/internal/theme"
)

// th is the default theme, the one the mockup pins the pipe tone against.
func th() theme.Theme { return theme.DefaultTheme() }

// text is the plain text a run set paints.
func text(runs []segment.Seg) string {
	var b strings.Builder
	for _, s := range runs {
		b.WriteString(s.Text)
	}
	return b.String()
}

// TestCapAndLabel: the keys print as one cap on the rail hover ground in bold
// rail text, with the label one space after it on the rail background.
func TestCapAndLabel(t *testing.T) {
	th := th()
	runs := Bar{Hints: []Hint{{Keys: []string{"enter"}, Label: "pick"}}}.Segments(20, th)
	if got, want := text(runs), " enter  pick"; got != want {
		t.Fatalf("bar = %q, want %q", got, want)
	}
	cap := runs[0]
	if cap.Text != " enter " {
		t.Fatalf("cap = %q, want the key padded one space a side", cap.Text)
	}
	if cap.BG != th.Palette.RailHoverBG || cap.FG != th.Palette.RailText || !cap.Bold {
		t.Fatalf("cap style = %+v, want bold rail text on the hover ground", cap)
	}
	label := runs[1]
	if label.Text != " pick" || label.BG != th.Palette.RailBG || label.FG != th.Palette.RailText {
		t.Fatalf("label = %+v, want the rail text on the rail background", label)
	}
}

// TestPipeColour pins the separator tone: 45% from the rail background toward
// the rail dim, which is #4b4d5a on the default theme. Rail dim itself is not
// the pipe colour.
func TestPipeColour(t *testing.T) {
	th := th()
	if got, want := PipeColour(th).Hex(), "#4b4d5a"; got != want {
		t.Fatalf("pipe = %s, want %s", got, want)
	}
	if PipeColour(th) == th.Palette.RailDim {
		t.Fatal("the pipe is rail dim itself")
	}
	sep := pipe(th)
	if sep.FG != PipeColour(th) || sep.BG != th.Palette.RailBG {
		t.Fatalf("separator style = %+v, want the faint tone on the rail background", sep)
	}
	if sep.Text != " "+th.Glyphs.BorderVertical+" " {
		t.Fatalf("separator = %q, want one space either side of the rule", sep.Text)
	}
}

// TestBalancedSpacing: one space either side of every pipe, and the caps keep
// their own padding, so no pipe touches a cap.
func TestBalancedSpacing(t *testing.T) {
	th := th()
	runs := Bar{Hints: []Hint{
		{Keys: []string{"esc"}, Label: "close"},
		{Keys: []string{"j/k"}, Label: "move"},
	}}.Segments(40, th)
	if got, want := text(runs), " esc  close │  j/k  move"; got != want {
		t.Fatalf("bar = %q, want %q", got, want)
	}
	if !strings.Contains(text(runs), "close │  j/k") {
		t.Fatalf("bar = %q, want one space either side of the pipe", text(runs))
	}
}

// TestMultiKeyTokens: a hint's keys join with "/" into one cap, so a pair or a
// control chord stays whole.
func TestMultiKeyTokens(t *testing.T) {
	th := th()
	for _, keys := range [][]string{
		{"j", "k"},
		{"ctrl+u", "ctrl+d"},
	} {
		runs := Bar{Hints: []Hint{{Keys: keys, Label: "move"}}}.Segments(40, th)
		if got, want := runs[0].Text, " "+strings.Join(keys, "/")+" "; got != want {
			t.Fatalf("cap for %v = %q, want %q", keys, got, want)
		}
	}
}

// TestEmptyKeysDropWhole: a hint with no key paints nothing, and a key list of
// empty names is a hint with no key.
func TestEmptyKeysDropWhole(t *testing.T) {
	th := th()
	runs := Bar{Hints: []Hint{
		{Keys: []string{""}, Label: "gone"},
		{Keys: nil, Label: "also gone"},
	}}.Segments(40, th)
	if len(runs) != 0 {
		t.Fatalf("bar = %q, want nothing", text(runs))
	}
	// A mixed list keeps the bound hint and drops the unbound one whole: no
	// stray pipe is left behind.
	runs = Bar{Hints: []Hint{
		{Keys: []string{"esc"}, Label: "close"},
		{Keys: []string{""}, Label: "gone"},
	}}.Segments(40, th)
	if got, want := text(runs), " esc  close"; got != want {
		t.Fatalf("bar = %q, want %q", got, want)
	}
}

// TestEmptyLabelKeepsCapOnly: a hint with no label ends on its cap, with no
// trailing space and no gap.
func TestEmptyLabelKeepsCapOnly(t *testing.T) {
	th := th()
	runs := Bar{Hints: []Hint{
		{Keys: []string{"enter"}},
		{Keys: []string{"esc"}},
	}}.Segments(40, th)
	if got, want := text(runs), " enter  │  esc "; got != want {
		t.Fatalf("bar = %q, want %q", got, want)
	}
}

// TestFitDropsFromTheTail: a bar wider than its cells keeps the head of the
// hint list and drops the rest whole.
func TestFitDropsFromTheTail(t *testing.T) {
	th := th()
	hints := []Hint{
		{Keys: []string{"j/k"}, Label: "move"},
		{Keys: []string{"b"}, Label: "body"},
		{Keys: []string{"p"}, Label: "pane"},
	}
	whole := Bar{Hints: hints}
	all := whole.Cells(100, th)
	if all != segment.SegCells(whole.Segments(100, th)) {
		t.Fatalf("Cells = %d, want the painted width", all)
	}
	two := Bar{Hints: hints[:2]}.Cells(100, th)
	if got := whole.Cells(two, th); got != two {
		t.Fatalf("bar at %d cells = %d, want the two-hint head", two, got)
	}
	one := Bar{Hints: hints[:1]}.Cells(100, th)
	if got := whole.Cells(two-1, th); got != one {
		t.Fatalf("bar one cell short = %d, want the first hint alone (%d)", got, one)
	}
	// A dropped hint takes its pipe with it: no trailing separator survives.
	if got := text(whole.Segments(two, th)); strings.HasSuffix(got, "│ ") {
		t.Fatalf("bar = %q, want no trailing pipe", got)
	}
}

// TestKeepSurvives: the drops skip a Keep hint, so the keys a caller pins stay
// on a narrow row.
func TestKeepSurvives(t *testing.T) {
	th := th()
	hints := []Hint{
		{Keys: []string{"j/k"}, Label: "move"},
		{Keys: []string{"space"}, Label: "pick"},
		{Keys: []string{"t"}, Label: "type"},
		{Keys: []string{"enter"}, Label: "send", Keep: true},
		{Keys: []string{"esc"}, Label: "decline", Keep: true},
	}
	bar := Bar{Hints: hints}
	// The row the keeps need on their own.
	keepOnly := Bar{Hints: hints[3:]}.Cells(100, th)
	if got, want := bar.Cells(keepOnly, th), keepOnly; got != want {
		t.Fatalf("bar at the keeps' width = %d, want %d", got, want)
	}
	if got := text(bar.Segments(keepOnly, th)); !strings.Contains(got, "enter") || strings.Contains(got, "type") {
		t.Fatalf("bar = %q, want the keeps alone", got)
	}
	// One cell wider than the keeps fit together takes the nearest droppable.
	if got := text(bar.Segments(keepOnly+5, th)); !strings.Contains(got, "t") {
		t.Fatalf("bar = %q, want the nearest droppable kept", got)
	}
}

// TestRightStatus: a right status pins to the row's right edge, the hints fit
// around it and the gap pads on the rail ground.
func TestRightStatus(t *testing.T) {
	th := th()
	status := []segment.Seg{{Text: "recent 40%", FG: th.Palette.RailDim, BG: th.Palette.RailBG}}
	bar := Bar{Hints: []Hint{{Keys: []string{"enter"}, Label: "pick"}}, Right: status}
	runs := bar.Segments(40, th)
	if len(runs) == 0 {
		t.Fatal("bar painted nothing")
	}
	if got, want := runs[len(runs)-1].Text, "recent 40%"; got != want {
		t.Fatalf("last run = %q, want the status", got)
	}
	if got, want := bar.Cells(40, th), 40; got != want {
		t.Fatalf("bar cells = %d, want the status at the right edge", got)
	}
	// A pad run sits between the hints and the status, on the rail ground.
	pad := runs[len(runs)-2]
	if pad.BG != th.Palette.RailBG || strings.Trim(pad.Text, " ") != "" {
		t.Fatalf("pad = %+v, want rail-ground spaces", pad)
	}
	// The status is never dropped, however narrow the row.
	runs = bar.Segments(12, th)
	if got := text(runs); !strings.HasSuffix(got, "recent 40%") {
		t.Fatalf("bar at 12 cells = %q, want the status at the right edge", got)
	}
	if got := bar.Cells(12, th); got > 12 {
		t.Fatalf("bar at 12 cells painted %d", got)
	}
}

// TestNarrowWidths: a bar never paints past its width, and a row too narrow
// for the Keep hints drops them too rather than overflow.
func TestNarrowWidths(t *testing.T) {
	th := th()
	bar := Bar{Hints: []Hint{
		{Keys: []string{"j/k"}, Label: "move"},
		{Keys: []string{"enter"}, Label: "send", Keep: true},
	}}
	for width := 0; width <= 20; width++ {
		if got := bar.Cells(width, th); got > width || got < 0 {
			t.Fatalf("bar at %d cells painted %d", width, got)
		}
	}
	if got := bar.Cells(0, th); got != 0 {
		t.Fatalf("bar at 0 cells painted %d", got)
	}
	// No trailing fill: the run set ends on the last hint's label.
	if got := text(bar.Segments(40, th)); strings.HasSuffix(got, "  ") {
		t.Fatalf("bar = %q, want no trailing fill", got)
	}
}

// TestLine paints a bar inline on a row a caller owns: it starts on the given
// cell, writes no fill, and reports the cells it took.
func TestLine(t *testing.T) {
	th := th()
	var buf bytes.Buffer
	bar := Bar{Hints: []Hint{{Keys: []string{"?"}, Label: "help"}}}
	n := Line(&buf, th, 3, 2, 20, bar)
	if n != bar.Cells(20, th) {
		t.Fatalf("Line wrote %d cells, want %d", n, bar.Cells(20, th))
	}
	if want := "\x1b[3;4H"; !strings.HasPrefix(buf.String(), want) {
		t.Fatalf("line starts %q, want %q", buf.String(), want)
	}
	if got := buf.String(); !strings.Contains(got, " ? ") || !strings.HasSuffix(got, " help") {
		t.Fatalf("line = %q, want the cap and the label", got)
	}
	if n := Line(&bytes.Buffer{}, th, 0, 0, 0, bar); n != 0 {
		t.Fatalf("Line at 0 cells = %d, want 0", n)
	}
}
