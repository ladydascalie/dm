package rail

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"dm/internal/cells"
	"dm/internal/layout"
	"dm/internal/segment"
	"dm/internal/theme"
	"dm/internal/wire"

	"github.com/hinshun/vt10x"
)

// railRowSnap is a rail with every row kind on it: a workspace header, an
// unfolded window line with a folder, a branch and its stats, that window's
// note, the focused pane, a folded window line, the gap, and a second
// workspace. Rows 8 and up are past the last item.
func railRowSnap(rail int) *wire.Snapshot {
	snap := &wire.Snapshot{
		Cols: 100, Rows: 10, Rail: rail,
		Root: &layout.Node{Pane: "w1p1"}, Focus: "w1p1",
	}
	snap.Panes = []wire.PaneInfo{{ID: "w1p1", Status: "working"}}
	snap.All = []wire.AllPane{
		{
			ID: "w1p1", Name: "pi", Status: "working", Agent: true,
			Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one",
			Focus: true, WorkspaceActive: true, WindowActive: true,
			WindowUnfolded: true, WorkspaceUnfolded: true,
			Cwd: "/work/dm/rail", Root: "/work/dm", Branch: "dm-rail", InRepo: true,
			Dirty: true, Added: 12, Deleted: 4, Message: "working hard",
		},
		{
			ID: "w1p2", Name: "beta", Status: "idle", Workspace: "s1",
			WorkspaceName: "main", Window: "w2", WindowName: "two",
			WorkspaceUnfolded: true, Cwd: "/work/dm/other", Root: "/work/dm",
		},
		{
			ID: "w2p1", Name: "gamma", Status: "done", Workspace: "s2",
			WorkspaceName: "work", Window: "w3", WindowName: "three",
			WorkspaceUnfolded: true,
		},
	}
	return snap
}

// railRowComposed is the cells a composed row paints: the left run, the pad,
// then the right-aligned run, with the row's trailing blanks trimmed.
func railRowComposed(left, tail, right []railSeg, w int) string {
	var sb strings.Builder
	for _, s := range append(append([]railSeg{}, left...), tail...) {
		sb.WriteString(s.Text)
	}
	if pad := w - segment.SegCells(left) - segment.SegCells(tail) - segment.SegCells(right); pad > 0 {
		sb.WriteString(strings.Repeat(" ", pad))
	}
	for _, s := range right {
		sb.WriteString(s.Text)
	}
	return strings.TrimRight(sb.String(), " ")
}

// railRowFind returns the only row of a kind that carries text.
func railRowFind(t *testing.T, items []RailItem, kind RailKind, text string) RailItem {
	t.Helper()
	for _, it := range items {
		if it.Kind == kind && it.Text == text {
			return it
		}
	}
	t.Fatalf("rail has no kind %d row %q: %+v", kind, text, items)
	return RailItem{}
}

// TestRailRowComposesPaint holds RailRow to the rail: every row it composes is
// the row DrawRail paints, at the default width and at a narrow one, in both
// glyph forms. No row writes past the rail width.
func TestRailRowComposesPaint(t *testing.T) {
	for _, nerd := range []bool{false, true} {
		env := "0"
		if nerd {
			env = "1"
		}
		t.Setenv("DM_NERD", env)
		for _, w := range []int{layout.DefaultRail, 12} {
			snap := railRowSnap(w)
			painted := railRows(t, snap)
			items := RailItems(snap)
			compact := layout.RailCompact(w)
			gripAt := (snap.Rows - 2) / 2
			for y, line := range painted {
				if y == snap.Rows-2 {
					// The control row is not a RailRow item; control_test.go owns it.
					continue
				}
				rowW := w
				if y < snap.Rows-2 && y == gripAt {
					rowW = w - 1
				}
				if y >= len(items) {
					want := ""
					if y == gripAt {
						want = strings.Repeat(" ", rowW) + theme.DefaultTheme().Glyphs.Grip
					}
					if line != want {
						t.Fatalf("nerd=%v w=%d row %d past the last item = %q, want %q",
							nerd, w, y, line, want)
					}
					continue
				}
				left, right, tail := RailRow(theme.DefaultTheme(), items[y], rowW, compact, nerd)
				want := railRowComposed(left, tail, right, rowW)
				if y == gripAt {
					if pad := rowW - cells.Cells(want); pad > 0 {
						want += strings.Repeat(" ", pad)
					}
					want += theme.DefaultTheme().Glyphs.Grip
				}
				if want != line {
					t.Fatalf("nerd=%v w=%d row %d kind %d composes %q, rail paints %q",
						nerd, w, y, items[y].Kind, want, line)
				}
				if n := segment.SegCells(left) + segment.SegCells(tail) + segment.SegCells(right); n > rowW {
					t.Fatalf("nerd=%v w=%d row %d is %d cells, want <= %d",
						nerd, w, y, n, rowW)
				}
			}
		}
	}
}

// TestRailRowSegments pins the runs RailRow composes for each row kind: their
// text, colour and weight, and which run is the tail.
func TestRailRowSegments(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railRowSnap(layout.DefaultRail)
	items := RailItems(snap)

	ws := railRowFind(t, items, Workspace, "main")
	left, right, tail := RailRow(theme.DefaultTheme(), ws, layout.DefaultRail, false, false)
	wantLeft := []railSeg{
		{Text: "\u25be", FG: theme.DefaultTheme().Accents[ws.Accent]},
		{Text: " ", FG: theme.DefaultTheme().Accents[ws.Accent]},
		{Text: "main", FG: theme.DefaultTheme().Accents[ws.Accent], Bold: true},
		{Text: " " + theme.DefaultTheme().RailMarks.Workspace, FG: theme.DefaultTheme().Accents[ws.Accent]},
	}
	wantRight := []railSeg{{Text: "2", FG: theme.DefaultTheme().Palette.RailDim}}
	if !reflect.DeepEqual(left, wantLeft) || !reflect.DeepEqual(right, wantRight) || tail != nil {
		t.Fatalf("workspace row = %+v / %+v / %+v, want %+v / %+v / nil",
			left, right, tail, wantLeft, wantRight)
	}

	// An unfolded window line carries the caret, the folder, the branch tail
	// and the stats.
	win := railRowFind(t, items, Window, "one")
	left, right, tail = RailRow(theme.DefaultTheme(), win, layout.DefaultRail, false, false)
	wantLeft = []railSeg{
		{Text: "  ", FG: theme.DefaultTheme().Palette.RailWindow},
		{Text: "\u25be", FG: theme.DefaultTheme().Palette.RailWindow},
		{Text: " ", FG: theme.DefaultTheme().Palette.RailWindow},
		{Text: "rail", FG: theme.DefaultTheme().Palette.RailWindow},
	}
	wantTail := []railSeg{
		{Text: "  ", FG: theme.DefaultTheme().Palette.RailDim},
		{Text: "dm-rail", FG: theme.DefaultTheme().Palette.RailDim},
	}
	wantRight = []railSeg{
		{Text: "\u25cf", FG: theme.DefaultTheme().Palette.RailWorking},
		{Text: " ", FG: theme.DefaultTheme().Palette.RailDim},
		{Text: "+12", FG: theme.DefaultTheme().Palette.RailDiffAdd},
		{Text: " ", FG: theme.DefaultTheme().Palette.RailDim},
		{Text: "-4", FG: theme.DefaultTheme().Palette.RailDiffDel},
	}
	if !reflect.DeepEqual(left, wantLeft) || !reflect.DeepEqual(right, wantRight) ||
		!reflect.DeepEqual(tail, wantTail) {
		t.Fatalf("window line = %+v / %+v / %+v, want %+v / %+v / %+v",
			left, right, tail, wantLeft, wantRight, wantTail)
	}

	// A folded window line flips the caret and keeps its folder. It has no
	// branch and no stats outside a repo.
	folded := railRowFind(t, items, Window, "two")
	left, right, tail = RailRow(theme.DefaultTheme(), folded, layout.DefaultRail, false, false)
	wantLeft = []railSeg{
		{Text: "  ", FG: theme.DefaultTheme().Palette.RailWindow},
		{Text: "\u25b8", FG: theme.DefaultTheme().Palette.RailWindow},
		{Text: " ", FG: theme.DefaultTheme().Palette.RailWindow},
		{Text: "other", FG: theme.DefaultTheme().Palette.RailWindow},
	}
	if folded.Folded != true {
		t.Fatalf("window two is not folded: %+v", folded)
	}
	if !reflect.DeepEqual(left, wantLeft) || right != nil || tail != nil {
		t.Fatalf("folded window line = %+v / %+v / %+v, want %+v / nil / nil",
			left, right, tail, wantLeft)
	}

	// The note line is one dim run under the window line.
	note := railRowFind(t, items, WindowNote, "working hard")
	left, right, tail = RailRow(theme.DefaultTheme(), note, layout.DefaultRail, false, false)
	wantLeft = []railSeg{
		{Text: "    ", FG: theme.DefaultTheme().Palette.RailDim},
		{Text: "working hard", FG: theme.DefaultTheme().Palette.RailDim},
	}
	if !reflect.DeepEqual(left, wantLeft) || right != nil || tail != nil {
		t.Fatalf("note line = %+v / %+v / %+v, want %+v / nil / nil",
			left, right, tail, wantLeft)
	}

	// A focused pane row puts its indents, marker, glyph and name on rail text,
	// and carries no right-aligned run. The focus fill and the row's bold are
	// the writer's, not the composition's.
	mark, glyphColour := RailMark(true, false), theme.DefaultTheme().Palette.RailWorking
	pane := railRowFind(t, items, Pane, "pi")
	left, right, tail = RailRow(theme.DefaultTheme(), pane, layout.DefaultRail, false, false)
	wantLeft = []railSeg{
		{Text: "      ", FG: theme.DefaultTheme().Palette.RailText},
		{Text: mark, FG: glyphColour},
		{Text: "\u25cf", FG: glyphColour},
		{Text: " pi", FG: theme.DefaultTheme().Palette.RailText},
		{Text: " " + theme.DefaultTheme().RailMarks.Pane, FG: theme.DefaultTheme().Palette.RailDim},
	}
	if !pane.Focus {
		t.Fatalf("pane pi is not focused: %+v", pane)
	}
	if !reflect.DeepEqual(left, wantLeft) || right != nil || tail != nil {
		t.Fatalf("pane row = %+v / %+v / %+v, want %+v / nil / nil",
			left, right, tail, wantLeft)
	}

	// The thin rail keeps the marker and the glyph and drops the indents.
	left, right, tail = RailRow(theme.DefaultTheme(), pane, layout.CollapsedRail, true, false)
	wantLeft = []railSeg{
		{Text: mark, FG: glyphColour},
		{Text: "\u25cf", FG: glyphColour},
	}
	if !reflect.DeepEqual(left, wantLeft) || right != nil || tail != nil {
		t.Fatalf("thin pane row = %+v / %+v / %+v, want %+v / nil / nil",
			left, right, tail, wantLeft)
	}

	// A gap row composes nothing at all.
	gap := railRowFind(t, items, Gap, "")
	left, right, tail = RailRow(theme.DefaultTheme(), gap, layout.DefaultRail, false, false)
	if left != nil || right != nil || tail != nil {
		t.Fatalf("gap row = %+v / %+v / %+v, want nothing", left, right, tail)
	}

	// The one thing RailRow does not return is the row's own ground and weight:
	// the focused pane row paints the focus fill bold, and every other row the
	// rail fill plain.
	var b bytes.Buffer
	DrawRail(&b, theme.DefaultTheme(), snap)
	out := b.String()
	focusRow := -1
	for y := 0; y < snap.Rows-1; y++ {
		want := theme.DefaultTheme().Palette.RailBG
		if y < len(items) {
			it := items[y]
			if it.Kind == Pane && it.Focus {
				focusRow, want = y, theme.DefaultTheme().Palette.RailFocusBG
			}
		}
		start := fmt.Sprintf("\x1b[%d;1H\x1b[0m", y+1) + want.BG()
		if !strings.Contains(out, start) {
			t.Fatalf("row %d is not painted on %v: %q", y, want, out)
		}
	}
	if focusRow < 0 {
		t.Fatal("the snapshot has no focused pane row")
	}
	bold := fmt.Sprintf("\x1b[%d;1H\x1b[0m", focusRow+1) + theme.DefaultTheme().Palette.RailFocusBG.BG() +
		"\x1b[0m" + theme.DefaultTheme().Palette.RailFocusBG.BG() + "\x1b[1m"
	if !strings.Contains(out, bold) {
		t.Fatalf("focused row %d is not bold on the focus fill: %q", focusRow, out)
	}
}

// TestRailRowLevelsDiffer proves the rail gives each row level its own look:
// the workspace row wears the accent and a bullet, the window row a caret,
// and the pane row an indent and a dot. The three composed rows differ.
func TestRailRowLevelsDiffer(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	snap := railRowSnap(layout.DefaultRail)
	items := RailItems(snap)
	compose := func(it RailItem) string {
		left, right, tail := RailRow(th, it, layout.DefaultRail, false, false)
		return railRowComposed(left, tail, right, layout.DefaultRail)
	}
	wsLine := compose(railRowFind(t, items, Workspace, "main"))
	winLine := compose(railRowFind(t, items, Window, "one"))
	paneLine := compose(railRowFind(t, items, Pane, "pi"))
	if wsLine == winLine || wsLine == paneLine || winLine == paneLine {
		t.Fatalf("rows are not distinct: workspace %q, window %q, pane %q",
			wsLine, winLine, paneLine)
	}
	if !strings.Contains(wsLine, th.RailMarks.Workspace) {
		t.Fatalf("workspace row lacks its mark %q: %q", th.RailMarks.Workspace, wsLine)
	}
	if !strings.Contains(winLine, th.Glyphs.FoldOpen) {
		t.Fatalf("window row lacks the caret %q: %q", th.Glyphs.FoldOpen, winLine)
	}
	if !strings.Contains(paneLine, th.RailMarks.Pane) {
		t.Fatalf("pane row lacks its mark %q: %q", th.RailMarks.Pane, paneLine)
	}
	if !strings.HasPrefix(wsLine, th.Glyphs.FoldOpen) {
		t.Fatalf("workspace row does not start with the caret: %q", wsLine)
	}
	if !strings.HasPrefix(winLine, "  "+th.Glyphs.FoldOpen) {
		t.Fatalf("window row does not start with the caret indent: %q", winLine)
	}
	if !strings.HasPrefix(paneLine, "      ") {
		t.Fatalf("pane row does not start with the pane indent: %q", paneLine)
	}
}

// TestRailRowElidesBeforeStats keeps the rule the narrow rail lives by: the
// branch yields whole, then the label elides, and the stats never lose a cell.
func TestRailRowElidesBeforeStats(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railRowSnap(12)
	items := RailItems(snap)
	win := railRowFind(t, items, Window, "one")
	full := []railSeg{
		{Text: "\u25cf", FG: theme.DefaultTheme().Palette.RailWorking},
		{Text: " ", FG: theme.DefaultTheme().Palette.RailDim},
		{Text: "+12", FG: theme.DefaultTheme().Palette.RailDiffAdd},
		{Text: " ", FG: theme.DefaultTheme().Palette.RailDim},
		{Text: "-4", FG: theme.DefaultTheme().Palette.RailDiffDel},
	}

	// At the default width the branch fits beside the folder and the pair.
	left, right, tail := RailRow(theme.DefaultTheme(), win, layout.DefaultRail, false, false)
	if tail == nil {
		t.Fatalf("branch dropped at %d columns: %+v", layout.DefaultRail, left)
	}
	if !reflect.DeepEqual(right, full) {
		t.Fatalf("stats at %d columns = %+v, want the whole pair", layout.DefaultRail, right)
	}

	// Twenty columns: the branch goes whole and the folder stays intact.
	left, right, tail = RailRow(theme.DefaultTheme(), win, 20, false, false)
	if tail != nil {
		t.Fatalf("branch survived at 20 columns: %+v", tail)
	}
	if !reflect.DeepEqual(right, full) {
		t.Fatalf("stats at 20 columns = %+v, want the whole pair", right)
	}
	if strings.Contains(railRowComposed(left, tail, right, 20), "\u2026") {
		t.Fatalf("folder elided while it fit: %q", railRowComposed(left, tail, right, 20))
	}

	// Twelve columns: the folder elides and the pair still keeps every cell.
	left, right, tail = RailRow(theme.DefaultTheme(), win, 12, false, false)
	if tail != nil {
		t.Fatalf("branch survived at 12 columns: %+v", tail)
	}
	if !reflect.DeepEqual(right, full) {
		t.Fatalf("stats at 12 columns = %+v, want the whole pair", right)
	}
	if n := len(left); n == 0 || left[n-1].Text != "\u2026" {
		t.Fatalf("folder did not elide at 12 columns: %+v", left)
	}
	if got := railRowComposed(left, tail, right, 12); got != "  \u2026 \u25cf +12 -4" {
		t.Fatalf("row at 12 columns = %q, want %q", got, "  \u2026 \u25cf +12 -4")
	}

	// At every width the window row either keeps the whole pair or drops it
	// whole, it never writes past the rail, and it composes what the rail
	// paints.
	for w := 1; w <= 40; w++ {
		if layout.RailCompact(w) {
			// The thin rail lists panes only; there is no window row to elide.
			continue
		}
		snap := railRowSnap(w)
		it := railRowFind(t, RailItems(snap), Window, "one")
		left, right, tail = RailRow(theme.DefaultTheme(), it, w, layout.RailCompact(w), false)
		if right != nil && !reflect.DeepEqual(right, full) {
			t.Fatalf("stats at %d columns = %+v, want the whole pair or none", w, right)
		}
		if n := segment.SegCells(left) + segment.SegCells(tail) + segment.SegCells(right); n > w {
			t.Fatalf("row at %d columns is %d cells: %+v / %+v / %+v",
				w, n, left, tail, right)
		}
		if got, line := railRowComposed(left, tail, right, w), railRows(t, snap)[1]; got != line {
			t.Fatalf("row at %d columns composes %q, rail paints %q", w, got, line)
		}
	}
}

// railSeg is the name the rail gave a styled run before seg.go made the rail
// and the bar one type. The moved test still spells it.
type railSeg = segment.Seg

// railRows draws a rail and returns the text of each row inside the rail width.
func railRows(t *testing.T, snap *wire.Snapshot) []string {
	t.Helper()
	var b bytes.Buffer
	DrawRail(&b, theme.DefaultTheme(), snap)
	vt := vt10x.New(vt10x.WithSize(snap.Cols, snap.Rows))
	if _, err := vt.Write(b.Bytes()); err != nil {
		t.Fatalf("draw rail: %v", err)
	}
	out := make([]string, snap.Rows-1)
	vt.Lock()
	for y := range out {
		var sb strings.Builder
		for x := 0; x < snap.Rail; x++ {
			g := vt.Cell(x, y)
			if g.Char == 0 {
				sb.WriteByte(' ')
				continue
			}
			sb.WriteRune(g.Char)
		}
		out[y] = strings.TrimRight(sb.String(), " ")
	}
	vt.Unlock()
	return out
}

func TestRailItemsOrder(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railRowSnap(layout.DefaultRail)
	items := RailItems(snap)
	// Expected order: Workspace "main", Window "one", Note "working hard",
	// Pane "pi", Window "two" (folded), Gap, Workspace "work", Window "three"
	// (folded).
	if len(items) != 8 {
		t.Fatalf("got %d items, want 8", len(items))
	}
	kinds := []RailKind{Workspace, Window, WindowNote, Pane, Window, Gap, Workspace, Window}
	for i, k := range kinds {
		if items[i].Kind != k {
			t.Fatalf("item %d kind = %d, want %d", i, items[i].Kind, k)
		}
	}
	if items[0].Folded {
		t.Fatal("workspace main is folded, want unfolded")
	}
	if items[1].Folded {
		t.Fatal("window one is folded, want unfolded")
	}
	if items[2].Text != "working hard" {
		t.Fatalf("note row = %q, want working hard", items[2].Text)
	}
	if items[3].Pane != "w1p1" {
		t.Fatalf("pane row = %q, want w1p1", items[3].Pane)
	}
	if !items[4].Folded {
		t.Fatal("window two is not folded")
	}
	if items[6].Folded {
		t.Fatal("workspace work is folded, want unfolded")
	}
	if !items[7].Folded {
		t.Fatal("window three is not folded")
	}
}

func TestRailItemsFoldedWorkspaceHidesPanes(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railRowSnap(layout.DefaultRail)
	for i := range snap.All {
		if snap.All[i].Workspace == "s1" {
			snap.All[i].WorkspaceUnfolded = false
		}
	}
	items := RailItems(snap)
	// Expected: Workspace "main" (folded), Gap, Workspace "work", Window
	// "three" (folded). A folded workspace hides its windows and panes.
	if len(items) != 4 {
		t.Fatalf("got %d items, want 4", len(items))
	}
	if items[0].Kind != Workspace || items[0].Text != "main" {
		t.Fatalf("item 0 = %+v, want Workspace main", items[0])
	}
	if !items[0].Folded {
		t.Fatal("workspace main is not folded")
	}
	if items[1].Kind != Gap {
		t.Fatalf("item 1 = %+v, want Gap", items[1])
	}
	if items[2].Kind != Workspace || items[2].Text != "work" {
		t.Fatalf("item 2 = %+v, want Workspace work", items[2])
	}
	if items[3].Kind != Window || items[3].Text != "three" {
		t.Fatalf("item 3 = %+v, want Window three", items[3])
	}
	if !items[3].Folded {
		t.Fatal("window three is not folded")
	}
}

// TestRailItemsFoldedWindowHidesPanes proves the two-level fold: a folded
// window hides only its panes while its workspace stays unfolded.
func TestRailItemsFoldedWindowHidesPanes(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railRowSnap(layout.DefaultRail)
	// Unfold window w2 so its pane row appears.
	for i := range snap.All {
		if snap.All[i].Window == "w2" {
			snap.All[i].WindowUnfolded = true
		}
	}
	items := RailItems(snap)
	found := false
	for _, it := range items {
		if it.Kind == Pane && it.Pane == "w1p2" {
			found = true
		}
	}
	if !found {
		t.Fatalf("unfolded window w2 hid its pane: %+v", items)
	}
	// Fold window w2 again. The workspace stays unfolded and its window line
	// stays, but the pane row goes.
	for i := range snap.All {
		if snap.All[i].Window == "w2" {
			snap.All[i].WindowUnfolded = false
		}
	}
	items = RailItems(snap)
	wsFolded := false
	winFolded := false
	for _, it := range items {
		if it.Kind == Pane && it.Pane == "w1p2" {
			t.Fatalf("folded window w2 kept its pane: %+v", items)
		}
		if it.Kind == Window && it.Window == "w2" {
			winFolded = it.Folded
		}
		if it.Kind == Workspace && it.Workspace == "s1" {
			wsFolded = it.Folded
		}
	}
	if wsFolded {
		t.Fatal("workspace s1 folded with its window")
	}
	if !winFolded {
		t.Fatal("window w2 is not folded")
	}
}

func TestRailWindowAndFoldAt(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railRowSnap(layout.DefaultRail)
	items := RailItems(snap)
	winIndex := -1
	noteIndex := -1
	wsIndex := -1
	paneIndex := -1
	for i, it := range items {
		switch {
		case it.Kind == Window && it.Text == "one":
			winIndex = i
		case it.Kind == WindowNote && it.Text == "working hard":
			noteIndex = i
		case it.Kind == Workspace && it.Text == "main":
			wsIndex = i
		case it.Kind == Pane && it.Text == "pi":
			paneIndex = i
		}
	}
	if winIndex < 0 || noteIndex < 0 || wsIndex < 0 || paneIndex < 0 {
		t.Fatalf("missing row: win=%d note=%d ws=%d pane=%d items=%+v",
			winIndex, noteIndex, wsIndex, paneIndex, items)
	}
	if got := RailWindowAt(snap, 0, winIndex); got != "w1" {
		t.Fatalf("RailWindowAt(0,%d) = %q, want w1", winIndex, got)
	}
	if got := RailWindowAt(snap, 0, noteIndex); got != "w1" {
		t.Fatalf("RailWindowAt(0,%d) = %q on the note row, want w1", noteIndex, got)
	}
	if got := RailWindowAt(snap, 0, wsIndex); got != "" {
		t.Fatalf("RailWindowAt(0,%d) = %q on a workspace row", wsIndex, got)
	}
	if got := RailWindowAt(snap, 0, paneIndex); got != "" {
		t.Fatalf("RailWindowAt(0,%d) = %q on a pane row", paneIndex, got)
	}
	if got := RailFoldAt(snap, 2, winIndex); got != "w1" {
		t.Fatalf("RailFoldAt(2,%d) = %q, want w1", winIndex, got)
	}
	if got := RailFoldAt(snap, 0, winIndex); got != "" {
		t.Fatalf("RailFoldAt(0,%d) = %q, want empty", winIndex, got)
	}
	if got := RailFoldAt(snap, 2, wsIndex); got != "" {
		t.Fatalf("RailFoldAt(2,%d) = %q on a workspace row, want empty", wsIndex, got)
	}
	if got := RailFoldAt(snap, 2, noteIndex); got != "" {
		t.Fatalf("RailFoldAt(2,%d) = %q on the note row, want empty", noteIndex, got)
	}
}

func TestRailWorkspaceAndFoldAt(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railRowSnap(layout.DefaultRail)
	items := RailItems(snap)
	wsIndex := -1
	for i, it := range items {
		if it.Kind == Workspace && it.Text == "main" {
			wsIndex = i
			break
		}
	}
	if wsIndex < 0 {
		t.Fatal("no workspace main row")
	}
	if got := RailWorkspaceAt(snap, 0, wsIndex); got != "s1" {
		t.Fatalf("RailWorkspaceAt(0,%d) = %q, want s1", wsIndex, got)
	}
	if got := RailFoldAt(snap, 0, wsIndex); got != "s1" {
		t.Fatalf("RailFoldAt(0,%d) = %q, want s1", wsIndex, got)
	}
	if got := RailFoldAt(snap, 1, wsIndex); got != "" {
		t.Fatalf("RailFoldAt(1,%d) = %q, want empty", wsIndex, got)
	}
	paneIndex := -1
	for i, it := range items {
		if it.Kind == Pane && it.Text == "pi" {
			paneIndex = i
			break
		}
	}
	if paneIndex < 0 {
		t.Fatal("no pane pi row")
	}
	if got := RailWorkspaceAt(snap, 0, paneIndex); got != "" {
		t.Fatalf("RailWorkspaceAt(0,%d) = %q on a pane row", paneIndex, got)
	}
	if got := RailFoldAt(snap, 0, paneIndex); got != "" {
		t.Fatalf("RailFoldAt(0,%d) = %q on a pane row", paneIndex, got)
	}
}
