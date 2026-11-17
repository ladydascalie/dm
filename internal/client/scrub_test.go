package client

import (
	"strings"
	"testing"

	"dm/internal/vt"
	"dm/internal/wire"
)

// termShowing builds one pane terminal showing text, both fed and flushed the
// way a live client draws one.
func termShowing(t *testing.T, w, h int, text string) *vt.Term {
	t.Helper()
	term := vt.NewTerm(w, h)
	term.Write([]byte(text))
	term.Flush()
	return term
}

// TestScrubFrameRepaintsTheLiveScreen proves the scrub frame is the toggle's
// repaint: the pane's repaired snapshot replaces the live terminal's picture,
// so the value the switch-off reveal drew is gone without new output.
func TestScrubFrameRepaintsTheLiveScreen(t *testing.T) {
	t.Parallel()
	tui, _ := clientTUI(t, nil)
	p := tui.paneByID("a")
	tui.vts["a"] = termShowing(t, p.Rect.W, p.Rect.H, "token: abcd1234\r\n")

	repaired := termShowing(t, p.Rect.W, p.Rect.H, "token: ********\r\n")
	tui.apply(wire.Msg{Type: "scrub", Target: "a", VTSnap: repaired.Snapshot()})

	got := tui.vts["a"].Grid().Text(0)
	if strings.Contains(got, "abcd1234") {
		t.Fatalf("live screen kept the revealed value: %q", got)
	}
	if !strings.Contains(got, "token: ********") {
		t.Fatalf("live screen = %q, want the repaired picture", got)
	}
}

// TestScrubFrameDropsTheCachedRing proves the scrollback cache goes with the
// repaint: the pane's ring is the only copy that was scrubbed, so the next
// scrollback asks for it again instead of serving the revealed bytes it
// cached.
func TestScrubFrameDropsTheCachedRing(t *testing.T) {
	t.Parallel()
	tui, sent := clientTUI(t, nil)
	p := tui.paneByID("a")
	revealed := "token: abcd1234\r\n"
	tui.vts["a"] = termShowing(t, p.Rect.W, p.Rect.H, revealed)
	tui.hist = map[string][]byte{"a": []byte(revealed)}
	tui.histMeta = map[string]histCache{"a": {at: p.Rect, got: true}}
	// The operator has the pane's scrollback open, built from the cached
	// ring, which is the surface the revealed copy would keep showing.
	tui.scroll = "a"
	tui.scrollVT = vt.NewTerm(p.Rect.W, 3*p.Rect.H)
	tui.mode = "scroll"

	repaired := termShowing(t, p.Rect.W, p.Rect.H, "token: ********\r\n")
	tui.apply(wire.Msg{Type: "scrub", Target: "a", VTSnap: repaired.Snapshot()})

	if _, ok := tui.hist["a"]; ok {
		t.Fatal("the revealed ring is still cached")
	}
	// The open scrollback asked for the ring again, so the cache entry is
	// back, but with nothing in hand: the next replay is the scrubbed one.
	if m, ok := tui.histMeta["a"]; !ok || m.got {
		t.Fatalf("ring cache = %+v after the scrub, want a pending refetch", m)
	}
	if tui.scrollVT != nil {
		t.Fatal("the revealed scrollback is still built")
	}
	select {
	case m := <-sent:
		if m.Type != "req" || m.Action != "replay" || m.Target != "a" {
			t.Fatalf("client sent %+v, want a replay request for the pane", m)
		}
	default:
		t.Fatal("client never asked the pane for the scrubbed ring")
	}
}
