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
			Focus: true, WorkspaceActive: true, WindowActive: true, WorkspaceUnfolded: true,
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
	paneLine := compose(railRowFind(t, items, Pane, "pi"))
	if wsLine == paneLine {
		t.Fatalf("rows are not distinct: workspace %q, pane %q", wsLine, paneLine)
	}
	if !strings.Contains(wsLine, th.RailMarks.Workspace) {
		t.Fatalf("workspace row lacks its mark %q: %q", th.RailMarks.Workspace, wsLine)
	}
	if !strings.Contains(paneLine, th.RailMarks.Pane) {
		t.Fatalf("pane row lacks its mark %q: %q", th.RailMarks.Pane, paneLine)
	}
	if !strings.HasPrefix(wsLine, th.Glyphs.FoldOpen) {
		t.Fatalf("workspace row does not start with the caret: %q", wsLine)
	}
	if !strings.HasPrefix(paneLine, "      ") {
		t.Fatalf("pane row does not start with the pane indent: %q", paneLine)
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

func TestRailItemsFlattenedOrder(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railRowSnap(layout.DefaultRail)
	items := RailItems(snap)
	// Expected order: Workspace "main", Pane "pi", Pane "beta", Gap, Workspace "work", Pane "gamma"
	if len(items) != 6 {
		t.Fatalf("got %d items, want 6", len(items))
	}
	kinds := []RailKind{Workspace, Pane, Pane, Gap, Workspace, Pane}
	for i, k := range kinds {
		if items[i].Kind != k {
			t.Fatalf("item %d kind = %d, want %d", i, items[i].Kind, k)
		}
	}
	if items[0].Folded {
		t.Fatal("workspace main is folded, want unfolded")
	}
	if items[4].Folded {
		t.Fatal("workspace work is folded, want unfolded")
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
	// Expected: Workspace "main" (folded), Gap, Workspace "work", Pane "gamma"
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
	if items[3].Kind != Pane || items[3].Text != "gamma" {
		t.Fatalf("item 3 = %+v, want Pane gamma", items[3])
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
