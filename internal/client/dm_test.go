package client

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"dm/internal/cells"
	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/rail"
	"dm/internal/segment"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/transport"
	"dm/internal/vt"
	"dm/internal/wire"

	"github.com/hinshun/vt10x"
)

func TestPadHitFindsThePair(t *testing.T) {
	a, b, c := "a", "b", "c"
	flat := &layout.Node{Dir: "row", Kids: []*layout.Node{{Pane: a}, {Pane: b}}, Sizes: []float64{0.5, 0.5}}
	area := layout.Rect{X: 0, Y: 0, W: 80, H: 24}
	boxes := layout.ChildBoxRects(flat, area)
	pad := layout.PadRect(flat, boxes, 0)
	split, first, second, axis, sr, _, ok := padHit(flat, area, pad.X, pad.Y+3)
	if !ok || split != flat || first != a || second != b || axis != "row" || sr != area {
		t.Fatalf("pad hit = %v %q %q %q %v %v", split, first, second, axis, sr, ok)
	}
	if _, _, _, _, _, _, ok := padHit(flat, area, boxes[0].X+1, boxes[0].Y+1); ok {
		t.Fatal("a pane interior hit the pad")
	}

	// Nested: the outer pad pairs a with the right column.
	inner := &layout.Node{Dir: "row", Kids: []*layout.Node{{Pane: b}, {Pane: c}}, Sizes: []float64{0.5, 0.5}}
	nested := &layout.Node{Dir: "row", Kids: []*layout.Node{{Pane: a}, inner}, Sizes: []float64{0.6, 0.4}}
	nb := layout.ChildBoxRects(nested, area)
	np := layout.PadRect(nested, nb, 0)
	_, first, second, axis, _, _, ok = padHit(nested, area, np.X, np.Y+3)
	if !ok || axis != "row" || first != a || (second != b && second != c) {
		t.Fatalf("nested pad hit = %q %q %q %v", first, second, axis, ok)
	}
	ib := layout.ChildBoxRects(inner, nb[1])
	ipad := layout.PadRect(inner, ib, 0)
	_, first, second, _, _, _, ok = padHit(nested, area, ipad.X, ipad.Y+3)
	if !ok || first != b || second != c {
		t.Fatalf("inner pad hit = %q %q %v", first, second, ok)
	}
}

func TestDrawPaneColors(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(24, 2))
	if _, err := vt.Write([]byte(
		"\x1b[38;2;30;144;255mTRUE" +
			"\x1b[0m\x1b[48;2;10;20;30mBG" +
			"\x1b[0m\x1b[38;5;196mPAL" +
			"\x1b[0m\x1b[7mREV\x1b[0m")); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	drawPane(&buf, vt, layout.Rect{X: 0, Y: 0, W: 24, H: 2})
	out := buf.String()
	for _, want := range []string{"38;2;30;144;255", "48;2;10;20;30", "38;5;196"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in rendered output", want)
		}
	}
	// Reverse over the default colours must reach the terminal as SGR 7.
	rev := out[strings.Index(out, "REV")-40 : strings.Index(out, "REV")]
	if !strings.Contains(rev, "[0;7;") && !strings.Contains(rev, ";7;") && !strings.Contains(rev, ";7m") {
		t.Fatalf("reverse not emitted: %q", rev)
	}
}

func TestDrawSessionsWidth(t *testing.T) {
	const cols, rows = 100, 30
	sessions := []transport.SessionInfo{{Name: "alpha", Live: true}, {Name: "beta"}}
	var buf bytes.Buffer
	drawSessions(&buf, theme.DefaultTheme(), cols, rows, sessions, 1, "alpha")
	screen := panelFilled(t, cols, rows, buf.Bytes())

	// The picker is 46 wide and centred: its top row, one row per session, the
	// tee row, the key hint and its bottom row.
	w := 46
	x := (cols - w) / 2
	y := (rows - (len(sessions) + 5)) / 2
	edges := []struct {
		row         int
		left, right rune
	}{
		{y, []rune(theme.DefaultTheme().Glyphs.BorderTopLeft)[0], []rune(theme.DefaultTheme().Glyphs.BorderTopRight)[0]},
		{y + 1, []rune(theme.DefaultTheme().Glyphs.BorderVertical)[0], []rune(theme.DefaultTheme().Glyphs.BorderVertical)[0]},
		{y + 2, []rune(theme.DefaultTheme().Glyphs.BorderVertical)[0], []rune(theme.DefaultTheme().Glyphs.BorderVertical)[0]},
		{y + 3, []rune(theme.DefaultTheme().Glyphs.BorderTeeLeft)[0], []rune(theme.DefaultTheme().Glyphs.BorderTeeRight)[0]},
		{y + 4, []rune(theme.DefaultTheme().Glyphs.BorderVertical)[0], []rune(theme.DefaultTheme().Glyphs.BorderVertical)[0]},
		{y + 5, []rune(theme.DefaultTheme().Glyphs.BorderBottomLeft)[0], []rune(theme.DefaultTheme().Glyphs.BorderBottomRight)[0]},
	}
	for _, e := range edges {
		if got := panelChar(screen, x, e.row); got != e.left {
			t.Fatalf("row %d starts with %q, want %q", e.row, got, e.left)
		}
		if got := panelChar(screen, x+w-1, e.row); got != e.right {
			t.Fatalf("row %d ends with %q, want %q", e.row, got, e.right)
		}
	}
	// Every cell of every box row is painted and no cell beside the box is.
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			got := panelChar(screen, col, row)
			in := row >= y && row <= y+5 && col >= x && col < x+w
			if in && got == 'X' {
				t.Fatalf("row %d leaves cell %d of the box unpainted", row, col)
			}
			if !in && got != 'X' {
				t.Fatalf("row %d paints cell %d outside the box: %q", row, col, got)
			}
		}
	}
}

// A pane daemon outlives the server, so the read reply must keep its shape.
func TestParseMouse(t *testing.T) {
	ev, n, ok := parseMouse([]byte("\x1b[<64;12;7Mrest"))
	if !ok || n != 11 || ev.btn != 64 || ev.x != 12 || ev.y != 7 || ev.release {
		t.Fatalf("wheel up: %+v n=%d ok=%v", ev, n, ok)
	}
	ev, _, ok = parseMouse([]byte("\x1b[<65;1;1m"))
	if !ok || ev.btn != 65 || !ev.release {
		t.Fatalf("wheel down release: %+v", ev)
	}
	if _, n, ok := parseMouse([]byte("\x1b[<64;12")); ok || n != 0 {
		t.Fatalf("partial sequence: n=%d ok=%v", n, ok)
	}
	if _, n, ok := parseMouse([]byte("\x1b[A")); ok || n != -1 {
		t.Fatalf("non-mouse: n=%d ok=%v", n, ok)
	}
}

func TestScrollStart(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(40, 100))
	var b strings.Builder
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&b, "line %d\r\n", i)
	}
	if _, err := vt.Write([]byte(b.String())); err != nil {
		t.Fatal(err)
	}
	live, max := scrollStart(vt, 10, 0)
	if live < 50 || live+10 > 100 {
		t.Fatalf("live start = %d", live)
	}
	if max < 45 {
		t.Fatalf("max offset = %d", max)
	}
	scrolled, _ := scrollStart(vt, 10, 5)
	if scrolled != live-5 {
		t.Fatalf("scrolled start = %d, want %d", scrolled, live-5)
	}
	top, _ := scrollStart(vt, 10, max+100)
	if top != 0 {
		t.Fatalf("clamped start = %d, want 0", top)
	}
}

func TestFocusForClick(t *testing.T) {
	p := &wire.PaneInfo{ID: "w1p2", Mouse: true}
	if got := focusForClick(p, "w1p1", mouseEvent{btn: 0}); got != "w1p2" {
		t.Fatalf("left press on a mouse-claiming pane = %q", got)
	}
	if got := focusForClick(p, "w1p2", mouseEvent{btn: 0}); got != "" {
		t.Fatalf("left press on the focused pane = %q", got)
	}
	if got := focusForClick(p, "w1p1", mouseEvent{btn: 64}); got != "" {
		t.Fatalf("wheel focused a pane: %q", got)
	}
	if got := focusForClick(p, "w1p1", mouseEvent{btn: 0, release: true}); got != "" {
		t.Fatalf("release focused a pane: %q", got)
	}
	if got := focusForClick(p, "w1p1", mouseEvent{btn: 32}); got != "" {
		t.Fatalf("motion focused a pane: %q", got)
	}
}

// TestSessionSizeIsLargestClient keeps a small client, such as a dm nested in
// one of the session's own panes, from shrinking every other display.
// TestSameSessionAttachRefused stops a dm nested in a pane from attaching the
// session it already lives in.
func TestSameSessionAttachRefused(t *testing.T) {
	t.Setenv("DM_SESSION", "main")
	if err := Run("main"); err == nil || !strings.Contains(err.Error(), "already inside") {
		t.Fatalf("same-session attach = %v, want refusal", err)
	}
	t.Setenv("DM_SESSION", "work")
	err := Run("main")
	if err == nil || strings.Contains(err.Error(), "already inside") {
		t.Fatalf("other-session attach = %v, want the terminal check", err)
	}
}

func TestRailItemsGrouping(t *testing.T) {
	snap := &wire.Snapshot{All: []wire.AllPane{
		{
			ID: "w1p1", Name: "alpha", Workspace: "s1", WorkspaceName: "main",
			Window: "w1", WindowName: "one", WorkspaceActive: true, WindowActive: true,
			Focus: true, WindowUnfolded: true, WorkspaceUnfolded: true, Message: "working hard",
		},
		{
			ID: "w1p2", Workspace: "s1", WorkspaceName: "main", Window: "w1",
			WindowName: "one", WindowUnfolded: true, WorkspaceUnfolded: true,
		},
		{
			ID: "w1p3", Workspace: "s1", WorkspaceName: "main", Window: "w2",
			WindowName: "two", WindowUnfolded: true, WorkspaceUnfolded: true,
		},
		{
			ID: "w2p1", App: "pi", Agent: true, Workspace: "s2", WorkspaceName: "work",
			Window: "w3", WindowName: "three", WindowUnfolded: true, WorkspaceUnfolded: true,
		},
	}}
	items := rail.RailItems(snap)
	wantKinds := []rail.RailKind{
		rail.Workspace, rail.Window, rail.WindowNote, rail.Pane, rail.Pane,
		rail.Window, rail.Pane,
		rail.Gap,
		rail.Workspace, rail.Window, rail.Pane,
	}
	wantText := []string{
		"main", "one", "working hard", "alpha", "w1p2",
		"two", "w1p3",
		"",
		"work", "three", "pi",
	}
	if len(items) != len(wantKinds) {
		t.Fatalf("rail has %d rows, want %d: %+v", len(items), len(wantKinds), items)
	}
	for i, want := range wantKinds {
		if items[i].Kind != want || items[i].Text != wantText[i] {
			t.Fatalf("row %d = kind %d %q, want kind %d %q", i,
				items[i].Kind, items[i].Text, want, wantText[i])
		}
	}
	gaps := 0
	for _, it := range items {
		if it.Kind == rail.Gap {
			gaps++
		}
	}
	if gaps != 1 || items[7].Kind != rail.Gap {
		t.Fatalf("rail has %d gaps, want one at row 7: %+v", gaps, items)
	}
	if items[0].Count != 3 || items[8].Count != 1 {
		t.Fatalf("workspace pane counts = %d, %d, want 3, 1", items[0].Count, items[8].Count)
	}
	if items[0].Workspace != "s1" || items[8].Workspace != "s2" {
		t.Fatalf("workspace ids = %q, %q", items[0].Workspace, items[8].Workspace)
	}
	if !items[0].Active || items[8].Active {
		t.Fatalf("workspace rows active = %v, %v", items[0].Active, items[8].Active)
	}
	if !items[10].Agent || items[3].Agent {
		t.Fatalf("pane rows agent = %v, %v", items[10].Agent, items[3].Agent)
	}
	if !items[3].Focus || items[4].Focus {
		t.Fatalf("pane rows focus = %v, %v", items[3].Focus, items[4].Focus)
	}
	// A folded workspace drops its windows and panes and leaves just the
	// workspace row.
	for i := range snap.All {
		if snap.All[i].Workspace == "s1" {
			snap.All[i].WorkspaceUnfolded = false
		}
	}
	items = rail.RailItems(snap)
	if len(items) != 5 || items[0].Kind != rail.Workspace || items[1].Kind != rail.Gap ||
		items[2].Kind != rail.Workspace || items[3].Kind != rail.Window || items[4].Kind != rail.Pane {
		t.Fatalf("folded rail rows = %+v, want workspace, gap, workspace, window, pane", items)
	}
}

func TestRailGlyphPerState(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	cases := []struct {
		status string
		glyph  string
		colour theme.RGB
	}{
		{"working", "●", theme.RGB{R: 245, G: 190, B: 80}},
		{"idle", "○", theme.RGB{R: 120, G: 200, B: 225}},
		{"blocked", "▲", theme.RGB{R: 255, G: 110, B: 110}},
		{"done", "✓", theme.RGB{R: 110, G: 225, B: 145}},
		{"error", "✕", theme.RGB{R: 255, G: 130, B: 160}},
		{"unknown", "?", theme.DefaultTheme().Palette.RailDim},
		{"", "?", theme.DefaultTheme().Palette.RailDim},
	}
	seen := map[theme.RGB]string{}
	for _, c := range cases {
		g, colour := rail.RailGlyph(theme.DefaultTheme(), c.status, false)
		if g != c.glyph || colour != c.colour {
			t.Fatalf("rail.RailGlyph(theme.DefaultTheme(), %q) = %q/%v, want %q/%v", c.status, g, colour, c.glyph, c.colour)
		}
		if prev, ok := seen[colour]; ok && prev != c.glyph {
			t.Fatalf("colour %v doubles for %q and %q", colour, prev, c.glyph)
		}
		seen[colour] = c.glyph

		var b bytes.Buffer
		rail.DrawRail(&b, theme.DefaultTheme(), testutil.AgentRail(c.status))
		out := b.String()
		if !strings.Contains(out, c.glyph) || !strings.Contains(out, colour.FG()) {
			t.Fatalf("status %q not drawn as %q in %q: %q", c.status, c.glyph, colour.FG(), out)
		}
	}
}

// TestRailContrast holds the rail to WCAG AA on both backgrounds it paints.
func TestRailContrast(t *testing.T) {
	foregrounds := map[string]theme.RGB{
		"text":    theme.DefaultTheme().Palette.RailText,
		"dim":     theme.DefaultTheme().Palette.RailDim,
		"header":  theme.DefaultTheme().Palette.RailHeader,
		"window":  theme.DefaultTheme().Palette.RailWindow,
		"diffAdd": theme.DefaultTheme().Palette.RailDiffAdd,
		"diffDel": theme.DefaultTheme().Palette.RailDiffDel,
	}
	for i, accent := range theme.DefaultTheme().Accents {
		foregrounds[fmt.Sprintf("accent %d", i)] = accent
	}
	for _, status := range []string{
		"working", "idle", "blocked", "done", "error", "unknown", "",
	} {
		fallback, colour := rail.RailGlyph(theme.DefaultTheme(), status, false)
		_, nerdColour := rail.RailGlyph(theme.DefaultTheme(), status, true)
		if fallback == "" || nerdColour != colour {
			t.Fatalf("state %q differs between glyph forms", status)
		}
		foregrounds["state "+status] = colour
	}
	for name, fg := range foregrounds {
		for bgName, bg := range map[string]theme.RGB{"rail": theme.DefaultTheme().Palette.RailBG, "focus": theme.DefaultTheme().Palette.RailFocusBG} {
			if r := theme.Contrast(fg, bg); r < 4.5 {
				t.Fatalf("%s on %s = %.2f:1, want >= 4.5:1", name, bgName, r)
			}
		}
	}
	if r := theme.Contrast(theme.DefaultTheme().Palette.RailText, theme.DefaultTheme().Palette.RailBG); r < 7 {
		t.Fatalf("body text on rail = %.2f:1, want >= 7:1", r)
	}
}

// TestRailAccent keeps the accent hash stable per id and inside the slot count.
// The built-in palette's six hues are distinct, so two slots never paint the
// same colour. Giving two live workspaces different slots is rail.RailItems' job.
func TestRailAccent(t *testing.T) {
	for _, ws := range []string{"main", "work", "dm-git-w1", ""} {
		first, second := rail.RailAccentSlot(ws), rail.RailAccentSlot(ws)
		if first != second {
			t.Fatalf("rail.RailAccentSlot(%q) = %d then %d", ws, first, second)
		}
	}
	slots := len(theme.DefaultTheme().Accents)
	if slots != 6 {
		t.Fatalf("built-in accents = %d, want 6 slots", slots)
	}
	seen := map[theme.RGB]bool{}
	for _, accent := range theme.DefaultTheme().Accents {
		if seen[accent] {
			t.Fatalf("accent %v repeats in the built-in palette", accent)
		}
		seen[accent] = true
	}
	for _, ws := range []string{"main", "work", "scratch", "s1", "s2", "ws1", "ws6"} {
		if slot := rail.RailAccentSlot(ws); slot < 0 || slot >= slots {
			t.Fatalf("rail.RailAccentSlot(%q) = %d, want a slot in [0,%d)", ws, slot, slots)
		}
	}
}

// TestRailShellHasNoStateGlyph keeps the state column blank for a pane that is
// not an agent: it has no status to report, so the question mark would ask
// about nothing.
func TestRailShellHasNoStateGlyph(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	for _, status := range []string{"", "unknown"} {
		var b bytes.Buffer
		rail.DrawRail(&b, theme.DefaultTheme(), testutil.RailOnePane(status, "w1p1", 20, 40, 6))
		vt := vt10x.New(vt10x.WithSize(40, 6))
		if _, err := vt.Write(b.Bytes()); err != nil {
			t.Fatalf("draw shell rail: %v", err)
		}
		vt.Lock()
		glyph, text := vt.Cell(8, 2).Char, vt.Cell(10, 2).Char
		vt.Unlock()
		if glyph != ' ' || text != 'w' {
			t.Fatalf("shell row status %q = %q at 8, %q at 10: want a blank cell",
				status, glyph, text)
		}
	}

	// An agent that has not reported yet still asks with the question mark.
	var b bytes.Buffer
	rail.DrawRail(&b, theme.DefaultTheme(), testutil.AgentRail(""))
	vt := vt10x.New(vt10x.WithSize(40, 6))
	if _, err := vt.Write(b.Bytes()); err != nil {
		t.Fatalf("draw agent rail: %v", err)
	}
	vt.Lock()
	glyph := vt.Cell(8, 2).Char
	vt.Unlock()
	if glyph != '?' {
		t.Fatalf("agent row without a status = %q at 8, want ?", glyph)
	}
}

// TestRailAgentMarker separates agents from shells. An agent gets a bar in the
// two cells a shell leaves blank, and its app= stands in for a missing name.
func TestRailAgentMarker(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	agent := testutil.RailOnePane("unknown", "", 20, 40, 6)
	agent.All[0].Agent = true
	agent.All[0].App = "pi"
	var b bytes.Buffer
	rail.DrawRail(&b, theme.DefaultTheme(), agent)
	out := b.String()
	if !strings.Contains(out, "\u258e") {
		t.Fatalf("agent row has no marker: %q", out)
	}
	if !strings.Contains(out, "pi") {
		t.Fatalf("agent row does not show the app: %q", out)
	}

	shell := testutil.RailOnePane("unknown", "", 20, 40, 6)
	b.Reset()
	rail.DrawRail(&b, theme.DefaultTheme(), shell)
	out = b.String()
	if strings.Contains(out, "\u258e") {
		t.Fatalf("shell row has the agent marker: %q", out)
	}
	if !strings.Contains(out, "w1p1") {
		t.Fatalf("shell row does not fall back to the pane id: %q", out)
	}

	// The thin rail keeps the marker and the glyph in their columns.
	thin := testutil.RailOnePane("working", "alpha", layout.CollapsedRail, 40, 6)
	thin.All[0].Agent = true
	b.Reset()
	rail.DrawRail(&b, theme.DefaultTheme(), thin)
	vt := vt10x.New(vt10x.WithSize(40, 6))
	if _, err := vt.Write(b.Bytes()); err != nil {
		t.Fatalf("draw thin agent rail: %v", err)
	}
	vt.Lock()
	bar, glyph := vt.Cell(0, 0).Char, vt.Cell(2, 0).Char
	vt.Unlock()
	if bar != '\u258e' || glyph != '●' {
		t.Fatalf("thin agent row = %q at 0, %q at 2", bar, glyph)
	}
}

func TestRailHit(t *testing.T) {
	snap := testutil.RailTestSnapshot(10)
	// Rows: workspace, window, w1p1, w1p2.
	cases := []struct {
		x, y int
		want string
	}{
		{5, 2, "w1p1"},
		{5, 3, "w1p2"},
		{5, 0, ""},  // workspace header
		{5, 1, ""},  // window row
		{5, 4, ""},  // past the last row
		{5, 9, ""},  // status bar
		{10, 2, ""}, // pane area
		{-1, 2, ""}, // left of the screen
	}
	for _, c := range cases {
		if got := rail.RailPaneAt(snap, c.x, c.y); got != c.want {
			t.Fatalf("rail.RailPaneAt(%d,%d) = %q, want %q", c.x, c.y, got, c.want)
		}
	}
	// The last item row above the control can be a pane row. The control
	// takes Rows-2, so the item rows stop at Rows-3.
	small := testutil.RailTestSnapshot(10)
	small.Rows = 6
	small.All = []wire.AllPane{
		{
			ID: "w1p1", Workspace: "s1", WorkspaceName: "main", Window: "w1",
			WindowName: "one", WindowUnfolded: true, WorkspaceUnfolded: true,
		},
		{
			ID: "w1p2", Workspace: "s1", WorkspaceName: "main", Window: "w1",
			WindowName: "one", WindowUnfolded: true, WorkspaceUnfolded: true,
		},
	}
	if got := rail.RailPaneAt(small, 5, 3); got != "w1p2" {
		t.Fatalf("last pane row = %q", got)
	}
}

func TestRailClickFocusesPane(t *testing.T) {
	tui, got := clientTUI(t, nil)
	tui.snap = testutil.RailTestSnapshot(10)
	// Left press on w1p2's row focuses w1p2, not the focused w1p1. The row is
	// zero-based y=3, so the one-based mouse row is 4.
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 4})
	if m := testutil.NextMsg(t, got); m.Action != "focus" || m.Target != "w1p2" {
		t.Fatalf("rail click sent %+v, want focus w1p2", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 4})
	// A workspace row focuses its first pane; the caret cell toggles its fold.
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 1})
	if m := testutil.NextMsg(t, got); m.Action != "focus" || m.Target != "w1p1" {
		t.Fatalf("workspace click sent %+v, want focus w1p1", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 1})
	tui.handleMouse(mouseEvent{btn: 0, x: 1, y: 1})
	if m := testutil.NextMsg(t, got); m.Action != "toggle-fold" || m.Target != "s1" {
		t.Fatalf("caret click sent %+v, want toggle-fold s1", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 1, y: 1})
	// A pane click still focuses the pane under the pointer, not a rail row.
	p := tui.paneByID("w1p2")
	tui.handleMouse(mouseEvent{btn: 0, x: p.Rect.X + 1, y: p.Rect.Y + 1})
	if m := testutil.NextMsg(t, got); m.Action != "focus" || m.Target != "w1p2" {
		t.Fatalf("pane click sent %+v, want focus w1p2", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: p.Rect.X + 1, y: p.Rect.Y + 1})
	// A right press in the rail opens the row's menu, not the pane menu.
	tui.handleMouse(mouseEvent{btn: 2, x: 3, y: 3})
	if tui.mode != "menu" || tui.menu == nil || tui.menu.pane != "" {
		t.Fatalf("rail right press opened %q %+v", tui.mode, tui.menu)
	}
	tui.closeMenu()
	// A wheel over a pane row must not focus it. SGR wheel-up is button 64.
	tui.handleMouse(mouseEvent{btn: 64, x: 3, y: 3})
	select {
	case m := <-got:
		t.Fatalf("rail wheel sent %+v", m)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestRailFold keeps panes folded by default and unfolds on the workspace row.
func TestRailFold(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := testutil.RailTestSnapshot(20)
	for i := range snap.All {
		snap.All[i].WorkspaceUnfolded = false
	}
	items := rail.RailItems(snap)
	if len(items) != 1 || items[0].Kind != rail.Workspace {
		t.Fatalf("folded rail rows = %+v, want just the workspace", items)
	}
	if !items[0].Folded || items[0].Workspace != "s1" {
		t.Fatalf("folded workspace row = %+v", items[0])
	}
	var b bytes.Buffer
	rail.DrawRail(&b, theme.DefaultTheme(), snap)
	if strings.Contains(b.String(), "alpha") {
		t.Fatalf("folded rail drew a pane: %q", b.String())
	}
	if got := rail.RailWorkspaceAt(snap, 3, 0); got != "s1" {
		t.Fatalf("workspace row hit = %q, want s1", got)
	}
	if got := rail.RailPaneAt(snap, 3, 0); got != "" {
		t.Fatalf("workspace row is a pane hit: %q", got)
	}

	// The thin rail is a glyph column, so it lists panes regardless of the fold
	// and has no workspace row to unfold.
	snap.Rail = layout.CollapsedRail
	items = rail.RailItems(snap)
	if len(items) != 2 || items[0].Kind != rail.Pane || items[1].Kind != rail.Pane {
		t.Fatalf("thin rail rows = %+v, want the two panes", items)
	}
	if items[0].Pane != "w1p1" || items[1].Pane != "w1p2" {
		t.Fatalf("thin rail panes = %q, %q, want w1p1, w1p2", items[0].Pane, items[1].Pane)
	}
	if got := rail.RailWorkspaceAt(snap, 3, 0); got != "" {
		t.Fatalf("thin rail row is a workspace hit: %q", got)
	}
}

// railTree is two workspaces, each with one unfolded window. The first is a
// dirty linked worktree with stats and a message; the second is clean.
func railTree(rail int) *wire.Snapshot {
	snap := &wire.Snapshot{
		Cols: 100, Rows: 12, Rail: rail,
		Root: &layout.Node{Pane: "w1p1"}, Focus: "w1p1",
	}
	snap.Panes = []wire.PaneInfo{{ID: "w1p1", Status: "working"}}
	snap.All = []wire.AllPane{
		{
			ID: "w1p1", Name: "pi", Status: "working", Agent: true,
			Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one",
			Focus: true, WorkspaceActive: true, WindowActive: true, WindowUnfolded: true, WorkspaceUnfolded: true,
			Worktree: "dm-rail-w1", Branch: "lenient-branch", InRepo: true,
			Dirty: true, Added: 12, Deleted: 4, Message: "working hard",
		},
		{
			ID: "w2p1", Workspace: "s2", WorkspaceName: "work", Window: "w2",
			WindowName: "two", WindowUnfolded: true, WorkspaceUnfolded: true, Branch: "main",
		},
	}
	return snap
}

// railRows draws a rail and returns the text of each row inside the rail width.
func railRows(t *testing.T, snap *wire.Snapshot) []string {
	t.Helper()
	var b bytes.Buffer
	rail.DrawRail(&b, theme.DefaultTheme(), snap)
	vt := vt10x.New(vt10x.WithSize(snap.Cols, snap.Rows))
	if _, err := vt.Write(b.Bytes()); err != nil {
		t.Fatalf("draw rail: %v", err)
	}
	out := make([]string, snap.Rows-1)
	vt.Lock()
	for y := range out {
		var sb strings.Builder
		for x := 0; x < snap.Rail; x++ {
			g := vt.Cell(x, y)
			if g.Char == 0 {
				sb.WriteByte(' ')
				continue
			}
			sb.WriteRune(g.Char)
		}
		out[y] = strings.TrimRight(sb.String(), " ")
	}
	vt.Unlock()
	return out
}

// TestRailWorkspaceAccent paints the caret in the workspace's own hue, keeps the
// pane count at the right edge, and leaves a gap row empty.
func TestRailWorkspaceAccent(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railTree(layout.DefaultRail)
	want := map[string]theme.RGB{}
	for _, it := range rail.RailItems(snap) {
		if it.Kind == rail.Workspace {
			want[it.Workspace] = theme.DefaultTheme().Accents[it.Accent]
		}
	}
	if len(want) != 2 || want["s1"] == want["s2"] {
		t.Fatalf("workspace accents = %v, want two distinct hues", want)
	}
	var b bytes.Buffer
	rail.DrawRail(&b, theme.DefaultTheme(), snap)
	out := b.String()
	for ws, accent := range want {
		if !strings.Contains(out, accent.FG()+"▾") {
			t.Fatalf("workspace %q does not wear its accent %v: %q", ws, accent, out)
		}
	}
	vt := vt10x.New(vt10x.WithSize(100, 12))
	if _, err := vt.Write(b.Bytes()); err != nil {
		t.Fatalf("draw accent rail: %v", err)
	}
	vt.Lock()
	// The grip strip owns the rail's last column, so the count ends one cell in.
	count := vt.Cell(layout.DefaultRail-2, 0).Char
	gapRow := true
	for x := 0; x < layout.DefaultRail; x++ {
		if g := vt.Cell(x, 4); g.Char != 0 && g.Char != ' ' {
			gapRow = false
		}
	}
	vt.Unlock()
	if count != '1' {
		t.Fatalf("workspace count at the right edge = %q, want 1", count)
	}
	if !gapRow {
		t.Fatal("gap row is not empty")
	}
}

// TestRailAccentDistinct gives live workspaces distinct hues even when their
// ids hash to the same preferred slot.
func TestRailAccentDistinct(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	ids := []string{"s2", "s3", "s4", "s5"}
	if rail.RailAccentSlot("s3") != rail.RailAccentSlot("s4") || rail.RailAccentSlot("s2") != rail.RailAccentSlot("s5") {
		t.Fatalf("fixture ids do not collide: %v %v %v %v",
			rail.RailAccentSlot("s2"), rail.RailAccentSlot("s3"), rail.RailAccentSlot("s4"), rail.RailAccentSlot("s5"))
	}
	snap := &wire.Snapshot{Cols: 100, Rows: 20, Rail: layout.DefaultRail}
	for _, id := range ids {
		snap.All = append(snap.All, wire.AllPane{
			ID: id + "p1", Workspace: id, WorkspaceName: id,
			Window: id + "w1", WindowName: "one", WindowUnfolded: true, WorkspaceUnfolded: true,
		})
	}
	seen := map[theme.RGB]string{}
	want := map[string]theme.RGB{}
	for _, it := range rail.RailItems(snap) {
		if it.Kind != rail.Workspace {
			continue
		}
		if prev, ok := seen[theme.DefaultTheme().Accents[it.Accent]]; ok {
			t.Fatalf("workspaces %q and %q share accent %v", prev, it.Workspace, theme.DefaultTheme().Accents[it.Accent])
		}
		seen[theme.DefaultTheme().Accents[it.Accent]] = it.Workspace
		want[it.Workspace] = theme.DefaultTheme().Accents[it.Accent]
	}
	if len(want) != len(ids) || len(seen) != len(ids) {
		t.Fatalf("rail has %d workspace accents, want %d", len(seen), len(ids))
	}
	// rail.DrawRail paints the carried accent, not a fresh hash.
	var b bytes.Buffer
	rail.DrawRail(&b, theme.DefaultTheme(), snap)
	out := b.String()
	for ws, accent := range want {
		if !strings.Contains(out, accent.FG()+"▾") {
			t.Fatalf("workspace %q does not wear its accent %v: %q", ws, accent, out)
		}
	}
}

func TestRailToggle(t *testing.T) {
	tui, got := clientTUI(t, nil)
	// A client that has only seen a hidden rail asks for the default.
	tui.apply(wire.Msg{Type: "snapshot", Snap: &wire.Snapshot{Rail: 0, Cols: 100, Rows: 30}})
	tui.key('b')
	if m := testutil.NextMsg(t, got); m.Action != "set-rail" || m.Index != layout.DefaultRail {
		t.Fatalf("toggle from hidden sent %+v, want set-rail %d", m, layout.DefaultRail)
	}
	// A full rail collapses to the thin width, not to hidden.
	tui.apply(wire.Msg{Type: "snapshot", Snap: &wire.Snapshot{Rail: 30, RailWant: 30, Cols: 100, Rows: 30}})
	tui.key('b')
	if m := testutil.NextMsg(t, got); m.Action != "set-rail" || m.Index != layout.CollapsedRail {
		t.Fatalf("toggle from full sent %+v, want set-rail %d", m, layout.CollapsedRail)
	}
	// The thin rail hides, and the full width is still remembered.
	tui.apply(wire.Msg{Type: "snapshot", Snap: &wire.Snapshot{
		Rail:     layout.CollapsedRail,
		RailWant: layout.CollapsedRail, Cols: 100, Rows: 30,
	}})
	tui.key('b')
	if m := testutil.NextMsg(t, got); m.Action != "set-rail" || m.Index != 0 {
		t.Fatalf("toggle from thin sent %+v, want set-rail 0", m)
	}
	tui.apply(wire.Msg{Type: "snapshot", Snap: &wire.Snapshot{Rail: 0, Cols: 100, Rows: 30}})
	tui.key('b')
	if m := testutil.NextMsg(t, got); m.Action != "set-rail" || m.Index != 30 {
		t.Fatalf("toggle after hide sent %+v, want set-rail 30", m)
	}
	// A narrow terminal clamps the effective rail but the requested width
	// survives, so hiding and showing restores the request, not the clamp.
	tui.apply(wire.Msg{Type: "snapshot", Snap: &wire.Snapshot{
		Rail: layout.EffectiveRail(80, 30), RailWant: 30, Cols: 80, Rows: 30,
	}})
	tui.key('b')
	if m := testutil.NextMsg(t, got); m.Action != "set-rail" || m.Index != layout.CollapsedRail {
		t.Fatalf("narrow collapse sent %+v, want set-rail %d", m, layout.CollapsedRail)
	}
	tui.apply(wire.Msg{Type: "snapshot", Snap: &wire.Snapshot{
		Rail: 0, RailWant: 0, Cols: 80, Rows: 30,
	}})
	tui.key('b')
	if m := testutil.NextMsg(t, got); m.Action != "set-rail" || m.Index != 30 {
		t.Fatalf("narrow toggle on sent %+v, want set-rail 30", m)
	}
	if got := keymap.LoadDefault().Key("main", "rename-workspace"); got != "r" {
		t.Fatalf("rename-workspace key = %q, want r", got)
	}
	found := false
	for _, kv := range keymap.LoadDefault().HelpRows() {
		if kv[0] == "r" && kv[1] == "rename workspace" {
			found = true
		}
	}
	if !found {
		t.Fatal("keymap has no r binding")
	}
}

// TestRailCollapsed draws and hits the thin rail. It shows one state glyph per
// pane and no names, from the first row, inside the fixed collapsed width.
func TestRailCollapsed(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := testutil.RailOnePane("working", "alpha", layout.CollapsedRail, 40, 6)
	var b bytes.Buffer
	rail.DrawRail(&b, theme.DefaultTheme(), snap)
	out := b.String()
	if !strings.Contains(out, "●") {
		t.Fatalf("thin rail has no state glyph: %q", out)
	}
	for _, name := range []string{"main", "one", "alpha"} {
		if strings.Contains(out, name) {
			t.Fatalf("thin rail shows %q: %q", name, out)
		}
	}
	vt := vt10x.New(vt10x.WithSize(40, 6))
	if _, err := vt.Write(b.Bytes()); err != nil {
		t.Fatalf("draw thin rail: %v", err)
	}
	vt.Lock()
	glyphX := -1
	for x := 0; x < 40; x++ {
		g := vt.Cell(x, 0)
		if g.Char == '●' {
			glyphX = x
		}
	}
	vt.Unlock()
	if glyphX != 2 {
		t.Fatalf("state glyph at x=%d, want 2", glyphX)
	}
	if got := rail.RailPaneAt(snap, 2, 0); got != "w1p1" {
		t.Fatalf("thin rail hit = %q, want w1p1", got)
	}
	// Past the fixed width is pane area, not rail.
	if got := rail.RailPaneAt(snap, layout.CollapsedRail, 0); got != "" {
		t.Fatalf("hit past collapsed width = %q", got)
	}
}

func TestRailNeverWritesPastWidth(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	if cells.Cells("A") != 1 || cells.Cells("日") != 2 || cells.Cells("\u0301") != 0 {
		t.Fatalf("cell widths: A=%d 日=%d combining=%d",
			cells.Cells("A"), cells.Cells("日"), cells.Cells("\u0301"))
	}
	cases := []struct {
		name  string
		label string
		wide  bool
	}{
		{"ascii name", strings.Repeat("long-name-", 8), false},
		{"wide name", strings.Repeat("日本語", 8), false},
		{"wide workspace header", "", true},
	}
	for _, c := range cases {
		snap := testutil.RailOnePane("working", c.label, 6, 20, 6)
		if c.wide {
			snap.All[0].WorkspaceName = strings.Repeat("全角見出し", 6)
		}
		var b bytes.Buffer
		rail.DrawRail(&b, theme.DefaultTheme(), snap)
		vt := vt10x.New(vt10x.WithSize(20, 6))
		if _, err := vt.Write(b.Bytes()); err != nil {
			t.Fatalf("%s: draw rail: %v", c.name, err)
		}
		vt.Lock()
		for y := 0; y < 6; y++ {
			for x := 6; x < 20; x++ {
				if g := vt.Cell(x, y); g.Char != 0 && g.Char != ' ' {
					vt.Unlock()
					t.Fatalf("%s: rail wrote %q at %d,%d, past width 6", c.name, g.Char, x, y)
				}
			}
		}
		if g := vt.Cell(0, 0); g.Char == 0 {
			vt.Unlock()
			t.Fatalf("%s: rail drew nothing", c.name)
		}
		vt.Unlock()
	}
}

func TestBarNeverWritesPastWidth(t *testing.T) {
	snap := &wire.Snapshot{
		Session:   strings.Repeat("日本語", 10),
		Workspace: strings.Repeat("作業", 6),
		Windows:   testWindows("一"),
		Cols:      20,
		Rows:      5,
		Focus:     "w1p1",
		Panes: []wire.PaneInfo{{
			ID: "w1p1", Name: strings.Repeat("日本語", 4),
			Status: "working",
		}},
	}
	var b bytes.Buffer
	drawBar(&b, theme.DefaultTheme(), snap, false, "")
	vt := vt10x.New(vt10x.WithSize(20, 5))
	if _, err := vt.Write(b.Bytes()); err != nil {
		t.Fatalf("draw bar: %v", err)
	}
	vt.Lock()
	defer vt.Unlock()
	// The bar is the last row. An overrun would wrap and scroll it up.
	painted := false
	for x := 0; x < 20; x++ {
		if g := vt.Cell(x, 4); g.Char != 0 && g.Char != ' ' {
			painted = true
		}
	}
	if !painted {
		t.Fatal("bar did not paint the last row")
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 20; x++ {
			if g := vt.Cell(x, y); g.Char != 0 && g.Char != ' ' {
				t.Fatalf("bar wrote past its row at %d,%d: %q", x, y, g.Char)
			}
		}
	}
}

// barElideStage names the rung of the elision ladder barTestSnap has reached.
func barElideStage(segs []segment.Seg) string {
	var text strings.Builder
	chips := 0
	for _, s := range segs {
		if s.Pin {
			continue
		}
		text.WriteString(s.Text)
		if len(s.Text) == 1 && s.Text[0] >= '1' && s.Text[0] <= '9' {
			chips++
		}
	}
	all := text.String()
	switch {
	case strings.Contains(all, " api-server"):
		return "full"
	case chips == 3:
		return "names"
	case !strings.Contains(all, "workspace-a"):
		if strings.Contains(all, "dev") {
			return "workspace"
		}
		if strings.Contains(all, " idle") {
			return "session"
		}
		if strings.Contains(all, " shell") {
			return "status"
		}
		return "name"
	}
	return "chips"
}

// TestBarSegmentOrder pins the footer's left block order, its colours, and the
// single gutter cell between segments, with the pinned right block last.
func TestBarSegmentOrder(t *testing.T) {
	snap := testutil.BarTestSnap(80)
	glyph, colour := rail.RailGlyph(theme.DefaultTheme(), "idle", rail.NerdFonts())
	segs := barSegments(theme.DefaultTheme(), snap, true, "READY")

	// Groups, in paint order: session, workspace, one chip per window in index
	// order, the focus chip, then the pinned block.
	groups := [][]string{
		{"dev"},
		{"workspace-a"},
		{"1 api-server"},
		{"▸ ", "2 web"},
		{"3 docs"},
		{glyph, " w1p2", " shell", " idle"},
	}
	var want []string
	for k, group := range groups {
		if k > 0 {
			want = append(want, " ")
		}
		want = append(want, group...)
	}
	want = append(want, "PREFIX ", "READY")
	got := testutil.BarTexts(segs)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("bar spans = %q, want %q", got, want)
	}

	if segs[0].FG != theme.DefaultTheme().Palette.RailText {
		t.Fatalf("session colour = %v, want theme.DefaultTheme().Palette.RailText", segs[0].FG)
	}
	if segs[2].FG != theme.DefaultTheme().Palette.RailHeader {
		t.Fatalf("workspace colour = %v, want theme.DefaultTheme().Palette.RailHeader", segs[2].FG)
	}
	if segs[4].FG != theme.DefaultTheme().Palette.RailDim || segs[4].BG != theme.DefaultTheme().Palette.RailBG || segs[9].FG != theme.DefaultTheme().Palette.RailDim {
		t.Fatalf("inactive chips = %+v %+v, want theme.DefaultTheme().Palette.RailDim on theme.DefaultTheme().Palette.RailBG", segs[4], segs[9])
	}

	// The active window reads as active: an accent mark, then bold text on the
	// focus fill.
	mark, chip := segs[6], segs[7]
	if mark.Text != "▸ " || mark.FG != theme.DefaultTheme().Palette.BarAccent || mark.BG != theme.DefaultTheme().Palette.RailFocusBG {
		t.Fatalf("active mark = %+v, want the accent ▸ on theme.DefaultTheme().Palette.RailFocusBG", mark)
	}
	if chip.Text != "2 web" || !chip.Bold || chip.BG != theme.DefaultTheme().Palette.RailFocusBG {
		t.Fatalf("active chip = %+v, want bold on theme.DefaultTheme().Palette.RailFocusBG", chip)
	}

	// The state glyph is the only state colour on the bar.
	if segs[11].FG != colour || segs[11].BG != theme.DefaultTheme().Palette.RailFocusBG {
		t.Fatalf("focus glyph = %+v, want %v on theme.DefaultTheme().Palette.RailFocusBG", segs[11], colour)
	}
	if segs[12].FG != theme.DefaultTheme().Palette.RailText || !segs[12].Bold {
		t.Fatalf("pane id = %+v, want bold theme.DefaultTheme().Palette.RailText", segs[12])
	}
	if segs[13].FG != theme.DefaultTheme().Palette.RailDim || segs[14].FG != theme.DefaultTheme().Palette.RailDim {
		t.Fatalf("pane name and status = %v %v, want theme.DefaultTheme().Palette.RailDim", segs[13].FG, segs[14].FG)
	}
	for _, k := range []int{11, 12, 13, 14} {
		if segs[k].BG != theme.DefaultTheme().Palette.RailFocusBG {
			t.Fatalf("focus span %d bg = %v, want theme.DefaultTheme().Palette.RailFocusBG", k, segs[k].BG)
		}
	}

	// One gutter cell between segments, and only between them: no group starts
	// or ends with one, and none sits inside the focus chip. The gutter is a
	// single cell of the bar ground, so a colour slip shows here.
	gutters := 0
	for k, s := range segs {
		if s.Pin {
			continue
		}
		if s.Text == " " {
			gutters++
			if s.BG != theme.DefaultTheme().Palette.RailBG || s.BG != theme.DefaultTheme().Palette.RailBG {
				t.Fatalf("gutter %d bg = %v, want the bar ground %v", k, s.BG, theme.DefaultTheme().Palette.RailBG)
			}
			continue
		}
		if s.Text == "" {
			t.Fatalf("span %d is empty", k)
		}
	}
	if gutters != len(groups)-1 {
		t.Fatalf("gutters = %d, want %d", gutters, len(groups)-1)
	}

	// The pinned block comes last, and nothing follows it.
	left := len(want) - 2
	for k := 0; k < left; k++ {
		if segs[k].Pin {
			t.Fatalf("span %d is pinned inside the left block", k)
		}
	}
	if segs[left].Text != "PREFIX " || !segs[left].Pin {
		t.Fatalf("span %d = %+v, want the pinned PREFIX", left, segs[left])
	}
	if segs[len(segs)-1].Text != "READY" || !segs[len(segs)-1].Pin {
		t.Fatalf("last span = %+v, want the pinned info", segs[len(segs)-1])
	}
	if segment.SegCells(segs) > snap.Cols {
		t.Fatalf("bar spans %d cells, want <= %d", segment.SegCells(segs), snap.Cols)
	}
}

// TestBarElide walks the elision ladder: each width stops at the rung it needs
// and never paints past the column count.
func TestBarElide(t *testing.T) {
	glyph, _ := rail.RailGlyph(theme.DefaultTheme(), "idle", rail.NerdFonts())
	cases := []struct {
		cols int
		want string
		text []string
	}{
		{80, "full", []string{
			"dev", " ", "workspace-a", " ", "1 api-server",
			" ", "▸ ", "2 web", " ", "3 docs", " ", glyph, " w1p2", " shell",
			" idle",
		}},
		{50, "names", []string{
			"dev", " ", "workspace-a", " ", "1", " ", "▸ ",
			"2", " ", "3", " ", glyph, " w1p2", " shell", " idle",
		}},
		{34, "workspace", []string{
			"dev", " ", "workspa…", " ", "▸ ", "2", " ",
			glyph, " w1p2", " shell", " idle",
		}},
		{20, "status", []string{
			"…", " ", "…", " ", "▸ ", "2", " ", glyph,
			" w1p2", " shell",
		}},
		{40, "chips", nil},
		{30, "workspace", nil},
		{26, "session", nil},
		{16, "name", nil},
	}
	for _, c := range cases {
		segs := barSegments(theme.DefaultTheme(), testutil.BarTestSnap(c.cols), false, "")
		if got := segment.SegCells(segs); got > c.cols {
			t.Fatalf("cols %d: spans paint %d cells: %q", c.cols, got, testutil.BarTexts(segs))
		}
		if got := barElideStage(segs); got != c.want {
			t.Fatalf("cols %d: elision reached %q, want %q: %q",
				c.cols, got, c.want, testutil.BarTexts(segs))
		}
		if c.text != nil {
			if got := strings.Join(testutil.BarTexts(segs), "|"); got != strings.Join(c.text, "|") {
				t.Fatalf("cols %d: spans = %q, want %q", c.cols, got, c.text)
			}
		}
	}

	// The active window chip survives every width that keeps a chip at all.
	for _, cols := range []int{60, 45, 40, 33, 30, 25, 22, 18, 16, 12} {
		segs := barSegments(theme.DefaultTheme(), testutil.BarTestSnap(cols), false, "")
		if segment.SegCells(segs) > cols {
			t.Fatalf("cols %d: spans paint %d cells", cols, segment.SegCells(segs))
		}
		text := strings.Join(testutil.BarTexts(segs), "")
		if cols >= 30 && !strings.Contains(text, "2") {
			t.Fatalf("cols %d: active chip dropped: %q", cols, text)
		}
		// Only the shrinking rungs end a name with an cells.Ellipsis; a dropped
		// chip or window name leaves no mark.
		switch barElideStage(segs) {
		case "workspace", "session", "status", "name":
			if !strings.Contains(text, "…") {
				t.Fatalf("cols %d: elided line has no cells.Ellipsis: %q", cols, text)
			}
		}
	}

	// A zero or negative width paints nothing.
	for _, cols := range []int{0, -5} {
		if segs := barSegments(theme.DefaultTheme(), testutil.BarTestSnap(cols), true, "READY"); len(segs) != 0 {
			t.Fatalf("cols %d: spans = %q, want none", cols, testutil.BarTexts(segs))
		}
	}
	if segs := barSegments(theme.DefaultTheme(), nil, false, ""); len(segs) != 0 {
		t.Fatalf("nil snapshot: spans = %q, want none", testutil.BarTexts(segs))
	}
}

// TestBarPinned keeps the pinned block whole while the left block gives way:
// both segments survive at 34 columns, and info survives alone at 20.
func TestBarPinned(t *testing.T) {
	snap := &wire.Snapshot{
		Session:      "long-session-name",
		Workspace:    "a-very-long-workspace-name",
		Windows:      testWindows("a-very-long-window-name-one", "two"),
		ActiveWindow: 0,
		Focus:        "w1p1",
		Panes:        []wire.PaneInfo{{ID: "w1p1", Name: "a-long-pane-name", Status: "working"}},
		All:          []wire.AllPane{{ID: "w1p1", Agent: true}},
		Cols:         34,
		Rows:         24,
	}
	info := "12 rows above, 4 hidden"

	segs := barSegments(theme.DefaultTheme(), snap, true, info)
	var pinned []segment.Seg
	for _, s := range segs {
		if s.Pin {
			pinned = append(pinned, s)
		}
	}
	if len(pinned) != 2 {
		t.Fatalf("pinned spans = %q, want PREFIX and info", testutil.BarTexts(segs))
	}
	if pinned[0].Text != "PREFIX " || pinned[1].Text != info {
		t.Fatalf("pinned spans = %q %q, want PREFIX and the whole info",
			pinned[0].Text, pinned[1].Text)
	}
	if segment.SegCells(segs) > snap.Cols {
		t.Fatalf("spans paint %d cells, want <= %d", segment.SegCells(segs), snap.Cols)
	}

	// 20 columns hold the info alone: PREFIX goes whole, never the left block's
	// room, and the left block gives way first.
	snap.Cols = 20
	segs = barSegments(theme.DefaultTheme(), snap, true, info)
	if len(segs) != 1 || !segs[0].Pin {
		t.Fatalf("20 cols: spans = %q, want the pinned info alone", testutil.BarTexts(segs))
	}
	if strings.Contains(segs[0].Text, "PREFIX") {
		t.Fatalf("20 cols: PREFIX survived: %q", segs[0].Text)
	}
	if !strings.HasSuffix(segs[0].Text, "…") {
		t.Fatalf("20 cols: clipped info has no cells.Ellipsis: %q", segs[0].Text)
	}
	if got := segment.SegCells(segs); got != 20 {
		t.Fatalf("20 cols: spans paint %d cells, want 20", got)
	}

	// An unarmed bar carries no PREFIX at all.
	snap.Cols = 80
	segs = barSegments(theme.DefaultTheme(), snap, false, info)
	for _, s := range segs {
		if s.Pin && s.Text == "PREFIX " {
			t.Fatalf("unarmed bar painted PREFIX: %q", testutil.BarTexts(segs))
		}
	}
}

// TestBarPinnedGutter leaves room for the ground cell between the blocks, even
// when the left block would otherwise fill the row exactly.
func TestBarPinnedGutter(t *testing.T) {
	cases := []struct {
		cols  int
		armed bool
		info  string
	}{
		{34, true, ""},                  // the exact fit the review found
		{34, true, "12 rows above"},     // the right block takes most of it
		{50, true, "12 rows above"},     // long names fill the rest
		{40, false, "SCROLL w1p2 +1/9"}, // info alone pins the right block
		{20, true, "12 rows above"},     // info alone: no PREFIX, no left room
	}
	for _, c := range cases {
		segs := barSegments(theme.DefaultTheme(), testutil.BarTestSnap(c.cols), c.armed, c.info)
		leftCells, rightCells, pinned := 0, 0, false
		for _, s := range segs {
			if s.Pin {
				pinned = true
				rightCells += cells.Cells(s.Text)
				continue
			}
			leftCells += cells.Cells(s.Text)
		}
		if !pinned {
			t.Fatalf("cols %d: no pinned block", c.cols)
		}
		if got := segment.SegCells(segs); got > c.cols {
			t.Fatalf("cols %d: spans paint %d cells", c.cols, got)
		}
		if leftCells == 0 {
			continue // nothing to separate from
		}
		if leftCells+rightCells > c.cols-1 {
			t.Fatalf("cols %d: left %d + pinned %d leaves no gutter cell",
				c.cols, leftCells, rightCells)
		}
	}
}

// TestBarPinnedAnchored pins the paint: the right block ends on the last
// column, and one ground cell separates it from the left block.
func TestBarPinnedAnchored(t *testing.T) {
	info := "SCROLL w1p2 +1/9"
	for _, cols := range []int{60, 50, 34} {
		snap := testutil.BarTestSnap(cols)
		segs := barSegments(theme.DefaultTheme(), snap, true, info)
		var pinnedText, leftText string
		for _, s := range segs {
			if s.Pin {
				pinnedText += s.Text
				continue
			}
			leftText += s.Text
		}
		if pinnedText != "PREFIX "+info {
			t.Fatalf("cols %d: pinned spans = %q", cols, pinnedText)
		}
		start := cols - cells.Cells(pinnedText)

		var b bytes.Buffer
		drawBar(&b, theme.DefaultTheme(), snap, true, info)
		vt := vt10x.New(vt10x.WithSize(cols, snap.Rows))
		if _, err := vt.Write(b.Bytes()); err != nil {
			t.Fatalf("cols %d: draw bar: %v", cols, err)
		}
		vt.Lock()
		// One cell is one rune here: the pin test data carries no wide rune in
		// the pinned block, so the columns line up with the cells.
		want := []rune(pinnedText)
		for i, r := range want {
			if x := start + i; x >= cols {
				vt.Unlock()
				t.Fatalf("cols %d: pinned block starts at %d and runs past the row", cols, start)
			} else if cell := vt.Cell(x, snap.Rows-1); cell.Char != r {
				vt.Unlock()
				t.Fatalf("cols %d: cell %d = %q, want %q from the pinned block",
					cols, x, cell.Char, r)
			}
		}
		ground := vt10x.Color(theme.DefaultTheme().Palette.RailBG.R<<16 | theme.DefaultTheme().Palette.RailBG.G<<8 | theme.DefaultTheme().Palette.RailBG.B)
		if start > 0 {
			cell := vt.Cell(start-1, snap.Rows-1)
			if cell.Char != ' ' || cell.BG != ground {
				vt.Unlock()
				t.Fatalf("cols %d: cell before the pinned block = %q bg %v, want a ground gutter",
					cols, cell.Char, cell.BG)
			}
		}
		if leftText == "" {
			vt.Unlock()
			t.Fatalf("cols %d: pinned only, want a left block", cols)
		}
		vt.Unlock()
	}
}

// TestBarWideRunes keeps wide runes whole and inside the column count.
func TestBarWideRunes(t *testing.T) {
	glyph, _ := rail.RailGlyph(theme.DefaultTheme(), "working", rail.NerdFonts())
	sources := []string{
		"日本語セッション", "作業ディレクトリ", "▸ ", "1 一二三",
		"2 四五六", " w1p1", " 日本語", " working", glyph,
	}
	snap := &wire.Snapshot{
		Session:      "日本語セッション",
		Workspace:    "作業ディレクトリ",
		Windows:      testWindows("一二三", "四五六"),
		ActiveWindow: 0,
		Focus:        "w1p1",
		Panes:        []wire.PaneInfo{{ID: "w1p1", Name: "日本語", Status: "working"}},
		All:          []wire.AllPane{{ID: "w1p1", Agent: true}},
		Rows:         24,
	}
	for _, cols := range []int{80, 41, 40, 21, 8, 3, 1} {
		snap.Cols = cols
		segs := barSegments(theme.DefaultTheme(), snap, false, "")
		if got := segment.SegCells(segs); got > cols {
			t.Fatalf("cols %d: spans paint %d cells", cols, got)
		}
		for _, s := range segs {
			text := strings.TrimSuffix(s.Text, "…")
			if strings.ContainsRune(text, utf8.RuneError) {
				t.Fatalf("cols %d: split rune in %q", cols, s.Text)
			}
			if text == "" || text == " " {
				continue
			}
			// Every painted run is a whole-rune prefix of the text it came from.
			whole := false
			for _, src := range sources {
				if strings.HasPrefix(src, text) {
					whole = true
					break
				}
			}
			if !whole {
				t.Fatalf("cols %d: %q is not a whole rune prefix of a source",
					cols, s.Text)
			}
		}
	}
}

// TestBarFocusChip reads the focus chip from the focused pane: an agent shows
// its state glyph and colour on the focus fill, a shell shows the prompt marker.
func TestBarFocusChip(t *testing.T) {
	nerd := rail.NerdFonts()
	snap := &wire.Snapshot{
		Session: "s",
		Focus:   "w1p1",
		Panes:   []wire.PaneInfo{{ID: "w1p1", Name: "agent", Status: "blocked"}},
		All:     []wire.AllPane{{ID: "w1p1", Agent: true, Status: "blocked"}},
		Cols:    80,
		Rows:    24,
	}
	glyph, colour := rail.RailGlyph(theme.DefaultTheme(), "blocked", nerd)
	segs := barSegments(theme.DefaultTheme(), snap, false, "")
	if len(segs) < 4 {
		t.Fatalf("spans = %q, want the focus chip", testutil.BarTexts(segs))
	}
	chip := segs[2]
	if chip.Text != glyph {
		t.Fatalf("blocked glyph = %q, want %q", chip.Text, glyph)
	}
	if chip.FG != colour {
		t.Fatalf("blocked glyph colour = %v, want %v", chip.FG, colour)
	}
	if chip.BG != theme.DefaultTheme().Palette.RailFocusBG {
		t.Fatalf("blocked glyph bg = %v, want theme.DefaultTheme().Palette.RailFocusBG", chip.BG)
	}
	if colour == theme.DefaultTheme().Palette.RailDim {
		t.Fatal("blocked shares the shell colour")
	}
	if got := segs[len(segs)-1].Text; got != " blocked" {
		t.Fatalf("status word = %q, want blocked", got)
	}
	// The painted bar carries the same glyph in that colour on the focus fill.
	var b bytes.Buffer
	drawBar(&b, theme.DefaultTheme(), snap, false, "")
	out := b.String()
	if !strings.Contains(out, colour.FG()+glyph) {
		t.Fatalf("bar does not paint %q in %v: %q", glyph, colour, out)
	}
	if !strings.Contains(out, theme.DefaultTheme().Palette.RailFocusBG.BG()+colour.FG()+glyph) {
		t.Fatalf("bar does not paint the glyph on theme.DefaultTheme().Palette.RailFocusBG: %q", out)
	}

	// A shell reports no state: it gets the prompt marker in dim text, and its
	// empty status reads as unknown.
	snap.Panes[0] = wire.PaneInfo{ID: "w1p1", Name: "bash"}
	snap.All[0] = wire.AllPane{ID: "w1p1", Agent: false}
	segs = barSegments(theme.DefaultTheme(), snap, false, "")
	if len(segs) < 4 {
		t.Fatalf("spans = %q, want the focus chip", testutil.BarTexts(segs))
	}
	if segs[2].Text != "▶" || segs[2].FG != theme.DefaultTheme().Palette.RailDim {
		t.Fatalf("shell glyph = %+v, want dim ▶", segs[2])
	}
	if got := segs[len(segs)-1].Text; got != " unknown" {
		t.Fatalf("shell status word = %q, want unknown", got)
	}
}

// TestDividerHover shows the grab affordance under the pointer and clears it
// when the pointer leaves. Hover and press use the same padHit area.
// TestPadHitCrossesChildBorders pins the nested case: a full-width horizontal
// divider is grabbable even when the pointer sits over the vertical divider of
// the split below it.
func TestPadHitCrossesChildBorders(t *testing.T) {
	inner := &layout.Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*layout.Node{{Pane: "b"}, {Pane: "c"}},
	}
	root := &layout.Node{
		Dir: "col", Sizes: []float64{0.5, 0.5},
		Kids: []*layout.Node{{Pane: "a"}, inner},
	}
	area := layout.Rect{X: 0, Y: 1, W: 80, H: 23}
	boxes := layout.ChildBoxRects(root, area)
	pad := layout.PadRect(root, boxes, 0)
	ib := layout.ChildBoxRects(inner, boxes[1])
	ipad := layout.PadRect(inner, ib, 0)

	for _, x := range []int{ipad.X, ipad.X + 1} {
		split, first, second, axis, _, zone, ok := padHit(root, area, x, pad.Y+1)
		if !ok || split != root || first != "a" || (second != "b" && second != "c") ||
			axis != "col" || zone != pad {
			t.Fatalf("pad hit at x=%d = %v %q %q %q %v %v, want a + b/c on the root",
				x, split, first, second, axis, zone, ok)
		}
	}

	// The same cell starts a vertical drag over the root split's full width.
	snap := &wire.Snapshot{Cols: 80, Rows: 25, Root: root}
	ds, ok := dragPress(snap, ipad.X, pad.Y+1)
	if !ok || ds.first != "a" || (ds.second != "b" && ds.second != "c") ||
		ds.axis != "col" || ds.rect != area || ds.zone != pad {
		t.Fatalf("drag press over a nested border = %+v ok=%v", ds, ok)
	}
}

// TestDividerGripsAlwaysVisible keeps a handle on every divider even before the
// pointer arrives, so the affordance does not depend on hover working.
func TestDividerGripsAlwaysVisible(t *testing.T) {
	inner := &layout.Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*layout.Node{{Pane: "b"}, {Pane: "c"}},
	}
	root := &layout.Node{
		Dir: "col", Sizes: []float64{0.5, 0.5},
		Kids: []*layout.Node{{Pane: "a"}, inner},
	}
	snap := &wire.Snapshot{Cols: 100, Rows: 30, Rail: 26, Root: root}
	var buf bytes.Buffer
	drawGrips(&buf, theme.DefaultTheme(), snap)
	vt := vt10x.New(vt10x.WithSize(100, 30))
	if _, err := vt.Write(buf.Bytes()); err != nil {
		t.Fatalf("draw grips: %v", err)
	}
	vt.Lock()
	defer vt.Unlock()
	// Horizontal divider midpoint: both cells.
	for _, y := range []int{14, 15} {
		if g := vt.Cell(63, y); g.Char != '•' {
			t.Fatalf("horizontal grip at 63,%d = %q", y, g.Char)
		}
	}
	// Vertical divider midpoint: both cells.
	for _, x := range []int{62, 63} {
		if g := vt.Cell(x, 22); g.Char != '•' {
			t.Fatalf("vertical grip at %d,22 = %q", x, g.Char)
		}
	}
	if n := strings.Count(buf.String(), "•"); n != 4 {
		t.Fatalf("grips = %d, want 4", n)
	}
}

func TestDividerHover(t *testing.T) {
	row := &layout.Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*layout.Node{{Pane: "a"}, {Pane: "b"}},
	}
	snap := testutil.TestSnapshot(row, 100, 30, nil)
	snap.Focus = "a"
	tui := &tui{snap: snap, vts: map[string]vt10x.Terminal{}}
	boxes := layout.ChildBoxRects(row, layout.PaneAreaForWithTabBar(100, 30, 0))
	zone := layout.PadRect(row, boxes, 0)
	// SGR button 35 is pointer motion with no button.
	tui.handleMouse(mouseEvent{btn: 35, x: zone.X + 1, y: zone.Y + 2})
	if tui.padZone != zone || tui.padAxis != "row" {
		t.Fatalf("hover zone = %v/%q, want %v/row", tui.padZone, tui.padAxis, zone)
	}
	if got := tui.barInfo(); got != "drag to resize" {
		t.Fatalf("hover hint = %q", got)
	}
	var b bytes.Buffer
	drawDivider(&b, theme.DefaultTheme(), tui.padZone, tui.padAxis)
	out := b.String()
	if !strings.Contains(out, "┃") || !strings.Contains(out, "\x1b[1;96m") {
		t.Fatalf("divider not highlighted: %q", out)
	}
	// The press lands on the same zone.
	if ds, ok := dragPress(snap, zone.X+1, zone.Y+2); !ok || ds.zone != zone {
		t.Fatalf("press zone = %v ok=%v, want %v", ds.zone, ok, zone)
	}
	// Leaving the divider clears it.
	tui.handleMouse(mouseEvent{btn: 35, x: 10, y: 10})
	if tui.padZone != (layout.Rect{}) {
		t.Fatalf("hover survived leaving the divider: %v", tui.padZone)
	}
	if got := tui.barInfo(); got != "" {
		t.Fatalf("stale hover hint = %q", got)
	}
}

// TestHoverMotionNotForwarded keeps no-button motion from reaching a pane that
// asked only for button motion.
func TestHoverMotionNotForwarded(t *testing.T) {
	tui, got := clientTUI(t, map[string]bool{"a": true})
	p := tui.paneByID("a")
	tui.handleMouse(mouseEvent{
		btn: 35, x: p.Rect.X + 1, y: p.Rect.Y + 1,
		raw: []byte("\x1b[<35;2;2M"),
	})
	testutil.NoMsg(t, got)
}

func TestRailDragUsesOffset(t *testing.T) {
	root := &layout.Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*layout.Node{{Pane: "a"}, {Pane: "b"}},
	}
	snap := &wire.Snapshot{Cols: 100, Rows: 30, Rail: 20, Root: root}
	area := layout.PaneAreaForWithTabBar(100, 30, 20)
	boxes := layout.ChildBoxRects(root, area)
	pad := layout.PadRect(root, boxes, 0)
	ds, ok := dragPress(snap, pad.X, pad.Y+1)
	if !ok || ds.first != "a" || ds.second != "b" || ds.rect != area {
		t.Fatalf("rail-aware divider press = %+v ok=%v, want rect %+v", ds, ok, area)
	}
	// The rail-zero pad position is inside pane a once the rail is counted.
	if _, ok := dragPress(snap, 49, pad.Y+1); ok {
		t.Fatal("drag started at the rail-zero pad, offset was dropped")
	}
}

func TestDrawPaneBox(t *testing.T) {
	var b bytes.Buffer
	panel.DrawPaneBox(&b, theme.DefaultTheme(), layout.Rect{X: 10, Y: 5, W: 20, H: 8}, true)
	out := b.String()
	for _, want := range []string{"┌", "┐", "└", "┘", "│", "─", "\x1b[1;96m"} {
		if !strings.Contains(out, want) {
			t.Fatalf("box missing %q: %q", want, out)
		}
	}
	if !strings.Contains(out, "\x1b[5;10H┌") || !strings.Contains(out, "\x1b[5;31H┐") {
		t.Fatalf("box corners are at the wrong cells: %q", out)
	}
	var dim bytes.Buffer
	panel.DrawPaneBox(&dim, theme.DefaultTheme(), layout.Rect{X: 10, Y: 5, W: 20, H: 8}, false)
	if strings.Contains(dim.String(), "\x1b[1;96m") || !strings.Contains(dim.String(), "\x1b[90m") {
		t.Fatalf("inactive box is not dim: %q", dim.String())
	}
}

func TestMenuClampsAndHits(t *testing.T) {
	row := &layout.Node{
		Dir: "row", Kids: []*layout.Node{{Pane: "a"}, {Pane: "b"}},
		Sizes: []float64{0.5, 0.5},
	}
	tui := &tui{snap: testutil.TestSnapshot(row, 100, 30, nil)}
	p := tui.paneByID("b")
	tui.openMenu(p, 98, 28)
	m := tui.menu
	if m == nil || tui.mode != "menu" {
		t.Fatalf("menu not opened: %+v mode=%q", m, tui.mode)
	}
	if m.x+m.w > 100 || m.y+len(m.items)+2 > 30 {
		t.Fatalf("menu escapes the screen: %+v", m)
	}
	if got := m.itemAt(m.x+2, m.y+1); got != 0 {
		t.Fatalf("first item row = %d", got)
	}
	if got := m.itemAt(m.x+2, m.y+len(m.items)+1); got != -1 {
		t.Fatalf("bottom border hit = %d", got)
	}
	if got := m.itemAt(0, 0); got != -1 {
		t.Fatalf("point outside the menu hit = %d", got)
	}
}

func TestRightPressOpensMenuOverMousePane(t *testing.T) {
	row := &layout.Node{
		Dir: "row", Kids: []*layout.Node{{Pane: "a"}, {Pane: "b"}},
		Sizes: []float64{0.5, 0.5},
	}
	snap := testutil.TestSnapshot(row, 100, 30, map[string]bool{"b": true})
	tui := &tui{snap: snap, vts: map[string]vt10x.Terminal{}}
	// A right press on pane b, whose app owns the mouse, still opens the menu.
	tui.handleMouse(mouseEvent{btn: 2, x: 80, y: 5})
	if tui.mode != "menu" || tui.menu == nil || tui.menu.pane != "b" {
		t.Fatalf("right press: mode=%q menu=%+v", tui.mode, tui.menu)
	}
	// The release of the opening right press sits on the border and must not
	// dismiss the menu.
	tui.handleMouse(mouseEvent{btn: 2, release: true, x: tui.menu.x + 1, y: tui.menu.y + 1})
	if tui.mode != "menu" {
		t.Fatal("right release on the border closed the menu")
	}
	// A click on the title or border is a near miss, not a dismissal.
	tui.handleMouse(mouseEvent{btn: 0, x: tui.menu.x + 2, y: tui.menu.y + 1})
	if tui.mode != "menu" {
		t.Fatal("click on the title closed the menu")
	}
	// A left press on a menu row runs that item and closes the menu.
	done := false
	tui.menu.items[0].run = func() { done = true }
	tui.handleMouse(mouseEvent{btn: 0, x: tui.menu.x + 2, y: tui.menu.y + 1 + 1})
	if !done || tui.mode != "" || tui.menu != nil {
		t.Fatalf("left pick: done=%v mode=%q", done, tui.mode)
	}
}

func TestPaneAtBoxIncludesBorder(t *testing.T) {
	row := &layout.Node{
		Dir: "row", Kids: []*layout.Node{{Pane: "a"}, {Pane: "b"}},
		Sizes: []float64{0.5, 0.5},
	}
	tui := &tui{snap: testutil.TestSnapshot(row, 100, 30, nil)}
	b := tui.paneByID("b").Rect
	if got := tui.paneAtBox(b.X, b.Y); got == nil || got.ID != "b" {
		t.Fatalf("top-left border did not hit b: %+v", got)
	}
	if got := tui.paneAtBox(b.X-1, b.Y-1); got == nil || got.ID != "b" {
		t.Fatalf("outer corner did not hit b: %+v", got)
	}
	if got := tui.paneAtBox(b.X+b.W, b.Y+b.H); got == nil || got.ID != "b" {
		t.Fatalf("bottom-right border did not hit b: %+v", got)
	}
	// Adjacent boxes share the divider: a's right border and b's left border
	// are adjacent cells, and each belongs to its own pane.
	if got := tui.paneAtBox(b.X-1, b.Y+1); got == nil || got.ID != "b" {
		t.Fatalf("left border did not hit b: %+v", got)
	}
	if got := tui.paneAtBox(b.X-2, b.Y+1); got == nil || got.ID != "a" {
		t.Fatalf("a's right border did not hit a: %+v", got)
	}
}

func TestMenuOutsideClickFocusesPane(t *testing.T) {
	row := &layout.Node{
		Dir: "row", Kids: []*layout.Node{{Pane: "a"}, {Pane: "b"}},
		Sizes: []float64{0.5, 0.5},
	}
	tui := &tui{snap: testutil.TestSnapshot(row, 100, 30, nil), vts: map[string]vt10x.Terminal{}}
	tui.openMenu(tui.paneByID("b"), 10, 5)
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	tui.conn = &transport.Conn{Conn: a}
	got := make(chan wire.Msg, 1)
	go func() {
		if m, err := wire.ReadMsg(b); err == nil {
			got <- m
		}
	}()
	// One left click on pane a, outside the menu, dismisses the menu and
	// focuses a in the same event.
	tui.handleMouse(mouseEvent{btn: 0, x: 5, y: 5})
	select {
	case m := <-got:
		if m.Action != "focus" || m.Target != "a" {
			t.Fatalf("outside click sent %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("outside click sent no focus")
	}
	if tui.menu != nil || tui.mode != "" {
		t.Fatalf("menu stayed open: %+v %q", tui.menu, tui.mode)
	}
}

func TestMenuHoverHighlights(t *testing.T) {
	row := &layout.Node{Pane: "a"}
	tui := &tui{snap: testutil.TestSnapshot(row, 100, 30, nil)}
	tui.openMenu(tui.paneByID("a"), 10, 5)
	// Bare motion (no button held) is SGR button 35.
	tui.handleMouse(mouseEvent{btn: 35, x: 14, y: tui.menu.y + 2 + 1})
	if tui.menu == nil || tui.menu.sel != 1 {
		t.Fatalf("hover did not highlight row 1: %+v", tui.menu)
	}
	if tui.mode != "menu" {
		t.Fatalf("hover closed the menu: %q", tui.mode)
	}
	// A bare click does not activate a row it merely hovers.
	ran := false
	tui.menu.items[1].run = func() { ran = true }
	tui.handleMouse(mouseEvent{btn: 35, x: 14, y: tui.menu.y + 2 + 1})
	if ran {
		t.Fatal("bare motion ran an item")
	}
}

// A wheel event is not a left press: it must not start a selection, a divider
// drag or a menu item.
func TestWheelIsNotAPress(t *testing.T) {
	row := &layout.Node{
		Dir: "row", Kids: []*layout.Node{{Pane: "a"}, {Pane: "b"}},
		Sizes: []float64{0.5, 0.5},
	}
	tui := &tui{snap: testutil.TestSnapshot(row, 100, 30, nil), vts: map[string]vt10x.Terminal{}}
	tui.handleMouse(mouseEvent{btn: 64, x: 5, y: 5})
	if tui.press != nil || tui.dragging {
		t.Fatalf("wheel started a press or drag: press=%+v dragging=%v", tui.press, tui.dragging)
	}
	// On a pad, a wheel must not start a resize drag either.
	padX := tui.paneByID("b").Rect.X - 2
	tui.handleMouse(mouseEvent{btn: 64, x: padX + 1, y: 5})
	if tui.dragging {
		t.Fatal("wheel on a pad started a drag")
	}
	// With a menu open, a wheel must not run an item or close it.
	tui.openMenu(tui.paneByID("b"), 10, 5)
	ran := false
	tui.menu.items[0].run = func() { ran = true }
	tui.handleMouse(mouseEvent{btn: 64, x: tui.menu.x + 2, y: tui.menu.y + 2})
	if ran || tui.menu == nil {
		t.Fatalf("wheel over the menu: ran=%v menu=%+v", ran, tui.menu)
	}
}

func TestMenuRightDragPicks(t *testing.T) {
	row := &layout.Node{Pane: "a"}
	tui := &tui{snap: testutil.TestSnapshot(row, 100, 30, nil)}
	tui.openMenu(tui.paneByID("a"), 10, 5)
	done := -1
	tui.menu.items[1].run = func() { done = 1 }
	// Move with the right button held: the row under the pointer lights up.
	tui.handleMouse(mouseEvent{btn: 34, x: 14, y: tui.menu.y + 2 + 1})
	if tui.menu.sel != 1 {
		t.Fatalf("right drag did not highlight row 1: %+v", tui.menu)
	}
	// Release over the row runs it.
	tui.handleMouse(mouseEvent{btn: 2, release: true, x: 14, y: tui.menu.y + 2 + 1})
	if done != 1 || tui.menu != nil {
		t.Fatalf("right drag did not pick: done=%d menu=%+v", done, tui.menu)
	}

	// A click outside dismisses.
	tui.openMenu(tui.paneByID("a"), 10, 5)
	tui.handleMouse(mouseEvent{btn: 0, x: 1, y: 1})
	if tui.menu != nil || tui.mode != "" {
		t.Fatalf("outside click did not close: %+v", tui.menu)
	}
}

func TestMenuKeyboard(t *testing.T) {
	row := &layout.Node{Pane: "a"}
	tui := &tui{snap: testutil.TestSnapshot(row, 100, 30, nil)}
	tui.openMenu(tui.paneByID("a"), 10, 10)
	if len(tui.menu.items) < 3 {
		t.Fatalf("menu items = %d", len(tui.menu.items))
	}
	tui.inbuf = []byte("j")
	if !tui.menuKey() || tui.menu.sel != 1 {
		t.Fatalf("j did not move: %+v", tui.menu)
	}
	tui.inbuf = []byte("k")
	tui.menuKey() // back to 0
	tui.inbuf = []byte("k")
	tui.menuKey() // wraps to the last item
	if tui.menu == nil || tui.menu.sel != len(tui.menu.items)-1 {
		t.Fatalf("wrap up failed: %+v", tui.menu)
	}
	tui.inbuf = []byte("q")
	tui.menuKey()
	if tui.menu != nil || tui.mode != "" {
		t.Fatalf("q did not close: %+v %q", tui.menu, tui.mode)
	}
}

func pasteTUI(t *testing.T) (*tui, chan wire.Msg) {
	t.Helper()
	return clientTUI(t, nil)
}

// whole buffer, or armed on its contents, fails here.
func TestPasteIsOneKeysMessage(t *testing.T) {
	tui, got := pasteTUI(t)
	paste := "\x1b[200~line one\nline two\n\x1b[201~"
	tui.inbuf = append(tui.inbuf, []byte(paste+"tail")...)
	tui.processInput()
	m := testutil.NextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != paste {
		t.Fatalf("paste came through as %+v (%q)", m, m.Keys)
	}
	if tui.armed {
		t.Fatal("the paste armed the prefix")
	}
	m = testutil.NextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != "tail" {
		t.Fatalf("trailing bytes came through as %+v (%q)", m, m.Keys)
	}
	testutil.NoMsg(t, got)
	if len(tui.inbuf) != 0 || tui.armed {
		t.Fatalf("paste left state: inbuf=%q armed=%v", tui.inbuf, tui.armed)
	}
}

// A ctrl+b inside a paste is text, not a prefix arm.
func TestPasteCtrlBDoesNotArm(t *testing.T) {
	tui, got := pasteTUI(t)
	paste := "\x1b[200~a\x02b\x1b[201~"
	tui.inbuf = append(tui.inbuf, []byte(paste)...)
	tui.processInput()
	m := testutil.NextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != paste {
		t.Fatalf("paste came through as %+v (%q)", m, m.Keys)
	}
	if tui.armed {
		t.Fatal("ctrl+b in a paste armed the prefix")
	}
	testutil.NoMsg(t, got)
}

// A paste split across two reads is buffered and forwarded once, intact.
func TestPasteSpansReads(t *testing.T) {
	tui, got := pasteTUI(t)
	head := "\x1b[200~first\n"
	tail := "second\n\x1b[201~"
	tui.inbuf = append(tui.inbuf, []byte(head)...)
	tui.processInput()
	testutil.NoMsg(t, got)
	if len(tui.inbuf) == 0 {
		t.Fatal("partial paste was not buffered")
	}
	tui.inbuf = append(tui.inbuf, []byte(tail)...)
	tui.processInput()
	m := testutil.NextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != head+tail {
		t.Fatalf("split paste came through as %+v (%q)", m, m.Keys)
	}
	testutil.NoMsg(t, got)
}

// A paste closes an open menu and reaches the pane instead of a menu item.
func TestPasteClosesMenu(t *testing.T) {
	tui, got := pasteTUI(t)
	tui.openMenu(tui.paneByID("a"), 10, 5)
	if tui.menu == nil {
		t.Fatal("menu did not open")
	}
	paste := "\x1b[200~x\x1b[201~"
	tui.inbuf = append(tui.inbuf, []byte(paste)...)
	tui.processInput()
	m := testutil.NextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != paste {
		t.Fatalf("paste came through as %+v (%q)", m, m.Keys)
	}
	if tui.menu != nil || tui.mode != "" {
		t.Fatalf("paste left the menu open: %+v %q", tui.menu, tui.mode)
	}
}

// A read that splits right after the escape still yields one paste: the lone
// escape is held as a possible marker instead of going to the pane.
func TestPasteSplitAtLoneEscape(t *testing.T) {
	tui, got := pasteTUI(t)
	tui.inbuf = append(tui.inbuf, 0x1b)
	tui.processInput()
	testutil.NoMsg(t, got)
	if tui.pasteDeadline.IsZero() {
		t.Fatal("lone escape was not held")
	}
	tui.inbuf = append(tui.inbuf, []byte("[200~a\nb\x1b[201~")...)
	tui.processInput()
	m := testutil.NextMsg(t, got)
	want := "\x1b[200~a\nb\x1b[201~"
	if m.Type != "keys" || string(m.Keys) != want {
		t.Fatalf("split paste came through as %+v (%q)", m, m.Keys)
	}
	testutil.NoMsg(t, got)
}

// A real lone escape waits for the deadline and then reaches the pane.
func TestLoneEscapeFlushedAfterDeadline(t *testing.T) {
	tui, got := pasteTUI(t)
	start := time.Now()
	tui.inbuf = append(tui.inbuf, 0x1b)
	tui.processInput()
	testutil.NoMsg(t, got)
	if tui.expirePaste(start) {
		t.Fatal("the escape expired before its deadline")
	}
	testutil.NoMsg(t, got)
	if !tui.expirePaste(start.Add(escTimeout + time.Millisecond)) {
		t.Fatal("the escape did not expire after its deadline")
	}
	m := testutil.NextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != "\x1b" {
		t.Fatalf("lone escape came through as %+v (%q)", m, m.Keys)
	}
	testutil.NoMsg(t, got)
}

// Bytes that cannot start a paste disprove the partial marker, so its deadline
// cannot flush a later, genuine paste early.
func TestPartialMarkerDisprovedClearsDeadline(t *testing.T) {
	tui, got := pasteTUI(t)
	tui.inbuf = append(tui.inbuf, []byte("\x1b[2")...)
	tui.processInput()
	testutil.NoMsg(t, got)
	if tui.pasteDeadline.IsZero() {
		t.Fatal("partial marker was not held")
	}
	tui.inbuf = append(tui.inbuf, 'x')
	tui.processInput()
	m := testutil.NextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != "\x1b[2x" {
		t.Fatalf("disproved marker came through as %+v (%q)", m, m.Keys)
	}
	if !tui.pasteDeadline.IsZero() {
		t.Fatal("disproved marker kept its deadline")
	}
	if tui.expirePaste(time.Now().Add(time.Second)) {
		t.Fatal("a disproved marker was flushed again")
	}
}

// A start marker that never completes is not a paste. The stall path sends
// the bytes down the normal input path instead of holding them forever.
func TestPasteStallFallsBack(t *testing.T) {
	tui, got := pasteTUI(t)
	tui.inbuf = append(tui.inbuf, []byte("\x1b[")...)
	tui.processInput()
	testutil.NoMsg(t, got)
	tui.staleInput()
	m := testutil.NextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != "\x1b[" {
		t.Fatalf("stalled marker came through as %+v (%q)", m, m.Keys)
	}
}

// A backwards drag covers the same row-major range as the forward one.
func TestSelectionRangeNormalizes(t *testing.T) {
	s := &selection{pane: "a", anchor: cell{4, 2}, focus: cell{1, 0}}
	r := s.rangeIn(10)
	if r.lo != 1 || r.hi != 24 {
		t.Fatalf("backwards range = %+v, want lo 1 hi 24", r)
	}
	s = &selection{pane: "a", anchor: cell{1, 0}, focus: cell{4, 2}}
	if got := s.rangeIn(10); got.lo != 1 || got.hi != 24 {
		t.Fatalf("forwards range = %+v", got)
	}
	if got := (*selection)(nil).rangeIn(10); got != nil {
		t.Fatal("nil selection has a range")
	}
}

func TestExtractSelection(t *testing.T) {
	vt := testutil.TestVT(t, 12, 3, "ab  cd\r\nsecond line\r\n")
	cases := []struct {
		name string
		a, b cell
		want string
	}{
		{"trailing spaces", cell{0, 0}, cell{6, 0}, "ab  cd"},
		{"multiple rows", cell{0, 0}, cell{5, 1}, "ab  cd\nsecond"},
		{"single cell", cell{1, 0}, cell{1, 0}, "b"},
		{"backwards drag", cell{5, 1}, cell{0, 0}, "ab  cd\nsecond"},
	}
	for _, c := range cases {
		if got := extractSelection(vt, 12, c.a, c.b); got != c.want {
			t.Fatalf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestOSC52Clipboard(t *testing.T) {
	t.Setenv("TMUX", "")
	seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("hi\nthere")) + "\x1b\\"
	if got := osc52("hi\nthere"); got != seq {
		t.Fatalf("osc52 = %q, want %q", got, seq)
	}
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	inner := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("hi")) + "\x1b\\"
	want := "\x1bPtmux;" + strings.ReplaceAll(inner, "\x1b", "\x1b\x1b") + "\x1b\\"
	if got := osc52("hi"); got != want {
		t.Fatalf("tmux osc52 = %q, want %q", got, want)
	}
}

// A click that never moves is not a selection: the app that claims the mouse
// gets the press and the release.
func TestClickForwardsToMouseApp(t *testing.T) {
	tui, got := clientTUI(t, map[string]bool{"a": true})
	p := tui.paneByID("a")
	tui.vts["a"] = testutil.TestVT(t, p.Rect.W, p.Rect.H, "hello")
	press := mouseEvent{
		btn: 0, x: p.Rect.X + 2, y: p.Rect.Y + 1,
		raw: []byte("\x1b[<0;2;2M"),
	}
	release := mouseEvent{
		btn: 0, release: true, x: p.Rect.X + 2, y: p.Rect.Y + 1,
		raw: []byte("\x1b[<0;2;2m"),
	}
	tui.handleMouse(press)
	testutil.NoMsg(t, got)
	if tui.press == nil {
		t.Fatal("press was not held")
	}
	tui.handleMouse(release)
	m := testutil.NextMsg(t, got)
	want := string(press.raw) + string(release.raw)
	if m.Type != "keys" || string(m.Keys) != want {
		t.Fatalf("click sent %+v (%q), want %q", m, m.Keys, want)
	}
	if tui.selText != nil || tui.press != nil {
		t.Fatal("click left selection state behind")
	}
	testutil.NoMsg(t, got)
}

// A left press held on a pane must not survive a right press and a menu pick.
// Otherwise the later left release forwards a click the app never asked for.
func TestRightPressCancelsHeldClick(t *testing.T) {
	tui, got := clientTUI(t, map[string]bool{"a": true})
	p := tui.paneByID("a")
	x, y := p.Rect.X+2, p.Rect.Y+2
	tui.handleMouse(mouseEvent{btn: 0, x: x, y: y, raw: []byte("\x1b[<0;3;3M")})
	if tui.press == nil {
		t.Fatal("press was not held")
	}
	tui.handleMouse(mouseEvent{btn: 2, x: x, y: y, raw: []byte("\x1b[<2;3;3M")})
	if tui.menu == nil {
		t.Fatal("right press did not open the menu")
	}
	if tui.press != nil {
		t.Fatal("the held press survived the right press")
	}
	// Pick a menu row with the left button, then release the left button.
	tui.handleMouse(mouseEvent{
		btn: 0, x: tui.menu.x + 2, y: tui.menu.y + 2,
		raw: []byte("\x1b[<0;4;4M"),
	})
	tui.handleMouse(mouseEvent{
		btn: 0, release: true, x: x, y: y,
		raw: []byte("\x1b[<0;3;3m"),
	})
	deadline := time.After(100 * time.Millisecond)
	for {
		select {
		case m := <-got:
			if m.Type == "keys" {
				t.Fatalf("a phantom click reached the app: %q", m.Keys)
			}
		case <-deadline:
			return
		}
	}
}

// A snapshot that changes the starting pane's rect drops the selection: its
// cells belong to the old geometry.
func TestSelectionClearsOnResize(t *testing.T) {
	tui, _ := clientTUI(t, nil)
	p := tui.paneByID("a")
	tui.handleMouse(mouseEvent{
		btn: 0, x: p.Rect.X + 1, y: p.Rect.Y + 1,
		raw: []byte("\x1b[<0;1;1M"),
	})
	tui.handleMouse(mouseEvent{
		btn: 32, x: p.Rect.X + 4, y: p.Rect.Y + 1,
		raw: []byte("\x1b[<32;4;1M"),
	})
	if tui.selText == nil || !tui.selText.moved {
		t.Fatal("drag did not select")
	}
	// The same geometry keeps the selection.
	row := &layout.Node{Pane: "a"}
	same := testutil.TestSnapshot(row, 100, 30, nil)
	same.Focus = "a"
	tui.apply(wire.Msg{Type: "snapshot", Snap: same})
	if tui.selText == nil {
		t.Fatal("an unchanged snapshot dropped the selection")
	}
	// A resized pane drops it.
	resized := testutil.TestSnapshot(row, 90, 30, nil)
	resized.Focus = "a"
	tui.apply(wire.Msg{Type: "snapshot", Snap: resized})
	if tui.selText != nil || tui.press != nil {
		t.Fatal("selection survived the resize")
	}
}

// A one-cell jitter is not a drag: the app that owns the mouse still gets the
// click.
func TestClickJitterStaysAClick(t *testing.T) {
	tui, got := clientTUI(t, map[string]bool{"a": true})
	p := tui.paneByID("a")
	tui.vts["a"] = testutil.TestVT(t, p.Rect.W, p.Rect.H, "hello")
	press := mouseEvent{
		btn: 0, x: p.Rect.X + 2, y: p.Rect.Y + 2,
		raw: []byte("\x1b[<0;3;3M"),
	}
	release := mouseEvent{
		btn: 0, release: true, x: p.Rect.X + 3, y: p.Rect.Y + 2,
		raw: []byte("\x1b[<0;4;3m"),
	}
	motion := mouseEvent{
		btn: 32, x: p.Rect.X + 3, y: p.Rect.Y + 2,
		raw: []byte("\x1b[<32;4;3M"),
	}
	tui.handleMouse(press)
	tui.handleMouse(motion)
	tui.handleMouse(release)
	m := testutil.NextMsg(t, got)
	if m.Type != "keys" {
		t.Fatalf("jitter sent %+v", m)
	}
	// The held press, the jitter and the release all reach the app.
	if want := string(press.raw) + string(motion.raw) + string(release.raw); string(m.Keys) != want {
		t.Fatalf("jitter sent %q, want %q", m.Keys, want)
	}
	if tui.selText != nil || tui.press != nil {
		t.Fatal("jitter left selection state behind")
	}
	testutil.NoMsg(t, got)
}

// Two cells is a drag: the range is selected and nothing reaches the app.
func TestTwoCellDragSelects(t *testing.T) {
	tui, got := clientTUI(t, map[string]bool{"a": true})
	p := tui.paneByID("a")
	tui.vts["a"] = testutil.TestVT(t, p.Rect.W, p.Rect.H, "hello world")
	oscOut := testutil.CaptureStdout(t, func() {
		tui.handleMouse(mouseEvent{
			btn: 0, x: p.Rect.X + 1, y: p.Rect.Y + 1,
			raw: []byte("\x1b[<0;1;1M"),
		})
		tui.handleMouse(mouseEvent{
			btn: 32, x: p.Rect.X + 3, y: p.Rect.Y + 1,
			raw: []byte("\x1b[<32;3;1M"),
		})
		tui.handleMouse(mouseEvent{
			btn: 0, release: true, x: p.Rect.X + 3, y: p.Rect.Y + 1,
			raw: []byte("\x1b[<0;3;1m"),
		})
	})
	testutil.NoMsg(t, got)
	if len(tui.toasts) != 1 || tui.toasts[0].text != "copied 3 chars" {
		t.Fatalf("toasts = %+v, want copied 3 chars", tui.toasts)
	}
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("hel")) + "\x1b\\"
	if !strings.Contains(oscOut, want) {
		t.Fatalf("clipboard payload missing from %q", oscOut)
	}
}

// A drag copies the range instead of forwarding anything to the app.
func TestDragCopiesSelection(t *testing.T) {
	tui, got := clientTUI(t, map[string]bool{"a": true})
	p := tui.paneByID("a")
	tui.vts["a"] = testutil.TestVT(t, p.Rect.W, p.Rect.H, "hello world\r\nsecond line")
	oscOut := testutil.CaptureStdout(t, func() {
		tui.handleMouse(mouseEvent{
			btn: 0, x: p.Rect.X + 1, y: p.Rect.Y + 1,
			raw: []byte("\x1b[<0;1;1M"),
		})
		tui.handleMouse(mouseEvent{
			btn: 32, x: p.Rect.X + 5, y: p.Rect.Y + 1,
			raw: []byte("\x1b[<32;5;1M"),
		})
		tui.handleMouse(mouseEvent{
			btn: 0, release: true, x: p.Rect.X + 5, y: p.Rect.Y + 1,
			raw: []byte("\x1b[<0;5;1m"),
		})
	})
	testutil.NoMsg(t, got)
	if tui.selText != nil || tui.press != nil {
		t.Fatal("drag left selection state behind")
	}
	if len(tui.toasts) != 1 || tui.toasts[0].text != "copied 5 chars" {
		t.Fatalf("toasts = %+v, want copied 5 chars", tui.toasts)
	}
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("hello")) + "\x1b\\"
	if !strings.Contains(oscOut, want) {
		t.Fatalf("clipboard payload missing from %q", oscOut)
	}
}

// A drag past the pane's edge stops at its content rect.
func TestSelectionClampsToStartingPane(t *testing.T) {
	tui, _ := clientTUI(t, nil)
	p := tui.paneByID("a")
	tui.handleMouse(mouseEvent{
		btn: 0, x: p.Rect.X + 1, y: p.Rect.Y + 1,
		raw: []byte("\x1b[<0;1;1M"),
	})
	tui.handleMouse(mouseEvent{
		btn: 32, x: p.Rect.X + p.Rect.W + 40,
		y: p.Rect.Y + p.Rect.H + 40, raw: []byte("\x1b[<32;99;99M"),
	})
	want := cell{x: p.Rect.W - 1, y: p.Rect.H - 1}
	if tui.selText == nil || tui.selText.focus != want {
		t.Fatalf("focus = %+v, want %+v", tui.selText, want)
	}
	if tui.selText.pane != "a" {
		t.Fatalf("selection left its pane: %q", tui.selText.pane)
	}
}

// than count sequences: an off-by-one paints the cell after the selection.
func TestSelectionHighlight(t *testing.T) {
	vt := testutil.TestVT(t, 10, 2, "abcdef\r\nghijkl")
	plain := &bytes.Buffer{}
	drawPane(plain, vt, layout.Rect{X: 0, Y: 0, W: 10, H: 2})
	if strings.Contains(plain.String(), "\x1b[7m") {
		t.Fatalf("unselected paint has reverse video: %q", plain.String())
	}
	marked := &bytes.Buffer{}
	drawPaneFrom(marked, vt, layout.Rect{X: 0, Y: 0, W: 10, H: 2}, 0, &selRange{lo: 1, hi: 3}, nil)
	rev := reversedCells(marked.String())
	for ch, want := range map[rune]bool{'a': false, 'b': true, 'c': true, 'd': true, 'e': false, 'g': false} {
		if rev[ch] != want {
			t.Fatalf("%q reversed=%v want %v (%q)", ch, rev[ch], want, marked.String())
		}
	}
}

// reversedCells walks an ANSI stream and reports each printed rune's reverse state.
func reversedCells(s string) map[rune]bool {
	out := map[rune]bool{}
	on := false
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				for _, p := range strings.Split(s[i+2:j], ";") {
					switch p {
					case "7":
						on = true
					case "0", "27":
						on = false
					}
				}
			}
			i = j + 1
			continue
		}
		out[rune(s[i])] = on
		i++
	}
	return out
}

func TestDrawMenu(t *testing.T) {
	m := &menu{x: 2, y: 3, w: 30, title: "w1p2 pi", items: []menuItem{
		{label: "Split right", key: "|"},
		{label: "Close pane", key: "x"},
	}, sel: 1}
	var b bytes.Buffer
	drawMenu(&b, theme.DefaultTheme(), m)
	out := b.String()
	for _, want := range []string{"Split right", "Close pane", "w1p2 pi", "\x1b[30;46m"} {
		if !strings.Contains(out, want) {
			t.Fatalf("menu missing %q: %q", want, out)
		}
	}
}

// TestBarPadsToWidth paints the whole last row on the bar ground, so no cell of
// the previous frame survives and the row reaches its final column.
func TestBarPadsToWidth(t *testing.T) {
	snap := &wire.Snapshot{
		Session: "dev", Workspace: "work", Windows: testWindows("one", "two"),
		ActiveWindow: 0, Focus: "w1p1",
		Panes: []wire.PaneInfo{{ID: "w1p1", Name: "bash"}},
		All:   []wire.AllPane{{ID: "w1p1", Agent: false}},
		Cols:  60, Rows: 6,
	}
	vt := vt10x.New(vt10x.WithSize(snap.Cols, snap.Rows))
	// A previous frame leaves stale glyphs on the bar row.
	if _, err := vt.Write([]byte(fmt.Sprintf("\x1b[%d;1H%s", snap.Rows, strings.Repeat("X", snap.Cols)))); err != nil {
		t.Fatalf("stale frame: %v", err)
	}
	var b bytes.Buffer
	drawBar(&b, theme.DefaultTheme(), snap, false, "")
	if _, err := vt.Write(b.Bytes()); err != nil {
		t.Fatalf("draw bar: %v", err)
	}
	vt.Lock()
	defer vt.Unlock()
	ground := vt10x.Color(theme.DefaultTheme().Palette.RailBG.R<<16 | theme.DefaultTheme().Palette.RailBG.G<<8 | theme.DefaultTheme().Palette.RailBG.B)
	for x := 0; x < snap.Cols; x++ {
		cell := vt.Cell(x, snap.Rows-1)
		if cell.Char == 'X' || cell.Char == 0 {
			t.Fatalf("cell %d = %q, want a painted cell", x, cell.Char)
		}
	}
	// The row is padded on the bar ground up to its final column.
	last := vt.Cell(snap.Cols-1, snap.Rows-1)
	if last.Char != ' ' || last.BG != ground {
		t.Fatalf("last cell = %q bg %v, want a gutter space on %v",
			last.Char, last.BG, ground)
	}
	if out := b.String(); !strings.Contains(out, "\x1b["+strconv.Itoa(snap.Rows)+";1H") {
		t.Fatalf("bar does not start on row %d: %q", snap.Rows, out)
	}
}

// TestBarUnarmed paints no prefix flag.
func TestBarUnarmed(t *testing.T) {
	snap := testutil.BarTestSnap(80)
	if out := testutil.BarTexts(barSegments(theme.DefaultTheme(), snap, false, "12 rows above")); strings.Contains(strings.Join(out, ""), "PREFIX") {
		t.Fatalf("unarmed spans carry PREFIX: %q", out)
	}
	var b bytes.Buffer
	drawBar(&b, theme.DefaultTheme(), snap, false, "")
	if strings.Contains(b.String(), "PREFIX") {
		t.Fatalf("unarmed bar paints PREFIX: %q", b.String())
	}
	b.Reset()
	drawBar(&b, theme.DefaultTheme(), snap, true, "")
	if !strings.Contains(b.String(), "PREFIX") {
		t.Fatalf("armed bar has no PREFIX: %q", b.String())
	}
}

// TestBarContrast holds every footer foreground to WCAG AA on both grounds it
// paints: the gutter and the focus fill.
func TestBarContrast(t *testing.T) {
	foregrounds := map[string]theme.RGB{
		"text":   theme.DefaultTheme().Palette.RailText,
		"header": theme.DefaultTheme().Palette.RailHeader,
		"dim":    theme.DefaultTheme().Palette.RailDim,
		"accent": theme.DefaultTheme().Palette.BarAccent,
	}
	for _, status := range []string{
		"working", "idle", "blocked", "done", "error", "unknown", "",
	} {
		fallback, colour := rail.RailGlyph(theme.DefaultTheme(), status, false)
		_, nerdColour := rail.RailGlyph(theme.DefaultTheme(), status, true)
		if fallback == "" || nerdColour != colour {
			t.Fatalf("state %q differs between glyph forms", status)
		}
		foregrounds["state "+status] = colour
	}
	// The bar has one ground: the rail's. There is no second background to
	// hold to contrast.
	for name, fg := range foregrounds {
		for bgName, bg := range map[string]theme.RGB{"gutter": theme.DefaultTheme().Palette.RailBG, "focus": theme.DefaultTheme().Palette.RailFocusBG} {
			if r := theme.Contrast(fg, bg); r < 4.5 {
				t.Fatalf("%s on %s = %.2f:1, want >= 4.5:1", name, bgName, r)
			}
		}
	}
}

func TestDragPress(t *testing.T) {
	row := &layout.Node{
		Dir: "row", Kids: []*layout.Node{{Pane: "a"}, {Pane: "b"}},
		Sizes: []float64{0.5, 0.5},
	}
	snap := testutil.TestSnapshot(row, 100, 30, nil)
	ds, ok := dragPress(snap, 49, 5)
	if !ok || ds.first != "a" || ds.second != "b" || ds.axis != "row" ||
		ds.rect != layout.PaneAreaForWithTabBar(100, 30, 0) {
		t.Fatalf("divider press: %+v ok=%v", ds, ok)
	}
	if _, ok := dragPress(snap, 10, 5); ok {
		t.Fatal("pane press started a drag")
	}

	// The press path itself must use dragPress.
	tui := &tui{snap: snap}
	tui.handleMouse(mouseEvent{btn: 0, x: 50, y: 6})
	if !tui.dragging {
		t.Fatal("handleMouse did not start a drag on a divider")
	}
	// startDrag must take its origin and cell space from the pressed rect.
	if tui.dragStart != ds.rect.X || tui.dragSpace != ds.rect.W {
		t.Fatalf("startDrag stored start=%d space=%d, want %d/%d",
			tui.dragStart, tui.dragSpace, ds.rect.X, ds.rect.W)
	}

	// Same-axis nesting: the outer hit carries the owning split's full rect,
	// not the bounding box of the two adjacent leaves.
	same := &layout.Node{Dir: "row", Kids: []*layout.Node{
		{Pane: "a"},
		{Dir: "row", Kids: []*layout.Node{{Pane: "b"}, {Pane: "c"}}},
	}}
	snap = testutil.TestSnapshot(same, 100, 30, nil)
	// The root is what layout hands its tree: the whole pane area.
	want := layout.PaneAreaForWithTabBar(snap.Cols, snap.Rows, snap.Rail)
	ds, ok = dragPress(snap, 49, 5)
	if !ok || ds.first != "a" || ds.second != "b" || ds.rect != want {
		t.Fatalf("same-axis outer press: %+v ok=%v, want rect %v", ds, ok, want)
	}
	tui.snap = snap
	tui.startDrag(ds)
	if tui.dragStart != want.X || tui.dragSpace != want.W {
		t.Fatalf("same-axis startDrag stored start=%d space=%d, want %d/%d",
			tui.dragStart, tui.dragSpace, want.X, want.W)
	}

	// A divider still belongs to dm when a pane in the split's subtree claims
	// the mouse: the app's PTY covers its content, not the border cells.
	nested := &layout.Node{Dir: "row", Kids: []*layout.Node{
		{Pane: "a"},
		{Dir: "row", Kids: []*layout.Node{{Pane: "b"}, {Pane: "c"}}},
	}}
	snap = testutil.TestSnapshot(nested, 100, 30, map[string]bool{"c": true})
	if ds, ok := dragPress(snap, 49, 5); !ok || ds.first != "a" {
		t.Fatalf("claimed-mouse divider press = %+v ok=%v", ds, ok)
	}
	if ds, ok := dragPress(snap, 74, 5); !ok {
		t.Fatalf("claimed-mouse inner divider press = %+v ok=%v", ds, ok)
	}
}

// compareScreens fails unless two terminals agree cell for cell and on the
// cursor, visibility, buffer, wrap and mouse modes.
func compareScreens(t *testing.T, want, got vt10x.View) {
	t.Helper()
	wc, wr := want.Size()
	gc, gr := got.Size()
	if wc != gc || wr != gr {
		t.Fatalf("size %dx%d, want %dx%d", gc, gr, wc, wr)
	}
	want.Lock()
	defer want.Unlock()
	got.Lock()
	defer got.Unlock()
	for y := 0; y < wr; y++ {
		for x := 0; x < wc; x++ {
			a := vt.NormCell(want.Cell(x, y))
			b := vt.NormCell(got.Cell(x, y))
			if a.Char != b.Char || a.FG != b.FG || a.BG != b.BG {
				t.Fatalf("cell %d,%d = {%q %v %v}, want {%q %v %v}",
					x, y, b.Char, b.FG, b.BG, a.Char, a.FG, a.BG)
			}
		}
	}
	if a, b := want.Cursor(), got.Cursor(); a.X != b.X || a.Y != b.Y {
		t.Fatalf("cursor %d,%d, want %d,%d", b.X, b.Y, a.X, a.Y)
	}
	if want.CursorVisible() != got.CursorVisible() {
		t.Fatalf("cursor visible = %v, want %v", got.CursorVisible(), want.CursorVisible())
	}
	if a, b := want.Mode(), got.Mode(); (a & vt10x.ModeAltScreen) != (b & vt10x.ModeAltScreen) {
		t.Fatalf("alt = %v, want %v", b&vt10x.ModeAltScreen != 0, a&vt10x.ModeAltScreen != 0)
	}
	if a, b := want.Mode(), got.Mode(); (a & vt10x.ModeWrap) != (b & vt10x.ModeWrap) {
		t.Fatalf("wrap = %v, want %v", b&vt10x.ModeWrap != 0, a&vt10x.ModeWrap != 0)
	}
	if a, b := want.Mode(), got.Mode(); (a & vt10x.ModeMouseMask) != (b & vt10x.ModeMouseMask) {
		t.Fatalf("mouse mask = %v, want %v", b&vt10x.ModeMouseMask, a&vt10x.ModeMouseMask)
	}
	if a, b := want.Mode(), got.Mode(); (a & vt10x.ModeMouseSgr) != (b & vt10x.ModeMouseSgr) {
		t.Fatalf("mouse sgr = %v, want %v", b&vt10x.ModeMouseSgr != 0, a&vt10x.ModeMouseSgr != 0)
	}
}

// A snapshot written into a fresh terminal must reproduce the screen exactly.
// Plain text, every colour class, reverse video, an alternate screen, a moved
// cursor and the mode flags all have to survive; dropping any one fails.
func TestScreenSnapshotRoundTrip(t *testing.T) {
	src := vt10x.New(vt10x.WithSize(24, 6))
	content := "\x1b[?1049h" + // alternate screen, the harder buffer
		"PLAIN\r\n" +
		"\x1b[31mRED\x1b[0m \x1b[44mBLUEBG\x1b[0m\r\n" +
		"\x1b[38;5;196mPAL256\x1b[0m\r\n" +
		"\x1b[38;2;30;144;255mTRUE\x1b[0m\r\n" +
		"\x1b[7mREV\x1b[0m \x1b[31;42;7mMIXREV\x1b[0m" +
		"\x1b[5;7H" + // cursor away from the end of the output
		"\x1b[?25l" + // hidden cursor
		"\x1b[?7l" + // wrap off
		"\x1b[?1002h\x1b[?1006h" // mouse motion plus SGR
	if _, err := src.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	dst := vt10x.New(vt10x.WithSize(24, 6))
	if _, err := dst.Write(vt.ScreenBytes(src)); err != nil {
		t.Fatal(err)
	}
	compareScreens(t, src, dst)

	// The same on the primary screen with wrap on, which is the common case.
	plain := vt10x.New(vt10x.WithSize(20, 3))
	if _, err := plain.Write([]byte("\x1b[36mCYAN\x1b[0m text\x1b[2;3H")); err != nil {
		t.Fatal(err)
	}
	plainDst := vt10x.New(vt10x.WithSize(20, 3))
	if _, err := plainDst.Write(vt.ScreenBytes(plain)); err != nil {
		t.Fatal(err)
	}
	compareScreens(t, plain, plainDst)
}

// A screen replaces the live VT; a later replay only replaces history.
func TestClientScreenThenReplay(t *testing.T) {
	src := vt10x.New(vt10x.WithSize(12, 3))
	if _, err := src.Write([]byte("\x1b[33mLIVE-SCREEN\x1b[0m\x1b[2;4H")); err != nil {
		t.Fatal(err)
	}
	data := vt.ScreenBytes(src)

	vts := map[string]vt10x.Terminal{"p1": vt10x.New(vt10x.WithSize(12, 3))}
	_, _ = vts["p1"].Write([]byte("OLD CONTENT"))
	hist := map[string][]byte{}

	applyScreen(vts, "p1", layout.Rect{W: 12, H: 3}, data)
	applyReplay(hist, "p1", []byte("RING-HISTORY"))

	live := vts["p1"]
	if live == nil {
		t.Fatal("screen did not install a VT")
	}
	got := live.String()
	if !strings.Contains(got, "LIVE-SCREEN") {
		t.Fatalf("live VT missing screen content:\n%s", got)
	}
	if strings.Contains(got, "OLD CONTENT") {
		t.Fatalf("live VT kept the old content:\n%s", got)
	}
	if strings.Contains(got, "RING-HISTORY") {
		t.Fatalf("replay wrote into the live VT:\n%s", got)
	}
	if string(hist["p1"]) != "RING-HISTORY" {
		t.Fatalf("history = %q", hist["p1"])
	}

	// A second replay still leaves the live VT alone.
	applyReplay(hist, "p1", []byte("MORE-HISTORY"))
	if again := live.String(); again != got {
		t.Fatalf("replay changed the live VT:\n%s", again)
	}
	if string(hist["p1"]) != "MORE-HISTORY" {
		t.Fatalf("history = %q", hist["p1"])
	}
}

// actions and the old keys do not.
func TestKeymapDispatchRemapsPrefixSplitAndDetach(t *testing.T) {
	tui, got := clientTUI(t, nil)
	tui.keymap = testutil.KeymapTestLoad(t, `
prefix "ctrl+a"

keymap "main" {
    "send-prefix" "prefix"
    "split-right" "S"
    "detach" "X"
}
`)
	// The configured prefix arms, and the configured split key splits right.
	feed(tui, "\x01")
	if !tui.armed {
		t.Fatal("the configured prefix did not arm")
	}
	testutil.NoMsg(t, got)
	feed(tui, "S")
	m := testutil.NextMsg(t, got)
	if m.Action != "split" || m.Dir != "right" {
		t.Fatalf("the configured split key sent %+v", m)
	}
	if tui.armed {
		t.Fatal("the split action left the client armed")
	}

	// The old prefix byte is plain input now.
	feed(tui, "\x02")
	m = testutil.NextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != "\x02" {
		t.Fatalf("the old prefix byte sent %+v", m)
	}
	if tui.armed {
		t.Fatal("the old prefix byte armed the client")
	}

	// The old split key is unbound: it notifies and disarms.
	tui.arm()
	feed(tui, "|")
	if tui.armed {
		t.Fatal("the old split key stayed armed")
	}
	if n := len(tui.toasts); n == 0 || !strings.Contains(tui.toasts[n-1].text, "no binding") {
		t.Fatalf("the old split key raised %+v", tui.toasts)
	}
	testutil.NoMsg(t, got)

	// The old detach key is unbound: it notifies and does not quit.
	tui.arm()
	feed(tui, "d")
	if tui.quit {
		t.Fatal("the old detach key still quit")
	}
	if tui.armed {
		t.Fatal("the old detach key stayed armed")
	}

	// The configured detach key quits.
	feed(tui, "\x01X")
	if !tui.quit {
		t.Fatal("the configured detach key did not quit")
	}
}

// TestKeymapDispatchMenuLabels: each menu row shows the configured main key for
// its action, or nothing when the action is unbound. The menu's own keys come
// from the menu mode.
func TestKeymapDispatchMenuLabels(t *testing.T) {
	tui := &tui{snap: testutil.TestSnapshot(&layout.Node{Pane: "a"}, 100, 30, nil)}
	tui.keymap = testutil.KeymapTestLoad(t, `
prefix "ctrl+b"

keymap "main" {
    "split-right" "S"
    "close-pane" "Q"
}

keymap "menu" {
    "down" "J"
}
`)
	tui.openMenu(tui.paneByID("a"), 5, 3)
	if tui.menu == nil {
		t.Fatal("menu did not open")
	}
	want := map[string]string{
		"Split right": "S",
		"Split down":  "",
		"Split left":  "",
		"Split up":    "",
		"Zoom":        "",
		"Close pane":  "Q",
		"Scrollback":  "",
	}
	if len(tui.menu.items) != len(want) {
		t.Fatalf("pane menu has %d rows, want the %d pane actions",
			len(tui.menu.items), len(want))
	}
	for _, it := range tui.menu.items {
		key, ok := want[it.label]
		if !ok {
			t.Fatalf("unexpected menu row %q", it.label)
		}
		if it.key != key {
			t.Fatalf("%s key = %q, want %q", it.label, it.key, key)
		}
	}
	// The item keys are hints. The row walk is the menu mode's own binding.
	tui.inbuf = []byte("J")
	if !tui.menuKey() || tui.menu.sel != 1 {
		t.Fatalf("J did not move the selection: %+v", tui.menu)
	}
	// A key that is neither a menu binding nor a hint closes the menu.
	tui.inbuf = []byte("y")
	tui.menuKey()
	if tui.menu != nil {
		t.Fatal("an unbound menu key kept the menu open")
	}
}

// TestKeymapDispatchUnboundNotifiesAndDisarms: a key with no binding raises the
// notify toast and leaves the armed state.
func TestKeymapDispatchUnboundNotifiesAndDisarms(t *testing.T) {
	tui, got := clientTUI(t, nil)
	feed(tui, "\x02")
	if !tui.armed {
		t.Fatal("the prefix did not arm")
	}
	feed(tui, "\x01")
	if tui.armed {
		t.Fatal("an unbound key kept the client armed")
	}
	if n := len(tui.toasts); n == 0 || !strings.Contains(tui.toasts[n-1].text, "no binding for") {
		t.Fatalf("an unbound key raised %+v", tui.toasts)
	}
	testutil.NoMsg(t, got)
}

// TestKeymapDispatchPartialEscapeWaitsAndLoneEscDismisses: a partial sequence
// waits for its remaining bytes, a stalled esc resolves to dismiss, and a
// complete arrow still runs its action.
func TestKeymapDispatchPartialEscapeWaitsAndLoneEscDismisses(t *testing.T) {
	tui, got := clientTUI(t, nil)
	feed(tui, "\x02")
	if !tui.armed {
		t.Fatal("the prefix did not arm")
	}
	start := time.Now()
	feed(tui, "\x1b")
	if !tui.armed {
		t.Fatal("the escape disarmed instead of waiting")
	}
	testutil.NoMsg(t, got)
	// The escape is held as a possible paste start first. Once that deadline
	// passes it takes the normal path and waits as the head of an arrow.
	if !tui.expirePaste(start.Add(escTimeout + time.Millisecond)) {
		t.Fatal("the escape was not released from the paste check")
	}
	if !tui.pending || !tui.armed {
		t.Fatalf("a lone escape did not wait: pending=%v armed=%v", tui.pending, tui.armed)
	}
	if tui.expireArmed(tui.escAt) {
		t.Fatal("the escape expired before its deadline")
	}
	if !tui.expireArmed(tui.escAt.Add(escTimeout + time.Millisecond)) {
		t.Fatal("the escape did not expire")
	}
	if tui.armed || len(tui.inbuf) != 0 {
		t.Fatalf("stalled escape left state: armed=%v inbuf=%q", tui.armed, tui.inbuf)
	}

	// A complete sequence runs its action.
	feed(tui, "\x02")
	feed(tui, "\x1b[A")
	m := testutil.NextMsg(t, got)
	if m.Action != "focus-dir" || m.Dir != "up" {
		t.Fatalf("the arrow sent %+v", m)
	}
	if tui.armed {
		t.Fatal("the arrow left the client armed")
	}
}

// TestKeymapDispatchUnboundEscapeKeepsItsTail: an unbound escape sequence is one
// key. It disarms and notifies, and none of its bytes reach the pane.
func TestKeymapDispatchUnboundEscapeKeepsItsTail(t *testing.T) {
	tui, got := clientTUI(t, nil)
	for _, seq := range []string{"\x1bOP", "\x1b[1;5A", "\x1b[H"} {
		tui.arm()
		feed(tui, seq)
		if tui.armed {
			t.Fatalf("%q left the client armed", seq)
		}
		if len(tui.inbuf) != 0 {
			t.Fatalf("%q left %q in the buffer", seq, tui.inbuf)
		}
		testutil.NoMsg(t, got)
	}
	if n := len(tui.toasts); n == 0 || !strings.Contains(tui.toasts[n-1].text, "no binding for") {
		t.Fatalf("the escape tail raised %+v", tui.toasts)
	}
}

// TestKeymapDispatchSplitPrefixStaysArmed: a multi-byte prefix that arrives in
// two reads arms and stays armed past the esc deadline.
func TestKeymapDispatchSplitPrefixStaysArmed(t *testing.T) {
	tui, got := clientTUI(t, nil)
	tui.keymap = testutil.KeymapTestLoad(t, `
prefix "up"

keymap "main" {
    "split-right" "|"
}
`)
	// The head waits as a possible prefix, then the rest completes it.
	start := time.Now()
	feed(tui, "\x1b")
	tui.expirePaste(start.Add(escTimeout + time.Millisecond))
	if !tui.pending || tui.armed {
		t.Fatalf("prefix head: pending=%v armed=%v", tui.pending, tui.armed)
	}
	feed(tui, "[A")
	if !tui.armed || tui.pending {
		t.Fatalf("split prefix: armed=%v pending=%v", tui.armed, tui.pending)
	}
	if tui.expireArmed(tui.escAt.Add(escTimeout + time.Millisecond)) {
		t.Fatal("the armed sequence expired with the old prefix head")
	}
	if !tui.armed {
		t.Fatal("the client disarmed itself")
	}
	feed(tui, "|")
	m := testutil.NextMsg(t, got)
	if m.Action != "split" || m.Dir != "right" {
		t.Fatalf("the split key sent %+v", m)
	}
}

// TestKeymapDispatchSendPrefixSendsConfiguredBytes: the armed send-prefix action
// sends the configured prefix bytes, not a literal ctrl+b.
func TestKeymapDispatchSendPrefixSendsConfiguredBytes(t *testing.T) {
	tui, got := clientTUI(t, nil)
	tui.keymap = testutil.KeymapTestLoad(t, `
prefix "ctrl+a"

keymap "main" {
    "send-prefix" "prefix"
}
`)
	feed(tui, "\x01")
	if !tui.armed {
		t.Fatal("the configured prefix did not arm")
	}
	feed(tui, "\x01")
	m := testutil.NextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != "\x01" {
		t.Fatalf("send-prefix sent %+v (%q)", m, m.Keys)
	}
	if tui.armed {
		t.Fatal("send-prefix left the client armed")
	}
	testutil.NoMsg(t, got)
}

// writeTheme puts a theme file in dir/themes and returns its path.
func writeTheme(t *testing.T, dir, name, src string) string {
	t.Helper()
	path := filepath.Join(dir, "themes", name+".kdl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestReloadActionReadsConfigAndTheme: the reload action re-reads the default
// config path and its named theme. A changed keymap and theme replace the
// running ones and a toast confirms the reload.
func TestReloadActionReadsConfigAndTheme(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "dm")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	tui := &tui{keymap: keymap.LoadDefault()}

	path := filepath.Join(cfgDir, "config.kdl")
	if err := os.WriteFile(path, []byte(`theme "night"
keymap "main" {
    "split-right" "S"
    "reload" "ctrl+r"
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTheme(t, cfgDir, "night", `palette { "rail-bg" "#010203" }`)

	feed(tui, "\x02\x12")
	if tui.armed {
		t.Fatal("reload left the client armed")
	}
	if got := tui.keymap.Key("main", "split-right"); got != "S" {
		t.Fatalf("split-right key = %q, want S after reload", got)
	}
	if got := tui.keymap.Theme().Palette.RailBG; got != (theme.RGB{R: 1, G: 2, B: 3}) {
		t.Fatalf("keymap theme rail-bg = %v, want 1 2 3", got)
	}
	if got := tui.theme().Palette.RailBG; got != (theme.RGB{R: 1, G: 2, B: 3}) {
		t.Fatalf("running theme rail-bg = %v, want 1 2 3", got)
	}
	if !tui.dirty {
		t.Fatal("reload did not mark the frame dirty")
	}
	if n := len(tui.toasts); n == 0 || tui.toasts[n-1].text != "config reloaded" {
		t.Fatalf("reload toasts = %+v, want a config reloaded toast", tui.toasts)
	}
}

// TestReloadActionKeepsSettingsOnBadConfig: a malformed reload leaves the
// running keymap and theme in place and toasts the error.
func TestReloadActionKeepsSettingsOnBadConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "dm")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfgDir, "config.kdl")
	if err := os.WriteFile(path, []byte(`theme "old"
keymap "main" {
    "split-right" "S"
    "reload" "ctrl+r"
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTheme(t, cfgDir, "old", `palette { "rail-bg" "#040506" }`)
	oldCfg, err := keymap.LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	oldTheme := oldCfg.Theme()
	tui := &tui{keymap: oldCfg, th: &oldTheme}

	if err := os.WriteFile(path, []byte(`bogus "x"`), 0o600); err != nil {
		t.Fatal(err)
	}
	feed(tui, "\x02\x12")
	if tui.armed {
		t.Fatal("failed reload left the client armed")
	}
	if tui.keymap != oldCfg {
		t.Fatal("failed reload replaced the running keymap")
	}
	if got := tui.keymap.Key("main", "split-right"); got != "S" {
		t.Fatalf("split-right key = %q, want S after a failed reload", got)
	}
	if got := tui.keymap.Theme().Palette.RailBG; got != (theme.RGB{R: 4, G: 5, B: 6}) {
		t.Fatalf("keymap theme rail-bg = %v, want 4 5 6", got)
	}
	if got := tui.theme().Palette.RailBG; got != (theme.RGB{R: 4, G: 5, B: 6}) {
		t.Fatalf("running theme rail-bg = %v, want 4 5 6", got)
	}
	if n := len(tui.toasts); n == 0 || !strings.Contains(tui.toasts[n-1].text, "unknown node") {
		t.Fatalf("failed reload toasts = %+v, want the parse error", tui.toasts)
	}
}

// TestKeymapDispatchPartialEscapeInMenu: a menu arrow split across two reads
// waits for its remaining bytes and then moves the selection.
func TestKeymapDispatchPartialEscapeInMenu(t *testing.T) {
	tui := &tui{snap: testutil.TestSnapshot(&layout.Node{Pane: "a"}, 100, 30, nil), keymap: keymap.LoadDefault()}
	tui.openMenu(tui.paneByID("a"), 5, 3)
	if tui.menu == nil {
		t.Fatal("menu did not open")
	}
	tui.inbuf = []byte("\x1b")
	if tui.menuKey() {
		t.Fatal("a lone escape did not wait")
	}
	if !tui.pending || tui.menu == nil {
		t.Fatalf("partial escape: pending=%v menu=%+v", tui.pending, tui.menu)
	}
	tui.inbuf = append(tui.inbuf, '[', 'B')
	if !tui.menuKey() {
		t.Fatal("the completed arrow still waited")
	}
	if tui.menu == nil || tui.menu.sel != 1 || tui.pending {
		t.Fatalf("the arrow did not move: %+v pending=%v", tui.menu, tui.pending)
	}
}

// TestKeymapDispatchSwitcherLeftRightAreNoOps: left, right and shift-tab have no
// meaning in the switcher list. They neither close it nor type into the query,
// and an unbound escape sequence is not text either.
func TestKeymapDispatchSwitcherLeftRightAreNoOps(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	if tui.sw == nil {
		t.Fatal("the switcher did not open")
	}
	for _, seq := range []string{"\x1b[D", "\x1b[C", "\x1b[Z", "\x1bOP"} {
		feed(tui, seq)
		if tui.sw == nil {
			t.Fatalf("%q closed the switcher", seq)
		}
		if q := tui.sw.Query(); q != "" {
			t.Fatalf("%q typed %q into the query", seq, q)
		}
	}
	// Plain text still reaches the query, and the arrows still move.
	feed(tui, "beta")
	if q := tui.sw.Query(); q != "beta" {
		t.Fatalf("query = %q, want beta", q)
	}
	feed(tui, "\x1b[A")
	if tui.sw == nil {
		t.Fatal("the up arrow closed the switcher")
	}
	testutil.NoMsg(t, got)
}

// TestHelpOpensAndCloses: the help action adds the keymap float and the help
// mode, and esc takes both down again. A second press toggles it closed.
func TestHelpOpensAndCloses(t *testing.T) {
	tui, _ := clientTUI(t, nil)
	tui.runAction("help")
	if tui.keys == nil {
		t.Fatal("the help action did not open the keymap view")
	}
	if tui.mode != "help" {
		t.Fatalf("mode = %q, want help", tui.mode)
	}
	if len(tui.floats) != 1 || tui.floats[0] != tui.keys.Float {
		t.Fatalf("floats = %+v, want the keymap float", tui.floats)
	}
	if tui.armed {
		t.Fatal("the help action left the client armed")
	}
	// A lone esc is the close key. It waits as the head of an escape sequence,
	// then closes once its deadline passes.
	feed(tui, "\x1b")
	if tui.keys == nil || !tui.pending {
		t.Fatalf("a lone esc did not wait: keys=%v pending=%v", tui.keys, tui.pending)
	}
	if !tui.expireMode(tui.escAt.Add(escTimeout + time.Millisecond)) {
		t.Fatal("the stalled escape did not close the view")
	}
	if tui.keys != nil || tui.mode != "" || len(tui.floats) != 0 || len(tui.inbuf) != 0 {
		t.Fatalf("esc did not close the view: keys=%v mode=%q floats=%d inbuf=%q",
			tui.keys, tui.mode, len(tui.floats), tui.inbuf)
	}

	// The default ? opens the view, and ? closes it again.
	feed(tui, "\x02?")
	if tui.keys == nil || tui.mode != "help" {
		t.Fatalf("prefix ? did not open the view: keys=%v mode=%q", tui.keys, tui.mode)
	}
	feed(tui, "?")
	if tui.keys != nil || tui.mode != "" {
		t.Fatalf("? did not close the view: keys=%v mode=%q", tui.keys, tui.mode)
	}
}

// zoomFrameTUI is a tui over a row split with a rail, each pane's screen
// carrying a mark, so a frame's bytes say which panes it painted.
type zoomFrameTUI struct {
	tui  *tui
	snap *wire.Snapshot
	out  *bytes.Buffer
}

func newZoomFrameTUI(t *testing.T) *zoomFrameTUI {
	t.Helper()
	const cols, rows, rail = 100, 20, 20
	root := &layout.Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*layout.Node{{Pane: "a"}, {Pane: "b"}},
	}
	snap := testutil.TestSnapshot(root, cols, rows, nil)
	snap.Rail = rail
	snap.Focus = "b"
	snap.Session = "one"
	snap.Workspace = "s1"
	snap.Windows = testWindows("main")
	for i := range snap.Panes {
		snap.Panes[i].Name = snap.Panes[i].ID + "-name"
	}
	relayout(snap)
	out := &bytes.Buffer{}
	tui := &tui{snap: snap, vts: map[string]vt10x.Terminal{}, out: out}
	for _, p := range snap.Panes {
		vt := vt10x.New(vt10x.WithSize(p.Rect.W, p.Rect.H))
		tui.vts[p.ID] = vt
		vt.Write([]byte("\x1b[?25l\x1b[1;1H" + strings.ToUpper(p.ID) + "-MARK"))
	}
	return &zoomFrameTUI{tui: tui, snap: snap, out: out}
}

// frame draws one frame and returns its bytes.
func (f *zoomFrameTUI) frame() string {
	f.out.Reset()
	f.tui.draw()
	return f.out.String()
}

// zoom sets or clears the snapshot's zoom the way the server does: the zoomed
// pane takes the single-pane box and its siblings keep their layout rects.
func (f *zoomFrameTUI) zoom(id string) {
	f.snap.Zoom = id
	relayout(f.snap)
	if id != "" {
		area := layout.PaneAreaForWithTabBar(f.snap.Cols, f.snap.Rows, f.snap.Rail)
		for i := range f.snap.Panes {
			if f.snap.Panes[i].ID == id {
				f.snap.Panes[i].Rect = layout.InsetBox(area)
			}
		}
	}
}

// screenGlyphs counts one glyph as a terminal ends up.
func screenGlyphs(vt vt10x.Terminal, ch rune) int {
	cols, rows := vt.Size()
	return screenGlyphsIn(vt, ch, layout.Rect{X: 0, Y: 0, W: cols, H: rows})
}

// screenCorners counts the box corners on a screen, so a frame that paints one
// box and a frame that paints two are told apart.
func screenCorners(vt vt10x.Terminal) int {
	n := 0
	for _, ch := range []rune{'┌', '┐', '└', '┘'} {
		n += screenGlyphs(vt, ch)
	}
	return n
}

// screenGlyphsIn counts one glyph inside a rectangle as the terminal ends up.
func screenGlyphsIn(vt vt10x.Terminal, ch rune, r layout.Rect) int {
	vt.Lock()
	defer vt.Unlock()
	n := 0
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			if vt.Cell(x, y).Char == ch {
				n++
			}
		}
	}
	return n
}

// TestZoomFrameFilter pins the draw filter: the zoomed frame paints the zoomed
// pane alone, with no sibling box and no grip, and clearing the zoom brings the
// unzoomed picture back byte for byte.
func TestZoomFrameFilter(t *testing.T) {
	f := newZoomFrameTUI(t)
	first := f.frame()
	if !strings.Contains(first, "A-MARK") || !strings.Contains(first, "B-MARK") {
		t.Fatalf("the split frame painted one pane: %q", first)
	}
	area := layout.PaneAreaForWithTabBar(f.snap.Cols, f.snap.Rows, f.snap.Rail)
	scr := vt10x.New(vt10x.WithSize(f.snap.Cols, f.snap.Rows))
	if _, err := scr.Write([]byte(first)); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	if got := screenCorners(scr); got != 8 {
		t.Fatalf("split screen carries %d box corners, want two boxes' 8", got)
	}
	if got := screenGlyphsIn(scr, '•', area); got != 2 {
		t.Fatalf("split screen carries %d grips in the pane area, want 2", got)
	}

	f.zoom("b")
	zoomed := f.frame()
	if strings.Contains(zoomed, "A-MARK") {
		t.Fatalf("the zoomed frame painted the hidden sibling: %q", zoomed)
	}
	if !strings.Contains(zoomed, "B-MARK") {
		t.Fatalf("the zoomed frame dropped the zoomed pane: %q", zoomed)
	}
	if _, err := scr.Write([]byte(zoomed)); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	if got := screenCorners(scr); got != 4 {
		t.Fatalf("zoomed screen carries %d box corners, want the zoomed pane's 4", got)
	}
	if got := screenGlyphsIn(scr, '•', area); got != 0 {
		t.Fatalf("zoomed screen carries %d grips in the pane area, want none", got)
	}
	// The hidden sibling's own sides run down the zoomed pane's middle. Its
	// top and bottom rows land under the zoomed box's borders, so the sides are
	// the cells that tell a filtered frame from an unfiltered one.
	inside := layout.InsetBox(area)
	inside.X, inside.Y = inside.X+1, inside.Y+1
	inside.W, inside.H = inside.W-2, inside.H-2
	if got := screenGlyphsIn(scr, '│', inside); got != 0 {
		t.Fatalf("zoomed screen carries %d sibling borders inside the pane, want none", got)
	}
	scr.Lock()
	corner := scr.Cell(area.X, area.Y).Char
	scr.Unlock()
	if corner != '┌' {
		t.Fatalf("zoomed box corner = %q, want it at the pane area %d,%d", corner, area.X, area.Y)
	}

	f.zoom("")
	if back := f.frame(); back != first {
		t.Fatalf("the unzoomed frame came back changed:\n got %q\nwant %q", back, first)
	}
}

// TestZoomHitsFollowTheZoomedPane pins the hit filter: a point in a hidden
// sibling's old rectangle lands on the zoomed pane, and a point outside the
// pane area keeps its present answer.
func TestZoomHitsFollowTheZoomedPane(t *testing.T) {
	f := newZoomFrameTUI(t)
	a := f.tui.paneByID("a").Rect
	x, y := a.X+2, a.Y+2
	f.zoom("b")
	for _, box := range []bool{true, false} {
		var got *wire.PaneInfo
		if box {
			got = f.tui.paneAtBox(x, y)
		} else {
			got = f.tui.paneAt(x, y)
		}
		if got == nil || got.ID != "b" {
			t.Fatalf("box=%v: a hidden sibling's cell hit %+v, want the zoomed b", box, got)
		}
	}
	// The rail column and the bar row are outside the pane area.
	if got := f.tui.paneAtBox(1, 1); got != nil {
		t.Fatalf("a rail cell hit %+v while zoomed", got)
	}
	if got := f.tui.paneAt(f.snap.Cols-1, f.snap.Rows-1); got != nil {
		t.Fatalf("a bar cell hit %+v while zoomed", got)
	}
	// Unzoomed, the same point is the sibling's again.
	f.zoom("")
	if got := f.tui.paneAtBox(x, y); got == nil || got.ID != "a" {
		t.Fatalf("unzoomed cell hit %+v, want a", got)
	}
}

// TestZoomDragPressGuard pins the divider guard: the same cell starts a drag
// while unzoomed and cannot start one while zoomed.
func TestZoomDragPressGuard(t *testing.T) {
	f := newZoomFrameTUI(t)
	root := f.snap.Root
	boxes := layout.ChildBoxRects(root, layout.PaneAreaFor(f.snap.Cols, f.snap.Rows, f.snap.Rail))
	pad := layout.PadRect(root, boxes, 0)
	x, y := pad.X, pad.Y+2
	if ds, ok := dragPress(f.snap, x, y); !ok || ds.first != "a" || ds.second != "b" {
		t.Fatalf("the split divider took no press: %+v ok=%v", ds, ok)
	}

	f.zoom("b")
	if ds, ok := dragPress(f.snap, x, y); ok {
		t.Fatalf("a zoomed frame started a drag: %+v", ds)
	}
	tui := &tui{snap: f.snap, vts: map[string]vt10x.Terminal{}}
	tui.handleMouse(mouseEvent{btn: 0, x: x, y: y})
	if tui.dragging {
		t.Fatal("a press on a hidden split's divider started a drag")
	}
	// The hover goes through the same guard, so it has nothing to show.
	tui.updatePadHover(x, y)
	if tui.padZone != (layout.Rect{}) || tui.barInfo() != "" {
		t.Fatalf("a zoomed frame showed a divider: %v %q", tui.padZone, tui.barInfo())
	}
}

// TestZoomLayoutSig pins the repaint trigger: the signature differs when only
// the zoom differs, even with every rectangle the same.
func TestZoomLayoutSig(t *testing.T) {
	f := newZoomFrameTUI(t)
	plain := f.tui.layoutSig(f.snap)
	if f.snap.Zoom != "" {
		t.Fatalf("the fixture starts zoomed on %q", f.snap.Zoom)
	}
	f.snap.Zoom = "b"
	if got := f.tui.layoutSig(f.snap); got == plain {
		t.Fatalf("the signature ignored the zoom: %q", got)
	}
	f.snap.Zoom = ""
	if got := f.tui.layoutSig(f.snap); got != plain {
		t.Fatalf("clearing the zoom left the signature at %q, want %q", got, plain)
	}
}

// TestZoomBarMark pins the focus chip: the chip carries a Z in its own spans
// while zoomed, and the elision ladder drops it with the status word and the
// name.
func TestZoomBarMark(t *testing.T) {
	f := newZoomFrameTUI(t)
	chipTexts := func(l *barLeft) []string {
		var out []string
		for _, g := range l.groups(theme.DefaultTheme()) {
			if len(g) > 0 && g[0].BG == theme.DefaultTheme().Palette.RailFocusBG {
				for _, s := range g {
					out = append(out, s.Text)
				}
			}
		}
		return out
	}
	has := func(texts []string, want string) bool {
		for _, s := range texts {
			if s == want {
				return true
			}
		}
		return false
	}

	plain := chipTexts(barLeftFor(theme.DefaultTheme(), f.snap))
	if has(plain, " Z") {
		t.Fatalf("an unzoomed chip carries the mark: %q", plain)
	}
	f.snap.Zoom = "b"
	zoomed := chipTexts(barLeftFor(theme.DefaultTheme(), f.snap))
	if !has(zoomed, " Z") {
		t.Fatalf("the zoomed chip carries no mark: %q", zoomed)
	}
	// The mark is a span of the chip, not a block of its own, and it sits
	// between the id and the name.
	id := -1
	for i, s := range zoomed {
		if s == " b" {
			id = i
		}
	}
	if id < 0 || id+1 >= len(zoomed) || zoomed[id+1] != " Z" {
		t.Fatalf("the mark is not the span after the id: %q", zoomed)
	}

	// Walk the ladder. The status word goes first, then the mark, then the
	// name; the mark never shows without the id.
	l := barLeftFor(theme.DefaultTheme(), f.snap)
	full := segment.SegCells(segment.Join(l.groups(theme.DefaultTheme()), segment.Seg{Text: " "}))
	sawNameWithoutMark := false
	for budget := 1; budget <= full; budget++ {
		segs := barLeftFor(theme.DefaultTheme(), f.snap).fit(theme.DefaultTheme(), budget)
		var got []string
		for _, s := range segs {
			if s.BG == theme.DefaultTheme().Palette.RailFocusBG {
				got = append(got, s.Text)
			}
		}
		mark := has(got, " Z")
		if has(got, " unknown") && !mark {
			t.Fatalf("budget %d kept the status word and dropped the mark: %q", budget, got)
		}
		if mark && !has(got, " b") {
			t.Fatalf("budget %d kept the mark and dropped the id: %q", budget, got)
		}
		if has(got, " b-name") && !mark {
			sawNameWithoutMark = true
		}
	}
	if !sawNameWithoutMark {
		t.Fatal("no budget dropped the mark while the name survived")
	}
}

// TestZoomExecReachesTheHandler pins the action the client sends: the keymap
// action carries the server's own name. The dm exec side, which needs the
// built binary, is TestZoomExecReachesTheServer in the blackbox package.
func TestZoomExecReachesTheHandler(t *testing.T) {
	tui, got := clientTUI(t, nil)
	tui.armed = true
	tui.runAction("zoom-pane")
	select {
	case m := <-got:
		if m.Type != "req" || m.Action != "zoom-pane" {
			t.Fatalf("the action sent %+v, want a req for zoom-pane", m)
		}
	case <-time.After(time.Second):
		t.Fatal("the zoom-pane action sent nothing")
	}
	if tui.armed {
		t.Fatal("the zoom-pane action left the client armed")
	}
	// The server side of this action is TestZoomExecReachesTheServer in the
	// blackbox package, which needs the built binary.
}
