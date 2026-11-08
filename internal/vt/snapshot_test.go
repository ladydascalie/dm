package vt

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// roundTrip encodes a snapshot, decodes it and restores it into a fresh
// terminal of the wrong size, which is what a client does on attach.
func roundTrip(t *testing.T, term *Term) (*Term, *Snapshot) {
	t.Helper()
	s := term.Snapshot()
	b, err := s.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := DecodeSnapshot(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	restored := NewTerm(1, 1)
	if err := restored.Restore(decoded); err != nil {
		t.Fatalf("restore: %v", err)
	}
	return restored, decoded
}

// sameSnapshot fails with both encoded forms when two snapshots differ.
func sameSnapshot(t *testing.T, want, got *Snapshot) {
	t.Helper()
	if reflect.DeepEqual(want, got) {
		return
	}
	wb, _ := want.Encode()
	gb, _ := got.Encode()
	t.Fatalf("snapshot mismatch\nwant: %s\ngot:  %s", wb, gb)
}

func TestSnapshotRoundTrip(t *testing.T) {
	term := NewTerm(40, 10)
	writeTerm(term, "\x1b[H\x1b[2J")
	writeTerm(term, "日本語")
	writeTerm(term, "👨\u200d👩\u200d👧")
	writeTerm(term, " e\u0301a")
	writeTerm(term, "\x1b]8;;https://example.com\x07L\x1b]8;;\x07")
	writeTerm(term, "\x1b[1;4;38;5;196;48;2;1;2;3mS\x1b[0m")
	// A saved cursor with a non-default pen, then the pen cleared.
	writeTerm(term, "\x1b[7;4;38;5;33m\x1b[5;5H\x1b7\x1b[0m")
	// A scroll region and a non-default charset.
	writeTerm(term, "\x1b[3;8r")
	writeTerm(term, "\x1b(0")
	// Fill the last column of the bottom row so the wrap stays pending.
	writeTerm(term, "\x1b[10;40HZ")
	// Leave a half-received escape in the parser.
	writeTerm(term, "\x1b[38;5;")

	if got := term.Grid().Text(0); !strings.Contains(got, "日本語") {
		t.Fatalf("row 0 lost CJK: %q", got)
	}
	if got := term.Grid().Text(0); !strings.Contains(got, "👨\u200d👩\u200d👧") {
		t.Fatalf("row 0 lost the ZWJ family: %q", got)
	}
	if got := term.Grid().Text(0); !strings.Contains(got, "e\u0301") {
		t.Fatalf("row 0 lost the combining mark: %q", got)
	}
	if !term.Grid().Cursor().WrapNext {
		t.Fatal("the pending wrap did not survive the writes")
	}
	if cs := term.Grid().Charset(); cs.G0 != CharsetDECSpecial {
		t.Fatalf("charset G0 = %v, want DEC special", cs.G0)
	}

	want := term.Snapshot()
	restored, _ := roundTrip(t, term)
	sameSnapshot(t, want, restored.Snapshot())

	if !reflect.DeepEqual(term.Grid().ScrollRegion(), restored.Grid().ScrollRegion()) {
		t.Fatalf("scroll region %+v became %+v", term.Grid().ScrollRegion(), restored.Grid().ScrollRegion())
	}

	// The half-received CSI completes after the restore.
	suffix := "196m\x1b[2;2Hq"
	writeTerm(term, suffix)
	writeTerm(restored, suffix)
	sameSnapshot(t, term.Snapshot(), restored.Snapshot())
	if got := restored.Grid().Cell(1, 1).Content; got != "q" {
		t.Fatalf("cell 1,1 = %q, want q", got)
	}
	if got := restored.style.Fg; got != PaletteColor(196) {
		t.Fatalf("completed CSI set fg %+v, want palette 196", got)
	}
	if got := restored.Grid().Charset(); got.G0 != CharsetDECSpecial {
		t.Fatalf("charset after restore = %+v, want DEC special", got)
	}
}

func TestSnapshotPendingRune(t *testing.T) {
	days := []byte("日")
	if len(days) != 3 {
		t.Fatalf("日 is %d bytes, want 3", len(days))
	}
	term := NewTerm(10, 2)
	term.Write(days[:2])
	if got := term.Snapshot().Parser.Pending; !bytes.Equal(got, days[:2]) {
		t.Fatalf("pending = %q, want %q", got, days[:2])
	}
	restored, _ := roundTrip(t, term)
	restored.Write(days[2:])
	restored.Flush()
	if got := restored.Grid().Cell(0, 0).Content; got != "日" {
		t.Fatalf("cell 0,0 = %q, want 日", got)
	}
}

func TestSnapshotAltScreen(t *testing.T) {
	term := NewTerm(20, 4)
	writeTerm(term, "P")
	writeTerm(term, "\x1b[?1049h")
	writeTerm(term, "ALT")
	restored, _ := roundTrip(t, term)
	if !restored.Grid().Modes().AltScreen {
		t.Fatal("alt screen mode lost")
	}
	if got := restored.Grid().Text(0); !strings.Contains(got, "ALT") {
		t.Fatalf("visible alt row = %q, want ALT", got)
	}
	// Leaving the alternate screen must have a grid to swap into.
	writeTerm(restored, "\x1b[?1049l")
	if restored.Grid().Modes().AltScreen {
		t.Fatal("alt screen mode survived the exit")
	}
}

func TestSnapshotVersionRefused(t *testing.T) {
	term := NewTerm(10, 2)
	writeTerm(term, "abc")
	before := term.Snapshot()

	s := term.Snapshot()
	s.Version = SnapshotVersion + 1
	b, err := s.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := DecodeSnapshot(b); !errors.Is(err, ErrSnapshotVersion) {
		t.Fatalf("decode error = %v, want ErrSnapshotVersion", err)
	}
	if err := term.Restore(s); !errors.Is(err, ErrSnapshotVersion) {
		t.Fatalf("restore error = %v, want ErrSnapshotVersion", err)
	}
	sameSnapshot(t, before, term.Snapshot())

	bad := term.Snapshot()
	bad.Rows++
	if err := term.Restore(bad); !errors.Is(err, ErrSnapshotShape) {
		t.Fatalf("shape error = %v, want ErrSnapshotShape", err)
	}
	sameSnapshot(t, before, term.Snapshot())
}

func TestSnapshotSize(t *testing.T) {
	blank := NewTerm(80, 24)
	blankSize := encodedSize(t, blank)

	plain := NewTerm(80, 24)
	for y := 0; y < 24; y++ {
		plainFiller(plain, y)
	}
	plainSize := encodedSize(t, plain)

	styled := NewTerm(80, 24)
	writeTerm(styled, "\x1b[1;4;38;5;196;48;2;1;2;3m")
	for y := 0; y < 24; y++ {
		styledFiller(styled, y)
	}
	styledSize := encodedSize(t, styled)

	t.Logf("80x24 JSON snapshot: blank %d B, plain %d B, styled %d B", blankSize, plainSize, styledSize)
	if blankSize <= 0 || plainSize <= 0 || styledSize <= 0 {
		t.Fatal("encoded size is not positive")
	}
}

func encodedSize(t *testing.T, term *Term) int {
	t.Helper()
	b, err := term.Snapshot().Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return len(b)
}

func plainFiller(term *Term, row int) {
	line := "\x1b[" + itoa(row+1) + ";1H" + strings.Repeat("abcdefghij", 8)
	writeTerm(term, line)
}

func styledFiller(term *Term, row int) {
	line := "\x1b[" + itoa(row+1) + ";1H" + "styled row " + strings.Repeat("x", 69)
	writeTerm(term, line)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
