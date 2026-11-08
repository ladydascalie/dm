package client

import (
	"testing"
	"time"

	"dm/internal/testutil"
)

// TestTermColoursBothRepliesOneRequest proves the two replies coalesce into one
// request and that no byte of either reply reaches a pane.
func TestTermColoursBothRepliesOneRequest(t *testing.T) {
	tui, got := clientTUI(t, nil)
	feed(tui, "\x1b]10;rgb:ffff/ffff/ffff\x1b\\")
	testutil.NoMsg(t, got)
	feed(tui, "\x1b]11;rgb:0000/0000/0000\x1b\\")

	m := testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != "term-colours" {
		t.Fatalf("message = %+v, want a term-colours request", m)
	}
	if m.FG != "rgb:ffff/ffff/ffff" || m.BG != "rgb:0000/0000/0000" {
		t.Fatalf("request = %q/%q, want both raw replies", m.FG, m.BG)
	}
	testutil.NoMsg(t, got)
}

// TestTermColoursBELReplies proves the BEL-terminated form is consumed too.
func TestTermColoursBELReplies(t *testing.T) {
	tui, got := clientTUI(t, nil)
	feed(tui, "\x1b]10;rgb:aaaa/bbbb/cccc\x07")
	feed(tui, "\x1b]11;rgb:dddd/eeee/ffff\x07")

	m := testutil.NextMsg(t, got)
	if m.Action != "term-colours" || m.FG != "rgb:aaaa/bbbb/cccc" || m.BG != "rgb:dddd/eeee/ffff" {
		t.Fatalf("request = %+v, want both BEL replies", m)
	}
	testutil.NoMsg(t, got)
}

// TestTermColoursOneReplyAfterGrace proves a lone reply is sent once the grace
// for the second passes, with the other half left empty.
func TestTermColoursOneReplyAfterGrace(t *testing.T) {
	tui, got := clientTUI(t, nil)
	feed(tui, "\x1b]10;rgb:ffff/ffff/ffff\x1b\\")
	testutil.NoMsg(t, got)

	if !tui.expireTermColour(time.Now().Add(termGrace + time.Millisecond)) {
		t.Fatal("expireTermColour reported no change")
	}
	m := testutil.NextMsg(t, got)
	if m.Action != "term-colours" || m.FG != "rgb:ffff/ffff/ffff" || m.BG != "" {
		t.Fatalf("request = %+v, want the one reply and an empty half", m)
	}
	testutil.NoMsg(t, got)
}

// TestTermColoursPartialStallsThenReachesPane proves a partial reply is held,
// then flushed as ordinary input after the escape deadline.
func TestTermColoursPartialStallsThenReachesPane(t *testing.T) {
	tui, got := clientTUI(t, nil)
	partial := "\x1b]10;rgb:ffff"
	feed(tui, partial)
	testutil.NoMsg(t, got)
	if len(tui.inbuf) == 0 {
		t.Fatal("partial reply was not buffered")
	}

	if !tui.expireTermColour(time.Now().Add(escTimeout + time.Millisecond)) {
		t.Fatal("expireTermColour reported no change")
	}
	m := testutil.NextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != partial {
		t.Fatalf("stalled reply came through as %+v (%q), want the raw bytes", m, m.Keys)
	}
	testutil.NoMsg(t, got)
}

// TestTermColoursKeystrokeUntouched proves an ordinary keystroke is forwarded
// unchanged and starts no colour state.
func TestTermColoursKeystrokeUntouched(t *testing.T) {
	tui, got := clientTUI(t, nil)
	feed(tui, "x")

	m := testutil.NextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != "x" {
		t.Fatalf("keystroke came through as %+v (%q)", m, m.Keys)
	}
	if !tui.termGrace.IsZero() || !tui.termDeadline.IsZero() {
		t.Fatalf("keystroke started colour state: grace=%v deadline=%v", tui.termGrace, tui.termDeadline)
	}
	testutil.NoMsg(t, got)
}
