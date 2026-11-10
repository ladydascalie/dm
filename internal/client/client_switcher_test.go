package client

import (
	"bytes"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"dm/internal/layout"
	"dm/internal/switcher"
	"dm/internal/testutil"
	"dm/internal/transport"
	"dm/internal/vt"
	"dm/internal/wire"
)

// switcherCell reads one cell of a replayed screen. The switcher's own test
// moved to internal/switcher, so the root tests keep their own copy.
func switcherCell(screen *vt.Term, x, y int) rune {
	r, _ := utf8.DecodeRuneInString(screen.Grid().Cell(x, y).Content)
	return r
}

// switcherTUI is a client over a real net.Pipe: keys go in through processInput,
// and every request it sends lands on the returned channel. The snapshot has one
// workspace and one window over two panes, so the dashboard has two rows to walk
// and a pane to preview.
func switcherTUI(t *testing.T) (*tui, chan wire.Msg) {
	t.Helper()
	root := &layout.Node{Pane: "w1p1"}
	snap := &wire.Snapshot{Cols: 100, Rows: 30, Rail: 34, Root: root, Focus: "w1p1"}
	rects := map[string]layout.Rect{}
	layout.Layout(root, layout.PaneAreaFor(100, 30, 34), testutil.Pad(), rects)
	snap.Panes = []wire.PaneInfo{{ID: "w1p1", Rect: rects["w1p1"]}}
	snap.All = []wire.AllPane{
		{
			ID: "w1p1", Name: "alpha", Workspace: "s1", WorkspaceName: "main",
			Window: "w1", WindowName: "one", Focus: true, WorkspaceActive: true,
			WindowActive: true, WindowUnfolded: true, WorkspaceUnfolded: true,
		},
		{
			ID: "w1p2", Name: "beta", Workspace: "s1", WorkspaceName: "main",
			Window: "w1", WindowName: "one", WindowUnfolded: true, WorkspaceUnfolded: true,
		},
	}
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

// feed hands bytes to processInput the way a read would.
func feed(t *tui, b string) {
	t.inbuf = append(t.inbuf, b...)
	t.processInput()
}

// TestSwitcherPrefixToggles: prefix a opens the float and a second prefix press
// closes it. Neither sends a request.
func TestSwitcherPrefixToggles(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	if tui.sw == nil {
		t.Fatal("prefix a did not open the switcher")
	}
	feed(tui, "\x02")
	if tui.sw != nil {
		t.Fatal("a second prefix press did not close the switcher")
	}
	testutil.NoMsg(t, got)
}

// TestSwitcherCtrlNPMoves: the contract binds down/up and ctrl+n/p to the same
// walk through selectable rows. The arrows are covered by the move tests; this
// holds the ctrl keys to the same step.
func TestSwitcherCtrlNPMoves(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	if it, ok := tui.sw.Selected(); !ok || it.Pane != "w1p1" {
		t.Fatalf("empty query selects %+v, want the first pane w1p1", it)
	}
	feed(tui, "\x0e") // ctrl+n, down
	if it, ok := tui.sw.Selected(); !ok || it.Pane != "w1p2" {
		t.Fatalf("ctrl+n selects %+v, want the next pane w1p2", it)
	}
	feed(tui, "\x10") // ctrl+p, up
	if it, ok := tui.sw.Selected(); !ok || it.Pane != "w1p1" {
		t.Fatalf("ctrl+p selects %+v, want the pane above w1p1", it)
	}
	testutil.NoMsg(t, got)
}

// TestSwitcherTypeAndEnter: typing narrows the rows and enter sends a focus
// request for the selected pane, then closes the float.
func TestSwitcherTypeAndEnter(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	full := tui.sw.RowCount()
	feed(tui, "beta")
	if n := tui.sw.RowCount(); n >= full {
		t.Fatalf("typing narrowed %d rows to %d", full, n)
	}
	if it, ok := tui.sw.Selected(); !ok || it.Pane != "w1p2" {
		t.Fatalf("selected = %+v, want the beta pane", it)
	}
	feed(tui, "\r")
	if tui.sw != nil {
		t.Fatal("enter left the switcher open")
	}
	select {
	case m := <-got:
		if m.Type != "req" || m.Action != "focus" || m.Target != "w1p2" {
			t.Fatalf("enter sent %+v, want a focus request for w1p2", m)
		}
	case <-time.After(time.Second):
		t.Fatal("enter sent nothing")
	}
	testutil.NoMsg(t, got)
}

// TestSwitcherEscSendsNothing: esc closes the float without a request.
func TestSwitcherEscSendsNothing(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	feed(tui, "\x1b")
	if !tui.pending {
		t.Fatal("esc did not wait for the pending timer")
	}
	tui.escAt = time.Now().Add(-time.Second)
	tui.expireSwitcher(time.Now())
	if tui.sw != nil {
		t.Fatal("the pending esc did not close the switcher")
	}
	testutil.NoMsg(t, got)
}

// TestSwitcherCtrlLRestores: ctrl+l clears the query and brings the rows back.
func TestSwitcherCtrlLRestores(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	full := tui.sw.RowCount()
	feed(tui, "beta")
	if tui.sw.RowCount() == full {
		t.Fatal("typing dropped no rows")
	}
	feed(tui, "\x0c")
	if n := tui.sw.RowCount(); n != full {
		t.Fatalf("ctrl+l left %d rows, want %d", n, full)
	}
	if q := tui.sw.Query(); q != "" {
		t.Fatalf("ctrl+l left the query %q", q)
	}
	testutil.NoMsg(t, got)
}

// TestSwitcherFullScreen: prefix a opens the dashboard over the whole screen in
// its own mode, and the close key gives the screen back.
func TestSwitcherFullScreen(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	if tui.sw == nil {
		t.Fatal("prefix a did not open the dashboard")
	}
	if r := tui.sw.Rect; r != (layout.Rect{W: 100, H: 30}) {
		t.Fatalf("dashboard rect = %+v, want the whole 100x30 screen", r)
	}
	if tui.mode != "dashboard" {
		t.Fatalf("mode = %q, want dashboard", tui.mode)
	}
	feed(tui, "\x02")
	if tui.sw != nil || tui.mode != "" {
		t.Fatalf("close left sw %v and mode %q", tui.sw, tui.mode)
	}
	testutil.NoMsg(t, got)
}

// TestSwitcherSortKey: s cycles the ordering recent, project, status.
func TestSwitcherSortKey(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	if m := tui.sw.SortMode(); m != switcher.SortRecent {
		t.Fatalf("default sort = %v, want recent", m)
	}
	feed(tui, "s")
	if m := tui.sw.SortMode(); m != switcher.SortProject {
		t.Fatalf("s landed on %v, want project", m)
	}
	feed(tui, "s")
	if m := tui.sw.SortMode(); m != switcher.SortStatus {
		t.Fatalf("a second s landed on %v, want status", m)
	}
	feed(tui, "s")
	if m := tui.sw.SortMode(); m != switcher.SortRecent {
		t.Fatalf("a third s landed on %v, want recent", m)
	}
	testutil.NoMsg(t, got)
}

// TestSwitcherPreviewKeys: ctrl+u and ctrl+d scroll the preview, +/- resize it
// and p hides it.
func TestSwitcherPreviewKeys(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = "line"
	}
	tui.sw.SetPreview(1, "w1p1")
	if !tui.sw.AcceptPreview(1, lines) {
		t.Fatal("the preview reply was refused")
	}
	feed(tui, "\x15") // ctrl+u, up
	if n := tui.sw.PreviewOffset(); n != 3 {
		t.Fatalf("ctrl+u moved the offset to %d, want 3", n)
	}
	feed(tui, "\x15")
	if n := tui.sw.PreviewOffset(); n != 6 {
		t.Fatalf("a second ctrl+u moved the offset to %d, want 6", n)
	}
	feed(tui, "\x04") // ctrl+d, down
	if n := tui.sw.PreviewOffset(); n != 3 {
		t.Fatalf("ctrl+d moved the offset to %d, want 3", n)
	}
	feed(tui, "+")
	if n := tui.sw.PreviewRatio(); n != 65 {
		t.Fatalf("+ took the ratio to %d, want 65", n)
	}
	feed(tui, "-")
	if n := tui.sw.PreviewRatio(); n != 60 {
		t.Fatalf("- took the ratio to %d, want 60", n)
	}
	for i := 0; i < 20; i++ {
		feed(tui, "-")
	}
	if n := tui.sw.PreviewRatio(); n != 20 {
		t.Fatalf("the ratio stopped at %d, want the 20 floor", n)
	}
	if !tui.sw.PreviewOn() {
		t.Fatal("the preview is off before the peek key")
	}
	feed(tui, "p")
	if tui.sw.PreviewOn() {
		t.Fatal("p did not hide the preview")
	}
	testutil.NoMsg(t, got)
}

// TestSwitcherInputForwards: i sends every key to the selected pane, esc leaves
// input mode without closing the dashboard, and the query stays untouched.
func TestSwitcherInputForwards(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	feed(tui, "i")
	if !tui.swInput {
		t.Fatal("i did not enter input mode")
	}
	if q := tui.sw.Query(); q != "" {
		t.Fatalf("input mode edited the query: %q", q)
	}
	feed(tui, "x")
	select {
	case m := <-got:
		if m.Type != "keys" || m.Target != "w1p1" || string(m.Keys) != "x" {
			t.Fatalf("input mode sent %+v, want keys x to w1p1", m)
		}
	case <-time.After(time.Second):
		t.Fatal("input mode forwarded nothing")
	}
	// The key also asks for the selected pane's screen, so its answer appears
	// without waiting for the preview cadence.
	if req := screenRequest(t, got); req.Target != "w1p1" {
		t.Fatalf("screen request = %+v, want w1p1", req)
	}
	// A lone esc waits for the rest of a possible sequence; the pending timer
	// then leaves input mode and closes nothing.
	feed(tui, "\x1b")
	if !tui.pending {
		t.Fatal("a bare esc did not wait for the rest of a sequence")
	}
	tui.escAt = time.Now().Add(-time.Second)
	tui.expireSwitcher(time.Now())
	if tui.swInput || tui.sw == nil {
		t.Fatalf("esc left input %v with the dashboard %v", tui.swInput, tui.sw != nil)
	}
	if q := tui.sw.Query(); q != "" {
		t.Fatalf("esc in input mode touched the query: %q", q)
	}
	testutil.NoMsg(t, got)
	// The dashboard still takes the keys its own way after the mode ends, and
	// a printable byte is query text again rather than a key for the pane.
	feed(tui, "j")
	if it, ok := tui.sw.Selected(); !ok || it.Pane != "w1p2" {
		t.Fatalf("after input mode j selects %+v, want w1p2", it)
	}
	feed(tui, "z")
	if q := tui.sw.Query(); q != "z" {
		t.Fatalf("after input mode the query is %q, want z", q)
	}
	testutil.NoMsg(t, got)
}

// TestSwitcherInputFooter: the footer names input mode while it is on.
func TestSwitcherInputFooter(t *testing.T) {
	tui, _ := switcherTUI(t)
	out := tui.out.(*bytes.Buffer)
	feed(tui, "\x02a")
	tui.draw()
	if strings.Contains(out.String(), "keys go to") {
		t.Fatal("the footer names input mode before it is on")
	}
	feed(tui, "i")
	out.Reset()
	tui.draw()
	if got := out.String(); !strings.Contains(got, "keys go to w1p1") {
		t.Fatal("the footer does not name input mode")
	}
}

// TestSwitcherPickDigit: a digit goes to that row and picks it.
func TestSwitcherPickDigit(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	feed(tui, "2")
	if tui.sw != nil {
		t.Fatal("a pick digit left the dashboard open")
	}
	select {
	case m := <-got:
		if m.Type != "req" || m.Action != "focus" || m.Target != "w1p2" {
			t.Fatalf("digit 2 sent %+v, want a focus request for w1p2", m)
		}
	case <-time.After(time.Second):
		t.Fatal("digit 2 sent nothing")
	}
}

// TestSwitcherPreviewReplies: a read reply with the wrong id is dropped, one
// with the current id fills the preview, and a failure stays silent.
func TestSwitcherPreviewReplies(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	feed(tui, "beta")
	tui.draw()

	var read wire.Msg
	select {
	case m := <-got:
		if m.Type != "req" || m.Action != "read" || m.Target != "w1p2" {
			t.Fatalf("draw sent %+v, want a read request for w1p2", m)
		}
		if m.ID <= 0 {
			t.Fatalf("read request carries no id: %+v", m)
		}
		read = m
	case <-time.After(time.Second):
		t.Fatal("draw sent no preview request")
	}

	// A second draw asks for nothing: the pane is already asked for.
	tui.draw()
	testutil.NoMsg(t, got)

	// A reply with another id is stale and stays out of the band. A failure
	// with a stale id stays silent too.
	tui.apply(wire.Msg{Type: "resp", ID: read.ID + 1, OK: true, Res: json.RawMessage(`{"lines":["stale"]}`)})
	if len(tui.sw.PreviewLines()) != 0 {
		t.Fatalf("a stale reply filled the preview: %q", tui.sw.PreviewLines())
	}
	tui.apply(wire.Msg{Type: "resp", ID: read.ID + 2, Error: "gone"})
	if len(tui.toasts) != 0 {
		t.Fatalf("a stale preview failure raised a toast: %+v", tui.toasts)
	}

	// The reply to the request fills the band.
	tui.apply(wire.Msg{Type: "resp", ID: read.ID, OK: true, Res: json.RawMessage(`{"lines":["live"]}`)})
	if got := tui.sw.PreviewLines(); len(got) != 1 || got[0] != "live" {
		t.Fatalf("the current reply filled %q, want [live]", got)
	}
}

// TestSwitcherMouseSwallowed: a mouse press while the float is open sends no
// keys to the pane and leaves the float up.
func TestSwitcherMouseSwallowed(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	feed(tui, "\x1b[<0;10;5M")
	if tui.sw == nil {
		t.Fatal("a mouse press closed the switcher")
	}
	testutil.NoMsg(t, got)
	feed(tui, "\x1b[<0;10;5m")
	if tui.sw == nil {
		t.Fatal("a mouse release closed the switcher")
	}
	testutil.NoMsg(t, got)
}
