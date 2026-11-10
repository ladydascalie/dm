package server

import (
	"net"
	"strings"
	"testing"
	"time"

	"dm/internal/layout"
	"dm/internal/session"
	"dm/internal/wire"
)

// keysServer is a server with two panes side by side in one window, the first
// in focus. Each pane's socket is one end of a pipe, so the test reads the keys
// the server sends to a pane.
func keysServer(t *testing.T) (*server, string, string, map[string]chan string) {
	t.Helper()
	t.Setenv("DM_STATE", t.TempDir())
	st := session.NewState("keystarget")
	w := st.ActiveWindow()
	a := w.NewPaneID()
	b := w.NewPaneID()
	w.Root = &layout.Node{Dir: "row", Kids: []*layout.Node{{Pane: a}, {Pane: b}}}
	w.Focus = a
	st.Panes[a] = &session.PaneState{}
	st.Panes[b] = &session.PaneState{}
	s := railServer(st)
	got := map[string]chan string{}
	for _, id := range []string{a, b} {
		srv, cli := net.Pipe()
		s.mu.Lock()
		s.panes[id] = &paneConn{id: id, c: srv, st: wire.Status{State: "unknown"}}
		s.mu.Unlock()
		ch := make(chan string, 8)
		got[id] = ch
		go func(c net.Conn, id string) {
			for {
				m, err := wire.ReadMsg(c)
				if err != nil {
					return
				}
				if m.Action == "write" {
					ch <- string(m.Keys)
				}
			}
		}(cli, id)
		t.Cleanup(func() {
			_ = srv.Close()
			_ = cli.Close()
		})
	}
	return s, a, b, got
}

// TestKeysHonourTarget: a keys message goes to the pane it names, not to the
// focus of the active window. An empty target falls back to the focus, and an
// unknown target replies with an error and writes nothing.
func TestKeysHonourTarget(t *testing.T) {
	s, focus, other, got := keysServer(t)
	srv, cli := net.Pipe()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = cli.Close()
	})
	go s.serve(srv)

	// A named target takes the keys; the focus gets none of them.
	if err := wire.WriteMsg(cli, wire.Msg{Type: "keys", Target: other, Keys: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	select {
	case keys := <-got[other]:
		if keys != "x" {
			t.Fatalf("target pane received %q, want %q", keys, "x")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the named pane received nothing")
	}
	select {
	case keys := <-got[focus]:
		t.Fatalf("the focus pane received %q, want nothing", keys)
	case <-time.After(100 * time.Millisecond):
	}

	// An empty target is the focus.
	if err := wire.WriteMsg(cli, wire.Msg{Type: "keys", Keys: []byte("y")}); err != nil {
		t.Fatal(err)
	}
	select {
	case keys := <-got[focus]:
		if keys != "y" {
			t.Fatalf("focus pane received %q, want %q", keys, "y")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the focus pane received nothing")
	}
	select {
	case keys := <-got[other]:
		t.Fatalf("the named pane received %q after an empty target", keys)
	case <-time.After(100 * time.Millisecond):
	}

	// An unknown target keeps the error and writes nowhere.
	if err := wire.WriteMsg(cli, wire.Msg{Type: "keys", Target: "ghost", Keys: []byte("z")}); err != nil {
		t.Fatal(err)
	}
	_ = cli.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := wire.ReadMsg(cli)
	if err != nil {
		t.Fatalf("no error reply for an unknown pane: %v", err)
	}
	if resp.Type != "resp" || resp.OK || !strings.Contains(resp.Error, `no pane "ghost"`) {
		t.Fatalf("reply = %+v, want an error for no pane \"ghost\"", resp)
	}
	for _, id := range []string{focus, other} {
		select {
		case keys := <-got[id]:
			t.Fatalf("pane %s received %q after an unknown target", id, keys)
		case <-time.After(100 * time.Millisecond):
		}
	}
}
