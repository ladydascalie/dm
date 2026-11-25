package client

// The rail's git mark through the client: the hit box follows the cell the rail
// paints at every width, and a clean row's mark appears under the pointer so the
// facts box stays reachable.

import (
	"testing"

	"dm/internal/rail"
	"dm/internal/theme"
)

// TestGitMarkHitBoxFollowsTheCell proves the mark's hit box and the painted
// glyph are one cell at every rail width: either both are there or neither is,
// because the ladder dropped the mark with the field.
func TestGitMarkHitBoxFollowsTheCell(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	glyph := []rune(th.Glyphs.GitMark)[0]
	for railWidth := 10; railWidth <= 40; railWidth++ {
		tui, _ := gitFloatTUI(t, 100, 14, railWidth)
		row := rail.RailGitMarkRow(tui.snap, "w1", 0)
		if row < 0 {
			// The ladder dropped the mark, so nothing may answer.
			for x := 0; x < railWidth; x++ {
				if got := rail.RailGitMarkAt(tui.snap, x, 1, 0); got != "" {
					t.Fatalf("rail %d: a dropped mark answers at %d with %q", railWidth, x, got)
				}
			}
			continue
		}
		term := gitScreen(tui)
		var painted, answered []int
		for x := 0; x < railWidth; x++ {
			if termCell(term, x, row).Char == glyph {
				painted = append(painted, x)
			}
			if rail.RailGitMarkAt(tui.snap, x, row, 0) != "" {
				answered = append(answered, x)
			}
		}
		if len(painted) != 1 {
			t.Fatalf("rail %d: the row paints %d marks at %v, want one", railWidth, len(painted), painted)
		}
		if len(answered) != 1 || answered[0] != painted[0] {
			t.Fatalf("rail %d: the hit box answers %v, the mark is painted at %v", railWidth, answered, painted)
		}
	}
}

// TestGitMarkHoverReachesACleanRow proves a clean row paints no mark at rest,
// paints it under the pointer, and opens the facts box from that cell, so the
// box stays reachable on a row with nothing to report.
func TestGitMarkHoverReachesACleanRow(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	tui, _ := gitFloatTUI(t, 100, 16, 26)
	// w2 is clean and in step with its upstream.
	clean := rail.RailWindowItem
	it, ok := clean(tui.snap, "w2")
	if !ok || it.Dirty || it.Ahead > 0 || it.Behind > 0 {
		t.Fatalf("the fixture's w2 is not the clean case: %+v", it)
	}
	row := rail.RailGitMarkRow(tui.snap, "w2", 0)
	if row < 0 {
		t.Fatal("the clean row draws no mark cell")
	}
	x := gitMarkCol(tui.snap, row)
	if x < 0 {
		t.Fatal("the clean row has no mark cell to open from")
	}
	th := tui.theme()
	glyph := []rune(th.Glyphs.GitMark)[0]
	if c := termCell(gitScreen(tui), x, row).Char; c == glyph {
		t.Fatal("a clean row paints the mark at rest")
	}
	// The pointer rests on the mark's own cell, not just the row.
	tui.handleMouse(mouseEvent{btn: 35, x: x + 1, y: row + 1})
	if got := termCell(gitScreen(tui), x, row).Char; got != glyph {
		t.Fatalf("the hovered clean row paints %q in the mark cell, want %q", got, glyph)
	}
	// A press there opens the box, the way a dirty row's mark does.
	tui.handleMouse(mouseEvent{btn: 0, x: x + 1, y: row + 1})
	if tui.git == nil || tui.git.window != "w2" {
		t.Fatalf("the clean row's mark opened %+v, want w2", tui.git)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: x + 1, y: row + 1})
	if tui.git == nil {
		t.Fatal("the release closed the box")
	}
	// A second press on the same mark closes it.
	tui.handleMouse(mouseEvent{btn: 0, x: x + 1, y: row + 1})
	if tui.git != nil {
		t.Fatalf("the second press left the box up: %+v", tui.git)
	}
}
