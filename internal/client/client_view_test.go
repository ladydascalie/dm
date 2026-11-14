package client

// The frame's cursor contract and the band scoping around an overlay: a
// screen-owning mode hides the cursor, and a new overlay leaves the bands it
// does not overlap alone.

import (
	"strings"
	"testing"
)

// A screen-owning mode hides the cursor the last pane frame showed. No cell
// in a mode frame takes typing from the client: rename and search draw their
// own carets, so the real cursor must not blink over the mode frame.
func TestCursorHidesInEveryMode(t *testing.T) {
	t.Parallel()
	tui, _ := switcherTUI(t)
	for _, mode := range []string{
		"box", "help", "search", "scroll", "sessions", "menu", "rename", "layout", "notif", "float",
	} {
		tui.mode = mode
		if got := tui.cursorBlock(); got != "\x1b[?25l" {
			t.Fatalf("mode %q cursor block = %q, want the hide sequence", mode, got)
		}
	}
}

// A toast appearing leaves the chrome quiet: the tab bar row and the bar row
// keep their bytes, and pane rows away from the toast stay unpainted. A
// blanket repaint of every band on an overlay change fails this.
func TestFrameToastSparesTheChrome(t *testing.T) {
	t.Parallel()
	f := newFrameTUI(t, 100, 20, 34)
	f.frame()

	f.tui.toasts = []toast{{text: "a scoped toast"}}
	got := f.frame()
	if !strings.Contains(got, "a scoped toast") {
		t.Fatalf("toast missing from the frame: %q", got)
	}
	if strings.Contains(got, "PANE-MARK") {
		t.Fatalf("a toast repainted the pane content: %q", got)
	}
	for _, a := range frameAddrs(got) {
		if a[0] == 1 && a[1] > f.snap.Rail {
			t.Fatalf("a toast rewrote the tab bar: %v", frameAddrs(got))
		}
		if a[0] == f.snap.Rows {
			t.Fatalf("a toast rewrote the bar row: %v", frameAddrs(got))
		}
	}
}
