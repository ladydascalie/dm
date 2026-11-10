package server

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/paths"
	"dm/internal/session"
	"dm/internal/wire"
)

// TestApplyDefaultFold proves an explicit default-fold node overrides saved
// window folds, while an absent node leaves them alone.
func TestApplyDefaultFold(t *testing.T) {
	st := session.NewState("fold")
	w := st.Workspaces[0].Windows[0]
	w.Unfolded = false

	cfg, err := keymap.ParseConfig(`default-fold "unfold"`)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	applyDefaultFold(st, cfg)
	if !w.Unfolded {
		t.Fatal("Unfolded = false after default-fold unfold, want true")
	}

	w.Unfolded = true
	absent, err := keymap.ParseConfig(`prefix "ctrl+b"`)
	if err != nil {
		t.Fatalf("ParseConfig absent: %v", err)
	}
	applyDefaultFold(st, absent)
	if !w.Unfolded {
		t.Fatal("Unfolded = false after absent node, want saved true")
	}
}

// TestNewWindowTakesDefaultFold proves a new window's Unfolded follows the
// config's default-fold setting. It starts a fake pane daemon so ensurePane
// adopts instead of spawning a real shell.
func TestNewWindowTakesDefaultFold(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"fold", `default-fold "fold"`, false},
		{"unfold", `default-fold "unfold"`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("DM_STATE", t.TempDir())
			cfg, err := keymap.ParseConfig(c.src)
			if err != nil {
				t.Fatalf("ParseConfig: %v", err)
			}
			st := session.NewState("fold")
			// Keep a live pane in w1 so closing the fake pane in w2 does not
			// end the session in the middle of the test.
			w1 := st.Workspaces[0].Windows[0]
			w1.Root = &layout.Node{Pane: "w1p1"}
			w1.Focus = "w1p1"
			st.Panes["w1p1"] = &session.PaneState{Cwd: "/tmp"}
			s := railServer(st)
			s.cfg = cfg

			const paneID = "w2p1"
			sock := paths.PaneSock(st.Name, paneID)
			if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
				t.Fatal(err)
			}
			ln, err := net.Listen("unix", sock)
			if err != nil {
				t.Fatal(err)
			}

			var mu sync.Mutex
			var conns []net.Conn
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					conn, err := ln.Accept()
					if err != nil {
						return
					}
					mu.Lock()
					conns = append(conns, conn)
					mu.Unlock()
					go func(c net.Conn) {
						defer func() { _ = c.Close() }()
						for {
							m, err := wire.ReadMsg(c)
							if err != nil {
								return
							}
							if m.Type == "req" {
								_ = wire.WriteMsg(c, wire.Msg{Type: "resp", OK: true, ID: m.ID})
							}
						}
					}(conn)
				}
			}()
			defer func() {
				_ = ln.Close()
				<-done
				mu.Lock()
				for _, c := range conns {
					_ = c.Close()
				}
				conns = nil
				mu.Unlock()
			}()

			wid, pid, err := s.newWindow("", "test", "/tmp", false)
			if err != nil {
				t.Fatalf("newWindow: %v", err)
			}
			if wid != "w2" || pid != paneID {
				t.Fatalf("newWindow ids = %q/%q, want w2/%s", wid, pid, paneID)
			}
			ws := st.Workspaces[0]
			if len(ws.Windows) != 2 {
				t.Fatalf("windows = %d, want 2", len(ws.Windows))
			}
			if got := ws.Windows[1].Unfolded; got != c.want {
				t.Fatalf("Unfolded = %v, want %v", got, c.want)
			}
		})
	}
}

func TestToggleWorkspaceFold(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	st := session.NewState("fold")
	s := railServer(st)

	ws := st.Workspaces[0]
	if !ws.Unfolded {
		t.Fatal("new workspace is folded, want unfolded")
	}

	// Toggle to folded
	unfolded, err := s.toggleFold(ws.ID)
	if err != nil {
		t.Fatalf("toggleFold(%q): %v", ws.ID, err)
	}
	if unfolded {
		t.Fatal("toggleFold returned unfolded=true, want false")
	}
	if ws.Unfolded {
		t.Fatal("workspace Unfolded = true, want false")
	}

	// Toggle back to unfolded
	unfolded, err = s.toggleFold(ws.ID)
	if err != nil {
		t.Fatalf("toggleFold(%q): %v", ws.ID, err)
	}
	if !unfolded {
		t.Fatal("toggleFold returned unfolded=false, want true")
	}
	if !ws.Unfolded {
		t.Fatal("workspace Unfolded = false, want true")
	}

	// A window ID still toggles the window's fold.
	w := ws.Windows[0]
	initial := w.Unfolded
	unfolded, err = s.toggleFold(w.ID)
	if err != nil {
		t.Fatalf("toggleFold(%q): %v", w.ID, err)
	}
	if unfolded != !initial {
		t.Fatalf("window Unfolded = %v, want %v", unfolded, !initial)
	}
}

// TestToggleProjectFold proves a project target flips the project's fold in
// the state and reaches the snapshot.
func TestToggleProjectFold(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	st := session.NewState("fold")
	st.Workspaces[0].Project = "dm"
	st.Workspaces[0].Windows[0].Root = &layout.Node{Pane: "w1p1"}
	st.Workspaces[0].Windows[0].Focus = "w1p1"
	st.Panes["w1p1"] = &session.PaneState{Cwd: "/tmp"}
	s := railServer(st)

	unfolded, err := s.toggleFold(wire.ProjectTargetPrefix + "dm")
	if err != nil {
		t.Fatalf("toggleFold(project dm): %v", err)
	}
	if unfolded {
		t.Fatal("toggleFold returned unfolded=true, want false")
	}
	if !st.ProjectFold["dm"] {
		t.Fatal("project dm is not folded in the state")
	}
	panes := s.allPanesLocked()
	if len(panes) == 0 || !panes[0].ProjectFolded {
		t.Fatalf("snapshot does not carry the folded project: %+v", panes)
	}

	unfolded, err = s.toggleFold(wire.ProjectTargetPrefix + "dm")
	if err != nil {
		t.Fatalf("toggleFold(project dm): %v", err)
	}
	if !unfolded {
		t.Fatal("toggleFold returned unfolded=false, want true")
	}
	if st.ProjectFold["dm"] {
		t.Fatal("project dm is still folded in the state")
	}
}
