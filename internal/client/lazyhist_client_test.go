package client

import (
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/wire"

	"github.com/hinshun/vt10x"
)

// The client keeps no history until scrollback is opened: the ring is asked
// for once, cached per pane, and dropped when the pane's rect moves.

// TestBytesDoNotGrowHistory pins the lazy side: live output reaches the VT and
// nothing else.
func TestBytesDoNotGrowHistory(t *testing.T) {
	tui, _ := clientTUI(t, nil)
	tui.vts["a"] = vt10x.New(vt10x.WithSize(20, 5))
	tui.apply(wire.Msg{Type: "bytes", Target: "a", Data: []byte("live output")})
	if got := tui.hist["a"]; len(got) != 0 {
		t.Fatalf("live bytes filled the ring: %q", got)
	}
}

// TestScrollAsksForHistoryOnce checks the first entry sends exactly one
// request and a second entry sends none.
func TestScrollAsksForHistoryOnce(t *testing.T) {
	tui, sent := clientTUI(t, nil)
	tui.startScrollOnFocus()

	m := nextMsg(t, sent)
	if m.Type != "req" || m.Action != "replay" || m.Target != "a" {
		t.Fatalf("first entry sent %+v, want a replay request for a", m)
	}
	noMsg(t, sent)
	if tui.mode != "scroll" || tui.scroll != "a" {
		t.Fatalf("awaiting the ring: mode=%q scroll=%q", tui.mode, tui.scroll)
	}
	if tui.scrollVT != nil {
		t.Fatal("scrollback was built before the ring arrived")
	}

	tui.exitScroll()
	tui.startScrollOnFocus()
	noMsg(t, sent)
	if tui.scroll != "a" || tui.mode != "scroll" {
		t.Fatalf("second entry: mode=%q scroll=%q", tui.mode, tui.scroll)
	}
}

// TestReplayFillsScrollback checks a reply fills the history and the scroll
// then works.
func TestReplayFillsScrollback(t *testing.T) {
	tui, sent := clientTUI(t, nil)
	tui.startScrollOnFocus()
	if m := nextMsg(t, sent); m.Action != "replay" {
		t.Fatalf("first entry sent %+v", m)
	}

	ring := []byte(strings.Repeat("HIST-ROW\r\n", 200))
	tui.apply(wire.Msg{Type: "replay", Target: "a", Data: ring})
	if got := string(tui.hist["a"]); got != string(ring) {
		t.Fatalf("history holds %d bytes, want %d", len(got), len(ring))
	}
	if tui.scrollVT == nil {
		t.Fatal("replay did not fill the scrollback")
	}
	if tui.maxOff == 0 {
		t.Fatal("replay left no scroll range")
	}
	if tui.offset != 1 {
		t.Fatalf("scrollback offset = %d, want 1", tui.offset)
	}

	tui.inbuf = []byte("k")
	tui.processInput()
	if tui.offset != 2 {
		t.Fatalf("a scroll after the replay moved to %d, want 2", tui.offset)
	}
}

// TestScrollUnknownPaneIsQuiet checks an unknown pane id sends nothing and
// leaves the client working.
func TestScrollUnknownPaneIsQuiet(t *testing.T) {
	tui, sent := clientTUI(t, nil)
	tui.snap.Focus = "ghost"
	tui.startScrollOnFocus()
	noMsg(t, sent)
	if tui.mode != "" || tui.scroll != "" {
		t.Fatalf("unknown pane entered scrollback: mode=%q scroll=%q", tui.mode, tui.scroll)
	}

	// A replay for a pane that is not in the picture is dropped, not cached.
	tui.apply(wire.Msg{Type: "replay", Target: "ghost", Data: []byte("stale")})
	if got, ok := tui.hist["ghost"]; ok {
		t.Fatalf("cached a ring for an unknown pane: %q", got)
	}
	if _, ok := tui.histMeta["ghost"]; ok {
		t.Fatal("kept a cache key for an unknown pane")
	}

	// The UI still answers: a real pane asks and opens as usual.
	tui.snap.Focus = "a"
	tui.startScrollOnFocus()
	if m := nextMsg(t, sent); m.Action != "replay" || m.Target != "a" {
		t.Fatalf("after the unknown id the client sent %+v", m)
	}
	if tui.mode != "scroll" {
		t.Fatalf("after the unknown id mode = %q", tui.mode)
	}
}

// TestScrollDropsHistoryOnGeometry checks a snapshot that moves the pane drops
// the ring, so the next entry asks again.
func TestScrollDropsHistoryOnGeometry(t *testing.T) {
	tui, sent := clientTUI(t, nil)
	tui.startScrollOnFocus()
	nextMsg(t, sent)
	tui.apply(wire.Msg{Type: "replay", Target: "a", Data: []byte(strings.Repeat("LINE\r\n", 60))})
	if tui.scrollVT == nil {
		t.Fatal("replay did not fill the scrollback")
	}

	small := testSnapshot(&layout.Node{Pane: "a"}, 80, 24, nil)
	small.Focus = "a"
	tui.apply(wire.Msg{Type: "snapshot", Snap: small})
	if got, ok := tui.hist["a"]; ok {
		t.Fatalf("kept a ring the new rect cannot use: %q", got)
	}
	if _, ok := tui.histMeta["a"]; ok {
		t.Fatal("kept the cache key of a ring the new rect cannot use")
	}

	tui.startScrollOnFocus()
	if m := nextMsg(t, sent); m.Action != "replay" || m.Target != "a" {
		t.Fatalf("re-entry after the resize sent %+v", m)
	}
}
