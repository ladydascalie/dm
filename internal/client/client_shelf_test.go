package client

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"dm/internal/box"
	"dm/internal/cells"
	"dm/internal/layout"
	"dm/internal/rail"
	"dm/internal/segment"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/wire"
)

// shelfTUI is the switcher fixture with two panes set aside: the snapshot lists
// them after the live pane, with the shelf flag and no rect, the way the server
// sends them.
func shelfTUI(t *testing.T) (*tui, chan wire.Msg) {
	t.Helper()
	tui, got := switcherTUI(t)
	tui.snap.Panes = append(tui.snap.Panes,
		wire.PaneInfo{ID: "w1p7", Name: "held", Status: "blocked", Shelf: true},
		wire.PaneInfo{ID: "w1p8", Name: "waiting", Status: "working", Shelf: true},
	)
	tui.snap.ShelfActive = 0
	return tui, got
}

// nextMsg takes the next request the client sent, or fails on the deadline.
func nextMsg(t *testing.T, got chan wire.Msg) wire.Msg {
	t.Helper()
	select {
	case m := <-got:
		return m
	case <-time.After(time.Second):
		t.Fatal("the client sent nothing")
		return wire.Msg{}
	}
}

// drainSummon takes the resize and the screen request a summon sends, in that
// order, so a later assertion sees only what the key under test sent.
func drainSummon(t *testing.T, tui *tui, got chan wire.Msg) {
	t.Helper()
	m := nextMsg(t, got)
	if m.Type != "resize" || m.Target != "w1p7" {
		t.Fatalf("first summon frame = %+v, want a resize for w1p7", m)
	}
	m = nextMsg(t, got)
	if m.Type != "req" || m.Action != "screen" || m.Target != "w1p7" {
		t.Fatalf("second summon frame = %+v, want a screen request for w1p7", m)
	}
}

// TestShelfPrefixToggles proves prefix F opens the shelf over the focused pane
// and a second press closes it, leaving the panes alone.
func TestShelfPrefixToggles(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	r := tui.shelfRect()
	if want := shelfRectFor(tui.snap, true, 80); r != want {
		t.Fatalf("default shelf rect = %+v, want the centred box %+v", r, want)
	}
	feed(tui, "\x02F")
	if tui.shelf == nil || tui.mode != "shelf" {
		t.Fatalf("prefix F left shelf %v and mode %q", tui.shelf, tui.mode)
	}
	if tui.shelf.float.Rect != r {
		t.Fatalf("shelf rect = %+v, want the focused pane's box %+v", tui.shelf.float.Rect, r)
	}
	// The summon fits the terminal to the box with the strip row taken off,
	// and tells the pane the size its program must reflow to.
	if tui.shelf.cols != r.W || tui.shelf.rows != r.H-1 {
		t.Fatalf("shelf content = %dx%d, want %dx%d", tui.shelf.cols, tui.shelf.rows, r.W, r.H-1)
	}
	term := tui.vts["w1p7"]
	if term == nil {
		t.Fatal("the shelf built no terminal for the active member")
	}
	if c, rw := term.Grid().Size(); c != r.W || rw != r.H-1 {
		t.Fatalf("terminal = %dx%d, want %dx%d", c, rw, r.W, r.H-1)
	}
	drainSummon(t, tui, got)

	feed(tui, "\x02F")
	if tui.shelf != nil || tui.mode != "" {
		t.Fatalf("a second prefix F left shelf %v and mode %q", tui.shelf, tui.mode)
	}
	testutil.NoMsg(t, got)
}

// TestShelfPrefixFRefusesAnEmptyShelf proves an empty shelf opens nothing and
// says why.
func TestShelfPrefixFRefusesAnEmptyShelf(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	feed(tui, "\x02F")
	if tui.shelf != nil || tui.mode != "" {
		t.Fatalf("an empty shelf opened %v in mode %q", tui.shelf, tui.mode)
	}
	if len(tui.toasts) == 0 || !strings.Contains(tui.toasts[len(tui.toasts)-1].text, "shelf is empty") {
		t.Fatalf("toasts = %+v, want one saying the shelf is empty", tui.toasts)
	}
	testutil.NoMsg(t, got)
}

// TestShelfKeysGoToTheActiveMember proves the shelf keeps only the prefix: a
// typed key reaches the active member, not the pane the float covers.
func TestShelfKeysGoToTheActiveMember(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	feed(tui, "\x02F")
	drainSummon(t, tui, got)

	feed(tui, "ls")
	m := nextMsg(t, got)
	if m.Type != "keys" || m.Target != "w1p7" {
		t.Fatalf("typed key sent %+v, want keys to w1p7", m)
	}
	if string(m.Keys) != "ls" {
		t.Fatalf("keys = %q, want ls", m.Keys)
	}
}

// TestShelfPrefixNPMovesTheMember proves prefix n and prefix p step in
// opposite directions. Three members, active 1: n names index 2 and p names
// index 0. Two members cannot prove it, because both steps round to the same
// pane from either index, so a moveShelf that ignored its sign would pass.
func TestShelfPrefixNPMovesTheMember(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	tui.snap.Panes = append(tui.snap.Panes,
		wire.PaneInfo{ID: "w1p9", Name: "third", Status: "idle", Shelf: true})
	feed(tui, "\x02F")
	drainSummon(t, tui, got)

	// From the middle member, n asks for the last.
	tui.snap.ShelfActive = 1
	feed(tui, "\x02n")
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "focus" || m.Target != "w1p9" {
		t.Fatalf("prefix n from member 1 sent %+v, want a focus request for w1p9 at index 2", m)
	}
	// From the same middle member, p asks for the first.
	tui.snap.ShelfActive = 1
	feed(tui, "\x02p")
	m = nextMsg(t, got)
	if m.Type != "req" || m.Action != "focus" || m.Target != "w1p7" {
		t.Fatalf("prefix p from member 1 sent %+v, want a focus request for w1p7 at index 0", m)
	}
	if tui.shelf == nil {
		t.Fatal("a move closed the shelf")
	}
}

// TestShelfPrefixGPops proves prefix G pops the member the shelf shows and
// closes the float.
func TestShelfPrefixGPops(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	feed(tui, "\x02F")
	drainSummon(t, tui, got)

	feed(tui, "\x02G")
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "pop-pane" || m.Target != "w1p7" {
		t.Fatalf("prefix G sent %+v, want pop-pane for w1p7", m)
	}
	if tui.shelf != nil || tui.mode != "" {
		t.Fatalf("a pop left shelf %v and mode %q", tui.shelf, tui.mode)
	}
}

// TestShelfPopKeyWithNoShelf proves G outside the shelf names no target, so the
// server takes the shelf's own active member.
func TestShelfPopKeyWithNoShelf(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	feed(tui, "\x02G")
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "pop-pane" || m.Target != "" {
		t.Fatalf("G sent %+v, want pop-pane with no target", m)
	}
}

// TestShelfShelveKeySendsTheAction proves f asks the server to shelve the
// focused pane.
func TestShelfShelveKeySendsTheAction(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	feed(tui, "\x02f")
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "shelve-pane" || m.Target != "w1p1" {
		t.Fatalf("f sent %+v, want shelve-pane for the focused pane", m)
	}
}

// TestShelfStripPaintsChips proves the strip carries one chip per member with
// its state glyph and name, that the active chip takes the focus ground, and
// that the title names the count.
func TestShelfStripPaintsChips(t *testing.T) {
	t.Parallel()
	tui, _ := shelfTUI(t)
	feed(tui, "\x02F")
	th, set := tui.theme(), tui.glyphSet()

	var buf bytes.Buffer
	inner := layout.Rect{X: 2, Y: 3, W: 40, H: 6}
	tui.paintShelfStrip(&buf, inner)
	out := buf.String()

	if !strings.HasPrefix(out, "\x1b[4;3H") {
		t.Fatalf("strip started at %q, want the box's first cell", out[:min(12, len(out))])
	}
	for _, want := range []string{rail.ShelfMark(set), "held", "waiting"} {
		if !strings.Contains(out, want) {
			t.Fatalf("strip = %q, want %q in it", out, want)
		}
	}
	blocked, _ := rail.RailGlyph(th, "blocked", set)
	working, _ := rail.RailGlyph(th, "working", set)
	if !strings.Contains(out, blocked) || !strings.Contains(out, working) {
		t.Fatalf("strip = %q, want the members' state glyphs %q and %q", out, blocked, working)
	}
	// The active chip is the first member, so its name rides the focus ground
	// and the bold weight the chip strip marks it with.
	active := theme.Style{
		Fg:    styleColor(th.Palette.RailText),
		Bg:    styleColor(th.Palette.RailFocusBG),
		Attrs: theme.AttrBold,
	}.SGR()
	if !strings.Contains(out, active) {
		t.Fatalf("strip = %q, want the active chip's focus ground and weight", out)
	}
	if i, j := strings.Index(out, "held"), strings.Index(out, "waiting"); i < 0 || j < i {
		t.Fatalf("strip names the members out of order: %q", out)
	}
	if cells.Cells(tui.shelf.float.Title) == 0 || !strings.Contains(tui.shelf.float.Title, "2") {
		t.Fatalf("title = %q, want it to name the count", tui.shelf.float.Title)
	}
}

// TestShelfBarChip proves the status bar gains one chip for a non-empty shelf:
// the shelf mark, the count and the worst member's state mark, at a cell span a
// click can find.
func TestShelfBarChip(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	th, set := tui.theme(), tui.glyphSet()
	badge := tui.barBadgeOf(th, set)
	segs := barSegments(th, tui.snap, tui.armed, tui.barInfo(), set, badge)
	var text strings.Builder
	for _, s := range segs {
		text.WriteString(s.Text)
	}
	worst, _ := rail.RailGlyph(th, "blocked", set)
	want := rail.ShelfMark(set) + "2 " + worst
	if !strings.Contains(text.String(), want) {
		t.Fatalf("bar = %q, want the shelf chip %q", text.String(), want)
	}

	lo, hi := tui.shelfChipSpan()
	if hi <= lo {
		t.Fatalf("shelf chip span = %d..%d, want a painted chip", lo, hi)
	}
	if !tui.shelfChipAt(lo) || tui.shelfChipAt(hi) {
		t.Fatalf("shelfChipAt disagrees with the span %d..%d", lo, hi)
	}

	// A left press on the chip summons the shelf.
	tui.handleMouse(mouseEvent{btn: 0, x: lo + 1, y: tui.snap.Rows})
	if tui.shelf == nil {
		t.Fatal("a click on the shelf chip did not summon the shelf")
	}
	drainSummon(t, tui, got)
}

// TestShelfBarHasNoChipWhenEmpty proves a session that never shelved a pane
// paints the bar it always did.
func TestShelfBarHasNoChipWhenEmpty(t *testing.T) {
	t.Parallel()
	tui, _ := switcherTUI(t)
	th, set := tui.theme(), tui.glyphSet()
	segs := barSegments(th, tui.snap, tui.armed, tui.barInfo(), set, tui.barBadgeOf(th, set))
	var text strings.Builder
	for _, s := range segs {
		text.WriteString(s.Text)
	}
	if strings.Contains(text.String(), rail.ShelfMark(set)) {
		t.Fatalf("an empty shelf painted a chip: %q", text.String())
	}
	if lo, hi := tui.shelfChipSpan(); hi != 0 || lo != 0 {
		t.Fatalf("empty shelf span = %d..%d, want none", lo, hi)
	}
}

// TestShelfSkipsShelvedPanesInTheDrawnSet proves a shelved pane is not painted
// as a pane and takes no click: its zero rect sits at the origin.
func TestShelfSkipsShelvedPanesInTheDrawnSet(t *testing.T) {
	t.Parallel()
	tui, _ := shelfTUI(t)
	panes := visiblePanes(tui.snap)
	for _, p := range panes {
		if p.Shelf {
			t.Fatalf("visiblePanes kept the shelved pane %s", p.ID)
		}
	}
	if len(panes) != 1 || panes[0].ID != "w1p1" {
		t.Fatalf("visiblePanes = %+v, want the live pane alone", panes)
	}
	if p := tui.paneAt(0, 0); p != nil {
		t.Fatalf("a click at the origin hit %s, want no pane", p.ID)
	}
	if p := tui.paneAtBox(0, 0); p != nil {
		t.Fatalf("a click at the origin hit box %s, want no pane", p.ID)
	}
}

// TestShelfScreenFillsTheFloat proves a screen the shelf asked for lands in the
// float's terminal at the float's size, and a screen for a member it no longer
// shows is dropped.
func TestShelfScreenFillsTheFloat(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	feed(tui, "\x02F")
	drainSummon(t, tui, got)

	term := tui.vts["w1p7"]
	term.Write([]byte("hello\nworld\n"))
	snap := term.Snapshot()
	// A screen for the member the shelf left is dropped.
	tui.applyShelfScreen("w1p8", snap)
	if tui.vts["w1p8"] != nil {
		t.Fatal("a stale shelf screen built a terminal for a member the shelf does not show")
	}
	tui.applyShelfScreen("w1p7", snap)
	if got := screenString(tui.vts["w1p7"]); !strings.Contains(got, "hello") {
		t.Fatalf("screen = %q, want the restored member screen", got)
	}
	if !tui.dirty {
		t.Fatal("a shelf screen did not mark the frame dirty")
	}
}

// TestRailShelfRowClickFocusesAndSummons proves the rail's shelf row does both
// jobs: it asks the server to move the shelf's active member, and it opens the
// float when it was closed.
func TestRailShelfRowClickFocusesAndSummons(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	row := -1
	for i, it := range rail.RailItems(tui.snap) {
		if it.Kind == rail.Shelf && it.Pane == "w1p8" {
			row = i
		}
	}
	if row < 0 {
		t.Fatalf("the rail has no shelf row for w1p8: %+v", rail.RailItems(tui.snap))
	}
	tui.handleMouse(mouseEvent{btn: 0, x: 6, y: row + 1})
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "focus" || m.Target != "w1p8" {
		t.Fatalf("a shelf row click sent %+v, want a focus request for w1p8", m)
	}
	if tui.shelf == nil {
		t.Fatal("a shelf row click left the shelf closed")
	}
}

// TestRailShelfHeaderFolds proves a click on the shelf header's caret sends the
// fold target the server answers.
func TestRailShelfHeaderFolds(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	row := -1
	for i, it := range rail.RailItems(tui.snap) {
		if it.Kind == rail.ShelfHeader {
			row = i
		}
	}
	if row < 0 {
		t.Fatal("the rail has no shelf header")
	}
	// The caret sits one cell right of the project bar, the column every
	// workspace and window caret owns.
	tui.handleMouse(mouseEvent{btn: 0, x: 2, y: row + 1})
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "toggle-fold" || m.Target != wire.ShelfFoldTarget {
		t.Fatalf("a header caret click sent %+v, want toggle-fold for the shelf", m)
	}
}

// TestRailShelfHeaderBodySummons proves a click on the header's body, the mark
// and the name beside the caret, opens the shelf float the way the status
// bar's chip does, so the mark the row paints answers a click.
func TestRailShelfHeaderBodySummons(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	row := -1
	var header rail.RailItem
	for i, it := range rail.RailItems(tui.snap) {
		if it.Kind == rail.ShelfHeader {
			row, header = i, it
		}
	}
	if row < 0 {
		t.Fatal("the rail has no shelf header")
	}
	// The mark's column follows the bar, the caret, the space and the
	// name, so the test presses the cell the frame paints it in.
	left, _, _ := rail.RailRow(tui.theme(), header, tui.snap.Rail-1, false, tui.glyphSet())
	if len(left) < 4 {
		t.Fatalf("the header composed %d runs, want the caret, the name and the mark", len(left))
	}
	mark := 1 + segment.SegCells(left[:3])
	tui.handleMouse(mouseEvent{btn: 0, x: mark + 1, y: row + 1})
	if tui.shelf == nil || tui.mode != "shelf" {
		t.Fatalf("a mark click left shelf %v in mode %q, want the float open", tui.shelf, tui.mode)
	}
	drainSummon(t, tui, got)

	// The space right of the caret is body too, and a second press closes
	// the float the first one opened.
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: row + 1})
	if tui.shelf != nil || tui.mode != "" {
		t.Fatalf("a body click left shelf %v in mode %q, want the float closed", tui.shelf, tui.mode)
	}
	testutil.NoMsg(t, got)
}

// TestShelfDrawsAFrame proves a session with a shelf paints: the rail's section
// header and the bar's chip reach the frame, and the open float paints its
// strip and the member's screen without a terminal of its own.
func TestShelfDrawsAFrame(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	tui.draw()
	frame := tui.out.(*bytes.Buffer).String()
	if !strings.Contains(frame, "shelf") {
		t.Fatal("the rail painted no shelf section")
	}
	if !strings.Contains(frame, rail.ShelfMark(tui.glyphSet())) {
		t.Fatal("the bar painted no shelf chip")
	}

	feed(tui, "\x02F")
	drainSummon(t, tui, got)
	tui.vts["w1p7"].Write([]byte("shelf screen\n"))
	tui.draw()
	frame = tui.out.(*bytes.Buffer).String()
	if !strings.Contains(frame, "held") || !strings.Contains(frame, "waiting") {
		t.Fatal("the shelf float painted no chips")
	}
	if !strings.Contains(frame, "shelf screen") {
		t.Fatal("the shelf float painted no member screen")
	}
}

// TestShelfChordWaitsForItsKey proves a prefix that arrives in its own read
// arms the chord instead of closing the shelf, and the key that follows still
// reads as that chord rather than as the member's input.
func TestShelfChordWaitsForItsKey(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	feed(tui, "\x02F")
	drainSummon(t, tui, got)

	feed(tui, "\x02")
	if tui.shelf == nil {
		t.Fatal("a prefix on its own closed the shelf")
	}
	testutil.NoMsg(t, got)

	feed(tui, "G")
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "pop-pane" || m.Target != "w1p7" {
		t.Fatalf("prefix then G sent %+v, want pop-pane for w1p7", m)
	}
}

// TestShelfChordSurvivesTheLonePrefixTimeout proves the shelf's prefix is a
// chord head, not a lone esc: a hand takes longer than escTimeout to press the
// second key, and that key must still read as a chord rather than as the
// member's input. The expiry handler that closes every other overlay must
// leave the shelf's wait alone.
func TestShelfChordSurvivesTheLonePrefixTimeout(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	feed(tui, "\x02F")
	drainSummon(t, tui, got)

	feed(tui, "\x02")
	tui.escAt = time.Now().Add(-escTimeout - time.Millisecond)
	if tui.expireMode(time.Now()) {
		t.Fatal("the shelf's lone prefix expired")
	}
	if len(tui.inbuf) != 1 {
		t.Fatalf("the buffer holds %q, want the prefix still in it", tui.inbuf)
	}
	testutil.NoMsg(t, got)

	feed(tui, "F")
	if tui.shelf != nil {
		t.Fatal("the chord key after the timeout did not close the shelf")
	}
	testutil.NoMsg(t, got)
}

// TestShelfRectModes proves the shelf's two placements: a centred tray takes
// a share of the pane area with equal gaps, the pane layout sits over the
// focused pane, a share under the floor is the floor, and a focused pane too
// small for the strip falls back to the centred box.
func TestShelfRectModes(t *testing.T) {
	t.Parallel()
	tui, _ := shelfTUI(t)
	area := layout.PaneAreaForWithTabBar(100, 30, 34)

	centered := shelfRectFor(tui.snap, true, 80)
	if want := (layout.Rect{X: 41, Y: 5, W: 51, H: 20}); centered != want {
		t.Fatalf("centred rect = %+v, want %+v", centered, want)
	}
	if l, r := centered.X-1-area.X, area.X+area.W-(centered.X+centered.W+1); l != r {
		t.Fatalf("side gaps = %d and %d, want equal", l, r)
	}
	if t1, b := centered.Y-1-area.Y, area.Y+area.H-(centered.Y+centered.H+1); t1 != b {
		t.Fatalf("vertical gaps = %d and %d, want equal", t1, b)
	}

	if got, want := shelfRectFor(tui.snap, false, 80), (layout.Rect{X: 35, Y: 2, W: 63, H: 25}); got != want {
		t.Fatalf("pane rect = %+v, want %+v", got, want)
	}

	if got, floor := shelfRectFor(tui.snap, true, 1), shelfRectFor(tui.snap, true, 20); got != floor {
		t.Fatalf("a share under the floor gave %+v, want the floor's %+v", got, floor)
	}
	if got, want := shelfRectFor(tui.snap, true, 100), (layout.Rect{X: 34, Y: 2, W: 65, H: 26}); got != want {
		t.Fatalf("a full share gave %+v, want %+v", got, want)
	}

	small := &wire.Snapshot{
		Cols: 100, Rows: 30, Rail: 34, Focus: "w1p1",
		Panes: []wire.PaneInfo{{ID: "w1p1", Rect: layout.Rect{X: 35, Y: 2, W: 10, H: 4}}},
	}
	if got := shelfRectFor(small, false, 80); got != centered {
		t.Fatalf("a small focused pane gave %+v, want the centred fallback %+v", got, centered)
	}
}

// TestShelfCursorFollowsTheMember proves the shelf places the block cursor on
// the active member's cell inside the float body. The shelf takes the pane's
// typing, so a hidden cursor leaves the operator with no insertion point in
// the box.
func TestShelfCursorFollowsTheMember(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	feed(tui, "\x02F")
	drainSummon(t, tui, got)

	term := tui.vts["w1p7"]
	if term == nil {
		t.Fatal("the summon built no terminal for the member")
	}
	term.Grid().SetCursor(3, 2)
	term.Grid().SetCursorVisible(true)
	r := tui.shelf.float.Rect
	want := fmt.Sprintf("\x1b[%d;%dH\x1b[?25h", r.Y+1+2+1, r.X+3+1)
	if got := tui.cursorBlock(); got != want {
		t.Fatalf("shelf cursor block = %q, want %q", got, want)
	}
	// The frame tail carries it, so the cursor the operator sees is the one
	// the last paint placed.
	buf := tui.out.(*bytes.Buffer)
	buf.Reset()
	tui.draw()
	if frame := buf.String(); !strings.Contains(frame, want) {
		t.Fatalf("frame does not place the shelf cursor: %q", frame[max(0, len(frame)-60):])
	}

	// A member with no visible cursor leaves the box with none.
	term.Grid().SetCursorVisible(false)
	if got := tui.cursorBlock(); got != "\x1b[?25l" {
		t.Fatalf("hidden member cursor block = %q, want the hide sequence", got)
	}
}

// TestShelfReloadsTheConfigWithTheBoxUp proves the config key still works with
// the shelf up: a size or placement change is exactly what the operator wants
// to see land, and reload refits the box.
func TestShelfReloadsTheConfigWithTheBoxUp(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	feed(tui, "\x02F")
	drainSummon(t, tui, got)

	feed(tui, "\x02\x12")
	select {
	case m := <-got:
		if m.Type != "req" || m.Action != "reload" {
			t.Fatalf("prefix ctrl+r sent %+v, want a reload request", m)
		}
	case <-time.After(200 * time.Millisecond):
		last := ""
		if n := len(tui.toasts); n > 0 {
			last = tui.toasts[n-1].text
		}
		t.Fatalf("nothing sent; inbuf=%q toast=%q mode=%q", tui.inbuf, last, tui.mode)
	}
	if tui.shelf == nil {
		t.Fatal("the reload closed the shelf")
	}
}

// TestShelfUnboundChordConsumesTheWholeKey proves an unbound chord spends its
// whole key: the tail of a two-byte chord must not reach the member as typed
// input.
func TestShelfUnboundChordConsumesTheWholeKey(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	feed(tui, "\x02F")
	drainSummon(t, tui, got)

	// The prefix then alt+x. The keymap matches the leading esc (dismiss-esc),
	// but the key as a whole has no binding, so the shelf has to spend both
	// bytes of it rather than run esc and hand the x to the member.
	feed(tui, "\x02\x1bx")
	if len(tui.toasts) == 0 {
		t.Fatal("an unbound chord raised no toast")
	}
	if last := tui.toasts[len(tui.toasts)-1].text; !strings.Contains(last, "no shelf binding") {
		t.Fatalf("toast = %q, want it to name the unbound chord", last)
	}
	if len(tui.inbuf) != 0 {
		t.Fatalf("the chord left %q in the buffer", tui.inbuf)
	}
	testutil.NoMsg(t, got)
}

// barLeftCells is the cells the status bar's left block paints at the current
// snapshot: the fitted block, elision included.
func barLeftCells(tui *tui) int {
	th, set := tui.theme(), tui.glyphSet()
	budget := barLeftBudget(th, tui.snap, set, tui.armed, tui.barInfo(), tui.barBadgeOf(th, set))
	return segment.SegCells(barLeftFor(th, tui.snap, set).fit(th, budget))
}

// TestShelfBarChipSpanClampsToThePaintedCells proves the chip's clickable span
// never reaches past the cells the bar painted: at a width where elision cuts
// the chip, only the painted cells summon it, and the cells elision gave up do
// nothing.
func TestShelfBarChipSpanClampsToThePaintedCells(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	// An unread badge pins cells on the right, so a narrow screen has to cut
	// the left block and the chip with it.
	tui.notifyRecent = []box.Entry{{Seq: 3, Key: "notify/w1p9", State: box.StateDone}}
	tui.snap.Cols = 120
	lo, hi := tui.shelfChipSpan()
	full := hi - lo
	if full < 2 {
		t.Fatalf("chip span at 120 cols = %d..%d, want a painted chip", lo, hi)
	}

	cut := 0
	for cols := 119; cols >= 8; cols-- {
		tui.snap.Cols = cols
		lo, hi := tui.shelfChipSpan()
		painted := barLeftCells(tui)
		if hi > painted {
			t.Fatalf("chip span %d..%d at %d cols reaches past the %d painted cells", lo, hi, cols, painted)
		}
		if cut == 0 && hi > lo && hi-lo < full {
			cut = cols
		}
	}
	if cut == 0 {
		t.Fatal("no width elided the chip in part, so the clamp is unproven")
	}

	tui.snap.Cols = cut
	lo, hi = tui.shelfChipSpan()
	if hi <= lo {
		t.Fatalf("chip span at %d cols = %d..%d, want the painted part of it", cut, lo, hi)
	}
	// The bar row owns the pointer, so a click past the painted chip must do
	// nothing at all.
	tui.handleMouse(mouseEvent{btn: 0, x: hi + 1, y: tui.snap.Rows})
	if tui.shelf != nil {
		t.Fatalf("a click at cell %d, past the painted chip %d..%d, summoned the shelf", hi, lo, hi)
	}
	testutil.NoMsg(t, got)
	tui.handleMouse(mouseEvent{btn: 0, x: hi, y: tui.snap.Rows})
	if tui.shelf == nil {
		t.Fatalf("a click at cell %d inside the painted chip %d..%d did not summon it", hi-1, lo, hi)
	}
}

// TestShelfMouseOutsideClosesTheFloat proves a press outside the float puts
// the shelf away and is spent on the float: the pane under it keeps its focus
// and no pane is popped or closed.
func TestShelfMouseOutsideClosesTheFloat(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	feed(tui, "\x02F")
	drainSummon(t, tui, got)

	area := layout.PaneAreaForWithTabBar(tui.snap.Cols, tui.snap.Rows, tui.snap.Rail)
	box := layout.BoxRect(tui.shelf.float.Rect, 1)
	if area.X >= box.X && area.X < box.X+box.W && area.Y >= box.Y && area.Y < box.Y+box.H {
		t.Fatalf("the pane area's first cell %d,%d sits inside the shelf box %+v", area.X, area.Y, box)
	}
	tui.handleMouse(mouseEvent{btn: 0, x: area.X + 1, y: area.Y + 1})
	if tui.shelf != nil || tui.mode != "" {
		t.Fatalf("a press outside left shelf %v in mode %q, want the float closed", tui.shelf, tui.mode)
	}
	if len(tui.floats) != 0 {
		t.Fatalf("floats = %d, want the shelf float removed", len(tui.floats))
	}
	if tui.press != nil || tui.selText != nil {
		t.Fatal("the dismissing press started a selection on the pane under the shelf")
	}
	if members := rail.ShelfPanes(tui.snap); len(members) != 2 {
		t.Fatalf("shelf members = %d, want the two panes kept", len(members))
	}
	// The release that follows belongs to nobody: nothing reaches a pane.
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: area.X + 1, y: area.Y + 1})
	testutil.NoMsg(t, got)
}

// TestShelfChipClickFocusesTheMember proves a click on a chip switches the
// member the shelf shows, at the cell span the strip paints it in.
func TestShelfChipClickFocusesTheMember(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	feed(tui, "\x02F")
	drainSummon(t, tui, got)

	segs, chips := tui.shelfStrip()
	if len(chips) != 2 {
		t.Fatalf("chips = %+v, want one per member", chips)
	}
	r := tui.shelf.float.Rect
	for _, c := range chips {
		if c.hi > segment.SegCells(segs) || c.lo >= c.hi {
			t.Fatalf("chip %+v reaches outside the %d painted cells", c, segment.SegCells(segs))
		}
		if got := tui.shelfChipHit(r.X+c.lo, r.Y); got != c.id {
			t.Fatalf("shelfChipHit at %d = %q, want %q", r.X+c.lo, got, c.id)
		}
		if got := tui.shelfChipHit(r.X+c.hi-1, r.Y); got != c.id {
			t.Fatalf("shelfChipHit at the last cell %d = %q, want %q", r.X+c.hi-1, got, c.id)
		}
	}
	if got := tui.shelfChipHit(r.X, r.Y+1); got != "" {
		t.Fatalf("shelfChipHit one row under the strip = %q, want no chip", got)
	}

	second := chips[1]
	tui.handleMouse(mouseEvent{btn: 0, x: r.X + second.lo + 1, y: r.Y + 1})
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "focus" || m.Target != "w1p8" {
		t.Fatalf("a chip click sent %+v, want a focus request for w1p8", m)
	}
	if tui.shelf == nil || tui.mode != "shelf" {
		t.Fatalf("a chip click left shelf %v in mode %q, want the float open", tui.shelf, tui.mode)
	}
}

// TestShelfDragCopies proves a left press inside the shelf's screen selects the
// member's text and a release copies it, the way a drag inside a pane does.
func TestShelfDragCopies(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	feed(tui, "\x02F")
	drainSummon(t, tui, got)

	body := tui.shelfBody()
	if body.W < 6 || body.H < 1 {
		t.Fatalf("shelf body = %+v, want room for a drag", body)
	}
	tui.vts["w1p7"] = testutil.TestVT(t, body.W, body.H, "hello world")
	oscOut := testutil.CaptureStdout(t, func() {
		tui.handleMouse(mouseEvent{
			btn: 0, x: body.X + 1, y: body.Y + 1,
			raw: []byte("\x1b[<0;1;1M"),
		})
		tui.handleMouse(mouseEvent{
			btn: 32, x: body.X + 5, y: body.Y + 1,
			raw: []byte("\x1b[<32;5;1M"),
		})
		tui.handleMouse(mouseEvent{
			btn: 0, release: true, x: body.X + 5, y: body.Y + 1,
			raw: []byte("\x1b[<0;5;1m"),
		})
	})
	testutil.NoMsg(t, got)
	if len(tui.toasts) != 1 || tui.toasts[0].text != "copied 5 chars" {
		t.Fatalf("toasts = %+v, want copied 5 chars", tui.toasts)
	}
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("hello")) + "\x1b\\"
	if !strings.Contains(oscOut, want) {
		t.Fatalf("clipboard payload missing from %q", oscOut)
	}
	if tui.selText != nil || tui.press != nil {
		t.Fatal("the drag left selection state behind")
	}
}

// TestShelfRightPressOpensTheShelfMenu proves a right press inside the shelf
// offers the shelf's own actions, and that closing that menu hands the keyboard
// back to the float.
func TestShelfRightPressOpensTheShelfMenu(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	feed(tui, "\x02F")
	drainSummon(t, tui, got)

	body := tui.shelfBody()
	tui.handleMouse(mouseEvent{btn: 2, x: body.X + 1, y: body.Y + 1})
	if tui.menu == nil || tui.mode != "menu" {
		t.Fatalf("a right press left menu %v in mode %q, want the shelf menu open", tui.menu, tui.mode)
	}
	if tui.menu.title != "shelf" {
		t.Fatalf("menu title = %q, want shelf", tui.menu.title)
	}
	for _, it := range tui.menu.items {
		if it.action == "split-right" || it.action == "scrollback" {
			t.Fatalf("the shelf menu offers %q, which a shelved pane cannot take", it.action)
		}
	}
	// A press inside the float but off the menu dismisses the menu and leaves
	// the shelf up with the keyboard.
	corner := layout.Rect{X: body.X + body.W - 1, Y: body.Y + body.H - 1}
	m := tui.menu
	if corner.X < m.x+m.w && corner.Y < m.y+len(m.items)+2 {
		t.Fatalf("the corner %+v sits inside the menu", corner)
	}
	tui.handleMouse(mouseEvent{btn: 0, x: corner.X + 1, y: corner.Y + 1})
	if tui.menu != nil {
		t.Fatal("a press outside the menu left it open")
	}
	if tui.shelf == nil || tui.mode != "shelf" {
		t.Fatalf("closing the shelf menu left shelf %v in mode %q", tui.shelf, tui.mode)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: corner.X + 1, y: corner.Y + 1})
	testutil.NoMsg(t, got)
}

// TestShelfMouseBytesReachTheShelf proves the input loop hands a pointer event
// in shelf mode to the shelf: a chip press in an SGR sequence switches the
// member, and a press outside the float puts it away.
func TestShelfMouseBytesReachTheShelf(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	feed(tui, "\x02F")
	drainSummon(t, tui, got)

	r := tui.shelf.float.Rect
	_, chips := tui.shelfStrip()
	if len(chips) < 2 {
		t.Fatalf("chips = %+v, want one per member", chips)
	}
	feed(tui, fmt.Sprintf("\x1b[<0;%d;%dM", r.X+chips[1].lo+1, r.Y+1))
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "focus" || m.Target != "w1p8" {
		t.Fatalf("a chip press sent %+v, want a focus request for w1p8", m)
	}
	feed(tui, "\x1b[<0;1;1M")
	if tui.shelf != nil || tui.mode != "" {
		t.Fatalf("a press at the screen's first cell left shelf %v in mode %q", tui.shelf, tui.mode)
	}
	testutil.NoMsg(t, got)
}
