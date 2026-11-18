package websession_test

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/vt"
	"dm/internal/websession"
	"dm/internal/wire"
)

// harness drives the session the way the client does: wire.Msg frames over a
// pipe, and the frames back through wire.ReadMsg. It keeps a local terminal for
// the panes the last snapshot listed, so a test can read what the client would
// have drawn.
type harness struct {
	t     *testing.T
	conn  net.Conn
	peer  net.Conn
	cols  int
	rows  int
	snap  *wire.Snapshot
	terms map[string]*vt.Term
}

// newHarness starts a session and attaches one client to it.
func newHarness(t *testing.T, cols, rows int) *harness {
	t.Helper()
	client, server := net.Pipe()
	size := func() (int, int) { return cols, rows }
	sess := websession.New(size, func(map[string]any) {}, server)
	go sess.Serve()
	h := &harness{t: t, conn: client, peer: server, cols: cols, rows: rows, terms: map[string]*vt.Term{}}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	h.send(wire.Msg{Type: "attach", Cols: cols, Rows: rows})
	first := h.read()
	if first.Type != "snapshot" || first.Snap == nil {
		t.Fatalf("attach answered %q, want a snapshot", first.Type)
	}
	for range first.Snap.Panes {
		if m := h.read(); m.Type != "screen" {
			t.Fatalf("attach after the snapshot sent %q, want a screen", m.Type)
		}
	}
	return h
}

// send writes one frame.
func (h *harness) send(m wire.Msg) {
	h.t.Helper()
	_ = h.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := wire.WriteMsg(h.conn, m); err != nil {
		h.t.Fatalf("write %s %s: %v", m.Type, m.Action, err)
	}
}

// read reads one frame and applies it to the local terminals.
func (h *harness) read() wire.Msg {
	h.t.Helper()
	_ = h.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	m, err := wire.ReadMsg(h.conn)
	if err != nil {
		h.t.Fatalf("read frame: %v", err)
	}
	h.apply(m)
	return m
}

// apply mirrors the client's handling of the three frames a test cares about:
// the snapshot resizes and prunes the local terminals, a screen restores one,
// and bytes append to one.
func (h *harness) apply(m wire.Msg) {
	switch m.Type {
	case "snapshot":
		h.snap = m.Snap
		live := map[string]bool{}
		for _, p := range m.Snap.Panes {
			live[p.ID] = true
			term := h.terms[p.ID]
			if term == nil {
				term = vt.NewTerm(p.Rect.W, p.Rect.H)
				h.terms[p.ID] = term
			}
			if cols, rows := term.Grid().Size(); cols != p.Rect.W || rows != p.Rect.H {
				term.Grid().Resize(p.Rect.W, p.Rect.H)
			}
		}
		for id := range h.terms {
			if !live[id] {
				delete(h.terms, id)
			}
		}
	case "screen":
		term := h.terms[m.Target]
		if term == nil {
			term = vt.NewTerm(m.VTSnap.Cols, m.VTSnap.Rows)
			h.terms[m.Target] = term
		}
		if err := term.Restore(m.VTSnap); err != nil {
			h.t.Fatalf("restore screen of %s: %v", m.Target, err)
		}
	case "bytes":
		if term := h.terms[m.Target]; term != nil {
			term.Write(m.Data)
		}
	}
}

// request sends one req frame and reads frames until its resp.
func (h *harness) request(action, target string, fill func(*wire.Msg)) []wire.Msg {
	h.t.Helper()
	m := wire.Msg{Type: "req", Action: action, Target: target}
	if fill != nil {
		fill(&m)
	}
	h.send(m)
	var frames []wire.Msg
	for {
		got := h.read()
		frames = append(frames, got)
		if got.Type == "resp" {
			return frames
		}
	}
}

// keys types into target, or into the focused pane when target is empty, and
// reads the bytes frame the read produced.
func (h *harness) keys(target, s string) wire.Msg {
	h.t.Helper()
	h.send(wire.Msg{Type: "keys", Target: target, Keys: []byte(s)})
	m := h.read()
	if m.Type != "bytes" {
		h.t.Fatalf("keys answered %q, want bytes", m.Type)
	}
	return m
}

// text is the live screen of one pane as the client holds it.
func (h *harness) text(id string) string {
	term := h.terms[id]
	if term == nil {
		return ""
	}
	cols, rows := term.Grid().Size()
	var b strings.Builder
	for y := 0; y < rows; y++ {
		row := make([]vt.Cell, cols)
		for x := 0; x < cols; x++ {
			row[x] = term.Grid().Cell(x, y)
		}
		b.WriteString(vt.PlainRow(row))
		b.WriteByte('\n')
	}
	return b.String()
}

// pane returns one pane from the last snapshot.
func (h *harness) pane(id string) (wire.PaneInfo, bool) {
	for _, p := range h.snap.Panes {
		if p.ID == id {
			return p, true
		}
	}
	return wire.PaneInfo{}, false
}

// allIDs is every pane in the last snapshot's All, in its order.
func (h *harness) allIDs() []string {
	out := make([]string, 0, len(h.snap.All))
	for _, p := range h.snap.All {
		out = append(out, p.ID)
	}
	return out
}

// allPane returns one All entry.
func (h *harness) allPane(id string) (wire.AllPane, bool) {
	for _, p := range h.snap.All {
		if p.ID == id {
			return p, true
		}
	}
	return wire.AllPane{}, false
}

// framesOf returns the frames of one type, in order.
func framesOf(frames []wire.Msg, typ string) []wire.Msg {
	var out []wire.Msg
	for _, m := range frames {
		if m.Type == typ {
			out = append(out, m)
		}
	}
	return out
}

// screenTargets is the panes whose screens one request sent.
func screenTargets(frames []wire.Msg) []string {
	out := []string{}
	for _, m := range framesOf(frames, "screen") {
		out = append(out, m.Target)
	}
	return out
}

// only is a small assertion helper: the slice must hold exactly want, in order.
func only(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}
}

// TestSplitThenNewWindow proves a split and a new window are real: the new
// window has its own pane, it is the active one, and the rail lists the old
// window's panes under it in tree order.
func TestSplitThenNewWindow(t *testing.T) {
	h := newHarness(t, 100, 30)
	sent := h.request("split", "w1p1", func(m *wire.Msg) { m.Dir = "right" })
	if h.snap.Focus != "w1p2" {
		t.Fatalf("split focused %q, want w1p2", h.snap.Focus)
	}
	if _, ok := h.pane("w1p2"); !ok {
		t.Fatalf("the split pane is not in the snapshot: %v", h.snap.Panes)
	}
	only(t, "split screens", screenTargets(sent), "w1p1", "w1p2")

	moved := h.request("new-window", "w1p2", nil)
	only(t, "the screens a window switch sends", screenTargets(moved), "w2p1")
	if len(h.snap.Windows) != 2 || h.snap.ActiveWindow != 1 {
		t.Fatalf("windows = %v, active = %d, want two windows with 1 active",
			h.snap.Windows, h.snap.ActiveWindow)
	}
	if h.snap.Windows[1].ID != "w2" {
		t.Fatalf("the new window is %q, want w2", h.snap.Windows[1].ID)
	}
	if len(h.snap.Panes) != 1 || h.snap.Panes[0].ID != "w2p1" {
		t.Fatalf("the active window lists %v, want only w2p1", h.snap.Panes)
	}
	if h.snap.Focus != "w2p1" {
		t.Fatalf("focus = %q, want w2p1", h.snap.Focus)
	}
	only(t, "the rail order", h.allIDs(), "w1p1", "w1p2", "w2p1")
	for _, id := range []string{"w1p1", "w1p2"} {
		p, _ := h.allPane(id)
		if p.Window != "w1" || p.WindowActive || p.Focus {
			t.Fatalf("old window pane %s = %+v, want inactive", id, p)
		}
	}
	p, _ := h.allPane("w2p1")
	if !p.WindowActive || !p.Focus || !p.WorkspaceActive {
		t.Fatalf("the new pane = %+v, want it active and focused", p)
	}
}

// TestSecondWorkspaceKeepsTheFirst proves a second workspace is a real sibling:
// s1's panes stay in All, in order, marked inactive, and s2's pane is the one
// the tabs and the pane list show.
func TestSecondWorkspaceKeepsTheFirst(t *testing.T) {
	h := newHarness(t, 100, 30)
	// The rail's workspace menu names the workspace, not a pane.
	h.request("new-window", "s1", nil)
	// w1p1 is in the first window; the active pane is w2p1 in s1.
	moved := h.request("new-workspace", "w2p1", nil)
	only(t, "the screens a workspace switch sends", screenTargets(moved), "w3p1")

	if h.snap.Workspace != "ws2" {
		t.Fatalf("workspace = %q, want ws2", h.snap.Workspace)
	}
	if len(h.snap.Windows) != 1 || h.snap.Windows[0].ID != "w3" || h.snap.ActiveWindow != 0 {
		t.Fatalf("windows = %v, active = %d, want only w3 at 0", h.snap.Windows, h.snap.ActiveWindow)
	}
	if len(h.snap.Panes) != 1 || h.snap.Panes[0].ID != "w3p1" {
		t.Fatalf("panes = %v, want only w3p1", h.snap.Panes)
	}
	only(t, "the rail order", h.allIDs(), "w1p1", "w2p1", "w3p1")

	for _, id := range []string{"w1p1", "w2p1"} {
		p, _ := h.allPane(id)
		if p.Workspace != "s1" || p.WorkspaceActive || p.WindowActive || p.Focus {
			t.Fatalf("%s = %+v, want an inactive pane in s1", id, p)
		}
	}
	p, _ := h.allPane("w3p1")
	if p.Workspace != "s2" || !p.WorkspaceActive || !p.WindowActive || !p.Focus {
		t.Fatalf("w3p1 = %+v, want the active pane of s2", p)
	}
}

// TestFocusInAnotherWorkspaceSwitchesBoth proves a focus names one pane and the
// session makes its window and its workspace active, so the rail, the tab and
// the keys agree.
func TestFocusInAnotherWorkspaceSwitchesBoth(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.request("new-workspace", "w1p1", nil)
	if h.snap.Workspace != "ws2" {
		t.Fatalf("setup left workspace %q", h.snap.Workspace)
	}

	frames := h.request("focus", "w1p1", nil)
	if h.snap.Workspace != "main" {
		t.Fatalf("focus left workspace %q, want main", h.snap.Workspace)
	}
	if len(h.snap.Windows) != 1 || h.snap.Windows[0].ID != "w1" || h.snap.ActiveWindow != 0 {
		t.Fatalf("windows = %v, active = %d, want only w1 at 0", h.snap.Windows, h.snap.ActiveWindow)
	}
	if len(h.snap.Panes) != 1 || h.snap.Panes[0].ID != "w1p1" || h.snap.Focus != "w1p1" {
		t.Fatalf("panes = %v, focus = %q, want only w1p1", h.snap.Panes, h.snap.Focus)
	}
	p, _ := h.allPane("w1p1")
	if !p.Focus || !p.WindowActive || !p.WorkspaceActive {
		t.Fatalf("w1p1 = %+v, want it active and focused", p)
	}
	q, _ := h.allPane("w2p1")
	if q.Focus || q.WindowActive || q.WorkspaceActive {
		t.Fatalf("w2p1 = %+v, want it inactive", q)
	}
	only(t, "the screens a focus sends", screenTargets(frames), "w1p1")
}

// TestWindowSwitchEndsZoom proves a zoom is one view: switching window ends it,
// and switching back does not bring the stale zoom with it.
func TestWindowSwitchEndsZoom(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.request("split", "w1p1", func(m *wire.Msg) { m.Dir = "right" })
	h.request("zoom-pane", "w1p2", nil)
	if h.snap.Zoom != "w1p2" {
		t.Fatalf("zoom = %q, want w1p2", h.snap.Zoom)
	}
	h.request("new-window", "w1p2", nil)
	if h.snap.Zoom != "" {
		t.Fatalf("a window switch kept zoom %q", h.snap.Zoom)
	}
	h.request("select-window", "", func(m *wire.Msg) { m.Index = 0 })
	if h.snap.Zoom != "" {
		t.Fatalf("coming back brought back zoom %q", h.snap.Zoom)
	}
}

// TestSyncBelongsToItsWindow proves synchronize-panes is a window flag: it
// reaches every pane of its own window and no pane of another.
func TestSyncBelongsToItsWindow(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.request("split", "w1p1", func(m *wire.Msg) { m.Dir = "right" })
	on := true
	h.request("synchronize-panes", "", func(m *wire.Msg) { m.On = &on })

	h.send(wire.Msg{Type: "keys", Keys: []byte("x")})
	only(t, "panes a synced read reaches", []string{h.read().Target, h.read().Target}, "w1p2", "w1p1")

	h.request("new-window", "w1p2", nil)
	h.send(wire.Msg{Type: "keys", Keys: []byte("y")})
	only(t, "panes the new window's read reaches", []string{h.read().Target}, "w2p1")

	h.request("select-window", "", func(m *wire.Msg) { m.Index = 0 })
	h.send(wire.Msg{Type: "keys", Keys: []byte("z")})
	only(t, "panes the first window's read reaches", []string{h.read().Target, h.read().Target}, "w1p2", "w1p1")
}

// TestCloseRefusesTheLastOne proves a request cannot empty the session: the last
// window, the last workspace and the last pane are refused, while a close that
// leaves something behind succeeds.
func TestCloseRefusesTheLastOne(t *testing.T) {
	h := newHarness(t, 100, 30)

	frames := h.request("close-window", "w1", nil)
	if got := frames[len(frames)-1]; got.OK || got.Error == "" {
		t.Fatalf("close-window of the last window answered %+v, want a refusal", got)
	}
	frames = h.request("close-workspace", "s1", nil)
	if got := frames[len(frames)-1]; got.OK || got.Error == "" {
		t.Fatalf("close-workspace of the last workspace answered %+v, want a refusal", got)
	}
	frames = h.request("close-pane", "w1p1", nil)
	if got := frames[len(frames)-1]; got.OK || got.Error == "" {
		t.Fatalf("close-pane of the last pane answered %+v, want a refusal", got)
	}
	only(t, "the session after three refusals", h.allIDs(), "w1p1")

	// With something left behind, the same closes go through.
	h.request("new-window", "w1p1", nil)
	if len(h.snap.Windows) != 2 {
		t.Fatalf("setup made %d windows", len(h.snap.Windows))
	}
	h.request("close-window", "w2", nil)
	if len(h.snap.Windows) != 1 || h.snap.Windows[0].ID != "w1" {
		t.Fatalf("windows after close = %v, want only w1", h.snap.Windows)
	}
	h.request("new-workspace", "w1p1", nil)
	h.request("close-workspace", "s1", nil)
	if h.snap.Workspace != "ws2" {
		t.Fatalf("workspace after close = %q, want ws2", h.snap.Workspace)
	}
	only(t, "the rail after closing s1", h.allIDs(), "w3p1")
}

// TestNextWindowAndWorkspaceWrap proves both cycles wrap at either end.
func TestNextWindowAndWorkspaceWrap(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.request("new-window", "w1p1", nil)
	h.request("select-window", "", func(m *wire.Msg) { m.Index = 0 })
	if h.snap.ActiveWindow != 0 {
		t.Fatalf("select-window 0 left active window %d", h.snap.ActiveWindow)
	}
	h.request("next-window", "", nil)
	if h.snap.ActiveWindow != 1 {
		t.Fatalf("next-window = %d, want 1", h.snap.ActiveWindow)
	}
	h.request("next-window", "", nil)
	if h.snap.ActiveWindow != 0 {
		t.Fatalf("next-window wrapped to %d, want 0", h.snap.ActiveWindow)
	}
	h.request("prev-window", "", nil)
	if h.snap.ActiveWindow != 1 {
		t.Fatalf("prev-window wrapped to %d, want 1", h.snap.ActiveWindow)
	}

	h.request("new-workspace", "w1p1", nil)
	if h.snap.Workspace != "ws2" {
		t.Fatalf("setup left workspace %q", h.snap.Workspace)
	}
	h.request("next-workspace", "", nil)
	if h.snap.Workspace != "main" {
		t.Fatalf("next-workspace wrapped to %q, want main", h.snap.Workspace)
	}
	h.request("next-workspace", "", nil)
	if h.snap.Workspace != "ws2" {
		t.Fatalf("next-workspace = %q, want ws2", h.snap.Workspace)
	}
}

// TestToggleFoldReachesAnotherWorkspace proves a rail caret can fold a window
// and a workspace that are not active, without switching to them.
func TestToggleFoldReachesAnotherWorkspace(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.request("new-workspace", "w1p1", nil)

	h.request("toggle-fold", "w1", nil)
	if h.snap.Workspace != "ws2" {
		t.Fatalf("folding w1 switched workspace to %q", h.snap.Workspace)
	}
	p, _ := h.allPane("w1p1")
	if p.WindowUnfolded {
		t.Fatalf("w1 is still unfolded: %+v", p)
	}
	q, _ := h.allPane("w2p1")
	if !q.WindowUnfolded {
		t.Fatalf("folding w1 also folded w2: %+v", q)
	}

	h.request("toggle-fold", "s1", nil)
	p, _ = h.allPane("w1p1")
	if p.WorkspaceUnfolded {
		t.Fatalf("s1 is still unfolded: %+v", p)
	}
	q, _ = h.allPane("w2p1")
	if !q.WorkspaceUnfolded {
		t.Fatalf("folding s1 also folded s2: %+v", q)
	}
}

// TestReplayRingBeatsTheScreen proves the replay ring is a pane's real history:
// enough output gives more rows than the screen holds, and the tail of the
// replay is the live screen.
func TestReplayRingBeatsTheScreen(t *testing.T) {
	h := newHarness(t, 100, 30)
	rect := h.snap.Panes[0].Rect
	for i := 0; i < 60; i++ {
		h.keys("", "echo line-"+itoa(i)+"\r")
	}

	var ring []byte
	for _, m := range h.request("replay", "w1p1", nil) {
		if m.Type == "replay" {
			ring = m.Data
		}
	}
	if len(ring) == 0 {
		t.Fatal("the replay carried no bytes")
	}
	rows := vt.ReplayRows(ring, rect.W, rect.H, 0)
	if len(rows) <= rect.H {
		t.Fatalf("replay gave %d rows for a %d row screen, want more", len(rows), rect.H)
	}
	found := false
	for _, row := range rows {
		if strings.Contains(vt.PlainRow(row), "line-0") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("the replay lost the first line")
	}
	// The last rows must be what the client holds on the live screen.
	live := strings.Split(strings.TrimSuffix(h.text("w1p1"), "\n"), "\n")
	for y := 0; y < rect.H; y++ {
		got := vt.PlainRow(rows[len(rows)-rect.H+y])
		if got != live[y] {
			t.Fatalf("replay row %d = %q, live = %q", y, got, live[y])
		}
	}
}

// TestResizeRepaintEntersTheRing proves a resize repaint is history too: the
// bytes the shell drew again after the pane changed size are in the ring, so a
// later scrollback ends with the screen the resize produced.
func TestResizeRepaintEntersTheRing(t *testing.T) {
	h := newHarness(t, 100, 30)
	banner := "dm, in a tab."
	if got := strings.Count(string(h.replay("w1p1")), banner); got != 1 {
		t.Fatalf("the ring holds the banner %d times before a resize, want 1", got)
	}
	h.send(wire.Msg{Type: "resize", Cols: 80, Rows: 24})
	if m := h.read(); m.Type != "snapshot" {
		t.Fatalf("resize answered %q, want a snapshot", m.Type)
	}
	for range h.snap.Panes {
		if m := h.read(); m.Type != "screen" {
			t.Fatalf("resize after the snapshot sent %q, want a screen", m.Type)
		}
	}
	if got := strings.Count(string(h.replay("w1p1")), banner); got != 2 {
		t.Fatalf("the ring holds the banner %d times after a resize, want 2", got)
	}
}

// TestWindowsKeepTheirOwnShells proves a pane's shell and screen belong to that
// pane: a line typed in one window is not in the other, and each replay carries
// only its own history.
func TestWindowsKeepTheirOwnShells(t *testing.T) {
	h := newHarness(t, 100, 30)
	if m := h.keys("", "echo one-only\r"); !strings.Contains(string(m.Data), "one-only") {
		t.Fatalf("typing in w1p1 printed %q", m.Data)
	}
	h.request("new-window", "w1p1", nil)
	if m := h.keys("", "echo two-only\r"); !strings.Contains(string(m.Data), "two-only") {
		t.Fatalf("typing in w2p1 printed %q", m.Data)
	}
	two := h.text("w2p1")
	if !strings.Contains(two, "two-only") || strings.Contains(two, "one-only") {
		t.Fatalf("the new window's screen = %q", two)
	}

	h.request("focus", "w1p1", nil)
	one := h.text("w1p1")
	if !strings.Contains(one, "one-only") || strings.Contains(one, "two-only") {
		t.Fatalf("the first window's screen = %q", one)
	}

	if ring := h.replay("w1p1"); !strings.Contains(string(ring), "one-only") ||
		strings.Contains(string(ring), "two-only") {
		t.Fatalf("w1p1's ring = %q", ring)
	}
	if ring := h.replay("w2p1"); !strings.Contains(string(ring), "two-only") ||
		strings.Contains(string(ring), "one-only") {
		t.Fatalf("w2p1's ring = %q", ring)
	}
}

// respOf returns the reply of one request.
func respOf(t *testing.T, frames []wire.Msg) wire.Msg {
	t.Helper()
	for i := len(frames) - 1; i >= 0; i-- {
		if frames[i].Type == "resp" {
			return frames[i]
		}
	}
	t.Fatalf("no resp in %v", frames)
	return wire.Msg{}
}

// resField reads one string field of a reply body.
func resField(t *testing.T, m wire.Msg, key string) string {
	t.Helper()
	if len(m.Res) == 0 {
		t.Fatalf("%s reply has no body", m.Action)
	}
	var res map[string]any
	if err := json.Unmarshal(m.Res, &res); err != nil {
		t.Fatalf("%s reply body %q: %v", m.Action, m.Res, err)
	}
	v, ok := res[key]
	if !ok {
		t.Fatalf("%s reply %q has no %q", m.Action, m.Res, key)
	}
	s, _ := v.(string)
	return s
}

// paneIDs is the active window's panes in the last snapshot.
func (h *harness) paneIDs() []string {
	out := make([]string, 0, len(h.snap.Panes))
	for _, p := range h.snap.Panes {
		out = append(out, p.ID)
	}
	return out
}

// checkTree fails on a split node with fewer than two kids or a leaf with a
// split under it. A split node has no pane, so one with no kids is a pane with
// no shell behind it.
func checkTree(t *testing.T, n *layout.Node) {
	t.Helper()
	if n == nil {
		t.Fatal("a nil node in the tree")
	}
	if n.Pane != "" {
		if len(n.Kids) != 0 {
			t.Fatalf("leaf %s has kids", n.Pane)
		}
		return
	}
	if len(n.Kids) < 2 {
		t.Fatalf("split node has %d kids: %+v", len(n.Kids), n)
	}
	for _, k := range n.Kids {
		checkTree(t, k)
	}
}

// TestSplitWithoutTargetKeepsTheTree proves an empty split target resolves to
// the focus before the tree is touched. A split node also has an empty Pane
// field, so locating "" used to find the root split and replace the whole tree
// with it, taking two shells and their rings with it.
func TestSplitWithoutTargetKeepsTheTree(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.request("split", "w1p1", func(m *wire.Msg) { m.Dir = "right" })
	frames := h.request("split", "", func(m *wire.Msg) { m.Dir = "down" })
	got := respOf(t, frames)
	if !got.OK || got.Error != "" {
		t.Fatalf("the split without a target answered %+v, want OK", got)
	}
	if id := resField(t, got, "pane"); id != "w1p3" {
		t.Fatalf("the split reply names pane %q, want w1p3", id)
	}
	only(t, "panes after the empty split", h.paneIDs(), "w1p1", "w1p2", "w1p3")
	for _, id := range []string{"w1p1", "w1p2", "w1p3"} {
		if _, ok := h.allPane(id); !ok {
			t.Fatalf("%s left the session: %v", id, h.allIDs())
		}
	}
	checkTree(t, h.snap.Root)

	// The first pane still has a shell and a ring behind it.
	frames = h.request("replay", "w1p1", nil)
	if got := respOf(t, frames); !got.OK {
		t.Fatalf("replay of w1p1 answered %+v", got)
	}
	if len(framesOf(frames, "replay")) != 1 {
		t.Fatalf("replay of w1p1 sent no replay frame: %v", frames)
	}

	// An empty close-pane target also resolves to the focus.
	frames = h.request("close-pane", "", nil)
	if got := respOf(t, frames); !got.OK {
		t.Fatalf("close-pane without a target answered %+v", got)
	}
	only(t, "panes after the empty close", h.paneIDs(), "w1p1", "w1p2")
}

// TestZoomRefusesOnePane proves a one-pane window does not zoom: the server
// answers an empty zoom and changes nothing. Once there are two panes, the same
// request zooms the focus.
func TestZoomRefusesOnePane(t *testing.T) {
	h := newHarness(t, 100, 30)
	frames := h.request("zoom-pane", "", nil)
	got := respOf(t, frames)
	if !got.OK || got.Error != "" {
		t.Fatalf("zoom on one pane answered %+v, want OK", got)
	}
	if zoom := resField(t, got, "zoom"); zoom != "" {
		t.Fatalf("zoom on one pane answered %q, want empty", zoom)
	}
	if h.snap.Zoom != "" {
		t.Fatalf("zoom on one pane set %q", h.snap.Zoom)
	}
	if len(framesOf(frames, "snapshot")) != 0 {
		t.Fatal("a refused zoom pushed a snapshot")
	}

	h.request("split", "w1p1", func(m *wire.Msg) { m.Dir = "right" })
	frames = h.request("zoom-pane", "", nil)
	if zoom := resField(t, respOf(t, frames), "zoom"); zoom != "w1p2" {
		t.Fatalf("zoom with two panes answered %q, want w1p2", zoom)
	}
	if h.snap.Zoom != "w1p2" {
		t.Fatalf("zoom with two panes set %q, want w1p2", h.snap.Zoom)
	}
}

// TestTabBarRowIsReserved proves the fake lays panes out with the helper the
// client draws and hit-tests with, so row 0 stays the window tab bar and the
// boxes agree with the divider geometry.
func TestTabBarRowIsReserved(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.request("set-rail", "", func(m *wire.Msg) { m.Index = 30 })
	pad := keymap.LoadDefault().Padding()
	area := layout.PaneAreaForWithTabBar(100, 30, 30)
	if area.Y != 1 {
		t.Fatalf("the pane area starts at Y=%d, want row 1 below the tab bar", area.Y)
	}
	want := layout.InsetBox(area, pad)
	p := h.snap.Panes[0]
	if p.Rect != want {
		t.Fatalf("one pane rect = %+v, want %+v from the tab bar helper", p.Rect, want)
	}
	// The content sits one row below the area for its own top border.
	if want.Y != 2 {
		t.Fatalf("the one pane content starts at Y=%d, want 2", want.Y)
	}

	h.request("split", "w1p1", func(m *wire.Msg) { m.Dir = "right" })
	rects := map[string]layout.Rect{}
	layout.Layout(h.snap.Root, area, pad, rects)
	for _, pane := range h.snap.Panes {
		if pane.Rect != rects[pane.ID] {
			t.Fatalf("pane %s rect = %+v, the helper says %+v", pane.ID, pane.Rect, rects[pane.ID])
		}
	}
	boxes := layout.ChildBoxRects(h.snap.Root, area)
	if len(boxes) != 2 || boxes[0].X+boxes[0].W != boxes[1].X {
		t.Fatalf("the split boxes do not meet at one divider: %+v", boxes)
	}
	if zone := layout.PadRect(h.snap.Root, boxes, 0); zone.W != 2 {
		t.Fatalf("divider zone = %+v, want two cells", zone)
	}
}

// TestSmallestAcceptedSize proves the page and the session share one minimum:
// 20 by 6 is laid out, and 19 columns or 5 rows is ignored.
func TestSmallestAcceptedSize(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.send(wire.Msg{Type: "resize", Cols: 20, Rows: 6})
	first := h.read()
	if first.Type != "snapshot" || first.Snap.Cols != 20 || first.Snap.Rows != 6 {
		t.Fatalf("a 20 by 6 resize answered %q at %dx%d", first.Type, first.Snap.Cols, first.Snap.Rows)
	}
	for range first.Snap.Panes {
		if m := h.read(); m.Type != "screen" {
			t.Fatalf("the resize sent %q after the snapshot", m.Type)
		}
	}
	p := h.snap.Panes[0]
	if p.Rect.W < 1 || p.Rect.H < 1 {
		t.Fatalf("a 20 by 6 session laid out a %dx%d pane", p.Rect.W, p.Rect.H)
	}

	// A smaller resize is ignored. A later request still reports 20 by 6.
	h.send(wire.Msg{Type: "resize", Cols: 19, Rows: 6})
	h.request("set-rail", "", func(m *wire.Msg) { m.Index = layout.DefaultRail })
	if h.snap.Cols != 20 || h.snap.Rows != 6 {
		t.Fatalf("a 19 by 6 resize changed the size to %dx%d", h.snap.Cols, h.snap.Rows)
	}
	h.send(wire.Msg{Type: "resize", Cols: 20, Rows: 5})
	h.request("set-rail", "", func(m *wire.Msg) { m.Index = layout.DefaultRail })
	if h.snap.Cols != 20 || h.snap.Rows != 6 {
		t.Fatalf("a 20 by 5 resize changed the size to %dx%d", h.snap.Cols, h.snap.Rows)
	}
}

// TestSplitDirections proves a split works in all four directions and puts the
// new pane on that side.
func TestSplitDirections(t *testing.T) {
	for _, dir := range []string{"left", "right", "up", "down"} {
		t.Run(dir, func(t *testing.T) {
			h := newHarness(t, 100, 30)
			frames := h.request("split", "w1p1", func(m *wire.Msg) { m.Dir = dir })
			if got := respOf(t, frames); !got.OK || resField(t, got, "pane") != "w1p2" {
				t.Fatalf("split %s answered %+v", dir, got)
			}
			if h.snap.Focus != "w1p2" {
				t.Fatalf("split %s focused %q", dir, h.snap.Focus)
			}
			added, ok := h.pane("w1p2")
			if !ok {
				t.Fatalf("split %s made no w1p2", dir)
			}
			old, ok := h.pane("w1p1")
			if !ok {
				t.Fatalf("split %s lost w1p1", dir)
			}
			ok = false
			switch dir {
			case "left":
				ok = added.Rect.X < old.Rect.X
			case "right":
				ok = added.Rect.X > old.Rect.X
			case "up":
				ok = added.Rect.Y < old.Rect.Y
			case "down":
				ok = added.Rect.Y > old.Rect.Y
			}
			if !ok {
				t.Fatalf("split %s put w1p2 at %+v and w1p1 at %+v", dir, added.Rect, old.Rect)
			}
		})
	}
}

// TestSwapByDirection proves the focused pane trades places with its neighbour
// in that direction and the focus stays on its own content.
func TestSwapByDirection(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.request("split", "w1p1", func(m *wire.Msg) { m.Dir = "right" })
	left, _ := h.pane("w1p1")
	right, _ := h.pane("w1p2")
	frames := h.request("swap-panes", "", func(m *wire.Msg) { m.Dir = "left" })
	if got := respOf(t, frames); !got.OK || got.Error != "" {
		t.Fatalf("swap-panes left answered %+v", got)
	}
	if h.snap.Focus != "w1p2" {
		t.Fatalf("the swap moved focus to %q", h.snap.Focus)
	}
	a, _ := h.pane("w1p2")
	b, _ := h.pane("w1p1")
	if a.Rect != left.Rect || b.Rect != right.Rect {
		t.Fatalf("after the swap w1p2 = %+v (want %+v) and w1p1 = %+v (want %+v)",
			a.Rect, left.Rect, b.Rect, right.Rect)
	}

	// The other direction trades them back.
	frames = h.request("swap-panes", "", func(m *wire.Msg) { m.Dir = "right" })
	if got := respOf(t, frames); !got.OK || got.Error != "" {
		t.Fatalf("swap-panes right answered %+v", got)
	}
	a, _ = h.pane("w1p2")
	b, _ = h.pane("w1p1")
	if a.Rect != right.Rect || b.Rect != left.Rect {
		t.Fatalf("after the second swap w1p2 = %+v (want %+v) and w1p1 = %+v (want %+v)",
			a.Rect, right.Rect, b.Rect, left.Rect)
	}
}

// TestNextAndPrevPane proves both pane keys walk the window and wrap.
func TestNextAndPrevPane(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.request("split", "w1p1", func(m *wire.Msg) { m.Dir = "right" })
	if h.snap.Focus != "w1p2" {
		t.Fatalf("setup focused %q", h.snap.Focus)
	}
	h.request("next-pane", "", nil)
	if h.snap.Focus != "w1p1" {
		t.Fatalf("next-pane focused %q, want w1p1", h.snap.Focus)
	}
	h.request("prev-pane", "", nil)
	if h.snap.Focus != "w1p2" {
		t.Fatalf("prev-pane focused %q, want w1p2", h.snap.Focus)
	}
}

// TestPrevWorkspace proves the previous workspace key walks backwards and wraps.
func TestPrevWorkspace(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.request("new-workspace", "w1p1", nil)
	if h.snap.Workspace != "ws2" {
		t.Fatalf("setup left workspace %q", h.snap.Workspace)
	}
	h.request("prev-workspace", "", nil)
	if h.snap.Workspace != "main" {
		t.Fatalf("prev-workspace = %q, want main", h.snap.Workspace)
	}
	h.request("prev-workspace", "", nil)
	if h.snap.Workspace != "ws2" {
		t.Fatalf("prev-workspace wrapped to %q, want ws2", h.snap.Workspace)
	}
}

// TestLayoutPresetsAndNext proves all five presets rebuild the same pane set,
// keep the window's stored name, and that next walks the documented cycle.
func TestLayoutPresetsAndNext(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.request("split", "w1p1", func(m *wire.Msg) { m.Dir = "right" })
	h.request("split", "w1p2", func(m *wire.Msg) { m.Dir = "down" })
	ids := layout.AllPanes(h.snap.Root)
	only(t, "setup panes", ids, "w1p1", "w1p2", "w1p3")

	shapes := []struct {
		name string
		dir  string
		kids int
	}{
		{"even-horizontal", "row", 3},
		{"even-vertical", "col", 3},
		{"main-horizontal", "col", 2},
		{"main-vertical", "row", 2},
		{"tiled", "col", 2},
	}
	for _, sh := range shapes {
		frames := h.request("select-layout", "", func(m *wire.Msg) { m.Layout = sh.name })
		got := respOf(t, frames)
		if !got.OK || got.Error != "" {
			t.Fatalf("%s answered %+v", sh.name, got)
		}
		if name := resField(t, got, "layout"); name != sh.name {
			t.Fatalf("%s replied layout %q", sh.name, name)
		}
		if h.snap.Windows[0].Layout != sh.name {
			t.Fatalf("%s stored layout %q", sh.name, h.snap.Windows[0].Layout)
		}
		if h.snap.Root.Dir != sh.dir || len(h.snap.Root.Kids) != sh.kids {
			t.Fatalf("%s made root %q with %d kids, want %q with %d",
				sh.name, h.snap.Root.Dir, len(h.snap.Root.Kids), sh.dir, sh.kids)
		}
		only(t, sh.name+" panes", layout.AllPanes(h.snap.Root), ids...)
	}
	// main-* keeps the first pane on the main side. tiled filled row-major.
	h.request("select-layout", "", func(m *wire.Msg) { m.Layout = "main-vertical" })
	if h.snap.Root.Kids[0].Pane != "w1p1" || h.snap.Root.Kids[1].Dir != "col" {
		t.Fatalf("main-vertical root = %+v", h.snap.Root)
	}

	// The cycle: tiled -> even-horizontal -> even-vertical.
	h.request("select-layout", "", func(m *wire.Msg) { m.Layout = "tiled" })
	frames := h.request("select-layout", "", func(m *wire.Msg) { m.Layout = "next" })
	if name := resField(t, respOf(t, frames), "layout"); name != "even-horizontal" {
		t.Fatalf("next after tiled named %q, want even-horizontal", name)
	}
	if h.snap.Root.Dir != "row" || len(h.snap.Root.Kids) != 3 {
		t.Fatalf("next after tiled made root %+v", h.snap.Root)
	}
	frames = h.request("select-layout", "", func(m *wire.Msg) { m.Layout = "next" })
	if name := resField(t, respOf(t, frames), "layout"); name != "even-vertical" {
		t.Fatalf("the second next named %q, want even-vertical", name)
	}
	if h.snap.Root.Dir != "col" || len(h.snap.Root.Kids) != 3 {
		t.Fatalf("the second next made root %+v", h.snap.Root)
	}
}

// TestSetRail proves the requested rail width reaches the snapshot, moves the
// panes, and is clamped to what the screen can spare.
func TestSetRail(t *testing.T) {
	h := newHarness(t, 100, 30)
	if h.snap.Rail != layout.EffectiveRail(100, layout.DefaultRail) {
		t.Fatalf("the first rail = %d", h.snap.Rail)
	}
	h.request("set-rail", "", func(m *wire.Msg) { m.Index = 0 })
	if h.snap.Rail != 0 || h.snap.RailWant != 0 {
		t.Fatalf("rail 0 gave Rail=%d RailWant=%d", h.snap.Rail, h.snap.RailWant)
	}
	if p := h.snap.Panes[0]; p.Rect.X != 1 {
		t.Fatalf("with no rail the pane starts at X=%d, want 1", p.Rect.X)
	}
	h.request("set-rail", "", func(m *wire.Msg) { m.Index = 30 })
	if h.snap.Rail != 30 || h.snap.RailWant != 30 {
		t.Fatalf("rail 30 gave Rail=%d RailWant=%d", h.snap.Rail, h.snap.RailWant)
	}
	if p := h.snap.Panes[0]; p.Rect.X != 31 {
		t.Fatalf("with a 30 column rail the pane starts at X=%d, want 31", p.Rect.X)
	}
	h.request("set-rail", "", func(m *wire.Msg) { m.Index = 100 })
	if h.snap.RailWant != 100 || h.snap.Rail != layout.EffectiveRail(100, 100) {
		t.Fatalf("rail 100 gave Rail=%d RailWant=%d", h.snap.Rail, h.snap.RailWant)
	}
}

// TestClosePaneOfTwoPanes proves a close-pane that leaves something behind
// succeeds: the pane leaves, the window stays, and focus lands on the survivor.
func TestClosePaneOfTwoPanes(t *testing.T) {
	h := newHarness(t, 100, 30)
	h.request("split", "w1p1", func(m *wire.Msg) { m.Dir = "right" })
	frames := h.request("close-pane", "w1p2", nil)
	got := respOf(t, frames)
	if !got.OK || got.Error != "" {
		t.Fatalf("close-pane of a two pane window answered %+v", got)
	}
	only(t, "panes after the close", h.paneIDs(), "w1p1")
	only(t, "the rail after the close", h.allIDs(), "w1p1")
	if h.snap.Focus != "w1p1" {
		t.Fatalf("focus after the close = %q, want w1p1", h.snap.Focus)
	}
	if len(h.snap.Windows) != 1 || h.snap.Windows[0].ID != "w1" {
		t.Fatalf("windows after the close = %v", h.snap.Windows)
	}
}

// TestUnservedActionGetsAnEmptyReply proves an action the fake does not serve
// still answers the client, with OK and no body, and changes nothing.
func TestUnservedActionGetsAnEmptyReply(t *testing.T) {
	h := newHarness(t, 100, 30)
	frames := h.request("rename-window", "w1", func(m *wire.Msg) { m.Name = "renamed" })
	got := respOf(t, frames)
	if !got.OK || got.Error != "" {
		t.Fatalf("an unserved action answered %+v", got)
	}
	if len(got.Res) != 0 {
		t.Fatalf("an unserved action replied with body %q", got.Res)
	}
	if len(framesOf(frames, "snapshot")) != 0 {
		t.Fatal("an unserved action pushed a snapshot")
	}
	if h.snap.Windows[0].Name != "one" {
		t.Fatalf("an unserved action renamed the window to %q", h.snap.Windows[0].Name)
	}
}

// replay asks for one pane's ring and returns it.
func (h *harness) replay(id string) []byte {
	h.t.Helper()
	for _, m := range h.request("replay", id, nil) {
		if m.Type == "replay" {
			return m.Data
		}
	}
	h.t.Fatalf("replay of %s carried no replay frame", id)
	return nil
}

// itoa is strconv.Itoa without importing strconv for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
