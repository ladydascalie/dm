package websession_test

import (
	"net"
	"strings"
	"testing"
	"time"

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
