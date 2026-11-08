package vt

import "testing"

// rectTerm returns an 8 by 8 terminal holding a..z and 0..9 in rows, the same
// grid the esctest2 rectangle family writes.
func rectTerm(t *testing.T) *Term {
	t.Helper()
	term := NewTerm(8, 8)
	writeTerm(term, "abcdefgh\r\nijklmnop\r\nqrstuvwx\r\nyz012345\r\n"+
		"ABCDEFGH\r\nIJKLMNOP\r\nQRSTUVWX\r\nYZ6789!@")
	return term
}

// rowStrings returns every visible row.
func rowStrings(term *Term) []string {
	_, rows := term.grid.Size()
	out := make([]string, rows)
	for y := range out {
		out[y] = term.grid.Text(y)
	}
	return out
}

func wantRows(t *testing.T, term *Term, want []string) {
	t.Helper()
	got := rowStrings(term)
	if len(got) != len(want) {
		t.Fatalf("rows = %d, want %d", len(got), len(want))
	}
	for y := range want {
		if got[y] != want[y] {
			t.Fatalf("row %d = %q, want %q", y, got[y], want[y])
		}
	}
}

func TestDECERARect(t *testing.T) {
	term := rectTerm(t)
	writeTerm(term, "\x1b[5;5;7;7$z")
	wantRows(t, term, []string{
		"abcdefgh", "ijklmnop", "qrstuvwx", "yz012345",
		"ABCD   H", "IJKL   P", "QRST   X", "YZ6789!@",
	})
}

func TestDECERARectOriginMode(t *testing.T) {
	term := rectTerm(t)
	writeTerm(term, "\x1b[2;9r\x1b[?69h\x1b[2;9s\x1b[?6h\x1b[1;1;3;3$z")
	wantRows(t, term, []string{
		"abcdefgh", "i   mnop", "q   uvwx", "y   2345",
		"ABCDEFGH", "IJKLMNOP", "QRSTUVWX", "YZ6789!@",
	})
}

func TestDECERARectIgnoresMargins(t *testing.T) {
	term := rectTerm(t)
	writeTerm(term, "\x1b[?69h\x1b[3;6s\x1b[3;6r\x1b[5;5;7;7$z")
	wantRows(t, term, []string{
		"abcdefgh", "ijklmnop", "qrstuvwx", "yz012345",
		"ABCD   H", "IJKL   P", "QRST   X", "YZ6789!@",
	})
}

func TestDECFRARect(t *testing.T) {
	term := rectTerm(t)
	writeTerm(term, "\x1b[37;5;5;7;7$x") // '%' is 37
	wantRows(t, term, []string{
		"abcdefgh", "ijklmnop", "qrstuvwx", "yz012345",
		"ABCD%%%H", "IJKL%%%P", "QRST%%%X", "YZ6789!@",
	})
}

func TestDECCRARect(t *testing.T) {
	term := rectTerm(t)
	writeTerm(term, "\x1b[2;2;4;4;1;5;5;1$v")
	wantRows(t, term, []string{
		"abcdefgh", "ijklmnop", "qrstuvwx", "yz012345",
		"ABCDjklH", "IJKLrstP", "QRSTz01X", "YZ6789!@",
	})
}

func TestDECCRARectOverlapping(t *testing.T) {
	term := rectTerm(t)
	writeTerm(term, "\x1b[2;2;4;4;1;3;3;1$v")
	wantRows(t, term, []string{
		"abcdefgh", "ijklmnop", "qrjklvwx", "yzrst345",
		"ABz01FGH", "IJKLMNOP", "QRSTUVWX", "YZ6789!@",
	})
}

func TestDECCRARectDefaultsAndClip(t *testing.T) {
	term := rectTerm(t)
	writeTerm(term, "\x1b[2;2;2;2$v")
	if got := term.grid.Text(0); got != "jbcdefgh" {
		t.Fatalf("copy with default dest row 0 = %q, want jbcdefgh", got)
	}
	// A source past the right and bottom edges is clipped to one cell.
	writeTerm(term, "\x1b[8;8;99;99;1;1;1;1$v")
	if got := term.grid.Text(0); got != "@bcdefgh" {
		t.Fatalf("clipped copy row 0 = %q, want !cdefgh", got)
	}
}

func TestDECSCRectProtects(t *testing.T) {
	term := rectTerm(t)
	// Protect row 1, leave the rest unprotected, then selectively erase.
	writeTerm(term, "\x1b[1;1H\x1b[1\"qabcdefgh\x1b[0\"q\x1b[1;1;2;8${")
	wantRows(t, term, []string{
		"abcdefgh", "", "qrstuvwx", "yz012345",
		"ABCDEFGH", "IJKLMNOP", "QRSTUVWX", "YZ6789!@",
	})
}

func TestDECSCRectSkipsUnprotected(t *testing.T) {
	term := rectTerm(t)
	writeTerm(term, "\x1b[1;1H\x1b[1\"qabcdefgh\x1b[0\"q\x1b[1;1;1;3${")
	// Only the protected cells survive, so the unprotected part of row 1 stays.
	if got := term.grid.Text(0); got != "abcdefgh" {
		t.Fatalf("protected row = %q, want abcdefgh", got)
	}
}

func TestDECICInsertsColumns(t *testing.T) {
	term := rectTerm(t)
	writeTerm(term, "\x1b[2;2H\x1b[2'}")
	wantRows(t, term, []string{
		"a  bcdef", "i  jklmn", "q  rstuv", "y  z0123",
		"A  BCDEF", "I  JKLMN", "Q  RSTUV", "Y  Z6789",
	})
}

func TestDECICRespectsMargins(t *testing.T) {
	term := rectTerm(t)
	writeTerm(term, "\x1b[?69h\x1b[2;5s\x1b[2;2H\x1b[1'}")
	wantRows(t, term, []string{
		"a bcdfgh", "i jklnop", "q rstvwx", "y z01345",
		"A BCDFGH", "I JKLNOP", "Q RSTVWX", "Y Z679!@",
	})
}

func TestDECBIMovesAndScrolls(t *testing.T) {
	term := rectTerm(t)
	writeTerm(term, "\x1b[6;5H\x1b6")
	if c := term.grid.Cursor(); c.X != 3 || c.Y != 5 {
		t.Fatalf("DECBI moved to %+v, want 3,5", c)
	}

	// At the left margin it scrolls the region's columns right.
	term = rectTerm(t)
	writeTerm(term, "\x1b[?69h\x1b[3;5s\x1b[4;6r\x1b[5;3H\x1b6")
	wantRows(t, term, []string{
		"abcdefgh", "ijklmnop", "qrstuvwx", "yz 01345",
		"AB CDFGH", "IJ KLNOP", "QRSTUVWX", "YZ6789!@",
	})
	if c := term.grid.Cursor(); c.X != 2 || c.Y != 4 {
		t.Fatalf("DECBI scroll left the cursor at %+v, want 2,4", c)
	}
}

func TestDECFIMovesAndScrolls(t *testing.T) {
	term := rectTerm(t)
	writeTerm(term, "\x1b[6;5H\x1b9")
	if c := term.grid.Cursor(); c.X != 5 || c.Y != 5 {
		t.Fatalf("DECFI moved to %+v, want 5,5", c)
	}

	// At the right margin it scrolls the region's columns left.
	term = rectTerm(t)
	writeTerm(term, "\x1b[?69h\x1b[3;5s\x1b[4;6r\x1b[5;5H\x1b9")
	wantRows(t, term, []string{
		"abcdefgh", "ijklmnop", "qrstuvwx", "yz12 345",
		"ABDE FGH", "IJLM NOP", "QRSTUVWX", "YZ6789!@",
	})
	if c := term.grid.Cursor(); c.X != 4 || c.Y != 4 {
		t.Fatalf("DECFI scroll left the cursor at %+v, want 4,4", c)
	}
}
