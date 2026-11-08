package vt

import "testing"

func TestSCOSCAndSCORCRestoreCursor(t *testing.T) {
	term := modeTerm(t, 10, 8, "\x1b[6;5H\x1b[s\x1b[1;1H\x1b[u")
	if c := term.grid.Cursor(); c.X != 4 || c.Y != 5 {
		t.Fatalf("after SCORC, Cursor() = %+v, want 4,5", c)
	}
}

func TestDECSTRResetsTheSCOSCPosition(t *testing.T) {
	term := modeTerm(t, 10, 8, "\x1b[6;5H\x1b[s\x1b[!p\x1b[u")
	if c := term.grid.Cursor(); c.X != 0 || c.Y != 0 {
		t.Fatalf("after DECSTR and SCORC, Cursor() = %+v, want the home position", c)
	}
}

func TestCSIisSCOSCWithoutLeftRightMargin(t *testing.T) {
	term := modeTerm(t, 10, 8, "\x1b[6;5H\x1b[s")
	if r := term.grid.ScrollRegion(); r.Left != 0 || r.Right != 9 {
		t.Fatalf("ScrollRegion() = %+v, want the screen edges", r)
	}
	if c := term.grid.Cursor(); c.X != 4 || c.Y != 5 {
		t.Fatalf("SCOSC moved the cursor to %+v, want 4,5", c)
	}
}

func TestCSIisDECSLRMInLeftRightMode(t *testing.T) {
	term := modeTerm(t, 10, 8, "\x1b[6;5H\x1b[s\x1b[?69h\x1b[2;4s")
	r := term.grid.ScrollRegion()
	if r.Left != 1 || r.Right != 3 {
		t.Fatalf("ScrollRegion() = %+v, want left 1 right 3", r)
	}
	// DECSLRM homed the cursor and did not overwrite the SCOSC position.
	writeTerm(term, "\x1b[u")
	if c := term.grid.Cursor(); c.X != 4 || c.Y != 5 {
		t.Fatalf("after SCORC, Cursor() = %+v, want the position SCOSC saved", c)
	}
}

func TestDECRQMIsSilentBelowLevel3(t *testing.T) {
	term := modeTerm(t, 10, 5, "\x1b[62\"p")
	if got := report(t, term, "\x1b[4$p\x1b[?6$p"); got != "" {
		t.Fatalf("level 2 DECRQM replied %q, want no reply", got)
	}
}

func TestDECRQMAnswersAtLevel3(t *testing.T) {
	term := modeTerm(t, 10, 5, "\x1b[63\"p")
	got := report(t, term, "\x1b[4$p\x1b[?6$p")
	if want := "\x1b[4;2$y\x1b[?6;2$y"; got != want {
		t.Fatalf("level 3 DECRQM replied %q, want %q", got, want)
	}
}
