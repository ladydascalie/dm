package rail

// The accepted v18 row, rule by rule: the counts a folded row carries, the
// silences, the pane row's shape and the state every row rolls up to the
// margin. The ladder and the git field live in rail_git_test.go and rail_test.go.

import (
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/segment"
	"dm/internal/theme"
	"dm/internal/wire"
)

// railRollupSnap is three projects, one of them all unknown, unfolded down to
// their panes so every group row is drawn:
//
//	alpha/main  w1 (idle, working) -> working, w2 (blocked) -> blocked
//	alpha/lane  w3 (done), w4 (unknown) -> done
//	beta/loose  w5 (a shell with no state, unknown) -> nothing
func railRollupSnap() *wire.Snapshot {
	pane := func(id, project, ws, wsName, win, winName, status string) wire.AllPane {
		return wire.AllPane{
			ID: id, Name: id, Status: status,
			Project: project, Workspace: ws, WorkspaceName: wsName,
			Window: win, WindowName: winName,
			WorkspaceUnfolded: true, WindowUnfolded: true,
		}
	}
	snap := &wire.Snapshot{Cols: 100, Rows: 24, Rail: layout.DefaultRail}
	snap.All = []wire.AllPane{
		pane("p1", "alpha", "s1", "main", "w1", "one", "idle"),
		pane("p2", "alpha", "s1", "main", "w1", "one", "working"),
		pane("p3", "alpha", "s1", "main", "w2", "two", "blocked"),
		pane("p4", "alpha", "s2", "lane", "w3", "three", "done"),
		pane("p5", "alpha", "s2", "lane", "w4", "four", "unknown"),
		pane("p6", "beta", "s3", "loose", "w5", "five", ""),
		pane("p7", "beta", "s3", "loose", "w5", "five", "unknown"),
	}
	return snap
}

// railItemBy finds one drawn row by kind and the id it carries.
func railItemBy(t *testing.T, items []RailItem, kind RailKind, field, id string) RailItem {
	t.Helper()
	for _, it := range items {
		if it.Kind != kind {
			continue
		}
		var got string
		switch field {
		case "workspace":
			got = it.Workspace
		case "window":
			got = it.Window
		case "project":
			got = it.Text
		}
		if got == id {
			return it
		}
	}
	t.Fatalf("rail has no kind %d row %q: %+v", kind, id, items)
	return RailItem{}
}

// TestRailRowCounts is rule 1: an open project or workspace row paints no count,
// its panes or workspaces are right there; a folded one paints the count of what
// it hides, the only evidence of it. A window row never carries a count, folded
// or not.
func TestRailRowCounts(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	snap := railRollupSnap()
	items := RailItems(snap)
	countOf := func(it RailItem) string {
		_, right, _ := RailRow(th, it, layout.DefaultRail, false, theme.GlyphUnicode)
		text := railSegTextsJoined(right)
		text = strings.TrimSuffix(text, " ")
		for _, glyph := range []string{"\u25cf", "\u25cb", "\u25b2", "\u2713", "\u2715"} {
			text = strings.TrimSuffix(text, glyph)
		}
		return strings.TrimSpace(text)
	}

	open := railItemBy(t, items, Workspace, "workspace", "s1")
	if got := countOf(open); got != "" {
		t.Fatalf("an open workspace paints the count %q: %+v", got, open)
	}
	foldedWS := railItemBy(t, items, Workspace, "workspace", "s1")
	foldedWS.Folded = true
	foldedWS.Count = 3
	if got := countOf(foldedWS); got != "3" {
		t.Fatalf("a folded workspace paints %q, want its count", got)
	}

	openProject := railItemBy(t, items, Project, "project", "alpha")
	if got := countOf(openProject); got != "" {
		t.Fatalf("an open project paints the count %q: %+v", got, openProject)
	}
	foldedProject := openProject
	foldedProject.Folded = true
	if got := countOf(foldedProject); got != "2" {
		t.Fatalf("a folded project paints %q, want its two workspaces", got)
	}

	// A window row is never given a count, folded or not, and its right run is
	// the git field, the mark, the air and the state.
	snap.All[0].WindowUnfolded = false
	for _, it := range RailItems(snap) {
		if it.Kind == Window && it.Count != 0 {
			t.Fatalf("window row %q carries a count %d: %+v", it.Window, it.Count, it)
		}
	}
}

// TestRailRowSilences is rule 2: no clean word, no dirty dot, no unknown mark
// and no pane level dot. A pane whose state is unknown or empty paints an empty
// state cell, and a pane row carries no agent mark: the state glyph is the row's
// only icon.
func TestRailRowSilences(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	snap := railRowSnap(layout.DefaultRail)
	items := RailItems(snap)

	// No clean word and no dirty dot, on a clean row or a dirty one.
	clean := RailItem{Kind: Window, Text: "one", Label: "one", Window: "w1", InRepo: true, State: "idle"}
	dirty := clean
	dirty.Dirty, dirty.Added, dirty.Deleted = true, 12, 4
	for _, it := range []RailItem{clean, dirty} {
		left, right, tail := RailRow(th, it, layout.DefaultRail, false, theme.GlyphUnicode)
		text := railSegTextsJoined(left) + railSegTextsJoined(tail) + railSegTextsJoined(right)
		for _, gone := range []string{"clean", th.Glyphs.Dirty} {
			if strings.Contains(text, gone) {
				t.Fatalf("window row still paints %q: %q", gone, text)
			}
		}
	}

	// No unknown mark, no agent mark and no dot on a pane row: six cells of
	// indent, the name, and the state glyph at the margin, or an empty cell
	// when the pane has no state to report.
	for _, status := range []string{"unknown", ""} {
		pane := RailItem{Kind: Pane, Text: "pi", Pane: "w1p1", Status: status, Agent: true}
		left, right, tail := RailRow(th, pane, layout.DefaultRail, false, theme.GlyphUnicode)
		want := []railSeg{{Text: "      ", FG: th.Palette.RailDim}, {Text: "pi", FG: th.Palette.RailDim}}
		if len(left) != len(want) || left[0] != want[0] || left[1] != want[1] {
			t.Fatalf("pane row with status %q = %+v, want the indent and the name alone", status, left)
		}
		if tail != nil {
			t.Fatalf("pane row kept a tail: %+v", tail)
		}
		if len(right) != 1 || right[0].Text != " " {
			t.Fatalf("pane row with status %q = %+v, want one empty state cell", status, right)
		}
	}
	if th.RailMarks.Pane == "" {
		t.Fatal("the theme has no pane mark, so the test cannot prove it is gone")
	}
	for _, it := range items {
		if it.Kind != Pane {
			continue
		}
		left, right, tail := RailRow(th, it, layout.DefaultRail, false, theme.GlyphUnicode)
		text := railSegTextsJoined(left) + railSegTextsJoined(right) + railSegTextsJoined(tail)
		if strings.Contains(text, th.RailMarks.Pane) {
			t.Fatalf("pane row %q still paints the dot: %q", it.Text, text)
		}
		if strings.Contains(text, strings.TrimSpace(RailMark(it.Agent, it.App, theme.GlyphUnicode))) &&
			RailMark(it.Agent, it.App, theme.GlyphUnicode) != "  " {
			t.Fatalf("pane row %q still paints an agent mark: %q", it.Text, text)
		}
	}
}

// TestRailRowPaneShape is rule 3: a pane row is six cells of indent, no icon,
// the name at column 7, and the state glyph at the margin. A focused pane row is
// bold and lit, and the thin rail keeps its own three cells.
func TestRailRowPaneShape(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	snap := railRowSnap(layout.DefaultRail)
	items := RailItems(snap)
	pane := railRowFind(t, items, Pane, "pi")
	row := -1
	for i, it := range items {
		if it.Kind == Pane && it.Pane == pane.Pane {
			row = i
		}
	}
	if row < 0 {
		t.Fatal("the fixture has no pane row")
	}
	term := railTerm(t, snap, theme.GlyphUnicode)
	// The bar owns column 0, so the six cell indent puts the name's first cell
	// in column 7, one cell right of a window row's label.
	if c := cellOf(term, 7, row); c.Char != 'p' {
		t.Fatalf("pane name starts %q at column 7, want p", c.Char)
	}
	for x := 1; x < 6; x++ {
		if c := cellOf(term, x, row); !thinBlank(c) {
			t.Fatalf("pane row holds %q in the indent cell %d", c.Char, x)
		}
	}
	// The state glyph is the row's last content cell, and it is the row's only
	// icon.
	state, _ := RailGlyph(th, pane.Status, theme.GlyphUnicode)
	if c := cellOf(term, snap.Rail-2, row); string(c.Char) != state {
		t.Fatalf("the state cell = %q, want %q", c.Char, state)
	}
	left, right, _ := RailRow(th, pane, layout.DefaultRail, false, theme.GlyphUnicode)
	if n := segment.SegCells(left); n != 6+segment.SegCells([]segment.Seg{{Text: pane.Text}}) {
		t.Fatalf("the pane row's left run is %d cells, want the indent and the name", n)
	}
	if len(right) != 1 || right[0].Text != state {
		t.Fatalf("the pane row's right run = %+v, want the state alone", right)
	}
	// Focus lights the row and bolds it, as it always did.
	if c := cellOf(term, 1, row); c.Bold != true {
		t.Fatalf("the focused pane row is not bold: %+v", c)
	}
	if c := cellOf(term, 1, row); c.BG != railColour(th.Palette.RailFocusBG) {
		t.Fatalf("the focused pane row ground = %v, want the focus fill", c.BG)
	}
	// The thin rail keeps its three cells: the bar, the agent glyph, the state.
	_, right, tail := RailRow(th, pane, layout.CollapsedRail, true, theme.GlyphUnicode)
	if right != nil || tail != nil {
		t.Fatalf("the thin pane row grew a right run: %+v / %+v", right, tail)
	}
}

// TestRailShelfRowShape proves the shelf follows the pane row: six cells of
// indent, the name and the state in the margin cell, with no agent mark and no
// dot. The section header carries its count only while the shelf is folded.
func TestRailShelfRowShape(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	snap := shelfSnap(layout.DefaultRail)
	items := RailItems(snap)

	shelfRows := 0
	for _, it := range items {
		if it.Kind != Shelf {
			continue
		}
		shelfRows++
		left, right, tail := RailRow(th, it, layout.DefaultRail, false, theme.GlyphUnicode)
		want := []railSeg{
			{Text: "      ", FG: th.Palette.RailDim},
			{Text: it.Text, FG: th.Palette.RailDim},
		}
		if it.Focus {
			want[0].FG, want[1].FG = th.Palette.RailText, th.Palette.RailText
		}
		if len(left) != len(want) || left[0] != want[0] || left[1] != want[1] {
			t.Fatalf("shelf row %q = %+v, want the indent and the name alone", it.Text, left)
		}
		if tail != nil {
			t.Fatalf("shelf row %q kept a tail: %+v", it.Text, tail)
		}
		if len(right) != 1 || right[0].Text != railStateSeg(th, it.Status, theme.GlyphUnicode).Text {
			t.Fatalf("shelf row %q = %+v, want the state alone at the margin", it.Text, right)
		}
		text := railSegTextsJoined(left) + railSegTextsJoined(right)
		for _, gone := range []string{th.RailMarks.Pane, strings.TrimSpace(RailMark(it.Agent, it.App, theme.GlyphUnicode))} {
			if gone != "" && strings.Contains(strings.TrimPrefix(text, "      "), gone) {
				t.Fatalf("shelf row %q still carries %q: %q", it.Text, gone, text)
			}
		}
	}
	if shelfRows != 2 {
		t.Fatalf("the fixture drew %d shelf rows, want 2", shelfRows)
	}
	// The drawn state sits in the rail's last content cell, like every other
	// row.
	term := railTerm(t, snap, theme.GlyphUnicode)
	for y, it := range items {
		if it.Kind != Shelf {
			continue
		}
		want := railStateSeg(th, it.Status, theme.GlyphUnicode).Text
		if got := cellOf(term, snap.Rail-2, y).Char; string(got) != want {
			t.Fatalf("shelf row %q at y=%d paints %q at the margin, want %q", it.Text, y, got, want)
		}
	}

	// The header counts what it hides while it is folded, and paints no count
	// while its rows are on the rail.
	for _, folded := range []bool{false, true} {
		snap := shelfSnap(layout.DefaultRail)
		snap.ShelfFolded = folded
		var header RailItem
		for _, it := range RailItems(snap) {
			if it.Kind == ShelfHeader {
				header = it
			}
		}
		if header.Count != 2 {
			t.Fatalf("the header counts %d members, want 2", header.Count)
		}
		_, right, _ := RailRow(th, header, layout.DefaultRail, false, theme.GlyphUnicode)
		got := railSegTextsJoined(right)
		if folded && got != "2" {
			t.Fatalf("a folded header paints %q, want its count", got)
		}
		if !folded && right != nil {
			t.Fatalf("an open header paints %+v, want no count", right)
		}
	}
}

// TestRailRowStateRollup is rule 4: every row's state glyph is the worst state
// of its members and sits in the rail's last content cell. A window row rolls up
// its panes, a workspace row its windows and a project row its workspaces, and an
// unknown state never wins a rollup, so a group whose members are all unknown
// paints nothing rather than a question mark.
func TestRailRowStateRollup(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	snap := railRollupSnap()
	items := RailItems(snap)
	glyph := func(status string) string {
		g, _ := RailGlyph(th, status, theme.GlyphUnicode)
		return g
	}
	for _, tc := range []struct {
		kind  RailKind
		field string
		id    string
		want  string
	}{
		{Window, "window", "w1", "working"},
		{Window, "window", "w2", "blocked"},
		{Window, "window", "w3", "done"},
		{Window, "window", "w4", ""},
		{Window, "window", "w5", ""},
		{Workspace, "workspace", "s1", "blocked"},
		{Workspace, "workspace", "s2", "done"},
		{Workspace, "workspace", "s3", ""},
		{Project, "project", "alpha", "blocked"},
		{Project, "project", "beta", ""},
	} {
		it := railItemBy(t, items, tc.kind, tc.field, tc.id)
		if it.State != tc.want {
			t.Fatalf("row %q rolled up %q, want %q", tc.id, it.State, tc.want)
		}
	}
	// The drawn glyph sits in the last content cell of every group and window
	// row; a group of unknown or stateless panes paints an empty cell there.
	term := railTerm(t, snap, theme.GlyphUnicode)
	for y, it := range items {
		var want string
		switch it.Kind {
		case Project, Workspace, Window:
			want = it.State
		default:
			continue
		}
		c := cellOf(term, snap.Rail-2, y)
		if want == "" || want == "unknown" {
			if !thinBlank(c) {
				t.Fatalf("%d row at y=%d paints %q, want an empty state cell", it.Kind, y, c.Char)
			}
			continue
		}
		if string(c.Char) != glyph(want) {
			t.Fatalf("%d row at y=%d paints %q, want %q", it.Kind, y, c.Char, glyph(want))
		}
	}
}
