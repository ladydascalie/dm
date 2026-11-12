package server

import (
	"testing"

	"dm/internal/layout"
	"dm/internal/session"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// TestSelectLayoutAction pins the action: it applies the preset to the active
// window's pane ids in tree order, the pane set and its order survive, and the
// focus stays on the focused pane through every preset.
func TestSelectLayoutAction(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, b := rpcServer(t, "rpcselectlayout")
	screenPane(t, s.st, "w1p3")
	screenPane(t, s.st, "w1p4")
	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "split", Target: b, Dir: "down", ID: 1}); !m.OK {
		t.Fatalf("split: %s", m.Error)
	}
	s.mu.Lock()
	before := layout.AllPanes(s.st.ActiveWindow().Root)
	if len(before) != 3 {
		s.mu.Unlock()
		t.Fatalf("setup panes = %v, want three", before)
	}
	focus := s.st.ActiveWindow().Focus
	s.mu.Unlock()
	if focus == "" {
		t.Fatal("setup left no focus")
	}

	for i, name := range []string{
		"even-horizontal", "even-vertical", "main-horizontal", "main-vertical", "tiled",
	} {
		m := rpcCall(t, s, cl, conn,
			wire.Msg{Type: "req", Action: "select-layout", Layout: name, ID: 2 + i})
		if !m.OK {
			t.Fatalf("%s: %s", name, m.Error)
		}
		s.mu.Lock()
		win := s.st.ActiveWindow()
		ids := layout.AllPanes(win.Root)
		gotFocus := win.Focus
		s.mu.Unlock()
		if len(ids) != len(before) {
			t.Fatalf("%s holds %v, want three panes", name, ids)
		}
		for j := range before {
			if ids[j] != before[j] {
				t.Fatalf("%s order = %v, want %v", name, ids, before)
			}
		}
		if gotFocus != focus {
			t.Fatalf("%s moved the focus to %q, want %q", name, gotFocus, focus)
		}
	}

	// An unknown preset is refused and leaves the tree alone.
	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "select-layout", Layout: "diagonal", ID: 9}); m.OK {
		t.Fatal("unknown preset = OK, want an error")
	}
	wantErr(t, "select-layout", "no layout", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "select-layout", Layout: "diagonal", ID: 10}))
}

// TestSelectLayoutSinglePaneFails pins the guard: a window with one pane
// answers with an error naming the reason.
func TestSelectLayoutSinglePaneFails(t *testing.T) {
	t.Parallel()
	st := session.NewStateAt(t.TempDir(), "rpcselectsingle")
	id := testutil.RootPane(st)
	st.Workspaces[0].Windows[0].Root = &layout.Node{Pane: id}
	s := railServer(st)
	cl, conn := boxConn(t)

	wantErr(t, "select-layout", "at least two panes", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "select-layout", Layout: "tiled", ID: 1}))
}
