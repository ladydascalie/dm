package vt

import "testing"

func TestInsertModeShiftsRight(t *testing.T) {
	term := modeTerm(t, 5, 1, "abc\x1b[1;1H\x1b[4hX")
	wantRows(t, term, []string{"Xabc"})
}

func TestInsertModeTruncatesAtRightMargin(t *testing.T) {
	term := modeTerm(t, 12, 1,
		"\x1b[1;5Habcdef\x1b[?69h\x1b[5;10s\x1b[1;7H\x1b[4hX")
	wantRows(t, term, []string{"    abXcde"})
}

func TestLineFeedNewLine(t *testing.T) {
	for _, lf := range []string{"\n", "\v", "\f"} {
		term := modeTerm(t, 8, 3, "\x1b[1;5H\x1b[20h"+lf)
		if c := term.grid.Cursor(); c.X != 0 || c.Y != 1 {
			t.Errorf("after %q with LNM on, Cursor() = %+v, want 0,1", lf, c)
		}
	}

	term := modeTerm(t, 8, 3, "\x1b[1;5H\x1b[20l\n")
	if c := term.grid.Cursor(); c.X != 4 || c.Y != 1 {
		t.Errorf("after LF with LNM off, Cursor() = %+v, want 4,1", c)
	}
}

func TestTabWithMoreFixWrapsToFirstTabStop(t *testing.T) {
	term := modeTerm(t, 20, 3, "\x1b[?41h"+"xxxxxxxxxxxxxxxxxxxx\t")
	if c := term.grid.Cursor(); c.X != 8 || c.Y != 1 {
		t.Fatalf("after a full row and a tab, Cursor() = %+v, want 8,1", c)
	}
	writeTerm(term, "1")
	if got := term.grid.Text(1); got != "        1" {
		t.Errorf("row 2 = %q, want the tab drawn at column 9", got)
	}
}

func TestTabWithoutMoreFixKeepsTheWrapPending(t *testing.T) {
	term := modeTerm(t, 20, 2, "\x1b[?41l"+"xxxxxxxxxxxxxxxxxxxx\t")
	c := term.grid.Cursor()
	if c.X != 19 || !c.WrapNext {
		t.Fatalf("after a full row and a tab, Cursor() = %+v, want column 19 with a pending wrap", c)
	}
	writeTerm(term, "2")
	if got := term.grid.Text(1); got != "2" {
		t.Errorf("row 2 = %q, want the write to have wrapped to column 1", got)
	}
}

func TestDECSCSavesProtection(t *testing.T) {
	term := modeTerm(t, 3, 1, "\x1b[1\"q\x1b7\x1b[0\"q\x1b8a")
	writeTerm(term, "\x1b[1;1;1;1${")
	wantRows(t, term, []string{"a"})
}

func TestWindowResizeOp(t *testing.T) {
	term := modeTerm(t, 80, 25, "\x1b[8;5;20t")
	if cols, rows := term.grid.Size(); cols != 20 || rows != 5 {
		t.Fatalf("Size() = %d,%d, want 20,5", cols, rows)
	}
}

func TestWindowResizeTracksColumnMode(t *testing.T) {
	term := modeTerm(t, 80, 25, "\x1b[8;25;132t")
	if !term.grid.Modes().ColumnMode {
		t.Fatal("a 132-column resize did not set the column mode")
	}
	writeTerm(term, "\x1b[8;25;80t")
	if term.grid.Modes().ColumnMode {
		t.Fatal("an 80-column resize did not clear the column mode")
	}
}

func TestAllow80To132IsOnByDefault(t *testing.T) {
	term := NewTerm(80, 25)
	if !term.grid.Modes().Allow80To132 {
		t.Fatal("DECSET 40 is off by default, want on as in xterm")
	}
}
