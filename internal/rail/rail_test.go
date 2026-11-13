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
	"dm/internal/vt"
	"dm/internal/wire"
)

// railRowSnap is a rail with every row kind on it: a workspace header, an
// unfolded window line with a folder, a branch and its stats, that window's
// note, the focused pane, a folded window line, the gap, and a second
// workspace. Rows 8 and up are past the last item.
func railRowSnap(rail int) *wire.Snapshot {
	snap := &wire.Snapshot{
		Cols: 100, Rows: 10, Rail: rail,
		Root: &layout.Node{Pane: "w1p1"}, Focus: "w1p1",
		WindowLabel: "{{base .Cwd}}",
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
	for _, set := range []theme.GlyphSet{theme.GlyphUnicode, theme.GlyphNerd} {
		for _, w := range []int{layout.DefaultRail, 12} {
			snap := railRowSnap(w)
			painted := railRows(t, snap, set)
			items := RailItems(snap)
			compact := layout.RailCompact(w)
			gripAt := (snap.Rows - 2) / 2
			for y, line := range painted {
				if y == snap.Rows-2 {
					// The control row is not a RailRow item; control_test.go owns it.
					continue
				}
				rowW := w
				if y < snap.Rows-2 {
					rowW = w - 1
				}
				if y >= len(items) {
					want := ""
					if y == gripAt {
						want = strings.Repeat(" ", rowW) + theme.DefaultTheme().Glyphs.Grip
					}
					if line != want {
						t.Fatalf("set=%v w=%d row %d past the last item = %q, want %q",
							set, w, y, line, want)
					}
					continue
				}
				th := theme.DefaultTheme().For(set)
				itemW := rowW
				var bar []railSeg
				if !compact {
					bar = []railSeg{RailBar(th, items[y])}
					itemW = rowW - 1
				}
				left, right, tail := RailRow(th, items[y], itemW, compact, set)
				want := railRowComposed(append(bar, left...), tail, right, rowW)
				if y == gripAt {
					if pad := rowW - cells.Cells(want); pad > 0 {
						want += strings.Repeat(" ", pad)
					}
					want += theme.DefaultTheme().Glyphs.Grip
				}
				if want != line {
					t.Fatalf("set=%v w=%d row %d kind %d composes %q, rail paints %q",
						set, w, y, items[y].Kind, want, line)
				}
				if n := len(bar) + segment.SegCells(left) + segment.SegCells(tail) + segment.SegCells(right); n > rowW {
					t.Fatalf("set=%v w=%d row %d is %d cells, want <= %d",
						set, w, y, n, rowW)
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
	left, right, tail := RailRow(theme.DefaultTheme(), ws, layout.DefaultRail, false, theme.GlyphUnicode)
	wantLeft := []railSeg{
		{Text: "\u25be", FG: theme.DefaultTheme().Palette.RailText},
		{Text: " ", FG: theme.DefaultTheme().Palette.RailText},
		{Text: "main", FG: theme.DefaultTheme().Palette.RailText, Bold: true},
		{Text: " " + theme.DefaultTheme().RailMarks.Workspace, FG: theme.DefaultTheme().Palette.RailText},
	}
	wantRight := []railSeg{{Text: "2", FG: theme.DefaultTheme().Palette.RailDim}}
	if !reflect.DeepEqual(left, wantLeft) || !reflect.DeepEqual(right, wantRight) || tail != nil {
		t.Fatalf("workspace row = %+v / %+v / %+v, want %+v / %+v / nil",
			left, right, tail, wantLeft, wantRight)
	}

	// An unfolded window line carries the caret, the folder, the branch tail
	// and the stats.
	win := railRowFind(t, items, Window, "one")
	left, right, tail = RailRow(theme.DefaultTheme(), win, layout.DefaultRail, false, theme.GlyphUnicode)
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
	left, right, tail = RailRow(theme.DefaultTheme(), folded, layout.DefaultRail, false, theme.GlyphUnicode)
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
	left, right, tail = RailRow(theme.DefaultTheme(), note, layout.DefaultRail, false, theme.GlyphUnicode)
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
	mark, glyphColour := RailMark(true, "", theme.GlyphUnicode), theme.DefaultTheme().Palette.RailWorking
	pane := railRowFind(t, items, Pane, "pi")
	left, right, tail = RailRow(theme.DefaultTheme(), pane, layout.DefaultRail, false, theme.GlyphUnicode)
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

	// The thin rail keeps the marker and the glyph, drops the indents, and
	// puts the project bar cell full left: blank here, since pi has no
	// project.
	left, right, tail = RailRow(theme.DefaultTheme(), pane, layout.CollapsedRail, true, theme.GlyphUnicode)
	wantLeft = []railSeg{
		{Text: " ", FG: theme.DefaultTheme().Palette.RailDim},
		{Text: strings.TrimSuffix(mark, " "), FG: glyphColour},
		{Text: "\u25cf", FG: glyphColour},
	}
	if !reflect.DeepEqual(left, wantLeft) || right != nil || tail != nil {
		t.Fatalf("thin pane row = %+v / %+v / %+v, want %+v / nil / nil",
			left, right, tail, wantLeft)
	}

	// A gap row composes nothing at all.
	gap := railRowFind(t, items, Gap, "")
	left, right, tail = RailRow(theme.DefaultTheme(), gap, layout.DefaultRail, false, theme.GlyphUnicode)
	if left != nil || right != nil || tail != nil {
		t.Fatalf("gap row = %+v / %+v / %+v, want nothing", left, right, tail)
	}

	// The one thing RailRow does not return is the row's own ground and weight:
	// the focused pane row paints the focus fill bold, and every other row the
	// rail fill plain.
	var b bytes.Buffer
	DrawRail(&b, theme.DefaultTheme(), snap, theme.GlyphUnicode, -1, nil)
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
	// A run writes one escape: weight, foreground, then the ground, so the
	// focused row's first run carries bold and the focus fill together.
	fill := theme.DefaultTheme().Palette.RailFocusBG
	fillTail := ";" + fill.SGR(true)
	row := out[strings.Index(out, fmt.Sprintf("\x1b[%d;1H", focusRow+1)):]
	at := strings.Index(row, "\x1b[0;1;")
	if at < 0 {
		t.Fatalf("focused row %d is not bold: %q", focusRow, out)
	}
	esc := row[at:]
	if end := strings.Index(esc, "m"); end < 0 || !strings.HasSuffix(esc[:end], fillTail) {
		t.Fatalf("focused row %d is not bold on the focus fill: %q", focusRow, esc)
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
		left, right, tail := RailRow(th, it, layout.DefaultRail, false, theme.GlyphUnicode)
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
// stats tail stands down first, then the branch yields whole, then the label
// elides, and the stats never lose a cell.
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
	left, right, tail := RailRow(theme.DefaultTheme(), win, layout.DefaultRail, false, theme.GlyphUnicode)
	if tail == nil {
		t.Fatalf("branch dropped at %d columns: %+v", layout.DefaultRail, left)
	}
	if !reflect.DeepEqual(right, full) {
		t.Fatalf("stats at %d columns = %+v, want the whole pair", layout.DefaultRail, right)
	}

	// Twenty columns: the folder fits beside the pair, so the branch goes
	// whole and the folder stays intact.
	left, right, tail = RailRow(theme.DefaultTheme(), win, 20, false, theme.GlyphUnicode)
	if tail != nil {
		t.Fatalf("branch survived at 20 columns: %+v", tail)
	}
	if !reflect.DeepEqual(right, full) {
		t.Fatalf("stats at 20 columns = %+v, want the whole pair", right)
	}
	if strings.Contains(railRowComposed(left, tail, right, 20), "\u2026") {
		t.Fatalf("folder elided while it fit: %q", railRowComposed(left, tail, right, 20))
	}

	// Twelve columns: the branch yields whole and the folder stays intact.
	left, right, tail = RailRow(theme.DefaultTheme(), win, 12, false, theme.GlyphUnicode)
	if tail != nil {
		t.Fatalf("branch survived at 12 columns: %+v", tail)
	}
	if right != nil {
		t.Fatalf("stats survived at 12 columns: %+v", right)
	}
	if got := railRowComposed(left, tail, right, 12); got != "  \u25be rail" {
		t.Fatalf("row at 12 columns = %q, want %q", got, "  \u25be rail")
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
		// The grip strip owns the rail's last column, so the row composes at w-1,
		// and the project bar takes column 0 of that.
		rowW := w - 1
		th := theme.DefaultTheme()
		bar := []railSeg{RailBar(th, it)}
		left, right, tail = RailRow(th, it, rowW-1, layout.RailCompact(w), theme.GlyphUnicode)
		if right != nil && !reflect.DeepEqual(right, full) {
			t.Fatalf("stats at %d columns = %+v, want the whole pair or none", w, right)
		}
		if n := len(bar) + segment.SegCells(left) + segment.SegCells(tail) + segment.SegCells(right); n > rowW {
			t.Fatalf("row at %d columns is %d cells: %+v / %+v / %+v",
				w, n, left, tail, right)
		}
		if got, line := railRowComposed(append(bar, left...), tail, right, rowW), railRows(t, snap, theme.GlyphUnicode)[1]; got != line {
			t.Fatalf("row at %d columns composes %q, rail paints %q", w, got, line)
		}
	}
}

// railSeg is the name the rail gave a styled run before seg.go made the rail
// and the bar one type. The moved test still spells it.
type railSeg = segment.Seg

// railRows draws a rail and returns the text of each row inside the rail width.
func railRows(t *testing.T, snap *wire.Snapshot, set theme.GlyphSet) []string {
	t.Helper()
	var b bytes.Buffer
	DrawRail(&b, theme.DefaultTheme().For(set), snap, set, -1, nil)
	term := vt.NewTerm(snap.Cols, snap.Rows)
	term.Write(b.Bytes())
	term.Flush()
	out := make([]string, snap.Rows-1)
	for y := range out {
		var sb strings.Builder
		for x := 0; x < snap.Rail; x++ {
			g := cellOf(term, x, y)
			if g.Char == 0 {
				sb.WriteByte(' ')
				continue
			}
			sb.WriteRune(g.Char)
		}
		out[y] = strings.TrimRight(sb.String(), " ")
	}
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
	if got := RailFoldAt(snap, 3, winIndex); got != "w1" {
		t.Fatalf("RailFoldAt(3,%d) = %q, want w1", winIndex, got)
	}
	if got := RailFoldAt(snap, 2, winIndex); got != "" {
		t.Fatalf("RailFoldAt(2,%d) = %q, want empty", winIndex, got)
	}
	if got := RailFoldAt(snap, 3, wsIndex); got != "" {
		t.Fatalf("RailFoldAt(3,%d) = %q on a workspace row, want empty", wsIndex, got)
	}
	if got := RailFoldAt(snap, 3, noteIndex); got != "" {
		t.Fatalf("RailFoldAt(3,%d) = %q on the note row, want empty", noteIndex, got)
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
	if got := RailFoldAt(snap, 1, wsIndex); got != "s1" {
		t.Fatalf("RailFoldAt(1,%d) = %q, want s1", wsIndex, got)
	}
	if got := RailFoldAt(snap, 2, wsIndex); got != "" {
		t.Fatalf("RailFoldAt(2,%d) = %q, want empty", wsIndex, got)
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
	if got := RailFoldAt(snap, 1, paneIndex); got != "" {
		t.Fatalf("RailFoldAt(1,%d) = %q on a pane row", paneIndex, got)
	}
}

// TestRailASCIIGlyphs proves the ascii set reaches the rail: no glyph from the
// Unicode set survives, so a terminal with no Unicode still draws the sidebar.
func TestRailASCIIGlyphs(t *testing.T) {
	snap := railRowSnap(layout.DefaultRail)
	var b bytes.Buffer
	DrawRail(&b, theme.DefaultTheme().For(theme.GlyphASCII), snap, theme.GlyphASCII, -1, nil)
	out := b.String()
	for _, glyph := range []string{"▾", "▸", "•", "·", "●", "○", "▲", "✓", "✕", "▶", "▌"} {
		if strings.Contains(out, glyph) {
			t.Fatalf("ascii rail carries %q", glyph)
		}
	}
}

// railProjectSnap is a rail with a two-workspace project, an untagged
// workspace and a second project, all unfolded down to their panes.
func railProjectSnap(rail int) *wire.Snapshot {
	snap := &wire.Snapshot{
		Cols: 100, Rows: 20, Rail: rail,
		Root: &layout.Node{Pane: "a1p1"}, Focus: "a1p1",
	}
	snap.All = []wire.AllPane{
		{
			ID: "a1p1", Name: "one", Workspace: "s1", WorkspaceName: "alpha-main",
			Project: "alpha", Window: "a1", WindowName: "one",
			WorkspaceUnfolded: true, WindowUnfolded: true,
			Focus: true, WorkspaceActive: true,
			Cwd: "/work/proj/web", Root: "/work/proj", InRepo: true,
			Branch: "web", Dirty: true, Added: 3, Deleted: 1,
		},
		{
			ID: "a2p1", Name: "two", Workspace: "s2", WorkspaceName: "alpha-lane",
			Project: "alpha", Window: "a2", WindowName: "two",
			WorkspaceUnfolded: true, WindowUnfolded: true,
		},
		{
			ID: "u1p1", Name: "loose", Workspace: "s3", WorkspaceName: "loose",
			Window: "u1", WindowName: "three",
			WorkspaceUnfolded: true, WindowUnfolded: true,
		},
		{
			ID: "b1p1", Name: "four", Workspace: "s4", WorkspaceName: "beta",
			Project: "beta", Window: "b1", WindowName: "four",
			WorkspaceUnfolded: true, WindowUnfolded: true,
		},
	}
	return snap
}

// railColour is theme RGB as the terminal reports it.
func railColour(c theme.RGB) vt.Color {
	return vt.RGBColor(uint8(c.R), uint8(c.G), uint8(c.B))
}

// TestRailProjectGroup proves a project names its group once and ties every
// row of it with the theme's bar in the project's accent. An untagged
// workspace and the gap after it carry no bar.
func TestRailProjectGroup(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	snap := railProjectSnap(layout.DefaultRail)
	items := RailItems(snap)

	var projects []int
	for i, it := range items {
		if it.Kind == Project {
			projects = append(projects, i)
		}
	}
	if len(projects) != 2 {
		t.Fatalf("rail has %d project rows, want 2: %+v", len(projects), items)
	}
	alpha, beta := items[projects[0]], items[projects[1]]
	if alpha.Text != "alpha" || alpha.Count != 2 {
		t.Fatalf("alpha row = %+v, want the name and two workspaces", alpha)
	}
	if beta.Text != "beta" || beta.Count != 1 {
		t.Fatalf("beta row = %+v, want the name and one workspace", beta)
	}
	if projects[0] != 0 || projects[1] >= len(items) {
		t.Fatalf("project rows sit at %v, want alpha first", projects)
	}
	// Every row from the alpha header through the gap between its two
	// workspaces carries the project and its accent. The gap before the loose
	// workspace, and the loose block itself, carry neither.
	loose := -1
	for i, it := range items {
		if it.Kind == Workspace && it.Workspace == "s3" {
			loose = i
		}
	}
	if loose < 0 {
		t.Fatalf("fixture has no loose workspace row: %+v", items)
	}
	for i, it := range items {
		inAlpha := i >= projects[0] && i < loose-1
		if inAlpha {
			if it.Project != "alpha" || it.ProjectAccent != alpha.ProjectAccent {
				t.Fatalf("row %d inside alpha = %+v, want the group's project and accent", i, it)
			}
			continue
		}
		if it.Kind != Project && it.Project == "alpha" {
			t.Fatalf("row %d outside alpha still carries it: %+v", i, it)
		}
	}
	// The gap between alpha's two workspaces is inside the group, so the bar
	// runs on through it. The gap before the loose workspace is outside.
	sawAlphaGap, sawLooseGap := false, false
	for _, it := range items {
		if it.Kind != Gap {
			continue
		}
		if it.Project == "alpha" {
			sawAlphaGap = true
		} else {
			sawLooseGap = true
		}
	}
	if !sawAlphaGap || !sawLooseGap {
		t.Fatalf("gaps = alpha %v loose %v, want one of each", sawAlphaGap, sawLooseGap)
	}

	// The painter puts the bar in column 0 of every row of the group, header
	// included, and a blank cell outside it, in both glyph forms.
	for _, set := range []theme.GlyphSet{theme.GlyphUnicode, theme.GlyphNerd} {
		glyphs := th.Glyphs.WorkspaceBar
		painted := railRows(t, snap, set)
		for y, line := range painted {
			if y >= len(items) || y == snap.Rows-2 {
				continue
			}
			it := items[y]
			if it.Project == "" {
				continue
			}
			if !strings.HasPrefix(line, glyphs) {
				t.Fatalf("set=%v row %d %+v starts %q, want the bar %q", set, y, it, line, glyphs)
			}
		}
	}
	term := vt.NewTerm(snap.Cols, snap.Rows)
	var b bytes.Buffer
	DrawRail(&b, th, snap, theme.GlyphUnicode, -1, nil)
	term.Write(b.Bytes())
	term.Flush()
	for y, it := range items {
		g := cellOf(term, 0, y)
		if it.Project == "" {
			if !thinBlank(g) {
				t.Fatalf("row %d outside a project holds %q in column 0", y, g.Char)
			}
			continue
		}
		if g.Char != theBar() {
			t.Fatalf("row %d bar = %q, want %q", y, g.Char, th.Glyphs.WorkspaceBar)
		}
		want := railColour(th.Accents[it.ProjectAccent])
		if g.FG != want {
			t.Fatalf("row %d bar colour = %v, want %v", y, g.FG, want)
		}
	}
	// The header has a fold caret: a project row folds its whole group.
	for i, it := range items {
		if it.Kind == Project && RailFoldAt(snap, 1, i) != wire.ProjectTargetPrefix+it.Project {
			t.Fatalf("project row %d does not answer a project fold", i)
		}
	}
}

// TestRailUntaggedWorkspaceHasNoProject proves a snapshot with no project
// names paints no project row and no bar column.
func TestRailUntaggedWorkspaceHasNoProject(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railRowSnap(layout.DefaultRail)
	for _, it := range RailItems(snap) {
		if it.Kind == Project {
			t.Fatalf("untagged rail has a project row: %+v", it)
		}
		if it.Project != "" || it.ProjectAccent != 0 {
			t.Fatalf("untagged row carries a project: %+v", it)
		}
	}
	term := vt.NewTerm(snap.Cols, snap.Rows)
	var b bytes.Buffer
	DrawRail(&b, theme.DefaultTheme(), snap, theme.GlyphUnicode, -1, nil)
	term.Write(b.Bytes())
	term.Flush()
	for y := 0; y < snap.Rows-2; y++ {
		if g := cellOf(term, 0, y); !thinBlank(g) {
			t.Fatalf("row %d outside a project holds %q in column 0", y, g.Char)
		}
	}
}

// TestRailProjectAccentCollision proves two projects that prefer one slot get
// different ones, the way workspaces did.
func TestRailProjectAccentCollision(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	slots := len(theme.DefaultTheme().Accents)
	bySlot := map[int]string{}
	var first, second string
	for i := 0; i < 100 && first == ""; i++ {
		name := fmt.Sprintf("proj-%d", i)
		slot := RailAccentSlot(name)
		if other, ok := bySlot[slot]; ok {
			first, second = other, name
			break
		}
		bySlot[slot] = name
	}
	if first == "" {
		t.Fatal("found no two project names that prefer one slot")
	}
	snap := &wire.Snapshot{Cols: 100, Rows: 8, Rail: layout.DefaultRail}
	snap.All = []wire.AllPane{
		{ID: "a", Workspace: "s1", WorkspaceName: "a", Project: first, Window: "w1", WorkspaceUnfolded: true},
		{ID: "b", Workspace: "s2", WorkspaceName: "b", Project: second, Window: "w2", WorkspaceUnfolded: true},
	}
	slotOf := map[string]int{}
	for _, it := range RailItems(snap) {
		if it.Kind == Project {
			slotOf[it.Text] = it.ProjectAccent
		}
	}
	if slotOf[first] == slotOf[second] {
		t.Fatalf("%q and %q share accent slot %d", first, second, slotOf[first])
	}
	if slotOf[first] >= slots || slotOf[second] >= slots {
		t.Fatalf("accent slots %v run past the theme's %d", slotOf, slots)
	}
}

// TestRailFoldCaretAfterBar proves the fold carets sit one column right of
// where they sat before the bar took column 0.
func TestRailFoldCaretAfterBar(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railProjectSnap(layout.DefaultRail)
	items := RailItems(snap)
	wsIndex, winIndex := -1, -1
	for i, it := range items {
		if it.Kind == Workspace && wsIndex < 0 {
			wsIndex = i
		}
		if it.Kind == Window && winIndex < 0 {
			winIndex = i
		}
	}
	if wsIndex < 0 || winIndex < 0 {
		t.Fatalf("fixture has no workspace or window row: %+v", items)
	}
	if got := RailFoldAt(snap, 1, wsIndex); got != items[wsIndex].Workspace {
		t.Fatalf("RailFoldAt(1,%d) = %q, want %q", wsIndex, got, items[wsIndex].Workspace)
	}
	if got := RailFoldAt(snap, 0, wsIndex); got != "" {
		t.Fatalf("RailFoldAt(0,%d) = %q, want empty", wsIndex, got)
	}
	if got := RailFoldAt(snap, 3, winIndex); got != items[winIndex].Window {
		t.Fatalf("RailFoldAt(3,%d) = %q, want %q", winIndex, got, items[winIndex].Window)
	}
	if got := RailFoldAt(snap, 2, winIndex); got != "" {
		t.Fatalf("RailFoldAt(2,%d) = %q, want empty", winIndex, got)
	}
	if got := RailPaneAt(snap, 2, winIndex); got != "" {
		t.Fatalf("RailPaneAt(2,%d) = %q on a window row, want empty", winIndex, got)
	}
}

// TestThinRailProjectTrack proves the thin rail keeps the project track:
// every pane row paints its project's bar in column 0, and no project row
// and no project row ever appears.
func TestThinRailProjectTrack(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railProjectSnap(layout.CollapsedRail)
	items := RailItems(snap)
	for _, it := range items {
		if it.Kind != Pane {
			t.Fatalf("thin rail has a %d row: %+v", it.Kind, it)
		}
	}
	term := thinScreen(t, snap)

	// Project rows paint the bar in column 0, in the project's accent; the
	// loose row paints no bar. Two live projects never share an accent, and
	// no row is a project row.
	for y, it := range items {
		bar := cellOf(term, 0, y)
		if it.Project == "" {
			if bar.Char == theBar() {
				t.Fatalf("loose thin row %d paints the bar", y)
			}
			continue
		}
		if bar.Char != theBar() {
			t.Fatalf("thin row %d for %s has no bar in column 0", y, it.Project)
		}
		if want := theme.DefaultTheme().Accents[it.ProjectAccent]; bar.FG != railColour(want) {
			t.Fatalf("thin row %d bar = %v, want the %s accent %v", y, bar.FG, it.Project, want)
		}
	}
	if items[0].Project != "alpha" || items[2].Project != "" || items[3].Project != "beta" {
		t.Fatalf("thin rows carry %q / %q / %q, want alpha / none / beta",
			items[0].Project, items[2].Project, items[3].Project)
	}
	if items[3].ProjectAccent == items[0].ProjectAccent {
		t.Fatalf("projects alpha and beta share accent slot %d", items[0].ProjectAccent)
	}
	if got := RailFoldAt(snap, 1, 0); got != "" {
		t.Fatalf("thin rail answers a fold at 1: %q", got)
	}
}

// theBar is the first rune of the default theme's workspace bar.
func theBar() rune {
	for _, r := range theme.DefaultTheme().Glyphs.WorkspaceBar {
		return r
	}
	return 0
}

// TestRailFoldedProjectHidesGroup proves a folded project paints its header
// with a closed caret and hides every workspace, window and pane of the group.
func TestRailFoldedProjectHidesGroup(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railProjectSnap(layout.DefaultRail)
	for i := range snap.All {
		if snap.All[i].Project == "alpha" {
			snap.All[i].ProjectFolded = true
		}
	}
	items := RailItems(snap)

	var alpha *RailItem
	beta, loose := false, false
	for i := range items {
		it := &items[i]
		if it.Kind == Project && it.Text == "alpha" {
			alpha = it
		}
		if it.Project == "alpha" && it.Kind != Project {
			t.Fatalf("folded alpha still paints %+v", *it)
		}
		if it.Kind == Project && it.Text == "beta" {
			beta = true
		}
		if it.Kind == Workspace && it.Workspace == "s3" {
			loose = true
		}
	}
	if alpha == nil {
		t.Fatal("folded alpha has no project row")
	}
	if !alpha.Folded {
		t.Fatal("alpha project row is not folded")
	}
	if !beta || !loose {
		t.Fatalf("folded alpha hid beta or loose: %+v", items)
	}

	th := theme.DefaultTheme()
	left, _, _ := RailRow(th, *alpha, layout.DefaultRail, false, theme.GlyphUnicode)
	if len(left) == 0 || left[0].Text != th.Glyphs.FoldClosed {
		t.Fatalf("project caret = %+v, want the closed glyph", left)
	}
}

// TestRailHoverBackground paints the hovered row with RailHoverBG. The
// focused pane row keeps its stronger ground, a gap row never lights, and a
// hover past the last item changes nothing.
func TestRailHoverBackground(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	snap := railRowSnap(layout.DefaultRail)
	items := RailItems(snap)
	windowAt, focusAt, gapAt := -1, -1, -1
	for y, it := range items {
		switch {
		case it.Kind == Window && windowAt < 0:
			windowAt = y
		case it.Kind == Pane && it.Focus:
			focusAt = y
		case it.Kind == Gap:
			gapAt = y
		}
	}
	if windowAt < 0 || focusAt < 0 || gapAt < 0 {
		t.Fatalf("fixture lost a row: window %d focus %d gap %d", windowAt, focusAt, gapAt)
	}
	draw := func(hover int) string {
		var b bytes.Buffer
		DrawRail(&b, th, snap, theme.GlyphUnicode, hover, nil)
		return b.String()
	}
	rowStart := fmt.Sprintf("\x1b[%d;1H\x1b[0m", windowAt+1) + th.Palette.RailHoverBG.BG()
	if out := draw(windowAt); !strings.Contains(out, rowStart) {
		t.Fatalf("hovered window row is not on the hover fill: %q", out)
	}
	// The focused pane row wins over the hover.
	focusStart := fmt.Sprintf("\x1b[%d;1H\x1b[0m", focusAt+1) + th.Palette.RailFocusBG.BG()
	if out := draw(focusAt); !strings.Contains(out, focusStart) {
		t.Fatalf("hovered focused row lost the focus fill: %q", out)
	}
	// A gap row stays on the rail fill.
	gapStart := fmt.Sprintf("\x1b[%d;1H\x1b[0m", gapAt+1) + th.Palette.RailBG.BG()
	if out := draw(gapAt); !strings.Contains(out, gapStart) {
		t.Fatalf("hovered gap row left the rail fill: %q", out)
	}
	// A hover past the last item paints the plain rail.
	if draw(len(items)+3) != draw(-1) {
		t.Fatal("an out-of-range hover changed the paint")
	}
}

// TestRailProjectAccentCarried proves the painter paints the slot the
// snapshot carries and falls back to the live allocator for a snapshot that
// carries none.
func TestRailProjectAccentCarried(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	slots := len(theme.DefaultTheme().Accents)
	base := []wire.AllPane{
		{ID: "a1", Workspace: "s1", WorkspaceName: "one", Project: "alpha", Window: "w1", WindowName: "one", WorkspaceUnfolded: true, WindowUnfolded: true},
		{ID: "b1", Workspace: "s2", WorkspaceName: "two", Project: "beta", Window: "w2", WindowName: "two", WorkspaceUnfolded: true, WindowUnfolded: true},
	}
	// carried is what a server pinned: one slot past each name's preference,
	// so it disagrees with the live walk and the test can tell trust from
	// fallback.
	carried := map[string]int{
		"alpha": (RailAccentSlot("alpha") + 1) % slots,
		"beta":  (RailAccentSlot("beta") + 1) % slots,
	}
	// live is what the fallback must produce for these names: the
	// preferred-then-free walk over the snapshot's own names.
	used := map[int]bool{}
	live := map[string]int{}
	for _, name := range []string{"alpha", "beta"} {
		pref := RailAccentSlot(name)
		for i := 0; i < slots; i++ {
			slot := (pref + i) % slots
			if !used[slot] {
				used[slot] = true
				live[name] = slot
				break
			}
		}
	}
	snapOf := func(set bool) *wire.Snapshot {
		snap := &wire.Snapshot{Cols: 100, Rows: 8, Rail: layout.DefaultRail}
		snap.All = append([]wire.AllPane(nil), base...)
		if set {
			for i := range snap.All {
				snap.All[i].ProjectAccent = carried[snap.All[i].Project]
				snap.All[i].ProjectAccentSet = true
			}
		}
		return snap
	}
	slotOf := func(snap *wire.Snapshot) map[string]int {
		out := map[string]int{}
		for _, it := range RailItems(snap) {
			if it.Kind == Project {
				out[it.Text] = it.ProjectAccent
			}
		}
		return out
	}
	got := slotOf(snapOf(true))
	for name, want := range carried {
		if got[name] != want {
			t.Fatalf("carried snapshot paints %q in slot %d, want the carried %d", name, got[name], want)
		}
	}
	if got["alpha"] == live["alpha"] && got["beta"] == live["beta"] {
		t.Fatalf("carried slots %v match the live walk, so the test cannot tell trust from fallback", got)
	}
	got = slotOf(snapOf(false))
	for name, want := range live {
		if got[name] != want {
			t.Fatalf("bare snapshot paints %q in slot %d, want the live walk's %d", name, got[name], want)
		}
	}
}

// TestRailStatsTailPaysRent proves the window row's stats tail stands down
// before it starves the label. The tail renders while the label fits beside
// it, and while a label that must elide still keeps half the row; it yields
// before the branch, the branch before the label, and no width ever renders
// a truncated pair.
func TestRailStatsTailPaysRent(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()

	// A wide pair: the dirty dot plus "+12 -4", eight cells, beside a
	// six-cell label. Four cells of chrome put the label block at ten.
	wide := RailItem{
		Kind: Window, Text: "two", Label: "window",
		Branch: "b", InRepo: true, Dirty: true,
		Added: 12, Deleted: 4,
	}
	const pair = "\u25cf +12 -4"

	// Eighteen columns: the pair would elide the label below half the row,
	// so it stands down and the short branch keeps the cells it gave back.
	// The tail drops before the branch.
	left, right, tail := RailRow(th, wide, 18, false, theme.GlyphUnicode)
	if right != nil {
		t.Fatalf("pair survived a row it starved: %+v", right)
	}
	if tail == nil {
		t.Fatalf("branch dropped beside a standing-down tail: %+v", left)
	}

	// The same row with a long branch: the branch yields before the label,
	// and the label stays intact.
	longBranch := wide
	longBranch.Branch = "feature/long"
	left, right, tail = RailRow(th, longBranch, 18, false, theme.GlyphUnicode)
	if right != nil || tail != nil {
		t.Fatalf("tails survived a row they starved: right %+v tail %+v", right, tail)
	}
	if strings.Contains(railSegTextsJoined(left), "\u2026") {
		t.Fatalf("label elided while it fit: %v", left)
	}

	// A long label pins the half: at twenty-four columns the seven-cell
	// pair renders and the label elides to exactly twelve cells, half the
	// row. One below, the pair stands down and the label alone elides.
	tall := RailItem{
		Kind: Window, Text: "two", Label: strings.Repeat("w", 40),
		Branch: "feature/long", InRepo: true, Dirty: true,
		Added: 1, Deleted: 1,
	}
	const small = "\u25cf +1 -1"
	left, right, tail = RailRow(th, tall, 24, false, theme.GlyphUnicode)
	if got := strings.Join(railSegTexts(right), ""); got != small {
		t.Fatalf("stats beside a long label = %q, want the whole pair %q", got, small)
	}
	if n := segment.SegCells(left); n != 16 || n-windowChrome != 12 {
		t.Fatalf("elided label kept %d of 24 cells, want exactly half", n-windowChrome)
	}
	left, right, tail = RailRow(th, tall, 23, false, theme.GlyphUnicode)
	if right != nil {
		t.Fatalf("pair survived one below the floor: %+v", right)
	}
	if n := segment.SegCells(left); n != 23 {
		t.Fatalf("label alone kept %d of 23 cells, want the whole row", n)
	}

	// A label that fits keeps its tail: the clean word renders beside it,
	// the way a twenty-column rail has always shown it.
	clean := RailItem{Kind: Window, Text: "two", Label: "window", Branch: "b", InRepo: true}
	left, right, tail = RailRow(th, clean, 18, false, theme.GlyphUnicode)
	if got := strings.Join(railSegTexts(right), ""); got != "clean" {
		t.Fatalf("clean tail beside a fitting label = %q, want clean", got)
	}

	// Full width renders as it always did. The long label elides exactly as
	// it did, keeping its half beside the whole pair.
	left, right, tail = RailRow(th, tall, 32, false, theme.GlyphUnicode)
	if got := strings.Join(railSegTexts(right), ""); got != small {
		t.Fatalf("stats at full width = %q, want the whole pair", got)
	}
	if n := segment.SegCells(left); n != 24 || n-windowChrome < 16 {
		t.Fatalf("elided label kept %d of 32 cells, want at least half", n-windowChrome)
	}
	// And a row with room keeps pair, branch and label whole.
	left, right, tail = RailRow(th, longBranch, 34, false, theme.GlyphUnicode)
	if got := strings.Join(railSegTexts(right), ""); got != pair {
		t.Fatalf("stats at 34 columns = %q, want the whole pair", got)
	}
	if tail == nil {
		t.Fatalf("branch dropped at 34 columns: %+v", left)
	}
	if strings.Contains(railSegTextsJoined(left), "\u2026") {
		t.Fatalf("label elided while it fit: %v", left)
	}

	// No width renders a truncated tail: the pair and "clean" go whole or
	// the right edge stays empty.
	for _, tc := range []struct {
		name string
		it   RailItem
		want map[string]bool
	}{
		{"dirty", tall, map[string]bool{"": true, small: true}},
		{"clean", func() RailItem { c := tall; c.Dirty, c.Added, c.Deleted = false, 0, 0; return c }(),
			map[string]bool{"": true, "clean": true}},
	} {
		for w := 1; w <= 40; w++ {
			if layout.RailCompact(w) {
				continue
			}
			_, right, _ := RailRow(th, tc.it, w, false, theme.GlyphUnicode)
			if got := strings.Join(railSegTexts(right), ""); !tc.want[got] {
				t.Fatalf("%s tail at %d columns = %q, want whole or nothing", tc.name, w, got)
			}
		}
	}
}

// railSegTexts lists the text of each run, for asserts that care what a row
// says and not how it is styled.
func railSegTexts(runs []railSeg) []string {
	out := make([]string, len(runs))
	for i, s := range runs {
		out[i] = s.Text
	}
	return out
}

// railSegTextsJoined is the runs' text as one string.
func railSegTextsJoined(runs []railSeg) string {
	return strings.Join(railSegTexts(runs), "")
}
