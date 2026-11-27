package client

// The frame's cursor contract and the band scoping around an overlay: a
// screen-owning mode hides the cursor, and a new overlay leaves the bands it
// does not overlap alone.

import (
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/panel"
)

// A screen-owning mode hides the cursor the last pane frame showed. No cell
// in a mode frame takes typing from the client: rename and search draw their
// own carets, so the real cursor must not blink over the mode frame.
func TestCursorHidesInEveryMode(t *testing.T) {
	t.Parallel()
	tui, _ := switcherTUI(t)
	for _, mode := range []string{
		"box", "help", "search", "scroll", "sessions", "menu", "rename", "layout", "float",
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

// The update notice is announced where the operator is looking, not only in
// the scrollback above the alternate screen. The stderr line stays for the
// history; this is the ping.
func TestUpdateNoticeToasts(t *testing.T) {
	t.Parallel()
	f := newFrameTUI(t, 100, 20, 34)
	f.frame()

	f.tui.announceUpdate("dm 9.9.9 is out, run dm upgrade")
	if len(f.tui.toasts) != 1 {
		t.Fatalf("toasts = %d, want the notice", len(f.tui.toasts))
	}
	if got := f.frame(); !strings.Contains(got, "dm 9.9.9 is out, run dm upgrade") {
		t.Fatalf("the notice is not in the frame: %q", got)
	}

	// An empty line is not a toast, and nothing is announced twice.
	f.tui.toasts = nil
	f.tui.announceUpdate("")
	if len(f.tui.toasts) != 0 {
		t.Fatalf("an empty notice toasted %d times", len(f.tui.toasts))
	}
}

// A frame that closes one float and opens another keeps the float count
// equal, so a prune gated on the count would keep the closed float's paint
// entry forever. The prune runs against the live set every frame.
func TestFloatSwapDropsTheClosedFloatEntry(t *testing.T) {
	t.Parallel()
	f := newFrameTUI(t, 100, 20, 34)
	a := &panel.FloatingPane{ID: "a", Rect: layout.Rect{X: 2, Y: 2, W: 20, H: 6}, Active: true}
	f.tui.AddFloatingPane(a)
	f.frame()
	b := &panel.FloatingPane{ID: "b", Rect: layout.Rect{X: 4, Y: 4, W: 20, H: 6}, Active: true}
	f.tui.RemoveFloatingPane("a")
	f.tui.AddFloatingPane(b)
	f.frame()
	if _, ok := f.tui.last.floats["a"]; ok {
		t.Fatal("the closed float's paint entry survived the even swap")
	}
	if _, ok := f.tui.last.floats["b"]; !ok {
		t.Fatal("the opened float has no paint entry")
	}
}
