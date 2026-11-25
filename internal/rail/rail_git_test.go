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
