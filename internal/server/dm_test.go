package server

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"dm/internal/layout"
	"dm/internal/paths"
	"dm/internal/rail"
	"dm/internal/session"
	"dm/internal/testutil"
	"dm/internal/wire"
)

func TestRailGeometry(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	st := session.NewState("rail")
	id := testutil.RootPane(st)
	s := railServer(st)

	snap := s.snapshotLocked()
	want := layout.EffectiveRail(80, layout.DefaultRail)
	if snap.Rail != want {
		t.Fatalf("new session rail = %d, want %d", snap.Rail, want)
	}
	pad := s.cfg.Padding()
	boxX := func(p wire.PaneInfo) int { return layout.BoxRect(p.Rect, pad).X - 1 }
	p := snap.Panes[0]
	if p.ID != id || boxX(p) != want {
		t.Fatalf("new session pane %q box starts at %d, want %d", p.ID, boxX(p), want)
	}
	if area := layout.PaneAreaForWithTabBar(80, 24, snap.Rail); area.X != want || p.Rect.X != area.X+1+pad {
		t.Fatalf("pane rect %+v does not sit inside area %+v", p.Rect, area)
	}

	// Rail 0 hides it and the pane starts at the screen edge.
	if rail := s.setRail(0); rail != 0 {
		t.Fatalf("set-rail 0 effective = %d", rail)
	}
	snap = s.snapshotLocked()
	if snap.Rail != 0 || boxX(snap.Panes[0]) != 0 {
		t.Fatalf("hidden rail: rail=%d box x=%d", snap.Rail, boxX(snap.Panes[0]))
	}

	// The thin rail keeps a fixed width and the glyph column.
	if rail := s.setRail(layout.CollapsedRail); rail != layout.CollapsedRail {
		t.Fatalf("set-rail %d effective = %d", layout.CollapsedRail, rail)
	}
	snap = s.snapshotLocked()
	if snap.Rail != layout.CollapsedRail || boxX(snap.Panes[0]) != layout.CollapsedRail {
		t.Fatalf("thin rail: rail=%d box x=%d", snap.Rail, boxX(snap.Panes[0]))
	}

	// A width over the clamp uses the clamp: a third of 120 is 40, under the
	// 46 cap.
	s.cols = 120
	if rail := s.setRail(50); rail != 40 {
		t.Fatalf("set-rail 50 at 120 cols effective = %d, want 40", rail)
	}
	snap = s.snapshotLocked()
	if snap.Rail != 40 || boxX(snap.Panes[0]) != 40 {
		t.Fatalf("clamped rail: rail=%d box x=%d", snap.Rail, boxX(snap.Panes[0]))
	}
	if area := layout.PaneAreaFor(120, 24, 45); area != (layout.Rect{X: 40, Y: 0, W: 80, H: 23}) {
		t.Fatalf("paneAreaFor clamps to %+v", area)
	}
	// A wide screen stops at the 46 cap.
	if m := layout.MaxRail(200); m != 46 {
		t.Fatalf("maxRail(200) = %d, want 46", m)
	}
	// The third-of-screen clamp binds below the 30 cap.
	if area := layout.PaneAreaFor(60, 24, 30); area.X != 20 || area.W != 40 {
		t.Fatalf("paneAreaFor third clamp = %+v", area)
	}
	// A negative request is no rail.
	if area := layout.PaneAreaFor(80, 24, -5); area.X != 0 {
		t.Fatalf("negative rail area = %+v", area)
	}
}

// TestRailBranch runs the branch check off the snapshot path and labels the
// workspace with the focused pane's branch.
func TestRailBranch(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	old := detectGit
	detectGit = func(cwd string) gitInfo {
		if cwd == "/work/dm" {
			return gitInfo{branch: "dm-rail"}
		}
		return gitInfo{}
	}
	defer func() { detectGit = old }()

	st := session.NewState("branch")
	id := testutil.RootPane(st)
	st.Panes[id].Cwd = "/work/dm"
	s := railServer(st)

	deadline := time.Now().Add(2 * time.Second)
	for {
		snap := s.snapshotLocked()
		if len(snap.All) == 1 && snap.All[0].Branch == "dm-rail" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("branch never reached the snapshot: %+v", snap.All)
		}
		time.Sleep(10 * time.Millisecond)
	}
	items := rail.RailItems(s.snapshotLocked())
	if len(items) < 2 || items[1].Branch != "dm-rail" || items[1].Folder != "dm" {
		t.Fatalf("window line = %+v, want folder dm and branch dm-rail", items)
	}
}
func TestSessionSizeIsLargestClient(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	st := session.NewState("sizes")
	s := railServer(st)
	big := &client{cols: 133, rows: 43}
	small := &client{cols: 77, rows: 7}

	s.mu.Lock()
	s.clients[big] = true
	if !s.fitToClientsLocked() || s.cols != 133 || s.rows != 43 {
		t.Fatalf("big client size = %dx%d", s.cols, s.rows)
	}
	s.clients[small] = true
	if s.fitToClientsLocked() {
		t.Fatal("a small client shrank the session")
	}
	s.mu.Unlock()
	if s.cols != 133 || s.rows != 43 {
		t.Fatalf("size after small attach = %dx%d", s.cols, s.rows)
	}

	// When the big client leaves, the remaining one owns the size.
	s.mu.Lock()
	delete(s.clients, big)
	if !s.fitToClientsLocked() {
		t.Fatal("size did not follow the remaining client")
	}
	s.mu.Unlock()
	if s.cols != 77 || s.rows != 7 {
		t.Fatalf("size after big detach = %dx%d", s.cols, s.rows)
	}
}
func TestRailState(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	st := session.NewState("rr")
	s := railServer(st)
	s.cols = 100
	if rail := s.setRail(30); rail != 30 {
		t.Fatalf("set-rail 30 effective = %d", rail)
	}

	got, err := session.LoadState("rr")
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	if got.Rail != 30 || !got.RailSet {
		t.Fatalf("reload rail = %d set=%v, want 30 true", got.Rail, got.RailSet)
	}

	// A state written before the rail existed asks for the default and is
	// saved with it.
	old := &session.State{
		Name: "old", Cols: 80, Rows: 24,
		Panes:      map[string]*session.PaneState{},
		Workspaces: []*session.Workspace{{ID: "s1", Name: "main"}},
		IDVersion:  2,
	}
	if err := old.Save(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(paths.StatePath("old"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	delete(raw, "rail")
	delete(raw, "railSet")
	b, _ = json.Marshal(raw)
	if err := os.WriteFile(paths.StatePath("old"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = session.LoadState("old")
	if err != nil {
		t.Fatalf("loadState old: %v", err)
	}
	if got.Rail != layout.DefaultRail || !got.RailSet {
		t.Fatalf("state without railSet = %d set=%v, want %d true", got.Rail, got.RailSet, layout.DefaultRail)
	}
	saved, err := os.ReadFile(paths.StatePath("old"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), fmt.Sprintf("\"rail\": %d", layout.DefaultRail)) ||
		!strings.Contains(string(saved), "\"railSet\": true") {
		t.Fatalf("state without railSet was not saved: %s", saved)
	}
}
func TestRailAllPanes(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	st := session.NewState("all")
	st.Workspaces = []*session.Workspace{
		{ID: "s1", Name: "main", Active: 0, Windows: []*session.Window{{
			ID: "w1", Name: "one", Focus: "w1p1",
			Root: &layout.Node{
				Dir: "row", Sizes: []float64{0.5, 0.5},
				Kids: []*layout.Node{{Pane: "w1p1"}, {Pane: "w1p2"}},
			},
		}}},
		{ID: "s2", Name: "work", Active: 0, Windows: []*session.Window{{
			ID: "w2", Name: "two", Focus: "w2p1",
			Root: &layout.Node{
				Dir: "col", Sizes: []float64{0.5, 0.5},
				Kids: []*layout.Node{{Pane: "w2p1"}, {Pane: "w2p2"}},
			},
		}}},
	}
	st.Active = 0
	st.Panes = map[string]*session.PaneState{
		"w1p1": {Name: "alpha"},
		"w1p2": {},
		"w2p1": {},
		"w2p2": {},
	}
	s := railServer(st)
	st.Panes["w1p2"].App = "pi"
	s.panes["w1p2"] = &paneConn{st: wire.Status{State: "working", Message: "working hard"}}
	s.panes["w2p1"] = &paneConn{st: wire.Status{State: "unknown", Message: "unreported"}}

	snap := s.snapshotLocked()
	if len(snap.Panes) != 2 {
		t.Fatalf("active window panes = %d, want 2", len(snap.Panes))
	}
	if len(snap.All) != 4 {
		t.Fatalf("All = %d panes, want 4: %+v", len(snap.All), snap.All)
	}
	wantIDs := []string{"w1p1", "w1p2", "w2p1", "w2p2"}
	for i, want := range wantIDs {
		if snap.All[i].ID != want {
			t.Fatalf("All[%d] = %q, want %q", i, snap.All[i].ID, want)
		}
	}
	first := snap.All[0]
	if first.Name != "alpha" || !first.Agent || !first.Focus ||
		!first.WorkspaceActive || !first.WindowActive {
		t.Fatalf("named agent row = %+v", first)
	}
	if first.Workspace != "s1" || first.WorkspaceName != "main" ||
		first.Window != "w1" || first.WindowName != "one" {
		t.Fatalf("first row group = %+v", first)
	}
	byStatus := snap.All[1]
	if !byStatus.Agent || byStatus.Status != "working" ||
		byStatus.Message != "working hard" || byStatus.Name != "" ||
		byStatus.App != "pi" {
		t.Fatalf("working pane row = %+v", byStatus)
	}
	unknown := snap.All[2]
	if unknown.Agent || unknown.Status != "unknown" || unknown.Message != "unreported" ||
		unknown.Workspace != "s2" || unknown.Window != "w2" ||
		unknown.WorkspaceActive || unknown.WindowActive || unknown.Focus {
		t.Fatalf("unknown-status shell row = %+v", unknown)
	}
	silent := snap.All[3]
	if silent.Agent || silent.Status != "" || silent.Name != "" {
		t.Fatalf("silent shell row = %+v", silent)
	}
}

// paneMsg is one message a pane's socket received, tagged with the pane.
type paneMsg struct {
	id string
	m  wire.Msg
}

// zoomFixture is an in-process server whose active window is one row split,
// both panes wired to pipes, so a test reads the resize messages syncSizes
// sends. The right pane is focused.
type zoomFixture struct {
	s     *server
	left  string
	right string
	inbox chan paneMsg
}

func newZoomFixture(t *testing.T) *zoomFixture {
	t.Helper()
	t.Setenv("DM_STATE", t.TempDir())
	st := session.NewState("zoom")
	w := st.ActiveWindow()
	left := testutil.RootPane(st)
	right := w.NewPaneID()
	st.Panes[right] = &session.PaneState{}
	w.Root = &layout.Node{Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*layout.Node{{Pane: left}, {Pane: right}}}
	w.Focus = right

	f := &zoomFixture{s: railServer(st), left: left, right: right,
		inbox: make(chan paneMsg, 64)}
	for _, id := range []string{left, right} {
		srv, cli := net.Pipe()
		t.Cleanup(func() { srv.Close(); cli.Close() })
		f.s.panes[id] = &paneConn{id: id, c: srv}
		go func(id string, c net.Conn) {
			for {
				m, err := wire.ReadMsg(c)
				if err != nil {
					return
				}
				f.inbox <- paneMsg{id: id, m: m}
			}
		}(id, cli)
	}
	// Settle the split sizes, so a later syncSizes reports only what the test
	// asked for.
	f.s.syncSizes()
	f.drain()
	return f
}

// drain returns the messages each pane received, once the channel goes quiet.
func (f *zoomFixture) drain() map[string][]wire.Msg {
	out := map[string][]wire.Msg{}
	for {
		select {
		case got := <-f.inbox:
			out[got.id] = append(out[got.id], got.m)
		case <-time.After(200 * time.Millisecond):
			return out
		}
	}
}

// TestZoomSyncSizes pins the PTY side: the zoomed pane is resized to the single
// pane box, and a hidden sibling's PTY stays at its split rect.
func TestZoomSyncSizes(t *testing.T) {
	f := newZoomFixture(t)
	f.s.mu.Lock()
	area := layout.PaneAreaForWithTabBar(f.s.cols, f.s.rows, f.s.railLocked())
	normal := f.s.paneRectsLocked()
	f.s.zoom = f.right
	f.s.mu.Unlock()
	want := layout.InsetBox(area, f.s.cfg.Padding())
	if normal[f.right] == want {
		t.Fatalf("the split rect %+v already fills %+v", normal[f.right], want)
	}

	f.s.syncSizes()
	got := f.drain()
	if len(got[f.left]) != 0 {
		t.Fatalf("hidden sibling got %v, want no resize", got[f.left])
	}
	if len(got[f.right]) != 1 {
		t.Fatalf("zoomed pane got %d messages, want one resize: %v", len(got[f.right]), got[f.right])
	}
	if m := got[f.right][0]; m.Action != "resize" || m.Cols != want.W || m.Rows != want.H {
		t.Fatalf("zoomed pane resize = %+v, want %dx%d", m, want.W, want.H)
	}
}

// TestZoomKeepsOnTerminalResize pins the resize exception: a bigger client
// resizes the session, keeps the zoom, and the zoomed pane takes the new area.
func TestZoomKeepsOnTerminalResize(t *testing.T) {
	f := newZoomFixture(t)
	f.s.mu.Lock()
	f.s.zoom = f.right
	f.s.mu.Unlock()
	f.s.syncSizes()
	f.drain()

	// The resize case in serve: the largest client sets the size, then the
	// panes are resynced.
	f.s.mu.Lock()
	f.s.clients[&client{cols: 140, rows: 44}] = true
	grew := f.s.fitToClientsLocked()
	f.s.mu.Unlock()
	if !grew {
		t.Fatal("the session did not take the client size")
	}
	f.s.syncSizes()
	got := f.drain()

	f.s.mu.Lock()
	snap := f.s.snapshotLocked()
	f.s.mu.Unlock()
	if snap.Zoom != f.right {
		t.Fatalf("a terminal resize cleared the zoom: %q", snap.Zoom)
	}
	want := layout.InsetBox(layout.PaneAreaForWithTabBar(140, 44, layout.EffectiveRail(140, layout.DefaultRail)), f.s.cfg.Padding())
	var rect layout.Rect
	for _, p := range snap.Panes {
		if p.ID == f.right {
			rect = p.Rect
		}
	}
	if rect != want {
		t.Fatalf("zoomed rect %+v, want %+v", rect, want)
	}
	if len(got[f.right]) != 1 ||
		got[f.right][0].Cols != want.W || got[f.right][0].Rows != want.H {
		t.Fatalf("zoomed pane resize = %v, want one to %dx%d", got[f.right], want.W, want.H)
	}
}

// TestZoomStalePaneNotSent pins the validation: a zoom that names a pane of
// another window is dropped, so the snapshot and the PTYs stay unzoomed.
func TestZoomStalePaneNotSent(t *testing.T) {
	f := newZoomFixture(t)
	f.s.st.Workspaces[0].Windows = append(f.s.st.Workspaces[0].Windows,
		&session.Window{ID: "w2", Name: "two", Root: &layout.Node{Pane: "w2p1"}})
	f.s.st.Panes["w2p1"] = &session.PaneState{}

	f.s.mu.Lock()
	f.s.zoom = "w2p1"
	snap := f.s.snapshotLocked()
	held := f.s.zoom
	area := layout.PaneAreaForWithTabBar(f.s.cols, f.s.rows, f.s.railLocked())
	f.s.mu.Unlock()
	if snap.Zoom != "" {
		t.Fatalf("snapshot sent a zoom outside the active window: %q", snap.Zoom)
	}
	if held != "" {
		t.Fatalf("stale zoom %q held after the snapshot", held)
	}
	for _, p := range snap.Panes {
		if p.ID == f.right && p.Rect == layout.InsetBox(area, f.s.cfg.Padding()) {
			t.Fatalf("pane %q fills the area %+v", f.right, area)
		}
	}

	f.s.syncSizes()
	if got := f.drain(); len(got[f.left])+len(got[f.right]) != 0 {
		t.Fatalf("a stale zoom resized the active window: %v", got)
	}
}

// TestAllPaneInRepo carries the in-repo flag onto the wire row, so a directory
// git never saw cannot look clean.
func TestAllPaneInRepo(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	old := detectGit
	detectGit = func(cwd string) gitInfo {
		if cwd == "/work/dm" {
			return gitInfo{branch: "dm-rail", inRepo: true}
		}
		return gitInfo{}
	}
	defer func() { detectGit = old }()

	st := session.NewState("inrepo")
	id := testutil.RootPane(st)
	st.Panes[id].Cwd = "/work/dm"
	s := railServer(st)
	deadline := time.Now().Add(2 * time.Second)
	for {
		snap := s.snapshotLocked()
		if len(snap.All) == 1 && snap.All[0].Branch == "dm-rail" {
			if !snap.All[0].InRepo {
				t.Fatalf("AllPane.InRepo = false, want true")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("git state never reached the snapshot: %+v", snap.All)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
