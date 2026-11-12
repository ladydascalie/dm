package server

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dm/internal/session"
	"dm/internal/wire"
)

// historyPane answers every pane request OK and records the ring budget each
// set-history request carries.
func historyPane(t *testing.T, st *session.State, id string) <-chan int {
	t.Helper()
	sock := st.PaneSock(id)
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	ch := make(chan int, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				for {
					m, err := wire.ReadMsg(c)
					if err != nil {
						return
					}
					if m.Action == "set-history" {
						select {
						case ch <- m.History:
						default:
						}
					}
					_ = wire.WriteMsg(c, wire.Msg{Type: "resp", ID: m.ID, OK: true})
				}
			}(c)
		}
	}()
	return ch
}

// TestReloadConfigSendsHistoryToEveryPane proves a successful reload pushes
// the config's ring budget to every pane in the session and that a pane which
// does not answer does not fail it.
func TestReloadConfigSendsHistoryToEveryPane(t *testing.T) {
	f := newReloadFixture(t, "reloadhist")
	panes := map[string]<-chan int{
		"w1p1": historyPane(t, f.s.st, "w1p1"),
		"w1p2": historyPane(t, f.s.st, "w1p2"),
	}
	// A third pane with no socket stands in for a dead daemon: the reload must
	// log it and still succeed.
	f.s.mu.Lock()
	f.s.st.Panes["w1p3"] = &session.PaneState{Cwd: "/tmp"}
	f.s.mu.Unlock()

	src := "prefix \"ctrl+b\"\nhistory-lines 8000\n"
	if err := os.WriteFile(f.path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	r := f.reload(t)
	if !r.ok {
		t.Fatalf("reload with an unanswered pane failed: %q", r.msg)
	}

	const want = 8000 * 256
	for id, ch := range panes {
		select {
		case got := <-ch:
			if got != want {
				t.Fatalf("%s got history %d, want %d", id, got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s never received set-history", id)
		}
	}
}

// TestPaneCommandPassesHistory proves the pane daemon is born with the ring
// budget the config asks for.
func TestPaneCommandPassesHistory(t *testing.T) {
	t.Parallel()
	ps := &session.PaneState{Cwd: "/tmp", Shell: "/bin/sh", Cols: 80, Rows: 24}
	cmd := paneCommand("/usr/bin/dm", "s1", "w1p1", ps, nil, 1<<20)
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--history 1048576") {
		t.Fatalf("pane command = %q, want --history 1048576", args)
	}
}
