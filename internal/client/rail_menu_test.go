package client

import (
	"dm/internal/rail"
	"dm/internal/testutil"
	"dm/internal/wire"
	"strings"
	"testing"
)

// railMenuTUI is a client on the railRowSnap rail with a live conn. The rail
// rows are: 0 workspace main, 1 window one, 2 note working hard, 3 pane pi,
// 4 window two, 5 pane beta, 6 gap, 7 workspace work, 8 window three, 9 pane
// gamma. Row 12 is the control.
func railMenuTUI(t *testing.T) (*tui, chan wire.Msg) {
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
	if it, ok := rail.RailItemAt(snap, 2, 0); !ok || it.Kind != rail.Workspace {
		t.Fatalf("row 0 = %+v, %v, want the workspace row", it, ok)
	}
	if it, ok := rail.RailItemAt(snap, 2, 9); !ok || it.Kind != rail.Pane {
		t.Fatalf("last item row = %+v, %v, want the pane row", it, ok)
	}
	for _, c := range []struct{ x, y int }{
		{-1, 0}, {20, 0}, {2, -1}, {2, snap.Rows - 2}, {2, snap.Rows - 1},
	} {
		if it, ok := rail.RailItemAt(snap, c.x, c.y); ok {
			t.Fatalf("rail.RailItemAt(%d,%d) = %+v, want false", c.x, c.y, it)
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
	if tui.menu.title != "workspace main" {
		t.Fatalf("title = %q, want workspace main", tui.menu.title)
	}
	railWantLabels(t, tui.menu,
		"New window", "Rename workspace", "Close workspace")
	testutil.NoMsg(t, got)
	tui.handleMouse(mouseEvent{btn: 2, release: true, x: 3, y: 1})
	if tui.mode != "menu" {
		t.Fatal("the release of the opening press closed the menu")
	}
	testutil.NoMsg(t, got)
}

// TestRailMenuPaneRow opens the pane menu on a pane row. A pane row offers its
// own rename between Focus and the split actions.
func TestRailMenuPaneRow(t *testing.T) {
	tui, got := railMenuTUI(t)
	railRightPress(tui, 3)
	if tui.mode != "menu" || tui.menu == nil {
		t.Fatalf("right press on the pane row: mode=%q menu=%+v", tui.mode, tui.menu)
	}
	if tui.menu.title != "pane pi" {
		t.Fatalf("title = %q, want pane pi", tui.menu.title)
	}
	railWantLabels(t, tui.menu,
		"Focus", "Rename pane", "Split right", "Split down", "Split left",
		"Split up", "Zoom", "Close pane", "Scrollback")
	testutil.NoMsg(t, got)
}

// TestRailMenuWindowRow opens the window menu on a window row and on its note
// row. Both name the window and offer its own actions.
func TestRailMenuWindowRow(t *testing.T) {
	tui, got := railMenuTUI(t)
	railRightPress(tui, 1)
	if tui.mode != "menu" || tui.menu == nil {
		t.Fatalf("right press on the window row: mode=%q menu=%+v", tui.mode, tui.menu)
	}
	if tui.menu.title != "window one" {
		t.Fatalf("title = %q, want window one", tui.menu.title)
	}
	railWantLabels(t, tui.menu,
		"Rename window", "Close window", "Fold/unfold", "Focus")
	testutil.NoMsg(t, got)

	// The note row belongs to the same window and opens the same menu.
	tui.closeMenu()
	railRightPress(tui, 2)
	if tui.mode != "menu" || tui.menu == nil {
		t.Fatalf("right press on the note row: mode=%q menu=%+v", tui.mode, tui.menu)
	}
	if tui.menu.title != "window one" {
		t.Fatalf("note row title = %q, want window one", tui.menu.title)
	}
	railWantLabels(t, tui.menu,
		"Rename window", "Close window", "Fold/unfold", "Focus")
	testutil.NoMsg(t, got)
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
		{3, "pane", "w1p1", "pi"},
		{4, "window", "w2", "two"},
		{5, "pane", "w1p2", "beta"},
		{7, "workspace", "s2", "work"},
		{8, "window", "w3", "three"},
		{9, "pane", "w2p1", "gamma"},
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
// request to the right action and target. A window row closes its window, a
// pane row closes its pane and a workspace row closes the workspace.
func TestRailMenuCloseTargetsRow(t *testing.T) {
	cases := []struct {
		row    int
		action string
		target string
	}{
		{0, "close-workspace", "s1"},
		{1, "close-window", "w1"},
		{3, "close-pane", "w1p1"},
		{4, "close-window", "w2"},
		{5, "close-pane", "w1p2"},
		{7, "close-workspace", "s2"},
		{8, "close-window", "w3"},
		{9, "close-pane", "w2p1"},
	}
	for _, c := range cases {
		tui, got := railMenuTUI(t)
		railRightPress(tui, c.row)
		railMenuPick(t, tui, "Close")
		m := testutil.NextMsg(t, got)
		if m.Type != "req" || m.Action != c.action || m.Target != c.target {
			t.Fatalf("row %d: sent %+v, want %s %s", c.row, m, c.action, c.target)
		}
	}
}

// TestRailMenuFocusTargetsPane picks Focus on each pane row and holds the
// request to that row's pane.
func TestRailMenuFocusTargetsPane(t *testing.T) {
	cases := []struct {
		row    int
		target string
	}{
		{3, "w1p1"},
		{5, "w1p2"},
		{9, "w2p1"},
	}
	for _, c := range cases {
		tui, got := railMenuTUI(t)
		railRightPress(tui, c.row)
		railMenuPick(t, tui, "Focus")
		m := testutil.NextMsg(t, got)
		if m.Type != "req" || m.Action != "focus" || m.Target != c.target {
			t.Fatalf("row %d: sent %+v, want focus %s", c.row, m, c.target)
		}
	}
}

// TestRailMenuFocusTargetsWindow picks Focus on a window row and holds the
// request to that window's first available pane.
func TestRailMenuFocusTargetsWindow(t *testing.T) {
	cases := []struct {
		row    int
		target string
	}{
		{1, "w1p1"},
		{4, "w1p2"},
		{8, "w2p1"},
	}
	for _, c := range cases {
		tui, got := railMenuTUI(t)
		railRightPress(tui, c.row)
		railMenuPick(t, tui, "Focus")
		m := testutil.NextMsg(t, got)
		if m.Type != "req" || m.Action != "focus" || m.Target != c.target {
			t.Fatalf("row %d: sent %+v, want focus %s", c.row, m, c.target)
		}
	}
}

// TestRailMenuNewWindowTargetsRow picks New window on a workspace row. A
// workspace row sends the workspace id and no cwd, so the server inherits from
// the active pane.
func TestRailMenuNewWindowTargetsRow(t *testing.T) {
	cases := []struct {
		row int
		ws  string
	}{
		{0, "s1"},
		{7, "s2"},
	}
	for _, c := range cases {
		tui, got := railMenuTUI(t)
		railRightPress(tui, c.row)
		railMenuPick(t, tui, "New window")
		m := testutil.NextMsg(t, got)
		if m.Type != "req" || m.Action != "new-window" || m.Target != c.ws || m.Cwd != "" {
			t.Fatalf("row %d: sent %+v, want new-window %s cwd \"\"", c.row, m, c.ws)
		}
	}
}

// TestRailMenuNewWorkspace picks New workspace in the session menu, where the
// rail keeps it now.
func TestRailMenuNewWorkspace(t *testing.T) {
	tui, got := railMenuTUI(t)
	railRightPress(tui, tui.snap.Rows-2)
	railMenuPick(t, tui, "New workspace")
	m := testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != "new-workspace" {
		t.Fatalf("sent %+v, want new-workspace", m)
	}
}

// TestRailMenuControlRowOpensSessionMenu holds the control row to the session
// menu: a right press there opens it, sends nothing, and its release is
// swallowed.
func TestRailMenuControlRowOpensSessionMenu(t *testing.T) {
	tui, got := railMenuTUI(t)
	tui.snap.Session = "dev"
	railRightPress(tui, tui.snap.Rows-2)
	if tui.mode != "menu" || tui.menu == nil {
		t.Fatalf("right press on the control row: mode=%q menu=%+v", tui.mode, tui.menu)
	}
	if tui.menu.title != "session dev" {
		t.Fatalf("title = %q, want session dev", tui.menu.title)
	}
	railWantLabels(t, tui.menu, "New workspace", "Switch session", "Detach")
	testutil.NoMsg(t, got)
	tui.handleMouse(mouseEvent{btn: 2, release: true, x: 3, y: tui.snap.Rows - 1})
	if tui.mode != "menu" {
		t.Fatal("the release of the opening press closed the session menu")
	}
	testutil.NoMsg(t, got)
}

// TestRailLeftPressFocusesPane holds the left press to its pane-row
// behaviour: a pane row focuses its pane.
func TestRailLeftPressFocusesPane(t *testing.T) {
	tui, got := railMenuTUI(t)
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 4})
	m := testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != "focus" || m.Target != "w1p1" {
		t.Fatalf("left press on the pane row sent %+v, want focus w1p1", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 4})
	testutil.NoMsg(t, got)
}

// TestRailLeftPressFocusesWorkspacePane holds the left press on a workspace
// row to focusing the first pane in that workspace. A press on the caret cell
// toggles the workspace's fold instead.
func TestRailLeftPressFocusesWorkspacePane(t *testing.T) {
	tui, got := railMenuTUI(t)
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 1})
	m := testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != "focus" || m.Target != "w1p1" {
		t.Fatalf("left press on the workspace row sent %+v, want focus w1p1", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 1})
	testutil.NoMsg(t, got)

	// A press on the caret cell (zero-based column 0, so mouse x 1) toggles
	// the workspace's fold.
	tui.handleMouse(mouseEvent{btn: 0, x: 1, y: 1})
	m = testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != "toggle-fold" || m.Target != "s1" {
		t.Fatalf("left press on the caret sent %+v, want toggle-fold s1", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 1, y: 1})
	testutil.NoMsg(t, got)
}

// TestRailDoubleClickRenamesPane proves a second left press on the same pane
// row within the double click window opens the pane rename prompt instead of
// focusing again.
func TestRailDoubleClickRenamesPane(t *testing.T) {
	tui, got := railMenuTUI(t)
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 4})
	if m := testutil.NextMsg(t, got); m.Action != "focus" {
		t.Fatalf("first press sent %+v, want focus", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 4})
	testutil.NoMsg(t, got)
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 4})
	testutil.NoMsg(t, got)
	if tui.mode != "rename" {
		t.Fatalf("second press mode = %q, want rename", tui.mode)
	}
	if tui.renameKind != "pane" || tui.renameID != "w1p1" || tui.renameDraft != "pi" {
		t.Fatalf("prompt = kind %q id %q draft %q, want pane w1p1 pi",
			tui.renameKind, tui.renameID, tui.renameDraft)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 4})
	testutil.NoMsg(t, got)
}

// TestRailDoubleClickRenamesWindow proves a second left press on the same
// window row within the double click window opens the window rename prompt
// instead of focusing again.
func TestRailDoubleClickRenamesWindow(t *testing.T) {
	tui, got := railMenuTUI(t)
	// The window caret is zero-based column 2, so the name starts at column 4
	// and the mouse column is 5.
	tui.handleMouse(mouseEvent{btn: 0, x: 5, y: 2})
	if m := testutil.NextMsg(t, got); m.Action != "focus" {
		t.Fatalf("first press sent %+v, want focus", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 5, y: 2})
	testutil.NoMsg(t, got)
	tui.handleMouse(mouseEvent{btn: 0, x: 5, y: 2})
	testutil.NoMsg(t, got)
	if tui.mode != "rename" {
		t.Fatalf("second press mode = %q, want rename", tui.mode)
	}
	if tui.renameKind != "window" || tui.renameID != "w1" || tui.renameDraft != "one" {
		t.Fatalf("prompt = kind %q id %q draft %q, want window w1 one",
			tui.renameKind, tui.renameID, tui.renameDraft)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 5, y: 2})
	testutil.NoMsg(t, got)
}

// TestRailMenuKeyHints holds each item to the main keymap's hint for its
// action. An unbound action shows no key.
func TestRailMenuKeyHints(t *testing.T) {
	cases := []struct {
		row  int
		want map[string]string
	}{
		{0, map[string]string{
			"New window": "c", "Rename workspace": "r",
			"Close workspace": "",
		}},
		{1, map[string]string{
			"Rename window": "", "Close window": "X",
			"Fold/unfold": "", "Focus": "",
		}},
		{3, map[string]string{
			"Focus": "", "Rename pane": "", "Split right": "|",
			"Split down": "-", "Split left": "<", "Split up": "^",
			"Zoom": "z", "Close pane": "x", "Scrollback": "[",
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
