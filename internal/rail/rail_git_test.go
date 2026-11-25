package rail

import (
	"bytes"
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/segment"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

// gitMarkSnap is railRowSnap with room for the shelf section and one shelved
// pane, so a shelf row is on the rail and can prove the mark stays off it.
func gitMarkSnap(rail int) *wire.Snapshot {
	snap := railRowSnap(rail)
	snap.Rows = 16
	p := wire.PaneInfo{ID: "shelf1", Status: "idle"}
	p.Shelf = true
	snap.Panes = append(snap.Panes, p)
	return snap
}

// railTerm draws a rail and replays it into a terminal, so a test reads the
// cell the rail painted at a screen point.
func railTerm(t *testing.T, snap *wire.Snapshot, set theme.GlyphSet) *vt.Term {
	t.Helper()
	var b bytes.Buffer
	DrawRail(&b, theme.DefaultTheme().For(set), snap, set, Paint{Hover: -1})
	term := vt.NewTerm(snap.Cols, snap.Rows)
	term.Write(b.Bytes())
	term.Flush()
	return term
}

// railPaintTerm draws the rail with a given paint state and replays it, so a
// test reads the cells the frame left behind.
func railPaintTerm(t *testing.T, snap *wire.Snapshot, set theme.GlyphSet, paint Paint) *vt.Term {
	t.Helper()
	var b bytes.Buffer
	DrawRail(&b, theme.DefaultTheme().For(set), snap, set, paint)
	term := vt.NewTerm(snap.Cols, snap.Rows)
	term.Write(b.Bytes())
	term.Flush()
	return term
}

// gitTwoMarkSnap is two windows inside a repo, so one mark can be open while
// the other keeps its rest state.
func gitTwoMarkSnap(rail int) *wire.Snapshot {
	snap := railRowSnap(rail)
	snap.Rows = 12
	for i := range snap.All {
		snap.All[i].InRepo = true
	}
	return snap
}

// TestRailWindowRowDropsBranchPaintsMark proves a window row stops painting the
// branch and closes with the git field and the mark instead, in the set's own
// form.
func TestRailWindowRowDropsBranchPaintsMark(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	for _, set := range []theme.GlyphSet{theme.GlyphUnicode, theme.GlyphASCII} {
		th := theme.DefaultTheme().For(set)
		row := railRowFind(t, RailItems(railRowSnap(layout.DefaultRail)), Window, "one")
		left, right, tail := RailRow(th, row, layout.DefaultRail, false, set)
		text := strings.Join(railSegTexts(left), "") +
			strings.Join(railSegTexts(tail), "") +
			strings.Join(railSegTexts(right), "")
		if strings.Contains(text, row.Branch) {
			t.Fatalf("set %v window row still paints the branch %q: %q", set, row.Branch, text)
		}
		if !strings.Contains(strings.Join(railSegTexts(right), ""), th.Glyphs.GitMark) {
			t.Fatalf("set %v window row lacks the git mark %q: %q", set, th.Glyphs.GitMark, text)
		}
	}
}

// TestRailWindowRowOutsideRepoPaintsNoMark proves a directory git never saw gets
// no field and no mark, because there is no repository to open; its right run is
// the state alone.
func TestRailWindowRowOutsideRepoPaintsNoMark(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	row := railRowFind(t, RailItems(railRowSnap(layout.DefaultRail)), Window, "two")
	if row.InRepo {
		t.Fatalf("window two is in a repo: %+v", row)
	}
	left, right, tail := RailRow(th, row, layout.DefaultRail, false, theme.GlyphUnicode)
	if tail != nil {
		t.Fatalf("row outside a repo kept a tail: %+v", tail)
	}
	if n := segment.SegCells(right); n != 1 {
		t.Fatalf("row outside a repo = %+v, want the state cell alone", right)
	}
	if strings.Contains(strings.Join(railSegTexts(right), ""), th.Glyphs.GitMark) {
		t.Fatalf("row outside a repo paints the mark: %+v", right)
	}
	if strings.Contains(strings.Join(railSegTexts(left), ""), th.Glyphs.GitMark) {
		t.Fatalf("row outside a repo paints the mark: %+v", left)
	}
}

// TestRailGitCount is the formatter's table: up to four digits read as
// themselves, and past that one whole unit, k then M then G, rounded to the
// nearest one. A count that rounds up to a thousand of its unit moves up, and a
// count of a thousand G or more clamps to 999G, so its column is always six
// cells wide.
func TestRailGitCount(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{1, "1"},
		{9999, "9999"},
		{10000, "10k"},
		{12345, "12k"},
		{999499, "999k"},
		{999999, "1M"},
		{1000000, "1M"},
		{1000000000, "1G"},
		{1000000000000, "999G"},
		{10000000000000, "999G"},
	}
	th := theme.DefaultTheme()
	for _, tc := range tests {
		if got := railGitCount(tc.n); got != tc.want {
			t.Fatalf("railGitCount(%d) = %q, want %q", tc.n, got, tc.want)
		}
		// A count never grows its column: the field is two six cell columns for
		// every value the formatter can return.
		for _, seg := range railGitField(th, RailItem{Added: tc.n, Deleted: tc.n}) {
			if n := segment.SegCells([]segment.Seg{seg}); n != gitFieldCols {
				t.Fatalf("count %d paints a %d cell column %q, want %d", tc.n, n, seg.Text, gitFieldCols)
			}
		}
	}
}

// TestRailGitField proves the field is two fixed six cell columns, each left
// aligned from its sign, so every plus sits in one cell and every minus in
// another, and a column with nothing to show is six cells of air.
func TestRailGitField(t *testing.T) {
	th := theme.DefaultTheme()
	field := func(added, deleted int) (string, []segment.Seg) {
		segs := railGitField(th, RailItem{Added: added, Deleted: deleted})
		return strings.Join(railSegTexts(segs), ""), segs
	}
	for _, tc := range []struct {
		added, deleted int
		want           string
	}{
		{0, 0, "            "},
		{1, 0, "+1          "},
		{12, 3, "+12   -3    "},
		{9999, 9999, "+9999 -9999 "},
		{12345, 1, "+12k  -1    "},
		{1000000, 1000000000, "+1M   -1G   "},
	} {
		got, segs := field(tc.added, tc.deleted)
		if got != tc.want {
			t.Fatalf("field(%d, %d) = %q, want %q", tc.added, tc.deleted, got, tc.want)
		}
		if n := segment.SegCells(segs); n != gitFieldCells {
			t.Fatalf("field(%d, %d) is %d cells, want %d", tc.added, tc.deleted, n, gitFieldCells)
		}
	}
	// Each column carries its own colour, so a count reads before its sign is
	// spelled out.
	_, segs := field(12, 3)
	if segs[0].FG != th.Palette.RailDiffAdd || segs[1].FG != th.Palette.RailDiffDel {
		t.Fatalf("field colours = %+v, want the added and removed tones", segs)
	}
}

// TestRailGitMarkPaintsOnlyWhenShown proves the mark's silences: it is painted
// when the worktree changes or the branch is ahead of or behind its upstream,
// blank on a clean row in step with its upstream, and painted on that row while
// the pointer rests on it, so the facts box stays reachable.
func TestRailGitMarkPaintsOnlyWhenShown(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	snap := gitTwoMarkSnap(layout.DefaultRail)
	// w1 is dirty and ahead; w2 is clean and in step with its upstream.
	snap.All[0].Dirty, snap.All[0].Ahead = true, 2
	snap.All[1].Dirty, snap.All[1].Ahead, snap.All[1].Behind = false, 0, 0
	snap.All[1].Upstream = "origin/main"
	items := RailItems(snap)
	one, two := railRowFind(t, items, Window, "one"), railRowFind(t, items, Window, "two")
	_, right, _ := RailRow(th, one, layout.DefaultRail, false, theme.GlyphUnicode)
	if !strings.Contains(railSegTextsJoined(right), th.Glyphs.GitMark) {
		t.Fatalf("a dirty row paints no mark: %+v", right)
	}
	_, right, _ = RailRow(th, two, layout.DefaultRail, false, theme.GlyphUnicode)
	if strings.Contains(railSegTextsJoined(right), th.Glyphs.GitMark) {
		t.Fatalf("a clean row paints the mark at rest: %+v", right)
	}
	if n := segment.SegCells(right); n != gitFieldCells+gitMarkCells+gitAirCells+1 {
		t.Fatalf("a clean row's right run is %d cells, want the field, the blank mark, the air and the state", n)
	}

	// Behind alone paints it too.
	snap.All[1].Behind = 1
	two = railRowFind(t, RailItems(snap), Window, "two")
	_, right, _ = RailRow(th, two, layout.DefaultRail, false, theme.GlyphUnicode)
	if !strings.Contains(railSegTextsJoined(right), th.Glyphs.GitMark) {
		t.Fatalf("a row behind its upstream paints no mark: %+v", right)
	}

	// The pointer paints it on a clean row: the glyph appears in the quiet
	// cell and takes the full accent.
	snap.All[1].Behind = 0
	twoRow, markX := -1, -1
	for i, it := range RailItems(snap) {
		if it.Kind == Window && it.Window == "w2" {
			twoRow = i
		}
	}
	term := railPaintTerm(t, snap, theme.GlyphUnicode, Paint{Hover: -1})
	if c := cellOf(term, snap.Rail-2-gitAirCells-gitMarkCells, twoRow); c.Char != ' ' {
		t.Fatalf("a clean row's mark at rest = %q, want the blank cell", c.Char)
	}
	term = railPaintTerm(t, snap, theme.GlyphUnicode, Paint{Hover: twoRow})
	markX = snap.Rail - 2 - gitAirCells - gitMarkCells
	c := cellOf(term, markX, twoRow)
	if c.Char != []rune(th.Glyphs.GitMark)[0] {
		t.Fatalf("a clean row under the pointer = %q, want the mark glyph", c.Char)
	}
	if c.FG != railColour(th.Palette.BarAccent) {
		t.Fatalf("the hovered mark colour = %v, want the full accent", c.FG)
	}
}

// TestRailGitMarkAt proves the mark is one cell, with one cell of air and the
// state after it, that exactly that cell answers with the window id, and that
// every other cell, a pane row, a shelf row, a folded row outside a repo and the
// grip strip do not.
func TestRailGitMarkAt(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := gitMarkSnap(layout.DefaultRail)
	winRow, paneRow, foldedRow, shelfRow := -1, -1, -1, -1
	for i, it := range RailItems(snap) {
		switch {
		case it.Kind == Window && it.Text == "one":
			winRow = i
		case it.Kind == Pane && it.Text == "pi":
			paneRow = i
		case it.Kind == Window && it.Text == "two":
			foldedRow = i
		case it.Kind == Shelf:
			shelfRow = i
		}
	}
	if winRow < 0 || paneRow < 0 || foldedRow < 0 || shelfRow < 0 {
		t.Fatalf("missing row: window=%d pane=%d folded=%d shelf=%d",
			winRow, paneRow, foldedRow, shelfRow)
	}

	th := theme.DefaultTheme()
	term := railTerm(t, snap, theme.GlyphUnicode)
	// The mark is the third cell from the row's right edge: the grip strip owns
	// the last column, the state the one before it, the air the one before
	// that.
	markX := snap.Rail - 2 - gitAirCells - gitMarkCells
	if got := cellOf(term, markX, winRow).Char; string(got) != th.Glyphs.GitMark {
		t.Fatalf("mark cell = %q, want %q", got, th.Glyphs.GitMark)
	}
	if got := cellOf(term, snap.Rail-2, winRow).Char; string(got) == th.Glyphs.GitMark {
		t.Fatalf("the state cell carries the mark: %q", got)
	}
	if got := RailGitMarkAt(snap, markX, winRow, 0); got != "w1" {
		t.Fatalf("RailGitMarkAt(%d,%d) = %q, want w1", markX, winRow, got)
	}
	for _, x := range []int{markX - 1, markX + 1, markX + 2, snap.Rail - 1} {
		if got := RailGitMarkAt(snap, x, winRow, 0); got != "" {
			t.Fatalf("RailGitMarkAt(%d,%d) = %q, want empty", x, winRow, got)
		}
	}
	if got := RailGitMarkAt(snap, markX, paneRow, 0); got != "" {
		t.Fatalf("pane row = %q, want empty", got)
	}
	if got := RailGitMarkAt(snap, markX, foldedRow, 0); got != "" {
		t.Fatalf("folded window outside a repo = %q, want empty", got)
	}
	if got := RailGitMarkAt(snap, markX, shelfRow, 0); got != "" {
		t.Fatalf("shelf row = %q, want empty", got)
	}
	if got := RailGitMarkAt(snap, snap.Rail, winRow, 0); got != "" {
		t.Fatalf("outside the rail = %q, want empty", got)
	}
	// off is the rail's scroll offset: the mark follows its row.
	if got := RailGitMarkAt(snap, markX, winRow-1, 1); got != "w1" {
		t.Fatalf("mark under a one-row offset = %q, want w1", got)
	}
}

// TestRailGitMarkAtEveryWidth holds the hit test to the painted cell: at every
// rail width either the mark is drawn in the cell the hit test answers, or the
// ladder dropped it and the cell answers nothing. A row too narrow for the mark
// beside its label paints no mark at all.
func TestRailGitMarkAtEveryWidth(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	for rail := layout.CollapsedRail + 1; rail <= 60; rail++ {
		snap := gitTwoMarkSnap(rail)
		snap.Rows = 6
		items := RailItems(snap)
		row := -1
		for i, it := range items {
			if it.Kind == Window && it.Window == "w1" {
				row = i
			}
		}
		if row < 0 {
			t.Fatalf("rail %d draws no window row", rail)
		}
		term := railTerm(t, snap, theme.GlyphUnicode)
		answered := -1
		for x := 0; x < rail; x++ {
			if RailGitMarkAt(snap, x, row, 0) != "" {
				if answered >= 0 {
					t.Fatalf("rail %d answers two cells, %d and %d", rail, answered, x)
				}
				answered = x
			}
		}
		// The mark's glyph is on the rail exactly when the hit test answers.
		painted := -1
		for x := 0; x < rail; x++ {
			if c := cellOf(term, x, row); c.Char == []rune(th.Glyphs.GitMark)[0] {
				if painted >= 0 {
					t.Fatalf("rail %d paints two marks, %d and %d", rail, painted, x)
				}
				painted = x
			}
		}
		if answered != painted {
			t.Fatalf("rail %d: hit test answers cell %d, the mark is painted in %d", rail, answered, painted)
		}
	}
}

// TestRailGitMarkStates proves the mark's three states: the glyph in the quiet
// accent at rest, the full accent under the pointer, and the accent filling its
// cell with the glyph in the rail's ground while a float has it open. An open
// mark leaves the other mark alone, so only one mark ever says which box is up.
func TestRailGitMarkStates(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	snap := gitTwoMarkSnap(layout.DefaultRail)
	glyph := th.Glyphs.GitMark
	// The grip strip owns the rail's last column, so the mark sits three cells
	// before it: the state, the air and then the mark.
	gx := snap.Rail - 2 - gitAirCells - gitMarkCells
	one, two := 1, 4
	if got := RailGitMarkRow(snap, "w1", 0); got != one {
		t.Fatalf("w1 mark row = %d, want %d", got, one)
	}
	if got := RailGitMarkRow(snap, "w2", 0); got != two {
		t.Fatalf("w2 mark row = %d, want %d", got, two)
	}
	quiet := railColour(gitMarkQuiet(th.Palette))
	ground := railColour(th.Palette.RailBG)
	accent := railColour(th.Palette.BarAccent)
	hover := railColour(th.Palette.RailHoverBG)

	// Rest: the glyph in the quiet accent on the rail's own ground.
	term := railPaintTerm(t, snap, theme.GlyphUnicode, Paint{Hover: -1})
	if c := cellOf(term, gx, one); c.Char != []rune(glyph)[0] || c.FG != quiet || c.BG != ground {
		t.Fatalf("rest mark = %+v, want %q in the quiet accent on the rail ground", c, glyph)
	}
	// Hover: the row takes the hover ground and the glyph the full accent.
	term = railPaintTerm(t, snap, theme.GlyphUnicode, Paint{Hover: one})
	if c := cellOf(term, gx, one); c.BG != hover || c.FG != accent {
		t.Fatalf("hovered mark = %+v, want the glyph in the full accent on the hover ground", c)
	}
	// Open: the cell fills with the accent and the glyph takes the rail ground.
	// The other mark stays at rest.
	term = railPaintTerm(t, snap, theme.GlyphUnicode, Paint{Hover: -1, Open: "w1"})
	if c := cellOf(term, gx, one); c.BG != accent || c.FG != ground {
		t.Fatalf("open mark = %+v, want the glyph in the rail ground on the accent", c)
	}
	if c := cellOf(term, gx, two); c.FG != quiet || c.BG != ground {
		t.Fatalf("the other mark = %+v, want its rest state while w1 is open", c)
	}
}

// TestRailGitMarkRow proves the float's anchor and the mark's hit test resolve
// one row: the drawn window row answers on both, a folded window inside a repo
// still draws its mark, a folded workspace hides it, a scroll moves it and takes
// it off the rail, a directory outside git has none, and a row too narrow for the
// mark has none either.
func TestRailGitMarkRow(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := gitTwoMarkSnap(layout.DefaultRail)
	markX := snap.Rail - 2 - gitAirCells - gitMarkCells

	row := RailGitMarkRow(snap, "w1", 0)
	if row != 1 {
		t.Fatalf("w1 anchor row = %d, want 1", row)
	}
	if got := RailGitMarkAt(snap, markX, row, 0); got != "w1" {
		t.Fatalf("hit test on the anchor row = %q, want w1", got)
	}

	// A folded window inside a repo still draws its row, so its mark is still
	// on the rail and still answers.
	snap.All[1].WindowUnfolded = false
	if got := RailGitMarkRow(snap, "w2", 0); got != 4 {
		t.Fatalf("folded window anchor row = %d, want 4", got)
	}
	if got := RailGitMarkAt(snap, markX, 4, 0); got != "w2" {
		t.Fatalf("folded window hit test = %q, want w2", got)
	}
	snap.All[1].WindowUnfolded = true

	// A folded workspace hides its windows, so nothing is drawn and nothing
	// answers.
	snap.All[2].WorkspaceUnfolded = false
	if got := RailGitMarkRow(snap, "w3", 0); got != -1 {
		t.Fatalf("folded workspace anchor row = %d, want -1", got)
	}
	if got := RailGitMarkAt(snap, markX, 0, 0); got != "" {
		t.Fatalf("folded workspace hit test = %q, want empty", got)
	}
	snap.All[2].WorkspaceUnfolded = true

	// A scroll moves the anchor with the row and takes it off the rail.
	if got := RailGitMarkRow(snap, "w1", 1); got != 0 {
		t.Fatalf("anchor under a one row scroll = %d, want 0", got)
	}
	if got := RailGitMarkRow(snap, "w1", 4); got != -1 {
		t.Fatalf("anchor past the rail = %d, want -1", got)
	}
	if got := RailGitMarkRow(snap, "w1", len(RailItems(snap))); got != -1 {
		t.Fatalf("anchor past the last row = %d, want -1", got)
	}

	// Outside git there is no mark, so there is no anchor.
	out := gitTwoMarkSnap(layout.DefaultRail)
	out.All[1].InRepo = false
	if got := RailGitMarkRow(out, "w2", 0); got != -1 {
		t.Fatalf("outside git anchor row = %d, want -1", got)
	}
	if it, ok := RailWindowItem(out, "w2"); !ok || it.InRepo {
		t.Fatalf("window item outside git = %+v, %v", it, ok)
	}

	// A rail too narrow for the mark beside the label drops it, so the box has
	// no anchor: the ladder gives the cells to the name.
	narrow := gitTwoMarkSnap(layout.CollapsedRail + 6)
	if got := RailGitMarkRow(narrow, "w1", 0); got != -1 {
		t.Fatalf("narrow rail anchor row = %d, want -1", got)
	}

	// The thin rail draws no window row.
	thin := gitTwoMarkSnap(layout.CollapsedRail)
	if got := RailGitMarkRow(thin, "w1", 0); got != -1 {
		t.Fatalf("thin rail anchor row = %d, want -1", got)
	}
	if _, ok := RailWindowItem(thin, "w1"); ok {
		t.Fatal("the thin rail answered a window row")
	}
}
