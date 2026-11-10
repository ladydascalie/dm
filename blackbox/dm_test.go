package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"dm/internal/keymap"
	"dm/internal/vt"
)

// The pane answers an application's feature query and publishes its status.
func TestOSC7501EndToEnd(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "probe")
	root := h.focus("probe")
	p2 := h.split("probe", root, "down")
	h.run("write-to", p2, "-s", "probe", "--msg", h.bin+" __probe --hold 2s", "--enter")

	h.waitFor("probe", p2, "PROBE-REPLY=ok")
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, err := h.try("status", p2, "-s", "probe")
		if err == nil && strings.Contains(out, "working") && strings.Contains(out, "probe reporting") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status never showed working: %q", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.waitFor("probe", p2, "PROBE-CLEARED")
	time.Sleep(200 * time.Millisecond)
	out := h.run("status", p2, "-s", "probe")
	if strings.Contains(out, "working") {
		t.Fatalf("status not cleared: %q", out)
	}
}

// A pane that enters the alternate screen and asks for mouse reporting must
// tell the server, because the client's replay ring may not reach back to the
// enabling sequences. The client uses that to forward the wheel to the app.
func TestAltScreenAndMouseMode(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "m")
	root := h.focus("m")
	h.run("write-to", root, "-s", "m", "--msg",
		`printf '\033[?1049h\033[?1000h'; sleep 2; printf '\033[?1000l\033[?1049l'`, "--enter")

	waitMode(t, h, "m", true, true)
	waitMode(t, h, "m", false, false)
}

// TestToggleFold drives the fold action through a live session.
func TestToggleFold(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "fold")
	unfolded := func() bool {
		tree := h.rpc("fold", map[string]any{"action": "list"})
		snap := tree["snapshot"].(map[string]any)
		all := snap["all"].([]any)
		return all[0].(map[string]any)["windowUnfolded"] == true
	}
	if unfolded() {
		t.Fatal("a new window starts unfolded, want folded")
	}
	if res := h.rpc("fold", map[string]any{"action": "toggle-fold", "target": "w1"}); res["unfolded"] != true {
		t.Fatalf("toggle-fold = %v, want unfolded", res["unfolded"])
	}
	if !unfolded() {
		t.Fatal("snapshot does not show the window unfolded")
	}
	st := h.sessionState("fold")
	if !st.Workspaces[0].Windows[0].Unfolded {
		t.Fatal("fold state not persisted")
	}
	if res := h.rpc("fold", map[string]any{"action": "toggle-fold", "target": "w1"}); res["unfolded"] != false {
		t.Fatalf("second toggle-fold = %v, want folded", res["unfolded"])
	}
}

func TestSetRailAction(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "ra")
	tree := h.rpc("ra", map[string]any{"action": "list"})
	snap := tree["snapshot"].(map[string]any)
	cols := int(snap["cols"].(float64))
	rows := int(snap["rows"].(float64))
	wantDefault := effectiveRail(cols, defaultRail)
	if rail := int(snap["rail"].(float64)); rail != wantDefault {
		t.Fatalf("new session rail = %d, want %d", rail, wantDefault)
	}
	pane := h.focus("ra")
	if x := h.rects("ra")[pane].X; x != paneAreaFor(cols, rows, defaultRail).X+1+pad() {
		t.Fatalf("pane starts at x=%d, want rail+border+padding %d", x,
			paneAreaFor(cols, rows, defaultRail).X+1+pad())
	}

	if res := h.rpc("ra", map[string]any{"action": "set-rail", "index": 0}); res["rail"] != float64(0) {
		t.Fatalf("set-rail 0 = %v", res["rail"])
	}
	if x := h.rects("ra")[pane].X; x != 1+pad() {
		t.Fatalf("hidden rail pane starts at x=%d, want %d", x, 1+pad())
	}

	want := maxRail(cols)
	res := h.rpc("ra", map[string]any{"action": "set-rail", "index": 500})
	if got := int(res["rail"].(float64)); got != want {
		t.Fatalf("set-rail 500 = %d, want clamp %d", got, want)
	}
	if x := h.rects("ra")[pane].X; x != want+1+pad() {
		t.Fatalf("clamped rail pane starts at x=%d, want %d", x, want+1+pad())
	}
}

// TestRailReflow drives set-rail through a live session. The pane rectangle
// and the pane's own persisted size must both follow the rail, so a skipped
// syncSizes fails even though the snapshot alone would look right.
func TestRailReflow(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "reflow")
	pane := h.focus("reflow")
	tree := h.rpc("reflow", map[string]any{"action": "list"})
	snap := tree["snapshot"].(map[string]any)
	cols := int(snap["cols"].(float64))
	rows := int(snap["rows"].(float64))
	rail := int(snap["rail"].(float64))

	check := func(label string, wantRail int) paneRect {
		t.Helper()
		r := h.rects("reflow")[pane]
		area := paneAreaFor(cols, rows, wantRail)
		if r.X != area.X+1+pad() || r.W != area.W-2-2*pad() || r.H != area.H-2-2*pad() {
			t.Fatalf("%s: pane rect %+v, want content of %+v", label, r, area)
		}
		st := h.sessionState("reflow")
		ps := st.Panes[pane]
		if ps == nil {
			t.Fatalf("%s: no state for pane %q", label, pane)
		}
		if ps.Cols != r.W || ps.Rows != r.H {
			t.Fatalf("%s: pane size %dx%d, rect %dx%d", label, ps.Cols, ps.Rows, r.W, r.H)
		}
		return r
	}

	before := check("new session", rail)
	if before.X != rail+1+pad() {
		t.Fatalf("new session pane x = %d, want %d", before.X, rail+1+pad())
	}

	if res := h.rpc("reflow", map[string]any{"action": "set-rail", "index": 0}); res["rail"] != float64(0) {
		t.Fatalf("set-rail 0 = %v", res["rail"])
	}
	hidden := check("hidden", 0)
	if hidden.X != 1+pad() {
		t.Fatalf("hidden pane x = %d, want %d", hidden.X, 1+pad())
	}
	if before.X-hidden.X != rail || hidden.W-before.W != rail {
		t.Fatalf("reflow moved x %d and width %d, want both %d",
			before.X-hidden.X, hidden.W-before.W, rail)
	}

	if res := h.rpc("reflow", map[string]any{"action": "set-rail", "index": defaultRail}); res["rail"] != float64(rail) {
		t.Fatalf("set-rail %d = %v, want effective %d", defaultRail, res["rail"], rail)
	}
	back := check("restored", rail)
	if back != before {
		t.Fatalf("restored rect %+v, want %+v", back, before)
	}
}

// TestZoomSplitWindow pins the snapshot: the zoomed pane fills the pane area,
// every sibling keeps its layout rect, and Zoom names the pane.
func TestZoomSplitWindow(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "zwin")
	left := h.focus("zwin")
	right := h.split("zwin", left, "right")
	before := h.rects("zwin")
	if got := h.zoom("zwin"); got != "" {
		t.Fatalf("a new split starts zoomed on %q", got)
	}

	if got := h.toggleZoom("zwin"); got != right {
		t.Fatalf("zoom-pane = %q, want the focused pane %q", got, right)
	}
	after := h.rects("zwin")
	area := h.paneAreaOf("zwin")
	if want := insetBox(area); after[right] != want {
		t.Fatalf("zoomed rect %+v, want the single-pane box %+v of %+v", after[right], want, area)
	}
	if after[left] != before[left] {
		t.Fatalf("sibling rect moved: %+v, want %+v", after[left], before[left])
	}
	if after[right] == before[right] {
		t.Fatalf("zoom left the rect at %+v", after[right])
	}
	if got := h.zoom("zwin"); got != right {
		t.Fatalf("snapshot zoom = %q, want %q", got, right)
	}
}

// TestZoomSinglePaneNoop pins the one-pane guard: the action replies ok, sets
// no zoom, and leaves the rect alone.
func TestZoomSinglePaneNoop(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "zsolo")
	pane := h.focus("zsolo")
	before := h.rects("zsolo")
	if got := h.toggleZoom("zsolo"); got != "" {
		t.Fatalf("zoom-pane on one pane = %q, want no zoom", got)
	}
	if after := h.rects("zsolo"); after[pane] != before[pane] {
		t.Fatalf("rect = %+v, want %+v", after[pane], before[pane])
	}
}

// TestZoomToggleRestores pins the toggle: a second zoom-pane clears the zoom
// and the split rects come back.
func TestZoomToggleRestores(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "ztoggle")
	left := h.focus("ztoggle")
	right := h.split("ztoggle", left, "right")
	before := h.rects("ztoggle")

	if got := h.toggleZoom("ztoggle"); got != right {
		t.Fatalf("zoom-pane = %q, want %q", got, right)
	}
	if got := h.toggleZoom("ztoggle"); got != "" {
		t.Fatalf("second zoom-pane = %q, want no zoom", got)
	}
	after := h.rects("ztoggle")
	if after[left] != before[left] || after[right] != before[right] {
		t.Fatalf("rects after the toggle = %v, want %v", after, before)
	}
	if got := h.zoom("ztoggle"); got != "" {
		t.Fatalf("snapshot zoom = %q after the toggle off", got)
	}
}

// TestZoomClearsOnFocusChange pins the focus guard: a focus move ends the zoom
// and the split rects return.
func TestZoomClearsOnFocusChange(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "zfocus")
	left := h.focus("zfocus")
	right := h.split("zfocus", left, "right")
	before := h.rects("zfocus")

	if got := h.toggleZoom("zfocus"); got != right {
		t.Fatalf("zoom-pane = %q, want %q", got, right)
	}
	h.rpc("zfocus", map[string]any{"action": "focus", "target": left})
	if got := h.zoom("zfocus"); got != "" {
		t.Fatalf("zoom survived a focus change: %q", got)
	}
	if got := h.rects("zfocus"); got[right] != before[right] || got[left] != before[left] {
		t.Fatalf("rects after the focus change = %v, want %v", got, before)
	}
}

// TestZoomClearsOnSplit pins the split guard: the new pane is visible, so the
// zoom ends and the split pane stops filling the area.
func TestZoomClearsOnSplit(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "zsplit")
	left := h.focus("zsplit")
	right := h.split("zsplit", left, "right")

	if got := h.toggleZoom("zsplit"); got != right {
		t.Fatalf("zoom-pane = %q, want %q", got, right)
	}
	h.split("zsplit", right, "down")
	if got := h.zoom("zsplit"); got != "" {
		t.Fatalf("zoom survived a split: %q", got)
	}
	if got, want := h.rects("zsplit")[right], insetBox(h.paneAreaOf("zsplit")); got == want {
		t.Fatalf("pane %q still fills the area %+v after the split", right, want)
	}
}

// TestZoomClearsOnClosePane pins the close guard: closing a sibling ends the
// zoom, so the zoomed pane returns to its split rect.
func TestZoomClearsOnClosePane(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "zclose")
	a := h.focus("zclose")
	b := h.split("zclose", a, "right")
	c := h.split("zclose", b, "down")

	if got := h.toggleZoom("zclose"); got != c {
		t.Fatalf("zoom-pane = %q, want %q", got, c)
	}
	h.rpc("zclose", map[string]any{"action": "close-pane", "target": a})
	if got := h.zoom("zclose"); got != "" {
		t.Fatalf("zoom survived a close: %q", got)
	}
	if got, want := h.rects("zclose")[c], insetBox(h.paneAreaOf("zclose")); got == want {
		t.Fatalf("pane %q still fills the area %+v after the close", c, want)
	}
}

// TestZoomClearsOnResizeSplit pins the resize-split guard: the drag is a layout
// change, so the zoom ends even though the handler skips afterMutation.
func TestZoomClearsOnResizeSplit(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "zdrag")
	left := h.focus("zdrag")
	right := h.split("zdrag", left, "right")

	if got := h.toggleZoom("zdrag"); got != right {
		t.Fatalf("zoom-pane = %q, want %q", got, right)
	}
	h.rpc("zdrag", map[string]any{"action": "resize-split",
		"target": left, "other": right, "ratio": 0.3})
	if got := h.zoom("zdrag"); got != "" {
		t.Fatalf("zoom survived resize-split: %q", got)
	}
	if got, want := h.rects("zdrag")[right], insetBox(h.paneAreaOf("zdrag")); got == want {
		t.Fatalf("pane %q still fills the area %+v after the drag", right, want)
	}
}

// TestZoomKeepsOnSetRail pins the rail exception: a rail width change keeps the
// zoom and the zoomed pane takes the new area.
func TestZoomKeepsOnSetRail(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "zrail")
	left := h.focus("zrail")
	right := h.split("zrail", left, "right")

	if got := h.toggleZoom("zrail"); got != right {
		t.Fatalf("zoom-pane = %q, want %q", got, right)
	}
	h.rpc("zrail", map[string]any{"action": "set-rail", "index": 0})
	if got := h.zoom("zrail"); got != right {
		t.Fatalf("set-rail cleared the zoom: %q", got)
	}
	want := insetBox(h.paneAreaOf("zrail"))
	if got := h.rects("zrail")[right]; got != want {
		t.Fatalf("zoomed rect %+v after the rail change, want %+v", got, want)
	}
	if want.X != 1+pad() {
		t.Fatalf("hidden rail content starts at %d, want %d", want.X, 1+pad())
	}
}

// TestZoomClearsOnPaneExit pins the death guard: when the zoomed pane's daemon
// dies, the pane leaves the layout and the zoom goes with it.
func TestZoomClearsOnPaneExit(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "zdead")
	left := h.focus("zdead")
	right := h.split("zdead", left, "right")

	if got := h.toggleZoom("zdead"); got != right {
		t.Fatalf("zoom-pane = %q, want %q", got, right)
	}
	pid := readPID(t, filepath.Join(h.stateDir(), "sessions", "zdead", "panes", right+".sock.pid"))
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		zoom := h.zoom("zdead")
		if zoom == "" && len(h.panes("zdead")) == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("zoom %q survived the exit of pane %q", zoom, right)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestDurabilityAcrossServerKill(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "durable")
	root := h.focus("durable")
	h.run("write-to", root, "-s", "durable", "--msg", "echo MARK-DURABLE", "--enter")
	h.waitFor("durable", root, "MARK-DURABLE")

	panePID := readPID(t, filepath.Join(h.stateDir(), "sessions", "durable", "panes", root+".sock.pid"))
	serverPID := readPID(t, filepath.Join(h.stateDir(), "sessions", "durable", "server.pid"))
	if err := syscall.Kill(serverPID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)

	// A fresh RPC must restart the server and adopt the same pane process.
	h.waitFor("durable", root, "MARK-DURABLE")
	if got := readPID(t, filepath.Join(h.stateDir(), "sessions", "durable", "panes", root+".sock.pid")); got != panePID {
		t.Fatalf("pane process changed: %d -> %d", panePID, got)
	}
	if got := readPID(t, filepath.Join(h.stateDir(), "sessions", "durable", "server.pid")); got == serverPID {
		t.Fatal("server was not restarted")
	}

	// Layout survives too.
	h.split("durable", root, "right")
	serverPID = readPID(t, filepath.Join(h.stateDir(), "sessions", "durable", "server.pid"))
	_ = syscall.Kill(serverPID, syscall.SIGKILL)
	time.Sleep(300 * time.Millisecond)
	tree := h.rpc("durable", map[string]any{"action": "list"})
	snap := tree["snapshot"].(map[string]any)
	if n := len(snap["panes"].([]any)); n != 2 {
		t.Fatalf("panes after restart = %d", n)
	}
}

func TestSplitStatusAndRPC(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "s1")
	root := h.focus("s1")
	p2 := h.split("s1", root, "right")
	if p2 == root {
		t.Fatalf("split returned the target pane %q", p2)
	}

	h.run("write-to", p2, "-s", "s1", "--msg", "echo FROM-P2", "--enter")
	h.waitFor("s1", p2, "FROM-P2")

	lines := h.run("read", p2, "-s", "s1")
	if !strings.Contains(lines, "FROM-P2") {
		t.Fatalf("read p2: %q", lines)
	}

	// The same action as JSON, exactly as an agent would send it.
	h.run("exec", `{"action":"write-to","target":"`+root+`","msg":"echo FROM-JSON","enter":true}`, "-s", "s1")
	h.waitFor("s1", root, "FROM-JSON")

	h.run("write-to", root, "-s", "s1", "--msg",
		`printf '\033]7501;{"state":"blocked","message":"needs review"}\007'`, "--enter")
	// The pane runs the operator's login shell, so its first command can wait
	// behind shell startup. Use the same budget as waitFor.
	deadline := time.Now().Add(waitBudget)
	for {
		out := h.run("status", root, "-s", "s1")
		if strings.Contains(out, "blocked") && strings.Contains(out, "needs review") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status never updated: %q", out)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Focus movement by direction.
	h.run("focus", root, "-s", "s1")
	h.run("exec", `{"action":"focus-dir","dir":"right"}`, "-s", "s1")
	tree := h.rpc("s1", map[string]any{"action": "list"})
	if snap := tree["snapshot"].(map[string]any); snap["focus"] != p2 {
		t.Fatalf("focus = %v", snap["focus"])
	}

	// Closing a pane prunes the split and keeps the session usable.
	h.run("exec", `{"action":"close-pane","target":"`+p2+`"}`, "-s", "s1")
	tree = h.rpc("s1", map[string]any{"action": "list"})
	if snap := tree["snapshot"].(map[string]any); len(snap["panes"].([]any)) != 1 {
		t.Fatalf("panes = %v", snap["panes"])
	}
}

// A pane that spoke the status protocol stays an agent after state=clear. The
// mark persists, so a restart cannot demote it back to a shell either.
func TestAgentStaysAfterClear(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "sticky")
	pane := h.focus("sticky")

	agent := func() bool {
		tree := h.rpc("sticky", map[string]any{"action": "list"})
		snap := tree["snapshot"].(map[string]any)
		all, _ := snap["all"].([]any)
		if len(all) == 0 {
			t.Fatalf("snapshot has no all rows: %v", snap)
		}
		return all[0].(map[string]any)["agent"] == true
	}
	waitAgent := func(label string, want bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if agent() == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: agent != %v", label, want)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitAgent("fresh shell", false)

	h.run("write-to", pane, "-s", "sticky", "--msg",
		`printf '\033]7501;state=working:app=pi\007'`, "--enter")
	waitAgent("reported working", true)
	app := func() string {
		tree := h.rpc("sticky", map[string]any{"action": "list"})
		snap := tree["snapshot"].(map[string]any)
		all := snap["all"].([]any)
		s, _ := all[0].(map[string]any)["app"].(string)
		return s
	}
	deadline := time.Now().Add(5 * time.Second)
	for app() != "pi" {
		if time.Now().After(deadline) {
			t.Fatalf("app never recorded")
		}
		time.Sleep(50 * time.Millisecond)
	}

	h.run("write-to", pane, "-s", "sticky", "--msg",
		`printf '\033]7501;state=clear\007'`, "--enter")
	deadline = time.Now().Add(5 * time.Second)
	for {
		out := h.run("status", pane, "-s", "sticky")
		if strings.Contains(out, "unknown") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("clear never arrived: %q", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	waitAgent("after clear", true)

	if got := app(); got != "pi" {
		t.Fatalf("app after clear = %q, want pi", got)
	}
	ps := h.sessionState("sticky").Panes[pane]
	if ps == nil || !ps.Reported || ps.App != "pi" {
		t.Fatalf("agent mark not persisted: %+v", ps)
	}
}

// TestCloseLastPaneDropsWindow closes a window's only pane. The window goes
// away and the session falls back to its other workspace instead of refusing.
func TestCloseLastPaneDropsWindow(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "last")
	h.run("exec", `{"action":"new-workspace","name":"two"}`, "-s", "last")
	h.run("exec", `{"action":"close-pane"}`, "-s", "last")
	deadline := time.Now().Add(5 * time.Second)
	for {
		tree := h.rpc("last", map[string]any{"action": "list"})
		snap := tree["snapshot"].(map[string]any)
		panes := snap["panes"].([]any)
		if snap["workspace"] == "main" && len(panes) == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("close of the last pane left %v panes in %v", len(panes), snap["workspace"])
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A shell that exits must take its split with it and leave focus on a live
// pane. Leaving a dead rect behind makes the whole client unusable.
func TestPaneExitPrunesSplit(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "x")
	root := h.focus("x")
	p2 := h.split("x", root, "right")
	h.waitPanes("x", 2)

	h.run("write-to", p2, "-s", "x", "--msg", "exit", "--enter")
	h.waitPanes("x", 1)

	h.run("write-to", root, "-s", "x", "--msg", "echo STILL-ALIVE", "--enter")
	h.waitFor("x", root, "STILL-ALIVE")

	tree := h.rpc("x", map[string]any{"action": "list"})
	snap := tree["snapshot"].(map[string]any)
	if snap["focus"] != root {
		t.Fatalf("focus = %v", snap["focus"])
	}

	// The last pane takes the session with it, like tmux.
	panePID := readPID(t, filepath.Join(h.stateDir(), "sessions", "x", "panes", root+".sock.pid"))
	h.run("write-to", root, "-s", "x", "--msg", "exit", "--enter")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(h.stateDir(), "sessions", "x")); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session x did not end")
		}
		time.Sleep(100 * time.Millisecond)
	}
	deadline = time.Now().Add(5 * time.Second)
	for syscall.Kill(panePID, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("pane process %d still alive", panePID)
		}
		time.Sleep(100 * time.Millisecond)
	}
	out := h.run("ls", "--json")
	if strings.Contains(out, `"x"`) {
		t.Fatalf("session x still listed: %s", out)
	}
}

// TestWindowAutoName labels a window after the folder it was created in.
func TestWindowAutoName(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "auto")
	cwd, _ := os.Getwd()
	want := folderName(cwd)
	windows := func() []string {
		tree := h.rpc("auto", map[string]any{"action": "list"})
		snap := tree["snapshot"].(map[string]any)
		raw := snap["windows"].([]any)
		out := make([]string, len(raw))
		for i, w := range raw {
			out[i] = w.(map[string]any)["name"].(string)
		}
		return out
	}
	if got := windows(); len(got) != 1 || got[0] != want {
		t.Fatalf("initial windows = %v, want [%q]", got, want)
	}

	sub := filepath.Join(t.TempDir(), "proj-x")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	res := h.rpc("auto", map[string]any{"action": "new-window", "cwd": sub})
	if got := windows(); len(got) != 2 || got[1] != "proj-x" {
		t.Fatalf("new window name = %v, want proj-x", got)
	}
	ps := h.sessionState("auto").Panes[res["pane"].(string)]
	if ps == nil || ps.Cwd != sub {
		t.Fatalf("new window pane cwd = %+v, want %q", ps, sub)
	}

	// An explicit name still wins.
	h.rpc("auto", map[string]any{"action": "new-window", "name": "tests", "cwd": sub})
	if got := windows(); got[len(got)-1] != "tests" {
		t.Fatalf("explicit window name = %v", got)
	}

	// The TUI sends the focused pane as the target; the new window still lands
	// in that pane's folder and takes its name.
	pane := h.focus("auto")
	h.rpc("auto", map[string]any{"action": "new-window", "target": pane})
	if got := windows(); got[len(got)-1] != "proj-x" {
		t.Fatalf("new window from pane %s = %v, want proj-x", pane, got)
	}

	// A new workspace's first window takes the folder name too.
	h.rpc("auto", map[string]any{"action": "new-workspace", "name": "other", "cwd": sub})
	if got := windows(); len(got) != 1 || got[0] != "proj-x" {
		t.Fatalf("new workspace window = %v, want proj-x", got)
	}
}

// TestNewPaneFollowsShellCwd opens a new pane where its source pane's shell is
// now, not where that pane was born: dm launched from /a, the pane cd'd to /b,
// so a new window, workspace or split belongs to /b.
func TestNewPaneFollowsShellCwd(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "cwd")
	root := h.focus("cwd")

	now := filepath.Join(t.TempDir(), "now")
	if err := os.MkdirAll(now, 0o755); err != nil {
		t.Fatal(err)
	}
	h.run("write-to", root, "-s", "cwd", "--msg", "cd "+now, "--enter")
	deadline := time.Now().Add(waitBudget)
	for {
		ps := h.sessionState("cwd").Panes[root]
		if ps != nil && ps.Cwd == now {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane %s never recorded %q", root, now)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// A workspace takes the folder the active pane stands in, and so does the
	// window the workspace is born with.
	h.rpc("cwd", map[string]any{"action": "new-workspace", "name": "second"})
	st := h.sessionState("cwd")
	if len(st.Workspaces) != 2 {
		t.Fatalf("workspaces = %d, want 2", len(st.Workspaces))
	}
	ws := st.Workspaces[1]
	if id := ws.Windows[0].Focus; st.Panes[id] == nil || st.Panes[id].Cwd != now {
		t.Fatalf("new workspace pane cwd = %+v, want %q", st.Panes[id], now)
	}
	if name := ws.Windows[0].Name; name != "now" {
		t.Fatalf("new workspace window name = %q, want now", name)
	}

	// A split inherits the same folder, whichever workspace its target is in.
	res := h.rpc("cwd", map[string]any{"action": "split", "target": root, "dir": "right"})
	split := res["pane"].(string)
	if ps := h.sessionState("cwd").Panes[split]; ps == nil || ps.Cwd != now {
		t.Fatalf("split pane cwd = %+v, want %q", ps, now)
	}
}

func TestWindowsAndWorkspaces(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "w1")
	h.run("exec", `{"action":"new-window"}`, "-s", "w1")
	h.run("exec", `{"action":"new-workspace","name":"other"}`, "-s", "w1")
	tree := h.rpc("w1", map[string]any{"action": "list"})
	snap := tree["snapshot"].(map[string]any)
	if snap["workspace"] != "other" {
		t.Fatalf("workspace = %v", snap["workspace"])
	}
	h.run("exec", `{"action":"prev-workspace"}`, "-s", "w1")
	tree = h.rpc("w1", map[string]any{"action": "list"})
	snap = tree["snapshot"].(map[string]any)
	if snap["workspace"] != "main" || len(snap["windows"].([]any)) != 2 {
		t.Fatalf("snapshot %v", snap)
	}
}

// History returns the rows above the live screen, oldest first, using the
// same reachable window as the client's scrollback.
func TestPaneHistory(t *testing.T) {
	h := newHarness(t)

	h.run("new", "-s", "hist")
	root := h.focus("hist")
	h.run("write-to", root, "-s", "hist", "--msg",
		`for i in $(seq 1 80); do echo HIST-$(printf %03d $i); done`, "--enter")
	h.waitFor("hist", root, "HIST-080")
	time.Sleep(200 * time.Millisecond)

	screen := h.run("read", root, "-s", "hist")
	if strings.Contains(screen, "HIST-001") {
		t.Fatalf("HIST-001 still on screen:\n%s", screen)
	}

	// Enough history for the requested window: exact count, oldest first.
	hist := h.history("hist", root, 20)
	if len(hist) != 20 {
		t.Fatalf("history returned %d rows, want 20:\n%v", len(hist), hist)
	}
	last, numbered := 0, 0
	for _, line := range hist {
		var num int
		if _, err := fmt.Sscanf(line, "HIST-%d", &num); err == nil {
			numbered++
			if num <= last {
				t.Fatalf("history not oldest first at %q:\n%v", line, hist)
			}
			last = num
		}
	}
	if numbered != 20 {
		t.Fatalf("history returned %d numbered rows, want 20:\n%v", numbered, hist)
	}
	if last >= 80 {
		t.Fatalf("history window does not end above the live screen: last=%d\n%v", last, hist)
	}

	// A pane in a background workspace keeps its own PTY size. The tall replay
	// must use that, not the inactive window's layout rect.
	bgRes := h.rpc("hist", map[string]any{
		"action": "new-workspace", "name": "bg",
		"window": "t", "cwd": "/tmp", "nofocus": true,
	})
	bg, _ := bgRes["pane"].(string)
	if bg == "" {
		t.Fatalf("background pane: %v", bgRes)
	}
	h.run("write-to", bg, "-s", "hist", "--msg",
		`for i in $(seq 1 60); do echo BG-$(printf %03d $i); done`, "--enter")
	h.waitFor("hist", bg, "BG-060")
	time.Sleep(200 * time.Millisecond)
	bgHist := h.history("hist", bg, 5)
	if len(bgHist) != 5 {
		t.Fatalf("background history rows = %d, want 5: %v", len(bgHist), bgHist)
	}
	lastBg := 0
	_, _ = fmt.Sscanf(bgHist[len(bgHist)-1], "BG-%d", &lastBg)
	if lastBg < 30 || lastBg > 45 {
		t.Fatalf("background history window is not next to the screen: last=%d\n%v", lastBg, bgHist)
	}

	// A wide window reaches the first row; -s before the pane id works.
	wide := h.run("read", "-s", "hist", root, "--scrollback", "100")
	if !strings.Contains(wide, "HIST-001") {
		t.Fatalf("history missing HIST-001:\n%s", wide)
	}

	if out, err := h.try("read", root, "-s", "hist", "--scrollback", "0"); err == nil {
		t.Fatalf("--scrollback 0 accepted: %q", out)
	}
	if out, err := h.try("exec", `{"action":"history","target":"`+root+`","count":-1}`, "-s", "hist"); err == nil {
		t.Fatalf("negative lines accepted: %q", out)
	}
	if out, err := h.try("read", "nope", "-s", "hist", "--scrollback", "5"); err == nil {
		t.Fatalf("unknown pane accepted: %q", out)
	}

	// Leading blank rows are not reachable, like the client's scrollStart.
	h.run("new", "-s", "histb")
	root = h.focus("histb")
	h.run("write-to", root, "-s", "histb", "--msg",
		`printf '\033[H\033[2J\033[10;1H'; for i in $(seq 1 80); do echo BLANK-$(printf %03d $i); done`, "--enter")
	h.waitFor("histb", root, "BLANK-080")
	time.Sleep(200 * time.Millisecond)
	blank := h.history("histb", root, 500)
	if len(blank) == 0 || strings.TrimSpace(blank[0]) == "" {
		t.Fatalf("history includes leading blank rows:\n%q", blank)
	}

	// A blank row immediately above the live screen is part of the window.
	h.run("new", "-s", "histg")
	root = h.focus("histg")
	rows := h.rects("histg")[root].H
	// sh -c keeps the shell prompt out of the picture until the sleep ends.
	// One line short of a full screen: the final newline scrolls twice, so the
	// blank row printed after TOP is the row immediately above the live screen.
	cmd := fmt.Sprintf(`sh -c "printf '\033[H\033[2J'; echo TOP; echo; `+
		`for i in \$(seq 1 %d); do echo TAIL-\$(printf %%03d \$i); done; `+
		`printf '\033[%d;1H'; sleep 15"`, rows-1, rows+2)
	h.run("write-to", root, "-s", "histg", "--msg", cmd, "--enter")
	h.waitFor("histg", root, fmt.Sprintf("TAIL-%03d", rows-1))
	time.Sleep(200 * time.Millisecond)
	if got := h.history("histg", root, 1); len(got) != 1 || got[0] != "" {
		t.Fatalf("blank row above the live screen not returned: %q", got)
	}

	// A pane with no scrollback answers empty, not an error.
	h.run("new", "-s", "short")
	root = h.focus("short")
	if got := h.history("short", root, 5); len(got) != 0 {
		t.Fatalf("short pane returned %v", got)
	}

	// The client refuses scrollback on the alternate screen; so does the server.
	h.run("new", "-s", "alt")
	root = h.focus("alt")
	h.run("write-to", root, "-s", "alt", "--msg", `printf '\033[?1049h'; sleep 3`, "--enter")
	waitMode(t, h, "alt", true, false)
	if out, err := h.try("exec", `{"action":"history","target":"`+root+`","count":5}`, "-s", "alt"); err == nil {
		t.Fatalf("alt pane returned history: %q", out)
	}
}

// A nested drag resizes the outer split, not the inner one, and clamps.
func TestResizeSplit(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "rs")
	// This test measures the pane area exactly. Hide the rail, which a new
	// session otherwise asks for, so the wrapper stays rail-zero.
	h.run("exec", `{"action":"set-rail","index":0}`, "-s", "rs")
	p1 := h.focus("rs")
	p2 := h.split("rs", p1, "right")
	p3 := h.split("rs", p1, "down")
	if focus := h.focus("rs"); focus != p3 {
		t.Fatalf("focus = %q, want %q", focus, p3)
	}

	before := h.rects("rs")
	h.run("exec", `{"action":"resize-split","target":"`+p1+`","other":"`+p2+`","ratio":0.25}`, "-s", "rs")
	after := h.rects("rs")
	if after[p2].W <= before[p2].W || after[p1].W >= before[p1].W {
		t.Fatalf("outer widths did not move: before=%v after=%v", before, after)
	}
	if after[p1].H != before[p1].H || after[p3].H != before[p3].H {
		t.Fatalf("inner heights moved with the outer drag: before=%v after=%v", before, after)
	}
	if focus := h.focus("rs"); focus != p3 {
		t.Fatalf("resize moved focus to %q", focus)
	}

	// The inner split still resizes along its own axis.
	before = after
	h.run("exec", `{"action":"resize-split","target":"`+p1+`","other":"`+p3+`","ratio":0.7}`, "-s", "rs")
	after = h.rects("rs")
	if after[p1].H == before[p1].H || after[p3].H == before[p3].H {
		t.Fatalf("inner heights did not move: before=%v after=%v", before, after)
	}
	if after[p1].W != before[p1].W || after[p2].W != before[p2].W {
		t.Fatalf("inner drag changed widths: before=%v after=%v", before, after)
	}

	// Ratios 0 and 1 clamp the squeezed side to exactly three cells. Leaving
	// both sides merely >= 3 would also pass an even split without a clamp.
	h.run("exec", `{"action":"resize-split","target":"`+p1+`","other":"`+p2+`","ratio":0}`, "-s", "rs")
	got := h.rects("rs")
	if got[p1].W != 3 || got[p2].W <= 3 {
		t.Fatalf("ratio 0 width: %v", got)
	}
	h.run("exec", `{"action":"resize-split","target":"`+p1+`","other":"`+p2+`","ratio":1}`, "-s", "rs")
	got = h.rects("rs")
	if got[p2].W != 3 || got[p1].W <= 3 {
		t.Fatalf("ratio 1 width: %v", got)
	}
	h.run("exec", `{"action":"resize-split","target":"`+p1+`","other":"`+p3+`","ratio":0}`, "-s", "rs")
	got = h.rects("rs")
	if got[p1].H != 3 || got[p3].H <= 3 {
		t.Fatalf("ratio 0 height: %v", got)
	}
	h.run("exec", `{"action":"resize-split","target":"`+p1+`","other":"`+p3+`","ratio":1}`, "-s", "rs")
	got = h.rects("rs")
	if got[p3].H != 3 || got[p1].H <= 3 {
		t.Fatalf("ratio 1 height: %v", got)
	}
	if focus := h.focus("rs"); focus != p3 {
		t.Fatalf("resize moved focus to %q", focus)
	}

	if out, err := h.try("exec", `{"action":"resize-split","target":"nope","other":"`+p2+`","ratio":0.5}`, "-s", "rs"); err == nil {
		t.Fatalf("unknown pane accepted: %q", out)
	}

	// Same-axis nesting: row{ q1, row{q2,q3} }. The outer divider spans the
	// whole row, so the minimum is exactly 3 content cells, not the leaf
	// bounding box. A pane box is border + padding + content + padding +
	// border, so a box costs 2+2*pad cells around its content.
	h.run("new", "-s", "rs2")
	h.run("exec", `{"action":"set-rail","index":0}`, "-s", "rs2")
	q1 := h.focus("rs2")
	q2 := h.split("rs2", q1, "right")
	q3 := h.split("rs2", q2, "right")
	tree := h.rpc("rs2", map[string]any{"action": "list"})
	cols := int(tree["snapshot"].(map[string]any)["cols"].(float64))
	pad := keymap.LoadDefault().Padding()
	overhead := 2 + 2*pad // border and padding around one pane's content
	h.run("exec", `{"action":"resize-split","target":"`+q1+`","other":"`+q2+`","ratio":0}`, "-s", "rs2")
	got = h.rects("rs2")
	if got[q1].W != 3 {
		t.Fatalf("outer minimum with same-axis nesting = %v, want 3", got)
	}
	if want := cols - 3 - 3*overhead; got[q2].W+got[q3].W != want {
		t.Fatalf("outer right side width: %v, want %d", got, want)
	}
	h.run("exec", `{"action":"resize-split","target":"`+q1+`","other":"`+q2+`","ratio":1}`, "-s", "rs2")
	got = h.rects("rs2")
	if want := cols - 3 - 2*overhead; got[q1].W != want {
		t.Fatalf("outer maximum with same-axis nesting: %v, want %d", got, want)
	}
}

// The pane action returns canonical ANSI for a live shell pane, not the ring.
func TestScreenAction(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "snap")
	pane := h.focus("snap")
	h.run("write-to", pane, "-s", "snap", "--msg", "echo SNAP-MARK", "--enter")
	h.waitFor("snap", pane, "SNAP-MARK")
	t.Setenv("DM_STATE", h.stateDir())

	m, err := paneRequest("snap", pane, paneMsg{Type: "req", Action: "screen"})
	if err != nil {
		t.Fatalf("screen: %v", err)
	}
	if m.Type != "screen" || len(m.VTSnap) == 0 {
		t.Fatalf("screen reply type=%q snapshot=%d", m.Type, len(m.VTSnap))
	}
	snap, err := vt.DecodeSnapshot(m.VTSnap)
	if err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	rect := h.rects("snap")[pane]
	term := vt.NewTerm(rect.W, rect.H)
	if err := term.Restore(snap); err != nil {
		t.Fatalf("restore snapshot: %v", err)
	}
	got := term.Grid().Text(0)
	found := false
	for y := 0; y < rect.H; y++ {
		if strings.Contains(term.Grid().Text(y), "SNAP-MARK") {
			found = true
		}
	}
	if !found {
		t.Fatalf("screen snapshot lost the pane text, row 0: %s", got)
	}
}

// TestZoomExecReachesTheServer drives the zoom-pane action the client sends
// through dm exec, so the CLI path and the handler agree.
func TestZoomExecReachesTheServer(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "zx")
	left := h.focus("zx")
	right := h.split("zx", left, "right")
	h.run("exec", `{"action":"zoom-pane"}`, "-s", "zx")
	if z := h.zoom("zx"); z != right {
		t.Fatalf("dm exec zoom-pane zoomed %q, want the focused %q", z, right)
	}
	h.run("exec", `{"action":"zoom-pane"}`, "-s", "zx")
	if z := h.zoom("zx"); z != "" {
		t.Fatalf("a second dm exec zoom-pane left %q zoomed", z)
	}
}
