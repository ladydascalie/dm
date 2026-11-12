package server

import (
	"encoding/json"
	"testing"

	"dm/internal/layout"
	"dm/internal/session"
	"dm/internal/wire"
)

// replyLayout decodes the layout name a select-layout reply carries.
func replyLayout(t *testing.T, m wire.Msg) string {
	t.Helper()
	var out struct {
		Layout string `json:"layout"`
	}
	if err := json.Unmarshal(m.Res, &out); err != nil {
		t.Fatalf("reply layout: %v", err)
	}
	return out.Layout
}

// neighborOf is what focus-dir would reach from the active window's focus in
// dir, an independent read of the same resolution the swap reuse.
func neighborOf(t *testing.T, s *server, dir string) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	win := s.st.ActiveWindow()
	rects := map[string]layout.Rect{}
	layout.Layout(win.Root, layout.PaneAreaForWithTabBar(s.cols, s.rows, s.railLocked()), s.cfg.Padding(), rects)
	return layout.Neighbor(rects, win.Focus, dir)
}

// swapDirTree is a 2x2 grid row(a,b) over row(c,d), the shape where every
// pane has a neighbour in at least one of the four directions.
func swapDirTree(t *testing.T, s *server, a, b, c, d string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.ActiveWindow().Root = &layout.Node{Dir: "col", Kids: []*layout.Node{
		{Dir: "row", Kids: []*layout.Node{{Pane: a}, {Pane: b}}},
		{Dir: "row", Kids: []*layout.Node{{Pane: c}, {Pane: d}}},
	}}
}

// TestSwapPanesByDir pins the dir form: each direction swaps the focused pane
// with exactly the neighbour focus-dir would reach, the focus survives, and
// a manual swap forgets the stored layout. The response carries no body, only
// ok.
func TestSwapPanesByDir(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, b := rpcServer(t, "rpcswapdir")
	screenPane(t, s.st, "w1p3")
	screenPane(t, s.st, "w1p4")
	c, d := "w1p3", "w1p4"
	swapDirTree(t, s, a, b, c, d)

	for i, tc := range []struct{ dir, src string }{
		{"right", a},
		{"left", b},
		{"down", a},
		{"up", c},
	} {
		swapDirTree(t, s, a, b, c, d)
		s.mu.Lock()
		s.st.ActiveWindow().Focus = tc.src
		s.mu.Unlock()
		dst := neighborOf(t, s, tc.dir)
		if dst == "" {
			t.Fatalf("%s from %s resolves no neighbour", tc.dir, tc.src)
		}
		s.mu.Lock()
		win := s.st.ActiveWindow()
		win.Layout = "tiled"
		before := paneRects(t, s, win)
		s.mu.Unlock()

		m := rpcCall(t, s, cl, conn,
			wire.Msg{Type: "req", Action: "swap-panes", Dir: tc.dir, ID: 1 + i})
		if !m.OK {
			t.Fatalf("swap-panes %s from %s: %s", tc.dir, tc.src, m.Error)
		}
		s.mu.Lock()
		if s.st.ActiveWindow().Focus != tc.src {
			t.Fatalf("%s moved the focus to %q, want %q", tc.dir, s.st.ActiveWindow().Focus, tc.src)
		}
		if s.st.ActiveWindow().Layout != "" {
			t.Fatalf("%s did not clear the stored layout", tc.dir)
		}
		after := paneRects(t, s, s.st.ActiveWindow())
		s.mu.Unlock()
		if after[dst] != before[tc.src] {
			t.Fatalf("%s swapped in %s but %s sits where %s was", tc.dir, dst, dst, tc.src)
		}
		if after[tc.src] != before[dst] {
			t.Fatalf("%s left %s where %s was", tc.dir, tc.src, dst)
		}
	}
}

// TestSwapPanesDirErrors pins the guards: no neighbour names the direction and
// the pane, and the action takes exactly one of dir or other.
func TestSwapPanesDirErrors(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, _ := rpcServer(t, "rpcswapdirerr")
	// row(a,b) focused on a has no left neighbour.
	s.mu.Lock()
	focus := s.st.ActiveWindow().Focus
	s.mu.Unlock()
	if focus != a {
		t.Fatalf("focus = %q, want %q", focus, a)
	}
	wantErr(t, "swap-panes", "no pane left of", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "swap-panes", Dir: "left", ID: 1}))
	// Both dir and other violate the one-of rule.
	wantErr(t, "swap-panes", "dir or other, not both", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "swap-panes", Dir: "right", Other: "ghost", ID: 2}))
	// Neither dir nor other violates the one-of rule.
	wantErr(t, "swap-panes", "needs a dir or an other pane", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "swap-panes", ID: 3}))
}

// TestSelectLayoutNextCycle pins the cycle: five "next" requests walk the
// fixed order and a sixth wraps back to the first, each reply and the window's
// stored name following the applied preset.
func TestSelectLayoutNextCycle(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _ := rpcServer(t, "rpcselectnext")
	order := []string{"even-horizontal", "even-vertical", "main-horizontal", "main-vertical", "tiled"}
	for i := 0; i < 8; i++ {
		want := order[i%len(order)]
		m := rpcCall(t, s, cl, conn,
			wire.Msg{Type: "req", Action: "select-layout", Layout: "next", ID: 1 + i})
		if !m.OK {
			t.Fatalf("next %d: %s", i, m.Error)
		}
		if got := replyLayout(t, m); got != want {
			t.Fatalf("next %d replied %q, want %q", i, got, want)
		}
		s.mu.Lock()
		stored := s.st.ActiveWindow().Layout
		s.mu.Unlock()
		if stored != want {
			t.Fatalf("next %d stored %q, want %q", i, stored, want)
		}
	}
}

// TestSelectLayoutExplicitStoresAndApplies pins the explicit form: the reply
// names the applied preset, the window stores it, and a following "next" walks
// on from there rather than the first.
func TestSelectLayoutExplicitStoresAndApplies(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _ := rpcServer(t, "rpcselectexplicit")

	m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "select-layout", Layout: "main-vertical", ID: 1})
	if !m.OK {
		t.Fatalf("select-layout: %s", m.Error)
	}
	if got := replyLayout(t, m); got != "main-vertical" {
		t.Fatalf("reply = %q, want main-vertical", got)
	}
	s.mu.Lock()
	stored := s.st.ActiveWindow().Layout
	s.mu.Unlock()
	if stored != "main-vertical" {
		t.Fatalf("stored %q, want main-vertical", stored)
	}

	// next walks on from the stored name.
	m = rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "select-layout", Layout: "next", ID: 2})
	if !m.OK {
		t.Fatalf("next: %s", m.Error)
	}
	if got := replyLayout(t, m); got != "tiled" {
		t.Fatalf("next after main-vertical replied %q, want tiled", got)
	}
}

// TestSelectLayoutStoredSurvivesRestart pins persistence: an explicit
// select-layout saves the name, and a fresh load on the same files sees it.
func TestSelectLayoutStoredSurvivesRestart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	st := session.NewStateAt(root, "layoutrestart")
	w := st.ActiveWindow()
	a := w.NewPaneID()
	b := w.NewPaneID()
	w.Root = &layout.Node{Dir: "row", Kids: []*layout.Node{{Pane: a}, {Pane: b}}}
	w.Focus = a
	st.Panes[a] = &session.PaneState{}
	st.Panes[b] = &session.PaneState{}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	s := railServer(st)
	cl, conn := boxConn(t)

	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "select-layout", Layout: "tiled", ID: 1}); !m.OK {
		t.Fatalf("select-layout: %s", m.Error)
	}

	got, err := session.LoadStateAt(root, "layoutrestart")
	if err != nil {
		t.Fatal(err)
	}
	if stored := got.ActiveWindow().Layout; stored != "tiled" {
		t.Fatalf("stored layout after restart = %q, want tiled", stored)
	}
}

// TestSelectLayoutSplitClears pins the manual-change clear: selecting a layout,
// then splitting, drops the stored name so the next "next" starts at the
// first preset again.
func TestSelectLayoutSplitClears(t *testing.T) {
	t.Parallel()
	s, cl, conn, b, _ := rpcServer(t, "rpcselectsplitclear")
	screenPane(t, s.st, "w1p3")

	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "select-layout", Layout: "main-vertical", ID: 1}); !m.OK {
		t.Fatalf("select-layout: %s", m.Error)
	}
	s.mu.Lock()
	stored := s.st.ActiveWindow().Layout
	s.mu.Unlock()
	if stored != "main-vertical" {
		t.Fatalf("set up %q, want main-vertical", stored)
	}

	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "split", Target: b, Dir: "down", ID: 2}); !m.OK {
		t.Fatalf("split: %s", m.Error)
	}
	s.mu.Lock()
	stored = s.st.ActiveWindow().Layout
	s.mu.Unlock()
	if stored != "" {
		t.Fatalf("split left the stored layout %q, want it cleared", stored)
	}

	m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "select-layout", Layout: "next", ID: 3})
	if !m.OK {
		t.Fatalf("next after split: %s", m.Error)
	}
	if got := replyLayout(t, m); got != "even-horizontal" {
		t.Fatalf("next after a split = %q, want to restart at even-horizontal", got)
	}
}

// TestManualTreeChangesClearStoredLayout pins the other manual edits a window
// can suffer: move-pane, swap-panes and close-pane each forget the stored
// preset so "next" restarts the cycle.
func TestManualTreeChangesClearStoredLayout(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, b := rpcServer(t, "rpcmanualclear")
	win := s.st.ActiveWindow()

	stash := func(id int) {
		t.Helper()
		if m := rpcCall(t, s, cl, conn,
			wire.Msg{Type: "req", Action: "select-layout", Layout: "even-vertical", ID: id}); !m.OK {
			t.Fatalf("stash: %s", m.Error)
		}
		s.mu.Lock()
		if win.Layout != "even-vertical" {
			t.Fatalf("setup stored %q", win.Layout)
		}
		s.mu.Unlock()
	}
	cleared := func(name string) {
		t.Helper()
		s.mu.Lock()
		ok := win.Layout == ""
		s.mu.Unlock()
		if !ok {
			t.Fatalf("%s left the stored layout %q", name, win.Layout)
		}
	}

	stash(1)
	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "move-pane", Target: a, Other: b, Dir: "right", ID: 2}); !m.OK {
		t.Fatalf("move-pane: %s", m.Error)
	}
	cleared("move-pane")

	stash(3)
	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "swap-panes", Target: a, Other: b, ID: 4}); !m.OK {
		t.Fatalf("swap-panes: %s", m.Error)
	}
	cleared("swap-panes")

	stash(5)
	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "close-pane", Target: b, ID: 6}); !m.OK {
		t.Fatalf("close-pane: %s", m.Error)
	}
	cleared("close-pane")
}
