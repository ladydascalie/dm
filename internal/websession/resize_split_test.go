package websession_test

import (
	"testing"

	"dm/internal/layout"
	"dm/internal/wire"
)

// TestResizeSplitMovesTheDivider proves the pad drag reaches the fake: the
// ratio the client's drag sends moves the two pane boxes, the floor keeps both
// draggable, and a request naming no split is refused.
func TestResizeSplitMovesTheDivider(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.request("split", "w1p1", func(m *wire.Msg) { m.Dir = "right" })
	before, ok := h.pane("w1p1")
	if !ok {
		t.Fatalf("the split left no w1p1: %v", h.allIDs())
	}
	other, ok := h.pane("w1p2")
	if !ok {
		t.Fatalf("the split left no w1p2: %v", h.allIDs())
	}

	// A drag to three quarters gives the first pane three quarters of the
	// split's cells, and the pair still fills the area.
	frames := h.request("resize-split", "w1p1", func(m *wire.Msg) {
		m.Other, m.Ratio = "w1p2", 0.75
	})
	if got := respOf(t, frames); !got.OK {
		t.Fatalf("resize-split answered %+v", got)
	}
	if len(framesOf(frames, "snapshot")) != 1 {
		t.Fatalf("resize-split sent %d snapshots, want one", len(framesOf(frames, "snapshot")))
	}
	wide, _ := h.pane("w1p1")
	narrow, _ := h.pane("w1p2")
	if wide.Rect.W <= before.Rect.W {
		t.Fatalf("w1p1 is %d cells wide, was %d: the drag did not move the divider",
			wide.Rect.W, before.Rect.W)
	}
	if narrow.Rect.W >= other.Rect.W {
		t.Fatalf("w1p2 is %d cells wide, was %d: the other side did not shrink",
			narrow.Rect.W, other.Rect.W)
	}
	if wide.Rect.W+narrow.Rect.W != before.Rect.W+other.Rect.W {
		t.Fatalf("the panes fill %d cells, was %d",
			wide.Rect.W+narrow.Rect.W, before.Rect.W+other.Rect.W)
	}

	// A ratio of zero clamps to the floor: the smallest box a drag may leave
	// holds three content cells, so neither pane disappears.
	frames = h.request("resize-split", "w1p1", func(m *wire.Msg) {
		m.Other, m.Ratio = "w1p2", 0
	})
	if got := respOf(t, frames); !got.OK {
		t.Fatalf("resize-split at the floor answered %+v", got)
	}
	wide, _ = h.pane("w1p1")
	narrow, _ = h.pane("w1p2")
	if wide.Rect.W < 3 || narrow.Rect.W < 3 {
		t.Fatalf("the floor left %d and %d content cells, want at least 3",
			wide.Rect.W, narrow.Rect.W)
	}

	// A request that names no such split is refused, and the tree is untouched.
	before, _ = h.pane("w1p1")
	for _, bad := range []struct {
		name string
		fill func(*wire.Msg)
	}{
		{"no other pane", func(m *wire.Msg) { m.Ratio = 0.5 }},
		{"the same pane twice", func(m *wire.Msg) { m.Other, m.Ratio = "w1p1", 0.5 }},
		{"a ghost pane", func(m *wire.Msg) { m.Other, m.Ratio = "ghost", 0.5 }},
	} {
		frames = h.request("resize-split", "w1p1", bad.fill)
		if got := frames[len(frames)-1]; got.OK || got.Error == "" {
			t.Fatalf("resize-split with %s answered %+v, want a refusal", bad.name, got)
		}
		if after, _ := h.pane("w1p1"); after.Rect != before.Rect {
			t.Fatalf("resize-split with %s moved w1p1 from %v to %v", bad.name, before.Rect, after.Rect)
		}
	}
}

// TestResizeSplitUsesTheTabBarArea proves the ratio is taken against the same
// area the client's hit test uses: the divider a drag pressed is the divider the
// fake moves, so a drag lands under the pointer.
func TestResizeSplitUsesTheTabBarArea(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.request("split", "w1p1", func(m *wire.Msg) { m.Dir = "right" })
	area := layout.PaneAreaForWithTabBar(100, 30, h.snap.Rail)
	if area.Y != 1 {
		t.Fatalf("the pane area starts at row %d, want row 1 under the tab bar", area.Y)
	}
	// Half of the split's own rect is the divider, so half the ratio leaves
	// both panes the same width.
	h.request("resize-split", "w1p1", func(m *wire.Msg) {
		m.Other, m.Ratio = "w1p2", 0.5
	})
	a, _ := h.pane("w1p1")
	b, _ := h.pane("w1p2")
	// An odd cell goes to one side, so the halves may differ by one.
	if d := a.Rect.W - b.Rect.W; d < -1 || d > 1 {
		t.Fatalf("equal halves gave %d and %d cells", a.Rect.W, b.Rect.W)
	}
	if a.Rect.X != area.X+1 || b.Rect.Y != area.Y+1 {
		t.Fatalf("the panes sit at %d,%d and %d,%d, want the pane area %v",
			a.Rect.X, a.Rect.Y, b.Rect.X, b.Rect.Y, area)
	}
}
