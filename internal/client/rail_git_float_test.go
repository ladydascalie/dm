package client

// The git facts float, driven through the client: the mark's click, its move,
// its three ways out, the anchor it follows, and the facts it paints from the
// snapshot.

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"dm/internal/cells"
	"dm/internal/layout"
	"dm/internal/rail"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

// gitFloatSnap is two workspaces with one window each, both inside a linked
// worktree, so two marks stand on one rail. w1 is dirty and tracks a ref; w2 is
// clean and tracks nothing, which is the (none) case. The mark rows are 1 and
// 7: workspace, window and three panes, a gap, workspace, window and three
// panes. The list runs past a short rail, so a wheel turn can move it.
func gitFloatSnap(cols, rows, railWidth int) *wire.Snapshot {
	snap := &wire.Snapshot{
		Cols: cols, Rows: rows, Rail: railWidth,
		Root: &layout.Node{Pane: "w1p1"}, Focus: "w1p1",
	}
	snap.Panes = []wire.PaneInfo{{ID: "w1p1", Status: "working"}}
	snap.All = []wire.AllPane{
		{
			ID: "w1p1", Name: "pi", Status: "working", Agent: true,
			Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one",
			Focus: true, WorkspaceActive: true, WindowActive: true,
			WorkspaceUnfolded: true, WindowUnfolded: true,
			Cwd: "/work/lb-key-dot-w1", Root: "/work/lb-key-dot-w1",
			Worktree: "lb-key-dot-w1", Branch: "lb-key-dot/w1", InRepo: true,
			Dirty: true, Added: 12, Deleted: 3, Files: 4,
			Head: "deadb33f", HeadSubject: "fix rail elision",
			Upstream: "origin/lb-key-dot/w1", Ahead: 2, Behind: 0,
		},
		{
			ID: "w1p2", Name: "beta", Workspace: "s1", WorkspaceName: "main",
			Window: "w1", WindowName: "one",
			WorkspaceUnfolded: true, WindowUnfolded: true,
		},
		{
			ID: "w1p3", Name: "gamma", Workspace: "s1", WorkspaceName: "main",
			Window: "w1", WindowName: "one",
			WorkspaceUnfolded: true, WindowUnfolded: true,
		},
		{
			ID: "w2p1", Name: "delta", Status: "idle",
			Workspace: "s2", WorkspaceName: "work", Window: "w2", WindowName: "two",
			WorkspaceUnfolded: true, WindowUnfolded: true,
			Cwd: "/work/suite-tail-w1", Root: "/work/suite-tail-w1",
			Worktree: "suite-tail-w1", Branch: "suite-tail/w1", InRepo: true,
			Head: "8a98d9d", HeadSubject: "land suite-tail",
		},
		{
			ID: "w2p2", Name: "epsilon", Workspace: "s2", WorkspaceName: "work",
			Window: "w2", WindowName: "two",
			WorkspaceUnfolded: true, WindowUnfolded: true,
		},
		{
			ID: "w2p3", Name: "zeta", Workspace: "s2", WorkspaceName: "work",
			Window: "w2", WindowName: "two",
			WorkspaceUnfolded: true, WindowUnfolded: true,
		},
	}
	return snap
}

// gitFloatTUI is a client on the facts fixture with a frame buffer.
func gitFloatTUI(t *testing.T, cols, rows, railWidth int) (*tui, chan wire.Msg) {
	t.Helper()
	tui, got := clientTUI(t, nil)
	tui.snap = gitFloatSnap(cols, rows, railWidth)
	tui.out = &bytes.Buffer{}
	return tui, got
}

// gitMarkClick sends one left click, press and release, on the git mark of the
// rail row y (zero based). The glyph sits two cells before the rail's last
// column, which the grip strip owns.
func gitMarkClick(tui *tui, y int) {
	x := tui.snap.Rail - 2
	tui.handleMouse(mouseEvent{btn: 0, x: x, y: y + 1})
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: x, y: y + 1})
}

// gitPaint draws one frame into term, so a test replays a sequence the way the
// terminal sees it.
func gitPaint(tui *tui, term *vt.Term) {
	buf := tui.out.(*bytes.Buffer)
	buf.Reset()
	tui.draw()
	term.Write(buf.Bytes())
	term.Flush()
}

// gitScreen replays one frame into a fresh terminal, so a test reads what the
// box painted and where.
func gitScreen(tui *tui) *vt.Term {
	term := vt.NewTerm(tui.snap.Cols, tui.snap.Rows)
	gitPaint(tui, term)
	return term
}

// gitRowText reads width cells of a screen row as text.
func gitRowText(term *vt.Term, y, x, width int) string {
	var b strings.Builder
	for i := 0; i < width; i++ {
		c := termCell(term, x+i, y)
		if c.Char == 0 {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(c.Char)
	}
	return b.String()
}

// TestGitMarkClickOpensTheFloat proves a click on the mark opens the box for
// that window, adds one float, and sends nothing: the focus, the fold and the
// pane under the click stay as they were.
func TestGitMarkClickOpensTheFloat(t *testing.T) {
	t.Parallel()
	tui, got := gitFloatTUI(t, 100, 12, 20)
	row := rail.RailGitMarkRow(tui.snap, "w1", 0)
	if row < 0 {
		t.Fatal("the fixture paints no mark for w1")
	}
	gitMarkClick(tui, row)
	if tui.git == nil || tui.git.window != "w1" {
		t.Fatalf("mark click left git = %+v, want w1", tui.git)
	}
	if len(tui.floats) != 1 || tui.floats[0].ID != gitFloatID {
		t.Fatalf("floats = %+v, want the one facts box", tui.floats)
	}
	if tui.snap.Focus != "w1p1" || tui.mode != "" {
		t.Fatalf("mark click moved focus to %q, mode %q", tui.snap.Focus, tui.mode)
	}
	testutil.NoMsg(t, got)
	// The box opens beside the rail, on the row that owns the mark.
	rect := tui.git.float.Rect
	if rect.X != tui.snap.Rail+1 || rect.Y != row+1 {
		t.Fatalf("box rect = %+v, want its top left at %d,%d", rect, tui.snap.Rail+1, row+1)
	}
	if rect.Y+rect.H >= tui.snap.Rows {
		t.Fatalf("box rect = %+v, want it inside %d rows", rect, tui.snap.Rows)
	}
	// The open mark inverts: the glyph takes the rail's ground on the accent,
	// so the row says which box is up.
	th := tui.theme()
	accent := vt.RGBColor(uint8(th.Palette.BarAccent.R), uint8(th.Palette.BarAccent.G), uint8(th.Palette.BarAccent.B))
	ground := vt.RGBColor(uint8(th.Palette.RailBG.R), uint8(th.Palette.RailBG.G), uint8(th.Palette.RailBG.B))
	if c := termCell(gitScreen(tui), tui.snap.Rail-3, row); c.BG != accent || c.FG != ground {
		t.Fatalf("open mark = %+v, want the glyph in the rail ground on the accent", c)
	}

	// A second click on the same mark closes it.
	gitMarkClick(tui, row)
	if tui.git != nil || len(tui.floats) != 0 {
		t.Fatalf("second click left git = %+v floats = %+v", tui.git, tui.floats)
	}
	testutil.NoMsg(t, got)
}

// TestGitMarkClickMovesTheFloat proves a click on another window's mark moves
// the one box: the rail never carries two.
func TestGitMarkClickMovesTheFloat(t *testing.T) {
	t.Parallel()
	tui, got := gitFloatTUI(t, 100, 16, 20)
	one := rail.RailGitMarkRow(tui.snap, "w1", 0)
	two := rail.RailGitMarkRow(tui.snap, "w2", 0)
	if one < 0 || two < 0 {
		t.Fatalf("fixture marks = %d, %d", one, two)
	}
	gitMarkClick(tui, one)
	gitMarkClick(tui, two)
	if tui.git == nil || tui.git.window != "w2" {
		t.Fatalf("moved box = %+v, want w2", tui.git)
	}
	if len(tui.floats) != 1 {
		t.Fatalf("floats = %+v, want one box after the move", tui.floats)
	}
	if want := two + 1; tui.git.float.Rect.Y != want {
		t.Fatalf("moved box top = %d, want %d", tui.git.float.Rect.Y, want)
	}
	testutil.NoMsg(t, got)
}

// TestGitFloatEscCloses proves esc closes the box and goes no further: no byte
// reaches a pane and no other overlay takes the key.
func TestGitFloatEscCloses(t *testing.T) {
	t.Parallel()
	tui, got := gitFloatTUI(t, 100, 12, 20)
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w1", 0))
	if tui.git == nil {
		t.Fatal("the mark click opened no box")
	}
	feed(tui, "\x1b")
	if tui.git != nil || len(tui.floats) != 0 {
		t.Fatalf("esc left git = %+v floats = %+v", tui.git, tui.floats)
	}
	if tui.mode != "" {
		t.Fatalf("esc left mode %q", tui.mode)
	}
	testutil.NoMsg(t, got)
}

// TestGitFloatEscBeatsTheRenamePrompt proves esc reaches the facts box before a
// surface that can stand with it: the box closes and the prompt keeps its mode.
// Delete the box's esc branch from processInput and the prompt takes the key, so
// this test fails.
func TestGitFloatEscBeatsTheRenamePrompt(t *testing.T) {
	t.Parallel()
	tui, got := gitFloatTUI(t, 100, 12, 20)
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w1", 0))
	if tui.git == nil {
		t.Fatal("the mark click opened no box")
	}
	tui.openRenamePrompt()
	if tui.mode != "rename" {
		t.Fatalf("the prompt opened mode %q, want rename", tui.mode)
	}
	testutil.NoMsg(t, got)
	feed(tui, "\x1b")
	if tui.git != nil {
		t.Fatalf("esc left the facts box open: %+v", tui.git)
	}
	if tui.mode != "rename" {
		t.Fatalf("esc closed the prompt under the box: mode=%q", tui.mode)
	}
	testutil.NoMsg(t, got)
}

// TestGitFloatFoldCloses proves a fold that stops drawing the row closes the
// box: the row it describes is no longer there.
func TestGitFloatFoldCloses(t *testing.T) {
	t.Parallel()
	tui, _ := gitFloatTUI(t, 100, 12, 20)
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w1", 0))
	if tui.git == nil {
		t.Fatal("the mark click opened no box")
	}
	folded := gitFloatSnap(100, 12, 20)
	for i := range folded.All {
		if folded.All[i].Workspace == "s1" {
			folded.All[i].WorkspaceUnfolded = false
		}
	}
	tui.apply(wire.Msg{Type: "snapshot", Snap: folded})
	if rail.RailGitMarkRow(folded, "w1", 0) >= 0 {
		t.Fatal("the folded workspace still draws the window row")
	}
	if tui.git != nil || len(tui.floats) != 0 {
		t.Fatalf("fold left git = %+v floats = %+v", tui.git, tui.floats)
	}
}

// TestGitFloatClickElsewhereCloses proves a left press off the mark closes the
// box, and still does what the press was for.
func TestGitFloatClickElsewhereCloses(t *testing.T) {
	t.Parallel()
	tui, got := gitFloatTUI(t, 100, 12, 20)
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w1", 0))
	// A press on the pane row under the window closes the box and focuses the
	// pane, so the click is not swallowed with the box.
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 4})
	if tui.git != nil || len(tui.floats) != 0 {
		t.Fatalf("press on a pane row left git = %+v floats = %+v", tui.git, tui.floats)
	}
	if m := nextMsg(t, got); m.Action != "focus" || m.Target != "w1p2" {
		t.Fatalf("press sent %+v, want focus w1p2", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 4})
}

// TestGitFloatRailScrollCloses proves the box follows its row and closes when a
// scroll takes the row off the rail.
func TestGitFloatRailScrollCloses(t *testing.T) {
	t.Parallel()
	// Nine rows is the shortest terminal the box stands on, and the list runs
	// past the seven item rows it leaves, so a wheel turn takes the mark off.
	tui, _ := gitFloatTUI(t, 100, 9, 20)
	row := rail.RailGitMarkRow(tui.snap, "w1", 0)
	if row != 1 {
		t.Fatalf("w1 mark row = %d, want 1", row)
	}
	gitMarkClick(tui, row)
	if tui.git == nil {
		t.Fatal("the mark click opened no box")
	}
	for i := 0; i < 3; i++ {
		railWheelTurn(tui, false)
	}
	if tui.railOff == 0 {
		t.Fatal("the rail never scrolled")
	}
	if rail.RailGitMarkRow(tui.snap, "w1", tui.railOff) >= 0 {
		t.Fatalf("the mark is still on the rail at offset %d", tui.railOff)
	}
	if tui.git != nil || len(tui.floats) != 0 {
		t.Fatalf("scroll left git = %+v floats = %+v", tui.git, tui.floats)
	}
}

// TestGitFloatFollowsTheRow proves the anchor moves with the rail scroll while
// the row is still drawn.
func TestGitFloatFollowsTheRow(t *testing.T) {
	t.Parallel()
	tui, _ := gitFloatTUI(t, 100, 12, 20)
	// The list runs one row past a twelve row rail, so a wheel turn clamps to
	// one row and w1's mark rides from the second item row to the first.
	row := rail.RailGitMarkRow(tui.snap, "w1", 0)
	gitMarkClick(tui, row)
	if tui.git == nil || tui.git.window != "w1" {
		t.Fatalf("git = %+v, want w1", tui.git)
	}
	if got := tui.git.float.Rect.Y; got != row+1 {
		t.Fatalf("box top = %d, want %d before the scroll", got, row+1)
	}
	railWheelTurn(tui, false)
	if tui.railOff != 1 {
		t.Fatalf("railOff = %d, want the one row the list overflows by", tui.railOff)
	}
	want := rail.RailGitMarkRow(tui.snap, "w1", tui.railOff)
	if want != 0 {
		t.Fatalf("mark row after the scroll = %d, want 0", want)
	}
	if tui.git == nil {
		t.Fatal("the box closed while its row was still drawn")
	}
	if got := tui.git.float.Rect.Y; got != want+1 {
		t.Fatalf("box top = %d, want the moved row %d", got, want+1)
	}
}

// TestGitFloatPaintsTheFacts proves the box paints the facts the snapshot
// carries, one fact a row, with the worktree, branch, head and upstream of the
// window the mark belongs to.
func TestGitFloatPaintsTheFacts(t *testing.T) {
	t.Parallel()
	tui, _ := gitFloatTUI(t, 100, 12, 20)
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w1", 0))
	rect := tui.git.float.Rect
	term := gitScreen(tui)
	rows := map[string]string{}
	for i := 0; i < rect.H; i++ {
		rows[strings.Fields(gitRowText(term, rect.Y+i, rect.X, rect.W))[0]] = gitRowText(term, rect.Y+i, rect.X, rect.W)
	}
	for _, want := range []struct{ key, value string }{
		{"worktree", "lb-key-dot-w1"},
		{"branch", "lb-key-dot/w1"},
		{"head", "deadb33f  fix rail elision"},
		{"changes", "+12 -3  4 files"},
		{"state", "dirty"},
		{"upstream", "origin/lb-key-dot/w1  \u21912 \u21930"},
	} {
		row, ok := rows[want.key]
		if !ok {
			t.Fatalf("no %q row in the box: %v", want.key, rows)
		}
		if !strings.Contains(row, want.value) {
			t.Fatalf("%s row = %q, want %q", want.key, row, want.value)
		}
	}
	it, ok := rail.RailWindowItem(tui.snap, "w1")
	if !ok {
		t.Fatal("the snapshot carries no w1 row")
	}
	want := tui.theme().Glyphs.GitMark + " " + gitLabel(it)
	if title := gitRowText(term, rect.Y-1, rect.X, rect.W+2); !strings.Contains(title, want) {
		t.Fatalf("title = %q, want the mark glyph and the window label %q", title, want)
	}
	if hint := gitRowText(term, rect.Y+rect.H, rect.X, rect.W+2); !strings.Contains(hint, "esc") {
		t.Fatalf("hint row = %q, want the dismiss key", hint)
	}
}

// TestGitFloatMissingUpstreamPaintsNone proves a window that tracks nothing
// paints (none) in the dim colour, never a zero and never a clean state it did
// not see.
func TestGitFloatMissingUpstreamPaintsNone(t *testing.T) {
	t.Parallel()
	tui, _ := gitFloatTUI(t, 100, 12, 20)
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w2", 0))
	rect := tui.git.float.Rect
	term := gitScreen(tui)
	th := theme.DefaultTheme()
	dim := vt.RGBColor(uint8(th.Palette.RailDim.R), uint8(th.Palette.RailDim.G), uint8(th.Palette.RailDim.B))
	row := rect.Y + 5
	text := gitRowText(term, row, rect.X, rect.W)
	if !strings.Contains(text, "upstream") || !strings.Contains(text, "(none)") {
		t.Fatalf("upstream row = %q, want (none)", text)
	}
	if strings.Contains(text, "0") {
		t.Fatalf("upstream row = %q, want no zero count", text)
	}
	for x := rect.X; x < rect.X+rect.W; x++ {
		if c := termCell(term, x, row); c.Char == '(' {
			if c.FG != dim {
				t.Fatalf("(none) painted in %+v, want the rail dim", c.FG)
			}
			return
		}
	}
	t.Fatalf("no (none) cell on the upstream row %q", text)
}

// TestGitFloatWidthCapAndElision proves the box holds its width between the
// floor and the cap, and that the head subject elides with the set's mark at the
// narrow end rather than wrapping or pushing the width past the cap.
func TestGitFloatWidthCapAndElision(t *testing.T) {
	t.Parallel()
	// A wide terminal: a long subject takes the box to the cap, and the subject
	// elides to the cells that are left.
	long := strings.Repeat("x", 60)
	tui, _ := gitFloatTUI(t, 120, 12, 20)
	tui.snap.All[0].HeadSubject = long
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w1", 0))
	rect := tui.git.float.Rect
	if got := rect.W + 2; got != gitMaxCols {
		t.Fatalf("box width = %d, want the cap %d", got, gitMaxCols)
	}
	term := gitScreen(tui)
	head := gitRowText(term, rect.Y+2, rect.X, rect.W)
	if !strings.Contains(head, "\u2026") {
		t.Fatalf("head row = %q, want the set's elision mark", head)
	}
	if !strings.Contains(head, "deadb33f") {
		t.Fatalf("head row = %q, want the short sha kept whole", head)
	}
	if trimmed := strings.TrimRight(head, " "); cells.Cells(trimmed) > rect.W {
		t.Fatalf("head row = %q, wider than the box %d", head, rect.W)
	}

	// A terminal that leaves exactly the floor beside the rail: the box stands
	// at its minimum and the head still elides, one row, never wrapped.
	narrow, _ := gitFloatTUI(t, 20+gitMinCols, 12, 20)
	narrow.snap.All[0].HeadSubject = long
	gitMarkClick(narrow, rail.RailGitMarkRow(narrow.snap, "w1", 0))
	rect = narrow.git.float.Rect
	if got := rect.W + 2; got != gitMinCols {
		t.Fatalf("box width = %d, want the floor %d", got, gitMinCols)
	}
	if rect.X != narrow.snap.Rail+1 {
		t.Fatalf("box left = %d, want it beside the rail at %d", rect.X, narrow.snap.Rail+1)
	}
	if rect.X+rect.W > narrow.snap.Cols {
		t.Fatalf("box runs off the terminal: %+v", rect)
	}
	term = gitScreen(narrow)
	head = gitRowText(term, rect.Y+2, rect.X, rect.W)
	if !strings.Contains(head, "\u2026") {
		t.Fatalf("narrow head row = %q, want the elision mark", head)
	}
	if got := cells.Cells(strings.TrimRight(head, " ")); got > rect.W {
		t.Fatalf("narrow head row = %q, wider than the box %d", head, rect.W)
	}
	// The box keeps its six fact rows: the subject never wraps into a seventh.
	for i := 0; i < rect.H; i++ {
		if row := gitRowText(term, rect.Y+i, rect.X, rect.W); !strings.HasPrefix(row, " ") {
			t.Fatalf("fact row %d = %q, want a keyed row", i, row)
		}
	}
}

// gitSweepSnap is a rail of windows inside one repository, more of them than any
// sweep rail can show, so the lowest drawn mark is always there to open.
func gitSweepSnap(cols, rows, railWidth int) *wire.Snapshot {
	snap := &wire.Snapshot{
		Cols: cols, Rows: rows, Rail: railWidth,
		Root: &layout.Node{Pane: "w1p1"}, Focus: "w1p1",
	}
	snap.Panes = []wire.PaneInfo{{ID: "w1p1"}}
	for i := 0; i < 8; i++ {
		snap.All = append(snap.All, wire.AllPane{
			ID:        "w1p" + strconv.Itoa(i+1),
			Name:      "p" + strconv.Itoa(i+1),
			Workspace: "s1", WorkspaceName: "main",
			Window: "w" + strconv.Itoa(i+1), WindowName: "win" + strconv.Itoa(i+1),
			WorkspaceActive: true, WindowActive: true,
			WorkspaceUnfolded: true, WindowUnfolded: true,
			Cwd: "/work/wt-" + strconv.Itoa(i+1), Root: "/work/wt-" + strconv.Itoa(i+1),
			Worktree: "wt-" + strconv.Itoa(i+1), Branch: "b/w" + strconv.Itoa(i+1),
			InRepo: true, Head: "abc1234", HeadSubject: "sweep the rail",
			Upstream: "origin/main", Ahead: 1,
		})
	}
	return snap
}

// gitLowestMark is the window whose mark sits on the lowest item row the rail
// draws, and that row.
func gitLowestMark(t *testing.T, snap *wire.Snapshot) (string, int) {
	t.Helper()
	for y := snap.Rows - 3; y >= 0; y-- {
		if w := rail.RailGitMarkAt(snap, snap.Rail-3, y, 0); w != "" {
			return w, y
		}
	}
	t.Fatalf("the rail draws no mark: %+v", snap)
	return "", -1
}

// TestGitFloatStatusBarBoundary proves the box leaves the status bar alone and
// closes where it cannot. Nine rows is the shortest terminal that stands the
// box: its bottom border lands on Rows-2 and the bar keeps every cell. Eight
// rows has no honest anchor, so the mark opens nothing at all.
func TestGitFloatStatusBarBoundary(t *testing.T) {
	for _, tc := range []struct {
		rows int
		open bool
	}{
		{gitRows + gitChromeRows, true},
		{gitRows + gitChromeRows - 1, false},
	} {
		tui, _ := gitFloatTUI(t, 100, tc.rows, 20)
		row := rail.RailGitMarkRow(tui.snap, "w1", 0)
		if row < 0 {
			t.Fatalf("rows %d: the fixture draws no mark for w1", tc.rows)
		}
		gitMarkClick(tui, row)
		if !tc.open {
			if tui.git != nil || len(tui.floats) != 0 {
				t.Fatalf("rows %d: the box opened on a terminal too short for it: %+v",
					tc.rows, tui.git)
			}
			continue
		}
		if tui.git == nil {
			t.Fatalf("rows %d: the mark opened no box", tc.rows)
		}
		rect := tui.git.float.Rect
		if got := rect.Y + rect.H; got != tc.rows-2 {
			t.Fatalf("rows %d: bottom border row = %d, want exactly %d",
				tc.rows, got, tc.rows-2)
		}
		// The status bar keeps every cell it painted before the box opened.
		term := gitScreen(tui)
		before := gitRowText(term, tc.rows-1, 0, tui.snap.Cols)
		tui.closeGitFloat()
		gitPaint(tui, term)
		if after := gitRowText(term, tc.rows-1, 0, tui.snap.Cols); after != before {
			t.Fatalf("rows %d: the box painted on the status bar: %q, want %q",
				tc.rows, after, before)
		}
	}
}

// TestGitFloatGeometrySweep holds the box's frame rules across rail widths,
// terminal widths and row counts: it stays on the terminal at the right, its
// bottom border never reaches the status bar, it never covers the rail's own
// cells, and a terminal too short to stand it opens nothing.
func TestGitFloatGeometrySweep(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	for _, cols := range []int{20, 47, 74, 101, 160} {
		maxRail := layout.MaxRail(cols)
		for railWidth := 5; railWidth <= maxRail; railWidth++ {
			for _, rows := range []int{8, 9, 12, 17, 33} {
				snap := gitSweepSnap(cols, rows, railWidth)
				tui := &tui{snap: snap, vts: map[string]*vt.Term{}, out: &bytes.Buffer{}}
				window, row := gitLowestMark(t, snap)
				gitMarkClick(tui, row)
				if rows < gitRows+gitChromeRows {
					if tui.git != nil || len(tui.floats) != 0 {
						t.Fatalf("%dx%d rail %d: the box opened on a terminal too short for it",
							cols, rows, railWidth)
					}
					continue
				}
				if tui.git == nil || tui.git.window != window {
					t.Fatalf("%dx%d rail %d: the mark on row %d opened %+v, want %q",
						cols, rows, railWidth, row, tui.git, window)
				}
				rect := tui.git.float.Rect
				if got := rect.Y + rect.H; got > rows-2 {
					t.Fatalf("%dx%d rail %d: bottom border row = %d, past the status bar at %d",
						cols, rows, railWidth, got, rows-1)
				}
				if got := rect.X + rect.W; got > cols-1 {
					t.Fatalf("%dx%d rail %d: right border column = %d, past the terminal at %d",
						cols, rows, railWidth, got, cols-1)
				}
				if got := rect.X - 1; got < railWidth {
					t.Fatalf("%dx%d rail %d: box left border = %d, over the rail",
						cols, rows, railWidth, got)
				}
				// The frame leaves the rail's own cells as the rail painted
				// them: the grip strip's last column belongs to the divider,
				// so the rail's cells are columns 0..Rail-2 through the band's
				// rows.
				frame := gitScreen(tui)
				var railBuf bytes.Buffer
				th := tui.theme()
				rail.DrawRail(&railBuf, th, snap, tui.glyphSet(), rail.Paint{
					Off: tui.railOff, Hover: tui.railHoverRow(), Drop: tui.railDrop(),
					Open: tui.gitOpen(),
				})
				ref := vt.NewTerm(cols, rows)
				ref.Write(railBuf.Bytes())
				ref.Flush()
				for y := 0; y < rows-1; y++ {
					for x := 0; x < railWidth-1; x++ {
						got, want := termCell(frame, x, y), termCell(ref, x, y)
						if got.Char != want.Char || got.FG != want.FG || got.BG != want.BG {
							t.Fatalf("%dx%d rail %d: the box painted the rail cell %d,%d: %+v, want %+v",
								cols, rows, railWidth, x, y, got, want)
						}
					}
				}
			}
		}
	}
}

// TestGitFloatMenuTakesTheDesk proves one surface at a time in both directions:
// opening the window row's menu takes the box down and leaves none of it
// painted, and a press on the mark while the menu is up puts the menu away and
// opens nothing, so the next press opens the box.
func TestGitFloatMenuTakesTheDesk(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	tui, _ := gitFloatTUI(t, 100, 16, 20)
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w1", 0))
	if tui.git == nil {
		t.Fatal("the mark click opened no box")
	}
	rect := tui.git.float.Rect
	if row := gitRowText(gitScreen(tui), rect.Y, rect.X, rect.W); !strings.Contains(row, "worktree") {
		t.Fatalf("frame with the box open = %q, want a fact row", row)
	}

	// The window row's menu takes the desk.
	tui.handleMouse(mouseEvent{btn: 2, x: 5, y: 2})
	if tui.mode != "menu" || tui.menu == nil {
		t.Fatalf("right press on the window row: mode=%q menu=%+v", tui.mode, tui.menu)
	}
	if tui.git != nil || len(tui.floats) != 0 {
		t.Fatalf("the menu left the box up: git=%+v floats=%+v", tui.git, tui.floats)
	}
	// No border and no fact of the box is painted: a session that never opened
	// it draws the same screen, down to the cell.
	other, _ := gitFloatTUI(t, 100, 16, 20)
	other.handleMouse(mouseEvent{btn: 2, x: 5, y: 2})
	if a, b := screenString(gitScreen(tui)), screenString(gitScreen(other)); a != b {
		t.Fatalf("the box is still painted under the menu:\n%s\nwant\n%s", a, b)
	}

	// A press on the mark under the menu puts the menu away and opens nothing.
	markX := tui.snap.Rail - 2
	tui.handleMouse(mouseEvent{btn: 0, x: markX, y: 2})
	if tui.mode != "" || tui.menu != nil {
		t.Fatalf("the mark press left the menu up: mode=%q", tui.mode)
	}
	if tui.git != nil || len(tui.floats) != 0 {
		t.Fatalf("the press that dismissed the menu opened the box: %+v", tui.git)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: markX, y: 2})
	// The next press opens it.
	tui.handleMouse(mouseEvent{btn: 0, x: markX, y: 2})
	if tui.git == nil || tui.git.window != "w1" {
		t.Fatalf("the next mark press opened %+v, want w1", tui.git)
	}
}

// TestGitHintNamesTheMark proves the bottom border says how the mark's own box
// closes, and that the whole bar fits the box's narrowest bottom row.
func TestGitHintNamesTheMark(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	// The narrowest box: the outside floor, both borders, and the two cells the
	// border bar keeps clear of the corners.
	width := gitMinCols - 4
	bar := gitHint()
	text := bar.Text(width, th)
	for _, want := range []string{"esc", "close", "click", "mark"} {
		if !strings.Contains(text, want) {
			t.Fatalf("hint = %q, want it to name %q", text, want)
		}
	}
	if got := bar.Cells(width, th); got > width {
		t.Fatalf("hint = %q at %d cells, wider than the bottom row allows", text, got)
	}

	// The narrowest box paints the whole bar on its bottom border.
	tui, _ := gitFloatTUI(t, 20+gitMinCols, 12, 20)
	gitMarkClick(tui, rail.RailGitMarkRow(tui.snap, "w1", 0))
	if tui.git == nil {
		t.Fatal("the mark opened no box")
	}
	rect := tui.git.float.Rect
	if got := rect.W + 2; got != gitMinCols {
		t.Fatalf("box width = %d, want the floor %d", got, gitMinCols)
	}
	border := gitRowText(gitScreen(tui), rect.Y+rect.H, rect.X-1, rect.W+2)
	if !strings.Contains(border, "click") || !strings.Contains(border, "mark") ||
		!strings.Contains(border, "esc") {
		t.Fatalf("bottom border = %q, want the whole hint", border)
	}
}

// gitDeskSurface is one surface that owns the desk, with the way a client opens
// it.
type gitDeskSurface struct {
	name string
	open func(t *tui)
}

// gitDeskSurfaces is every surface the one-at-a-time rule covers, each with its
// own open path. The toasts and the armed prefix help panel are absent on
// purpose: neither owns the keys, so neither takes the desk.
func gitDeskSurfaces() []gitDeskSurface {
	return []gitDeskSurface{
		{"dashboard", func(t *tui) { openSwitcher(t) }},
		{"palette", func(t *tui) { openCommandPalette(t) }},
		{"help", func(t *tui) { openKeymapView(t) }},
		{"sessions", func(t *tui) { t.openPicker() }},
		{"layout", func(t *tui) { openLayoutSwitcher(t) }},
		{"search", func(t *tui) {
			// The query row lives on the scrollback band, so the pane needs a
			// real rect and the tall terminal behind it.
			t.snap.Panes[0].Rect = layout.Rect{X: t.snap.Rail, Y: 0, W: 40, H: 8}
			t.histRows = 40
			p := t.paneByID("w1p1")
			t.scrollVT = vt.NewTerm(p.Rect.W, p.Rect.H+t.histWindow())
			t.scrollVT.Write([]byte("ROW-001\r\nROW-002\r\nROW-003"))
			t.scrollVT.Flush()
			t.scroll = p.ID
			t.mode = "scroll"
			t.openSearch()
		}},
	}
}

// TestGitFloatDeskSurfacesTakeItDown proves one surface at a time across every
// overlay that owns the desk. Opening one closes the facts box, and the frame
// carries no cell of it: the screen a session that never opened the box paints
// is the screen the reader gets, down to the cell.
func TestGitFloatDeskSurfacesTakeItDown(t *testing.T) {
	// The session picker reads the state directory, so the test owns one and
	// plants a session in it.
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "sessions", "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DM_STATE", state)
	for _, surface := range gitDeskSurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			// opened clicked the mark and left the box up; plain clicked the
			// same mark and put the box away, so both carry the same pointer
			// and the same rail hover, and the box is the only difference.
			opened, _ := gitFloatTUI(t, 100, 16, 20)
			row := rail.RailGitMarkRow(opened.snap, "w1", 0)
			gitMarkClick(opened, row)
			if opened.git == nil {
				t.Fatal("the mark click opened no box")
			}
			surface.open(opened)
			if opened.git != nil {
				t.Fatalf("the %s left the box up: %+v", surface.name, opened.git)
			}
			for _, f := range opened.floats {
				if f.ID == gitFloatID {
					t.Fatalf("the %s left the facts float in the stack", surface.name)
				}
			}

			plain, _ := gitFloatTUI(t, 100, 16, 20)
			gitMarkClick(plain, rail.RailGitMarkRow(plain.snap, "w1", 0))
			plain.closeGitFloat()
			surface.open(plain)

			if a, b := screenString(gitScreen(opened)), screenString(gitScreen(plain)); a != b {
				t.Fatalf("the box is still painted under the %s:\n%s\nwant\n%s",
					surface.name, a, b)
			}
		})
	}
}
