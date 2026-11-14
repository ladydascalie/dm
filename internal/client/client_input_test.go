package client

import (
	"testing"

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
