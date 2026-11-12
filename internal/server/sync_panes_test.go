package server

import (
	"encoding/json"
	"os"
	"testing"

	"dm/internal/layout"
	"dm/internal/session"
	"dm/internal/wire"
)

// TestSynchronizePanesToggle pins the action: absent toggles, true and false
// set, the flag lands on the active window only, and the snapshot carries it.
func TestSynchronizePanesToggle(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _ := rpcServer(t, "rpcsynctoggle")

	// A second window must stay untouched by the toggle.
	s.mu.Lock()
	s.st.Workspaces[0].Windows = append(s.st.Workspaces[0].Windows,
		&session.Window{ID: "w2", Name: "two", Root: &layout.Node{Pane: "w2p1"}, Focus: "w2p1"})
	s.st.Panes["w2p1"] = &session.PaneState{}
	s.mu.Unlock()

	toggle := func(id int) bool {
		t.Helper()
		m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "synchronize-panes", ID: id})
		if !m.OK {
			t.Fatalf("synchronize-panes: %s", m.Error)
		}
		var rep struct {
			Sync bool `json:"sync"`
		}
		if err := json.Unmarshal(m.Res, &rep); err != nil {
			t.Fatalf("decode reply: %v", err)
		}
		return rep.Sync
	}

	if !toggle(1) {
		t.Fatal("the first toggle left sync off")
	}
	if toggle(2) {
		t.Fatal("the second toggle left sync on")
	}

	on := true
	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "synchronize-panes", On: &on, ID: 3}); !m.OK {
		t.Fatalf("synchronize-panes on: %s", m.Error)
	}
	s.mu.Lock()
	first := s.st.Workspaces[0].Windows[0].Sync
	second := s.st.Workspaces[0].Windows[1].Sync
	s.mu.Unlock()
	if !first {
		t.Fatal("an explicit true left sync off")
	}
	if second {
		t.Fatal("the toggle hit the non-active window")
	}
	if snap := snapshot(s); !snap.Sync {
		t.Fatal("the snapshot does not carry the active window's sync")
	}

	off := false
	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "synchronize-panes", On: &off, ID: 4}); !m.OK {
		t.Fatalf("synchronize-panes off: %s", m.Error)
	}
	if snap := snapshot(s); snap.Sync {
		t.Fatal("an explicit false left sync on")
	}
}

// TestSynchronizePanesPersistsAcrossRestart proves the toggle lands in the
// state file: a server built from the same files after the action still sees
// the flag on.
func TestSynchronizePanesPersistsAcrossRestart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	st := session.NewStateAt(root, "sync")
	w := st.ActiveWindow()
	a := w.NewPaneID()
	b := w.NewPaneID()
	w.Root = &layout.Node{Dir: "row", Kids: []*layout.Node{{Pane: a}, {Pane: b}}}
	w.Focus = a
	st.Panes[a] = &session.PaneState{}
	st.Panes[b] = &session.PaneState{}
	if err := os.MkdirAll(st.SessionDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	s := railServer(st)
	cl, conn := boxConn(t)

	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "synchronize-panes", ID: 1}); !m.OK {
		t.Fatalf("synchronize-panes: %s", m.Error)
	}

	// The action saved state; a fresh load on the same files sees the flag.
	got, err := session.LoadStateAt(root, "sync")
	if err != nil {
		t.Fatal(err)
	}
	if !got.ActiveWindow().Sync {
		t.Fatal("sync did not survive a restart")
	}
}

// syncWriteTree is the shared broadcast shape: window one holds
// row(a, col(b, c)) and window two holds d alone.
func syncWriteTree(t *testing.T, s *server, a, b string) {
	t.Helper()
	screenPane(t, s.st, "w1p3")
	screenPane(t, s.st, "w1p4")
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.st.Workspaces[0]
	ws.Windows[0].Root = &layout.Node{Dir: "row", Kids: []*layout.Node{
		{Pane: a},
		{Dir: "col", Kids: []*layout.Node{{Pane: b}, {Pane: "w1p3"}}},
	}}
	s.st.Panes["w1p3"] = &session.PaneState{}
	ws.Windows = append(ws.Windows,
		&session.Window{ID: "w2", Name: "two", Root: &layout.Node{Pane: "w1p4"}})
	s.st.Panes["w1p4"] = &session.PaneState{}
}

// TestSyncBroadcastWrite pins the synced write path: every live pane of the
// window gets the write in tree order, none outside the window does, and the
// off path keeps writing to the one named pane.
func TestSyncBroadcastWrite(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, b := rpcServer(t, "rpcsyncwrite")
	syncWriteTree(t, s, a, b)

	spyA := spyPane(t, s, a)
	spyB := spyPane(t, s, b)
	spyC := spyPane(t, s, "w1p3")
	spyD := spyPane(t, s, "w1p4")

	// Sync off: the write reaches only the target, byte-identical to today.
	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "write-to", Target: b, Msg: "solo", Enter: true, ID: 1}); !m.OK {
		t.Fatalf("write-to: %s", m.Error)
	}
	if m := readSpy(t, spyB); m.Action != "write" || m.Msg != "solo" || !m.Enter {
		t.Fatalf("off write = %+v, want the solo frame on the target", m)
	}
	spyEmpty(t, spyA)
	spyEmpty(t, spyC)
	spyEmpty(t, spyD)

	// Sync on: the same write reaches every live pane of window one in tree
	// order, and the pane in window two gets nothing.
	on := true
	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "synchronize-panes", On: &on, ID: 2}); !m.OK {
		t.Fatalf("synchronize-panes: %s", m.Error)
	}
	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "write-to", Target: b, Msg: "all", Enter: true, ID: 3}); !m.OK {
		t.Fatalf("synced write-to: %s", m.Error)
	}
	for _, spy := range []chan wire.Msg{spyA, spyB, spyC} {
		if m := readSpy(t, spy); m.Action != "write" || m.Msg != "all" || !m.Enter {
			t.Fatalf("synced write = %+v, want the all frame", m)
		}
	}
	spyEmpty(t, spyD)
}

// TestSyncBroadcastKeys pins the keys path: the same keys frame reaches every
// live pane of the synced window, and the off path sends only to the target.
func TestSyncBroadcastKeys(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, b := rpcServer(t, "rpcsynckeys")
	syncWriteTree(t, s, a, b)

	spyA := spyPane(t, s, a)
	spyB := spyPane(t, s, b)
	spyC := spyPane(t, s, "w1p3")
	spyD := spyPane(t, s, "w1p4")

	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "keys", Target: b, Keys: []byte("x"), ID: 1}); !m.OK {
		t.Fatalf("keys: %s", m.Error)
	}
	if m := readSpy(t, spyB); m.Action != "write" || string(m.Keys) != "x" {
		t.Fatalf("off keys = %+v, want the x frame on the target", m)
	}
	spyEmpty(t, spyA)
	spyEmpty(t, spyC)
	spyEmpty(t, spyD)

	on := true
	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "synchronize-panes", On: &on, ID: 2}); !m.OK {
		t.Fatalf("synchronize-panes: %s", m.Error)
	}
	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "keys", Target: b, Keys: []byte("y"), ID: 3}); !m.OK {
		t.Fatalf("synced keys: %s", m.Error)
	}
	for _, spy := range []chan wire.Msg{spyA, spyB, spyC} {
		if m := readSpy(t, spy); m.Action != "write" || string(m.Keys) != "y" {
			t.Fatalf("synced keys = %+v, want the y frame", m)
		}
	}
	spyEmpty(t, spyD)
}
