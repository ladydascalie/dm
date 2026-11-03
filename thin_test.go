package main

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/hinshun/vt10x"
)

// thinPane is one pane row of a thin-rail snapshot: a pane in a window in a
// workspace, with the state its glyph reports.
func thinPane(id, ws, win, status string, focus bool) AllPane {
	return AllPane{
		ID: id, Status: status, Focus: focus,
		Workspace: ws, Window: win, WorkspaceActive: ws == "s1",
		WindowActive: win == "w1", WindowUnfolded: true,
	}
}

// thinSnapshot is a thin rail on a 40x6 screen holding panes.
func thinSnapshot(panes []AllPane) *Snapshot {
	return &Snapshot{
		Session: "x", Cols: 40, Rows: 6, Rail: collapsedRail, All: panes,
	}
}

// thinScreen draws the rail and returns the screen it wrote.
func thinScreen(t *testing.T, snap *Snapshot) vt10x.Terminal {
	t.Helper()
	var b bytes.Buffer
	drawRail(&b, snap)
	vt := vt10x.New(vt10x.WithSize(snap.Cols, snap.Rows))
	if _, err := vt.Write(b.Bytes()); err != nil {
		t.Fatalf("draw thin rail: %v", err)
	}
	return vt
}

func thinBlank(g vt10x.Glyph) bool { return g.Char == 0 || g.Char == ' ' }

// TestThinRailPaneRowsOnly draws a thin rail holding three panes over two
// windows and two workspaces. The rail is one row per pane from row 0, with no
// workspace row, no window row and no blank row between them.
func TestThinRailPaneRowsOnly(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := thinSnapshot([]AllPane{
		thinPane("w1p1", "s1", "w1", "working", true),
		thinPane("w1p2", "s1", "w1", "idle", false),
		thinPane("w2p1", "s2", "w2", "blocked", false),
	})
	want := []string{"w1p1", "w1p2", "w2p1"}

	items := railItems(snap)
	if len(items) != len(want) {
		t.Fatalf("thin rail has %d rows, want %d: %+v", len(items), len(want), items)
	}
	for i, id := range want {
		if items[i].kind != railPane || items[i].pane != id {
			t.Fatalf("row %d = kind %d pane %q, want pane %q",
				i, items[i].kind, items[i].pane, id)
		}
	}

	vt := thinScreen(t, snap)
	vt.Lock()
	defer vt.Unlock()
	glyphs := []rune{'●', '○', '▲'}
	for y, id := range want {
		if g := vt.Cell(2, y); g.Char != glyphs[y] {
			t.Fatalf("row %d glyph = %q, want %q for %s", y, g.Char, glyphs[y], id)
		}
		if got := railPaneAt(snap, 2, y); got != id {
			t.Fatalf("railPaneAt(2,%d) = %q, want %q", y, got, id)
		}
	}
	// The rows past the last pane are blank rail background, with no header,
	// note or gap row taking their place.
	for y := len(want); y < snap.Rows-1; y++ {
		for x := 0; x < snap.Rail; x++ {
			if g := vt.Cell(x, y); !thinBlank(g) {
				t.Fatalf("row %d past the last pane holds %q at %d", y, g.Char, x)
			}
		}
	}
	// A thin row is never a window row, so a fold click finds nothing.
	for y := 0; y < snap.Rows; y++ {
		if got := railWindowAt(snap, 2, y); got != "" {
			t.Fatalf("railWindowAt(2,%d) = %q on the thin rail", y, got)
		}
	}
}

// TestThinRailFoldState is a thin rail that talks about panes only, so a
// folded and an unfolded window draw the same rows.
func TestThinRailFoldState(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	panes := []AllPane{
		thinPane("w1p1", "s1", "w1", "working", true),
		thinPane("w1p2", "s1", "w1", "idle", false),
		thinPane("w2p1", "s2", "w2", "blocked", false),
	}
	open := thinSnapshot(panes)
	// Fold a copy: folding the shared slice would fold the open snapshot too,
	// and the comparison below would only be comparing a snapshot to itself.
	frozen := append([]AllPane(nil), panes...)
	for i := range frozen {
		frozen[i].WindowUnfolded = false
		frozen[i].Message = "a note no thin row can show"
	}
	folded := thinSnapshot(frozen)
	if folded.All[0].WindowUnfolded || folded.All[0].Message == "" {
		t.Fatalf("folded snapshot was not folded: %+v", folded.All[0])
	}
	if !open.All[0].WindowUnfolded || open.All[0].Message != "" {
		t.Fatalf("folding the copy changed the open snapshot: %+v", open.All[0])
	}

	openItems, foldedItems := railItems(open), railItems(folded)
	if len(openItems) != len(foldedItems) {
		t.Fatalf("folded rail has %d rows, unfolded %d",
			len(foldedItems), len(openItems))
	}
	for i := range openItems {
		if openItems[i].pane != foldedItems[i].pane ||
			openItems[i].kind != foldedItems[i].kind {
			t.Fatalf("row %d differs: folded %+v, unfolded %+v",
				i, foldedItems[i], openItems[i])
		}
	}
	// The folded window carries a message that an expanded rail would draw as
	// a note row. No thin row may carry it, or any other header row.
	for i, it := range foldedItems {
		if it.kind != railPane {
			t.Fatalf("folded thin row %d is kind %d, want a pane: %+v",
				i, it.kind, it)
		}
	}

	openVT, foldedVT := thinScreen(t, open), thinScreen(t, folded)
	openVT.Lock()
	foldedVT.Lock()
	defer openVT.Unlock()
	defer foldedVT.Unlock()
	for y := 0; y < open.Rows; y++ {
		for x := 0; x < open.Cols; x++ {
			if a, b := openVT.Cell(x, y), foldedVT.Cell(x, y); a.Char != b.Char {
				t.Fatalf("cell %d,%d: folded %q, unfolded %q", x, y, b.Char, a.Char)
			}
		}
	}
}

// TestThinRailGlyphColumn draws the two states a thin row can leave empty: a
// shell with no state shows a blank at the glyph column, an agent with no
// state shows the fallback mark there.
func TestThinRailGlyphColumn(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	shell := thinPane("w1p1", "s1", "w1", "", false)
	agent := thinPane("w1p2", "s1", "w1", "", false)
	agent.Agent = true
	snap := thinSnapshot([]AllPane{shell, agent})
	vt := thinScreen(t, snap)
	vt.Lock()
	defer vt.Unlock()
	if g := vt.Cell(2, 0); !thinBlank(g) {
		t.Fatalf("shell row glyph = %q at column 2, want a blank", g.Char)
	}
	if g := vt.Cell(2, 1); g.Char != '?' {
		t.Fatalf("agent row glyph = %q at column 2, want ?", g.Char)
	}
	// The mark before the glyph keeps its column: an agent bar on the agent
	// row and a blank on the shell row.
	if g := vt.Cell(0, 1); g.Char != '\u258e' {
		t.Fatalf("agent row mark = %q at column 0, want the agent bar", g.Char)
	}
	if g := vt.Cell(0, 0); !thinBlank(g) {
		t.Fatalf("shell row mark = %q at column 0, want a blank", g.Char)
	}
}

// TestThinRailFocusBackground paints the focused pane row with railFocusBG and
// leaves every other row on the rail background, so one row of a stack of
// glyph rows reads as the focused one.
func TestThinRailFocusBackground(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := thinSnapshot([]AllPane{
		thinPane("w1p1", "s1", "w1", "working", false),
		thinPane("w1p2", "s1", "w1", "idle", true),
		thinPane("w2p1", "s2", "w2", "blocked", false),
	})
	ground := func(c rgb) vt10x.Color { return vt10x.Color(c.r<<16 | c.g<<8 | c.b) }
	vt := thinScreen(t, snap)
	vt.Lock()
	defer vt.Unlock()
	for y := 0; y < snap.Rows-1; y++ {
		want := ground(railBG)
		if y == 1 {
			want = ground(railFocusBG)
		}
		for x := 0; x < snap.Rail; x++ {
			if g := vt.Cell(x, y); g.BG != want {
				t.Fatalf("cell %d,%d bg = %v, want %v", x, y, g.BG, want)
			}
		}
	}
	// The focused row is the bold one, on its glyph cell.
	focus := vt.Cell(2, 1)
	if focus.Char != '○' || focus.Mode&attrBold == 0 {
		t.Fatalf("focused glyph = %q mode %d, want a bold ○", focus.Char, focus.Mode)
	}
	if g := vt.Cell(2, 0); g.Mode&attrBold != 0 {
		t.Fatalf("unfocused glyph is bold: mode %d", g.Mode)
	}
}

// TestThinRailClips drives eight panes onto a six-row screen. The list clips
// at the last row above the bar, and no row writes past the rail width.
func TestThinRailClips(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	var panes []AllPane
	for i := 0; i < 8; i++ {
		ws, win := "s1", "w1"
		if i >= 4 {
			ws, win = "s2", "w2"
		}
		id := fmt.Sprintf("p%d", i+1)
		panes = append(panes, thinPane(id, ws, win, "working", i == 0))
	}
	snap := thinSnapshot(panes)

	items := railItems(snap)
	if len(items) != 8 {
		t.Fatalf("thin rail has %d rows, want 8", len(items))
	}
	railRows := snap.Rows - 1
	vt := thinScreen(t, snap)
	vt.Lock()
	defer vt.Unlock()
	for y := 0; y < railRows; y++ {
		if got := railPaneAt(snap, 2, y); got != items[y].pane {
			t.Fatalf("railPaneAt(2,%d) = %q, want %q", y, got, items[y].pane)
		}
		if g := vt.Cell(2, y); g.Char != '●' {
			t.Fatalf("row %d glyph = %q, want the pane glyph", y, g.Char)
		}
	}
	// The row below the last rail row is the status bar, not a pane row.
	if got := railPaneAt(snap, 2, railRows); got != "" {
		t.Fatalf("railPaneAt(2,%d) = %q, want the bar", railRows, got)
	}
	for y := 0; y < snap.Rows; y++ {
		for x := collapsedRail; x < snap.Cols; x++ {
			if g := vt.Cell(x, y); !thinBlank(g) {
				t.Fatalf("thin rail wrote %q at %d,%d, past column %d",
					g.Char, x, y, collapsedRail-1)
			}
		}
	}
}
