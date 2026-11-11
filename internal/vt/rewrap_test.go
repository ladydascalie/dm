package vt

import (
	"strings"
	"testing"
)

// rewrapScreen renders every row of a terminal as text, top to bottom.
func rewrapScreen(t *Term) []string {
	_, rows := t.Grid().Size()
	out := make([]string, rows)
	for y := 0; y < rows; y++ {
		out[y] = t.Grid().Text(y)
	}
	return out
}

// TestRewrapMatchesFreshTerminal pins the core promise: for plain content the
// rewrapped screen is the screen a fresh terminal of the new size shows when
// it is fed the same bytes, cursor included.
func TestRewrapMatchesFreshTerminal(t *testing.T) {
	cases := []struct {
		name  string
		ring  string
		cols  int
		rows  int
		width int
	}{
		{"widen short lines", "one\r\ntwo\r\nthree", 10, 6, 20},
		{"narrow short lines", "one\r\ntwo\r\nthree", 20, 6, 10},
		{"widen one wrapped line", "the quick brown fox jumps over the lazy dog", 17, 8, 40},
		{"narrow one line", "the quick brown fox jumps over the lazy dog", 40, 8, 17},
		{"widen mixed", "short\r\na longer line that wraps a couple of times\r\nend", 16, 9, 30},
		{"narrow mixed", "short\r\na longer line that wraps a couple of times\r\nend", 30, 9, 16},
		{"empty lines", "a\r\n\r\nb\r\n\r\n", 12, 8, 9},
		{"trailing spaces", "cat   \r\ndog\t\r\n", 12, 6, 7},
		{"exact fit", "0123456789\r\nabcdefghij\r\n", 10, 6, 10},
		{"wide cells", "日本語abc", 5, 5, 6},
		{"wide cells narrow", "日本語abc", 8, 5, 5},
		{"colours", "\x1b[31mred \x1b[32mgreen\x1b[0m plain", 14, 6, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			live := NewTerm(tc.cols, tc.rows)
			live.Write([]byte(tc.ring))
			live.Flush()
			if !live.Grid().Rewrap(tc.width, tc.rows) {
				t.Fatal("Rewrap refused a plain primary screen")
			}
			want := NewTerm(tc.width, tc.rows)
			want.Write([]byte(tc.ring))
			want.Flush()

			got, wantRows := rewrapScreen(live), rewrapScreen(want)
			for y := range wantRows {
				if got[y] != wantRows[y] {
					t.Errorf("row %d = %q, want %q", y, got[y], wantRows[y])
				}
			}
			if live.Grid().Cursor() != want.Grid().Cursor() {
				t.Errorf("cursor = %+v, want %+v", live.Grid().Cursor(), want.Grid().Cursor())
			}
		})
	}
}

// TestRewrapGrowTaller pins the shape the live screen keeps when the rows
// grow: content stays where it was and the new rows stay blank at the bottom.
func TestRewrapGrowTaller(t *testing.T) {
	term := NewTerm(20, 4)
	term.Write([]byte("one\r\ntwo\r\nthree"))
	term.Flush()

	if !term.Grid().Rewrap(20, 8) {
		t.Fatal("Rewrap refused a plain primary screen")
	}
	cols, rows := term.Grid().Size()
	if cols != 20 || rows != 8 {
		t.Fatalf("size = %dx%d, want 20x8", cols, rows)
	}
	want := []string{"one", "two", "three", "", "", "", "", ""}
	for y, w := range want {
		if got := term.Grid().Text(y); got != w {
			t.Errorf("row %d = %q, want %q", y, got, w)
		}
	}
	if c := term.Grid().Cursor(); c.Y != 2 {
		t.Errorf("cursor row = %d, want 2", c.Y)
	}
}

// TestRewrapTopDrop pins the narrow shape: rows the viewport cannot hold
// leave at the top, the cursor's line stays visible, and the first kept row of
// a line that was cut still reads as a continuation.
func TestRewrapTopDrop(t *testing.T) {
	term := NewTerm(10, 6)
	// Six rows of content: aaa..ccc plus a line that wraps over two rows.
	term.Write([]byte("aaa\r\nbbb\r\nccc\r\n0123456789abc"))
	term.Flush()
	// The screen holds: aaa, bbb, ccc, 0123456789, abc.
	if got := rewrapScreen(term); !strings.Contains(strings.Join(got, "|"), "abc") {
		t.Fatalf("setup did not wrap the last line: %q", got)
	}

	if !term.Grid().Rewrap(10, 3) {
		t.Fatal("Rewrap refused a plain primary screen")
	}
	got := rewrapScreen(term)
	// Three rows fit: ccc and the wrapped line survive; the older lines do not
	// come back from anywhere.
	want := []string{"ccc", "0123456789", "abc"}
	for y, w := range want {
		if got[y] != w {
			t.Errorf("row %d = %q, want %q", y, got[y], w)
		}
	}
	if c := term.Grid().Cursor(); c.Y != 2 || c.X != 3 {
		t.Errorf("cursor = %+v, want row 2 col 3", c)
	}
}

// TestRewrapNoHistory pins the rule the reflow used to break: a screen that
// holds less than the ring produced does not pull the older output back when
// the width grows.
func TestRewrapNoHistory(t *testing.T) {
	term := NewTerm(40, 6)
	term.Write([]byte(strings.Repeat("old line\r\n", 20))) // all but the last scroll away
	term.Write([]byte("\x1b[2J\x1b[Hprompt only"))
	term.Flush()
	for y := 1; y < 6; y++ {
		if got := term.Grid().Text(y); got != "" {
			t.Fatalf("setup: row %d holds %q, want blank", y, got)
		}
	}

	if !term.Grid().Rewrap(60, 10) {
		t.Fatal("Rewrap refused a plain primary screen")
	}
	joined := strings.Join(rewrapScreen(term), "\n")
	if strings.Contains(joined, "old line") {
		t.Errorf("scrollback leaked onto the screen:\n%s", joined)
	}
	if got := term.Grid().Text(0); got != "prompt only" {
		t.Errorf("row 0 = %q, want the prompt alone", got)
	}
	for y := 1; y < 10; y++ {
		if got := term.Grid().Text(y); got != "" {
			t.Errorf("row %d = %q, want blank", y, got)
		}
	}
}

// TestRewrapKeepsStyle pins that a styled blank survives the trim and the
// wrap: a coloured tail keeps its background at the new width.
func TestRewrapKeepsStyle(t *testing.T) {
	term := NewTerm(12, 4)
	term.Write([]byte("\x1b[41mred tail \x1b[0m|"))
	term.Flush()
	styled := term.Grid().Cell(8, 0).Style

	if !term.Grid().Rewrap(20, 4) {
		t.Fatal("Rewrap refused a plain primary screen")
	}
	if got := term.Grid().Text(0); got != "red tail |" {
		t.Fatalf("row 0 = %q", got)
	}
	if got := term.Grid().Cell(8, 0).Style; got != styled {
		t.Errorf("styled blank lost its style")
	}
}

// TestRewrapRefusals pins the guards: an alternate screen and an application
// scroll region both refuse, and the grid comes out unchanged.
func TestRewrapRefusals(t *testing.T) {
	t.Run("alt screen", func(t *testing.T) {
		term := NewTerm(10, 4)
		term.Write([]byte("\x1b[?1049happ"))
		term.Flush()
		before := rewrapScreen(term)
		if term.Grid().Rewrap(20, 4) {
			t.Fatal("Rewrap ran on the alternate screen")
		}
		if got := rewrapScreen(term); strings.Join(got, "|") != strings.Join(before, "|") {
			t.Errorf("alternate screen changed: %q -> %q", before, got)
		}
	})
	t.Run("scroll region", func(t *testing.T) {
		term := NewTerm(10, 6)
		term.Write([]byte("top\r\n\x1b[2;5rbody"))
		term.Flush()
		before := rewrapScreen(term)
		if term.Grid().Rewrap(20, 6) {
			t.Fatal("Rewrap ran inside an application scroll region")
		}
		if got := rewrapScreen(term); strings.Join(got, "|") != strings.Join(before, "|") {
			t.Errorf("screen changed: %q -> %q", before, got)
		}
	})
	t.Run("degenerate sizes", func(t *testing.T) {
		term := NewTerm(10, 4)
		term.Write([]byte("hi"))
		term.Flush()
		if !term.Grid().Rewrap(0, 0) {
			t.Fatal("Rewrap refused clamped sizes")
		}
		if cols, rows := term.Grid().Size(); cols != 1 || rows != 1 {
			t.Errorf("size = %dx%d, want 1x1", cols, rows)
		}
	})
}

// TestRewrapSameShapeIsSafe pins that a rewrap at the shape the screen already
// has changes nothing, the path a drag sends twice.
func TestRewrapSameShapeIsSafe(t *testing.T) {
	term := NewTerm(15, 8)
	term.Write([]byte("the quick brown fox jumps\r\nover the lazy dog"))
	term.Flush()
	before := rewrapScreen(term)
	beforeCur := term.Grid().Cursor()

	if !term.Grid().Rewrap(15, 8) {
		t.Fatal("Rewrap refused a plain primary screen")
	}
	after := rewrapScreen(term)
	for y := range before {
		if before[y] != after[y] {
			t.Errorf("row %d = %q, want %q", y, after[y], before[y])
		}
	}
	if term.Grid().Cursor() != beforeCur {
		t.Errorf("cursor = %+v, want %+v", term.Grid().Cursor(), beforeCur)
	}
}
