package vt

import "testing"

func TestEDIgsnoresDECSCAProtection(t *testing.T) {
	term := NewTerm(3, 1)
	writeTerm(term, "ab\x1b[1\"qc\x1b[0\"q\x1b[1;1H\x1b[0J")
	wantRows(t, term, []string{""})
}

func TestEDRespectsISOProtection(t *testing.T) {
	term := NewTerm(3, 1)
	writeTerm(term, "ab\x1bVc\x1bW\x1b[1;1H\x1b[0J")
	wantRows(t, term, []string{"  c"})
}

func TestELRespectsISOProtection(t *testing.T) {
	term := NewTerm(3, 1)
	writeTerm(term, "ab\x1bVc\x1bW\x1b[2K")
	wantRows(t, term, []string{"  c"})
}

func TestECHRespectsISOProtection(t *testing.T) {
	term := NewTerm(3, 1)
	writeTerm(term, "ab\x1bVc\x1bW\x1b[1;1H\x1b[3X")
	wantRows(t, term, []string{"  c"})
}

func TestDECSEDLeavesProtectedCells(t *testing.T) {
	term := NewTerm(10, 1)
	writeTerm(term, "\x1b[1\"qabcdefghij\x1b[0\"q")
	writeTerm(term, "\x1b[1;1H\x1b[?0J")
	wantRows(t, term, []string{"abcdefghij"})

	// An unprotected cell written over a protected one is erased.
	writeTerm(term, "\x1b[1;1HX\x1b[1;1H\x1b[?0J")
	wantRows(t, term, []string{" bcdefghij"})

	writeTerm(term, "\x1b[?2J")
	wantRows(t, term, []string{" bcdefghij"})
}

func TestDECSEDIgnoresMargins(t *testing.T) {
	term := NewTerm(5, 3)
	writeTerm(term, "abcde\r\nfghij\r\nklmno")
	writeTerm(term, "\x1b[?69h\x1b[2;4s\x1b[2;3r\x1b[2;3H\x1b[?0J")
	wantRows(t, term, []string{"abcde", "fg", ""})
}

func TestDECSELIgnoresMargins(t *testing.T) {
	term := NewTerm(10, 1)
	writeTerm(term, "abcdefghij\x1b[?69h\x1b[2;4s\x1b[5G\x1b[?2K")
	wantRows(t, term, []string{""})
}

func TestDECSELProtection(t *testing.T) {
	fresh := func() *Term {
		term := NewTerm(10, 1)
		writeTerm(term, "\x1b[1\"qa\x1b[0\"qbcdefghij")
		return term
	}

	term := fresh()
	writeTerm(term, "\x1b[5G\x1b[?0K")
	wantRows(t, term, []string{"abcd"})

	// The protected first cell survives an erase that covers it.
	term = fresh()
	writeTerm(term, "\x1b[5G\x1b[?1K")
	wantRows(t, term, []string{"a    fghij"})
}

func TestDECSTRClearsProtection(t *testing.T) {
	term := NewTerm(3, 1)
	writeTerm(term, "\x1b[1\"q\x1b[1!pX\x1b[?2J")
	wantRows(t, term, []string{""})
}

func TestRISClearsProtection(t *testing.T) {
	term := NewTerm(3, 1)
	writeTerm(term, "\x1b[1\"q\x1bcX\x1b[?2J")
	wantRows(t, term, []string{""})
}

func TestSPAEPAStampsOnlyTheArea(t *testing.T) {
	term := NewTerm(4, 1)
	writeTerm(term, "a\x1bVbc\x1bWd")
	if !term.grid.Cell(1, 0).ISOProtected || !term.grid.Cell(2, 0).ISOProtected {
		t.Fatal("cells inside the protected area are not protected")
	}
	if term.grid.Cell(0, 0).ISOProtected || term.grid.Cell(3, 0).ISOProtected {
		t.Fatal("cells outside the protected area are protected")
	}
}
