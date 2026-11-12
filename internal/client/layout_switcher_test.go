package client

import (
	"bytes"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"dm/internal/keymap"
	"dm/internal/keymapview"
	"dm/internal/layout"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/transport"
	"dm/internal/vt"
	"dm/internal/wire"
)

// layoutTUI is a client over a real net.Pipe whose active window holds two
// panes side by side. The window carries no stored layout name, so the custom
// row is the current one until a test says otherwise.
func layoutTUI(t *testing.T) (*tui, chan wire.Msg) {
	t.Helper()
	root := &layout.Node{Dir: "row", Kids: []*layout.Node{{Pane: "w1p1"}, {Pane: "w1p2"}}}
	snap := testutil.TestSnapshot(root, 100, 30, nil)
	snap.Rail = 34
	// TestSnapshot laid the panes out with no rail; relayout over the pane
	// area the rail leaves, so the preview's aspect matches the screen.
	rects := map[string]layout.Rect{}
	layout.Layout(root, layout.PaneAreaForWithTabBar(100, 30, 34), testutil.Pad(), rects)
	for i := range snap.Panes {
		snap.Panes[i].Rect = rects[snap.Panes[i].ID]
	}
	snap.Windows = []wire.WindowInfo{{ID: "w1", Name: "one"}}
	snap.ActiveWindow = 0
	tui := &tui{snap: snap, vts: map[string]*vt.Term{}, out: &bytes.Buffer{}}
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	tui.conn = &transport.Conn{Conn: a}
	got := make(chan wire.Msg, 32)
	go func() {
		defer close(got)
		for {
			m, err := wire.ReadMsg(b)
			if err != nil {
				return
			}
			got <- m
		}
	}()
	return tui, got
}

// openLayout opens the palette with prefix L the way the keymap binds it.
func openLayout(t *testing.T, tui *tui) {
	t.Helper()
	feed(tui, "\x02L")
	if tui.mode != "layout" || tui.lsw == nil {
		t.Fatalf("prefix L did not open the palette: mode %q lsw %+v", tui.mode, tui.lsw)
	}
}

// TestLayoutPickerOpenClose: prefix L opens, esc and q close, and neither
// sends anything.
func TestLayoutPickerOpenClose(t *testing.T) {
	t.Parallel()
	tui, got := layoutTUI(t)
	openLayout(t, tui)
	if tui.lsw.sel != layoutCustomRow {
		t.Fatalf("empty layout name opened on row %d, want the custom row %d", tui.lsw.sel, layoutCustomRow)
	}
	// A lone esc shares its head with the arrow keys and the paste marker, so
	// it waits out the escape timer and the expiry is the close, as in every
	// mode overlay.
	feed(tui, "\x1b")
	if !tui.expirePaste(time.Now().Add(escTimeout + time.Millisecond)) {
		t.Fatal("the held esc never flushed")
	}
	if !tui.pending {
		t.Fatal("the flushed esc did not reach the palette as a lone key")
	}
	tui.escAt = time.Now().Add(-time.Second)
	if !tui.expireMode(time.Now()) {
		t.Fatal("expireMode did not close the palette")
	}
	if tui.mode != "" || tui.lsw != nil {
		t.Fatal("esc did not close the palette")
	}
	testutil.NoMsg(t, got)
	openLayout(t, tui)
	feed(tui, "q")
	if tui.mode != "" || tui.lsw != nil {
		t.Fatal("q did not close the palette")
	}
	testutil.NoMsg(t, got)
}

// TestLayoutPickerMovementWraps: j and the down arrow walk next, k and the up
// arrow walk prev, and both ends wrap.
func TestLayoutPickerMovementWraps(t *testing.T) {
	t.Parallel()
	tui, got := layoutTUI(t)
	openLayout(t, tui)
	feed(tui, "j")
	if tui.lsw.sel != 0 {
		t.Fatalf("j from the custom row = %d, want the wrap to 0", tui.lsw.sel)
	}
	feed(tui, "\x1b[B") // down arrow
	if tui.lsw.sel != 1 {
		t.Fatalf("down arrow = %d, want 1", tui.lsw.sel)
	}
	feed(tui, "k")
	if tui.lsw.sel != 0 {
		t.Fatalf("k = %d, want 0", tui.lsw.sel)
	}
	feed(tui, "\x1b[A") // up arrow
	if tui.lsw.sel != layoutCustomRow {
		t.Fatalf("up arrow = %d, want the wrap to %d", tui.lsw.sel, layoutCustomRow)
	}
	testutil.NoMsg(t, got)
}

// TestLayoutPickerPickSendsSelectLayout: enter on a preset row sends
// select-layout with that preset's name and closes.
func TestLayoutPickerPickSendsSelectLayout(t *testing.T) {
	t.Parallel()
	tui, got := layoutTUI(t)
	openLayout(t, tui)
	feed(tui, "j") // even-horizontal
	feed(tui, "\r")
	if tui.mode != "" || tui.lsw != nil {
		t.Fatal("pick left the palette open")
	}
	select {
	case m := <-got:
		if m.Type != "req" || m.Action != "select-layout" || m.Layout != "even-horizontal" {
			t.Fatalf("pick sent %+v, want a select-layout for even-horizontal", m)
		}
	case <-time.After(time.Second):
		t.Fatal("pick sent nothing")
	}
}

// TestLayoutPickerPickCustomSendsNothing: enter on the custom row closes and
// keeps the tree as it stands.
func TestLayoutPickerPickCustomSendsNothing(t *testing.T) {
	t.Parallel()
	tui, got := layoutTUI(t)
	openLayout(t, tui)
	if tui.lsw.sel != layoutCustomRow {
		t.Fatalf("opened on row %d, want the custom row", tui.lsw.sel)
	}
	feed(tui, "\r")
	if tui.mode != "" || tui.lsw != nil {
		t.Fatal("pick on custom left the palette open")
	}
	testutil.NoMsg(t, got)
}

// TestLayoutPickerOnePaneRefuses: a one-pane window dims every preset row, the
// footer says why, and pick on a preset row sends nothing and keeps the
// palette open.
func TestLayoutPickerOnePaneRefuses(t *testing.T) {
	t.Parallel()
	root := &layout.Node{Pane: "w1p1"}
	snap := testutil.TestSnapshot(root, 100, 30, nil)
	// A rail keeps the pane area's aspect inside the preview band, so the
	// one-pane dim state and its footer are what this test asserts.
	snap.Rail = 34
	snap.Windows = []wire.WindowInfo{{ID: "w1"}}
	tui := &tui{snap: snap, vts: map[string]*vt.Term{}, out: &bytes.Buffer{}}
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	tui.conn = &transport.Conn{Conn: a}
	got := make(chan wire.Msg, 32)
	go func() {
		defer close(got)
		for {
			m, err := wire.ReadMsg(b)
			if err != nil {
				return
			}
			got <- m
		}
	}()
	openLayout(t, tui)
	feed(tui, "j") // even-horizontal, dimmed
	feed(tui, "\r")
	if tui.mode != "layout" || tui.lsw == nil {
		t.Fatal("pick on a dimmed preset row closed the palette")
	}
	testutil.NoMsg(t, got)
	var out bytes.Buffer
	drawLayoutSwitcher(&out, tui.theme(), snap, tui.lsw)
	if s := out.String(); !strings.Contains(s, "one pane: every layout is this") {
		t.Fatalf("footer = %q, want the one-pane line", s)
	}
}

// TestLayoutCurrentMarkFollowsSnapshot: the row matching the window's stored
// layout name carries the current mark, an empty name moves it to the custom
// row, and the palette opens on the current row.
func TestLayoutCurrentMarkFollowsSnapshot(t *testing.T) {
	t.Parallel()
	l := &layoutSwitcher{}
	snap := &wire.Snapshot{Windows: []wire.WindowInfo{{ID: "w1"}}, ActiveWindow: 0}
	if got := l.currentRow(snap); got != layoutCustomRow {
		t.Fatalf("empty name = row %d, want the custom row", got)
	}
	snap.Windows[0].Layout = "main-vertical"
	if got := l.currentRow(snap); got != 3 {
		t.Fatalf("main-vertical = row %d, want 3", got)
	}
	snap2 := testutil.TestSnapshot(&layout.Node{Pane: "a"}, 100, 30, nil)
	snap2.Windows = []wire.WindowInfo{{ID: "w1", Layout: "tiled"}}
	snap2.ActiveWindow = 0
	tui := &tui{snap: snap2, vts: map[string]*vt.Term{}, out: &bytes.Buffer{}}
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	tui.conn = &transport.Conn{Conn: a}
	openLayout(t, tui)
	if tui.lsw.sel != 4 {
		t.Fatalf("tiled window opened on row %d, want 4", tui.lsw.sel)
	}
}

// TestLayoutPreviewMatchesPresetsAndHighlightsFocus: the painted preview puts
// every pane id inside its box, exactly one box carries the accent and the
// rest stay dim, so the focused pane is the highlighted one.
func TestLayoutPreviewMatchesPresetsAndHighlightsFocus(t *testing.T) {
	t.Parallel()
	root := &layout.Node{Dir: "row", Kids: []*layout.Node{{Pane: "w1p1"}, {Pane: "w1p2"}}}
	area := layout.Rect{X: 0, Y: 0, W: 40, H: 16}
	rects, ok := layout.MiniRects(root, area, []string{"w1p1", "w1p2"}, "even-horizontal")
	if !ok {
		t.Fatal("MiniRects failed")
	}
	th := theme.DefaultTheme()
	accent := theme.Style{Fg: styleColor(th.Palette.BarAccent), Attrs: theme.AttrBold}.SGR()
	dim := "\x1b[0m\x1b[90m"
	var out bytes.Buffer
	paintLayoutPreview(&out, th, rects, "w1p1")
	s := out.String()
	if !strings.Contains(s, accent) {
		t.Fatal("no box carried the accent")
	}
	if got := strings.Count(s, accent); got != 1 {
		t.Fatalf("the accent coloured %d boxes, want the one focused pane", got)
	}
	if got := strings.Count(s, dim); got != 1 {
		t.Fatalf("the dim style coloured %d boxes, want the one unfocused pane", got)
	}
	if !strings.Contains(s, "w1p1") || !strings.Contains(s, "w1p2") {
		t.Fatalf("preview = %q, want both pane ids inside their boxes", s)
	}
}

// TestLayoutRefreshFollowsSnapshot: the palette re-reads the window's panes,
// so a pane that exits while it is open dims the preset rows again.
func TestLayoutRefreshFollowsSnapshot(t *testing.T) {
	t.Parallel()
	tui, got := layoutTUI(t)
	openLayout(t, tui)
	if len(tui.lsw.panes) != 2 {
		t.Fatalf("opened over %d panes, want 2", len(tui.lsw.panes))
	}
	// The pane w1p2 exits; the next frame's snapshot carries one pane.
	tui.snap.Root = &layout.Node{Pane: "w1p1"}
	tui.snap.Panes = tui.snap.Panes[:1]
	tui.lsw.refresh(tui.snap)
	if len(tui.lsw.panes) != 1 {
		t.Fatalf("refresh kept %d panes, want 1", len(tui.lsw.panes))
	}
	feed(tui, "j")
	feed(tui, "\r")
	if tui.mode != "layout" || tui.lsw == nil {
		t.Fatal("pick on a preset row after the pane exited closed the palette")
	}
	testutil.NoMsg(t, got)
}

// TestLayoutPreviewBandDropsOnSmallScreen: a screen too small for the minimum
// preview shows the list alone with a footer note.
func TestLayoutPreviewBandDropsOnSmallScreen(t *testing.T) {
	t.Parallel()
	snap := testutil.TestSnapshot(&layout.Node{Pane: "a"}, 40, 16, nil)
	g := layoutPaletteGeom(snap, 1)
	if g.preview.W != 0 {
		t.Fatalf("40x16 kept a %+v preview, want it dropped", g.preview)
	}
	if g.rect.W < 1 {
		t.Fatal("the list alone did not paint")
	}
	l := &layoutSwitcher{panes: []string{"a"}, focus: "a"}
	var out bytes.Buffer
	drawLayoutSwitcher(&out, theme.DefaultTheme(), snap, l)
	if s := out.String(); !strings.Contains(s, "no room here for the preview") {
		t.Fatalf("footer = %q, want the dropped-preview note", s)
	}
}

// seamPanes builds a window root holding n fresh panes split side by side,
// so MiniRects can build tiled over them.
func seamPanes(n int) *layout.Node {
	kids := make([]*layout.Node, n)
	for i := range kids {
		kids[i] = &layout.Node{Pane: fmt.Sprintf("p%d", i+1)}
	}
	return &layout.Node{Dir: "row", Kids: kids}
}

// seamTiledRows walks tiled over area the way the server and the preview walk
// it and reports the number of rows it builds, so the tests compare the real
// walks rather than a hand-reproduced formula.
func seamTiledRows(t *testing.T, root *layout.Node, area layout.Rect) int {
	t.Helper()
	rects, ok := layout.MiniRects(root, area, layout.AllPanes(root), "tiled")
	if !ok {
		t.Fatalf("MiniRects(tiled) over %+v failed", area)
	}
	rows := map[int]bool{}
	for _, r := range rects {
		rows[r.Y] = true
	}
	return len(rows)
}

// TestLayoutSeamTiledRowsMatchPaneArea drives a real 200x30 screen, the
// review's live-lie repro, through layoutPaletteGeom and the server's
// pane-area walk. The extreme-wide aspect (rail 20 leaves area 180x28) cannot
// sit inside the 24x12 to 40x20 band without stretching one axis, so the fix
// drops the band; the stretched 40x12 band the review rejected is gone. On a
// screen whose area fits the band, the preview keeps the pane area's aspect,
// so the tiled row count it shows is the server's over the pane area for 4, 5
// and 6 panes.
func TestLayoutSeamTiledRowsMatchPaneArea(t *testing.T) {
	t.Parallel()
	for _, n := range []int{4, 5, 6} {
		root := seamPanes(n)
		snap := testutil.TestSnapshot(root, 200, 30, nil)
		snap.Rail = 20
		g := layoutPaletteGeom(snap, n)
		if g.preview.W != 0 || g.preview.H != 0 {
			t.Fatalf("%d panes on 200x30 kept a %+v preview, want it dropped", n, g.preview)
		}
	}
	for _, n := range []int{4, 5, 6} {
		root := seamPanes(n)
		snap := testutil.TestSnapshot(root, 100, 30, nil)
		snap.Rail = 34
		area := layout.PaneAreaForWithTabBar(100, 30, 34)
		g := layoutPaletteGeom(snap, n)
		if g.preview.W == 0 {
			t.Fatalf("%d panes on 100x30 dropped the preview: %+v", n, g)
		}
		serverRows := seamTiledRows(t, root, area)
		if got := seamTiledRows(t, root, g.preview); got != serverRows {
			t.Fatalf("%d panes on 100x30: preview tiled rows=%d, server area rows=%d", n, got, serverRows)
		}
	}
}

// TestLayoutPaletteGeomLiftsToMinimum pins the lift-to-minimum on an extreme
// aspect. A wide 86x22 screen, rail 20, leaves area 66x20; lifting to the
// band's minimums by the larger scale factor lands the band at its maximum
// width and minimum height (40x12) while keeping the area's aspect, never by
// stretching one axis alone. The tiled row count it shows stays the server's
// over the pane area for 4, 5 and 6 panes.
func TestLayoutPaletteGeomLiftsToMinimum(t *testing.T) {
	t.Parallel()
	root := seamPanes(6)
	snap := testutil.TestSnapshot(root, 86, 22, nil)
	snap.Rail = 20
	area := layout.PaneAreaForWithTabBar(86, 22, 20)
	g := layoutPaletteGeom(snap, 6)
	if g.preview.W != layoutPreviewMaxW || g.preview.H != layoutPreviewMinH {
		t.Fatalf("extreme band = %dx%d, want the lift to %dx%d", g.preview.W, g.preview.H, layoutPreviewMaxW, layoutPreviewMinH)
	}
	for _, n := range []int{4, 5, 6} {
		r := seamPanes(n)
		serverRows := seamTiledRows(t, r, area)
		if got := seamTiledRows(t, r, g.preview); got != serverRows {
			t.Fatalf("%d panes: preview tiled rows=%d, server area rows=%d", n, got, serverRows)
		}
	}
}

// TestLayoutMousePaths: motion lights the row under the pointer, a press and
// release on one row cell picks it, a press on the preview band does nothing,
// and a press outside the palette closes it.
func TestLayoutMousePaths(t *testing.T) {
	t.Parallel()
	tui, got := layoutTUI(t)
	openLayout(t, tui)
	g := layoutPaletteGeom(tui.snap, len(tui.lsw.panes))
	if g.preview.W == 0 {
		t.Fatalf("the 100x30 fixture dropped the preview: %+v", g)
	}
	// Motion over the first list row.
	feed(tui, fmt.Sprintf("\x1b[<32;35;%dM", g.listY+1))
	if tui.lsw.sel != 0 {
		t.Fatalf("hover = row %d, want 0", tui.lsw.sel)
	}
	// A press and release on the second row picks it.
	feed(tui, fmt.Sprintf("\x1b[<0;35;%dM", g.listY+2))
	feed(tui, fmt.Sprintf("\x1b[<0;35;%dm", g.listY+2))
	if tui.mode != "" || tui.lsw != nil {
		t.Fatal("the click left the palette open")
	}
	select {
	case m := <-got:
		if m.Action != "select-layout" || m.Layout != "even-vertical" {
			t.Fatalf("click sent %+v, want select-layout even-vertical", m)
		}
	case <-time.After(time.Second):
		t.Fatal("the click sent nothing")
	}
	// A press on the preview band does nothing.
	openLayout(t, tui)
	feed(tui, "\x1b[<0;35;10M")
	feed(tui, "\x1b[<0;35;10m")
	if tui.mode != "layout" || tui.lsw == nil {
		t.Fatal("a press on the preview band closed the palette")
	}
	testutil.NoMsg(t, got)
	// A press outside the palette closes it.
	feed(tui, "\x1b[<0;2;2M")
	if tui.mode != "" || tui.lsw != nil {
		t.Fatal("a press outside the palette left it open")
	}
	testutil.NoMsg(t, got)
}

// TestLayoutKeymapModeListed: the keymap view lists the layout mode with its
// bindings, after the switcher.
func TestLayoutKeymapModeListed(t *testing.T) {
	t.Parallel()
	rows := keymapview.KeymapRows(keymap.LoadDefault())
	at := -1
	for i, r := range rows {
		if r == " [layout]" {
			at = i
		}
	}
	if at < 0 {
		t.Fatal("the keymap view did not list the layout mode")
	}
	if !strings.Contains(strings.Join(rows[:at], "\n"), "[switcher]") {
		t.Fatal("the layout mode did not come after the switcher")
	}
	joined := strings.Join(rows[at:at+10], "\n")
	for _, want := range []string{"j", "k", "enter", "q", "esc"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("layout rows %q miss key %q", joined, want)
		}
	}
}
