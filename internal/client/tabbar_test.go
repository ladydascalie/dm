package client

import (
	"bytes"
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/wire"
)

func testTabWidth() tabWidth {
	return tabWidthFor(builtinKeymap())
}

func TestTabBarRect(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 20, Rail: 26}
	bar := tabBarRect(snap)
	want := layout.Rect{X: 26, Y: 0, W: 74, H: layout.TabBarRows}
	if bar != want {
		t.Fatalf("tab bar rect = %+v, want %+v", bar, want)
	}
}

func TestTabBarRectShortScreen(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 2, Rail: 26}
	if got := tabBarRect(snap); got != (layout.Rect{}) {
		t.Fatalf("short screen tab bar rect = %+v, want empty", got)
	}
}

func TestTabBarItems(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 20, Rail: 26, Windows: []string{"one", "two"}}
	items := tabBarItems(snap, testTabWidth())
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].Name != "one" || items[0].Index != 0 {
		t.Fatalf("first item = %+v", items[0])
	}
	if items[1].Name != "two" || items[1].Index != 1 {
		t.Fatalf("second item = %+v", items[1])
	}
}

func TestTabBarItemsDropsOverflow(t *testing.T) {
	// A tab bar too narrow for both names keeps only the first tab.
	snap := &wire.Snapshot{Cols: 30, Rows: 20, Rail: 26, Windows: []string{"long-one", "two"}}
	items := tabBarItems(snap, testTabWidth())
	if len(items) != 0 {
		t.Fatalf("items = %d, want 0 when the first tab does not fit", len(items))
	}
}

func TestTabBarAt(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 20, Rail: 26, Windows: []string{"one", "two"}}
	it, ok := tabBarAt(snap, 26, 0, testTabWidth())
	if !ok || it.Name != "one" {
		t.Fatalf("tab bar hit = %+v ok=%v", it, ok)
	}
	if _, ok := tabBarAt(snap, 10, 0, testTabWidth()); ok {
		t.Fatal("rail cell hit a tab")
	}
	if _, ok := tabBarAt(snap, 26, 1, testTabWidth()); ok {
		t.Fatal("pane row hit a tab")
	}
}

func TestTabBarClickSendsSelectWindow(t *testing.T) {
	tr, got := clientTUI(t, nil)
	tr.snap.Windows = []string{"one", "two"}
	tr.snap.ActiveWindow = 0
	tr.snap.Rail = 0
	tr.snap.Cols = 100
	tr.snap.Rows = 20

	tr.handleMouse(mouseEvent{btn: 0, x: 1, y: 1})
	m := testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != "select-window" || m.Index != 0 {
		t.Fatalf("sent %+v", m)
	}
}

func TestTabBarDoubleClickOpensRename(t *testing.T) {
	tr, got := clientTUI(t, nil)
	tr.snap.Windows = []string{"one", "two"}
	tr.snap.ActiveWindow = 0
	tr.snap.Rail = 0
	tr.snap.Cols = 100
	tr.snap.Rows = 20
	tr.snap.Workspace = "s1"
	tr.snap.All = []wire.AllPane{
		{ID: "a", Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one", Focus: true},
	}

	tr.handleMouse(mouseEvent{btn: 0, x: 1, y: 1})
	_ = testutil.NextMsg(t, got)

	tr.handleMouse(mouseEvent{btn: 0, x: 1, y: 1})
	if tr.mode != "rename" || tr.renameKind != "window" || tr.renameID != "w1" {
		t.Fatalf("double click did not open window rename: mode=%q kind=%q id=%q",
			tr.mode, tr.renameKind, tr.renameID)
	}
	testutil.NoMsg(t, got)
}

// TestTabBarActiveItem pins the active tab: the tab whose index matches the
// snapshot's active window is the one the painter highlights.
func TestTabBarActiveItem(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 20, Rail: 26, Windows: []string{"one", "two"}, ActiveWindow: 1}
	items := tabBarItems(snap, testTabWidth())
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[1].Index != 1 {
		t.Fatalf("second item index = %d, want 1", items[1].Index)
	}
	if items[0].Index != 0 {
		t.Fatalf("first item index = %d, want 0", items[0].Index)
	}
}

// TestDrawTabBarHighlightsActive proves the painter shows every window name and
// paints the active tab on the focus background.
func TestDrawTabBarHighlightsActive(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 20, Rail: 26, Windows: []string{"one", "two"}, ActiveWindow: 1}
	var buf bytes.Buffer
	DrawTabBar(&buf, theme.DefaultTheme(), snap, testTabWidth())
	out := buf.String()
	if !strings.Contains(out, " one ") || !strings.Contains(out, " two ") {
		t.Fatalf("tab names missing: %q", out)
	}
	focusBG := theme.DefaultTheme().Palette.RailFocusBG.BG()
	if !strings.Contains(out, focusBG) {
		t.Fatalf("active tab background missing: %q", out)
	}
}

// TestTabBarRowSitsAbovePaneArea proves the tab bar row and the pane area do not
// overlap: the bar is row 0 and the pane boxes start on row 1.
func TestTabBarRowSitsAbovePaneArea(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 20, Rail: 26}
	bar := tabBarRect(snap)
	area := layout.PaneAreaForWithTabBar(snap.Cols, snap.Rows, snap.Rail)
	if bar.Y+bar.H != area.Y {
		t.Fatalf("tab bar %+v does not end where pane area %+v starts", bar, area)
	}
	if area.Y != layout.TabBarRows {
		t.Fatalf("pane area starts at row %d, want %d", area.Y, layout.TabBarRows)
	}
}

// TestTabCellsClamps proves a tab is its name plus padding, clamped to the
// configured bounds.
func TestTabCellsClamps(t *testing.T) {
	tw := tabWidth{Min: 8, Max: 10}
	cases := []struct {
		name string
		want int
	}{
		{"a", 8},
		{"abc", 8},
		{"abcdef", 8},
		{"abcdefg", 9},
		{"abcdefgh", 10},
		{"averylongwindowname", 10},
	}
	for _, c := range cases {
		if got := tabCells(c.name, tw); got != c.want {
			t.Fatalf("tabCells(%q) = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestTabBarItemsClampedWidth proves every tab takes the clamped width, so the
// bar has a known size.
func TestTabBarItemsClampedWidth(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 20, Rail: 0, Windows: []string{"a", "abcdefgh", "averylongwindowname"}}
	items := tabBarItems(snap, tabWidth{Min: 8, Max: 10})
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	want := []int{8, 10, 10}
	for i, it := range items {
		if it.Rect.W != want[i] {
			t.Fatalf("tab %d width = %d, want %d", i, it.Rect.W, want[i])
		}
	}
}

// TestDrawTabBarElidesLongName proves a name wider than the tab is cut with an
// ellipsis and the tab keeps its width.
func TestDrawTabBarElidesLongName(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 20, Rail: 0, Windows: []string{"averylongwindowname"}}
	var buf bytes.Buffer
	DrawTabBar(&buf, theme.DefaultTheme(), snap, tabWidth{Min: 8, Max: 10})
	out := buf.String()
	if !strings.Contains(out, "averylo…") {
		t.Fatalf("long name not elided: %q", out)
	}
	if strings.Contains(out, "averylongwindowname") {
		t.Fatalf("long name painted whole: %q", out)
	}
}
