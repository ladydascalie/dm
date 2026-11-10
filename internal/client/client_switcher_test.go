package client

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"dm/internal/keymap"
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
	feed(tui, "/beta")
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

// TestSwitcherFilterHoldsBoundLetters: the bug filter mode exists for. A query
// that holds a letter the table binds is text, so it neither opens the float,
// nor sorts, nor enters input mode, nor jumps.
func TestSwitcherFilterHoldsBoundLetters(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	feed(tui, "/")
	if !tui.sw.Filtering() {
		t.Fatal("the filter key did not enter filter mode")
	}
	feed(tui, "dsip1")
	if tui.sw == nil {
		t.Fatal("a bound letter closed the dashboard")
	}
	if tui.float != nil {
		t.Fatal("d opened the float from the query")
	}
	if q := tui.sw.Query(); q != "dsip1" {
		t.Fatalf("query = %q, want the whole word dsip1", q)
	}
	if m := tui.sw.SortMode(); m != switcher.SortRecent {
		t.Fatalf("s sorted the table to %v", m)
	}
	testutil.NoMsg(t, got)
}

// TestSwitcherEscUnwindsFilter: esc clears the query, then leaves the mode, then
// closes. Outside the mode a letter is a key again.
func TestSwitcherEscUnwindsFilter(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	feed(tui, "/beta")
	esc := func() {
		feed(tui, "\x1b")
		if !tui.pending {
			t.Fatal("esc did not wait for the pending timer")
		}
		tui.escAt = time.Now().Add(-time.Second)
		tui.expireSwitcher(time.Now())
	}
	esc()
	if tui.sw == nil || !tui.sw.Filtering() || tui.sw.Query() != "" {
		t.Fatalf("the first esc left filtering %v with query %q", tui.sw.Filtering(), tui.sw.Query())
	}
	if n := tui.sw.RowCount(); n != 2 {
		t.Fatalf("the first esc left %d rows, want both", n)
	}
	esc()
	if tui.sw == nil || tui.sw.Filtering() {
		t.Fatal("the second esc did not leave filter mode")
	}
	// The mode is gone, so the letters act again: j moves the selection.
	before := tui.sw.Target()
	feed(tui, "j")
	if tui.sw.Target() == before {
		t.Fatalf("j did not move the selection off %q after filter mode", before)
	}
	esc()
	if tui.sw != nil {
		t.Fatal("the third esc did not close the dashboard")
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
	feed(tui, "/beta")
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

// TestSwitcherPreviewShare: the config sets the share the band opens at, zero
// opens the table alone, and the peek key still shows the band at the default
// share.
func TestSwitcherPreviewShare(t *testing.T) {
	cases := []struct {
		name  string
		node  string
		on    bool
		ratio int
	}{
		{"absent", "", true, 60},
		{"off", "dashboard-preview 0\n", false, 60},
		{"small", "dashboard-preview 25\n", true, 25},
		{"under the floor", "dashboard-preview 5\n", true, 20},
		{"over the ceiling", "dashboard-preview 100\n", true, 85},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.kdl")
			if err := os.WriteFile(path, []byte(c.node), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := keymap.LoadPath(path)
			if err != nil {
				t.Fatalf("LoadPath: %v", err)
			}
			tui, got := switcherTUI(t)
			tui.keymap = cfg
			feed(tui, "\x02a")
			if tui.sw == nil {
				t.Fatal("prefix a did not open the dashboard")
			}
			if got, want := tui.sw.PreviewOn(), c.on; got != want {
				t.Fatalf("PreviewOn = %v, want %v", got, want)
			}
			if got := tui.sw.PreviewRatio(); got != c.ratio {
				t.Fatalf("PreviewRatio = %d, want %d", got, c.ratio)
			}
			// Peek always brings the band back, at the share the config set.
			if !c.on {
				feed(tui, "p")
				if !tui.sw.PreviewOn() {
					t.Fatal("the peek key did not show the band")
				}
				if got := tui.sw.PreviewRatio(); got != c.ratio {
					t.Fatalf("peeked share = %d, want %d", got, c.ratio)
				}
			}
			testutil.NoMsg(t, got)
		})
	}
}

// TestSwitcherFloatLayout: dashboard-layout "float" centres the palette and
// leaves the panes live under it, so it claims no screen mode and the frame
// keeps its tab bar and hover.
func TestSwitcherFloatLayout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.kdl")
	if err := os.WriteFile(path, []byte("dashboard-layout \"float\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := keymap.LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	tui, got := switcherTUI(t)
	tui.keymap = cfg
	feed(tui, "\x02a")
	if tui.sw == nil {
		t.Fatal("prefix a did not open the dashboard")
	}
	if tui.mode != "" {
		t.Fatalf("the palette claimed the screen with mode %q", tui.mode)
	}
	want := switcher.PaletteRect(100, 30)
	if tui.sw.Rect != want {
		t.Fatalf("palette rect = %+v, want %+v", tui.sw.Rect, want)
	}
	if tui.sw.Rect.W >= 100 || tui.sw.Rect.H >= 30 {
		t.Fatalf("palette rect %+v fills the 100x30 screen", tui.sw.Rect)
	}
	// The palette still picks and closes like the full-screen dashboard.
	feed(tui, "\r")
	if tui.sw != nil {
		t.Fatal("enter left the palette open")
	}
	select {
	case m := <-got:
		if m.Type != "req" || m.Action != "focus" || m.Target != "w1p1" {
			t.Fatalf("enter sent %+v, want a focus request for w1p1", m)
		}
	case <-time.After(time.Second):
		t.Fatal("enter sent nothing")
	}
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

// TestSwitcherCopyPaneID: i copies the selected row's pane id and leaves the
// dashboard up, so the id can name the pane in the next command.
func TestSwitcherCopyPaneID(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	feed(tui, "j")
	if it, ok := tui.sw.Selected(); !ok || it.Pane != "w1p2" {
		t.Fatalf("j selects %+v, want w1p2", it)
	}
	out := testutil.CaptureStdout(t, func() { feed(tui, "i") })
	if !strings.Contains(out, osc52("w1p2")) {
		t.Fatalf("clipboard = %q, want w1p2", out)
	}
	if tui.sw == nil {
		t.Fatal("the copy closed the dashboard")
	}
	if len(tui.toasts) != 1 || tui.toasts[0].text != "copied w1p2" {
		t.Fatalf("toasts = %+v, want copied w1p2", tui.toasts)
	}
	testutil.NoMsg(t, got)
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
	feed(tui, "/beta")
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
	tui.apply(wire.Msg{Type: "resp", Action: "read", ID: read.ID + 1, OK: true, Res: json.RawMessage(`{"lines":["stale"]}`)})
	if len(tui.sw.PreviewLines()) != 0 {
		t.Fatalf("a stale reply filled the preview: %q", tui.sw.PreviewLines())
	}
	tui.apply(wire.Msg{Type: "resp", Action: "read", ID: read.ID + 2, Error: "gone"})
	if len(tui.toasts) != 0 {
		t.Fatalf("a stale preview failure raised a toast: %+v", tui.toasts)
	}

	// The reply to the request fills the band.
	tui.apply(wire.Msg{Type: "resp", Action: "read", ID: read.ID, OK: true, Res: json.RawMessage(`{"lines":["live"]}`)})
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
