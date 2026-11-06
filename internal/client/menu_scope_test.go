package client

import (
	"os"
	"path/filepath"
	"testing"

	"dm/internal/layout"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// paneMenuLabels is what every pane menu offers, without Focus.
var paneMenuLabels = []string{
	"Split right", "Split down", "Split left", "Split up",
	"Zoom", "Close pane", "Scrollback",
}

// menuHasLabel reports whether the open menu holds a row with the label.
func menuHasLabel(tui *tui, label string) bool {
	if tui.menu == nil {
		return false
	}
	for _, it := range tui.menu.items {
		if it.label == label {
			return true
		}
	}
	return false
}

// TestScreenRightClickOpensPaneMenuOnly holds the on-screen pane menu to the
// pane's own actions: no window, workspace or session row appears.
func TestScreenRightClickOpensPaneMenuOnly(t *testing.T) {
	tui, got := clientTUI(t, nil)
	p := tui.paneByID("a")
	tui.handleMouse(mouseEvent{btn: 2, x: p.Rect.X + 2, y: p.Rect.Y + 2})
	if tui.mode != "menu" || tui.menu == nil {
		t.Fatalf("right press on a pane: mode=%q menu=%+v", tui.mode, tui.menu)
	}
	if tui.menu.title != "pane a" {
		t.Fatalf("title = %q, want pane a", tui.menu.title)
	}
	railWantLabels(t, tui.menu, paneMenuLabels...)
	testutil.NoMsg(t, got)
	for _, label := range []string{
		"Focus", "New window", "New workspace", "Rename window",
		"Rename workspace", "Close window", "Close workspace",
		"Switch session", "Detach",
	} {
		if menuHasLabel(tui, label) {
			t.Fatalf("the pane menu offered %q: %v", label, railMenuLabels(tui.menu))
		}
	}
}

// TestRailPaneRowMenuOffersPaneActions holds a rail pane row to the pane
// actions with Focus first, and Close pane to that row's pane.
func TestRailPaneRowMenuOffersPaneActions(t *testing.T) {
	tui, got := railMenuTUI(t)
	railRightPress(tui, 3)
	if tui.mode != "menu" || tui.menu == nil {
		t.Fatalf("pane row menu: mode=%q menu=%+v", tui.mode, tui.menu)
	}
	if tui.menu.title != "pane pi" {
		t.Fatalf("title = %q, want pane pi", tui.menu.title)
	}
	railWantLabels(t, tui.menu, append([]string{"Focus", "Rename pane"}, paneMenuLabels...)...)
	testutil.NoMsg(t, got)
	railMenuPick(t, tui, "Close pane")
	m := testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != "close-pane" || m.Target != "w1p1" {
		t.Fatalf("Close pane sent %+v, want close-pane w1p1", m)
	}
}

// TestRailWorkspaceRowMenuOffersWorkspaceActions holds a workspace row to the
// workspace actions, and Close workspace to that workspace.
func TestRailWorkspaceRowMenuOffersWorkspaceActions(t *testing.T) {
	tui, got := railMenuTUI(t)
	railRightPress(tui, 0)
	if tui.mode != "menu" || tui.menu == nil {
		t.Fatalf("workspace row menu: mode=%q menu=%+v", tui.mode, tui.menu)
	}
	if tui.menu.title != "workspace main" {
		t.Fatalf("title = %q, want workspace main", tui.menu.title)
	}
	railWantLabels(t, tui.menu, "New window", "Rename workspace", "Close workspace")
	testutil.NoMsg(t, got)
	railMenuPick(t, tui, "Close workspace")
	m := testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != "close-workspace" || m.Target != "s1" {
		t.Fatalf("Close workspace sent %+v, want close-workspace s1", m)
	}
}

// TestRailControlRowMenuOffersSessionActions holds a right press on the control
// row to the session menu, and its rows to new workspace, the picker and quit.
func TestRailControlRowMenuOffersSessionActions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DM_STATE", dir)
	if err := os.MkdirAll(filepath.Join(dir, "sessions", "dev"), 0o755); err != nil {
		t.Fatalf("make session dir: %v", err)
	}
	control := func(t *testing.T) (*tui, chan wire.Msg) {
		t.Helper()
		tui, got := railMenuTUI(t)
		tui.snap.Session = "dev"
		railRightPress(tui, tui.snap.Rows-2)
		if tui.mode != "menu" || tui.menu == nil {
			t.Fatalf("control row menu: mode=%q menu=%+v", tui.mode, tui.menu)
		}
		if tui.menu.title != "session dev" {
			t.Fatalf("title = %q, want session dev", tui.menu.title)
		}
		railWantLabels(t, tui.menu, "New workspace", "Switch session", "Detach")
		testutil.NoMsg(t, got)
		return tui, got
	}

	tui, got := control(t)
	railMenuPick(t, tui, "New workspace")
	m := testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != "new-workspace" {
		t.Fatalf("New workspace sent %+v, want new-workspace", m)
	}

	tui, got = control(t)
	railMenuPick(t, tui, "Switch session")
	if tui.mode != "sessions" {
		t.Fatalf("Switch session gave mode %q, want sessions", tui.mode)
	}
	testutil.NoMsg(t, got)

	tui, got = control(t)
	railMenuPick(t, tui, "Detach")
	if !tui.quit {
		t.Fatal("Detach did not set quit")
	}
	testutil.NoMsg(t, got)
}

// TestRailGapRowMenuOpensNothing holds the gap row clear of every menu.
func TestRailGapRowMenuOpensNothing(t *testing.T) {
	tui, got := railMenuTUI(t)
	railRightPress(tui, 6)
	if tui.mode != "" || tui.menu != nil {
		t.Fatalf("right press on the gap row: mode=%q menu=%+v", tui.mode, tui.menu)
	}
	testutil.NoMsg(t, got)
}

// TestMenuScopeHints holds each menu row to the main keymap's key for its
// action. An action the main keymap leaves unbound shows no hint.
func TestMenuScopeHints(t *testing.T) {
	pane := map[string]string{
		"Split right": "|", "Split down": "-", "Split left": "<",
		"Split up": "^", "Zoom": "z", "Close pane": "x", "Scrollback": "[",
	}
	workspace := map[string]string{
		"New window": "c", "Rename workspace": "r", "Close workspace": "",
	}
	window := map[string]string{
		"Rename window": "", "Close window": "X", "Fold/unfold": "", "Focus": "",
	}
	session := map[string]string{
		"New workspace": "t", "Switch session": "s", "Detach": "d",
	}
	check := func(t *testing.T, tui *tui, want map[string]string) {
		t.Helper()
		if tui.menu == nil {
			t.Fatal("no menu open")
		}
		if len(tui.menu.items) != len(want) {
			t.Fatalf("menu rows = %v, want %d", railMenuLabels(tui.menu), len(want))
		}
		for _, it := range tui.menu.items {
			key, ok := want[it.label]
			if !ok {
				t.Fatalf("unexpected menu row %q", it.label)
			}
			if it.key != key {
				t.Fatalf("%s key = %q, want %q", it.label, it.key, key)
			}
			if it.action != "" && it.key != tui.km().Key("main", it.action) {
				t.Fatalf("%s key = %q, not the main keymap key for %q",
					it.label, it.key, it.action)
			}
		}
	}

	screen := &tui{snap: testutil.TestSnapshot(&layout.Node{Pane: "a"}, 100, 30, nil)}
	screen.openMenu(screen.paneByID("a"), 5, 3)
	check(t, screen, pane)

	rail := func(row int) *tui {
		t.Helper()
		tui, _ := railMenuTUI(t)
		railRightPress(tui, row)
		return tui
	}
	check(t, rail(0), workspace)
	check(t, rail(1), window)
	check(t, rail(3), map[string]string{
		"Focus": "", "Rename pane": "", "Split right": "|",
		"Split down": "-", "Split left": "<", "Split up": "^",
		"Zoom": "z", "Close pane": "x", "Scrollback": "[",
	})
	ctl, _ := railMenuTUI(t)
	ctl.snap.Session = "dev"
	railRightPress(ctl, ctl.snap.Rows-2)
	check(t, ctl, session)
}
