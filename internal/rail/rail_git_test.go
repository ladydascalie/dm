package rail

import (
	"bytes"
	"strings"
	"testing"

	"dm/internal/layout"
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

// TestRailWindowRowDropsBranchPaintsMark proves a window row stops painting the
// branch and closes with the git mark instead, in the set's own form.
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

// TestRailWindowRowOutsideRepoPaintsNoMark proves a directory git never saw
// gets no mark, because there is no repository to open.
func TestRailWindowRowOutsideRepoPaintsNoMark(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	row := railRowFind(t, RailItems(railRowSnap(layout.DefaultRail)), Window, "two")
	if row.InRepo {
		t.Fatalf("window two is in a repo: %+v", row)
	}
	left, right, tail := RailRow(th, row, layout.DefaultRail, false, theme.GlyphUnicode)
	if right != nil || tail != nil {
		t.Fatalf("row outside a repo = %+v / %+v, want no right run and no tail", right, tail)
	}
	if strings.Contains(strings.Join(railSegTexts(left), ""), th.Glyphs.GitMark) {
		t.Fatalf("row outside a repo paints the mark: %+v", left)
	}
}

// TestRailGitMarkAt proves the mark is the last three cells before the grip
// strip, that all three answer with the window id, and that the cell to their
// left, a pane row, a folded window outside a repo and a shelf row do not.
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
	// The grip strip owns the rail's last column, so the row's own cells end
	// there and the mark is the three cells before it.
	end := snap.Rail - 1
	if got := cellOf(term, end-2, winRow).Char; string(got) != th.Glyphs.GitMark {
		t.Fatalf("glyph cell = %q, want %q", got, th.Glyphs.GitMark)
	}
	if got := cellOf(term, end-gitMarkCells, winRow).Char; got != ' ' {
		t.Fatalf("first mark cell = %q, want a space", got)
	}
	if got := cellOf(term, end-1, winRow).Char; got != ' ' {
		t.Fatalf("last mark cell = %q, want a space", got)
	}
	for _, x := range []int{end - gitMarkCells, end - 2, end - 1} {
		if got := RailGitMarkAt(snap, x, winRow, 0); got != "w1" {
			t.Fatalf("RailGitMarkAt(%d,%d) = %q, want w1", x, winRow, got)
		}
	}
	if got := RailGitMarkAt(snap, end-gitMarkCells-1, winRow, 0); got != "" {
		t.Fatalf("cell left of the mark = %q, want empty", got)
	}
	if got := RailGitMarkAt(snap, end-gitMarkCells, paneRow, 0); got != "" {
		t.Fatalf("pane row = %q, want empty", got)
	}
	if got := RailGitMarkAt(snap, end-gitMarkCells, foldedRow, 0); got != "" {
		t.Fatalf("folded window outside a repo = %q, want empty", got)
	}
	if got := RailGitMarkAt(snap, end-gitMarkCells, shelfRow, 0); got != "" {
		t.Fatalf("shelf row = %q, want empty", got)
	}
	if got := RailGitMarkAt(snap, end, winRow, 0); got != "" {
		t.Fatalf("grip strip cell = %q, want empty", got)
	}
	// off is the rail's scroll offset: the mark follows its row.
	if got := RailGitMarkAt(snap, end-gitMarkCells, winRow-1, 1); got != "w1" {
		t.Fatalf("mark under a one-row offset = %q, want w1", got)
	}
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

// TestRailGitMarkStates proves the mark's three states: the glyph in the quiet
// accent at rest, the full accent under the pointer, and the accent filling its
// three cells with the glyph in the rail's ground while a float has it open. An
// open mark leaves the other mark alone, so only one mark ever says which box is
// up.
func TestRailGitMarkStates(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	snap := gitTwoMarkSnap(layout.DefaultRail)
	glyph := th.Glyphs.GitMark
	// The grip strip owns the rail's last column, so the mark's glyph sits two
	// cells before it.
	gx := snap.Rail - 3
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
	// Hover: the three cells take the rail hover ground and the glyph the full
	// accent.
	term = railPaintTerm(t, snap, theme.GlyphUnicode, Paint{Hover: one})
	for x := gx - 1; x <= gx+1; x++ {
		if c := cellOf(term, x, one); c.BG != hover {
			t.Fatalf("hovered mark cell %d ground = %+v, want the hover ground", x, c.BG)
		}
	}
	if c := cellOf(term, gx, one); c.FG != accent {
		t.Fatalf("hovered glyph colour = %+v, want the full accent", c.FG)
	}
	// Open: the box fills with the accent and the glyph takes the rail ground.
	// The other mark stays at rest.
	term = railPaintTerm(t, snap, theme.GlyphUnicode, Paint{Hover: -1, Open: "w1"})
	for x := gx - 1; x <= gx+1; x++ {
		if c := cellOf(term, x, one); c.BG != accent {
			t.Fatalf("open mark cell %d ground = %+v, want the accent", x, c.BG)
		}
	}
	if c := cellOf(term, gx, one); c.FG != ground {
		t.Fatalf("open glyph colour = %+v, want the rail ground", c.FG)
	}
	if c := cellOf(term, gx, two); c.FG != quiet || c.BG != ground {
		t.Fatalf("the other mark = %+v, want its rest state while w1 is open", c)
	}
}

// TestRailGitMarkRow proves the float's anchor and the mark's hit test resolve
// one row: the drawn window row answers on both, a folded window inside a repo
// still draws its mark, a folded workspace hides it, a scroll moves it and takes
// it off the rail, and a directory outside git has none.
func TestRailGitMarkRow(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := gitTwoMarkSnap(layout.DefaultRail)
	markX := snap.Rail - gitMarkCells

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

	// The thin rail draws no window row.
	thin := gitTwoMarkSnap(layout.CollapsedRail)
	if got := RailGitMarkRow(thin, "w1", 0); got != -1 {
		t.Fatalf("thin rail anchor row = %d, want -1", got)
	}
	if _, ok := RailWindowItem(thin, "w1"); ok {
		t.Fatal("the thin rail answered a window row")
	}
}
