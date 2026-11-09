package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// xRows returns the screen rows that hold only the long line's characters,
// oldest first. The echoed command carries no such run, so a row of pure "x"
// is the printed line alone.
func xRows(out string) []string {
	var rows []string
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.Trim(trimmed, "x") != "" {
			continue
		}
		rows = append(rows, trimmed)
	}
	return rows
}

// xTotal counts the line's characters across the screen.
func xTotal(rows []string) int {
	n := 0
	for _, r := range rows {
		n += len(r)
	}
	return n
}

// paneContentWidth is the columns a pane can print in at a given rail width:
// the pane area minus its border and padding, the same rule TestRailReflow
// asserts on.
func paneContentWidth(cols, rail int) int {
	area := paneAreaFor(cols, 1, rail)
	return area.W - 2 - 2*pad()
}

// waitScreen polls the visible screen until hold accepts it. It never sleeps
// on a guess: it reads the pane and re-checks against a deadline.
func waitScreen(t *testing.T, h *harness, session, pane string, hold func([]string) bool) string {
	t.Helper()
	deadline := time.Now().Add(waitBudget)
	var last string
	for time.Now().Before(deadline) {
		last = h.run("read", pane, "-s", session)
		if hold(xRows(last)) {
			return last
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("pane %s never reached the expected screen within the %s wait budget; last read:\n%s",
		pane, waitBudget, last)
	return last
}

// TestReflowOnResize proves the pane re-wraps its live screen when its width
// changes: the same long line spreads over more rows when the rail narrows it
// and returns to one row when the rail gives the width back. Without the
// settle the grid only clips the line, so the character total would shrink.
func TestReflowOnResize(t *testing.T) {
	h := newHarness(t)
	const session = "wrap"
	h.run("new", "-s", session)
	pane := h.focus(session)

	tree := h.rpc(session, map[string]any{"action": "list"})
	snap := tree["snapshot"].(map[string]any)
	cols := int(snap["cols"].(float64))

	wide, narrow := 0, maxRail(cols)
	wideW := paneContentWidth(cols, wide)
	narrowW := paneContentWidth(cols, narrow)
	if wideW-narrowW < 10 {
		t.Fatalf("the rail narrows the pane by %d columns, want at least 10 (%d -> %d)",
			wideW-narrowW, wideW, narrowW)
	}
	line := wideW - 1

	if res := h.rpc(session, map[string]any{"action": "set-rail", "index": wide}); res["rail"] != float64(wide) {
		t.Fatalf("set-rail %d = %v", wide, res["rail"])
	}

	// Print the line at the wide width: it fits on one row.
	h.run("write-to", pane, "-s", session, "--msg",
		fmt.Sprintf(`printf 'x%%.0s' $(seq 1 %d); echo`, line), "--enter")
	waitScreen(t, h, session, pane, func(rows []string) bool {
		return len(rows) == 1 && xTotal(rows) == line
	})

	// Narrow the pane. The reflow replays the ring at the new width, so the
	// line wraps rather than being clipped: every character survives.
	if res := h.rpc(session, map[string]any{"action": "set-rail", "index": narrow}); res["rail"] != float64(narrow) {
		t.Fatalf("set-rail %d = %v", narrow, res["rail"])
	}
	waitScreen(t, h, session, pane, func(rows []string) bool {
		return len(rows) == 2 && xTotal(rows) == line
	})
	rows := xRows(h.run("read", pane, "-s", session))
	if len(rows[0]) > narrowW {
		t.Fatalf("first wrapped row is %d columns, want at most %d", len(rows[0]), narrowW)
	}
	if rows[0] == strings.Repeat("x", line) {
		t.Fatalf("the line did not wrap at the narrow width: %q", rows[0])
	}

	// Widen it back: the same characters come back on one row.
	if res := h.rpc(session, map[string]any{"action": "set-rail", "index": wide}); res["rail"] != float64(wide) {
		t.Fatalf("set-rail %d = %v", wide, res["rail"])
	}
	waitScreen(t, h, session, pane, func(rows []string) bool {
		return len(rows) == 1 && xTotal(rows) == line
	})
}

// TestReflowEndsAtFinalWidth proves a pace of two quick width changes settles
// at the final width, not the intermediate one. The first row of the wrapped
// line must be exactly as wide as the final rail allows.
func TestReflowEndsAtFinalWidth(t *testing.T) {
	h := newHarness(t)
	const session = "drag"
	h.run("new", "-s", session)
	pane := h.focus(session)

	tree := h.rpc(session, map[string]any{"action": "list"})
	snap := tree["snapshot"].(map[string]any)
	cols := int(snap["cols"].(float64))

	wide, narrow := 0, maxRail(cols)
	mid := narrow / 2
	wideW := paneContentWidth(cols, wide)
	narrowW := paneContentWidth(cols, narrow)
	midW := paneContentWidth(cols, mid)
	if narrowW >= midW || midW >= wideW {
		t.Fatalf("rail widths do not narrow the pane: wide %d, mid %d, narrow %d", wideW, midW, narrowW)
	}
	line := wideW - 1

	h.rpc(session, map[string]any{"action": "set-rail", "index": wide})
	h.run("write-to", pane, "-s", session, "--msg",
		fmt.Sprintf(`printf 'x%%.0s' $(seq 1 %d); echo`, line), "--enter")
	waitScreen(t, h, session, pane, func(rows []string) bool {
		return len(rows) == 1 && xTotal(rows) == line
	})

	// Two width changes back to back: the intermediate one must not survive.
	h.rpc(session, map[string]any{"action": "set-rail", "index": mid})
	h.rpc(session, map[string]any{"action": "set-rail", "index": narrow})

	waitScreen(t, h, session, pane, func(rows []string) bool {
		return len(rows) == 2 && xTotal(rows) == line && len(rows[0]) == narrowW
	})
}
