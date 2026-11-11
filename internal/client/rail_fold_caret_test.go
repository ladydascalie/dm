package client

import (
	"testing"

	"dm/internal/testutil"
	"dm/internal/wire"
)

// TestRailCaretToggleDoesNotChangeFocus checks that a left press on the
// workspace row's caret sends toggle-fold with the workspace id and sends no
// focus request. The project bar owns column 0, so the caret cell is zero-based
// column 1 and the mouse column is 2. Row 0 is the first workspace, so the
// mouse row is 1.
func TestRailCaretToggleDoesNotChangeFocus(t *testing.T) {
	t.Parallel()
	tui, got := railMenuTUI(t)
	tui.handleMouse(mouseEvent{btn: 0, x: 2, y: 1})
	m := testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != "toggle-fold" || m.Target != "s1" {
		t.Fatalf("left press on the caret sent %+v, want toggle-fold s1", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 2, y: 1})
	// A caret press must not change focus, so no further message follows.
	testutil.NoMsg(t, got)
}

// TestRailWindowCaretToggleDoesNotChangeFocus checks that a left press on a
// window row's caret sends toggle-fold with the window id and sends no focus
// request. The project bar owns column 0, so the window caret is zero-based
// column 3 and the mouse column is 4. Row 1 is window one, so the mouse row is
// 2.
func TestRailWindowCaretToggleDoesNotChangeFocus(t *testing.T) {
	t.Parallel()
	tui, got := railMenuTUI(t)
	tui.handleMouse(mouseEvent{btn: 0, x: 4, y: 2})
	m := testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != "toggle-fold" || m.Target != "w1" {
		t.Fatalf("left press on the window caret sent %+v, want toggle-fold w1", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 4, y: 2})
	// A caret press must not change focus, so no further message follows.
	testutil.NoMsg(t, got)
}

// TestRailProjectCaretToggle checks that a left press on a project row's caret
// sends toggle-fold with the project target and sends no focus request. The
// project bar owns column 0, so the caret is zero-based column 1 and the mouse
// column is 2. Row 0 is the project header, so the mouse row is 1.
func TestRailProjectCaretToggle(t *testing.T) {
	t.Parallel()
	tui, got := railMenuTUI(t)
	tui.snap = projectRailSnap()
	tui.handleMouse(mouseEvent{btn: 0, x: 2, y: 1})
	m := testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != "toggle-fold" || m.Target != wire.ProjectTargetPrefix+"dm" {
		t.Fatalf("left press on the project caret sent %+v, want toggle-fold project:dm", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 2, y: 1})
	testutil.NoMsg(t, got)
}
