package esctest

import "testing"

// TestDECRQMRoutesToTheVT pins that the harness hands a mode query to the
// report terminal instead of answering it itself.
func TestDECRQMRoutesToTheVT(t *testing.T) {
	h, r, w := dsrHarness(t)
	// Autowrap is on in a fresh terminal.
	got := feedRead(t, h, r, w, "\x1b[?7$p")
	if want := "\x1b[?7;1$y"; got != want {
		t.Fatalf("DECRQM reply = %q, want %q", got, want)
	}
}

// TestDECRQMRoutesStateToTheVT pins that the report terminal sees the state the
// mode family set.
func TestDECRQMRoutesStateToTheVT(t *testing.T) {
	h, r, w := dsrHarness(t)
	h.feed([]byte("\x1b[?7l\x1b[4h"))
	got := feedRead(t, h, r, w, "\x1b[?7$p\x1b[4$p")
	if want := "\x1b[?7;2$y\x1b[4;1$y"; got != want {
		t.Fatalf("DECRQM reply = %q, want %q", got, want)
	}
}

// TestDECRQSSRoutesToTheVT pins that a DECRQSS DCS is handed to the report
// terminal and its answer comes back.
func TestDECRQSSRoutesToTheVT(t *testing.T) {
	h, r, w := dsrHarness(t)
	h.feed([]byte("\x1b[5;6r"))
	got := feedRead(t, h, r, w, "\x1bP$qr\x1b\\")
	if want := "\x1bP1$r5;6r\x1b\\"; got != want {
		t.Fatalf("DECRQSS reply = %q, want %q", got, want)
	}
}
