package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"dm/internal/layout"
	"dm/internal/theme"
	"dm/internal/wire"

	"github.com/hinshun/vt10x"
)

// gripTUI is a client whose rail is 20 wide on a 100 column screen, so the
// grip is column 19 zero-based and maxRail is 33.
func gripTUI(t *testing.T) (*tui, chan wire.Msg) {
	t.Helper()
	tui, got := clientTUI(t, nil)
	tui.snap = railTestSnapshot(20)
	return tui, got
}

// drainMsg collects every message the client sends until the wire is quiet.
func drainMsg(t *testing.T, got chan wire.Msg) []wire.Msg {
	t.Helper()
	var out []wire.Msg
	for {
		select {
		case m, ok := <-got:
			if !ok {
				t.Fatal("conn closed")
			}
			out = append(out, m)
		case <-time.After(50 * time.Millisecond):
			return out
		}
	}
}

// TestRailGripHit pins the grip cell: the rail's last column, above the row
// the rail keeps free for its bottom control.
func TestRailGripHit(t *testing.T) {
	snap := railTestSnapshot(20)
	if !railGripAt(snap, snap.Rail-1, 0) || !railGripAt(snap, snap.Rail-1, snap.Rows-3) {
		t.Fatal("grip missing a row it owns")
	}
	if railGripAt(snap, snap.Rail-2, 3) {
		t.Fatal("grip claimed an inner rail column")
	}
	if railGripAt(snap, snap.Rail-1, snap.Rows-2) || railGripAt(snap, snap.Rail-1, snap.Rows-1) {
		t.Fatal("grip claimed the rail's bottom row")
	}
	snap.Rail = 0
	if railGripAt(snap, -1, 3) {
		t.Fatal("hidden rail has a grip")
	}
}

// TestRailGripMark draws the grip: one dim mark at the midpoint of the strip.
func TestRailGripMark(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railRowSnap(20)
	var b bytes.Buffer
	drawRail(&b, theme.DefaultTheme(), snap)
	vt := vt10x.New(vt10x.WithSize(snap.Cols, snap.Rows))
	if _, err := vt.Write(b.Bytes()); err != nil {
		t.Fatalf("draw rail: %v", err)
	}
	vt.Lock()
	defer vt.Unlock()
	strip := snap.Rail - 1
	mid := (snap.Rows - 2) / 2
	if g := vt.Cell(strip, mid); g.Char != []rune(theme.DefaultTheme().Glyphs.Grip)[0] {
		t.Fatalf("grip mark = %q at %d,%d, want %q", g.Char, strip, mid, theme.DefaultTheme().Glyphs.Grip)
	}
}

// TestRailGripDrag drives one drag: the press sends nothing, motion sends the
// width at most every 30 ms, and the release sends the final width once.
func TestRailGripDrag(t *testing.T) {
	tui, got := gripTUI(t)
	// The grip is zero-based 19,3, so the one-based mouse cell is 20,4.
	tui.handleMouse(mouseEvent{btn: 0, x: 20, y: 4})
	if !tui.railDragging {
		t.Fatal("press on the grip did not start a rail drag")
	}
	if tui.dragging {
		t.Fatal("rail drag borrowed the split drag")
	}
	noMsg(t, got)
	tui.handleMouse(mouseEvent{btn: 32, x: 30, y: 4})
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "set-rail" || m.Index != 30 {
		t.Fatalf("motion sent %+v, want set-rail 30", m)
	}
	// A second motion inside 30 ms is throttled.
	tui.handleMouse(mouseEvent{btn: 32, x: 31, y: 4})
	noMsg(t, got)
	// The release forces the final width through, once.
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 32, y: 4})
	m = nextMsg(t, got)
	if m.Action != "set-rail" || m.Index != 32 {
		t.Fatalf("release sent %+v, want set-rail 32", m)
	}
	if tui.railDragging {
		t.Fatal("release did not end the rail drag")
	}
	noMsg(t, got)
}

// TestRailGripClamps keeps a drag inside the widths a rail may take: the thin
// rail at the left, maxRail at the right, never hidden.
func TestRailGripClamps(t *testing.T) {
	tui, got := gripTUI(t)
	tui.handleMouse(mouseEvent{btn: 0, x: 20, y: 4})
	tui.handleMouse(mouseEvent{btn: 32, x: 1, y: 4})
	if m := nextMsg(t, got); m.Action != "set-rail" || m.Index != layout.CollapsedRail {
		t.Fatalf("left clamp sent %+v, want set-rail %d", m, layout.CollapsedRail)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 1, y: 4})
	noMsg(t, got)
	// A fresh drag from the thin rail still works: the grip sits in column 3
	// and dragging right expands.
	tui.snap = railTestSnapshot(layout.CollapsedRail)
	tui.handleMouse(mouseEvent{btn: 0, x: layout.CollapsedRail, y: 4})
	if !tui.railDragging {
		t.Fatal("thin rail grip did not start a drag")
	}
	tui.handleMouse(mouseEvent{btn: 32, x: 90, y: 4})
	want := layout.MaxRail(tui.snap.Cols)
	if m := nextMsg(t, got); m.Action != "set-rail" || m.Index != want {
		t.Fatalf("right clamp sent %+v, want set-rail %d", m, want)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 90, y: 4})
	noMsg(t, got)
}

// TestRailGripStrayClick sends nothing for a click that never moves.
func TestRailGripStrayClick(t *testing.T) {
	tui, got := gripTUI(t)
	tui.handleMouse(mouseEvent{btn: 0, x: 20, y: 4})
	noMsg(t, got)
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 20, y: 4})
	if tui.railDragging {
		t.Fatal("stray click left the drag running")
	}
	noMsg(t, got)
}

// TestRailGripSwallows keeps motion and release off a pane whatever the
// pointer does, and never sends a split resize or a focus.
func TestRailGripSwallows(t *testing.T) {
	tui, got := gripTUI(t)
	p := tui.paneByID("w1p2")
	tui.handleMouse(mouseEvent{btn: 0, x: 20, y: 4})
	tui.handleMouse(mouseEvent{btn: 32, x: p.Rect.X + 1, y: p.Rect.Y + 1})
	tui.handleMouse(mouseEvent{btn: 32, x: 60, y: 2})
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: p.Rect.X + 1, y: p.Rect.Y + 1})
	for _, m := range drainMsg(t, got) {
		if m.Action != "set-rail" {
			t.Fatalf("rail drag sent %+v, want only set-rail", m)
		}
	}
	if tui.railDragging || tui.dragging || tui.press != nil {
		t.Fatalf("rail drag left state behind: rail=%v split=%v press=%+v",
			tui.railDragging, tui.dragging, tui.press)
	}
}

// TestRailGripHover shows the resize hint over the grip and paints the strip.
func TestRailGripHover(t *testing.T) {
	f := newFrameTUI(t, 100, 30, 20)
	f.tui.handleMouse(mouseEvent{btn: 35, x: 20, y: 5})
	if f.tui.padAxis != railPadAxis {
		t.Fatalf("grip hover axis = %q, want %q", f.tui.padAxis, railPadAxis)
	}
	if got := f.tui.barInfo(); got != "drag to resize" {
		t.Fatalf("grip hover hint = %q", got)
	}
	frame := f.frame()
	if !strings.Contains(frame, "┃") {
		t.Fatalf("rail hover did not paint the grip strip: %q", frame)
	}
	if strings.Contains(frame, "━") {
		t.Fatalf("rail hover painted a horizontal divider: %q", frame)
	}
	// Leaving the grip clears the hint.
	f.tui.handleMouse(mouseEvent{btn: 35, x: 3, y: 5})
	if frame := f.frame(); strings.Contains(frame, "┃") {
		t.Fatalf("grip strip survived leaving the grip: %q", frame)
	}
	if f.tui.padZone != (layout.Rect{}) || f.tui.barInfo() != "" {
		t.Fatalf("grip hover survived: %v %q", f.tui.padZone, f.tui.barInfo())
	}
}

// TestPanePressStillFocuses keeps the grip from stealing a pane click: a press
// on a pane row still focuses its pane.
func TestPanePressStillFocuses(t *testing.T) {
	tui, got := gripTUI(t)
	p := tui.paneByID("w1p2")
	tui.handleMouse(mouseEvent{btn: 0, x: p.Rect.X + 1, y: p.Rect.Y + 1})
	if m := nextMsg(t, got); m.Action != "focus" || m.Target != "w1p2" {
		t.Fatalf("pane press sent %+v, want focus w1p2", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: p.Rect.X + 1, y: p.Rect.Y + 1})
	if tui.railDragging {
		t.Fatal("pane press started a rail drag")
	}
}
