package rail

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

// scrollSnap is a rail whose list is taller than the sidebar: six unfolded
// windows of one pane each under one workspace come to thirteen item rows,
// and a rail of ten rows shows eight of them.
func scrollSnap() *wire.Snapshot {
	snap := &wire.Snapshot{
		Cols: 60, Rows: 10, Rail: layout.DefaultRail,
		Root: &layout.Node{Pane: "w1p1"}, Focus: "w1p1",
	}
	for i, name := range []string{"one", "two", "three", "four", "five", "six"} {
		snap.All = append(snap.All, wire.AllPane{
			ID: fmt.Sprintf("w%dp1", i+1), Name: fmt.Sprintf("pane%d", i+1),
			Workspace: "s1", WorkspaceName: "main",
			Window: fmt.Sprintf("w%d", i+1), WindowName: name,
			WorkspaceActive: true, WindowActive: true,
			WorkspaceUnfolded: true, WindowUnfolded: true,
		})
	}
	return snap
}

// railPaintedRows draws the rail with paint and returns its lines, trailing
// blanks dropped, the way railRows does for a rail with no scroll.
func railPaintedRows(t *testing.T, snap *wire.Snapshot, paint Paint) []string {
	t.Helper()
	var b bytes.Buffer
	DrawRail(&b, theme.DefaultTheme(), snap, theme.GlyphUnicode, paint)
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

// TestClampOffset holds the scroll offset inside the rows the rail can show.
// The scrollSnap list is five rows taller than its rail, so the last page
// starts at 5, and a negative or oversized offset lands on the first or the
// last page.
func TestClampOffset(t *testing.T) {
	t.Parallel()
	snap := scrollSnap()
	items := RailItems(snap)
	full := snap.Rows - 2
	if len(items)-full != 5 {
		t.Fatalf("the fixture overflows by %d rows, want 5", len(items)-full)
	}
	for _, c := range []struct{ off, want int }{
		{-5, 0}, {0, 0}, {1, 1}, {5, 5}, {6, 5}, {50, 5},
	} {
		if got := ClampOffset(snap, c.off); got != c.want {
			t.Fatalf("ClampOffset(%d) = %d, want %d", c.off, got, c.want)
		}
	}
	// A list that fits has only the first page, and a nil snapshot has none.
	short := scrollSnap()
	short.Rows = len(items) + 2
	if got := ClampOffset(short, 4); got != 0 {
		t.Fatalf("ClampOffset on a list that fits = %d, want 0", got)
	}
	if got := ClampOffset(nil, 4); got != 0 {
		t.Fatalf("ClampOffset(nil) = %d, want 0", got)
	}
}

// TestDrawRailPaintsTheScrollWindow proves the paint starts at the offset: the
// first line carries the item the offset names, the item the offset pushed off
// is gone, and the hover is an item index rather than a screen row.
func TestDrawRailPaintsTheScrollWindow(t *testing.T) {
	t.Parallel()
	snap := scrollSnap()
	items := RailItems(snap)
	off := len(items) - (snap.Rows - 2)
	rows := railPaintedRows(t, snap, Paint{Off: off})
	first, head := items[off], items[0]
	if !strings.Contains(rows[0], first.Window) {
		t.Fatalf("first row = %q, want the item %q at the offset", rows[0], first.Window)
	}
	for _, row := range rows {
		if strings.Contains(row, head.Text) {
			t.Fatalf("row %q still paints the first item %q, which the offset pushed off", row, head.Text)
		}
	}
	// The hover index the client passes is the item row, so the item at that
	// index carries the hover fill even though it paints lower down.
	hoverBG := theme.DefaultTheme().Palette.RailHoverBG.BG()
	var raw bytes.Buffer
	DrawRail(&raw, theme.DefaultTheme(), snap, theme.GlyphUnicode, Paint{Off: off, Hover: off + 1})
	if !strings.Contains(raw.String(), hoverBG) {
		t.Fatal("the row the hover index names is not painted on the hover fill")
	}
	if got := len(railPaintedRows(t, snap, Paint{Off: off, Hover: off + 1})); got != len(rows) {
		t.Fatalf("hover paint has %d rows, want %d", got, len(rows))
	}
}

// TestRailItemAtFollowsTheOffset holds the hit tests to the window the paint
// shows: row 0 answers the item at the offset, and the item the offset pushed
// off answers no row.
func TestRailItemAtFollowsTheOffset(t *testing.T) {
	t.Parallel()
	snap := scrollSnap()
	items := RailItems(snap)
	off := len(items) - (snap.Rows - 2)
	if it, ok := RailItemAt(snap, 2, 0, off); !ok || it.Kind != items[off].Kind || it.Window != items[off].Window {
		t.Fatalf("row 0 at offset %d = %+v, %v, want %+v", off, it, ok, items[off])
	}
	if got := RailPaneAt(snap, 2, 1, off); got != "w3p1" {
		t.Fatalf("RailPaneAt row 1 at offset %d = %q, want w3p1", off, got)
	}
	if got := RailPaneAt(snap, 2, 0, off); got != "" {
		t.Fatalf("RailPaneAt row 0 at offset %d = %q, want no pane there", off, got)
	}
	for y := 0; y < snap.Rows-2; y++ {
		if it, ok := RailItemAt(snap, 2, y, off); ok && it.Kind == Workspace {
			t.Fatalf("row %d still answers the workspace row the offset moved off", y)
		}
	}
	// An offset past the last page never answers a row.
	if _, ok := RailItemAt(snap, 2, 0, len(items)); ok {
		t.Fatal("an offset past the last page answered a row")
	}
}
