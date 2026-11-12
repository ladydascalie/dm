package blackbox

import (
	"testing"
	"time"
)

// waitLayout polls the list snapshot until the named window's row carries the
// layout name, because a select-layout reply lands before the broadcast every
// client sees.
func waitLayout(t *testing.T, h *harness, session, window, want string) {
	t.Helper()
	deadline := time.Now().Add(waitBudget)
	for {
		tree := h.rpc(session, map[string]any{"action": "list"})
		snap := tree["snapshot"].(map[string]any)
		for _, raw := range snap["windows"].([]any) {
			w := raw.(map[string]any)
			if w["id"] != window {
				continue
			}
			got, _ := w["layout"].(string)
			if got == want {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("window %s never carried layout %q within the %s wait budget: %v",
				window, want, waitBudget, snap["windows"])
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestSelectLayoutSnapshotField proves the snapshot field end to end through
// the binary: select-layout stores the preset name on the window and the next
// list snapshot carries it, a split clears it, and a second select flips the
// field to the new preset.
func TestSelectLayoutSnapshotField(t *testing.T) {
	h := newHarness(t)
	s := "laysnap"
	h.run("new", "-s", s)
	p1 := h.focus(s)
	h.split(s, p1, "right")
	h.waitPanes(s, 2)

	tree := h.rpc(s, map[string]any{"action": "list"})
	w1 := tree["snapshot"].(map[string]any)["windows"].([]any)[0].(map[string]any)
	window, _ := w1["id"].(string)
	if got, _ := w1["layout"].(string); got != "" {
		t.Fatalf("a custom window starts with layout %q, want empty", got)
	}

	// select-layout stores the name on the window and the snapshot carries it.
	res := h.rpc(s, map[string]any{"action": "select-layout", "layout": "main-vertical"})
	if res["error"] != nil {
		t.Fatalf("select-layout main-vertical: %v", res["error"])
	}
	if st := h.sessionState(s); st.Workspaces[0].Windows[0].Layout != "main-vertical" {
		t.Fatalf("state file stores %q, want main-vertical", st.Workspaces[0].Windows[0].Layout)
	}
	waitLayout(t, h, s, window, "main-vertical")

	// A split is a manual tree change: the stored name clears and the next
	// snapshot carries empty.
	h.split(s, p1, "down")
	if st := h.sessionState(s); st.Workspaces[0].Windows[0].Layout != "" {
		t.Fatalf("split left the stored layout %q", st.Workspaces[0].Windows[0].Layout)
	}
	waitLayout(t, h, s, window, "")

	// A second select flips the field to the new preset.
	if res := h.rpc(s, map[string]any{"action": "select-layout", "layout": "tiled"}); res["error"] != nil {
		t.Fatalf("select-layout tiled: %v", res["error"])
	}
	waitLayout(t, h, s, window, "tiled")
}
