package server

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"dm/internal/layout"
	"dm/internal/session"
	"dm/internal/wire"
)

// shelfServer is the rpc fixture with a third pane in the window, so a pane can
// be shelved and the window still holds two.
func shelfServer(t *testing.T, name string) (*server, *client, net.Conn, string, string, string) {
	t.Helper()
	s, cl, conn, a, b := rpcServer(t, name)
	s.mu.Lock()
	win := s.st.ActiveWindow()
	c := win.NewPaneID()
	s.st.Panes[c] = &session.PaneState{}
	layout.SplitLeaf(win.Root, b, "right", c, 0)
	s.mu.Unlock()
	screenPane(t, s.st, c)
	return s, cl, conn, a, b, c
}

// replyPane reads the pane id a split, shelve or pop replies with.
func replyPane(t *testing.T, action string, m wire.Msg) string {
	t.Helper()
	if !m.OK {
		t.Fatalf("%s: %s", action, m.Error)
	}
	var rep struct {
		Pane string `json:"pane"`
	}
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("decode %s reply: %v", action, err)
	}
	return rep.Pane
}

// TestShelvePaneAction pins the shelve contract: the pane leaves the tree, the
// split collapses, focus moves to a survivor, the shelf gains the pane with its
// state intact, and the pane's PTY is never told to die.
func TestShelvePaneAction(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, b, c := shelfServer(t, "rpcshelve")
	spy := spyPane(t, s, c)

	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "shelve-pane", Target: c, ID: 1})
	if got := replyPane(t, "shelve-pane", m); got != c {
		t.Fatalf("shelve reply = %q, want %q", got, c)
	}

	s.mu.Lock()
	win := s.st.ActiveWindow()
	ids := layout.AllPanes(win.Root)
	focus := win.Focus
	shelf := append([]string(nil), s.st.Shelf...)
	active := s.st.ShelfActive
	ps := s.st.Panes[c]
	s.mu.Unlock()

	want := []string{a, b}
	if len(ids) != len(want) || ids[0] != want[0] || ids[1] != want[1] {
		t.Fatalf("tree = %v, want %v", ids, want)
	}
	if focus == c || focus == "" {
		t.Fatalf("focus = %q, want a surviving pane", focus)
	}
	if len(shelf) != 1 || shelf[0] != c {
		t.Fatalf("shelf = %v, want [%s]", shelf, c)
	}
	if active != 0 {
		t.Fatalf("shelf active = %d, want 0 for the first member", active)
	}
	if ps == nil {
		t.Fatalf("the shelved pane lost its state")
	}
	spyEmpty(t, spy)

	// The snapshot lists the shelved pane with the shelf flag and no rect.
	s.mu.Lock()
	snap := s.snapshotLocked()
	s.mu.Unlock()
	var got *wire.PaneInfo
	for i := range snap.Panes {
		if snap.Panes[i].ID == c {
			got = &snap.Panes[i]
		}
	}
	if got == nil {
		t.Fatalf("the snapshot lost the shelved pane: %+v", snap.Panes)
	}
	if !got.Shelf {
		t.Fatal("the shelved pane is not marked on the shelf")
	}
	if got.Rect != (layout.Rect{}) {
		t.Fatalf("shelved pane rect = %+v, want the zero rect", got.Rect)
	}
	if snap.ShelfActive != 0 {
		t.Fatalf("snapshot shelf active = %d, want 0", snap.ShelfActive)
	}
}

// TestShelvePaneRefusesLastPane proves a window never loses its last pane to
// the shelf: the refusal names the reason and leaves the tree alone.
func TestShelvePaneRefusesLastPane(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, _ := rpcServer(t, "rpclast")
	s.mu.Lock()
	win := s.st.ActiveWindow()
	win.Root = &layout.Node{Pane: a}
	win.Focus = a
	s.mu.Unlock()

	wantErr(t, "shelve-pane", "last pane", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "shelve-pane", Target: a, ID: 1}))
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.st.Shelf) != 0 {
		t.Fatalf("a refused shelve left shelf %v", s.st.Shelf)
	}
	if !layout.FindLeaf(s.st.ActiveWindow().Root, a) {
		t.Fatal("a refused shelve removed the pane from its window")
	}
}

// TestShelvePaneErrors pins the two guards: a pane already on the shelf and a
// pane no window holds.
func TestShelvePaneErrors(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _, c := shelfServer(t, "rpcshelverr")
	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "shelve-pane", Target: c, ID: 1}); !m.OK {
		t.Fatalf("shelve: %s", m.Error)
	}
	wantErr(t, "shelve-pane", "already on the shelf", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "shelve-pane", Target: c, ID: 2}))
	wantErr(t, "shelve-pane", "no pane", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "shelve-pane", Target: "ghost", ID: 3}))
}

// TestShelvePaneEmptyTargetUsesFocus pins the default target.
func TestShelvePaneEmptyTargetUsesFocus(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, _, _ := shelfServer(t, "rpcshelvefocus")
	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "shelve-pane", ID: 1})
	if got := replyPane(t, "shelve-pane", m); got != a {
		t.Fatalf("shelve with no target = %q, want the focus %q", got, a)
	}
}

// TestPopPaneAction pins the pop contract: the member returns split off the
// focused pane's right, takes the focus, and leaves the shelf. An empty target
// names the active shelf member.
func TestPopPaneAction(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, b, c := shelfServer(t, "rpcpop")
	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "shelve-pane", Target: c, ID: 1}); !m.OK {
		t.Fatalf("shelve: %s", m.Error)
	}
	s.mu.Lock()
	focus := s.st.ActiveWindow().Focus
	s.mu.Unlock()

	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "pop-pane", ID: 2})
	if got := replyPane(t, "pop-pane", m); got != c {
		t.Fatalf("pop reply = %q, want the active member %q", got, c)
	}
	s.mu.Lock()
	win := s.st.ActiveWindow()
	ids := layout.AllPanes(win.Root)
	at := win.Focus
	shelf := len(s.st.Shelf)
	s.mu.Unlock()

	if len(ids) != 3 {
		t.Fatalf("tree = %v, want the popped pane back", ids)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	if !seen[a] || !seen[b] {
		t.Fatalf("tree = %v, want %s and %s to survive the pop", ids, a, b)
	}
	// The popped pane sits to the right of the pane that held the focus.
	idx := -1
	for i, id := range ids {
		if id == c {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("the popped pane is not in the tree: %v", ids)
	}
	if at != c {
		t.Fatalf("focus = %q, want the popped pane %q", at, c)
	}
	if idx == 0 || ids[idx-1] != focus {
		t.Fatalf("tree = %v, want %q right of %q", ids, c, focus)
	}
	if shelf != 0 {
		t.Fatalf("the shelf still holds %d members", shelf)
	}
}

// TestPopPaneErrors pins the guards: an empty shelf and a pane that is not on
// it.
func TestPopPaneErrors(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, _ := rpcServer(t, "rpcpoperr")
	wantErr(t, "pop-pane", "shelf is empty", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "pop-pane", ID: 1}))
	wantErr(t, "pop-pane", "not on the shelf", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "pop-pane", Target: a, ID: 2}))
}

// TestFocusShelvedPaneMovesTheShelf pins the focus rule: naming a shelved pane
// moves the shelf's active member and leaves the window focus where it was.
func TestFocusShelvedPaneMovesTheShelf(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, b, c := shelfServer(t, "rpcshelffocus")
	for i, id := range []string{c, b} {
		if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "shelve-pane", Target: id, ID: i + 1}); !m.OK {
			t.Fatalf("shelve %s: %s", id, m.Error)
		}
	}
	s.mu.Lock()
	focus := s.st.ActiveWindow().Focus
	s.mu.Unlock()
	if focus != a {
		t.Fatalf("focus = %q, want the surviving pane %q", focus, a)
	}

	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "focus", Target: b, ID: 3})
	if !m.OK {
		t.Fatalf("focus on a shelved pane: %s", m.Error)
	}
	s.mu.Lock()
	active := s.st.ShelfActive
	focus = s.st.ActiveWindow().Focus
	shelf := append([]string(nil), s.st.Shelf...)
	s.mu.Unlock()
	if active != 1 {
		t.Fatalf("shelf active = %d, want 1 for %q at shelf %v", active, b, shelf)
	}
	if focus != a {
		t.Fatalf("window focus moved to %q on a shelved focus request", focus)
	}
}

// TestClosePaneRefusesShelvedPane proves a shelved pane's PTY is never killed:
// the close is refused and the pane's conn hears nothing.
func TestClosePaneRefusesShelvedPane(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _, c := shelfServer(t, "rpcclosehelf")
	spy := spyPane(t, s, c)
	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "shelve-pane", Target: c, ID: 1}); !m.OK {
		t.Fatalf("shelve: %s", m.Error)
	}
	wantErr(t, "close-pane", "on the shelf", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "close-pane", Target: c, ID: 2}))
	s.mu.Lock()
	_, live := s.st.Panes[c]
	s.mu.Unlock()
	if !live {
		t.Fatal("the refused close dropped the shelved pane's state")
	}
	spyEmpty(t, spy)
}

// TestShelvedPaneCountsForTheSession proves a shelved pane keeps the session
// alive: the count that decides a teardown sees it.
func TestShelvedPaneCountsForTheSession(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _, c := shelfServer(t, "rpccountshelf")
	s.mu.Lock()
	before := s.countPanes()
	s.mu.Unlock()
	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "shelve-pane", Target: c, ID: 1}); !m.OK {
		t.Fatalf("shelve: %s", m.Error)
	}
	s.mu.Lock()
	after := s.countPanes()
	s.mu.Unlock()
	if after != before {
		t.Fatalf("countPanes = %d after a shelve, want the %d it counted before", after, before)
	}
}

// TestResizeShelvedPaneSizesItsPTY proves the summon path: a resize naming a
// shelved pane reaches that pane and leaves the client's own size alone.
func TestResizeShelvedPaneSizesItsPTY(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, _, c := shelfServer(t, "rpcresizeshelf")
	spy := spyPane(t, s, c)
	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "shelve-pane", Target: c, ID: 1}); !m.OK {
		t.Fatalf("shelve: %s", m.Error)
	}
	s.mu.Lock()
	cl.cols, cl.rows = 120, 40
	s.mu.Unlock()
	if !s.resizeShelfPane(c, 40, 12) {
		t.Fatal("resizeShelfPane did not claim the shelved pane")
	}
	m := readSpyUntil(t, spy, "resize")
	if m.Cols != 40 || m.Rows != 12 {
		t.Fatalf("pane resize = %dx%d, want 40x12", m.Cols, m.Rows)
	}
	s.mu.Lock()
	cols, rows := cl.cols, cl.rows
	ps := s.st.Panes[c]
	s.mu.Unlock()
	if cols != 120 || rows != 40 {
		t.Fatalf("client size = %dx%d, want it untouched", cols, rows)
	}
	if ps.Cols != 40 || ps.Rows != 12 {
		t.Fatalf("pane state = %dx%d, want 40x12", ps.Cols, ps.Rows)
	}
	if s.resizeShelfPane(a, 10, 10) {
		t.Fatal("resizeShelfPane claimed a pane that is not on the shelf")
	}
}

// TestToggleShelfFold proves the rail's shelf section folds through the same
// action every other fold uses.
func TestToggleShelfFold(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _, _ := shelfServer(t, "rpcselfold")
	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "toggle-fold", Target: wire.ShelfFoldTarget, ID: 1})
	if !m.OK {
		t.Fatalf("toggle-fold shelf: %s", m.Error)
	}
	s.mu.Lock()
	folded := s.st.ShelfFolded
	s.mu.Unlock()
	if !folded {
		t.Fatal("the shelf section did not fold")
	}
	m = rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "toggle-fold", Target: wire.ShelfFoldTarget, ID: 2})
	if !m.OK {
		t.Fatalf("toggle-fold shelf back: %s", m.Error)
	}
	s.mu.Lock()
	folded = s.st.ShelfFolded
	s.mu.Unlock()
	if folded {
		t.Fatal("the shelf section did not unfold")
	}
}

// TestShelfErrorNamesThePane proves the refusal texts name the pane, so a CLI
// error says which one the shelf holds.
func TestShelfErrorNamesThePane(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _, c := shelfServer(t, "rpcnameerr")
	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "shelve-pane", Target: c, ID: 1}); !m.OK {
		t.Fatalf("shelve: %s", m.Error)
	}
	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "close-pane", Target: c, ID: 2})
	if m.OK || !strings.Contains(m.Error, c) {
		t.Fatalf("close error = %q, want it to name %q", m.Error, c)
	}
}

// TestShelveLeavesASidePaneFocusAlone proves the focus rule: only a pane that
// held the focus passes it on. Shelving a pane from the side leaves the
// operator's focus exactly where it was.
func TestShelveLeavesASidePaneFocusAlone(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, b, c := shelfServer(t, "rpcshelvefocuskeep")
	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "focus", Target: a, ID: 1}); !m.OK {
		t.Fatalf("focus %s: %s", a, m.Error)
	}
	s.mu.Lock()
	focus := s.st.ActiveWindow().Focus
	s.mu.Unlock()
	if focus != a {
		t.Fatalf("focus = %q, want %q before the shelve", focus, a)
	}

	// b is neither the focused pane nor the shelf target: shelving c must not
	// move the focus.
	if focus == b || focus == c {
		t.Fatalf("fixture focus %q must differ from %q and %q", focus, b, c)
	}
	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "shelve-pane", Target: c, ID: 2}); !m.OK {
		t.Fatalf("shelve: %s", m.Error)
	}
	s.mu.Lock()
	after := s.st.ActiveWindow().Focus
	s.mu.Unlock()
	if after != focus {
		t.Fatalf("focus moved from %q to %q when a pane off focus was shelved", focus, after)
	}

	// The shelved pane's own focus still moves: shelve the focused pane and the
	// window hands focus to a survivor.
	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "shelve-pane", Target: after, ID: 3}); !m.OK {
		t.Fatalf("shelve the focus: %s", m.Error)
	}
	s.mu.Lock()
	win := s.st.ActiveWindow()
	ids := layout.AllPanes(win.Root)
	moved := win.Focus
	s.mu.Unlock()
	if moved == focus {
		t.Fatalf("shelving the focused pane %q left the focus on it", focus)
	}
	if !layout.FindLeaf(win.Root, moved) {
		t.Fatalf("focus = %q, want a pane in the tree %v", moved, ids)
	}
}

// TestClosePaneRefusesTheLastTreePaneWithAShelf proves the session can never
// reach zero windows with a live shelf: the close is refused, the error names
// the shelf count, and the pane keeps its PTY.
func TestClosePaneRefusesTheLastTreePaneWithAShelf(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _, c := shelfServer(t, "rpclastwithshelf")
	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "shelve-pane", Target: c, ID: 1}); !m.OK {
		t.Fatalf("shelve: %s", m.Error)
	}
	// Reduce the tree to one pane, then spy on that pane: it is the target the
	// refused close names, so its conn is the one that must stay silent.
	s.mu.Lock()
	ids := layout.AllPanes(s.st.ActiveWindow().Root)
	s.mu.Unlock()
	for i, id := range ids {
		if i == len(ids)-1 {
			continue
		}
		if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "close-pane", Target: id, ID: 2 + i}); !m.OK {
			t.Fatalf("close %s: %s", id, m.Error)
		}
	}
	s.mu.Lock()
	win := s.st.ActiveWindow()
	rest := layout.AllPanes(win.Root)
	shelf := len(s.st.Shelf)
	s.mu.Unlock()
	if len(rest) != 1 || shelf != 1 {
		t.Fatalf("after the setup the session has %v tree panes and %d shelved, want 1 and 1", rest, shelf)
	}
	last := rest[0]
	spy := spyPane(t, s, last)

	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "close-pane", Target: last, ID: 9})
	// The refusal must come back without a kill: a close that reached its own
	// target would take the last pane and strand the shelf. The drain runs
	// first, so a revert fails on the kill and not on the missing error.
	deadline := time.After(200 * time.Millisecond)
drain:
	for {
		select {
		case msg := <-spy:
			if msg.Action == "kill" {
				t.Fatalf("the refused close sent kill to its target %q", last)
			}
		case <-deadline:
			break drain
		}
	}
	wantErr(t, "close-pane", "shelf", m)
	if !strings.Contains(m.Error, "1 pane") {
		t.Fatalf("close error = %q, want it to name the shelf count", m.Error)
	}
	s.mu.Lock()
	tree := s.treePaneCountLocked()
	s.mu.Unlock()
	if tree != 1 {
		t.Fatalf("the refused close took the last tree pane, %d left", tree)
	}
}

// TestNewWindowBuildsAWorkspaceWhenThereIsNone proves new-window never appends
// to the throwaway workspace ActiveWorkspace hands back for an empty session:
// the window lands in a real workspace, so a pop can use it.
func TestNewWindowBuildsAWorkspaceWhenThereIsNone(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, _, _ := shelfServer(t, "rpcnewws")
	// The pane new-window will open, so the join finds a socket instead of
	// spawning a process.
	screenPane(t, s.st, "w2p1")
	// The state the shelf can leave behind: a live shelf, no workspace.
	s.mu.Lock()
	s.st.Shelf = []string{a}
	s.st.Workspaces = nil
	s.st.Active = 0
	s.mu.Unlock()

	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "new-window", ID: 1})
	if !m.OK {
		t.Fatalf("new-window: %s", m.Error)
	}
	s.mu.Lock()
	workspaces := len(s.st.Workspaces)
	windows := 0
	if workspaces > 0 {
		windows = len(s.st.Workspaces[0].Windows)
	}
	wsID := ""
	if workspaces > 0 {
		wsID = s.st.Workspaces[0].ID
	}
	s.mu.Unlock()
	if workspaces != 1 || windows != 1 {
		t.Fatalf("new-window left %d workspaces and %d windows, want one each", workspaces, windows)
	}
	if wsID == "" || wsID == "s0" {
		t.Fatalf("workspace id = %q, want a fresh one from NewWorkspaceID", wsID)
	}
}

// shelfExitSession is a session with one tree pane and two shelf members: the
// state a shell exit leaves when the last tree pane goes.
func shelfExitSession(t *testing.T, name string) (*server, string, [2]string) {
	t.Helper()
	st := session.NewStateAt(t.TempDir(), name)
	win := st.ActiveWindow()
	a, b, c := win.NewPaneID(), win.NewPaneID(), win.NewPaneID()
	win.Root = &layout.Node{Pane: a}
	win.Focus = a
	st.Panes[a] = &session.PaneState{}
	st.Panes[b] = &session.PaneState{}
	st.Panes[c] = &session.PaneState{}
	st.Shelf = []string{b, c}
	st.ShelfActive = 0
	s := railServer(st)
	paneConnFor(t, s, a, "done for now")
	return s, a, [2]string{b, c}
}

// emptyWorkspaceSession saves a session that has a live shelf and no workspace:
// the state an older build could strand, and the state the start path and pop
// must both survive.
func emptyWorkspaceSession(t *testing.T, name string) (string, string) {
	t.Helper()
	root := t.TempDir()
	st := session.NewStateAt(root, name)
	win := st.ActiveWindow()
	id := win.NewPaneID()
	win.Root = nil
	st.Panes = map[string]*session.PaneState{id: {}}
	st.Workspaces = nil
	st.Active = 0
	st.Shelf = []string{id}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	return root, id
}

// TestPaneExitWithAShelfKeepsAWindow proves a tree pane's own exit never leaves
// zero windows with a live shelf: the shelf's active member takes the emptied
// window, the state round-trips, a restart seeds no phantom pane, and a pop
// lands the rest in a real window.
func TestPaneExitWithAShelfKeepsAWindow(t *testing.T) {
	s, a, shelf := shelfExitSession(t, "shelfexit")
	s.paneExited(a)

	st := s.st
	if len(st.Workspaces) != 1 {
		t.Fatalf("workspaces = %d, want the one window kept", len(st.Workspaces))
	}
	ws := st.Workspaces[0]
	if len(ws.Windows) != 1 {
		t.Fatalf("windows = %d, want one", len(ws.Windows))
	}
	win := ws.Windows[0]
	if ids := layout.AllPanes(win.Root); len(ids) != 1 || ids[0] != shelf[0] {
		t.Fatalf("window root = %v, want the active shelf member %q", ids, shelf[0])
	}
	if win.Focus != shelf[0] {
		t.Fatalf("focus = %q, want the promoted member %q", win.Focus, shelf[0])
	}
	if len(st.Shelf) != 1 || st.Shelf[0] != shelf[1] {
		t.Fatalf("shelf = %v, want the member that stayed", st.Shelf)
	}
	if st.ShelfActive != 0 {
		t.Fatalf("shelf active = %d, want 0", st.ShelfActive)
	}
	if _, ok := st.Panes[shelf[0]]; !ok {
		t.Fatal("the promoted pane lost its state")
	}
	if s.countPanes() != 2 {
		t.Fatalf("countPanes = %d, want the promoted pane and the member left", s.countPanes())
	}

	// A restart reads that state back and seeds nothing from a throwaway
	// window: every pane in the state is one the session really has.
	loaded, err := session.LoadStateAt(st.Dir(), st.Name)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	next := railServer(loaded)
	next.seedWindow()
	for id := range loaded.Panes {
		if id != shelf[0] && id != shelf[1] {
			t.Fatalf("the start path stored a phantom pane %q: %v", id, loaded.Panes)
		}
	}
	if len(loaded.Shelf) != 1 || loaded.Shelf[0] != shelf[1] {
		t.Fatalf("reloaded shelf = %v, want the member that stayed", loaded.Shelf)
	}

	// The member left pops into the window the reloaded session has.
	if id, err := next.popPane(""); err != nil || id != shelf[1] {
		t.Fatalf("pop after restart = %q, %v", id, err)
	}
	lws := loaded.Workspaces[0]
	lwin := lws.Windows[0]
	if lwin.Focus != shelf[1] {
		t.Fatalf("focus = %q, want the popped member %q", lwin.Focus, shelf[1])
	}
	if ids := layout.AllPanes(lwin.Root); len(ids) != 2 {
		t.Fatalf("tree = %v, want the member beside the promoted pane", ids)
	}
	if len(loaded.Shelf) != 0 {
		t.Fatalf("shelf = %v, want it empty after the pop", loaded.Shelf)
	}
}

// TestStartSkipsTheSeedWithNoWorkspace proves the start path never mints a pane
// from the throwaway window ActiveWindow returns: a session with only a shelf
// restarts with exactly its shelf, and no phantom id reaches the state file.
func TestStartSkipsTheSeedWithNoWorkspace(t *testing.T) {
	root, id := emptyWorkspaceSession(t, "shelfnoseed")
	loaded, err := session.LoadStateAt(root, "shelfnoseed")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	s := railServer(loaded)
	s.seedWindow()
	if len(loaded.Workspaces) != 0 {
		t.Fatalf("the start path built %d workspaces, want none", len(loaded.Workspaces))
	}
	if len(loaded.Panes) != 1 {
		t.Fatalf("panes = %v, want the shelf member alone", loaded.Panes)
	}
	if _, ok := loaded.Panes[id]; !ok {
		t.Fatalf("panes = %v, want %q", loaded.Panes, id)
	}
	// The state parses back with no phantom pane either.
	if err := loaded.Save(); err != nil {
		t.Fatal(err)
	}
	again, err := session.LoadStateAt(root, "shelfnoseed")
	if err != nil {
		t.Fatalf("second reload: %v", err)
	}
	for pid := range again.Panes {
		if pid != id {
			t.Fatalf("a save and load minted the phantom pane %q", pid)
		}
	}
	if len(again.Shelf) != 1 || again.Shelf[0] != id {
		t.Fatalf("shelf = %v, want %q", again.Shelf, id)
	}
}

// TestPopPaneBuildsAWorkspaceWhenThereIsNone proves pop is always landable: a
// session with a live shelf and no workspace gets a real one, with a real
// window, and the member becomes its root and its focus.
func TestPopPaneBuildsAWorkspaceWhenThereIsNone(t *testing.T) {
	root, id := emptyWorkspaceSession(t, "shelfpopws")
	loaded, err := session.LoadStateAt(root, "shelfpopws")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	s := railServer(loaded)
	s.seedWindow()
	if got, err := s.popPane(""); err != nil || got != id {
		t.Fatalf("pop = %q, %v", got, err)
	}
	if len(loaded.Workspaces) != 1 {
		t.Fatalf("workspaces = %d, want one", len(loaded.Workspaces))
	}
	ws := loaded.Workspaces[0]
	if ws.ID == "" || len(ws.Windows) != 1 {
		t.Fatalf("workspace = %+v, want one real window", ws)
	}
	win := ws.Windows[0]
	if win.ID == "" {
		t.Fatal("the built window has no id")
	}
	if ids := layout.AllPanes(win.Root); len(ids) != 1 || ids[0] != id {
		t.Fatalf("window root = %v, want the member %q", ids, id)
	}
	if win.Focus != id {
		t.Fatalf("focus = %q, want the member", win.Focus)
	}
	if len(loaded.Shelf) != 0 {
		t.Fatalf("shelf = %v, want it empty", loaded.Shelf)
	}
	if err := loaded.Save(); err != nil {
		t.Fatal(err)
	}
	again, err := session.LoadStateAt(root, "shelfpopws")
	if err != nil {
		t.Fatalf("second reload: %v", err)
	}
	if len(again.Workspaces) != 1 || len(again.Workspaces[0].Windows) != 1 {
		t.Fatalf("reloaded workspaces = %+v, want one with one window", again.Workspaces)
	}
}

// TestNewWindowRefusedLeavesNoEmptyWorkspace proves a targeted new-window that
// names no workspace or pane leaves the state exactly as it was: the workspace
// it would need is built only after the target resolves.
func TestNewWindowRefusedLeavesNoEmptyWorkspace(t *testing.T) {
	root, id := emptyWorkspaceSession(t, "shelfbadtarget")
	loaded, err := session.LoadStateAt(root, "shelfbadtarget")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	before, err := json.Marshal(loaded.Workspaces)
	if err != nil {
		t.Fatal(err)
	}
	s := railServer(loaded)
	// The target is the shelf member: it is in no tree, so it names no window
	// either.
	if _, _, err := s.newWindow(id, "", "", false, nil, nil); err == nil {
		t.Fatal("new-window accepted a target that is no workspace and no pane")
	}
	after, err := json.Marshal(loaded.Workspaces)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("a refused new-window changed the workspaces: %s -> %s", before, after)
	}
	if len(loaded.Workspaces) != 0 {
		t.Fatalf("workspaces = %+v, want none", loaded.Workspaces)
	}
	// A save and a load of that state carry no empty workspace either.
	if err := loaded.Save(); err != nil {
		t.Fatal(err)
	}
	again, err := session.LoadStateAt(root, "shelfbadtarget")
	if err != nil {
		t.Fatalf("second reload: %v", err)
	}
	if len(again.Workspaces) != 0 {
		t.Fatalf("reloaded workspaces = %+v, want none", again.Workspaces)
	}
	if len(again.Shelf) != 1 || again.Shelf[0] != id {
		t.Fatalf("shelf = %v, want the member %q", again.Shelf, id)
	}
}

// TestStartSkipsTheSeedWithNoWindow proves the same refusal one level down: a
// workspace with no window hands back a throwaway window, so the start path
// seeds no pane into it either.
func TestStartSkipsTheSeedWithNoWindow(t *testing.T) {
	root := t.TempDir()
	st := session.NewStateAt(root, "shelfnowindow")
	win := st.ActiveWindow()
	id := win.NewPaneID()
	st.Panes = map[string]*session.PaneState{id: {}}
	ws := st.ActiveWorkspace()
	ws.Windows = nil
	ws.Active = 0
	st.Shelf = []string{id}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := session.LoadStateAt(root, "shelfnowindow")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	s := railServer(loaded)
	s.seedWindow()
	if len(loaded.Panes) != 1 {
		t.Fatalf("panes = %v, want the shelf member alone", loaded.Panes)
	}
	if _, ok := loaded.Panes[id]; !ok {
		t.Fatalf("panes = %v, want %q", loaded.Panes, id)
	}
	if len(loaded.Workspaces) != 1 || len(loaded.Workspaces[0].Windows) != 0 {
		t.Fatalf("the seed built %+v, want the workspace left as it was", loaded.Workspaces)
	}
}
