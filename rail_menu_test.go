package main

import (
	"strings"
	"testing"
)

// railMenuTUI is a client on the railRowSnap rail with a live conn. The rail
// rows are: 0 workspace main, 1 window one, 2 its note, 3 pane pi, 4 window
// two, 5 gap, 6 workspace work, 7 window three. Row 8 is the control.
func railMenuTUI(t *testing.T) (*tui, chan Msg) {
	t.Helper()
	tui, got := clientTUI(t, nil)
	tui.snap = railRowSnap(20)
	return tui, got
}

// railRightPress sends a right press on zero-based rail row y.
func railRightPress(tui *tui, y int) {
	tui.handleMouse(mouseEvent{btn: 2, x: 3, y: y + 1})
}

// railMenuLabels is the menu's item labels in order.
func railMenuLabels(m *menu) []string {
	out := make([]string, len(m.items))
	for i, it := range m.items {
		out[i] = it.label
	}
	return out
}

// railWantLabels fails unless the menu holds exactly the wanted labels.
func railWantLabels(t *testing.T, m *menu, want ...string) {
	t.Helper()
	got := railMenuLabels(m)
	if len(got) != len(want) {
		t.Fatalf("menu items = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("menu items = %v, want %v", got, want)
		}
	}
}

// railMenuPick finds the item whose label starts with prefix and runs it.
func railMenuPick(t *testing.T, tui *tui, prefix string) {
	t.Helper()
	if tui.menu == nil {
		t.Fatalf("no menu open for %q", prefix)
	}
	for i, it := range tui.menu.items {
		if strings.HasPrefix(it.label, prefix) {
			tui.menuPick(i)
			return
		}
	}
	t.Fatalf("menu has no item %q: %v", prefix, railMenuLabels(tui.menu))
}

// TestRailItemAt holds the hit test to the rail: an item row answers, the
// control row, the status row, a point outside the rail and a point past the
// last item do not.
func TestRailItemAt(t *testing.T) {
	snap := railRowSnap(20)
	if it, ok := railItemAt(snap, 2, 0); !ok || it.kind != railWorkspace {
		t.Fatalf("row 0 = %+v, %v, want the workspace row", it, ok)
	}
	if it, ok := railItemAt(snap, 2, 7); !ok || it.kind != railWindow {
		t.Fatalf("last item row = %+v, %v, want the window row", it, ok)
	}
	for _, c := range []struct{ x, y int }{
		{-1, 0}, {20, 0}, {2, -1}, {2, snap.Rows - 2}, {2, snap.Rows - 1},
	} {
		if it, ok := railItemAt(snap, c.x, c.y); ok {
			t.Fatalf("railItemAt(%d,%d) = %+v, want false", c.x, c.y, it)
		}
	}
}

// TestRailMenuWorkspaceRow opens the workspace menu on a right press and sends
// nothing. The release of the opening press is swallowed and leaves it up.
func TestRailMenuWorkspaceRow(t *testing.T) {
	tui, got := railMenuTUI(t)
	railRightPress(tui, 0)
	if tui.mode != "menu" || tui.menu == nil {
		t.Fatalf("right press on the workspace row: mode=%q menu=%+v", tui.mode, tui.menu)
	}
	if tui.menu.title != "main" {
		t.Fatalf("title = %q, want main", tui.menu.title)
	}
	railWantLabels(t, tui.menu,
		"Rename workspace", "Close workspace", "New window", "New workspace")
	noMsg(t, got)
	tui.handleMouse(mouseEvent{btn: 2, release: true, x: 3, y: 1})
	if tui.mode != "menu" {
		t.Fatal("the release of the opening press closed the menu")
	}
	noMsg(t, got)
}

// TestRailMenuWindowRow opens the window menu on a window row and on its note
// row.
func TestRailMenuWindowRow(t *testing.T) {
	for _, row := range []int{1, 2} {
		tui, got := railMenuTUI(t)
		railRightPress(tui, row)
		if tui.mode != "menu" || tui.menu == nil {
			t.Fatalf("row %d: mode=%q menu=%+v", row, tui.mode, tui.menu)
		}
		railWantLabels(t, tui.menu,
			"Rename window", "Close window", "New window", "Fold/unfold")
		noMsg(t, got)
	}
}

// TestRailMenuPaneRow opens the pane menu on a pane row.
func TestRailMenuPaneRow(t *testing.T) {
	tui, got := railMenuTUI(t)
	railRightPress(tui, 3)
	if tui.mode != "menu" || tui.menu == nil {
		t.Fatalf("right press on the pane row: mode=%q menu=%+v", tui.mode, tui.menu)
	}
	if tui.menu.title != "pi" {
		t.Fatalf("title = %q, want pi", tui.menu.title)
	}
	railWantLabels(t, tui.menu,
		"Focus", "Rename window", "Close window", "Fold/unfold")
	noMsg(t, got)
}

// TestRailMenuRenameTargetsRow picks Rename on each row kind and holds the
// prompt to that row's kind, id and current label.
func TestRailMenuRenameTargetsRow(t *testing.T) {
	cases := []struct {
		row   int
		kind  string
		id    string
		draft string
	}{
		{0, "workspace", "s1", "main"},
		{1, "window", "w1", "one"},
		{2, "window", "w1", "one"},
		{3, "window", "w1", "one"},
	}
	for _, c := range cases {
		tui, _ := railMenuTUI(t)
		railRightPress(tui, c.row)
		railMenuPick(t, tui, "Rename")
		if tui.mode != "rename" {
			t.Fatalf("row %d: mode = %q, want rename", c.row, tui.mode)
		}
		if tui.renameKind != c.kind || tui.renameID != c.id || tui.renameDraft != c.draft {
			t.Fatalf("row %d: prompt = kind %q id %q draft %q, want %q %q %q",
				c.row, tui.renameKind, tui.renameID, tui.renameDraft,
				c.kind, c.id, c.draft)
		}
	}
}

// TestRailMenuCloseTargetsRow picks Close on each row kind and holds the
// request to the right action and target.
func TestRailMenuCloseTargetsRow(t *testing.T) {
	cases := []struct {
		row    int
		action string
		target string
	}{
		{0, "close-workspace", "s1"},
		{1, "close-window", "w1"},
		{2, "close-window", "w1"},
		{3, "close-window", "w1"},
	}
	for _, c := range cases {
		tui, got := railMenuTUI(t)
		railRightPress(tui, c.row)
		railMenuPick(t, tui, "Close")
		m := nextMsg(t, got)
		if m.Type != "req" || m.Action != c.action || m.Target != c.target {
			t.Fatalf("row %d: sent %+v, want %s %s", c.row, m, c.action, c.target)
		}
	}
}

// TestRailMenuFoldTargetsWindow picks Fold/unfold on a window row and a pane
// row and holds the request to the row's window.
func TestRailMenuFoldTargetsWindow(t *testing.T) {
	for _, row := range []int{1, 2, 3} {
		tui, got := railMenuTUI(t)
		railRightPress(tui, row)
		railMenuPick(t, tui, "Fold")
		m := nextMsg(t, got)
		if m.Type != "req" || m.Action != "toggle-fold" || m.Target != "w1" {
			t.Fatalf("row %d: sent %+v, want toggle-fold w1", row, m)
		}
	}
}

// TestRailMenuFocusTargetsPane picks Focus on a pane row.
func TestRailMenuFocusTargetsPane(t *testing.T) {
	tui, got := railMenuTUI(t)
	railRightPress(tui, 3)
	railMenuPick(t, tui, "Focus")
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "focus" || m.Target != "w1p1" {
		t.Fatalf("sent %+v, want focus w1p1", m)
	}
}

// TestRailMenuNewWindowTargetsRow picks New window on a workspace row and a
// window row. The window row carries its folder as cwd; the workspace row
// carries none.
func TestRailMenuNewWindowTargetsRow(t *testing.T) {
	cases := []struct {
		row int
		cwd string
	}{
		{0, ""},
		{1, "rail"},
		{2, "rail"},
	}
	for _, c := range cases {
		tui, got := railMenuTUI(t)
		railRightPress(tui, c.row)
		railMenuPick(t, tui, "New window")
		m := nextMsg(t, got)
		if m.Type != "req" || m.Action != "new-window" || m.Target != "s1" || m.Cwd != c.cwd {
			t.Fatalf("row %d: sent %+v, want new-window s1 cwd %q", c.row, m, c.cwd)
		}
	}
}

// TestRailMenuNewWorkspace picks New workspace on a workspace row.
func TestRailMenuNewWorkspace(t *testing.T) {
	tui, got := railMenuTUI(t)
	railRightPress(tui, 0)
	railMenuPick(t, tui, "New workspace")
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "new-workspace" {
		t.Fatalf("sent %+v, want new-workspace", m)
	}
}

// TestRailMenuControlRowOpensNothing holds the control row clear of the menu:
// a right press there opens nothing and sends nothing.
func TestRailMenuControlRowOpensNothing(t *testing.T) {
	tui, got := railMenuTUI(t)
	railRightPress(tui, tui.snap.Rows-2)
	if tui.mode != "" || tui.menu != nil {
		t.Fatalf("right press on the control row: mode=%q menu=%+v", tui.mode, tui.menu)
	}
	noMsg(t, got)
	tui.handleMouse(mouseEvent{btn: 2, release: true, x: 3, y: tui.snap.Rows - 1})
	noMsg(t, got)
}

// TestRailLeftPressStillWorks holds the left press to its old behaviour: a
// pane row focuses its pane and a window row folds its window.
func TestRailLeftPressStillWorks(t *testing.T) {
	tui, got := railMenuTUI(t)
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 4})
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "focus" || m.Target != "w1p1" {
		t.Fatalf("left press on the pane row sent %+v, want focus w1p1", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 4})
	noMsg(t, got)

	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 2})
	m = nextMsg(t, got)
	if m.Type != "req" || m.Action != "toggle-fold" || m.Target != "w1" {
		t.Fatalf("left press on the window row sent %+v, want toggle-fold w1", m)
	}
}

// TestRailMenuKeyHints holds each item to the main keymap's hint for its
// action. An unbound action shows no key.
func TestRailMenuKeyHints(t *testing.T) {
	cases := []struct {
		row  int
		want map[string]string
	}{
		{0, map[string]string{
			"Rename workspace": "r", "Close workspace": "",
			"New window": "c", "New workspace": "t",
		}},
		{1, map[string]string{
			"Rename window": "", "Close window": "X",
			"New window": "c", "Fold/unfold": "",
		}},
		{3, map[string]string{
			"Focus": "", "Rename window": "", "Close window": "X",
			"Fold/unfold": "",
		}},
	}
	for _, c := range cases {
		tui, _ := railMenuTUI(t)
		railRightPress(tui, c.row)
		for _, it := range tui.menu.items {
			if it.key != c.want[it.label] {
				t.Fatalf("row %d: %q key = %q, want %q",
					c.row, it.label, it.key, c.want[it.label])
			}
		}
	}
}
