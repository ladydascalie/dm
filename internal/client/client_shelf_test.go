package client

import (
	"bytes"
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
	r := shelfRect(tui.snap)
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

// TestShelfPrefixNPMovesTheMember proves prefix n and prefix p ask the server
// for opposite steps: two members, active 0, n names index 1 and p names index
// 0. The server's snapshot answers each request, so the second move starts from
// where the first landed.
func TestShelfPrefixNPMovesTheMember(t *testing.T) {
	t.Parallel()
	tui, got := shelfTUI(t)
	feed(tui, "\x02F")
	drainSummon(t, tui, got)
	if at := shelfActiveIndex(tui.snap, rail.ShelfPanes(tui.snap)); at != 0 {
		t.Fatalf("shelf starts on member %d, want 0", at)
	}

	feed(tui, "\x02n")
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "focus" || m.Target != "w1p8" {
		t.Fatalf("prefix n sent %+v, want a focus request for w1p8", m)
	}
	// The server answers with the new active index, the way the shelf's own
	// reply snapshot does.
	tui.snap.ShelfActive = 1
	feed(tui, "\x02p")
	m = nextMsg(t, got)
	if m.Type != "req" || m.Action != "focus" || m.Target != "w1p7" {
		t.Fatalf("prefix p sent %+v, want a focus request back to w1p7", m)
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
	budget := barLeftBudget(th, tui.snap, tui.armed, tui.barInfo(), tui.barBadgeOf(th, set))
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
