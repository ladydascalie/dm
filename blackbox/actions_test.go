package blackbox

// The window, workspace and pane actions the earlier tests never drove: the
// cycle and select family, rename-window, move-window, move-workspace,
// close-workspace, keys, replay and float-close. Each proof reads the result
// back from the snapshot or the state file, not from the reply alone.

import (
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dm/internal/wire"
)

// snapOf is the session's current list snapshot.
func snapOf(t *testing.T, h *harness, session string) map[string]any {
	t.Helper()
	tree := h.rpc(session, map[string]any{"action": "list"})
	return tree["snapshot"].(map[string]any)
}

// windowRows is the snapshot's windows as plain maps, in rail order.
func windowRows(t *testing.T, snap map[string]any) []map[string]any {
	t.Helper()
	raw, _ := snap["windows"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.(map[string]any))
	}
	return out
}

func snapInt(snap map[string]any, key string) int {
	n, _ := snap[key].(float64)
	return int(n)
}

func snapStr(snap map[string]any, key string) string {
	s, _ := snap[key].(string)
	return s
}

// TestWindowActions drives the window family end to end: new-window focuses
// its pane, the cycle and select actions move the active window, rename-window
// and move-window land in the state file, and close-window kills the window's
// panes and refuses the last window of the session.
func TestWindowActions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "wins")
	p1 := h.focus("wins")
	w1 := h.sessionState("wins").Workspaces[0].Windows[0].ID

	res := h.rpc("wins", map[string]any{"action": "new-window"})
	w2, _ := res["window"].(string)
	p2, _ := res["pane"].(string)
	if w2 == "" || p2 == "" {
		t.Fatalf("new-window = %v, want a window and a pane", res)
	}
	snap := snapOf(t, h, "wins")
	if got := snapInt(snap, "activeWindow"); got != 1 {
		t.Fatalf("after new-window activeWindow = %d, want 1", got)
	}
	if focus := snapStr(snap, "focus"); focus != p2 {
		t.Fatalf("after new-window focus = %q, want the new pane %q", focus, p2)
	}
	if n := len(windowRows(t, snap)); n != 2 {
		t.Fatalf("after new-window windows = %d, want 2", n)
	}

	// The cycle wraps: next from the last window lands on the first, and the
	// focus follows the window it lands in.
	h.rpc("wins", map[string]any{"action": "next-window"})
	snap = snapOf(t, h, "wins")
	if got := snapInt(snap, "activeWindow"); got != 0 {
		t.Fatalf("after next-window activeWindow = %d, want 0", got)
	}
	if focus := snapStr(snap, "focus"); focus != p1 {
		t.Fatalf("after next-window focus = %q, want %q", focus, p1)
	}
	h.rpc("wins", map[string]any{"action": "prev-window"})
	if got := snapInt(snapOf(t, h, "wins"), "activeWindow"); got != 1 {
		t.Fatalf("after prev-window activeWindow = %d, want 1", got)
	}

	h.rpc("wins", map[string]any{"action": "select-window", "index": 0})
	if got := snapInt(snapOf(t, h, "wins"), "activeWindow"); got != 0 {
		t.Fatalf("after select-window 0 activeWindow = %d, want 0", got)
	}
	if out, err := h.try("exec", `{"action":"select-window","index":7}`, "-s", "wins"); err == nil {
		t.Fatalf("select-window 7 accepted: %s", out)
	}

	h.rpc("wins", map[string]any{"action": "focus-window", "target": w2})
	snap = snapOf(t, h, "wins")
	if got := snapInt(snap, "activeWindow"); got != 1 {
		t.Fatalf("after focus-window activeWindow = %d, want 1", got)
	}
	if focus := snapStr(snap, "focus"); focus != p2 {
		t.Fatalf("after focus-window focus = %q, want %q", focus, p2)
	}
	if out, err := h.try("exec", `{"action":"focus-window","target":"nope"}`, "-s", "wins"); err == nil {
		t.Fatalf("focus-window of an unknown window accepted: %s", out)
	}

	h.rpc("wins", map[string]any{"action": "rename-window", "target": w2, "name": "build"})
	rows := windowRows(t, snapOf(t, h, "wins"))
	if name, _ := rows[1]["name"].(string); name != "build" {
		t.Fatalf("snapshot window name = %q, want build", name)
	}
	if name := h.sessionState("wins").Workspaces[0].Windows[1].Name; name != "build" {
		t.Fatalf("state file window name = %q, want build", name)
	}

	// move-window swaps the slice order, and the active window follows the
	// window it named through the swap.
	h.rpc("wins", map[string]any{"action": "move-window", "target": w2, "dir": "up"})
	st := h.sessionState("wins")
	if id := st.Workspaces[0].Windows[0].ID; id != w2 {
		t.Fatalf("after move-window up the first window is %q, want %q", id, w2)
	}
	if id := st.Workspaces[0].Windows[1].ID; id != w1 {
		t.Fatalf("after move-window up the second window is %q, want %q", id, w1)
	}
	snap = snapOf(t, h, "wins")
	if got := snapInt(snap, "activeWindow"); got != 0 {
		t.Fatalf("after move-window activeWindow = %d, want the moved window's new index 0", got)
	}
	raw, _ := json.Marshal(map[string]any{"action": "move-window", "target": w2, "dir": "left"})
	if out, err := h.try("exec", string(raw), "-s", "wins"); err == nil {
		t.Fatalf("move-window dir left accepted: %s", out)
	}

	// close-window takes the window's panes with it.
	h.rpc("wins", map[string]any{"action": "close-window", "target": w2})
	st = h.sessionState("wins")
	if n := len(st.Workspaces[0].Windows); n != 1 {
		t.Fatalf("after close-window windows = %d, want 1", n)
	}
	if id := st.Workspaces[0].Windows[0].ID; id != w1 {
		t.Fatalf("after close-window the surviving window is %q, want %q", id, w1)
	}
	snap = snapOf(t, h, "wins")
	if focus := snapStr(snap, "focus"); focus != p1 {
		t.Fatalf("after close-window focus = %q, want %q", focus, p1)
	}
	for _, id := range snapStrs(snap["all"]) {
		if id == p2 {
			t.Fatalf("pane %q survived close-window: %v", p2, snap["all"])
		}
	}
	raw, _ = json.Marshal(map[string]any{"action": "close-window", "target": w1})
	if out, err := h.try("exec", string(raw), "-s", "wins"); err == nil {
		t.Fatalf("close-window of the last window accepted: %s", out)
	}
	if focus := snapStr(snapOf(t, h, "wins"), "focus"); focus != p1 {
		t.Fatalf("the refused close-window killed the session: focus %q", focus)
	}
}

// snapStrs is the pane id of every row in the snapshot's all list.
func snapStrs(raw any) []string {
	all, _ := raw.([]any)
	out := make([]string, 0, len(all))
	for _, r := range all {
		id, _ := r.(map[string]any)["id"].(string)
		out = append(out, id)
	}
	return out
}

// TestWorkspaceActions drives the workspace family end to end: new-workspace
// focuses the workspace it opens, the cycle and focus actions move the active
// workspace, move-workspace swaps the rail order, and close-workspace kills
// the workspace's panes and refuses the last one.
func TestWorkspaceActions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "wss")
	_ = h.focus("wss")
	ws1 := h.sessionState("wss").Workspaces[0].ID
	var raw []byte

	res := h.rpc("wss", map[string]any{"action": "new-workspace", "name": "beta"})
	ws2, _ := res["workspace"].(string)
	p2, _ := res["pane"].(string)
	if ws2 == "" || p2 == "" {
		t.Fatalf("new-workspace = %v, want a workspace and a pane", res)
	}
	if got := snapStr(snapOf(t, h, "wss"), "workspace"); got != "beta" {
		t.Fatalf("after new-workspace workspace = %q, want beta", got)
	}

	// The cycle wraps both ways with two workspaces.
	h.rpc("wss", map[string]any{"action": "next-workspace"})
	if got := snapStr(snapOf(t, h, "wss"), "workspace"); got != "main" {
		t.Fatalf("after next-workspace workspace = %q, want main", got)
	}
	h.rpc("wss", map[string]any{"action": "next-workspace"})
	if got := snapStr(snapOf(t, h, "wss"), "workspace"); got != "beta" {
		t.Fatalf("after the second next-workspace workspace = %q, want beta", got)
	}

	h.rpc("wss", map[string]any{"action": "focus-workspace", "target": ws1})
	if got := snapStr(snapOf(t, h, "wss"), "workspace"); got != "main" {
		t.Fatalf("after focus-workspace workspace = %q, want main", got)
	}
	if out, err := h.try("exec", `{"action":"focus-workspace","target":"nope"}`, "-s", "wss"); err == nil {
		t.Fatalf("focus-workspace of an unknown workspace accepted: %s", out)
	}

	// move-workspace swaps the rail order and the active index follows the
	// workspace it named, so beta stays the one on screen.
	h.rpc("wss", map[string]any{"action": "focus-workspace", "target": ws2})
	h.rpc("wss", map[string]any{"action": "move-workspace", "target": ws2, "dir": "up"})
	st := h.sessionState("wss")
	if id := st.Workspaces[0].ID; id != ws2 {
		t.Fatalf("after move-workspace up the first workspace is %q, want %q", id, ws2)
	}
	if st.Active != 0 {
		t.Fatalf("after move-workspace active = %d, want 0", st.Active)
	}
	if got := snapStr(snapOf(t, h, "wss"), "workspace"); got != "beta" {
		t.Fatalf("after move-workspace workspace = %q, want beta", got)
	}
	raw, _ = json.Marshal(map[string]any{"action": "move-workspace", "target": ws2, "dir": "sideways"})
	if out, err := h.try("exec", string(raw), "-s", "wss"); err == nil {
		t.Fatalf("move-workspace dir sideways accepted: %s", out)
	}

	// close-workspace takes the background workspace's panes with it.
	h.rpc("wss", map[string]any{"action": "close-workspace", "target": ws1})
	st = h.sessionState("wss")
	if n := len(st.Workspaces); n != 1 {
		t.Fatalf("after close-workspace workspaces = %d, want 1", n)
	}
	if id := st.Workspaces[0].ID; id != ws2 {
		t.Fatalf("after close-workspace the surviving workspace is %q, want %q", id, ws2)
	}
	if ids := snapStrs(snapOf(t, h, "wss")["all"]); len(ids) != 1 || ids[0] != p2 {
		t.Fatalf("after close-workspace panes = %v, want only %q", ids, p2)
	}
	raw, _ = json.Marshal(map[string]any{"action": "close-workspace", "target": ws2})
	if out, err := h.try("exec", string(raw), "-s", "wss"); err == nil {
		t.Fatalf("close-workspace of the last workspace accepted: %s", out)
	}
	if got := snapStr(snapOf(t, h, "wss"), "workspace"); got != "beta" {
		t.Fatalf("the refused close-workspace killed the session: workspace %q", got)
	}
}

// TestPaneCycleAndKeys drives next-pane and prev-pane through a live split and
// proves the keys action lands raw bytes in a named pane's program.
func TestPaneCycleAndKeys(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "cyc")
	p1 := h.focus("cyc")
	p2 := h.split("cyc", p1, "right")
	var raw []byte
	if focus := h.focus("cyc"); focus != p2 {
		t.Fatalf("the split left the focus on %q, want the new pane %q", focus, p2)
	}

	h.rpc("cyc", map[string]any{"action": "next-pane"})
	if focus := h.focus("cyc"); focus != p1 {
		t.Fatalf("after next-pane focus = %q, want %q", focus, p1)
	}
	h.rpc("cyc", map[string]any{"action": "prev-pane"})
	if focus := h.focus("cyc"); focus != p2 {
		t.Fatalf("after prev-pane focus = %q, want %q", focus, p2)
	}
	h.rpc("cyc", map[string]any{"action": "prev-pane"})
	if focus := h.focus("cyc"); focus != p1 {
		t.Fatalf("after the second prev-pane focus = %q, want %q", focus, p1)
	}

	// The bytes go to the named pane, not the focused one: p1 runs the shell
	// that prints the marker while p2 holds the focus.
	h.rpc("cyc", map[string]any{"action": "keys", "target": p1, "keys": []byte("echo KEYS-LANDED\n")})
	h.waitFor("cyc", p1, "KEYS-LANDED")
	raw, _ = json.Marshal(map[string]any{"action": "keys", "target": "nope", "keys": []byte("x")})
	if out, err := h.try("exec", string(raw), "-s", "cyc"); err == nil {
		t.Fatalf("keys to an unknown pane accepted: %s", out)
	}
}

// TestReplayAction proves the replay action answers and pushes the pane's ring
// on the asking connection. exec drops the replay frame, so this test reads
// the socket itself: the frame must come before the reply, name the pane and
// carry the lines the pane printed.
func TestReplayAction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "rep")
	root := h.focus("rep")
	h.run("write-to", root, "-s", "rep", "--msg",
		`for i in $(seq 1 40); do echo REPLAY-$(printf %03d $i); done`, "--enter")
	h.waitFor("rep", root, "REPLAY-040")

	sock := filepath.Join(h.stateDir(), "sessions", "rep", "server.sock")
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial %s: %v", sock, err)
	}
	defer func() { _ = c.Close() }()
	raw, _ := json.Marshal(map[string]any{"action": "replay", "target": root})
	var req wire.Msg
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	req.Type = "req"
	req.ID = 1
	if err := wire.WriteMsg(c, req); err != nil {
		t.Fatalf("replay send: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	var ring []byte
	answered := false
	for !answered || ring == nil {
		m, err := wire.ReadMsg(c)
		if err != nil {
			t.Fatalf("replay reply: %v (answered=%v ring=%v)", err, answered, ring != nil)
		}
		switch {
		case m.Type == "replay":
			if m.Target != root {
				t.Fatalf("replay frame target = %q, want %q", m.Target, root)
			}
			ring = m.Data
		case m.Type == "resp" && m.ID == 1:
			if !m.OK {
				t.Fatalf("replay refused: %s", m.Error)
			}
			answered = true
		}
	}
	if !strings.Contains(string(ring), "REPLAY-040") {
		t.Fatalf("replay ring does not carry the pane's lines: %d bytes, head %q",
			len(ring), string(ring[:min(len(ring), 120)]))
	}

	if out, err := h.try("exec", `{"action":"replay","target":"nope"}`, "-s", "rep"); err == nil {
		t.Fatalf("replay of an unknown pane accepted: %s", out)
	}
}

// TestFloatCloseAction proves float-close ends the float the asking client
// opened: the exit frame names the float, the reply says OK, and a float the
// server does not know answers OK and closes nothing.
func TestFloatCloseAction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "flt")
	root := h.focus("flt")
	conn, _ := floatOpen(t, h, "flt", map[string]any{
		"action": "float-open", "target": root, "float": "f1",
		"cols": 80, "rows": 24,
		"res": map[string]any{"command": "sleep 30", "label": "sleeper"},
	})
	defer func() { _ = conn.Close() }()

	send := func(id int, floatID string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"action": "float-close", "float": floatID})
		var m wire.Msg
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		m.Type = "req"
		m.ID = id
		if err := wire.WriteMsg(conn, m); err != nil {
			t.Fatalf("float-close send: %v", err)
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	send(2, "f1")
	answered, exited := false, false
	for !answered || !exited {
		m, err := wire.ReadMsg(conn)
		if err != nil {
			t.Fatalf("float-close reply: %v (answered=%v exited=%v)", err, answered, exited)
		}
		switch {
		case m.Type == "resp" && m.ID == 2:
			if !m.OK {
				t.Fatalf("float-close refused: %s", m.Error)
			}
			answered = true
		case m.Type == "exit" && m.Target == "f1":
			exited = true
		}
	}

	// An unknown float is nobody's to close, and the answer is still OK.
	send(3, "nope")
	for {
		m, err := wire.ReadMsg(conn)
		if err != nil {
			t.Fatalf("float-close of an unknown float: %v", err)
		}
		if m.Type == "resp" && m.ID == 3 {
			if !m.OK {
				t.Fatalf("float-close of an unknown float refused: %s", m.Error)
			}
			break
		}
	}
}
