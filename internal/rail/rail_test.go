package rail

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"dm/internal/cells"
	"dm/internal/layout"
	"dm/internal/segment"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

// railRowSnap is a rail with every row kind on it: a workspace header, an
// unfolded window line with a folder, its stats and the git mark, that
// window's note, the focused pane, a folded window line outside a repo, the
// gap, and a second workspace. Rows 8 and up are past the last item.
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
// text, colour and weight, and which run is the tail. Every group and window row
// carries its rolled-up state in the last content cell; the window row carries
// the git field, the mark and one cell of air before it.
func TestRailRowSegments(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	p := th.Palette
	snap := railRowSnap(layout.DefaultRail)
	items := RailItems(snap)
	state := func(name string) railSeg {
		glyph, colour := RailGlyph(th, name, theme.GlyphUnicode)
		return railSeg{Text: glyph, FG: colour}
	}

	// The open workspace carries no count: its windows are on the rail. Its
	// state is the worst of them, which is the working pane under w1.
	ws := railRowFind(t, items, Workspace, "main")
	left, right, tail := RailRow(th, ws, layout.DefaultRail, false, theme.GlyphUnicode)
	wantLeft := []railSeg{
		{Text: "\u25be", FG: p.RailText},
		{Text: " ", FG: p.RailText},
		{Text: "main", FG: p.RailText, Bold: true},
	}
	wantRight := []railSeg{state("working")}
	if !reflect.DeepEqual(left, wantLeft) || !reflect.DeepEqual(right, wantRight) || tail != nil {
		t.Fatalf("workspace row = %+v / %+v / %+v, want %+v / %+v / nil",
			left, right, tail, wantLeft, wantRight)
	}

	// An unfolded window line carries the caret, the folder, the two six cell
	// git columns, the mark, one cell of air and the state.
	win := railRowFind(t, items, Window, "one")
	left, right, tail = RailRow(th, win, layout.DefaultRail, false, theme.GlyphUnicode)
	wantLeft = []railSeg{
		{Text: "  ", FG: p.RailWindow},
		{Text: "\u25be", FG: p.RailWindow},
		{Text: " ", FG: p.RailWindow},
		{Text: "rail", FG: p.RailWindow},
	}
	wantRight = []railSeg{
		{Text: "+12   ", FG: p.RailDiffAdd},
		{Text: "-4    ", FG: p.RailDiffDel},
		{Text: th.Glyphs.GitMark, FG: gitMarkQuiet(p)},
		{Text: " ", FG: p.RailDim},
		state("working"),
	}
	if !reflect.DeepEqual(left, wantLeft) || !reflect.DeepEqual(right, wantRight) || tail != nil {
		t.Fatalf("window line = %+v / %+v / %+v, want %+v / %+v / nil",
			left, right, tail, wantLeft, wantRight)
	}
	if strings.Contains(strings.Join(railSegTexts(left), ""), "dm-rail") {
		t.Fatalf("window line still paints the branch: %+v", left)
	}

	// A folded window line flips the caret and keeps its folder. Outside a
	// repository it carries no field and no mark, so its state is the whole
	// right run.
	folded := railRowFind(t, items, Window, "two")
	left, right, tail = RailRow(th, folded, layout.DefaultRail, false, theme.GlyphUnicode)
	wantLeft = []railSeg{
		{Text: "  ", FG: p.RailWindow},
		{Text: "\u25b8", FG: p.RailWindow},
		{Text: " ", FG: p.RailWindow},
		{Text: "other", FG: p.RailWindow},
	}
	wantRight = []railSeg{state("idle")}
	if folded.Folded != true {
		t.Fatalf("window two is not folded: %+v", folded)
	}
	if !reflect.DeepEqual(left, wantLeft) || !reflect.DeepEqual(right, wantRight) || tail != nil {
		t.Fatalf("folded window line = %+v / %+v / %+v, want %+v / %+v / nil",
			left, right, tail, wantLeft, wantRight)
	}

	// The note line is one dim run under the window line.
	note := railRowFind(t, items, WindowNote, "working hard")
	left, right, tail = RailRow(th, note, layout.DefaultRail, false, theme.GlyphUnicode)
	wantLeft = []railSeg{
		{Text: "    ", FG: p.RailDim},
		{Text: "working hard", FG: p.RailDim},
	}
	if !reflect.DeepEqual(left, wantLeft) || right != nil || tail != nil {
		t.Fatalf("note line = %+v / %+v / %+v, want %+v / nil / nil",
			left, right, tail, wantLeft)
	}

	// A focused pane row is four cells of indent, the agent mark, a space and the
	// name on rail text, with the state glyph alone at the margin: no dot. The
	// focus fill and the row's bold are the writer's, not the composition's.
	pane := railRowFind(t, items, Pane, "pi")
	agentMark, _ := utf8.DecodeRuneInString(RailMark(true, pane.App, theme.GlyphUnicode))
	left, right, tail = RailRow(th, pane, layout.DefaultRail, false, theme.GlyphUnicode)
	wantLeft = []railSeg{
		{Text: "    ", FG: p.RailText},
		{Text: string(agentMark), FG: p.RailHeader},
		{Text: " ", FG: p.RailText},
		{Text: "pi", FG: p.RailText},
	}
	wantRight = []railSeg{state("working")}
	if !pane.Focus {
		t.Fatalf("pane pi is not focused: %+v", pane)
	}
	if !reflect.DeepEqual(left, wantLeft) || !reflect.DeepEqual(right, wantRight) || tail != nil {
		t.Fatalf("pane row = %+v / %+v / %+v, want %+v / %+v / nil",
			left, right, tail, wantLeft, wantRight)
	}

	// The thin rail keeps the marker and the glyph, drops the indents, and
	// puts the project bar cell full left: blank here, since pi has no
	// project. The mark takes the rail's dim tone, the git cell is blank
	// because pi's repository says nothing, and the badge cell stays blank
	// because no question is listed on it.
	mark := RailMark(true, "", theme.GlyphUnicode)
	left, right, tail = RailRow(th, pane, layout.CollapsedRail, true, theme.GlyphUnicode)
	wantLeft = []railSeg{
		{Text: " ", FG: p.RailDim},
		{Text: strings.TrimSuffix(mark, " "), FG: p.RailDim},
		{Text: " ", FG: gitMarkQuiet(p)},
		{Text: "\u25cf", FG: p.RailWorking},
		{Text: " ", FG: p.RailDim},
	}
	if !reflect.DeepEqual(left, wantLeft) || right != nil || tail != nil {
		t.Fatalf("thin pane row = %+v / %+v / %+v, want %+v / nil / nil",
			left, right, tail, wantLeft)
	}

	// A gap row composes nothing at all.
	gap := railRowFind(t, items, Gap, "")
	left, right, tail = RailRow(th, gap, layout.DefaultRail, false, theme.GlyphUnicode)
	if left != nil || right != nil || tail != nil {
		t.Fatalf("gap row = %+v / %+v / %+v, want nothing", left, right, tail)
	}

	// The one thing RailRow does not return is the row's own ground and weight:
	// the focused pane row paints the focus fill bold, and every other row the
	// rail fill plain.
	var b bytes.Buffer
	DrawRail(&b, th, snap, theme.GlyphUnicode, Paint{Hover: -1})
	out := b.String()
	focusRow := -1
	for y := 0; y < snap.Rows-1; y++ {
		want := p.RailBG
		if y < len(items) {
			it := items[y]
			if it.Kind == Pane && it.Focus {
				focusRow, want = y, p.RailFocusBG
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
	fill := p.RailFocusBG
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

// TestRailRowLevelsDiffer proves the rail gives each row level its own look: a
// project and a workspace row are a caret and a name, the window row indents its
// caret, and the pane row is six cells of indent with no icon at all. The three
// composed rows differ, and no row paints a bullet. A pane row's only icon is its
// state glyph.
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
	// A workspace row paints no bullet: it reads as a project row does, and
	// only the caret and the name say which level it is.
	if th.RailMarks.Workspace != "" && strings.Contains(wsLine, th.RailMarks.Workspace) {
		t.Fatalf("workspace row still paints the mark %q: %q", th.RailMarks.Workspace, wsLine)
	}
	if !strings.Contains(winLine, th.Glyphs.FoldOpen) {
		t.Fatalf("window row lacks the caret %q: %q", th.Glyphs.FoldOpen, winLine)
	}
	if !strings.HasPrefix(wsLine, th.Glyphs.FoldOpen) {
		t.Fatalf("workspace row does not start with the caret: %q", wsLine)
	}
	if !strings.HasPrefix(winLine, "  "+th.Glyphs.FoldOpen) {
		t.Fatalf("window row does not start with the caret indent: %q", winLine)
	}
	// A pane row is four cells of indent, the agent mark in one cell, a space and
	// then the name at column 7: no dot. The mark is what says the pane runs pi.
	agent := strings.TrimSpace(RailMark(true, "", theme.GlyphUnicode))
	if !strings.HasPrefix(paneLine, "    "+agent+" pi") {
		t.Fatalf("pane row does not start with the indent and the agent mark: %q", paneLine)
	}
	if th.RailMarks.Pane != "" && strings.Contains(strings.TrimPrefix(paneLine, "    "+agent+" "), th.RailMarks.Pane) {
		t.Fatalf("pane row still carries the dot %q: %q", th.RailMarks.Pane, paneLine)
	}
}

// TestRailRowLadder proves the order a row gives things up: the git field stands
// down whole first, then the label elides, and the mark goes last, so a narrow
// row keeps its way into the facts box. The state keeps the margin at every
// width.
func TestRailRowLadder(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	row := RailItem{
		Kind: Window, Text: "w1", Label: "w1 dm", Window: "w1",
		InRepo: true, Dirty: true, Added: 12, Deleted: 3, State: "working",
	}
	state, _ := RailGlyph(th, "working", theme.GlyphUnicode)

	// At 26 cells the rail composes 24 for the row: the grip strip owns the
	// last column and the project bar the first. The field and the whole label
	// cannot both fit there, so the field stands down and the row reads caret,
	// label, mark, state.
	snap := railLadderSnap(26)
	painted := railRows(t, snap, theme.GlyphUnicode)
	winRow := -1
	for i, it := range RailItems(snap) {
		if it.Kind == Window {
			winRow = i
		}
	}
	if winRow < 0 {
		t.Fatalf("the ladder snapshot has no window row: %+v", RailItems(snap))
	}
	line := painted[winRow]
	for _, drop := range []string{"+", "12"} {
		if strings.Contains(line, drop) {
			t.Fatalf("the row at 26 cells paints the git field %q: %q", drop, line)
		}
	}
	if !strings.Contains(line, th.Glyphs.GitMark) {
		t.Fatalf("the row at 26 cells lost the mark: %q", line)
	}
	if got := cellOf(railTerm(t, snap, theme.GlyphUnicode), snap.Rail-2, winRow).Char; string(got) != state {
		t.Fatalf("the state at 26 cells = %q, want %q in the last content cell", got, state)
	}
	left, right, tail := RailRow(th, row, 24, false, theme.GlyphUnicode)
	if got, want := railRowComposed(left, tail, right, 24), "  \u25be w1 dm"; !strings.HasPrefix(got, want) {
		t.Fatalf("the row at 24 item cells = %q, want it to read %q", got, want)
	}
	if got := strings.Join(railSegTexts(right), ""); got != th.Glyphs.GitMark+" "+state {
		t.Fatalf("the right run at 24 item cells = %q, want the mark, the air and the state", got)
	}

	// The mark outranks the label: a label too long for the row elides while the
	// mark stays, at the same 26 rail cells. The mark is the only way into the
	// facts box, so it is the last thing the row gives up.
	long := row
	long.Label = strings.Repeat("n", 22)
	left, right, tail = RailRow(th, long, 24, false, theme.GlyphUnicode)
	if !strings.Contains(railSegTextsJoined(left), th.Glyphs.Ellipsis) {
		t.Fatalf("a long label at 24 item cells = %q, want it elided", railSegTextsJoined(left))
	}
	if !strings.Contains(railSegTextsJoined(right), th.Glyphs.GitMark) {
		t.Fatalf("a long label at 24 item cells lost the mark: %+v", right)
	}
	if n := segment.SegCells(left) + segment.SegCells(right); n > 24 {
		t.Fatalf("the elided row writes %d cells of 24: %+v / %+v", n, left, right)
	}
	if got := right[len(right)-1].Text; got != state {
		t.Fatalf("the elided row's state = %q, want %q at the margin", got, state)
	}

	// The ladder in order, over the widths a rail can take. A twelve cell label
	// shows all three steps in the widths a rail uses: the field from 32 cells,
	// the label elided from 20 down, and the mark from 17 down.
	var fieldAt, markAt, elideAt []int
	step := long
	step.Label = strings.Repeat("n", 12)
	for w := 2; w <= 40; w++ {
		left, right, _ := RailRow(th, step, w, false, theme.GlyphUnicode)
		text := railSegTextsJoined(left)
		if strings.Contains(railSegTextsJoined(right), "+") {
			fieldAt = append(fieldAt, w)
		}
		if strings.Contains(railSegTextsJoined(right), th.Glyphs.GitMark) {
			markAt = append(markAt, w)
		}
		if strings.Contains(text, th.Glyphs.Ellipsis) {
			elideAt = append(elideAt, w)
		}
		if len(right) == 0 || right[len(right)-1].Text != state {
			t.Fatalf("at %d cells the state is not the margin: %+v", w, right)
		}
		if n := segment.SegCells(left) + segment.SegCells(right); n > w {
			t.Fatalf("at %d cells the row writes %d: %+v / %+v", w, n, left, right)
		}
	}
	if len(fieldAt) == 0 || len(markAt) == 0 || len(elideAt) == 0 {
		t.Fatalf("nothing dropped: field %v mark %v elide %v", fieldAt, markAt, elideAt)
	}
	// The field goes at the widest rail, the label elides next, and the mark
	// goes last: the order is the one the plan names. The narrowest rail that
	// still elides is where the elision begins, and the mark must still be on
	// the row there.
	elides := elideAt[len(elideAt)-1]
	if fieldAt[0] <= elides {
		t.Fatalf("the field stands down at %d cells, before the label elides at %d",
			fieldAt[0], elides)
	}
	if markAt[0] > elides {
		t.Fatalf("the mark stands down at %d cells, before the label elides at %d",
			markAt[0], elides)
	}
}

// railLadderSnap is one window with a two word label inside a repository, wide
// enough to carry the whole field, on a rail of width rail.
func railLadderSnap(rail int) *wire.Snapshot {
	snap := &wire.Snapshot{
		Cols: 100, Rows: 8, Rail: rail,
		Root: &layout.Node{Pane: "w1p1"}, Focus: "w1p1",
		WindowLabel: "{{.ID}} dm",
	}
	snap.All = []wire.AllPane{{
		ID: "w1p1", Name: "pi", Status: "working", Agent: true,
		Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one",
		Focus: true, WorkspaceActive: true, WindowActive: true,
		WindowUnfolded: true, WorkspaceUnfolded: true,
		Cwd: "/work/dm", Root: "/work/dm", Branch: "dm", InRepo: true,
		Dirty: true, Added: 12, Deleted: 3,
	}}
	return snap
}

// railSeg is the name the rail gave a styled run before seg.go made the rail
// and the bar one type. The moved test still spells it.
type railSeg = segment.Seg

// railRows draws a rail and returns the text of each row inside the rail width.
func railRows(t *testing.T, snap *wire.Snapshot, set theme.GlyphSet) []string {
	t.Helper()
	var b bytes.Buffer
	DrawRail(&b, theme.DefaultTheme().For(set), snap, set, Paint{Hover: -1})
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
	if got := RailWindowAt(snap, 0, winIndex, 0); got != "w1" {
		t.Fatalf("RailWindowAt(0,%d) = %q, want w1", winIndex, got)
	}
	if got := RailWindowAt(snap, 0, noteIndex, 0); got != "w1" {
		t.Fatalf("RailWindowAt(0,%d) = %q on the note row, want w1", noteIndex, got)
	}
	if got := RailWindowAt(snap, 0, wsIndex, 0); got != "" {
		t.Fatalf("RailWindowAt(0,%d) = %q on a workspace row", wsIndex, got)
	}
	if got := RailWindowAt(snap, 0, paneIndex, 0); got != "" {
		t.Fatalf("RailWindowAt(0,%d) = %q on a pane row", paneIndex, got)
	}
	if got := RailFoldAt(snap, 3, winIndex, 0); got != "w1" {
		t.Fatalf("RailFoldAt(3,%d) = %q, want w1", winIndex, got)
	}
	if got := RailFoldAt(snap, 2, winIndex, 0); got != "" {
		t.Fatalf("RailFoldAt(2,%d) = %q, want empty", winIndex, got)
	}
	if got := RailFoldAt(snap, 3, wsIndex, 0); got != "" {
		t.Fatalf("RailFoldAt(3,%d) = %q on a workspace row, want empty", wsIndex, got)
	}
	if got := RailFoldAt(snap, 3, noteIndex, 0); got != "" {
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
	if got := RailWorkspaceAt(snap, 0, wsIndex, 0); got != "s1" {
		t.Fatalf("RailWorkspaceAt(0,%d) = %q, want s1", wsIndex, got)
	}
	if got := RailFoldAt(snap, 1, wsIndex, 0); got != "s1" {
		t.Fatalf("RailFoldAt(1,%d) = %q, want s1", wsIndex, got)
	}
	if got := RailFoldAt(snap, 2, wsIndex, 0); got != "" {
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
	if got := RailWorkspaceAt(snap, 0, paneIndex, 0); got != "" {
		t.Fatalf("RailWorkspaceAt(0,%d) = %q on a pane row", paneIndex, got)
	}
	if got := RailFoldAt(snap, 1, paneIndex, 0); got != "" {
		t.Fatalf("RailFoldAt(1,%d) = %q on a pane row", paneIndex, got)
	}
}

// TestRailASCIIGlyphs proves the ascii set reaches the rail: no glyph from the
// Unicode set survives, so a terminal with no Unicode still draws the sidebar.
func TestRailASCIIGlyphs(t *testing.T) {
	snap := railRowSnap(layout.DefaultRail)
	var b bytes.Buffer
	DrawRail(&b, theme.DefaultTheme().For(theme.GlyphASCII), snap, theme.GlyphASCII, Paint{Hover: -1})
	out := b.String()
	for _, glyph := range []string{"▾", "▸", "•", "·", "●", "○", "▲", "✓", "✕", "▶", "▌", "⎇"} {
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
	DrawRail(&b, th, snap, theme.GlyphUnicode, Paint{Hover: -1})
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
		if it.Kind == Project && RailFoldAt(snap, 1, i, 0) != wire.ProjectTargetPrefix+it.Project {
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
	DrawRail(&b, theme.DefaultTheme(), snap, theme.GlyphUnicode, Paint{Hover: -1})
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
	if got := RailFoldAt(snap, 1, wsIndex, 0); got != items[wsIndex].Workspace {
		t.Fatalf("RailFoldAt(1,%d) = %q, want %q", wsIndex, got, items[wsIndex].Workspace)
	}
	if got := RailFoldAt(snap, 0, wsIndex, 0); got != "" {
		t.Fatalf("RailFoldAt(0,%d) = %q, want empty", wsIndex, got)
	}
	if got := RailFoldAt(snap, 3, winIndex, 0); got != items[winIndex].Window {
		t.Fatalf("RailFoldAt(3,%d) = %q, want %q", winIndex, got, items[winIndex].Window)
	}
	if got := RailFoldAt(snap, 2, winIndex, 0); got != "" {
		t.Fatalf("RailFoldAt(2,%d) = %q, want empty", winIndex, got)
	}
	if got := RailPaneAt(snap, 2, winIndex, 0); got != "" {
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
	if got := RailFoldAt(snap, 1, 0, 0); got != "" {
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
		DrawRail(&b, th, snap, theme.GlyphUnicode, Paint{Hover: hover})
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

// shelfSnap is railRowSnap with two panes set aside: they appear after the
// window panes with no rect, in shelf order.
func shelfSnap(rail int) *wire.Snapshot {
	snap := railRowSnap(rail)
	// The shelf rows land past the 10-row fixture's last paintable row, so the
	// hit tests get the screen the section needs.
	snap.Rows = 20
	snap.ShelfActive = 1
	snap.Panes = append(snap.Panes,
		wire.PaneInfo{ID: "w2p7", Name: "held", Status: "blocked", Shelf: true},
		wire.PaneInfo{ID: "w2p8", Name: "waiting", Status: "idle", Shelf: true},
	)
	return snap
}

// TestRailSkipsShelfMembersInAll proves the workspace walk ignores the shelf
// entries the server appends to All. A shelved pane is in no window, so it
// carries an empty workspace and window; grouping it would paint a nameless
// group row, folded, holding a count and nothing to unfold.
func TestRailSkipsShelfMembersInAll(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	for _, rail := range []int{layout.DefaultRail, layout.CollapsedRail} {
		snap := shelfSnap(rail)
		want := RailItems(snap)
		// The server lists each shelf member after the window rows, with no
		// workspace and no window of its own.
		for _, p := range ShelfPanes(snap) {
			snap.All = append(snap.All, wire.AllPane{ID: p.ID, Name: p.Name, Status: p.Status, Shelf: true})
		}
		got := RailItems(snap)
		if len(got) != len(want) {
			t.Fatalf("rail %d: %d rows with the shelf in All, want the %d a live-only list has", rail, len(got), len(want))
		}
		for i := range got {
			if got[i].Kind != want[i].Kind || got[i].Text != want[i].Text || got[i].Count != want[i].Count {
				t.Fatalf("rail %d row %d = %+v, want %+v", rail, i, got[i], want[i])
			}
		}
		for _, it := range got {
			if it.Kind == Workspace && it.Text == "" {
				t.Fatalf("rail %d painted a nameless workspace row: %+v", rail, it)
			}
		}
	}
}

// TestRailShelfSection proves the shelf section follows the last workspace with
// one row per member, marks the active member, folds whole, and never answers a
// live pane's hit test.
func TestRailShelfSection(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	tests := []struct {
		name   string
		folded bool
		want   []RailKind
		panes  []string
	}{
		{
			name: "unfolded lists every member",
			want: []RailKind{Workspace, Window, WindowNote, Pane, Window, Gap, Workspace, Window,
				ShelfHeader, Shelf, Shelf},
			panes: []string{"w2p7", "w2p8"},
		},
		{
			name:   "folded keeps the header alone",
			folded: true,
			want: []RailKind{Workspace, Window, WindowNote, Pane, Window, Gap, Workspace, Window,
				ShelfHeader},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snap := shelfSnap(layout.DefaultRail)
			snap.ShelfFolded = tc.folded
			items := RailItems(snap)
			if len(items) != len(tc.want) {
				t.Fatalf("got %d items, want %d: %+v", len(items), len(tc.want), items)
			}
			for i, k := range tc.want {
				if items[i].Kind != k {
					t.Fatalf("item %d kind = %d, want %d", i, items[i].Kind, k)
				}
			}
			header := items[len(items)-1-len(tc.panes)]
			if header.Kind != ShelfHeader || header.Text != "shelf" || header.Count != 2 {
				t.Fatalf("header = %+v, want a shelf header naming two members", header)
			}
			for i, id := range tc.panes {
				row := items[len(items)-len(tc.panes)+i]
				if row.Pane != id {
					t.Fatalf("shelf row %d = %q, want %q in order", i, row.Pane, id)
				}
				if row.Kind != Shelf {
					t.Fatalf("shelf row %d kind = %d, want Shelf", i, row.Kind)
				}
				if row.Text == "" {
					t.Fatalf("shelf row %d has no label", i)
				}
				if row.Focus != (i == 1) {
					t.Fatalf("shelf row %d active = %v, want the active index only", i, row.Focus)
				}
			}
			// The header caret folds the section, and a member row answers its
			// own id: no live pane row moves.
			if id := RailFoldAt(snap, 1, len(items)-1-len(tc.panes), 0); id != wire.ShelfFoldTarget {
				t.Fatalf("fold at the header caret = %q, want %q", id, wire.ShelfFoldTarget)
			}
			// Only the body right of the caret summons the float: the caret
			// cell folds, and a member row answers its own id.
			headerRow := len(items) - 1 - len(tc.panes)
			if RailShelfHeaderAt(snap, 1, headerRow, 0) {
				t.Fatal("RailShelfHeaderAt answered the caret cell")
			}
			if !RailShelfHeaderAt(snap, 6, headerRow, 0) {
				t.Fatal("RailShelfHeaderAt refused the header body")
			}
			if !tc.folded && RailShelfHeaderAt(snap, 6, len(items)-1, 0) {
				t.Fatal("RailShelfHeaderAt answered a member row")
			}
			if tc.folded {
				return
			}
			if id := RailShelfAt(snap, 6, len(items)-1, 0); id != "w2p8" {
				t.Fatalf("RailShelfAt over the last member = %q, want w2p8", id)
			}
			if id := RailPaneAt(snap, 6, len(items)-1, 0); id != "" {
				t.Fatalf("RailPaneAt answered a shelf row with %q", id)
			}
		})
	}
}

// TestRailNoShelfKeepsTheOldRows proves an empty shelf adds no row: the rail a
// session without a shelved pane paints is the one it always did.
func TestRailNoShelfKeepsTheOldRows(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railRowSnap(layout.DefaultRail)
	if got := len(RailItems(snap)); got != 8 {
		t.Fatalf("rows = %d, want the 8 a shelf-free session has", got)
	}
	// The thin rail is a bare glyph column: it lists no sections, the shelf
	// included.
	thin := shelfSnap(layout.CollapsedRail)
	for _, it := range RailItems(thin) {
		if it.Kind == ShelfHeader || it.Kind == Shelf {
			t.Fatalf("the thin rail painted a shelf row: %+v", it)
		}
	}
}

// TestRailWorstState pins the order the shelf chip reads: an error outranks a
// block, a block outranks work, and a pane that never reported ranks last.
func TestRailWorstState(t *testing.T) {
	tests := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"idle", "working", "done"}, "working"},
		{[]string{"working", "blocked"}, "blocked"},
		{[]string{"blocked", "error"}, "error"},
		{[]string{"", "done", ""}, "done"},
		{[]string{"", "", ""}, ""},
	}
	for _, tc := range tests {
		if got := WorstState(tc.in); got != tc.want {
			t.Fatalf("WorstState(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestShelfMarkFollowsTheSet proves the shelf mark is one cell of text in every
// set, so a strip row and a bar chip never shift by the font.
func TestShelfMarkFollowsTheSet(t *testing.T) {
	for _, set := range []theme.GlyphSet{theme.GlyphUnicode, theme.GlyphNerd, theme.GlyphASCII} {
		if n := cells.Cells(ShelfMark(set)); n != 2 {
			t.Fatalf("ShelfMark(%v) = %q, %d cells, want 2 with the space", set, ShelfMark(set), n)
		}
	}
}
