package vt

import "testing"

// marginTerm returns a terminal of cols by rows with s written and flushed.
func marginTerm(t *testing.T, cols, rows int, s string) *Term {
	t.Helper()
	term := NewTerm(cols, rows)
	writeTerm(term, s)
	return term
}

// wantRegion fails when the scroll region differs from want.
func wantRegion(t *testing.T, term *Term, want ScrollRegion) {
	t.Helper()
	if got := term.grid.ScrollRegion(); got != want {
		t.Fatalf("ScrollRegion() = %+v, want %+v", got, want)
	}
}

func TestDECSTBMSetsRegionAndHomes(t *testing.T) {
	term := marginTerm(t, 20, 10, "\x1b[3;5H\x1b[2;5r")
	wantRegion(t, term, ScrollRegion{Top: 1, Bottom: 4, Left: 0, Right: 19})
	if c := term.grid.Cursor(); c.X != 0 || c.Y != 0 {
		t.Fatalf("Cursor() = %+v, want home", c)
	}
}

func TestDECSTBMDefaultsAndInvalid(t *testing.T) {
	term := marginTerm(t, 20, 10, "\x1b[2;5r\x1b[3;3r")
	wantRegion(t, term, ScrollRegion{Top: 1, Bottom: 4, Left: 0, Right: 19})

	// A zero top is the top of the screen, a zero bottom the bottom, and a
	// bottom past the screen is clamped.
	writeTerm(term, "\x1b[0;0r")
	wantRegion(t, term, ScrollRegion{Top: 0, Bottom: 9, Left: 0, Right: 19})
	writeTerm(term, "\x1b[2;99r")
	wantRegion(t, term, ScrollRegion{Top: 1, Bottom: 9, Left: 0, Right: 19})
}

func TestDECSLRMNeedsModeAndLevel(t *testing.T) {
	term := marginTerm(t, 20, 10, "\x1b[5;10s")
	wantRegion(t, term, ScrollRegion{Top: 0, Bottom: 9, Left: 0, Right: 19})

	writeTerm(term, "\x1b[?69h\x1b[5;10s")
	wantRegion(t, term, ScrollRegion{Top: 0, Bottom: 9, Left: 4, Right: 9})

	// Level 3 conformance has no DECSLRM.
	term = marginTerm(t, 20, 10, "\x1b[63;1\"p\x1b[?69h\x1b[5;10s")
	wantRegion(t, term, ScrollRegion{Top: 0, Bottom: 9, Left: 0, Right: 19})
}

func TestDECLRMMResetClearsLeftRightMargins(t *testing.T) {
	term := marginTerm(t, 20, 10, "\x1b[?69h\x1b[5;10s\x1b[?69l")
	wantRegion(t, term, ScrollRegion{Top: 0, Bottom: 9, Left: 0, Right: 19})
}

func TestMarginSequencesHomeInOriginMode(t *testing.T) {
	term := marginTerm(t, 20, 10, "\x1b[2;5r\x1b[?69h\x1b[5;10s\x1b[?6h")
	// Turning origin mode on homes to the margins' top left.
	if c := term.grid.Cursor(); c.X != 4 || c.Y != 1 {
		t.Fatalf("Cursor() = %+v, want the margins' origin 4,1", c)
	}
	// A new top margin homes there again while origin mode is on.
	writeTerm(term, "\x1b[3;5r")
	if c := term.grid.Cursor(); c.X != 4 || c.Y != 2 {
		t.Fatalf("Cursor() = %+v, want 4,2", c)
	}
	// CUP is relative to the margins and clamps to them.
	writeTerm(term, "\x1b[99;99H")
	if c := term.grid.Cursor(); c.X != 9 || c.Y != 4 {
		t.Fatalf("Cursor() = %+v, want the bottom right margin 9,4", c)
	}
}

func TestDECSTRClearsMargins(t *testing.T) {
	term := marginTerm(t, 20, 10, "\x1b[2;5r\x1b[?69h\x1b[5;10s\x1b[!p")
	wantRegion(t, term, ScrollRegion{Top: 0, Bottom: 9, Left: 0, Right: 19})
}

func TestRISClearsMargins(t *testing.T) {
	term := marginTerm(t, 20, 10, "\x1b[2;5r\x1b[?69h\x1b[5;10s\x1bc")
	wantRegion(t, term, ScrollRegion{Top: 0, Bottom: 9, Left: 0, Right: 19})
}

func TestDECDCDeletesInsideMargins(t *testing.T) {
	term := marginTerm(t, 10, 2, "abcdefg\r\nABCDEFG\x1b[?69h\x1b[2;5s\x1b[1;3H\x1b[1'~")
	if got := term.grid.Text(0); got != "abde fg" {
		t.Fatalf("Text(0) = %q, want abde fg", got)
	}
	if got := term.grid.Text(1); got != "ABDE FG" {
		t.Fatalf("Text(1) = %q, want ABDE FG", got)
	}
}

func TestDECDCIsNoOpOutsideMargins(t *testing.T) {
	term := marginTerm(t, 10, 1, "abcdefg\x1b[?69h\x1b[2;5s\x1b[1;1H\x1b[9'~")
	if got := term.grid.Text(0); got != "abcdefg" {
		t.Fatalf("Text(0) = %q, want abcdefg", got)
	}
}

func TestICHRespectsMargins(t *testing.T) {
	term := marginTerm(t, 10, 1, "abcde\x1b[?69h\x1b[2;4s\x1b[1;3H\x1b[1@")
	if got := term.grid.Text(0); got != "ab ce" {
		t.Fatalf("Text(0) = %q, want ab ce", got)
	}
}

func TestDCHIsNoOpOutsideMargins(t *testing.T) {
	term := marginTerm(t, 10, 1, "abcde\x1b[?69h\x1b[2;4s\x1b[1;1H\x1b[9P")
	if got := term.grid.Text(0); got != "abcde" {
		t.Fatalf("Text(0) = %q, want abcde", got)
	}
}

func TestDLRespectsMargins(t *testing.T) {
	term := marginTerm(t, 5, 3, "abcde\r\nfghij\r\nklmno")
	writeTerm(term, "\x1b[1;2r\x1b[?69h\x1b[2;4s\x1b[1;3H\x1b[M")
	want := []string{"aghie", "f   j", "klmno"}
	for y, w := range want {
		if got := term.grid.Text(y); got != w {
			t.Fatalf("row %d = %q, want %q", y, got, w)
		}
	}
}

func TestILRespectsMargins(t *testing.T) {
	term := marginTerm(t, 5, 3, "abcde\r\nfghij\r\nklmno")
	writeTerm(term, "\x1b[1;2r\x1b[?69h\x1b[2;4s\x1b[1;3H\x1b[L")
	want := []string{"a   e", "fbcdj", "klmno"}
	for y, w := range want {
		if got := term.grid.Text(y); got != w {
			t.Fatalf("row %d = %q, want %q", y, got, w)
		}
	}
}

func TestDLLeavesColumnsOutsideMargins(t *testing.T) {
	term := marginTerm(t, 5, 3, "abcde\r\nfghij\r\nklmno")
	writeTerm(term, "\x1b[?69h\x1b[2;4s\x1b[1;1H\x1b[M")
	want := []string{"abcde", "fghij", "klmno"}
	for y, w := range want {
		if got := term.grid.Text(y); got != w {
			t.Fatalf("row %d = %q, want %q", y, got, w)
		}
	}
}

func TestSUScrollsTheMarginRectangle(t *testing.T) {
	term := marginTerm(t, 5, 3, "abcde\r\nfghij\r\nklmno")
	writeTerm(term, "\x1b[?69h\x1b[2;4s\x1b[S")
	want := []string{"aghie", "flmnj", "k   o"}
	for y, w := range want {
		if got := term.grid.Text(y); got != w {
			t.Fatalf("row %d = %q, want %q", y, got, w)
		}
	}
}

func TestSDScrollsTheMarginRectangle(t *testing.T) {
	term := marginTerm(t, 5, 3, "abcde\r\nfghij\r\nklmno")
	writeTerm(term, "\x1b[?69h\x1b[2;4s\x1b[T")
	want := []string{"a   e", "fbcdj", "kghio"}
	for y, w := range want {
		if got := term.grid.Text(y); got != w {
			t.Fatalf("row %d = %q, want %q", y, got, w)
		}
	}
}

func TestLFOutsideMarginsDoesNotScroll(t *testing.T) {
	term := marginTerm(t, 10, 5, "\x1b[2;5r\x1b[?69h\x1b[2;5s\x1b[3;5Hx\x1b[5;6H\n\n")
	if got := term.grid.Text(2); got != "    x" {
		t.Fatalf("row 2 = %q, want the region unchanged", got)
	}
	if c := term.grid.Cursor(); c.X != 5 || c.Y != 4 {
		t.Fatalf("Cursor() = %+v, want 5,4", c)
	}
}

func TestAutowrapAtTheMargins(t *testing.T) {
	term := marginTerm(t, 10, 3, "\x1b[?69h\x1b[3;5s\x1b[1;4Habcdef")
	want := []string{"   ab", "  cde", "  f"}
	for y, w := range want {
		if got := term.grid.Text(y); got != w {
			t.Fatalf("row %d = %q, want %q", y, got, w)
		}
	}
}

func TestTabStopsAtTheRightMargin(t *testing.T) {
	term := marginTerm(t, 20, 2, "\x1b[?69h\x1b[3;6s\x1b[1;1H\t")
	if c := term.grid.Cursor(); c.X != 5 {
		t.Fatalf("Cursor() = %+v, want the right margin at 5", c)
	}
}

func TestCRGoesToTheLeftMargin(t *testing.T) {
	term := marginTerm(t, 20, 2, "\x1b[?69h\x1b[5;10s\x1b[1;6H\r")
	if c := term.grid.Cursor(); c.X != 4 {
		t.Fatalf("Cursor() = %+v, want the left margin at 4", c)
	}
	// Left of the margin CR goes to the screen edge.
	writeTerm(term, "\x1b[1;4H\r")
	if c := term.grid.Cursor(); c.X != 0 {
		t.Fatalf("Cursor() = %+v, want 0", c)
	}
}

func TestBSStopsAtTheLeftMargin(t *testing.T) {
	term := marginTerm(t, 20, 2, "\x1b[?69h\x1b[5;10s\x1b[1;5H\b")
	if c := term.grid.Cursor(); c.X != 4 {
		t.Fatalf("Cursor() = %+v, want the left margin at 4", c)
	}
}

func TestBSReverseWrapsToTheRightMargin(t *testing.T) {
	term := marginTerm(t, 20, 4, "\x1b[?7h\x1b[?45h\x1b[?69h\x1b[5;10s\x1b[3;5H\b")
	if c := term.grid.Cursor(); c.X != 9 || c.Y != 1 {
		t.Fatalf("Cursor() = %+v, want the right margin on the row above 9,1", c)
	}
}

func TestDECCOLMResetsTheMargins(t *testing.T) {
	term := marginTerm(t, 80, 5, "\x1b[?40h\x1b[?69h\x1b[5;10s\x1b[?3h")
	if r := term.grid.ScrollRegion(); r.Top != 0 || r.Bottom != 4 || r.Left != 0 || r.Right != 131 {
		t.Fatalf("ScrollRegion() = %+v, want the whole 132-column grid", r)
	}
}
