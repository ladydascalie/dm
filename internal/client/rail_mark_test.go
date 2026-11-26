package client

// The rail's git mark through the client: the hit box follows the cell the rail
// paints at every width, and a clean row's mark appears under the pointer so the
// facts box stays reachable.

import (
	"strings"
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
	// w2's source pane is clean and in step with its upstream.
	clean := rail.RailPaneItem
	it, ok := clean(tui.snap, "w2p1")
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

// TestGitMarkOutranksALongLabel proves the mark is still there when the label
// gives way: at 26 rail cells a window whose label is too long for the row
// elides the label and keeps the mark, and a click on that mark opens the facts
// box.
func TestGitMarkOutranksALongLabel(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	tui, _ := gitFloatTUI(t, 100, 16, 26)
	// A literal label of 22 cells is longer than the row can hold beside the
	// mark, so the mark outranks it.
	tui.snap.WindowLabel = strings.Repeat("n", 22)
	row := rail.RailGitMarkRow(tui.snap, "w1", 0)
	if row < 0 {
		t.Fatalf("the long label dropped the mark: %+v", rail.RailItems(tui.snap))
	}
	x := gitMarkCol(tui.snap, row)
	if x < 0 {
		t.Fatal("the long label left no mark cell")
	}
	term := gitScreen(tui)
	glyph := []rune(tui.theme().Glyphs.GitMark)[0]
	if got := termCell(term, x, row).Char; got != glyph {
		t.Fatalf("the row paints %q in the mark cell, want %q", got, glyph)
	}
	if got := railRowText(term, row, tui.snap.Rail); !strings.Contains(got, "\u2026") {
		t.Fatalf("the long label at 26 cells = %q, want it elided", got)
	}
	// The click still opens the box, so the facts box has an entry point on a
	// narrow rail.
	tui.handleMouse(mouseEvent{btn: 0, x: x + 1, y: row + 1})
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: x + 1, y: row + 1})
	if tui.git == nil || tui.git.window != "w1" {
		t.Fatalf("the mark click opened %+v, want w1", tui.git)
	}
}
