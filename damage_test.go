package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"dm/internal/layout"

	"github.com/hinshun/vt10x"
)

// frameTUI is a tui whose frames land in a buffer, with one pane whose screen
// the test drives by hand.
type frameTUI struct {
	tui  *tui
	snap *Snapshot
	out  *bytes.Buffer
}

// newFrameTUI builds a one-pane session with a rail. The pane's cursor is
// hidden, so a frame's only cursor moves are the content rows.
func newFrameTUI(t *testing.T, cols, rows, rail int) *frameTUI {
	t.Helper()
	root := &layout.Node{Pane: "w1p1"}
	snap := &Snapshot{Cols: cols, Rows: rows, Rail: rail, Root: root, Focus: "w1p1"}
	rects := map[string]layout.Rect{}
	layout.Layout(root, layout.PaneAreaFor(cols, rows, rail), rects)
	snap.Panes = []PaneInfo{{ID: "w1p1", Rect: rects["w1p1"]}}
	snap.All = []AllPane{{
		ID: "w1p1", Name: "alpha", Workspace: "s1", WorkspaceName: "main",
		Window: "w1", WindowName: "one", Focus: true, WorkspaceActive: true,
		WindowActive: true, WindowUnfolded: true,
	}}
	out := &bytes.Buffer{}
	tui := &tui{snap: snap, vts: map[string]vt10x.Terminal{}, out: out}
	p := snap.Panes[0]
	vt := vt10x.New(vt10x.WithSize(p.Rect.W, p.Rect.H))
	tui.vts[p.ID] = vt
	vt.Write([]byte("\x1b[?25l\x1b[1;1HPANE-MARK"))
	return &frameTUI{tui: tui, snap: snap, out: out}
}

// frame draws one frame and returns its bytes.
func (f *frameTUI) frame() string {
	f.out.Reset()
	f.tui.draw()
	return f.out.String()
}

// relayout recomputes the pane rects after a size change, the way the server
// does not: a test owns both halves of the picture.
func relayout(snap *Snapshot) {
	rects := map[string]layout.Rect{}
	layout.Layout(snap.Root, layout.PaneAreaFor(snap.Cols, snap.Rows, snap.Rail), rects)
	for i := range snap.Panes {
		snap.Panes[i].Rect = rects[snap.Panes[i].ID]
	}
}

// TestFrameSteadyStateWritesNothing pins the biggest win: a frame with no
// change on screen costs nothing.
func TestFrameSteadyStateWritesNothing(t *testing.T) {
	f := newFrameTUI(t, 100, 20, 34)
	first := f.frame()
	if first == "" {
		t.Fatal("first frame wrote nothing")
	}
	if !strings.Contains(first, "\x1b[2J") {
		t.Fatalf("first frame is not a whole repaint: %q", first)
	}
	if got := f.frame(); got != "" {
		t.Fatalf("steady frame wrote %d bytes: %q", len(got), got)
	}

	// A toast that is already up is part of the steady picture too.
	f.tui.notify("saved")
	if got := f.frame(); got == "" {
		t.Fatal("a raised toast wrote no frame")
	}
	if got := f.frame(); got != "" {
		t.Fatalf("steady toast frame wrote %d bytes: %q", len(got), got)
	}
}

// TestFrameRepaintsOneRow checks that one changed line costs one line, and
// that a steady frame never clears the screen.
func TestFrameRepaintsOneRow(t *testing.T) {
	f := newFrameTUI(t, 100, 20, 34)
	f.frame()
	p := f.snap.Panes[0]
	vt := f.tui.vts[p.ID]
	vt.Write([]byte("\x1b[3;1HROW-THREE"))
	f.frame()

	vt.Write([]byte("\x1b[3;1HROW-CHANGED"))
	got := f.frame()
	if got == "" {
		t.Fatal("a changed row wrote nothing")
	}
	if strings.Contains(got, "\x1b[2J") {
		t.Fatalf("steady frame cleared the screen: %q", got)
	}
	if strings.Contains(got, "PANE-MARK") {
		t.Fatalf("untouched row repainted: %q", got)
	}
	if !strings.Contains(got, "ROW-CHANGED") {
		t.Fatalf("changed row missing: %q", got)
	}
	addrs := frameAddrs(got)
	if len(addrs) != 1 {
		t.Fatalf("frame wrote %d rows, want 1: %v", len(addrs), addrs)
	}
	if want := [2]int{p.Rect.Y + 2 + 1, p.Rect.X + 1}; addrs[0] != want {
		t.Fatalf("changed row written at %v, want %v", addrs[0], want)
	}
}

// TestFrameSyncMarkers checks every written frame is bracketed, so the
// terminal swaps the frame in whole and never shows a half-painted screen.
func TestFrameSyncMarkers(t *testing.T) {
	f := newFrameTUI(t, 100, 20, 34)
	vt := f.tui.vts[f.snap.Panes[0].ID]
	for i := 0; i < 3; i++ {
		vt.Write([]byte(fmt.Sprintf("\x1b[2;1HSTEP-%d", i)))
		got := f.frame()
		if got == "" {
			t.Fatalf("frame %d wrote nothing", i)
		}
		if !strings.HasPrefix(got, "\x1b[?2026h") || !strings.HasSuffix(got, "\x1b[?2026l") {
			t.Fatalf("frame %d is not bracketed: %q", i, got)
		}
		if n := strings.Count(got, "\x1b[?2026h"); n != 1 {
			t.Fatalf("frame %d carries %d opens", i, n)
		}
		if n := strings.Count(got, "\x1b[?2026l"); n != 1 {
			t.Fatalf("frame %d carries %d closes", i, n)
		}
	}
}

// TestFrameResizeRepaints checks a size change repaints the whole picture.
func TestFrameResizeRepaints(t *testing.T) {
	f := newFrameTUI(t, 100, 20, 34)
	f.frame()

	f.snap.Cols, f.snap.Rows = 120, 24
	relayout(f.snap)
	p := f.snap.Panes[0]
	f.tui.vts[p.ID].Resize(p.Rect.W, p.Rect.H)

	got := f.frame()
	if !strings.Contains(got, "\x1b[2J") {
		t.Fatalf("a resize did not clear: %q", got)
	}
	rows := frameRows(got)
	for row := 0; row < p.Rect.H; row++ {
		if !rows[p.Rect.Y+row] {
			t.Fatalf("resize left content row %d unpainted", p.Rect.Y+row)
		}
	}
}

// TestFrameRailOnly keeps a rail label change inside the rail: no pane row and
// no bar row is written.
func TestFrameRailOnly(t *testing.T) {
	f := newFrameTUI(t, 100, 20, 34)
	f.frame()

	f.snap.All[0].Name = "beta"
	got := f.frame()
	if got == "" {
		t.Fatal("a rail label change wrote nothing")
	}
	if strings.Contains(got, "\x1b[2J") {
		t.Fatalf("a rail label change cleared the screen: %q", got)
	}
	if !strings.Contains(got, "beta") {
		t.Fatalf("new rail label missing: %q", got)
	}
	if strings.Contains(got, "PANE-MARK") {
		t.Fatalf("a rail label change repainted the pane: %q", got)
	}
	for _, a := range frameAddrs(got) {
		if a[1] > f.snap.Rail {
			t.Fatalf("rail frame wrote column %d: %v", a[1], frameAddrs(got))
		}
		if a[0] > f.snap.Rows-1 {
			t.Fatalf("rail frame wrote the bar row %d", a[0])
		}
	}
}

// TestFrameLeaveScrollback checks leaving scrollback repaints the whole
// screen: the scrollback rows are not the live pane's rows.
func TestFrameLeaveScrollback(t *testing.T) {
	f := newFrameTUI(t, 100, 20, 34)
	p := f.snap.Panes[0]
	vt := f.tui.vts[p.ID]
	for i := 0; i < 80; i++ {
		vt.Write([]byte(fmt.Sprintf("LINE-%03d\r\n", i)))
	}
	f.frame()

	f.tui.scrollVT = vt10x.New(vt10x.WithSize(p.Rect.W, p.Rect.H+histRows))
	f.tui.scrollVT.Write([]byte("HIST-ONE\r\nHIST-TWO"))
	f.tui.scroll = p.ID
	f.tui.mode = "scroll"
	if got := f.frame(); got == "" {
		t.Fatal("entering scrollback wrote nothing")
	}

	f.tui.exitScroll()
	got := f.frame()
	if !strings.Contains(got, "\x1b[2J") {
		t.Fatalf("leaving scrollback did not clear: %q", got)
	}
	rows := frameRows(got)
	for row := 0; row < p.Rect.H; row++ {
		if !rows[p.Rect.Y+row] {
			t.Fatalf("leaving scrollback left content row %d unpainted", p.Rect.Y+row)
		}
	}
}

// TestFrameToastLeavesNothingBehind checks a toast that goes takes nothing
// with it: the row under it is painted again, by the bands when it is a border
// row and by the panes when it is a content row.
func TestFrameToastLeavesNothingBehind(t *testing.T) {
	f := newFrameTUI(t, 100, 20, 34)
	f.frame()

	f.tui.notify("saved")
	up := f.frame()
	if !strings.Contains(up, "saved") {
		t.Fatalf("toast frame missing the toast: %q", up)
	}
	f.tui.toasts = nil
	gone := f.frame()
	if gone == "" {
		t.Fatal("an expired toast wrote nothing")
	}
	if strings.Contains(gone, "\x1b[2J") {
		t.Fatalf("toast expiry cleared the screen: %q", gone)
	}
	if strings.Contains(gone, "saved") {
		t.Fatalf("toast glyphs survived the toast: %q", gone)
	}
	if !strings.Contains(gone, "\u2514") {
		t.Fatalf("box border under the toast was not painted again: %q", gone)
	}
}

// TestFrameHelpLeavesContentBehind checks the content under an overlay comes
// back when the overlay closes.
func TestFrameHelpLeavesContentBehind(t *testing.T) {
	f := newFrameTUI(t, 100, 20, 34)
	p := f.snap.Panes[0]
	vt := f.tui.vts[p.ID]
	vt.Write([]byte("\x1b[11;36HUNDER-HELP"))
	f.frame()

	f.tui.armed = true
	if got := f.frame(); !strings.Contains(got, "prefix") {
		t.Fatalf("help box missing: %q", got)
	}
	f.tui.armed = false
	got := f.frame()
	if got == "" {
		t.Fatal("closing the help box wrote nothing")
	}
	if strings.Contains(got, "\x1b[2J") {
		t.Fatalf("closing the help box cleared the screen: %q", got)
	}
	if !strings.Contains(got, "UNDER-HELP") {
		t.Fatalf("content under the help box was not painted again: %q", got)
	}
}

// TestFrameSelectionRepaintsRows checks a selection moves only the rows it
// covers.
func TestFrameSelectionRepaintsRows(t *testing.T) {
	f := newFrameTUI(t, 100, 20, 34)
	f.frame()
	p := f.snap.Panes[0]
	vt := f.tui.vts[p.ID]
	vt.Write([]byte("\x1b[1;1HTOP-ROW\x1b[6;1HMID-ROW\x1b[12;1HLOW-ROW"))
	f.frame()

	f.tui.selText = &selection{
		pane: p.ID, anchor: cell{x: 0, y: 5}, focus: cell{x: 6, y: 5},
		w: p.Rect.W, h: p.Rect.H,
	}
	got := f.frame()
	if got == "" {
		t.Fatal("a selection wrote nothing")
	}
	if strings.Contains(got, "\x1b[2J") {
		t.Fatalf("a selection cleared the screen: %q", got)
	}
	rows := frameRows(got)
	if !rows[p.Rect.Y+5] {
		t.Fatalf("selected row not repainted: %v", rows)
	}
	if rows[p.Rect.Y] || rows[p.Rect.Y+11] {
		t.Fatalf("selection repainted rows it did not touch: %v", rows)
	}
}

// frameAddrs returns the row and column of every cursor move a frame writes,
// one-based, the way the frame spells them.
func frameAddrs(s string) [][2]int {
	var out [][2]int
	for i := 0; i+1 < len(s); i++ {
		if s[i] != 0x1b || s[i+1] != '[' {
			continue
		}
		j := i + 2
		row, n := 0, 0
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			row = row*10 + int(s[j]-'0')
			j++
			n++
		}
		if n == 0 || j >= len(s) || s[j] != ';' {
			continue
		}
		j++
		col, m := 0, 0
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			col = col*10 + int(s[j]-'0')
			j++
			m++
		}
		if m == 0 || j >= len(s) || s[j] != 'H' {
			continue
		}
		out = append(out, [2]int{row, col})
	}
	return out
}

// frameRows is the set of zero-based rows a frame addresses.
func frameRows(s string) map[int]bool {
	rows := map[int]bool{}
	for _, a := range frameAddrs(s) {
		rows[a[0]-1] = true
	}
	return rows
}

// oracle compares the accumulated diff frames against a clean full repaint of
// the same state.
func checkFrame(t *testing.T, live vt10x.Terminal, tr *tui, label string, raw []byte) {
	t.Helper()
	clone := *tr
	var buf bytes.Buffer
	clone.out = &buf
	clone.laysig = ""
	clone.rowsig = nil
	clone.last = lastFrame{}
	clone.draw()
	if buf.Len() == 0 {
		t.Fatalf("%s: oracle frame empty", label)
	}
	ref := vt10x.New(vt10x.WithSize(tr.snap.Cols, tr.snap.Rows))
	ref.Write(buf.Bytes())
	cols, rows := live.Size()
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			a, b := normCell(live.Cell(x, y)), normCell(ref.Cell(x, y))
			if a.Char != b.Char || a.FG != b.FG || a.BG != b.BG || a.Mode != b.Mode {
				t.Fatalf("%s: cell %d,%d live=%q/%v/%v/%v ref=%q/%v/%v/%v byte=%q",
					label, x, y, a.Char, a.FG, a.BG, a.Mode, b.Char, b.FG, b.BG, b.Mode, frameBytes(tr))
			}
		}
	}
	if bytes.Contains(raw, []byte("\x1b[?25h")) && live.Cursor() != ref.Cursor() {
		t.Fatalf("%s: visible cursor live=%v ref=%v", label, live.Cursor(), ref.Cursor())
	}
}

func frameBytes(tr *tui) []byte {
	if tr.out == nil {
		return nil
	}
	if b, ok := tr.out.(*bytes.Buffer); ok {
		return b.Bytes()
	}
	return nil
}

func scene(t *testing.T, f *frameTUI, live vt10x.Terminal) {
	t.Helper()
	step := func(label string) {
		raw := f.frame()
		live.Write([]byte(raw))
		checkFrame(t, live, f.tui, label, []byte(raw))
	}
	step("first")
	vt := f.tui.vts[f.snap.Panes[0].ID]
	vt.Write([]byte("\x1b[3;1HROW-THREE"))
	step("row-three")
	vt.Write([]byte("\x1b[3;1HROW-CHANGED"))
	step("row-changed")
	f.tui.notify("saved")
	step("toast-up")
	f.tui.toasts = nil
	step("toast-gone")
	f.tui.armed = true
	step("help-up")
	f.tui.armed = false
	step("help-gone")
	p := f.snap.Panes[0]
	f.tui.selText = &selection{pane: p.ID, anchor: cell{x: 2, y: 3}, focus: cell{x: 8, y: 6}, w: p.Rect.W, h: p.Rect.H}
	step("selection")
	f.tui.selText = nil
	step("selection-gone")
	f.snap.All[0].Name = "beta"
	step("rail")
	f.snap.All[0].Status = "working"
	step("rail-status")
	step("steady")
}

func TestOracleOnePane(t *testing.T) {
	f := newFrameTUI(t, 100, 20, 34)
	live := vt10x.New(vt10x.WithSize(100, 20))
	scene(t, f, live)
}

func TestOracleResize(t *testing.T) {
	f := newFrameTUI(t, 100, 20, 34)
	live := vt10x.New(vt10x.WithSize(100, 20))
	f.frame()
	live.Write([]byte("\x1b[2J"))
	// live must match the accumulated picture; feed the whole history instead.
	// Rebuild from scratch: first frame then resize.
	f2 := newFrameTUI(t, 100, 20, 34)
	live = vt10x.New(vt10x.WithSize(100, 20))
	f2.frame()
	live.Write([]byte(f2.out.String()))
	f2.out.Reset()
	f2.tui.draw()

	p := f2.snap.Panes[0]
	vt := f2.tui.vts[p.ID]
	vt.Write([]byte("\x1b[2;1Hhello"))
	f2.out.Reset()
	f2.tui.draw()
	live.Write([]byte(f2.out.String()))
	checkFrame(t, live, f2.tui, "pre-resize", nil)
}

func TestOracleDividerHover(t *testing.T) {
	root := &layout.Node{Dir: "row", Kids: []*layout.Node{{Pane: "a"}, {Pane: "b"}}}
	snap := &Snapshot{Cols: 100, Rows: 20, Rail: 0, Root: root, Focus: "a"}
	rects := map[string]layout.Rect{}
	layout.Layout(root, layout.PaneAreaFor(100, 20, 0), rects)
	snap.Panes = []PaneInfo{{ID: "a", Rect: rects["a"]}, {ID: "b", Rect: rects["b"]}}
	snap.All = []AllPane{
		{ID: "a", Name: "one", Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one", Focus: true, WorkspaceActive: true, WindowActive: true, WindowUnfolded: true},
		{ID: "b", Name: "two", Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one", WorkspaceActive: true, WindowActive: true, WindowUnfolded: true},
	}
	out := &bytes.Buffer{}
	tr := &tui{snap: snap, vts: map[string]vt10x.Terminal{}, out: out}
	for _, p := range snap.Panes {
		vt := vt10x.New(vt10x.WithSize(p.Rect.W, p.Rect.H))
		tr.vts[p.ID] = vt
		vt.Write([]byte(fmt.Sprintf("\x1b[2;1H%s", p.ID)))
	}
	live := vt10x.New(vt10x.WithSize(100, 20))
	step := func(label string) {
		out.Reset()
		tr.draw()
		live.Write(out.Bytes())
		checkFrame(t, live, tr, label, out.Bytes())
	}
	step("first")
	boxes := layout.ChildBoxRects(root, layout.PaneAreaFor(100, 20, 0))
	zone := layout.PadRect(root, boxes, 0)
	tr.padZone, tr.padAxis = zone, root.Dir
	step("hover")
	tr.padZone = layout.Rect{}
	step("unhover")
	tr.vts["a"].Write([]byte("\x1b[5;1Hchange-a"))
	step("row-a")
	tr.vts["b"].Write([]byte("\x1b[5;1Hchange-b"))
	step("row-b")
}

func TestOracleScrollback(t *testing.T) {
	f := newFrameTUI(t, 100, 20, 34)
	p := f.snap.Panes[0]
	vt := f.tui.vts[p.ID]
	for i := 0; i < 80; i++ {
		vt.Write([]byte(fmt.Sprintf("LINE-%03d\r\n", i)))
	}
	live := vt10x.New(vt10x.WithSize(100, 20))
	step := func(label string) {
		raw := f.frame()
		live.Write([]byte(raw))
		checkFrame(t, live, f.tui, label, []byte(raw))
	}
	step("first")
	// Enter scrollback the way exitScroll/enterScroll do it.
	f.tui.scrollVT = vt10x.New(vt10x.WithSize(p.Rect.W, p.Rect.H+histRows))
	f.tui.scrollVT.Write([]byte("HIST-ONE\r\nHIST-TWO"))
	f.tui.scroll = p.ID
	f.tui.mode = "scroll"
	step("enter")
	f.tui.offset = 3
	step("offset-3")
	f.tui.offset = 1
	step("offset-1")
	f.tui.offset = 0
	step("offset-0")
	f.tui.exitScroll()
	step("exit")
	step("steady")
}

func TestOracleModes(t *testing.T) {
	f := newFrameTUI(t, 100, 24, 34)
	live := vt10x.New(vt10x.WithSize(100, 24))
	step := func(label string) {
		raw := f.frame()
		live.Write([]byte(raw))
		checkFrame(t, live, f.tui, label, []byte(raw))
	}
	step("first")
	f.snap.All[0].Name = "alpha-renamed"
	f.tui.mode = "sessions"
	f.tui.sessions = []SessionInfo{{Name: "main", Live: true}, {Name: "two"}, {Name: "three", Live: true}}
	f.tui.sel = 1
	f.tui.session = "main"
	step("picker")
	f.tui.sel = 2
	step("picker-move")
	f.tui.sessions = append(f.tui.sessions, SessionInfo{Name: "four", Live: true})
	step("picker-grow")
	f.tui.mode = ""
	step("picker-close")
	f.tui.menu = &menu{x: 10, y: 4, w: 30, title: "pane", items: []menuItem{{label: "split", key: "|"}, {label: "close", key: "x"}}}
	f.tui.mode = "menu"
	step("menu")
	f.tui.menu.sel = 1
	step("menu-sel")
	f.tui.menu = nil
	f.tui.mode = ""
	step("menu-close")
	step("steady")
}

func TestOracleOverlayOverGap(t *testing.T) {
	root := &layout.Node{Dir: "row", Kids: []*layout.Node{{Pane: "a"}, {Pane: "b"}}}
	snap := &Snapshot{Cols: 100, Rows: 20, Rail: 0, Root: root, Focus: "a"}
	rects := map[string]layout.Rect{}
	layout.Layout(root, layout.PaneAreaFor(100, 20, 0), rects)
	snap.Panes = []PaneInfo{{ID: "a", Rect: rects["a"]}, {ID: "b", Rect: rects["b"]}}
	snap.All = []AllPane{
		{ID: "a", Name: "one", Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one", Focus: true, WorkspaceActive: true, WindowActive: true, WindowUnfolded: true},
		{ID: "b", Name: "two", Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one", WorkspaceActive: true, WindowActive: true, WindowUnfolded: true},
	}
	out := &bytes.Buffer{}
	tr := &tui{snap: snap, vts: map[string]vt10x.Terminal{}, out: out}
	for _, p := range snap.Panes {
		vt := vt10x.New(vt10x.WithSize(p.Rect.W, p.Rect.H))
		tr.vts[p.ID] = vt
		vt.Write([]byte(fmt.Sprintf("\x1b[2;1H%s-content", p.ID)))
	}
	live := vt10x.New(vt10x.WithSize(100, 20))
	step := func(label string) {
		out.Reset()
		tr.draw()
		live.Write(out.Bytes())
		checkFrame(t, live, tr, label, out.Bytes())
	}
	step("first")
	boxes := layout.ChildBoxRects(root, layout.PaneAreaFor(100, 20, 0))
	tr.padZone, tr.padAxis = layout.PadRect(root, boxes, 0), root.Dir
	step("hover")
	tr.toasts = []toast{{text: "divider-hover-toast"}}
	step("toast-over-divider")
	tr.toasts = nil
	step("toast-gone")
	tr.padZone = layout.Rect{}
	step("unhover")
}

func TestOracleToastsShrink(t *testing.T) {
	f := newFrameTUI(t, 100, 20, 34)
	live := vt10x.New(vt10x.WithSize(100, 20))
	step := func(label string) {
		raw := f.frame()
		live.Write([]byte(raw))
		checkFrame(t, live, f.tui, label, []byte(raw))
	}
	step("first")
	f.tui.toasts = []toast{{text: "a much longer toast message than before"}}
	step("long")
	f.tui.toasts = []toast{{text: "short"}, {text: "second"}}
	step("two-short")
	f.tui.toasts = []toast{{text: "second"}}
	step("one")
	f.tui.toasts = nil
	step("none")
	f.tui.armed = true
	step("help")
	f.tui.toasts = []toast{{text: "overlap"}}
	step("help+toast")
	f.tui.armed = false
	step("help-off")
	f.tui.toasts = nil
	step("done")
}

func TestOracleResizeLive(t *testing.T) {
	f := newFrameTUI(t, 100, 20, 34)
	live := vt10x.New(vt10x.WithSize(100, 20))
	raw := f.frame()
	live.Write([]byte(raw))
	checkFrame(t, live, f.tui, "first", []byte(raw))

	f.snap.Cols, f.snap.Rows = 120, 24
	f.snap.Rail = layout.EffectiveRail(120, f.snap.Rail)
	relayout(f.snap)
	p := f.snap.Panes[0]
	f.tui.vts[p.ID].Resize(p.Rect.W, p.Rect.H)
	f.tui.vts[p.ID].Write([]byte("\x1b[1;1HSIZED"))
	live.Resize(120, 24)
	raw = f.frame()
	live.Write([]byte(raw))
	checkFrame(t, live, f.tui, "resize", []byte(raw))

	f.snap.Rail = 0
	relayout(f.snap)
	p = f.snap.Panes[0]
	f.tui.vts[p.ID].Resize(p.Rect.W, p.Rect.H)
	raw = f.frame()
	live.Write([]byte(raw))
	checkFrame(t, live, f.tui, "rail-hidden", []byte(raw))
}

func twoPaneTUI(cols, rows, rail int) (*tui, *bytes.Buffer, *Snapshot) {
	root := &layout.Node{Dir: "row", Kids: []*layout.Node{{Pane: "a"}, {Pane: "b"}}}
	snap := &Snapshot{Cols: cols, Rows: rows, Rail: rail, Root: root, Focus: "a"}
	rects := map[string]layout.Rect{}
	layout.Layout(root, layout.PaneAreaFor(cols, rows, rail), rects)
	snap.Panes = []PaneInfo{{ID: "a", Rect: rects["a"]}, {ID: "b", Rect: rects["b"]}}
	snap.All = []AllPane{
		{ID: "a", Name: "one", Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one", Focus: true, WorkspaceActive: true, WindowActive: true, WindowUnfolded: true},
		{ID: "b", Name: "two", Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one", WorkspaceActive: true, WindowActive: true, WindowUnfolded: true},
	}
	out := &bytes.Buffer{}
	tr := &tui{snap: snap, vts: map[string]vt10x.Terminal{}, out: out}
	for _, p := range snap.Panes {
		vt := vt10x.New(vt10x.WithSize(p.Rect.W, p.Rect.H))
		tr.vts[p.ID] = vt
		vt.Write([]byte(fmt.Sprintf("\x1b[2;1H%s-content", p.ID)))
	}
	return tr, out, snap
}

// A toast is up, then the divider is hovered: the divider band paints after
// the toasts band, so the toast must be painted again.
func TestOracleDividerOverToast(t *testing.T) {
	tr, out, snap := twoPaneTUI(100, 20, 0)
	live := vt10x.New(vt10x.WithSize(100, 20))
	step := func(label string) {
		out.Reset()
		tr.draw()
		live.Write(out.Bytes())
		checkFrame(t, live, tr, label, out.Bytes())
	}
	step("first")
	tr.toasts = []toast{{text: "a toast that spans the divider pad"}}
	step("toast")
	boxes := layout.ChildBoxRects(snap.Root, layout.PaneAreaFor(100, 20, 0))
	tr.padZone, tr.padAxis = layout.PadRect(snap.Root, boxes, 0), snap.Root.Dir
	step("hover-over-toast")
	tr.padZone = layout.Rect{}
	step("unhover")
}

// A help box over the rail on a narrow screen, then a rail label change.
func TestOracleRailUnderHelp(t *testing.T) {
	f := newFrameTUI(t, 40, 14, 13)
	live := vt10x.New(vt10x.WithSize(40, 14))
	step := func(label string) {
		raw := f.frame()
		live.Write([]byte(raw))
		checkFrame(t, live, f.tui, label, []byte(raw))
	}
	step("first")
	f.tui.armed = true
	step("help")
	f.snap.All[0].Name = "renamed-pane"
	step("rail-change-under-help")
	f.snap.All[0].Status = "working"
	f.snap.All[0].Agent = true
	step("rail-status-under-help")
	f.tui.armed = false
	step("help-off")
}

// A wide toast over the rail on a narrow screen, then a rail label change.
func TestOracleRailUnderToast(t *testing.T) {
	f := newFrameTUI(t, 40, 14, 13)
	live := vt10x.New(vt10x.WithSize(40, 14))
	step := func(label string) {
		raw := f.frame()
		live.Write([]byte(raw))
		checkFrame(t, live, f.tui, label, []byte(raw))
	}
	step("first")
	f.tui.toasts = []toast{{text: "a toast wide enough to cover the rail"}}
	step("toast")
	f.snap.All[0].Name = "renamed-pane"
	step("rail-change-under-toast")
	f.snap.All[0].Status = "working"
	f.snap.All[0].Agent = true
	step("rail-status-under-toast")
}

func TestOraclePalette(t *testing.T) {
	f := newFrameTUI(t, 100, 20, 34)
	live := vt10x.New(vt10x.WithSize(100, 20))
	step := func(label string) {
		raw := f.frame()
		live.Write([]byte(raw))
		checkFrame(t, live, f.tui, label, []byte(raw))
	}
	step("first")
	rect := paletteRect(100, 20)
	f.tui.sw = newSwitcher(rect, f.snap)
	step("open")
	f.tui.sw.appendText('t')
	step("type")
	f.tui.sw.appendText('y')
	step("type2")
	f.tui.sw = nil
	step("close")
	step("steady")
}

// The palette floats over the rail columns; a rail change must not eat it.
func TestOracleRailUnderPalette(t *testing.T) {
	f := newFrameTUI(t, 100, 30, 34)
	live := vt10x.New(vt10x.WithSize(100, 30))
	step := func(label string) {
		raw := f.frame()
		live.Write([]byte(raw))
		checkFrame(t, live, f.tui, label, []byte(raw))
	}
	step("first")
	rect := paletteRect(100, 30)
	f.tui.sw = newSwitcher(rect, f.snap)
	step("open")
	f.snap.All[0].Status = "working"
	f.snap.All[0].Agent = true
	step("rail-change-under-palette")
}

// A toast up, then a divider hover, with the default rail.
func TestOracleDefaultRailDividerOverToast(t *testing.T) {
	tr, out, snap := twoPaneTUI(120, 30, 34)
	live := vt10x.New(vt10x.WithSize(120, 30))
	step := func(label string) {
		out.Reset()
		tr.draw()
		live.Write(out.Bytes())
		checkFrame(t, live, tr, label, out.Bytes())
	}
	step("first")
	tr.toasts = []toast{{text: "saved"}}
	step("toast")
	boxes := layout.ChildBoxRects(snap.Root, layout.PaneAreaFor(120, 30, 34))
	tr.padZone, tr.padAxis = layout.PadRect(snap.Root, boxes, 0), snap.Root.Dir
	step("hover")
}

// A split changes the layout with the same screen size: it must clear and
// paint everything.
func TestOracleSplit(t *testing.T) {
	f := newFrameTUI(t, 100, 20, 34)
	live := vt10x.New(vt10x.WithSize(100, 20))
	raw := f.frame()
	live.Write([]byte(raw))
	checkFrame(t, live, f.tui, "first", []byte(raw))

	f.snap.Root = &layout.Node{Dir: "row", Kids: []*layout.Node{{Pane: "w1p1"}, {Pane: "w1p2"}}}
	f.snap.Panes = []PaneInfo{{ID: "w1p1"}, {ID: "w1p2"}}
	relayout(f.snap)
	for _, p := range f.snap.Panes {
		vt := vt10x.New(vt10x.WithSize(p.Rect.W, p.Rect.H))
		f.tui.vts[p.ID] = vt
		vt.Write([]byte("\x1b[2;1H" + p.ID))
	}
	raw = f.frame()
	if !strings.Contains(raw, "\x1b[2J") {
		t.Fatalf("a split did not clear: %q", raw)
	}
	live.Write([]byte(raw))
	checkFrame(t, live, f.tui, "split", []byte(raw))

	f.snap.Focus = "w1p2"
	raw = f.frame()
	if !strings.Contains(raw, "\x1b[2J") {
		t.Fatalf("a focus change did not clear: %q", raw)
	}
	live.Write([]byte(raw))
	checkFrame(t, live, f.tui, "focus", []byte(raw))
}

// Every written frame is bracketed exactly once.
func TestOracleBrackets(t *testing.T) {
	f := newFrameTUI(t, 100, 20, 34)
	vt := f.tui.vts[f.snap.Panes[0].ID]
	for i := 0; i < 5; i++ {
		vt.Write([]byte(fmt.Sprintf("\x1b[%d;1HSTEP-%d", i+1, i)))
		raw := f.frame()
		if raw == "" {
			t.Fatalf("step %d wrote nothing", i)
		}
		if !strings.HasPrefix(raw, "\x1b[?2026h") || !strings.HasSuffix(raw, "\x1b[?2026l") {
			t.Fatalf("step %d unbracketed", i)
		}
		if strings.Count(raw, "\x1b[?2026h") != 1 || strings.Count(raw, "\x1b[?2026l") != 1 {
			t.Fatalf("step %d double bracketed", i)
		}
	}
	if got := f.frame(); got != "" {
		t.Fatalf("steady frame wrote %d bytes", len(got))
	}
}

// A band is tracked by the cursor moves its bytes carry, so bandRows must read
// every absolute form: row;col, a bare row and a bare home.
func TestBandRowsReadsEveryCursorForm(t *testing.T) {
	cases := []struct {
		name string
		buf  string
		want []int
	}{
		{"row and col", "\x1b[3;7Hx", []int{2}},
		{"row only", "\x1b[3Hx", []int{2}},
		{"home", "\x1b[Hx", []int{0}},
		{"row one", "\x1b[1Hx", []int{0}},
		{"zero row is the top", "\x1b[0;0Hx", []int{0}},
		{"hvp final", "\x1b[4f", []int{3}},
		{"screen order, once each", "\x1b[9;9Hx\x1b[2Hx\x1b[3;1Hx", []int{1, 2, 8}},
		{"sgr and clears are not moves", "\x1b[0m\x1b[1;96m\x1b[2J\x1b[K", nil},
		{"relative moves address no row", "\x1b[2Ax\x1b[1By\x1b[3Cz", nil},
	}
	for _, c := range cases {
		got := bandRows([]byte(c.buf))
		if len(got) != len(c.want) {
			t.Fatalf("%s: rows %v, want %v", c.name, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: rows %v, want %v", c.name, got, c.want)
			}
		}
	}
}

// Every band lands with an absolute move, because bandRows can see no other
// kind. A band that switched to a relative move would leave stale cells under
// a standing overlay with no test to catch it, so scan every frame instead.
func TestBandsUseAbsoluteCursorMoves(t *testing.T) {
	f := newFrameTUI(t, 100, 24, 34)
	steps := []struct {
		name string
		arm  func()
	}{
		{"first", func() {}},
		{"toast", func() { f.tui.notify("a toast that lands on the bar") }},
		{"help", func() { f.tui.armed = true }},
		{"palette", func() {
			f.tui.armed = false
			rect := paletteRect(f.snap.Cols, f.snap.Rows)
			f.tui.sw = newSwitcher(rect, f.snap)
		}},
		{"menu", func() {
			f.tui.sw = nil
			f.tui.menu = &menu{x: 9, y: 3, w: 20, title: "pane", items: []menuItem{{label: "split", key: "|"}}}
			f.tui.mode = "menu"
		}},
		{"sessions", func() {
			f.tui.menu = nil
			f.tui.mode = "sessions"
			f.tui.sessions = []SessionInfo{{Name: "main", Live: true}}
		}},
		{"rail", func() {
			f.tui.mode = ""
			f.snap.All[0].Name = "renamed"
		}},
	}
	for _, s := range steps {
		s.arm()
		raw := f.frame()
		if raw == "" {
			t.Fatalf("%s: frame wrote nothing", s.name)
		}
		if got := relMoves([]byte(raw)); len(got) > 0 {
			t.Fatalf("%s: frame carries relative cursor moves %q", s.name, got)
		}
	}

	// The divider band only draws between two panes.
	tr, out, snap := twoPaneTUI(100, 24, 0)
	out.Reset()
	tr.draw()
	boxes := layout.ChildBoxRects(snap.Root, layout.PaneAreaFor(100, 24, 0))
	tr.padZone, tr.padAxis = layout.PadRect(snap.Root, boxes, 0), snap.Root.Dir
	out.Reset()
	tr.draw()
	if got := relMoves(out.Bytes()); len(got) > 0 {
		t.Fatalf("hover frame carries relative cursor moves %q", got)
	}
	// The scan has to see a relative move, or the guard above is silent.
	if got := relMoves([]byte("\x1b[2A\x1b[3;4B")); len(got) != 2 {
		t.Fatalf("the relative-move scan found %q, want two", got)
	}
}

// relMoves returns the relative cursor moves in a frame. A band must not use
// one: bandRows reads absolute moves only.
func relMoves(buf []byte) []string {
	var out []string
	for i := 0; i+2 < len(buf); i++ {
		if buf[i] != 0x1b || buf[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(buf) && (buf[j] == ';' || (buf[j] >= '0' && buf[j] <= '9')) {
			j++
		}
		if j >= len(buf) {
			continue
		}
		switch buf[j] {
		case 'A', 'B', 'C', 'D', 'E', 'F', 'G':
			out = append(out, string(buf[i:j+1]))
		}
	}
	return out
}
