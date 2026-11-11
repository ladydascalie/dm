package client

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/testutil"
	"dm/internal/vt"
	"dm/internal/wire"
)

// scrollbackTUI is a one-pane client whose pane has a replay ring of rows
// lines, cached for the pane's current rect. The live screen shows the last
// rows of the ring, the way a real pane's screen does.
func scrollbackTUI(t *testing.T, rows int) (*tui, *wire.PaneInfo) {
	t.Helper()
	tui, _ := clientTUI(t, nil)
	p := tui.paneByID("a")
	var ring strings.Builder
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&ring, "ROW-%03d\r\n", i)
	}
	tui.hist = map[string][]byte{"a": []byte(ring.String())}
	tui.histMeta = map[string]histCache{"a": {at: p.Rect, got: true}}
	live := vt.NewTerm(p.Rect.W, p.Rect.H)
	live.Write([]byte(ring.String()))
	live.Flush()
	tui.vts["a"] = live
	return tui, p
}

// pressDrag starts a selection at the pane cell (x0,y0) and drags it to
// (x1,y1), both counted from the pane's top-left.
func pressDrag(tui *tui, p *wire.PaneInfo, x0, y0, x1, y1 int) {
	tui.handleMouse(mouseEvent{
		btn: 0, x: p.Rect.X + x0 + 1, y: p.Rect.Y + y0 + 1,
		raw: []byte("\x1b[<0;1;1M"),
	})
	tui.handleMouse(mouseEvent{
		btn: 32, x: p.Rect.X + x1 + 1, y: p.Rect.Y + y1 + 1,
		raw: []byte("\x1b[<32;1;1M"),
	})
}

// wheel turns the wheel over the pane. up is the wheel-up button.
func wheel(tui *tui, p *wire.PaneInfo, up bool) {
	btn := 64
	if !up {
		btn = 65
	}
	tui.handleMouse(mouseEvent{btn: btn, x: p.Rect.X + 1, y: p.Rect.Y + 1})
}

// release ends the drag at the pane cell (x,y) and returns what it copied.
func release(t *testing.T, tui *tui, p *wire.PaneInfo, x, y int) string {
	t.Helper()
	return testutil.CaptureStdout(t, func() {
		tui.handleMouse(mouseEvent{
			btn: 0, release: true, x: p.Rect.X + x + 1, y: p.Rect.Y + y + 1,
			raw: []byte("\x1b[<0;1;1m"),
		})
	})
}

// A wheel turn while the left button is held scrolls the pane's scrollback and
// moves the focus with the rows under the pointer, so the release copies the
// scrolled rows and not the live screen's rows at the same coordinates.
func TestWheelDuringDragCopiesScrolledRows(t *testing.T) {
	tui, p := scrollbackTUI(t, 100)
	pressDrag(tui, p, 6, 0, 0, 0)
	wheel(tui, p, true)
	if tui.scroll != "a" {
		t.Fatalf("the wheel did not open scrollback: scroll=%q", tui.scroll)
	}
	if tui.offset != 3 {
		t.Fatalf("wheel offset = %d, want 3", tui.offset)
	}
	oscOut := release(t, tui, p, 0, 0)
	if len(tui.toasts) != 1 {
		t.Fatalf("toasts = %+v, want one", tui.toasts)
	}
	want := "ROW-071\nROW-072\nROW-073\nROW-074"
	if !strings.Contains(oscOut, osc52(want)) {
		t.Fatalf("clipboard = %q, want %q", oscOut, want)
	}
	if tui.toasts[0].text != fmt.Sprintf("copied %d chars", len(want)) {
		t.Fatalf("toast = %q, want copied %d chars", tui.toasts[0].text, len(want))
	}
}

// A wheel turn past the pane's top edge keeps selecting older rows: the
// pointer clamps at the edge, but the rows under it keep moving.
func TestWheelPastTheTopEdgeKeepsExtending(t *testing.T) {
	tui, p := scrollbackTUI(t, 100)
	pressDrag(tui, p, 6, 0, 0, 0)
	// The pointer leaves the pane above its top edge, then the wheel turns.
	tui.handleMouse(mouseEvent{btn: 32, x: p.Rect.X + 1, y: p.Rect.Y - 5, raw: []byte("\x1b[<32;1;1M")})
	wheel(tui, p, true)
	wheel(tui, p, true)
	if tui.offset != 6 {
		t.Fatalf("two wheel turns moved to %d, want 6", tui.offset)
	}
	oscOut := release(t, tui, p, 0, 0)
	want := "ROW-068\nROW-069\nROW-070\nROW-071\nROW-072\nROW-073\nROW-074"
	if !strings.Contains(oscOut, osc52(want)) {
		t.Fatalf("clipboard = %q, want %q", oscOut, want)
	}
}

// A selection made over a scrolled pane is painted on the scrollback rows the
// window shows, not on the live screen behind it.
func TestSelectionPaintsScrollbackRows(t *testing.T) {
	t.Parallel()
	tui, p := scrollbackTUI(t, 100)
	out := &bytes.Buffer{}
	tui.out = out
	tui.startScrollOnFocus()
	if tui.scrollVT == nil || tui.offset != 1 {
		t.Fatalf("scrollback did not open: vt=%v offset=%d", tui.scrollVT != nil, tui.offset)
	}
	pressDrag(tui, p, 0, 0, 6, 1)
	out.Reset()
	tui.draw()
	term := vt.NewTerm(tui.snap.Cols, tui.snap.Rows)
	term.Write(out.Bytes())
	term.Flush()
	g := term.Grid()
	rev := func(x, y int) bool {
		return g.Cell(p.Rect.X+x, p.Rect.Y+y).Style.Attrs&vt.Reverse != 0
	}
	for x := 0; x < p.Rect.W; x++ {
		if !rev(x, 0) {
			t.Fatalf("scrollback row 0 col %d is not selected", x)
		}
	}
	for x := 0; x <= 6; x++ {
		if !rev(x, 1) {
			t.Fatalf("scrollback row 1 col %d is not selected", x)
		}
	}
	if rev(7, 1) {
		t.Fatal("the selection painted past its focus")
	}
	if rev(0, 2) {
		t.Fatal("the selection painted a row it does not cover")
	}
}

// A selection made in scrollback copies the scrollback rows, and leaving
// scrollback drops it.
func TestScrollbackSelectionCopiesAndLeaves(t *testing.T) {
	tui, p := scrollbackTUI(t, 100)
	tui.startScrollOnFocus()
	pressDrag(tui, p, 6, 0, 0, 0)
	oscOut := release(t, tui, p, 0, 0)
	want := "ROW-073"
	if !strings.Contains(oscOut, osc52(want)) {
		t.Fatalf("clipboard = %q, want %q", oscOut, want)
	}

	// A second selection is dropped when scrollback closes.
	pressDrag(tui, p, 0, 0, 6, 0)
	if tui.selText == nil {
		t.Fatal("the second drag did not select")
	}
	tui.exitScroll()
	if tui.selText != nil || tui.press != nil {
		t.Fatal("leaving scrollback kept the selection")
	}
}

// A geometry change drops a selection anchored in scrollback, the same way it
// drops one anchored in the live screen.
func TestScrollbackSelectionClearsOnResize(t *testing.T) {
	t.Parallel()
	tui, p := scrollbackTUI(t, 100)
	tui.startScrollOnFocus()
	pressDrag(tui, p, 0, 0, 6, 0)
	if tui.selText == nil {
		t.Fatal("the drag did not select")
	}
	row := &layout.Node{Pane: "a"}
	resized := testutil.TestSnapshot(row, 90, 30, nil)
	resized.Focus = "a"
	tui.apply(wire.Msg{Type: "snapshot", Snap: resized})
	if tui.selText != nil || tui.press != nil {
		t.Fatal("the selection survived the resize")
	}
}

// An alternate-screen pane has no scrollback: the wheel keeps the focus
// clamped and says why.
func TestWheelOnAlternateScreenKeepsTheClamp(t *testing.T) {
	t.Parallel()
	tui, p := scrollbackTUI(t, 100)
	p.Alt = true
	pressDrag(tui, p, 0, 0, 6, 0)
	before := tui.selText.focus
	wheel(tui, p, true)
	if tui.scroll != "" {
		t.Fatalf("an alternate-screen pane opened scrollback: %q", tui.scroll)
	}
	if tui.selText == nil || tui.selText.focus != before {
		t.Fatalf("the wheel moved the focus: %+v", tui.selText)
	}
	if len(tui.toasts) != 1 || !strings.Contains(tui.toasts[0].text, "alternate screen") {
		t.Fatalf("toasts = %+v, want the alternate-screen toast", tui.toasts)
	}
}

// New output arriving below a scrollback selection does not move it: the
// scrollback is frozen, so the selection is anchored to the rows it covers.
func TestNewOutputKeepsScrollbackSelection(t *testing.T) {
	tui, p := scrollbackTUI(t, 100)
	tui.startScrollOnFocus()
	pressDrag(tui, p, 6, 0, 0, 0)
	anchor, focus := tui.selText.anchor, tui.selText.focus
	// New output lands on the live screen behind the frozen scrollback.
	tui.apply(wire.Msg{Type: "bytes", Target: "a", Data: []byte("\r\nNEW-OUTPUT")})
	if tui.selText == nil || tui.selText.anchor != anchor || tui.selText.focus != focus {
		t.Fatalf("new output moved the selection: %+v", tui.selText)
	}
	oscOut := release(t, tui, p, 0, 0)
	if !strings.Contains(oscOut, osc52("ROW-073")) {
		t.Fatalf("clipboard = %q, want the same scrollback row", oscOut)
	}
}

// A wheel turn during a drag before the ring lands is applied when it does:
// the selection moves to the tall terminal and the turns are not lost.
func TestWheelDuringDragWaitsForTheRing(t *testing.T) {
	tui, _ := clientTUI(t, nil)
	p := tui.paneByID("a")
	pressDrag(tui, p, 6, 0, 0, 0)
	wheel(tui, p, true)
	if tui.scroll != "a" || tui.scrollVT != nil {
		t.Fatalf("the wheel did not wait for the ring: scroll=%q vt=%v", tui.scroll, tui.scrollVT != nil)
	}
	if tui.selText == nil || tui.selText.pending != 3 {
		t.Fatalf("pending = %+v, want 3", tui.selText)
	}
	var ring strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&ring, "ROW-%03d\r\n", i)
	}
	tui.apply(wire.Msg{Type: "replay", Target: "a", Data: []byte(ring.String())})
	if tui.scrollVT == nil {
		t.Fatal("the ring did not build the scrollback")
	}
	if tui.selText == nil || tui.selText.src != selScroll {
		t.Fatalf("the selection did not move to scrollback: %+v", tui.selText)
	}
	if tui.offset != 3 {
		t.Fatalf("offset = %d, want 3", tui.offset)
	}
	oscOut := release(t, tui, p, 0, 0)
	want := "ROW-071\nROW-072\nROW-073\nROW-074"
	if !strings.Contains(oscOut, osc52(want)) {
		t.Fatalf("clipboard = %q, want %q", oscOut, want)
	}
}

// A scroll selection belongs to its pane. Opening another pane's scrollback
// keeps it but does not paint it on the live screen, and leaving that pane
// does not drop it.
func TestScrollSelectionBelongsToItsPane(t *testing.T) {
	t.Parallel()
	tui, a, _ := twoPaneScrollbackTUI(t, 100)
	tui.startScrollOnFocus()
	pressDrag(tui, a, 6, 0, 0, 0)
	if tui.selText == nil || tui.selText.pane != "a" || tui.selText.src != selScroll {
		t.Fatalf("selection = %+v, want a scroll selection on a", tui.selText)
	}

	// Open b's scrollback. a's selection is kept but a shows its live screen,
	// so the selection has no rows to mark there.
	tui.snap.Focus = "b"
	tui.startScrollOnFocus()
	if tui.scroll != "b" {
		t.Fatalf("scroll = %q, want b", tui.scroll)
	}
	if tui.selText == nil || tui.selText.pane != "a" {
		t.Fatalf("opening b dropped a's selection: %+v", tui.selText)
	}
	if got := paneSel(tui, "a"); got != nil {
		t.Fatalf("a's live screen painted the scroll selection: %+v", got)
	}

	// Leaving b's scrollback keeps a's selection.
	tui.exitScroll()
	if tui.selText == nil || tui.selText.pane != "a" {
		t.Fatalf("leaving b dropped a's selection: %+v", tui.selText)
	}

	// Coming back to a paints it again.
	tui.snap.Focus = "a"
	tui.startScrollOnFocus()
	if tui.scroll != "a" {
		t.Fatalf("scroll = %q, want a", tui.scroll)
	}
	if got := paneSel(tui, "a"); got == nil {
		t.Fatal("a's selection was not painted after coming back")
	}
}

// paneSel is the selection range a pane's paint carries, nil when none.
func paneSel(tui *tui, id string) *selRange {
	for _, pp := range tui.panePaints(onScreenPanes(tui.snap)) {
		if pp.id == id {
			return pp.sel
		}
	}
	return nil
}

// twoPaneScrollbackTUI is a two-pane client with a replay ring cached for each
// pane, so either pane can open scrollback without a request.
func twoPaneScrollbackTUI(t *testing.T, rows int) (*tui, *wire.PaneInfo, *wire.PaneInfo) {
	t.Helper()
	tui, _ := clientTUI(t, nil)
	root := &layout.Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*layout.Node{{Pane: "a"}, {Pane: "b"}},
	}
	tui.snap = testutil.TestSnapshot(root, 100, 30, nil)
	tui.snap.Focus = "a"
	tui.hist = map[string][]byte{}
	tui.histMeta = map[string]histCache{}
	a, b := tui.paneByID("a"), tui.paneByID("b")
	for _, p := range []*wire.PaneInfo{a, b} {
		var ring strings.Builder
		for i := 0; i < rows; i++ {
			fmt.Fprintf(&ring, "ROW-%03d\r\n", i)
		}
		tui.hist[p.ID] = []byte(ring.String())
		tui.histMeta[p.ID] = histCache{at: p.Rect, got: true}
		live := vt.NewTerm(p.Rect.W, p.Rect.H)
		live.Write([]byte(ring.String()))
		live.Flush()
		tui.vts[p.ID] = live
	}
	return tui, a, b
}

// New output that scrolls the live screen drops a live selection, because the
// text under it has moved.
func TestNewOutputDropsLiveSelectionOnScroll(t *testing.T) {
	t.Parallel()
	tui, _ := clientTUI(t, nil)
	p := tui.paneByID("a")
	tui.vts["a"] = testutil.TestVT(t, p.Rect.W, p.Rect.H, "")
	pressDrag(tui, p, 0, 0, 6, 0)
	if tui.selText == nil || tui.selText.src != selLive {
		t.Fatalf("the drag did not make a live selection: %+v", tui.selText)
	}
	var b strings.Builder
	for i := 0; i < p.Rect.H+2; i++ {
		fmt.Fprintf(&b, "line-%d\r\n", i)
	}
	tui.apply(wire.Msg{Type: "bytes", Target: "a", Data: []byte(b.String())})
	if tui.selText != nil {
		t.Fatalf("a scrolling live screen kept the selection: %+v", tui.selText)
	}
}

// New output that does not scroll the live screen leaves a live selection
// alone.
func TestNewOutputKeepsLiveSelectionWithoutScroll(t *testing.T) {
	t.Parallel()
	tui, _ := clientTUI(t, nil)
	p := tui.paneByID("a")
	tui.vts["a"] = testutil.TestVT(t, p.Rect.W, p.Rect.H, "")
	pressDrag(tui, p, 0, 0, 6, 0)
	anchor, focus := tui.selText.anchor, tui.selText.focus
	tui.apply(wire.Msg{Type: "bytes", Target: "a", Data: []byte("hello")})
	if tui.selText == nil || tui.selText.anchor != anchor || tui.selText.focus != focus {
		t.Fatalf("output without a scroll moved the selection: %+v", tui.selText)
	}
}

// A wheel down before the ring lands leaves scrollback instead of sitting in
// scroll mode on the live row, and keeps the live selection.
func TestWheelDownBeforeRingStaysLive(t *testing.T) {
	t.Parallel()
	tui, _ := clientTUI(t, nil)
	p := tui.paneByID("a")
	pressDrag(tui, p, 6, 0, 0, 0)
	wheel(tui, p, false)
	if tui.selText == nil || tui.selText.pending != -3 {
		t.Fatalf("pending = %+v, want -3", tui.selText)
	}
	var ring strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&ring, "ROW-%03d\r\n", i)
	}
	tui.apply(wire.Msg{Type: "replay", Target: "a", Data: []byte(ring.String())})
	if tui.scroll != "" || tui.scrollVT != nil || tui.mode == "scroll" {
		t.Fatalf("wheel down left scrollback open: scroll=%q vt=%v mode=%q",
			tui.scroll, tui.scrollVT != nil, tui.mode)
	}
	if tui.selText == nil || tui.selText.src != selLive {
		t.Fatalf("the live selection was not kept: %+v", tui.selText)
	}
}
