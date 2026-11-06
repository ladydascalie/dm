package client

import (
	"testing"

	"dm/internal/testutil"
)

// TestRailCaretToggleDoesNotChangeFocus checks that a left press on the
// workspace row's caret sends toggle-fold with the workspace id and sends no
// focus request. The caret cell is zero-based column 0, so the mouse column is
// 1. Row 0 is the first workspace, so the mouse row is 1.
func TestRailCaretToggleDoesNotChangeFocus(t *testing.T) {
	tui, got := railMenuTUI(t)
	tui.handleMouse(mouseEvent{btn: 0, x: 1, y: 1})
	m := testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != "toggle-fold" || m.Target != "s1" {
		t.Fatalf("left press on the caret sent %+v, want toggle-fold s1", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 1, y: 1})
	// A caret press must not change focus, so no further message follows.
	testutil.NoMsg(t, got)
}

// TestRailWindowCaretToggleDoesNotChangeFocus checks that a left press on a
// window row's caret sends toggle-fold with the window id and sends no focus
// request. The window caret is zero-based column 2, so the mouse column is 3.
// Row 1 is window one, so the mouse row is 2.
func TestRailWindowCaretToggleDoesNotChangeFocus(t *testing.T) {
	tui, got := railMenuTUI(t)
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 2})
	m := testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != "toggle-fold" || m.Target != "w1" {
		t.Fatalf("left press on the window caret sent %+v, want toggle-fold w1", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 2})
	// A caret press must not change focus, so no further message follows.
	testutil.NoMsg(t, got)
}
