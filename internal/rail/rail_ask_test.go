package rail

// The ask badge: a pane the session lists a question on carries it, so a
// question in a window the reader is not looking at still says so.

import (
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/theme"
	"dm/internal/wire"
)

// TestRailAskBadge: RailItems marks the pane with a listed ask and no other,
// RailRow puts the badge on the right edge, and the drawn rail carries it.
func TestRailAskBadge(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railRowSnap(layout.DefaultRail)
	snap.Ask = []wire.Ask{{ID: "ask1", Pane: "w1p1", ToolCallID: "tc1"}}
	items := RailItems(snap)
	asked := railRowFind(t, items, Pane, "pi")
	if !asked.Ask {
		t.Fatal("the pane with a listed ask carries no badge")
	}
	for _, it := range items {
		if it.Kind == Pane && it.Pane != "w1p1" && it.Ask {
			t.Fatalf("pane %q carries a badge with no ask on it", it.Pane)
		}
	}
	th := theme.DefaultTheme()
	_, right, _ := RailRow(th, asked, layout.DefaultRail, false, theme.GlyphUnicode)
	if len(right) == 0 || !strings.Contains(right[len(right)-1].Text, "?") {
		t.Fatalf("the badge is not on the right edge: %+v", right)
	}
	// The badge paints on the pane's own row of the drawn rail.
	row := -1
	for i, it := range items {
		if it.Kind == Pane && it.Pane == "w1p1" {
			row = i
		}
	}
	if row < 0 {
		t.Fatal("the pane row is missing")
	}
	painted := railRows(t, snap, theme.GlyphUnicode)
	if !strings.Contains(painted[row], "?") {
		t.Fatalf("row %d = %q, want the ask badge", row, painted[row])
	}
}

// TestRailAskBadgeNoAsk: a snapshot with no ask paints no badge, so the rail is
// the picture it always was.
func TestRailAskBadgeNoAsk(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railRowSnap(layout.DefaultRail)
	for _, it := range RailItems(snap) {
		if it.Ask {
			t.Fatalf("pane %q carries a badge with no ask listed", it.Pane)
		}
	}
	for _, row := range railRows(t, snap, theme.GlyphUnicode) {
		if strings.Contains(row, "?") {
			t.Fatalf("row %q carries a badge with no ask listed", row)
		}
	}
}
