package vt

import "testing"

// scrubScreenRows renders a terminal as text rows for a scrub assertion.
func scrubRowsOf(t *Term) []string {
	_, rows := t.Grid().Size()
	out := make([]string, rows)
	for y := 0; y < rows; y++ {
		out[y] = t.Grid().Text(y)
	}
	return out
}

// TestScrubPaintsEveryOccurrence proves the grid rewrite replaces a value
// wherever the screen shows it and leaves the rest of the row alone.
func TestScrubPaintsEveryOccurrence(t *testing.T) {
	t.Parallel()
	term := NewTerm(30, 3)
	term.Write([]byte("key: abcd1234\r\nplain abcd1234 again\r\n"))
	term.Flush()
	if n := term.Scrub([]string{"abcd1234"}); n != 16 {
		t.Fatalf("scrubbed %d cells, want 16", n)
	}
	want := []string{
		"key: ********",
		"plain ******** again",
		"",
	}
	got := scrubRowsOf(term)
	for y := range want {
		if got[y] != want[y] {
			t.Fatalf("row %d = %q, want %q", y, got[y], want[y])
		}
	}
}

// TestScrubMatchesAcrossAutowrap proves a value the terminal split over two
// rows is still caught: the terminal drew one line, so the scrub follows the
// logical line and not the row.
func TestScrubMatchesAcrossAutowrap(t *testing.T) {
	t.Parallel()
	term := NewTerm(10, 3)
	term.Write([]byte("xxabcd1234zz"))
	term.Flush()
	if n := term.Scrub([]string{"abcd1234"}); n != 8 {
		t.Fatalf("scrubbed %d cells, want 8", n)
	}
	got := scrubRowsOf(term)
	if got[0] != "xx********" {
		t.Fatalf("row 0 = %q, want the first half redacted", got[0])
	}
	if got[1] != "zz" {
		t.Fatalf("row 1 = %q, want the tail untouched", got[1])
	}
}

// TestScrubPaintsBothScreens proves the hidden screen is scrubbed too: a
// program that revealed a value on the alternate screen must not find it again
// after the program exits and the primary screen comes back.
func TestScrubPaintsBothScreens(t *testing.T) {
	t.Parallel()
	term := NewTerm(20, 3)
	term.Write([]byte("primary abcd1234"))
	term.Flush()
	term.Grid().EnterAltScreen(true)
	term.Write([]byte("\x1b[Halt abcd1234"))
	term.Flush()
	if n := term.Scrub([]string{"abcd1234"}); n != 16 {
		t.Fatalf("scrubbed %d cells, want 16", n)
	}
	if got := term.Grid().Text(0); got != "alt ********" {
		t.Fatalf("alternate screen = %q, want the value redacted", got)
	}
	term.Grid().ExitAltScreen(true)
	if got := term.Grid().Text(0); got != "primary ********" {
		t.Fatalf("primary screen = %q, want the value redacted", got)
	}
}

// TestScrubLeavesTheGridAloneWithoutAValue proves an unregistered value, and
// an empty list, change nothing: the toggle's repair runs on every switch-on,
// and a pane with nothing on screen must keep its picture.
func TestScrubLeavesTheGridAloneWithoutAValue(t *testing.T) {
	t.Parallel()
	term := NewTerm(20, 3)
	term.Write([]byte("plain text abcd1234"))
	term.Flush()
	for _, values := range [][]string{nil, {}, {"not-here"}} {
		if n := term.Scrub(values); n != 0 {
			t.Fatalf("scrub %q changed %d cells, want 0", values, n)
		}
	}
	if got := term.Grid().Text(0); got != "plain text abcd1234" {
		t.Fatalf("screen = %q, want it untouched", got)
	}
}

// TestScrubShrinksAWideCellToAStar proves a value covering a wide cluster
// leaves the row the same width: the continuation slot is given back and the
// cells after the match stay in their columns.
func TestScrubShrinksAWideCellToAStar(t *testing.T) {
	t.Parallel()
	term := NewTerm(10, 2)
	term.Write([]byte("日ab tail"))
	term.Flush()
	if n := term.Scrub([]string{"日ab"}); n != 3 {
		t.Fatalf("scrubbed %d cells, want 3", n)
	}
	got := term.Grid().Text(0)
	// The wide cell gave its continuation back: a star, the freed slot, the
	// two stars it covered, then the untouched tail.
	want := "* ** tail"
	if got != want {
		t.Fatalf("row = %q, want %q", got, want)
	}
	cols, _ := term.Grid().Size()
	if cols != 10 {
		t.Fatalf("columns = %d, want the width kept", cols)
	}
}
