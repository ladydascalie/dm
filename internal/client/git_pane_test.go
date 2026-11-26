package client

// The pane-keyed facts box, driven through the client: a pane's own mark in
// either rail opens the box on that pane, a window mark opens it on the window's
// source pane, and the mark takes the press before the row does.

import (
	"bytes"
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/rail"
	"dm/internal/testutil"
	"dm/internal/vt"
	"dm/internal/wire"
)

// gitPaneSnap is one window with three panes in two repositories: pi is the
// focused pane and the window's source, beta shares pi's repository, and gamma
// sits in a repository of its own with work in the tree. The window line speaks
// for pi's repository, so only gamma's row carries a mark of its own.
func gitPaneSnap(cols, rows, railWidth int) *wire.Snapshot {
	snap := &wire.Snapshot{
		Cols: cols, Rows: rows, Rail: railWidth,
		Root: &layout.Node{Pane: "w1p1"}, Focus: "w1p1",
		WindowLabel: "{{.Name}}",
	}
	pane := func(id, name, status, cwd, root, branch, file string) wire.AllPane {
		return wire.AllPane{
			ID: id, Name: name, Status: status,
			Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one",
			WorkspaceActive: true, WindowActive: true,
			WorkspaceUnfolded: true, WindowUnfolded: true,
			Cwd: cwd, Root: root, Branch: branch, InRepo: true,
			Head: "deadb33f", HeadSubject: "paint the pane",
			Upstream: "origin/" + branch, Files: 1, FileNames: []string{file},
		}
	}
	snap.All = []wire.AllPane{
		pane("w1p1", "pi", "working", "/work/dm", "/work/dm", "dm", "internal/rail/rail.go"),
		pane("w1p2", "beta", "idle", "/work/dm/internal", "/work/dm", "dm", "internal/client/client_git.go"),
		pane("w1p3", "gamma", "idle", "/work/web", "/work/web", "web", "src/app.ts"),
	}
	snap.All[0].Agent, snap.All[0].Focus = true, true
	snap.All[0].Dirty, snap.All[0].Added, snap.All[0].Deleted = true, 12, 3
	// The pane in its own repository has work of its own, so its mark is
	// painted at rest and the box is reachable without a hover.
	snap.All[2].Dirty, snap.All[2].Added, snap.All[2].Deleted, snap.All[2].Ahead = true, 4, 1, 2
	return snap
}

// gitPaneTUI is a client on the two repository fixture with a frame buffer.
func gitPaneTUI(t *testing.T, cols, rows, railWidth int) (*tui, chan wire.Msg) {
	t.Helper()
	tui, got := clientTUI(t, nil)
	tui.snap = gitPaneSnap(cols, rows, railWidth)
	tui.out = &bytes.Buffer{}
	return tui, got
}

// paneMarkClick sends one left click, press and release, on the pane's own git
// mark, found through the hit test so the click follows the painted cell.
func paneMarkClick(t *testing.T, tui *tui, pane string) {
	t.Helper()
	row := rail.RailPaneGitMarkRow(tui.snap, pane, 0)
	if row < 0 {
		t.Fatalf("the fixture paints no mark for pane %q", pane)
	}
	x := -1
	for i := 0; i < tui.snap.Rail; i++ {
		if rail.RailPaneGitMarkAt(tui.snap, i, row, 0) == pane {
			x = i
		}
	}
	if x < 0 {
		t.Fatalf("no cell answers for pane %q on row %d", pane, row)
	}
	tui.handleMouse(mouseEvent{btn: 0, x: x + 1, y: row + 1})
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: x + 1, y: row + 1})
}

// gitFactRow reads one fact row of the open box off a frame the test already
// painted, so a test can say which pane the box describes.
func gitFactRow(term *vt.Term, tui *tui, key string) string {
	rect := tui.git.float.Rect
	for i := 0; i < rect.H; i++ {
		row := gitRowText(term, rect.Y+i, rect.X, rect.W)
		if strings.HasPrefix(row, " "+key) {
			return row
		}
	}
	return ""
}

// TestPaneMarkOpensThatPanesBox proves a pane row's own mark opens the box on
// that pane, not on its window's source pane: the title names the pane's folder
// and every fact is the pane's own. The mark also takes the press before the row
// does, so the click focuses nothing.
func TestPaneMarkOpensThatPanesBox(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	tui, got := gitPaneTUI(t, 100, 16, 30)
	paneMarkClick(t, tui, "w1p3")
	if tui.git == nil || tui.git.pane != "w1p3" {
		t.Fatalf("the mark opened %+v, want the box on w1p3", tui.git)
	}
	if tui.git.window != "" {
		t.Fatalf("the box records window %q, want no window for a pane mark", tui.git.window)
	}
	if len(tui.floats) != 1 || tui.floats[0].ID != gitFloatID {
		t.Fatalf("floats = %+v, want the one facts box", tui.floats)
	}
	// The mark takes precedence over focusing the pane: the press sends nothing.
	testutil.NoMsg(t, got)
	// The box hangs from the pane's row.
	row := rail.RailPaneGitMarkRow(tui.snap, "w1p3", 0)
	if got := tui.git.float.Rect.Y; got != row+1 {
		t.Fatalf("box top = %d, want the pane's row %d", got, row+1)
	}
	// The title names the pane's folder, and the facts are the pane's own.
	it, ok := rail.RailPaneItem(tui.snap, "w1p3")
	if !ok {
		t.Fatal("the snapshot carries no w1p3 row")
	}
	if want := tui.theme().Glyphs.GitMark + " " + it.Folder; !strings.Contains(tui.git.float.Title, want) {
		t.Fatalf("title = %q, want %q", tui.git.float.Title, want)
	}
	frame := gitScreen(tui)
	if row := gitFactRow(frame, tui, "branch"); !strings.Contains(row, "web") || strings.Contains(row, " dm") {
		t.Fatalf("branch row = %q, want the pane's own branch", row)
	}
	if row := gitFactRow(frame, tui, "worktree"); !strings.Contains(row, "(main checkout)") {
		t.Fatalf("worktree row = %q, want the pane's own worktree", row)
	}
	if row := gitFactRow(frame, tui, "upstream"); !strings.Contains(row, "origin/web") {
		t.Fatalf("upstream row = %q, want the pane's own upstream", row)
	}
	// The open mark inverts, so the row says which box is up.
	th := tui.theme()
	x := -1
	for i := 0; i < tui.snap.Rail; i++ {
		if rail.RailPaneGitMarkAt(tui.snap, i, row, 0) == "w1p3" {
			x = i
		}
	}
	c := termCell(frame, x, row)
	if c.FG != vt.RGBColor(uint8(th.Palette.RailBG.R), uint8(th.Palette.RailBG.G), uint8(th.Palette.RailBG.B)) ||
		c.BG != vt.RGBColor(uint8(th.Palette.BarAccent.R), uint8(th.Palette.BarAccent.G), uint8(th.Palette.BarAccent.B)) {
		t.Fatalf("open pane mark = %+v, want the glyph in the rail ground on the accent", c)
	}
	// A second press on the same mark closes it.
	paneMarkClick(t, tui, "w1p3")
	if tui.git != nil || len(tui.floats) != 0 {
		t.Fatalf("second press left git = %+v floats = %+v", tui.git, tui.floats)
	}
}

// TestThinMarkOpensThatPanesBox proves the thin rail's git cell opens the box on
// that pane and not on its window: the thin rail is one row per pane, so the
// cell names the pane it belongs to.
func TestThinMarkOpensThatPanesBox(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	tui, got := gitPaneTUI(t, 100, 16, layout.CollapsedRail)
	paneMarkClick(t, tui, "w1p3")
	if tui.git == nil || tui.git.pane != "w1p3" {
		t.Fatalf("the thin mark opened %+v, want the box on w1p3", tui.git)
	}
	if tui.git.window != "" {
		t.Fatalf("the thin mark opened the window %q, want the pane", tui.git.window)
	}
	testutil.NoMsg(t, got)
	// Every thin row is a pane, so the box is as wide as its facts and stands
	// beside the rail.
	if row := gitFactRow(gitScreen(tui), tui, "branch"); !strings.Contains(row, "web") {
		t.Fatalf("branch row = %q, want the pane's own branch", row)
	}
}

// TestThinMarkOfTheSourcePaneOpensItsOwnBox proves the thin rail marks every pane
// in a repository, the source pane included: that rail draws no window row, so
// no row above speaks for the pane's facts, and the cell names the pane it
// describes.
func TestThinMarkOfTheSourcePaneOpensItsOwnBox(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	tui, got := gitPaneTUI(t, 100, 16, layout.CollapsedRail)
	paneMarkClick(t, tui, "w1p1")
	if tui.git == nil || tui.git.pane != "w1p1" || tui.git.window != "" {
		t.Fatalf("the thin mark opened %+v, want the box on w1p1 alone", tui.git)
	}
	testutil.NoMsg(t, got)
	if row := gitFactRow(gitScreen(tui), tui, "branch"); !strings.Contains(row, "dm") || strings.Contains(row, "web") {
		t.Fatalf("branch row = %q, want w1p1's own branch", row)
	}
}

// TestWindowMarkOpensTheSourcePanesBox proves a window mark still opens the box
// on the window's source pane, which is the pane the window line reads, and that
// it hangs from the window's row rather than a pane's.
func TestWindowMarkOpensTheSourcePanesBox(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	tui, _ := gitPaneTUI(t, 100, 16, 30)
	row := rail.RailGitMarkRow(tui.snap, "w1", 0)
	if row < 0 {
		t.Fatal("the fixture paints no window mark")
	}
	gitMarkClick(tui, row)
	if tui.git == nil || tui.git.pane != "w1p1" {
		t.Fatalf("the window mark opened %+v, want the source pane w1p1", tui.git)
	}
	if tui.git.window != "w1" {
		t.Fatalf("the box records window %q, want w1", tui.git.window)
	}
	if got := tui.git.float.Rect.Y; got != row+1 {
		t.Fatalf("box top = %d, want the window's row %d", got, row+1)
	}
	if row := gitFactRow(gitScreen(tui), tui, "branch"); !strings.Contains(row, "dm") {
		t.Fatalf("branch row = %q, want the source pane's branch", row)
	}
}

// TestPaneMarkLeavesTheCommonWindowAlone proves a window whose panes share one
// repository paints no pane mark: the window line already says the repository,
// so the common case costs no cell and draws exactly what it always did.
func TestPaneMarkLeavesTheCommonWindowAlone(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := gitPaneSnap(100, 16, 30)
	// Put gamma in the window's own repository.
	snap.All[2].Root, snap.All[2].Cwd = "/work/dm", "/work/dm/web"
	for _, it := range rail.RailItems(snap) {
		if it.Kind == rail.Pane && it.PaneMark {
			t.Fatalf("pane %q carries a mark in a one repository window: %+v", it.Pane, it)
		}
	}
	for y := 0; y < snap.Rows; y++ {
		for x := 0; x < snap.Rail; x++ {
			if rail.RailPaneGitMarkAt(snap, x, y, 0) != "" {
				t.Fatalf("cell %d,%d answers a pane mark in a one repository window", x, y)
			}
		}
	}
}
