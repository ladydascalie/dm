package blackbox

import (
	"strings"
	"testing"
)

// TestServerSemanticsEndToEnd proves the argument-free server semantics through
// the binary: swap-panes by dir trades ids without moving rects, select-layout
// next walks the whole preset cycle and wraps, a split clears the stored name
// so the next next starts at the first preset, and synchronize-panes toggles
// off and on so a write broadcasts only while the flag is set.
func TestServerSemanticsEndToEnd(t *testing.T) {
	h := newHarness(t)
	s := "keysem"
	h.run("new", "-s", s)
	p1 := h.focus(s)
	p2 := h.split(s, p1, "right")
	_ = h.split(s, p1, "down")
	p4 := h.split(s, p2, "right")
	h.waitPanes(s, 4)
	ids := h.panes(s)
	if len(ids) != 4 || ids[0] != p1 || ids[3] != p4 {
		t.Fatalf("setup panes = %v, want four leaves %s..%s", ids, p1, p4)
	}
	waitAdopted(t, h, s, ids)

	// Focus a pane that has a right neighbour and swap-panes with dir right:
	// the focused pane and its right neighbour trade ids while every rectangle
	// keeps its screen position, and the focus survives. The neighbour is read
	// from the geometry, not assumed, so the assertion holds whatever the split
	// tree arranges.
	if res := h.rpc(s, map[string]any{"action": "focus", "target": p1}); res["error"] != nil {
		t.Fatalf("focus: %v", res["error"])
	}
	if got := h.focus(s); got != p1 {
		t.Fatalf("focus = %q, want %q", got, p1)
	}
	beforeRects := h.rects(s)
	if res := h.rpc(s, map[string]any{"action": "swap-panes", "dir": "right"}); res["error"] != nil {
		t.Fatalf("swap-panes right: %v", res["error"])
	}
	afterRects := h.rects(s)
	if got := h.focus(s); got != p1 {
		t.Fatalf("swap moved the focus to %q, want %q", got, p1)
	}
	// The right neighbour is the pane that now sits where the focused pane
	// used to stand; it and the focused pane exchanged rects, everyone else
	// keeps theirs exactly.
	var n string
	for id, r := range afterRects {
		if id != p1 && r == beforeRects[p1] {
			n = id
		}
	}
	if n == "" {
		t.Fatalf("no pane took the focused pane's former rect: before %v after %v", beforeRects, afterRects)
	}
	if afterRects[p1] != beforeRects[n] {
		t.Fatalf("swap left the focus %s %v here, want the neighbour's %v", p1, afterRects[p1], beforeRects[n])
	}
	for id, r := range beforeRects {
		if id == p1 || id == n {
			continue
		}
		if afterRects[id] != r {
			t.Fatalf("swap moved %s's rect: %v -> %v", id, r, afterRects[id])
		}
	}

	// select-layout next five times: the cycle visits every preset, then a
	// sixth wraps back to the first. Each reply carries the applied name.
	const cycle = 5
	presets := []string{"even-horizontal", "even-vertical", "main-horizontal", "main-vertical", "tiled"}
	seen := map[string]bool{}
	for i := 0; i < cycle; i++ {
		res := h.rpc(s, map[string]any{"action": "select-layout", "layout": "next"})
		if res["error"] != nil {
			t.Fatalf("select-layout next %d: %v", i, res["error"])
		}
		name, _ := res["layout"].(string)
		if name != presets[i] {
			t.Fatalf("next %d applied %q, want %q", i, name, presets[i])
		}
		seen[name] = true
	}
	if len(seen) != cycle {
		t.Fatalf("five next requests visited %d distinct presets %v, want all %d", len(seen), seen, cycle)
	}
	if res := h.rpc(s, map[string]any{"action": "select-layout", "layout": "next"}); res["error"] != nil {
		t.Fatalf("wrapping next: %v", res["error"])
	} else if name, _ := res["layout"].(string); name != "even-horizontal" {
		t.Fatalf("the wrap applied %q, want even-horizontal", name)
	}

	// The stored name rides the state file, a split clears it, and the next
	// next restarts at the first preset.
	if got := h.activeLayout(s); got != "even-horizontal" {
		t.Fatalf("stored layout after the wrap = %q, want even-horizontal", got)
	}
	_ = h.split(s, p2, "right")
	h.waitPanes(s, 5)
	if got := h.activeLayout(s); got != "" {
		t.Fatalf("a split left the stored layout %q, want it cleared", got)
	}
	if res := h.rpc(s, map[string]any{"action": "select-layout", "layout": "next"}); res["error"] != nil {
		t.Fatalf("next after split: %v", res["error"])
	} else if name, _ := res["layout"].(string); name != "even-horizontal" {
		t.Fatalf("next after a split applied %q, want even-horizontal", name)
	}

	// synchronize-panes with no on field toggles: first call turns it on, a
	// write reaches every pane; second call turns it off, a write reaches only
	// the touched pane. The snapshot always carries the flag.
	res := h.rpc(s, map[string]any{"action": "synchronize-panes"})
	if res["sync"] != true {
		t.Fatalf("first toggle sync = %v, want true", res["sync"])
	}
	// Markers stay short on purpose: the panes are a few columns wide after
	// the preset reshapes, and a long line wraps so the needle scatters across
	// two rows and never reads back contiguously.
	onMarker := "SMK-ON"
	h.run("write-to", p1, "-s", s, "--msg", "echo "+onMarker, "--enter")
	waitMarkers(t, h, s, ids, onMarker)

	res = h.rpc(s, map[string]any{"action": "synchronize-panes"})
	if res["sync"] != false {
		t.Fatalf("second toggle sync = %v, want false", res["sync"])
	}
	offMarker := "SMK-OFF"
	h.run("write-to", p1, "-s", s, "--msg", "echo "+offMarker, "--enter")
	h.waitFor(s, p1, offMarker)
	// Sync off: the marker stays out of the other panes of the window.
	other := p2
	out, err := h.try("read", other, "-s", s)
	if err != nil {
		t.Fatalf("read %s: %v", other, err)
	}
	if strings.Contains(out, offMarker) {
		t.Fatalf("an off-flag write reached pane %s: %q shows the marker", other, out)
	}
}
