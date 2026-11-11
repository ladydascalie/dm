package client

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"dm/internal/layout"
	"dm/internal/rail"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

// ctlPane is one pane row of a control snapshot: a named agent pane in one
// window of one workspace, so both rail forms list it.
func ctlPane(id string) wire.AllPane {
	return wire.AllPane{
		ID: id, Name: id, Status: "working", Agent: true,
		Workspace: "s1", WorkspaceName: "main",
		Window: "w1", WindowName: "one",
		WorkspaceActive: true, WindowActive: true, WindowUnfolded: true, WorkspaceUnfolded: true,
	}
}

// ctlSnapshot is a 40x10 screen with the rail at the given width. It holds
// exactly Rows-2 item rows: one row per pane on the thin rail, and a workspace
// row and a window row in front of the panes on a wider one. The item list
// therefore ends on the row above the control.
func ctlSnapshot(rail int) *wire.Snapshot {
	n := 8
	if !layout.RailCompact(rail) {
		n = 6
	}
	snap := &wire.Snapshot{Session: "x", Cols: 40, Rows: 10, Rail: rail}
	for i := 0; i < n; i++ {
		snap.All = append(snap.All, ctlPane(fmt.Sprintf("p%d", i+1)))
	}
	snap.Focus = snap.All[0].ID
	return snap
}

// ctlScreen draws the rail and returns the screen it wrote.
func ctlScreen(t *testing.T, snap *wire.Snapshot) *vt.Term {
	t.Helper()
	var b bytes.Buffer
	rail.DrawRail(&b, theme.DefaultTheme(), snap, theme.GlyphUnicode, -1)
	term := vt.NewTerm(snap.Cols, snap.Rows)
	term.Write(b.Bytes())
	term.Flush()
	return term
}

// ctlGlyph is the slice of a cell a rail test reads.
type ctlGlyph struct {
	Char rune
	FG   vt.Color
	BG   vt.Color
}

// ctlCell reads one screen cell.
func ctlCell(t *testing.T, term *vt.Term, x, y int) ctlGlyph {
	t.Helper()
	c := term.Grid().Cell(x, y)
	r, _ := utf8.DecodeRuneInString(c.Content)
	return ctlGlyph{Char: r, FG: c.Style.Fg, BG: c.Style.Bg}
}

// ctlRow reads one screen row as text, one rune per cell.
func ctlRow(t *testing.T, term *vt.Term, y, cells int) string {
	t.Helper()
	var runes []rune
	for x := 0; x < cells; x++ {
		r := ctlCell(t, term, x, y).Char
		if r == 0 {
			r = ' '
		}
		runes = append(runes, r)
	}
	return string(runes)
}

// ctlBlank reports an unwritten or space-filled cell.
func ctlBlank(g ctlGlyph) bool { return g.Char == 0 || g.Char == ' ' }

// ctlTUI is a client on a 40x10 screen with the rail at the given width.
func ctlTUI(t *testing.T, rail int) (*tui, chan wire.Msg) {
	t.Helper()
	tui, got := clientTUI(t, nil)
	tui.snap = ctlSnapshot(rail)
	return tui, got
}

// TestRailControlRow holds the control to row Rows-2 at every rail width. The
// item rows end one row above it, every pane keeps its row, the row names the
// move it makes, and nothing is written past the rail's last column.
func TestRailControlRow(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	cases := []struct {
		rail  int
		text  string
		thin  bool
		named bool
	}{
		{layout.CollapsedRail, "  ▶", true, false},
		{8, "◀ colla…", false, false},
		{20, "◀ collapse", false, true},
		{40, "◀ collapse", false, true},
	}
	dim := vt.RGBColor(uint8(theme.DefaultTheme().Palette.RailDim.R), uint8(theme.DefaultTheme().Palette.RailDim.G), uint8(theme.DefaultTheme().Palette.RailDim.B))
	ground := vt.RGBColor(uint8(theme.DefaultTheme().Palette.RailBG.R), uint8(theme.DefaultTheme().Palette.RailBG.G), uint8(theme.DefaultTheme().Palette.RailBG.B))
	for _, c := range cases {
		snap := ctlSnapshot(c.rail)
		vt := ctlScreen(t, snap)
		row := snap.Rows - 2

		// No pane row is lost: every pane lists and answers on a row above
		// the control.
		seen := map[string]bool{}
		for y := 0; y < row; y++ {
			if id := rail.RailPaneAt(snap, 2, y); id != "" {
				seen[id] = true
			}
		}
		for _, p := range snap.All {
			if !seen[p.ID] {
				t.Fatalf("rail %d: pane %q has no row above the control", c.rail, p.ID)
			}
		}
		last := snap.All[len(snap.All)-1]
		if got := rail.RailPaneAt(snap, 2, row-1); got != last.ID {
			t.Fatalf("rail %d: last item row = %q, want %q", c.rail, got, last.ID)
		}
		// The last item row is drawn, not merely listed. A rail too narrow
		// for the label elides it, so the pane name is only there when it fits.
		if c.thin {
			if g := ctlCell(t, vt, 2, row-1); g.Char != '●' {
				t.Fatalf("rail %d: last item row glyph = %q, want the pane glyph", c.rail, g.Char)
			}
		} else if strings.TrimSpace(ctlRow(t, vt, row-1, c.rail)) == "" {
			t.Fatalf("rail %d: last item row draws nothing", c.rail)
		}
		if c.named && !strings.Contains(ctlRow(t, vt, row-1, c.rail), last.ID) {
			t.Fatalf("rail %d: last item row %q does not name %q",
				c.rail, ctlRow(t, vt, row-1, c.rail), last.ID)
		}

		// The control names its move, dim on the rail background.
		if got := strings.TrimRight(ctlRow(t, vt, row, c.rail), " "); got != c.text {
			t.Fatalf("rail %d: control row = %q, want %q", c.rail, got, c.text)
		}
		for x := 0; x < c.rail; x++ {
			g := ctlCell(t, vt, x, row)
			if g.FG != dim || g.BG != ground {
				t.Fatalf("rail %d: control cell %d is fg %v bg %v, want dim %v on rail %v",
					c.rail, x, g.FG, g.BG, dim, ground)
			}
			if x >= len([]rune(c.text)) && !ctlBlank(g) {
				t.Fatalf("rail %d: control cell %d = %q, want a blank", c.rail, x, g.Char)
			}
		}
		// Nothing past the rail's last column, on any row.
		for y := 0; y < snap.Rows; y++ {
			for x := c.rail; x < snap.Cols; x++ {
				if g := ctlCell(t, vt, x, y); !ctlBlank(g) {
					t.Fatalf("rail %d wrote %q at %d,%d", c.rail, g.Char, x, y)
				}
			}
		}

		// The row belongs to the control, never to an item.
		if got := rail.RailPaneAt(snap, 2, row); got != "" {
			t.Fatalf("rail %d: rail.RailPaneAt answered %q on the control row", c.rail, got)
		}
		if got := rail.RailWorkspaceAt(snap, 2, row); got != "" {
			t.Fatalf("rail %d: rail.RailWorkspaceAt answered %q on the control row", c.rail, got)
		}
		for x := 0; x < c.rail; x++ {
			if !rail.RailControlAt(snap, x, row) {
				t.Fatalf("rail %d: column %d of the control row is not a target", c.rail, x)
			}
		}
	}
}

// TestRailControlOwnsItsRow keeps a pane off the control row when the item
// list is longer than the rows above it, and keeps the grip off that row.
func TestRailControlOwnsItsRow(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := ctlSnapshot(layout.CollapsedRail)
	snap.All = append(snap.All, ctlPane("p9"), ctlPane("p10"))
	row := snap.Rows - 2
	vt := ctlScreen(t, snap)

	if got := rail.RailPaneAt(snap, 2, row); got != "" {
		t.Fatalf("rail.RailPaneAt(2,%d) = %q on the control row", row, got)
	}
	if got := rail.RailPaneAt(snap, 2, row-1); got != "p8" {
		t.Fatalf("last item row = %q, want p8", got)
	}
	if g := ctlCell(t, vt, 2, row); g.Char != '▶' {
		t.Fatalf("control cell = %q, want the caret over the ninth pane", g.Char)
	}
	// The grip stops one row short, and the control owns the last column of
	// its own row.
	if railGripAt(snap, snap.Rail-1, row) {
		t.Fatal("grip claimed the control row")
	}
	if !rail.RailControlAt(snap, snap.Rail-1, row) {
		t.Fatal("control is missing the last column of its row")
	}
	if rail.RailControlAt(snap, snap.Rail, row) || rail.RailControlAt(snap, snap.Rail-1, row-1) {
		t.Fatal("control answered outside its row")
	}
}

// TestRailControlClickCollapses clicks every column of the control row of a
// 20 column rail. Each click asks for the thin rail and nothing else.
func TestRailControlClickCollapses(t *testing.T) {
	t.Parallel()
	tui, got := ctlTUI(t, 20)
	// The control is zero-based row 8, so the one-based mouse row is 9.
	for x := 1; x <= 20; x++ {
		tui.handleMouse(mouseEvent{btn: 0, x: x, y: 9})
		m := testutil.NextMsg(t, got)
		if m.Type != "req" || m.Action != "set-rail" || m.Index != layout.CollapsedRail {
			t.Fatalf("click at column %d sent %+v, want set-rail %d", x, m, layout.CollapsedRail)
		}
		tui.handleMouse(mouseEvent{btn: 0, release: true, x: x, y: 9})
		testutil.NoMsg(t, got)
	}
}

// TestRailControlClickExpands expands the thin rail to the last full width,
// and to the default when nothing was remembered.
func TestRailControlClickExpands(t *testing.T) {
	t.Parallel()
	cases := []struct{ want, railWant int }{
		{34, 34},
		{20, 20},
		{layout.DefaultRail, 0},
	}
	for _, c := range cases {
		tui, got := ctlTUI(t, layout.CollapsedRail)
		tui.railWant = c.railWant
		tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 9})
		if m := testutil.NextMsg(t, got); m.Action != "set-rail" || m.Index != c.want {
			t.Fatalf("railWant %d: click sent %+v, want set-rail %d",
				c.railWant, m, c.want)
		}
	}
}

// TestRailControlClickSendsNoPaneGesture holds the control row clear of every
// other rail gesture: no focus, no fold, no split, no rail drag.
func TestRailControlClickSendsNoPaneGesture(t *testing.T) {
	t.Parallel()
	tui, got := ctlTUI(t, 20)
	for x := 1; x <= 20; x++ {
		tui.handleMouse(mouseEvent{btn: 0, x: x, y: 9})
		if m := testutil.NextMsg(t, got); m.Action != "set-rail" {
			t.Fatalf("click at column %d sent %+v, want only set-rail", x, m)
		}
		tui.handleMouse(mouseEvent{btn: 0, release: true, x: x, y: 9})
		testutil.NoMsg(t, got)
		if tui.railDragging || tui.dragging || tui.railPress {
			t.Fatalf("click at column %d left a gesture armed: rail %v split %v press %v",
				x, tui.railDragging, tui.dragging, tui.railPress)
		}
	}
}

// TestRailControlMatchesPrefix pins the two routes together: both collapse a
// full rail to the same width, and only `prefix r` hides a thin one.
func TestRailControlMatchesPrefix(t *testing.T) {
	t.Parallel()
	tui, got := ctlTUI(t, 20)
	tui.toggleRail()
	if m := testutil.NextMsg(t, got); m.Action != "set-rail" || m.Index != layout.CollapsedRail {
		t.Fatalf("prefix r from a full rail sent %+v, want set-rail %d", m, layout.CollapsedRail)
	}
	tui.handleMouse(mouseEvent{btn: 0, x: 10, y: 9})
	if m := testutil.NextMsg(t, got); m.Action != "set-rail" || m.Index != layout.CollapsedRail {
		t.Fatalf("control from a full rail sent %+v, want set-rail %d", m, layout.CollapsedRail)
	}

	tui, got = ctlTUI(t, layout.CollapsedRail)
	tui.railWant = 20
	tui.toggleRail()
	if m := testutil.NextMsg(t, got); m.Action != "set-rail" || m.Index != 0 {
		t.Fatalf("prefix r from the thin rail sent %+v, want the hidden rail", m)
	}
	// The thin control row is four columns wide, so click inside them.
	tui.handleMouse(mouseEvent{btn: 0, x: 2, y: 9})
	if m := testutil.NextMsg(t, got); m.Action != "set-rail" || m.Index != 20 {
		t.Fatalf("control from the thin rail sent %+v, want set-rail 20", m)
	}
}

// TestRailControlHidden draws no control when the rail is hidden, and leaves
// that screen row to the panes.
func TestRailControlHidden(t *testing.T) {
	t.Parallel()
	snap := ctlSnapshot(0)
	row := snap.Rows - 2
	for x := 0; x < snap.Cols; x++ {
		if rail.RailControlAt(snap, x, row) {
			t.Fatalf("hidden rail has a control at column %d", x)
		}
	}
	area := layout.PaneAreaFor(snap.Cols, snap.Rows, 0)
	if row < area.Y || row >= area.Y+area.H {
		t.Fatalf("row %d is not pane area for a hidden rail: %+v", row, area)
	}
	var b bytes.Buffer
	rail.DrawRail(&b, theme.DefaultTheme(), snap, theme.GlyphUnicode, -1)
	if b.Len() != 0 {
		t.Fatalf("hidden rail drew %d bytes", b.Len())
	}
}
