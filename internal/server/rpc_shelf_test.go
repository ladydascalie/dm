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
	s, cl, conn, a, _, c := shelfServer(t, "rpclastwithshelf")
	spy := spyPane(t, s, a)
	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "shelve-pane", Target: c, ID: 1}); !m.OK {
		t.Fatalf("shelve: %s", m.Error)
	}
	// Reduce the tree to the one pane the close would take.
	s.mu.Lock()
	win := s.st.ActiveWindow()
	closest := win.Focus
	other := ""
	for _, id := range layout.AllPanes(win.Root) {
		if id != closest {
			other = id
		}
	}
	s.mu.Unlock()
	if other != "" {
		if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "close-pane", Target: other, ID: 2}); !m.OK {
			t.Fatalf("close %s: %s", other, m.Error)
		}
	}
	s.mu.Lock()
	tree := s.treePaneCountLocked()
	shelf := len(s.st.Shelf)
	s.mu.Unlock()
	if tree != 1 || shelf != 1 {
		t.Fatalf("after the setup the session has %d tree panes and %d shelved, want 1 and 1", tree, shelf)
	}

	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "close-pane", ID: 3})
	wantErr(t, "close-pane", "shelf", m)
	if !strings.Contains(m.Error, "1 pane") {
		t.Fatalf("close error = %q, want it to name the shelf count", m.Error)
	}
	s.mu.Lock()
	tree = s.treePaneCountLocked()
	s.mu.Unlock()
	if tree != 1 {
		t.Fatalf("the refused close took the last tree pane, %d left", tree)
	}
	// The refused close must not reach the pane. A resync may still send it a
	// resize, so the test looks for the kill frame alone.
	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case msg := <-spy:
			if msg.Action == "kill" {
				t.Fatalf("the refused close sent kill to %q", a)
			}
		case <-deadline:
			return
		}
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
