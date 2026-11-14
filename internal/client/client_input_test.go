package client

import (
	"testing"
	"time"

	"dm/internal/testutil"
)

// TestFloatPasteGoesToCommandWhenAsked: a command that turned bracketed paste
// on gets the paste whole, markers and all, aimed at the float.
func TestFloatPasteGoesToCommandWhenAsked(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	req := openFloat(t, tui, got, "cat")
	tui.float.vt.Write([]byte("\x1b[?2004h"))
	feed(tui, "\x1b[200~hi there\x1b[201~")
	m := <-got
	if m.Type != "keys" || m.Target != req.Float || string(m.Keys) != "\x1b[200~hi there\x1b[201~" {
		t.Fatalf("paste sent %+v, want the framed paste to the float", m)
	}
	if tui.mode != "float" {
		t.Fatalf("mode = %q, want the float still up", tui.mode)
	}
	testutil.NoMsg(t, got)
}

// TestFloatPasteBareWhenNotAsked: a command that never asked for bracketed
// paste gets the text alone, so the markers never reach its line editor.
func TestFloatPasteBareWhenNotAsked(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	req := openFloat(t, tui, got, "cat")
	feed(tui, "\x1b[200~hi there\x1b[201~")
	m := <-got
	if m.Type != "keys" || m.Target != req.Float || string(m.Keys) != "hi there" {
		t.Fatalf("paste sent %+v, want the bare text to the float", m)
	}
	testutil.NoMsg(t, got)
}

// TestFloatMouseGoesToCommandWhenAsked: a click lands on the float's command
// as one SGR event whose point is relative to the float's rect.
func TestFloatMouseGoesToCommandWhenAsked(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	req := openFloat(t, tui, got, "htop")
	tui.float.vt.Write([]byte("\x1b[?1000h\x1b[?1006h"))
	// A left press at screen (12,6). The float's rect starts at (4,1) over the
	// 100x30 fixture, so the command reads a press at (8,5).
	feed(tui, "\x1b[<0;12;6M")
	m := <-got
	if m.Type != "keys" || m.Target != req.Float || string(m.Keys) != "\x1b[<0;8;5M" {
		t.Fatalf("press sent %+v, want the remapped press to the float", m)
	}
	feed(tui, "\x1b[<0;12;6m")
	m = <-got
	if m.Type != "keys" || m.Target != req.Float || string(m.Keys) != "\x1b[<0;8;5m" {
		t.Fatalf("release sent %+v, want the remapped release to the float", m)
	}
	if tui.mode != "float" {
		t.Fatalf("mode = %q, want the float still up", tui.mode)
	}
	testutil.NoMsg(t, got)
}

// TestFloatMouseDroppedWhenNotAsked: a command that never asked for the mouse
// gets no click, as before.
func TestFloatMouseDroppedWhenNotAsked(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	openFloat(t, tui, got, "cat")
	feed(tui, "\x1b[<0;12;6M")
	feed(tui, "\x1b[<0;12;6m")
	testutil.NoMsg(t, got)
	if tui.mode != "float" {
		t.Fatalf("mode = %q, want the float still up", tui.mode)
	}
}

// TestFloatMouseHoverDropped: motion with no button is hover, and dm keeps it
// from apps that asked only for button tracking, floats included.
func TestFloatMouseHoverDropped(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	req := openFloat(t, tui, got, "cat")
	tui.float.vt.Write([]byte("\x1b[?1000h\x1b[?1006h"))
	feed(tui, "\x1b[<32;12;6M")
	testutil.NoMsg(t, got)
	if tui.mode != "float" || tui.float.id != req.Float {
		t.Fatalf("hover left mode %q and float %v", tui.mode, tui.float)
	}
}

// TestPickerClicksGoNowhere: the session picker paints the whole screen, so a
// click under it must not act on the rail, the tab bar or a pane beneath, and
// a right click must not open the rail menu over it.
func TestPickerClicksGoNowhere(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	tui.mode = "sessions"
	// A left press and release over the rail, a right press over the rail and
	// a wheel over the middle: every one of them lands on picker pixels.
	feed(tui, "\x1b[<0;5;10M")
	feed(tui, "\x1b[<0;5;10m")
	feed(tui, "\x1b[<2;5;10M")
	feed(tui, "\x1b[<64;50;15M")
	testutil.NoMsg(t, got)
	if tui.mode != "sessions" {
		t.Fatalf("mode = %q, want the picker still up", tui.mode)
	}
	if tui.press != nil || tui.menu != nil || tui.selText != nil {
		t.Fatalf("a click under the picker armed press %v, menu %v or selection %v", tui.press, tui.menu, tui.selText)
	}
}

// TestPickerPasteClosesCleanly: a paste closes the picker the way any other
// input does, and the bytes go nowhere.
func TestPickerPasteClosesCleanly(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	tui.mode = "sessions"
	feed(tui, "\x1b[200~hi\x1b[201~")
	m := <-got
	// forwardPaste names no target: the server routes keys with none to the
	// focused pane, and the focus here is w1p1.
	if m.Type != "keys" || m.Target != "" || string(m.Keys) != "hi" {
		t.Fatalf("paste sent %+v, want the bare text for the focused pane", m)
	}
	if tui.mode != "" {
		t.Fatalf("mode = %q, want the picker closed", tui.mode)
	}
	testutil.NoMsg(t, got)
}

// feedKeys hands bytes to processInput the way a read would, and flushes a
// paste hold the way the ticker would: a lone esc or the head of a sequence
// sits in the paste hold first, and the test sees the key the operator pressed.
func feedKeys(tui *tui, keys string) {
	tui.inbuf = append(tui.inbuf, keys...)
	tui.processInput()
	if !tui.pasteDeadline.IsZero() {
		tui.expirePaste(time.Now().Add(pasteTimeout))
	}
}

// TestRenamePromptTakesUTF8 proves a multi-byte rune appends whole, in one
// read or split across two, where the byte filter used to drop it silently.
func TestRenamePromptTakesUTF8(t *testing.T) {
	t.Parallel()
	tr, _ := renamePromptTUI(t)
	feedKeys(tr, "\x02r")
	feedKeys(tr, "caf\xc3\xa9")
	if tr.renameDraft != "maincafé" {
		t.Fatalf("draft = %q, want maincafé", tr.renameDraft)
	}
	// A rune split across reads waits for its tail instead of dropping the
	// lead byte, and the tail joins it whole.
	feedKeys(tr, "\xe2")
	if tr.renameDraft != "maincafé" {
		t.Fatalf("a split lead byte changed the draft: %q", tr.renameDraft)
	}
	if !tr.pending {
		t.Fatal("a split rune did not hold the prompt")
	}
	feedKeys(tr, "\x82\xac")
	if tr.renameDraft != "maincafé€" {
		t.Fatalf("draft = %q, want maincafé€", tr.renameDraft)
	}
	// A control byte and a broken byte are still not text.
	feedKeys(tr, "\x01\xff")
	if tr.renameDraft != "maincafé€" {
		t.Fatalf("draft = %q, want maincafé€", tr.renameDraft)
	}
}

// TestRenamePromptUnboundEscapeKeepsDraft proves Delete, Home, ctrl+left and
// alt+d do not cancel the prompt: they are unbound escape sequences, and the
// one-byte esc binding for cancel must not eat their head and discard a
// half-typed name. The arrows still cancel, whole or one byte at a time.
func TestRenamePromptUnboundEscapeKeepsDraft(t *testing.T) {
	t.Parallel()
	tr, _ := renamePromptTUI(t)
	feedKeys(tr, "\x02r")
	feedKeys(tr, "keep")
	for _, seq := range []string{"\x1b[3~", "\x1b[H", "\x1b[1;5D", "\x1bd"} {
		feedKeys(tr, seq)
		if tr.mode != "rename" {
			t.Fatalf("%q left mode %q, want the prompt still open", seq, tr.mode)
		}
		if tr.renameDraft != "mainkeep" {
			t.Fatalf("%q left draft %q, want mainkeep", seq, tr.renameDraft)
		}
	}
	feedKeys(tr, "\x1b[D")
	if tr.mode != "" {
		t.Fatalf("an arrow key left mode %q, want the prompt closed", tr.mode)
	}
	feedKeys(tr, "\x02r")
	feedKeys(tr, "\x1b")
	tr.pasteDeadline = time.Now().Add(-escTimeout)
	if !tr.expirePaste(time.Now()) {
		t.Fatal("the stalled escape was not flushed")
	}
	if tr.mode != "" {
		t.Fatalf("a lone esc left mode %q, want the prompt closed", tr.mode)
	}
}

// TestRenamePromptPartialEscapeWaits proves the head of an unbound sequence
// waits for the rest instead of cancelling on its first bytes, and the
// completed sequence is dropped whole.
func TestRenamePromptPartialEscapeWaits(t *testing.T) {
	t.Parallel()
	tr, _ := renamePromptTUI(t)
	feedKeys(tr, "\x02r")
	feedKeys(tr, "keep")
	feedKeys(tr, "\x1b[")
	if tr.mode != "rename" {
		t.Fatal("the head of an escape sequence cancelled the prompt")
	}
	if !tr.pending {
		t.Fatal("the partial escape did not arm the expiry")
	}
	feedKeys(tr, "3~")
	if tr.mode != "rename" {
		t.Fatalf("mode = %q after the sequence completed, want rename", tr.mode)
	}
	if tr.renameDraft != "mainkeep" {
		t.Fatalf("draft = %q, want mainkeep", tr.renameDraft)
	}
}

// TestSearchQueryTakesUTF8 proves the scrollback query takes a multi-byte rune
// whole, in one read or split across two, where bytes were dropped silently.
func TestSearchQueryTakesUTF8(t *testing.T) {
	t.Parallel()
	tui, _ := searchTUI(t, 60)
	press(t, tui, "/")
	press(t, tui, "caf\xc3\xa9")
	if tui.search.draft != "café" {
		t.Fatalf("draft = %q, want café", tui.search.draft)
	}
	// A rune split across reads waits for its tail, and the tail joins it.
	tui.inbuf = append(tui.inbuf, "\xe2"...)
	tui.processInput()
	if tui.search.draft != "café" {
		t.Fatalf("a split lead byte changed the draft: %q", tui.search.draft)
	}
	if !tui.pending {
		t.Fatal("a split rune did not hold the query row")
	}
	tui.inbuf = append(tui.inbuf, "\x82\xac"...)
	tui.processInput()
	if tui.search.draft != "café€" {
		t.Fatalf("draft = %q, want café€", tui.search.draft)
	}
}

// TestTermColourDeadlineSlides proves a partial reply that keeps arriving is
// not flushed mid-flight: every sighting re-arms the deadline, and only a
// quiet partial flushes. The test pins the slide and the raw flush, not the
// stall window's length, so widening the window later does not rewrite it.
func TestTermColourDeadlineSlides(t *testing.T) {
	t.Parallel()
	tui, got := clientTUI(t, nil)
	feed(tui, "\x1b]10;rgb:ff")
	// Age the first arm past its deadline, the way a slow link leaves it
	// before the rest of the reply shows up.
	tui.termDeadline = time.Now().Add(-escTimeout - time.Millisecond)
	// More of the same reply arrives; the deadline moves with it.
	feed(tui, "ff/ff")
	// A tick probing between the first arm and the re-arm sees a reply that
	// is still growing: no flush. A deadline that never slid would already
	// be in the past here and would flush.
	if tui.expireTermColour(time.Now().Add(escTimeout - 5*time.Millisecond)) {
		t.Fatal("a growing reply was flushed mid-flight")
	}
	testutil.NoMsg(t, got)
	// A quiet partial still flushes as ordinary input, at whatever the
	// stall window is now.
	if !tui.expireTermColour(time.Now().Add(pasteTimeout + 10*time.Millisecond)) {
		t.Fatal("a stalled reply was not flushed")
	}
	m := testutil.NextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != "\x1b]10;rgb:ffff/ff" {
		t.Fatalf("stalled reply came through as %+v, want the raw bytes", m)
	}
	testutil.NoMsg(t, got)
}
