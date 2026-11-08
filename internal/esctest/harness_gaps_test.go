package esctest

import (
	"strings"
	"testing"
)

// TestFixedArgsDeclareBothOptions pins that the harness tells esctest2 which
// capabilities it answers itself. allowC2Controls covers the 8-bit C1 forms and
// xtermWinopsEnabled covers the window operations, including the resize the
// DECCOLM tests need.
func TestFixedArgsDeclareBothOptions(t *testing.T) {
	args := fixedArgs("/tmp/esctest.log")
	got := map[string]bool{}
	for i, a := range args {
		if a != "--options" {
			continue
		}
		for _, v := range args[i+1:] {
			if strings.HasPrefix(v, "-") {
				break
			}
			got[v] = true
		}
	}
	for _, want := range []string{"allowC2Controls", "xtermWinopsEnabled"} {
		if !got[want] {
			t.Errorf("fixedArgs() option %q is missing from %q", want, args)
		}
	}
}

// TestDECRQCRAAppliesOriginMode pins that a checksum rectangle is read
// relative to the top and left margins in origin mode. The X sits at absolute
// 5,5, so the test asks for origin cell 1,1 with the margins at 5 through 7.
func TestDECRQCRAAppliesOriginMode(t *testing.T) {
	h, r, w := dsrHarness(t)
	h.feed([]byte("\x1b[5;5HX\x1b[5;7r\x1b[?69h\x1b[5;7s\x1b[?6h"))
	got := feedRead(t, h, r, w, "\x1b[1;0;1;1;1;1*y")
	if want := "\x1bP1!~0058\x1b\\"; got != want {
		t.Fatalf("DECRQCRA reply = %q, want %q", got, want)
	}
}

// TestDECRQCRAIsAbsoluteWithoutOriginMode pins that the same rectangle stays
// absolute when origin mode is off.
func TestDECRQCRAIsAbsoluteWithoutOriginMode(t *testing.T) {
	h, r, w := dsrHarness(t)
	h.feed([]byte("\x1b[5;5HX\x1b[5;7r\x1b[?69h\x1b[5;7s\x1b[?6l"))
	got := feedRead(t, h, r, w, "\x1b[1;0;1;1;1;1*y")
	if want := "\x1bP1!~0020\x1b\\"; got != want {
		t.Fatalf("DECRQCRA reply = %q, want %q", got, want)
	}
}
