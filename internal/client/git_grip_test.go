package client

// The facts box's grip, its drag, the size the prefs remember and the changed
// names a taller box lists, all driven through the client.

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dm/internal/layout"
	"dm/internal/rail"
	"dm/internal/segment"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

// gitBoxTUI opens the box on w1 and returns it with the box's grip cell, zero
// based.
func gitBoxTUI(t *testing.T, cols, rows, railWidth int) (*tui, int, int) {
	t.Helper()
	tui, _ := gitFloatTUI(t, cols, rows, railWidth)
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w1", 0))
	if tui.git == nil {
		t.Fatal("the mark click opened no box")
	}
	r := tui.git.float.Rect
	return tui, r.X + r.W, r.Y + r.H
}

// gitPressAt sends one left press at the zero based cell x,y.
func gitPressAt(tui *tui, x, y int) {
	tui.handleMouse(mouseEvent{btn: 0, x: x + 1, y: y + 1})
}

// gitReleaseAt sends one left release at the zero based cell x,y.
func gitReleaseAt(tui *tui, x, y int) {
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: x + 1, y: y + 1})
}

// gitMoveAt sends one held-button motion at the zero based cell x,y, after
// clearing the drag's clock: the drag applies at most once every dragEvery, and
// a test drives several positions without waiting it out.
func gitMoveAt(tui *tui, x, y int) {
	if tui.git != nil {
		tui.git.at = time.Time{}
	}
	tui.handleMouse(mouseEvent{btn: 32, x: x + 1, y: y + 1})
}

// TestGitGripPressStartsTheDrag proves the grip is the only handle: a press on
// it starts a resize and sends nothing, and a press one cell away starts no
// drag, keeps the box up and never reaches the pane or the rail under it.
func TestGitGripPressStartsTheDrag(t *testing.T) {
	t.Parallel()
	tui, got := gitFloatTUI(t, 100, 16, 20)
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w1", 0))
	r := tui.git.float.Rect
	gx, gy := r.X+r.W, r.Y+r.H
	gitPressAt(tui, gx, gy)
	if !tui.git.resizing {
		t.Fatal("the press on the grip started no resize")
	}
	if tui.git.moved {
		t.Fatal("the press alone counted as movement")
	}
	if tui.padAxis != gitPadAxis {
		t.Fatalf("the grip hover axis = %q, want %q", tui.padAxis, gitPadAxis)
	}
	testutil.NoMsg(t, got)

	// One cell away, on the box's own bottom border: no drag, the box stays,
	// and the press goes no further.
	other, otherGot := gitFloatTUI(t, 100, 16, 20)
	gitMarkClick(other, rail.RailGitMarkRow(other.snap, "w1", 0))
	or := other.git.float.Rect
	gitPressAt(other, or.X+or.W-1, or.Y+or.H)
	if other.git == nil {
		t.Fatal("a press on the box's border closed it")
	}
	if other.git.resizing || other.press != nil || other.selText != nil {
		t.Fatalf("a press beside the grip started %+v press=%+v selection=%+v",
			other.git, other.press, other.selText)
	}
	testutil.NoMsg(t, otherGot)
}

// TestGitGripPressOnTheBoxDoesNothing proves the box owns the cells it covers: a
// left press on its body keeps it up, starts no selection and sends nothing, so
// a click on the box never lands on the pane underneath.
func TestGitGripPressOnTheBoxDoesNothing(t *testing.T) {
	t.Parallel()
	tui, got := gitFloatTUI(t, 100, 16, 20)
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w1", 0))
	r := tui.git.float.Rect
	gitPressAt(tui, r.X+3, r.Y+2)
	if tui.git == nil {
		t.Fatal("a press on the box body closed it")
	}
	if tui.press != nil || tui.selText != nil || tui.git.resizing {
		t.Fatalf("the press started press=%+v selection=%+v resize=%v",
			tui.press, tui.selText, tui.git.resizing)
	}
	testutil.NoMsg(t, got)
	// A right press there is the box's too: no menu opens through it.
	tui.handleMouse(mouseEvent{btn: 2, x: r.X + 3, y: r.Y + 2})
	if tui.mode == "menu" || tui.menu != nil {
		t.Fatalf("a right press on the box opened %q", tui.mode)
	}
	testutil.NoMsg(t, got)
}

// TestGitGripDragResizesLive proves a drag sizes the box as it moves: the right
// edge follows the pointer's column and the bottom edge its row, the frame is
// local so nothing is sent, a second motion inside the drag clock is held back,
// and the release forces the last size through.
func TestGitGripDragResizesLive(t *testing.T) {
	t.Parallel()
	tui, got := gitFloatTUI(t, 100, 16, 20)
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w1", 0))
	r := tui.git.float.Rect
	gx, gy := r.X+r.W, r.Y+r.H
	gitPressAt(tui, gx, gy)
	gitMoveAt(tui, gx+10, gy+3)
	if want := (layout.Rect{X: r.X, Y: r.Y, W: r.W + 10, H: r.H + 3}); tui.git.float.Rect != want {
		t.Fatalf("dragged box = %+v, want %+v", tui.git.float.Rect, want)
	}
	if tui.prefs.GitBoxCols != r.W+12 || tui.prefs.GitBoxRows != r.H+3 {
		t.Fatalf("remembered size = %d,%d, want %d,%d",
			tui.prefs.GitBoxCols, tui.prefs.GitBoxRows, r.W+12, r.H+3)
	}
	testutil.NoMsg(t, got)

	// A second motion inside the drag clock is held back, and the release forces
	// its cell through, so a fast drag never lands short.
	sized := tui.git.float.Rect
	tui.git.at = time.Now()
	tui.handleMouse(mouseEvent{btn: 32, x: gx + 12, y: gy + 4})
	if tui.git.float.Rect != sized {
		t.Fatalf("a motion inside the drag clock moved the box to %+v", tui.git.float.Rect)
	}
	gitReleaseAt(tui, gx+12, gy+4)
	if tui.git.resizing {
		t.Fatal("the release left the drag in flight")
	}
	if tui.git.float.Rect.W != r.W+12 || tui.git.float.Rect.H != r.H+4 {
		t.Fatalf("box after the release = %+v, want the release cell", tui.git.float.Rect)
	}
	testutil.NoMsg(t, got)
}

// TestGitGripClamps proves a drag stays inside its bounds: never narrower than
// the floor, never past the room the rail leaves, never above the facts height,
// and its bottom border never reaches the status bar at any dragged height.
func TestGitGripClamps(t *testing.T) {
	t.Parallel()
	tui, _ := gitFloatTUI(t, 100, 16, 20)
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w1", 0))
	r := tui.git.float.Rect
	gx, gy := r.X+r.W, r.Y+r.H
	gitPressAt(tui, gx, gy)

	// The narrow end: the pointer left of the rail still leaves the floor.
	gitMoveAt(tui, 0, gy)
	if got := tui.git.float.Rect.W + 2; got != gitMinCols {
		t.Fatalf("narrow drag width = %d, want the floor %d", got, gitMinCols)
	}
	// The wide end: the right border lands on the terminal's last column.
	gitMoveAt(tui, tui.snap.Cols+40, gy)
	if got := tui.git.float.Rect.X + tui.git.float.Rect.W; got != tui.snap.Cols-1 {
		t.Fatalf("wide drag right border = %d, want %d", got, tui.snap.Cols-1)
	}
	// Every dragged height keeps the bottom border off the status bar and at
	// least the facts rows.
	for y := 0; y < tui.snap.Rows; y++ {
		gitMoveAt(tui, gx+2, y)
		got := tui.git.float.Rect
		if got.Y+got.H > tui.snap.Rows-2 {
			t.Fatalf("drag to row %d put the bottom border on %d, past the status bar",
				y, got.Y+got.H)
		}
		if got.Y < 1 {
			t.Fatalf("drag to row %d put the top border on %d", y, got.Y-1)
		}
		if got.H < gitRows {
			t.Fatalf("drag to row %d left %d fact rows, want %d", y, got.H, gitRows)
		}
	}
	gitReleaseAt(tui, gx+2, tui.snap.Rows-1)
}

// TestGitGripTapResetsSize proves the movement threshold in both directions: a
// press and release on the grip cell with no movement puts the box back to its
// automatic size, and one cell of movement is a drag whose size stands.
func TestGitGripTapResetsSize(t *testing.T) {
	t.Parallel()
	tui, _ := gitFloatTUI(t, 100, 16, 20)
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w1", 0))
	auto := tui.git.float.Rect
	gx, gy := auto.X+auto.W, auto.Y+auto.H

	// Drag it bigger and let go: the size is the drag's.
	gitPressAt(tui, gx, gy)
	gitMoveAt(tui, gx+8, gy+2)
	gitReleaseAt(tui, gx+8, gy+2)
	sized := tui.git.float.Rect
	if sized.W <= auto.W || sized.H <= auto.H {
		t.Fatalf("dragged box = %+v, want it past the automatic %+v", sized, auto)
	}
	if tui.prefs.GitBoxCols == 0 || tui.prefs.GitBoxRows == 0 {
		t.Fatalf("a drag remembered no size: %d,%d",
			tui.prefs.GitBoxCols, tui.prefs.GitBoxRows)
	}

	// A tap on the grip, with no movement at all, gives the hugging box back.
	gitPressAt(tui, sized.X+sized.W, sized.Y+sized.H)
	gitReleaseAt(tui, sized.X+sized.W, sized.Y+sized.H)
	if got := tui.git.float.Rect; got != auto {
		t.Fatalf("box after the tap = %+v, want the automatic %+v", got, auto)
	}
	if tui.prefs.GitBoxCols != 0 || tui.prefs.GitBoxRows != 0 {
		t.Fatalf("the tap remembered %d,%d, want automatic",
			tui.prefs.GitBoxCols, tui.prefs.GitBoxRows)
	}

	// One cell of movement is a drag: the size it reaches is kept.
	gitPressAt(tui, auto.X+auto.W, auto.Y+auto.H)
	gitMoveAt(tui, auto.X+auto.W+1, auto.Y+auto.H)
	gitReleaseAt(tui, auto.X+auto.W+1, auto.Y+auto.H)
	if got := tui.git.float.Rect.W; got != auto.W+1 {
		t.Fatalf("one cell of drag left width %d, want %d", got, auto.W+1)
	}
	if tui.prefs.GitBoxCols == 0 {
		t.Fatal("a one cell drag remembered automatic, want the size it reached")
	}
}

// TestGitGripSizeSurvivesReload proves the size crosses a start: a drag writes
// it through the prefs save, a second client reads it back and opens the same
// box, and a value no drag could have made falls back to automatic.
func TestGitGripSizeSurvivesReload(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "prefs.json")
	tui, _ := gitFloatTUI(t, 100, 16, 20)
	tui.prefsPath = path
	tui.prefs = loadPrefs(path)
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w1", 0))
	r := tui.git.float.Rect
	gitPressAt(tui, r.X+r.W, r.Y+r.H)
	gitMoveAt(tui, r.X+r.W+6, r.Y+r.H+2)
	gitReleaseAt(tui, r.X+r.W+6, r.Y+r.H+2)
	dragged := tui.git.float.Rect
	if got := loadPrefs(path); got.GitBoxCols != dragged.W+2 || got.GitBoxRows != dragged.H {
		t.Fatalf("saved size = %d,%d, want %d,%d",
			got.GitBoxCols, got.GitBoxRows, dragged.W+2, dragged.H)
	}

	// The next run reads the size and opens the box the drag left.
	next, _ := gitFloatTUI(t, 100, 16, 20)
	next.prefsPath = path
	next.prefs = loadPrefs(path)
	gitMarkClick(next, rail.RailGitMarkRow(next.snap, "w1", 0))
	if next.git == nil {
		t.Fatal("the reloaded size opened no box")
	}
	if got := next.git.float.Rect; got.W != dragged.W || got.H != dragged.H {
		t.Fatalf("box after the reload = %+v, want the dragged %+v", got, dragged)
	}

	// A tap on the grip writes the automatic size back, so the next run hugs
	// the facts again.
	nr := next.git.float.Rect
	gitPressAt(next, nr.X+nr.W, nr.Y+nr.H)
	gitReleaseAt(next, nr.X+nr.W, nr.Y+nr.H)
	if got := loadPrefs(path); got.GitBoxCols != 0 || got.GitBoxRows != 0 {
		t.Fatalf("a reset saved %d,%d, want automatic", got.GitBoxCols, got.GitBoxRows)
	}

	// A value no drag could have made is automatic, and it never toasts.
	if err := savePrefs(path, prefs{GitBoxCols: -4, GitBoxRows: 40}); err != nil {
		t.Fatal(err)
	}
	bad, got := gitFloatTUI(t, 100, 16, 20)
	bad.prefsPath = path
	bad.prefs = loadPrefs(path)
	gitMarkClick(bad, rail.RailGitMarkRow(bad.snap, "w1", 0))
	auto, _ := gitFloatTUI(t, 100, 16, 20)
	gitMarkClick(auto, rail.RailGitMarkRow(auto.snap, "w1", 0))
	if a, b := bad.git.float.Rect, auto.git.float.Rect; a != b {
		t.Fatalf("an absurd saved size opened %+v, want the automatic %+v", a, b)
	}
	testutil.NoMsg(t, got)
}

// TestGitFloatListsTheFiles proves a taller box lists the changed names the rail
// row carries, under a rule row and in the rail's dim, one name a row.
func TestGitFloatListsTheFiles(t *testing.T) {
	t.Parallel()
	tui, _ := gitFloatTUI(t, 100, 20, 20)
	tui.prefs = prefs{GitBoxCols: 40, GitBoxRows: 12}
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w1", 0))
	// The box reads the window's source pane, so its names come from that pane.
	it, ok := rail.RailPaneItem(tui.snap, "w1p1")
	if !ok || len(it.FileNames) == 0 {
		t.Fatalf("the fixture carries no changed names: %+v", it.FileNames)
	}
	r := tui.git.float.Rect
	if r.H != 12 {
		t.Fatalf("box height = %d, want the remembered 12", r.H)
	}
	th := theme.DefaultTheme()
	dim := vt.RGBColor(uint8(th.Palette.RailDim.R), uint8(th.Palette.RailDim.G), uint8(th.Palette.RailDim.B))
	term := gitScreen(tui)
	rule := gitRowText(term, r.Y+gitRows, r.X, r.W)
	if want := strings.Repeat(th.Glyphs.DividerRow, r.W); rule != want {
		t.Fatalf("rule row = %q, want a full rule %q", rule, want)
	}
	for i, name := range it.FileNames {
		row := gitRowText(term, r.Y+gitRows+1+i, r.X, r.W)
		if !strings.Contains(row, name) {
			t.Fatalf("name row %d = %q, want %q", i, row, name)
		}
		for x := r.X; x < r.X+r.W; x++ {
			if c := termCell(term, x, r.Y+gitRows+1+i); c.Char == rune(name[0]) {
				if c.FG != dim {
					t.Fatalf("name %q painted in %+v, want the rail dim", name, c.FG)
				}
				break
			}
		}
	}
	// Four names in five rows list whole: no count of missing ones.
	last := gitRowText(term, r.Y+gitRows+len(it.FileNames)+1, r.X, r.W)
	if strings.TrimSpace(last) != "" {
		t.Fatalf("row after the names = %q, want nothing", last)
	}
}

// TestGitFloatFileListElidesAndCounts proves the file rows keep one row a name:
// a long path elides with the set's mark, and a list the box cannot show whole
// ends with the count it left out, counted from every changed file and not only
// from the names the probe listed.
func TestGitFloatFileListElidesAndCounts(t *testing.T) {
	t.Parallel()
	tui, _ := gitFloatTUI(t, 100, 20, 20)
	long := "internal/client/some/deeply/nested/place/that/never/ends/client_git.go"
	tui.snap.All[0].FileNames = []string{long, "b.go", "c.go", "d.go"}
	tui.snap.All[0].Files = 20
	// Nine rows is the facts, the rule, one name and the count.
	tui.prefs = prefs{GitBoxCols: 40, GitBoxRows: gitRows + 3}
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w1", 0))
	r := tui.git.float.Rect
	if r.H != gitRows+3 {
		t.Fatalf("box height = %d, want %d", r.H, gitRows+3)
	}
	term := gitScreen(tui)
	name := gitRowText(term, r.Y+gitRows+1, r.X, r.W)
	if !strings.Contains(name, "\u2026") {
		t.Fatalf("long name row = %q, want the set's elision mark", name)
	}
	if strings.Contains(name, "client_git.go") {
		t.Fatalf("long name row = %q, want the path cut short", name)
	}
	last := gitRowText(term, r.Y+gitRows+2, r.X, r.W)
	if !strings.Contains(last, "19 more files") {
		t.Fatalf("last file row = %q, want the count left out", last)
	}
}

// TestGitFloatTallBoxWithNothingToList proves a taller box on a row that carries
// no names keeps the facts height instead of growing empty.
func TestGitFloatTallBoxWithNothingToList(t *testing.T) {
	t.Parallel()
	tui, _ := gitFloatTUI(t, 100, 20, 20)
	tui.prefs = prefs{GitBoxCols: 40, GitBoxRows: 14}
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w2", 0))
	if tui.git == nil {
		t.Fatal("the mark click opened no box")
	}
	if got := tui.git.float.Rect.H; got != gitRows {
		t.Fatalf("box height on a row with no names = %d, want the facts height %d",
			got, gitRows)
	}
}

// TestGitFloatGripPaintAndHint proves the grip is the corner cell: the theme's
// grip glyph in the rail's dim, the rail accent under the pointer, the status
// bar saying what it does, and no divider painted through the box.
func TestGitFloatGripPaintAndHint(t *testing.T) {
	t.Parallel()
	tui, _ := gitFloatTUI(t, 100, 16, 20)
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w1", 0))
	th := theme.DefaultTheme()
	dim := vt.RGBColor(uint8(th.Palette.RailDim.R), uint8(th.Palette.RailDim.G), uint8(th.Palette.RailDim.B))
	accent := vt.RGBColor(uint8(th.Palette.BarAccent.R), uint8(th.Palette.BarAccent.G), uint8(th.Palette.BarAccent.B))
	r := tui.git.float.Rect
	gx, gy := r.X+r.W, r.Y+r.H
	if c := termCell(gitScreen(tui), gx, gy); c.Char != []rune(th.Glyphs.Grip)[0] || c.FG != dim {
		t.Fatalf("grip at rest = %+v, want %q in the rail dim", c, th.Glyphs.Grip)
	}

	// The pointer rests on it: the grip takes the accent and the bar says what
	// the grip does.
	tui.handleMouse(mouseEvent{btn: 32, x: gx + 1, y: gy + 1})
	term := gitScreen(tui)
	if c := termCell(term, gx, gy); c.Char != []rune(th.Glyphs.Grip)[0] || c.FG != accent {
		t.Fatalf("hovered grip = %+v, want %q in the rail accent", c, th.Glyphs.Grip)
	}
	if got := tui.barInfo(); got != "drag to resize" {
		t.Fatalf("bar info = %q, want the resize hint", got)
	}
	if bar := gitRowText(term, tui.snap.Rows-1, 0, tui.snap.Cols); !strings.Contains(bar, "drag to resize") {
		t.Fatalf("status bar = %q, want the resize hint", bar)
	}
	// No divider stands in the box: the hint comes without a rule painted
	// through it.
	for y := r.Y - 1; y <= r.Y+r.H; y++ {
		for x := r.X - 1; x <= r.X+r.W; x++ {
			c := termCell(term, x, y)
			if c.Char == []rune(th.Glyphs.DividerRow)[0] || c.Char == []rune(th.Glyphs.DividerCol)[0] {
				t.Fatalf("divider %q painted at %d,%d through the box", c.Char, x, y)
			}
		}
	}
}

// gitWheelProbe is the review's probe shape: a pane under the box with a real
// rect that claims the mouse, so a wheel it receives comes back as keys.
func gitWheelProbe(t *testing.T) (*tui, chan wire.Msg) {
	t.Helper()
	tui, got := gitFloatTUI(t, 100, 30, 20)
	tui.snap.Panes[0].Rect = layout.Rect{X: 20, Y: 0, W: 79, H: 28}
	tui.snap.Panes[0].Mouse = true
	return tui, got
}

// TestGitBoxLetsTheWheelThrough proves the box owns the press and the grip, and
// not the wheel: a wheel turn on a box cell reaches the pane under it, a wheel
// turn outside the box still does, and neither closes the box, while a press on
// the box stays inert and the grip still starts a drag.
func TestGitBoxLetsTheWheelThrough(t *testing.T) {
	t.Parallel()
	tui, got := gitWheelProbe(t)
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w1", 0))
	if tui.git == nil {
		t.Fatal("the mark click opened no box")
	}
	r := tui.git.float.Rect

	// A wheel turn on a box cell goes through to the pane.
	tui.handleMouse(mouseEvent{btn: 64, x: r.X + 3, y: r.Y + 2})
	if m := testutil.NextMsg(t, got); m.Type != "keys" || len(m.Keys) == 0 {
		t.Fatalf("wheel over the box sent %+v, want the keys of the pane under it", m)
	}
	if tui.git == nil {
		t.Fatal("a wheel over the box closed it")
	}

	// A wheel turn outside the box still reaches the pane.
	tui.handleMouse(mouseEvent{btn: 64, x: r.X + r.W + 6, y: r.Y + 2})
	if m := testutil.NextMsg(t, got); m.Type != "keys" || len(m.Keys) == 0 {
		t.Fatalf("wheel outside the box sent %+v, want the keys of the pane", m)
	}

	// A press on the box stays inert: the box stays up and nothing is sent.
	gitPressAt(tui, r.X+3, r.Y+2)
	if tui.git == nil {
		t.Fatal("a press on the box body closed it")
	}
	if tui.press != nil || tui.selText != nil || tui.git.resizing {
		t.Fatalf("a press on the box started press=%+v selection=%+v resize=%v",
			tui.press, tui.selText, tui.git.resizing)
	}
	gitReleaseAt(tui, r.X+3, r.Y+2)
	testutil.NoMsg(t, got)

	// The grip still starts a drag.
	gitPressAt(tui, r.X+r.W, r.Y+r.H)
	if !tui.git.resizing {
		t.Fatal("the press on the grip started no resize")
	}
	testutil.NoMsg(t, got)
}

// TestGitBoxPaintsOnItsGround proves the box has no black holes: every interior
// cell, the file rows and the rule included, sits on the panel's ground, and the
// grip cell does too. segment.Write always writes a background, so a run that
// never sets one paints true black through the panel's fill.
func TestGitBoxPaintsOnItsGround(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	ground := vt.RGBColor(uint8(th.Palette.RailBG.R), uint8(th.Palette.RailBG.G), uint8(th.Palette.RailBG.B))
	tui, _, _ := gitBoxTUI(t, 100, 20, 20)
	// A tall box, so the rule row and the names are painted as well.
	tui.setGitSize(gitMinCols, 14)
	tui.syncGit()
	r := tui.git.float.Rect
	term := gitScreen(tui)
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			if got := termCell(term, x, y).BG; got != ground {
				t.Fatalf("cell %d,%d bg = %v, want the box ground %v", x, y, got, ground)
			}
		}
	}
	// The grip owns the bottom right corner of the border.
	if got := termCell(term, r.X+r.W, r.Y+r.H).BG; got != ground {
		t.Fatalf("grip cell bg = %v, want the box ground %v", got, ground)
	}
}

// TestGitBoxColourHierarchy proves the box reads in layers instead of one weight:
// the sha recedes under its subject, the diff counts carry the diff colours, the
// file count and a clean state stay quiet, a dirty state is the rail's amber, and
// an arrow is coloured only when it carries commits.
func TestGitBoxColourHierarchy(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	p := th.Palette
	snap := gitFloatSnap(100, 20, 20)
	byKey := func(pane string) map[string][]segment.Seg {
		t.Helper()
		it, ok := rail.RailPaneItem(snap, pane)
		if !ok {
			t.Fatalf("no rail row for pane %s", pane)
		}
		out := map[string][]segment.Seg{}
		for _, f := range gitFacts(th, it) {
			out[f.key] = f.value
		}
		return out
	}
	fgOf := func(segs []segment.Seg, want string) theme.RGB {
		t.Helper()
		for _, s := range segs {
			if s.Text == want {
				return s.FG
			}
		}
		t.Fatalf("no run %q in %+v", want, segs)
		return theme.RGB{}
	}

	dirty := byKey("w1p1")
	if got := fgOf(dirty["head"], "deadb33f"); got != p.RailDim {
		t.Errorf("sha = %v, want the dim tone", got)
	}
	if got := fgOf(dirty["head"], "fix rail elision"); got != p.RailText {
		t.Errorf("subject = %v, want the text tone", got)
	}
	if got := fgOf(dirty["changes"], "+12"); got != p.RailDiffAdd {
		t.Errorf("+12 = %v, want the add tone", got)
	}
	if got := fgOf(dirty["changes"], "-3"); got != p.RailDiffDel {
		t.Errorf("-3 = %v, want the del tone", got)
	}
	if got := fgOf(dirty["changes"], "4 files"); got != p.RailDim {
		t.Errorf("file count = %v, want the dim tone", got)
	}
	if got := fgOf(dirty["state"], "dirty"); got != p.RailWorking {
		t.Errorf("dirty = %v, want the working tone", got)
	}
	if got := fgOf(dirty["upstream"], "\u21912"); got != p.RailDiffAdd {
		t.Errorf("ahead 2 = %v, want the add tone", got)
	}
	if got := fgOf(dirty["upstream"], "\u21930"); got != p.RailDim {
		t.Errorf("behind 0 = %v, want the dim tone", got)
	}

	clean := byKey("w2p1")
	if got := fgOf(clean["state"], "clean"); got != p.RailDim {
		t.Errorf("clean = %v, want the dim tone", got)
	}
	if got := fgOf(clean["changes"], "none"); got != p.RailDim {
		t.Errorf("changes none = %v, want the dim tone", got)
	}
	if got := fgOf(clean["upstream"], "(none)"); got != p.RailDim {
		t.Errorf("upstream none = %v, want the dim tone", got)
	}
}

// TestGroundGitSegs proves the ground pass covers every run, including one an
// elision produced.
func TestGroundGitSegs(t *testing.T) {
	th := theme.DefaultTheme()
	segs := groundGitSegs(th, []segment.Seg{
		{Text: "a", FG: th.Palette.RailText},
		{Text: "b", FG: th.Palette.RailDim, BG: th.Palette.RailFocusBG},
	})
	for i, s := range segs {
		if s.BG != th.Palette.RailBG {
			t.Fatalf("run %d bg = %v, want the box ground", i, s.BG)
		}
	}
}
