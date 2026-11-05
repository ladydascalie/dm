package main

import "testing"

// menuHintTUI is a client with a live conn and an open pane menu on pane a.
func menuHintTUI(t *testing.T) (*tui, chan Msg) {
	t.Helper()
	tui, got := clientTUI(t, nil)
	tui.openMenu(tui.paneByID("a"), 10, 5)
	if tui.menu == nil {
		t.Fatal("menu did not open")
	}
	noMsg(t, got)
	return tui, got
}

// TestMenuHintClosesPane: the close-pane hint dispatches close pane on the
// clicked pane and closes the menu.
func TestMenuHintClosesPane(t *testing.T) {
	tui, got := menuHintTUI(t)
	feed(tui, "x")
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "close-pane" || m.Target != "a" {
		t.Fatalf("x sent %+v, want close-pane on a", m)
	}
	noMsg(t, got)
	if tui.menu != nil || tui.mode != "" {
		t.Fatalf("the hint left the menu open: %+v %q", tui.menu, tui.mode)
	}
	if len(tui.inbuf) != 0 {
		t.Fatalf("the hint left bytes in the buffer: %q", tui.inbuf)
	}
}

// TestMenuHintNewWindow: the new-window hint dispatches new window.
func TestMenuHintNewWindow(t *testing.T) {
	tui, got := menuHintTUI(t)
	feed(tui, "c")
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "new-window" {
		t.Fatalf("c sent %+v, want new-window", m)
	}
	noMsg(t, got)
	if tui.menu != nil {
		t.Fatalf("the hint left the menu open: %+v", tui.menu)
	}
}

// TestRailMenuHintClosesWindow: the close-window hint in a rail window menu
// targets that row's window, never the focused pane.
func TestRailMenuHintClosesWindow(t *testing.T) {
	tui, got := railMenuTUI(t)
	// Row 4 is window two, whose pane is not the focused one.
	railRightPress(tui, 4)
	if tui.menu == nil {
		t.Fatal("menu did not open")
	}
	noMsg(t, got)
	feed(tui, "X")
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "close-window" || m.Target != "w2" {
		t.Fatalf("X sent %+v, want close-window on w2", m)
	}
	if m.Target == tui.snap.Focus {
		t.Fatalf("the hint targeted the focused pane %q", m.Target)
	}
	noMsg(t, got)
	if tui.menu != nil {
		t.Fatalf("the hint left the menu open: %+v", tui.menu)
	}
}

// TestMenuBindingBeatsHint: q closes the menu and runs no item, so the menu
// mode's own binding wins over any hint.
func TestMenuBindingBeatsHint(t *testing.T) {
	tui, got := menuHintTUI(t)
	feed(tui, "q")
	noMsg(t, got)
	if tui.menu != nil || tui.mode != "" {
		t.Fatalf("q did not close the menu: %+v %q", tui.menu, tui.mode)
	}
}

// TestMenuHintUnboundCloses: a key that is neither a menu binding nor a hint
// closes the menu and dispatches nothing.
func TestMenuHintUnboundCloses(t *testing.T) {
	tui, got := menuHintTUI(t)
	feed(tui, "y")
	noMsg(t, got)
	if tui.menu != nil || tui.mode != "" {
		t.Fatalf("an unbound key kept the menu open: %+v %q", tui.menu, tui.mode)
	}
	if len(tui.inbuf) != 0 {
		t.Fatalf("an unbound key left bytes in the buffer: %q", tui.inbuf)
	}
}

// TestMenuHintSkipsBadLabel: a label that names no key is skipped, not fatal,
// and the next row's hint still dispatches.
func TestMenuHintSkipsBadLabel(t *testing.T) {
	picked := false
	tui := &tui{snap: testSnapshot(&Node{Pane: "a"}, 100, 30, nil)}
	tui.menu = &menu{x: 1, y: 1, w: 20, title: "t", items: []menuItem{
		{label: "Bad", key: "bogus"},
		{label: "Good", key: ""},
		{label: "Close", key: "x", run: func() { picked = true }},
	}}
	tui.mode = "menu"
	tui.inbuf = []byte("x")
	if !tui.menuKey() {
		t.Fatal("the hint waited")
	}
	if !picked || tui.menu != nil {
		t.Fatalf("the hint did not run: picked=%v menu=%+v", picked, tui.menu)
	}
}

// TestMenuHintSpansReads: a hint split across two reads dispatches once, on the
// completing read. The test keymap leaves esc and the arrows unbound in menu
// mode, so the hint alone decides.
func TestMenuHintSpansReads(t *testing.T) {
	tui, got := clientTUI(t, nil)
	tui.keymap = keymapTestLoad(t, `
prefix "ctrl+b"

keymap "main" {
    "close-pane" "shift-tab"
}

keymap "menu" {
    "close" "q"
}
`)
	tui.openMenu(tui.paneByID("a"), 10, 5)
	if tui.menu == nil {
		t.Fatal("menu did not open")
	}
	noMsg(t, got)
	tui.inbuf = append(tui.inbuf, []byte("\x1b[")...)
	tui.processInput()
	noMsg(t, got)
	if tui.menu == nil {
		t.Fatal("a partial hint closed the menu")
	}
	if len(tui.inbuf) != 2 {
		t.Fatalf("a partial hint was not buffered: %q", tui.inbuf)
	}
	tui.inbuf = append(tui.inbuf, 'Z')
	tui.processInput()
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "close-pane" || m.Target != "a" {
		t.Fatalf("the completed hint sent %+v, want close-pane on a", m)
	}
	noMsg(t, got)
	if tui.menu != nil {
		t.Fatalf("the hint left the menu open: %+v", tui.menu)
	}
}
