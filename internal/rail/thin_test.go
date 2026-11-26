package rail

import (
	"bytes"
	"fmt"
	"testing"
	"unicode/utf8"

	"dm/internal/layout"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

// thinPane is one pane row of a thin-rail snapshot: a pane in a window in a
// workspace, with the state its glyph reports.
func thinPane(id, ws, win, status string, focus bool) wire.AllPane {
	return wire.AllPane{
		ID: id, Status: status, Focus: focus,
		Workspace: ws, Window: win, WorkspaceActive: ws == "s1",
		WindowActive: win == "w1", WindowUnfolded: true, WorkspaceUnfolded: true,
	}
}

// thinSnapshot is a thin rail on a 40x6 screen holding panes.
func thinSnapshot(panes []wire.AllPane) *wire.Snapshot {
	return &wire.Snapshot{
		Session: "x", Cols: 40, Rows: 6, Rail: layout.CollapsedRail, All: panes,
	}
}

// thinScreen draws the rail and returns the screen it wrote.
func thinScreen(t *testing.T, snap *wire.Snapshot) *vt.Term {
	t.Helper()
	var b bytes.Buffer
	DrawRail(&b, theme.DefaultTheme(), snap, theme.GlyphUnicode, Paint{Hover: -1})
	term := vt.NewTerm(snap.Cols, snap.Rows)
	term.Write(b.Bytes())
	term.Flush()
	return term
}

// railCell is the slice of a cell a thin rail test reads.
type railCell struct {
	Char  rune
	FG    vt.Color
	BG    vt.Color
	Bold  bool
	Width int8
}

func cellOf(term *vt.Term, x, y int) railCell {
	c := term.Grid().Cell(x, y)
	r, _ := utf8.DecodeRuneInString(c.Content)
	return railCell{Char: r, FG: c.Style.Fg, BG: c.Style.Bg, Bold: c.Style.Attrs&vt.Bold != 0, Width: c.Width}
}

func thinBlank(g railCell) bool { return g.Char == 0 || g.Char == ' ' }

// TestThinRailPaneRowsOnly draws a thin rail holding three panes over two
// windows and two workspaces. The rail is one row per pane from row 0, with no
// workspace row, no window row and no blank row between them.
func TestThinRailPaneRowsOnly(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := thinSnapshot([]wire.AllPane{
		thinPane("w1p1", "s1", "w1", "working", true),
		thinPane("w1p2", "s1", "w1", "idle", false),
		thinPane("w2p1", "s2", "w2", "blocked", false),
	})
	want := []string{"w1p1", "w1p2", "w2p1"}

	items := RailItems(snap)
	if len(items) != len(want) {
		t.Fatalf("thin rail has %d rows, want %d: %+v", len(items), len(want), items)
	}
	for i, id := range want {
		if items[i].Kind != Pane || items[i].Pane != id {
			t.Fatalf("row %d = kind %d pane %q, want pane %q",
				i, items[i].Kind, items[i].Pane, id)
		}
	}

	vt := thinScreen(t, snap)
	glyphs := []rune{'●', '○', '▲'}
	for y, id := range want {
		// The state keeps the fourth cell: the bar, the agent mark and the git
		// cell stand before it.
		if g := cellOf(vt, 3, y); g.Char != glyphs[y] {
			t.Fatalf("row %d glyph = %q, want %q for %s", y, g.Char, glyphs[y], id)
		}
		if got := RailPaneAt(snap, 2, y, 0); got != id {
			t.Fatalf("RailPaneAt(2,%d) = %q, want %q", y, got, id)
		}
	}
	// The rows past the last pane and above the control are blank rail
	// background, with no header, note or gap row taking their place. The
	// control row below them holds the expand caret at the glyph column.
	for y := len(want); y < snap.Rows-2; y++ {
		for x := 0; x < snap.Rail; x++ {
			if g := cellOf(vt, x, y); !thinBlank(g) {
				t.Fatalf("row %d past the last pane holds %q at %d", y, g.Char, x)
			}
		}
	}
	ctrl := snap.Rows - 2
	if g := cellOf(vt, 2, ctrl); g.Char != '▶' {
		t.Fatalf("control row %d glyph = %q at 2, want the expand caret", ctrl, g.Char)
	}
	for _, x := range []int{0, 1, 3, 4, 5} {
		if g := cellOf(vt, x, ctrl); !thinBlank(g) {
			t.Fatalf("control row %d holds %q at %d, want a blank", ctrl, g.Char, x)
		}
	}
	// A thin row is never a workspace row, so a workspace or fold click finds nothing.
	for y := 0; y < snap.Rows; y++ {
		if got := RailWorkspaceAt(snap, 2, y, 0); got != "" {
			t.Fatalf("RailWorkspaceAt(2,%d) = %q on the thin rail", y, got)
		}
		if got := RailFoldAt(snap, 2, y, 0); got != "" {
			t.Fatalf("RailFoldAt(2,%d) = %q on the thin rail", y, got)
		}
	}
}

// TestThinRailFoldState is a thin rail that talks about panes only, so a
// folded and an unfolded workspace draw the same rows.
func TestThinRailFoldState(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	panes := []wire.AllPane{
		thinPane("w1p1", "s1", "w1", "working", true),
		thinPane("w1p2", "s1", "w1", "idle", false),
		thinPane("w2p1", "s2", "w2", "blocked", false),
	}
	open := thinSnapshot(panes)
	// Fold a copy: folding the shared slice would fold the open snapshot too,
	// and the comparison below would only be comparing a snapshot to itself.
	frozen := append([]wire.AllPane(nil), panes...)
	for i := range frozen {
		frozen[i].WorkspaceUnfolded = false
		frozen[i].Message = "a note no thin row can show"
	}
	folded := thinSnapshot(frozen)
	if folded.All[0].WorkspaceUnfolded || folded.All[0].Message == "" {
		t.Fatalf("folded snapshot was not folded: %+v", folded.All[0])
	}
	if !open.All[0].WorkspaceUnfolded || open.All[0].Message != "" {
		t.Fatalf("folding the copy changed the open snapshot: %+v", open.All[0])
	}

	openItems, foldedItems := RailItems(open), RailItems(folded)
	if len(openItems) != len(foldedItems) {
		t.Fatalf("folded rail has %d rows, unfolded %d",
			len(foldedItems), len(openItems))
	}
	for i := range openItems {
		if openItems[i].Pane != foldedItems[i].Pane ||
			openItems[i].Kind != foldedItems[i].Kind {
			t.Fatalf("row %d differs: folded %+v, unfolded %+v",
				i, foldedItems[i], openItems[i])
		}
	}
	// The folded workspace carries a message that an expanded rail would draw
	// as a note row. No thin row may carry it, or any other header row.
	for i, it := range foldedItems {
		if it.Kind != Pane {
			t.Fatalf("folded thin row %d is kind %d, want a pane: %+v",
				i, it.Kind, it)
		}
	}

	openVT, foldedVT := thinScreen(t, open), thinScreen(t, folded)
	for y := 0; y < open.Rows; y++ {
		for x := 0; x < open.Cols; x++ {
			if a, b := cellOf(openVT, x, y), cellOf(foldedVT, x, y); a.Char != b.Char {
				t.Fatalf("cell %d,%d: folded %q, unfolded %q", x, y, b.Char, a.Char)
			}
		}
	}
}

// TestThinRailGlyphColumn draws the two states a thin row can leave empty: a
// shell with no state and an agent with no state both leave the state cell
// blank, the silence the full rail keeps, and neither asks a question about a
// state it does not have.
func TestThinRailGlyphColumn(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	shell := thinPane("w1p1", "s1", "w1", "", false)
	unknown := thinPane("w1p2", "s1", "w1", "unknown", false)
	unknown.Agent = true
	snap := thinSnapshot([]wire.AllPane{shell, unknown})
	vt := thinScreen(t, snap)
	for y := 0; y < 2; y++ {
		if g := cellOf(vt, 3, y); !thinBlank(g) {
			t.Fatalf("row %d state cell = %q at column 3, want a blank", y, g.Char)
		}
	}
	// The mark sits between the bar and the git cell: the agent mark on the
	// agent row and a blank on the shell row, both in the rail's dim tone.
	if g := cellOf(vt, 1, 1); g.Char != '\u258e' {
		t.Fatalf("agent row mark = %q at column 1, want the agent bar", g.Char)
	}
	if g := cellOf(vt, 1, 0); !thinBlank(g) {
		t.Fatalf("shell row mark = %q at column 1, want a blank", g.Char)
	}
	if g := cellOf(vt, 1, 1); g.FG != railColour(theme.DefaultTheme().Palette.RailDim) {
		t.Fatalf("agent row mark colour = %v, want the rail's dim tone", g.FG)
	}
}

// TestThinRailShellKeepsItsTerminalMark proves a shell keeps the terminal mark
// the glyph set carries and a blank state cell beside it, so the mark says what
// runs in the pane and never how it is doing.
func TestThinRailShellKeepsItsTerminalMark(t *testing.T) {
	t.Setenv("DM_NERD", "1")
	shell := thinPane("w1p1", "s1", "w1", "", false)
	snap := thinSnapshot([]wire.AllPane{shell})
	var b bytes.Buffer
	DrawRail(&b, theme.DefaultTheme().For(theme.GlyphNerd), snap, theme.GlyphNerd, Paint{Hover: -1})
	term := vt.NewTerm(snap.Cols, snap.Rows)
	term.Write(b.Bytes())
	term.Flush()
	// The nerd set's shell mark is the Font Awesome terminal, one cell of a
	// two cell mark, and the state beside it stays blank.
	mark := []rune(RailMark(false, "", theme.GlyphNerd))[0]
	if g := cellOf(term, 1, 0); g.Char != mark {
		t.Fatalf("shell mark = %q at column 1, want the terminal %q", g.Char, mark)
	}
	if g := cellOf(term, 3, 0); !thinBlank(g) {
		t.Fatalf("shell state cell = %q at column 3, want a blank", g.Char)
	}
}

// thinCells is one thin row's six cells, read left to right.
func thinCells(term *vt.Term, y int) []railCell {
	out := make([]railCell, layout.CollapsedRail)
	for x := range out {
		out[x] = cellOf(term, x, y)
	}
	return out
}

// TestThinRailCells holds the thin row to its composition in every state, for an
// agent and for a shell: the project bar, the agent mark in the rail's dim tone,
// the git cell, the state in its own colour, the question badge, and a grip
// strip that carries nothing. The state is the only coloured cell.
func TestThinRailCells(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	dim := railColour(th.Palette.RailDim)
	states := []string{"working", "idle", "blocked", "done", "error", "unknown", ""}
	for _, agent := range []bool{true, false} {
		var panes []wire.AllPane
		for i, status := range states {
			p := thinPane(fmt.Sprintf("p%d", i+1), "s1", "w1", status, false)
			p.Agent = agent
			p.Project = "dm"
			panes = append(panes, p)
		}
		snap := thinSnapshot(panes)
		// Every state needs a row of its own above the control row.
		snap.Rows = len(states) + 3
		term := thinScreen(t, snap)
		mark := string([]rune(RailMark(agent, "", theme.GlyphUnicode))[0])
		for y, status := range states {
			cells := thinCells(term, y)
			if len(cells) != 6 {
				t.Fatalf("row %d has %d cells, want six", y, len(cells))
			}
			// The bar, in the project's accent.
			if cells[0].Char != theBar() {
				t.Fatalf("agent %v row %d bar = %q, want %q", agent, y, cells[0].Char, theBar())
			}
			// The agent mark, neutral: the rail's dim tone for both marks.
			if string(cells[1].Char) != mark {
				t.Fatalf("agent %v row %d mark = %q, want %q", agent, y, cells[1].Char, mark)
			}
			if cells[1].FG != dim {
				t.Fatalf("agent %v row %d mark colour = %v, want the rail dim", agent, y, cells[1].FG)
			}
			// The git cell: blank, because the pane sits outside git.
			if !thinBlank(cells[2]) {
				t.Fatalf("agent %v row %d git cell = %q, want a blank", agent, y, cells[2].Char)
			}
			// The state: blank for unknown and empty, else the glyph in its
			// own colour.
			if status == "" || status == "unknown" {
				if !thinBlank(cells[3]) {
					t.Fatalf("state %q paints %q, want a blank state cell", status, cells[3].Char)
				}
			} else {
				glyph, colour := RailGlyph(th, status, theme.GlyphUnicode)
				if string(cells[3].Char) != glyph {
					t.Fatalf("state %q paints %q, want %q", status, cells[3].Char, glyph)
				}
				if cells[3].FG != railColour(colour) {
					t.Fatalf("state %q colour = %v, want %v", status, cells[3].FG, railColour(colour))
				}
			}
			// The badge cell, and the grip strip beyond it. The strip carries
			// the grip at its own row and nothing anywhere else.
			if !thinBlank(cells[4]) {
				t.Fatalf("agent %v row %d badge cell = %q, want a blank", agent, y, cells[4].Char)
			}
			if y == (snap.Rows-2)/2 {
				if cells[5].Char != []rune(th.Glyphs.Grip)[0] {
					t.Fatalf("agent %v row %d strip = %q, want the grip", agent, y, cells[5].Char)
				}
				continue
			}
			if !thinBlank(cells[5]) {
				t.Fatalf("agent %v row %d strip = %q, want a blank", agent, y, cells[5].Char)
			}
		}
	}
}

// TestThinRailBadgeCell proves the question badge has a cell of its own: it
// paints in the last content cell, the state keeps the cell before it, and the
// grip strip is never asked to carry it.
func TestThinRailBadgeCell(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	pane := thinPane("w1p1", "s1", "w1", "working", true)
	pane.Agent = true
	snap := thinSnapshot([]wire.AllPane{pane})
	snap.Ask = []wire.Ask{{ID: "ask1", Pane: "w1p1", ToolCallID: "tc1"}}
	vt := thinScreen(t, snap)
	cells := thinCells(vt, 0)
	glyph, _ := RailGlyph(th, "working", theme.GlyphUnicode)
	if string(cells[3].Char) != glyph {
		t.Fatalf("state cell = %q, want %q beside the badge", cells[3].Char, glyph)
	}
	if cells[4].Char != '?' {
		t.Fatalf("badge cell = %q at column 4, want ?", cells[4].Char)
	}
	if cells[4].FG != railColour(th.Palette.BarAccent) {
		t.Fatalf("badge colour = %v, want the rail accent", cells[4].FG)
	}
	if !thinBlank(cells[5]) {
		t.Fatalf("the badge landed on the grip strip: %q", cells[5].Char)
	}
}

// TestThinRailGitCell proves the git cell says what the full rail's mark says:
// the glyph in the quiet accent when the worktree has work or the branch is off
// its upstream, a blank cell when the row is clean, the glyph under the pointer
// on such a row, and the pane's own mark as the cell's hit box either way.
func TestThinRailGitCell(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	th := theme.DefaultTheme()
	glyph := []rune(th.Glyphs.GitMark)[0]
	pane := thinPane("w1p1", "s1", "w1", "idle", false)
	pane.InRepo, pane.Root, pane.Branch = true, "/work/dm", "dm"
	snap := thinSnapshot([]wire.AllPane{pane})
	// A clean pane in step with its upstream paints the cell blank at rest.
	rest := thinScreen(t, snap)
	if g := cellOf(rest, 2, 0); !thinBlank(g) {
		t.Fatalf("a clean row paints %q in the git cell, want a blank", g.Char)
	}
	if got := RailPaneGitMarkAt(snap, 2, 0, 0); got != "w1p1" {
		t.Fatalf("RailPaneGitMarkAt(2,0) = %q, want the pane", got)
	}
	// The pointer takes the glyph to the full accent, so the cell is reachable
	// on a row with nothing to report.
	var b bytes.Buffer
	DrawRail(&b, th, snap, theme.GlyphUnicode, Paint{Hover: 0})
	hover := vt.NewTerm(snap.Cols, snap.Rows)
	hover.Write(b.Bytes())
	hover.Flush()
	if g := cellOf(hover, 2, 0); g.Char != glyph || g.FG != railColour(th.Palette.BarAccent) {
		t.Fatalf("the hovered git cell = %+v, want the glyph in the full accent", g)
	}
	// Work in the tree paints it at rest, in the quiet accent.
	for _, dirty := range []struct {
		name string
		set  func(p *wire.AllPane)
	}{
		{"dirty", func(p *wire.AllPane) { p.Dirty = true }},
		{"ahead", func(p *wire.AllPane) { p.Ahead = 2 }},
		{"behind", func(p *wire.AllPane) { p.Behind = 1 }},
	} {
		marked := thinPane("w1p1", "s1", "w1", "idle", false)
		marked.InRepo, marked.Root, marked.Branch = true, "/work/dm", "dm"
		dirty.set(&marked)
		snap := thinSnapshot([]wire.AllPane{marked})
		vt := thinScreen(t, snap)
		g := cellOf(vt, 2, 0)
		if g.Char != glyph {
			t.Fatalf("%s: the git cell = %q, want the mark", dirty.name, g.Char)
		}
		if g.FG != railColour(gitMarkQuiet(th.Palette)) {
			t.Fatalf("%s: the git cell colour = %v, want the quiet accent", dirty.name, g.FG)
		}
	}
}

// TestThinRailGitCellOutsideRepo proves a pane outside git paints no mark and
// answers no click: there is no repository to describe.
func TestThinRailGitCellOutsideRepo(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := thinSnapshot([]wire.AllPane{thinPane("w1p1", "s1", "w1", "idle", false)})
	vt := thinScreen(t, snap)
	if g := cellOf(vt, 2, 0); !thinBlank(g) {
		t.Fatalf("a pane outside git paints %q in the git cell", g.Char)
	}
	if got := RailPaneGitMarkAt(snap, 2, 0, 0); got != "" {
		t.Fatalf("a pane outside git answers %q, want nothing", got)
	}
}

// TestThinRailFocusBackground paints the focused pane row with theme.DefaultTheme().Palette.RailFocusBG and
// leaves every other row on the rail background, so one row of a stack of
// glyph rows reads as the focused one.
func TestThinRailFocusBackground(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := thinSnapshot([]wire.AllPane{
		thinPane("w1p1", "s1", "w1", "working", false),
		thinPane("w1p2", "s1", "w1", "idle", true),
		thinPane("w2p1", "s2", "w2", "blocked", false),
	})
	ground := func(c theme.RGB) vt.Color { return vt.RGBColor(uint8(c.R), uint8(c.G), uint8(c.B)) }
	screen := thinScreen(t, snap)
	for y := 0; y < snap.Rows-1; y++ {
		want := ground(theme.DefaultTheme().Palette.RailBG)
		if y == 1 {
			want = ground(theme.DefaultTheme().Palette.RailFocusBG)
		}
		for x := 0; x < snap.Rail; x++ {
			if g := cellOf(screen, x, y); g.BG != want {
				t.Fatalf("cell %d,%d bg = %v, want %v", x, y, g.BG, want)
			}
		}
	}
	// The focused row is the bold one, on its glyph cell.
	focus := cellOf(screen, 3, 1)
	if focus.Char != '○' || !focus.Bold {
		t.Fatalf("focused glyph = %q bold %v, want a bold ○", focus.Char, focus.Bold)
	}
	if g := cellOf(screen, 3, 0); g.Bold {
		t.Fatalf("unfocused glyph is bold")
	}
}

// TestThinRailClips drives eight panes onto a six-row screen. The list clips
// at the last row above the bar, and no row writes past the rail width.
func TestThinRailClips(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	var panes []wire.AllPane
	for i := 0; i < 8; i++ {
		ws, win := "s1", "w1"
		if i >= 4 {
			ws, win = "s2", "w2"
		}
		id := fmt.Sprintf("p%d", i+1)
		panes = append(panes, thinPane(id, ws, win, "working", i == 0))
	}
	snap := thinSnapshot(panes)

	items := RailItems(snap)
	if len(items) != 8 {
		t.Fatalf("thin rail has %d rows, want 8", len(items))
	}
	// The clipped pane rows stop at Rows-3: the control takes Rows-2 and the
	// status bar takes Rows-1, so neither answers as a pane row.
	railRows := snap.Rows - 2
	vt := thinScreen(t, snap)
	for y := 0; y < railRows; y++ {
		if got := RailPaneAt(snap, 2, y, 0); got != items[y].Pane {
			t.Fatalf("RailPaneAt(2,%d) = %q, want %q", y, got, items[y].Pane)
		}
		if g := cellOf(vt, 3, y); g.Char != '●' {
			t.Fatalf("row %d glyph = %q, want the pane glyph", y, g.Char)
		}
	}
	for _, y := range []int{railRows, railRows + 1} {
		if got := RailPaneAt(snap, 2, y, 0); got != "" {
			t.Fatalf("RailPaneAt(2,%d) = %q, want no pane row", y, got)
		}
	}
	for y := 0; y < snap.Rows; y++ {
		for x := layout.CollapsedRail; x < snap.Cols; x++ {
			if g := cellOf(vt, x, y); !thinBlank(g) {
				t.Fatalf("thin rail wrote %q at %d,%d, past column %d",
					g.Char, x, y, layout.CollapsedRail-1)
			}
		}
	}
}
