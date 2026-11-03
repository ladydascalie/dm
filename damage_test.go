package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

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
	root := &Node{Pane: "w1p1"}
	snap := &Snapshot{Cols: cols, Rows: rows, Rail: rail, Root: root, Focus: "w1p1"}
	rects := map[string]Rect{}
	layout(root, paneAreaFor(cols, rows, rail), rects)
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
	rects := map[string]Rect{}
	layout(snap.Root, paneAreaFor(snap.Cols, snap.Rows, snap.Rail), rects)
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
		if a[1] != 1 {
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
