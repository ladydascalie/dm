package main

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// railRowSnap is a rail with every row kind on it: a workspace header, an
// unfolded window line with a folder, a branch and its stats, that window's
// note, the focused pane, a folded window line, the gap, and a second
// workspace. Rows 8 and up are past the last item.
func railRowSnap(rail int) *Snapshot {
	snap := &Snapshot{
		Cols: 100, Rows: 10, Rail: rail,
		Root: &Node{Pane: "w1p1"}, Focus: "w1p1",
	}
	snap.Panes = []PaneInfo{{ID: "w1p1", Status: "working"}}
	snap.All = []AllPane{
		{
			ID: "w1p1", Name: "pi", Status: "working", Agent: true,
			Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one",
			Focus: true, WorkspaceActive: true, WindowActive: true, WindowUnfolded: true,
			Cwd: "/work/dm/rail", Root: "/work/dm", Branch: "dm-rail", InRepo: true,
			Dirty: true, Added: 12, Deleted: 4, Message: "working hard",
		},
		{
			ID: "w1p2", Name: "beta", Status: "idle", Workspace: "s1",
			WorkspaceName: "main", Window: "w2", WindowName: "two",
			WindowUnfolded: false, Cwd: "/work/dm/other", Root: "/work/dm",
		},
		{
			ID: "w2p1", Name: "gamma", Status: "done", Workspace: "s2",
			WorkspaceName: "work", Window: "w3", WindowName: "three",
			WindowUnfolded: false,
		},
	}
	return snap
}

// railRowComposed is the cells a composed row paints: the left run, the pad,
// then the right-aligned run, with the row's trailing blanks trimmed.
func railRowComposed(left, tail, right []railSeg, w int) string {
	var sb strings.Builder
	for _, seg := range append(append([]railSeg{}, left...), tail...) {
		sb.WriteString(seg.text)
	}
	if pad := w - segCells(left) - segCells(tail) - segCells(right); pad > 0 {
		sb.WriteString(strings.Repeat(" ", pad))
	}
	for _, seg := range right {
		sb.WriteString(seg.text)
	}
	return strings.TrimRight(sb.String(), " ")
}

// railRowFind returns the only row of a kind that carries text.
func railRowFind(t *testing.T, items []railItem, kind railKind, text string) railItem {
	t.Helper()
	for _, it := range items {
		if it.kind == kind && it.text == text {
			return it
		}
	}
	t.Fatalf("rail has no kind %d row %q: %+v", kind, text, items)
	return railItem{}
}

// TestRailRowComposesPaint holds railRow to the rail: every row it composes is
// the row drawRail paints, at the default width and at a narrow one, in both
// glyph forms. No row writes past the rail width.
func TestRailRowComposesPaint(t *testing.T) {
	for _, nerd := range []bool{false, true} {
		env := "0"
		if nerd {
			env = "1"
		}
		t.Setenv("DM_NERD", env)
		for _, w := range []int{defaultRail, 12} {
			snap := railRowSnap(w)
			painted := railRows(t, snap)
			items := railItems(snap)
			compact := railCompact(w)
			for y, line := range painted {
				if y == snap.Rows-2 {
					// The control row is not a railRow item; control_test.go owns it.
					continue
				}
				if y >= len(items) {
					if line != "" {
						t.Fatalf("nerd=%v w=%d row %d past the last item = %q, want blank",
							nerd, w, y, line)
					}
					continue
				}
				left, right, tail := railRow(items[y], w, compact, nerd)
				if got := railRowComposed(left, tail, right, w); got != line {
					t.Fatalf("nerd=%v w=%d row %d kind %d composes %q, rail paints %q",
						nerd, w, y, items[y].kind, got, line)
				}
				if n := segCells(left) + segCells(tail) + segCells(right); n > w {
					t.Fatalf("nerd=%v w=%d row %d is %d cells, want <= %d",
						nerd, w, y, n, w)
				}
			}
		}
	}
}

// TestRailRowSegments pins the runs railRow composes for each row kind: their
// text, colour and weight, and which run is the tail.
func TestRailRowSegments(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railRowSnap(defaultRail)
	items := railItems(snap)

	ws := railRowFind(t, items, railWorkspace, "main")
	left, right, tail := railRow(ws, defaultRail, false, false)
	wantLeft := []railSeg{
		{text: "\u258c", colour: ws.accent},
		{text: " ", colour: railText},
		{text: "main", colour: railText, bold: true},
	}
	wantRight := []railSeg{{text: "2", colour: railDim}}
	if !reflect.DeepEqual(left, wantLeft) || !reflect.DeepEqual(right, wantRight) || tail != nil {
		t.Fatalf("workspace row = %+v / %+v / %+v, want %+v / %+v / nil",
			left, right, tail, wantLeft, wantRight)
	}

	// An unfolded window line carries the caret, the folder, the branch tail
	// and the stats.
	win := railRowFind(t, items, railWindow, "one")
	left, right, tail = railRow(win, defaultRail, false, false)
	wantLeft = []railSeg{
		{text: "  ", colour: railWindowFg},
		{text: "\u25be", colour: railWindowFg},
		{text: " ", colour: railWindowFg},
		{text: "rail", colour: railWindowFg},
	}
	wantTail := []railSeg{
		{text: "  ", colour: railDim},
		{text: "dm-rail", colour: railDim},
	}
	wantRight = []railSeg{
		{text: "\u25cf", colour: railWorking},
		{text: " ", colour: railDim},
		{text: "+12", colour: railDiffAdd},
		{text: " ", colour: railDim},
		{text: "-4", colour: railDiffDel},
	}
	if !reflect.DeepEqual(left, wantLeft) || !reflect.DeepEqual(right, wantRight) ||
		!reflect.DeepEqual(tail, wantTail) {
		t.Fatalf("window line = %+v / %+v / %+v, want %+v / %+v / %+v",
			left, right, tail, wantLeft, wantRight, wantTail)
	}

	// A folded window line flips the caret and keeps its folder. It has no
	// branch and no stats outside a repo.
	folded := railRowFind(t, items, railWindow, "two")
	left, right, tail = railRow(folded, defaultRail, false, false)
	wantLeft = []railSeg{
		{text: "  ", colour: railWindowFg},
		{text: "\u25b8", colour: railWindowFg},
		{text: " ", colour: railWindowFg},
		{text: "other", colour: railWindowFg},
	}
	if folded.folded != true {
		t.Fatalf("window two is not folded: %+v", folded)
	}
	if !reflect.DeepEqual(left, wantLeft) || right != nil || tail != nil {
		t.Fatalf("folded window line = %+v / %+v / %+v, want %+v / nil / nil",
			left, right, tail, wantLeft)
	}

	// The note line is one dim run under the window line.
	note := railRowFind(t, items, railWindowNote, "working hard")
	left, right, tail = railRow(note, defaultRail, false, false)
	wantLeft = []railSeg{
		{text: "    ", colour: railDim},
		{text: "working hard", colour: railDim},
	}
	if !reflect.DeepEqual(left, wantLeft) || right != nil || tail != nil {
		t.Fatalf("note line = %+v / %+v / %+v, want %+v / nil / nil",
			left, right, tail, wantLeft)
	}

	// A focused pane row puts its indents, marker, glyph and name on rail text,
	// and carries no right-aligned run. The focus fill and the row's bold are
	// the writer's, not the composition's.
	mark, glyphColour := railMark(true, false), railWorking
	pane := railRowFind(t, items, railPane, "pi")
	left, right, tail = railRow(pane, defaultRail, false, false)
	wantLeft = []railSeg{
		{text: "      ", colour: railText},
		{text: mark, colour: glyphColour},
		{text: "\u25cf", colour: glyphColour},
		{text: " pi", colour: railText},
	}
	if !pane.focus {
		t.Fatalf("pane pi is not focused: %+v", pane)
	}
	if !reflect.DeepEqual(left, wantLeft) || right != nil || tail != nil {
		t.Fatalf("pane row = %+v / %+v / %+v, want %+v / nil / nil",
			left, right, tail, wantLeft)
	}

	// The thin rail keeps the marker and the glyph and drops the indents.
	left, right, tail = railRow(pane, collapsedRail, true, false)
	wantLeft = []railSeg{
		{text: mark, colour: glyphColour},
		{text: "\u25cf", colour: glyphColour},
	}
	if !reflect.DeepEqual(left, wantLeft) || right != nil || tail != nil {
		t.Fatalf("thin pane row = %+v / %+v / %+v, want %+v / nil / nil",
			left, right, tail, wantLeft)
	}

	// A gap row composes nothing at all.
	gap := railRowFind(t, items, railGap, "")
	left, right, tail = railRow(gap, defaultRail, false, false)
	if left != nil || right != nil || tail != nil {
		t.Fatalf("gap row = %+v / %+v / %+v, want nothing", left, right, tail)
	}

	// The one thing railRow does not return is the row's own ground and weight:
	// the focused pane row paints the focus fill bold, and every other row the
	// rail fill plain.
	var b bytes.Buffer
	drawRail(&b, snap)
	out := b.String()
	focusRow := -1
	for y := 0; y < snap.Rows-1; y++ {
		want := railBG
		if y < len(items) {
			it := items[y]
			if it.kind == railPane && it.focus {
				focusRow, want = y, railFocusBG
			}
		}
		start := fmt.Sprintf("\x1b[%d;1H\x1b[0m", y+1) + want.bg()
		if !strings.Contains(out, start) {
			t.Fatalf("row %d is not painted on %v: %q", y, want, out)
		}
	}
	if focusRow < 0 {
		t.Fatal("the snapshot has no focused pane row")
	}
	bold := fmt.Sprintf("\x1b[%d;1H\x1b[0m", focusRow+1) + railFocusBG.bg() +
		"\x1b[0m" + railFocusBG.bg() + "\x1b[1m"
	if !strings.Contains(out, bold) {
		t.Fatalf("focused row %d is not bold on the focus fill: %q", focusRow, out)
	}
}

// TestRailRowElidesBeforeStats keeps the rule the narrow rail lives by: the
// branch yields whole, then the label elides, and the stats never lose a cell.
func TestRailRowElidesBeforeStats(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railRowSnap(12)
	items := railItems(snap)
	win := railRowFind(t, items, railWindow, "one")
	full := []railSeg{
		{text: "\u25cf", colour: railWorking},
		{text: " ", colour: railDim},
		{text: "+12", colour: railDiffAdd},
		{text: " ", colour: railDim},
		{text: "-4", colour: railDiffDel},
	}

	// At the default width the branch fits beside the folder and the pair.
	left, right, tail := railRow(win, defaultRail, false, false)
	if tail == nil {
		t.Fatalf("branch dropped at %d columns: %+v", defaultRail, left)
	}
	if !reflect.DeepEqual(right, full) {
		t.Fatalf("stats at %d columns = %+v, want the whole pair", defaultRail, right)
	}

	// Twenty columns: the branch goes whole and the folder stays intact.
	left, right, tail = railRow(win, 20, false, false)
	if tail != nil {
		t.Fatalf("branch survived at 20 columns: %+v", tail)
	}
	if !reflect.DeepEqual(right, full) {
		t.Fatalf("stats at 20 columns = %+v, want the whole pair", right)
	}
	if strings.Contains(railRowComposed(left, tail, right, 20), "\u2026") {
		t.Fatalf("folder elided while it fit: %q", railRowComposed(left, tail, right, 20))
	}

	// Twelve columns: the folder elides and the pair still keeps every cell.
	left, right, tail = railRow(win, 12, false, false)
	if tail != nil {
		t.Fatalf("branch survived at 12 columns: %+v", tail)
	}
	if !reflect.DeepEqual(right, full) {
		t.Fatalf("stats at 12 columns = %+v, want the whole pair", right)
	}
	if n := len(left); n == 0 || left[n-1].text != "\u2026" {
		t.Fatalf("folder did not elide at 12 columns: %+v", left)
	}
	if got := railRowComposed(left, tail, right, 12); got != "  \u2026 \u25cf +12 -4" {
		t.Fatalf("row at 12 columns = %q, want %q", got, "  \u2026 \u25cf +12 -4")
	}

	// At every width the window row either keeps the whole pair or drops it
	// whole, it never writes past the rail, and it composes what the rail
	// paints.
	for w := 1; w <= 40; w++ {
		if railCompact(w) {
			// The thin rail lists panes only; there is no window row to elide.
			continue
		}
		snap := railRowSnap(w)
		it := railRowFind(t, railItems(snap), railWindow, "one")
		left, right, tail = railRow(it, w, railCompact(w), false)
		if right != nil && !reflect.DeepEqual(right, full) {
			t.Fatalf("stats at %d columns = %+v, want the whole pair or none", w, right)
		}
		if n := segCells(left) + segCells(tail) + segCells(right); n > w {
			t.Fatalf("row at %d columns is %d cells: %+v / %+v / %+v",
				w, n, left, tail, right)
		}
		if got, line := railRowComposed(left, tail, right, w), railRows(t, snap)[1]; got != line {
			t.Fatalf("row at %d columns composes %q, rail paints %q", w, got, line)
		}
	}
}
