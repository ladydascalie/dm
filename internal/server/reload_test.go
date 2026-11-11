package server

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/session"
	"dm/internal/wire"
)

// reloadFixture is an in-process server with two panes and one client socket,
// plus a config file at a temp XDG path. No pane daemons are spawned: a missing
// pane socket makes the resync's screen and replay reads fail fast.
type reloadFixture struct {
	s    *server
	cl   *client
	cli  net.Conn
	path string
}

func newReloadFixture(t *testing.T, name string) *reloadFixture {
	t.Helper()
	// Serial: the config load reads XDG_CONFIG_HOME, which t.Setenv cannot
	// parallelise. The state itself is explicit, no DM_STATE needed.
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	path := filepath.Join(xdg, "dm", "config.kdl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	writeReloadConfig(t, path, 0)

	st := session.NewStateAt(t.TempDir(), name)
	w := st.ActiveWindow()
	w.Root = &layout.Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*layout.Node{{Pane: "w1p1"}, {Pane: "w1p2"}},
	}
	w.Focus = "w1p1"
	for _, id := range layout.AllPanes(w.Root) {
		st.Panes[id] = &session.PaneState{Cwd: "/tmp"}
	}

	s := railServer(st)
	s.cfg = keymap.LoadDefault()
	// Record the current size, so the first reload is the only width change.
	s.mu.Lock()
	for id, r := range s.paneRectsLocked() {
		if ps := s.st.Panes[id]; ps != nil {
			ps.Cols, ps.Rows = r.W, r.H
		}
	}
	s.mu.Unlock()

	srv, cli := net.Pipe()
	cl := newClient(srv)
	s.mu.Lock()
	s.clients[cl] = true
	s.mu.Unlock()
	t.Cleanup(func() {
		cl.shutdown()
		_ = srv.Close()
		_ = cli.Close()
		s.stopReflow()
	})
	return &reloadFixture{s: s, cl: cl, cli: cli, path: path}
}

func writeReloadConfig(t *testing.T, path string, pad int) {
	t.Helper()
	src := fmt.Sprintf("prefix \"ctrl+b\"\npadding %d\n", pad)
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
}

// reload drives one reload request and returns its reply plus the snapshot the
// resync broadcast, if one went out.
type reloadResult struct {
	ok   bool
	msg  string
	pad  int
	snap *wire.Snapshot
}

func (f *reloadFixture) reload(t *testing.T) reloadResult {
	t.Helper()
	f.s.handleReq(f.cl, wire.Msg{Type: "req", Action: "reload"})
	var out reloadResult
	deadline := time.Now().Add(2 * time.Second)
	for {
		_ = f.cli.SetReadDeadline(deadline)
		m, err := wire.ReadMsg(f.cli)
		if err != nil {
			t.Fatalf("reload reply: %v", err)
		}
		if m.Type == "snapshot" {
			out.snap = m.Snap
		}
		if m.Type == "resp" && m.Action == "reload" {
			out.ok, out.msg = m.OK, m.Error
			var res struct {
				Pad int `json:"pad"`
			}
			if len(m.Res) > 0 {
				if err := json.Unmarshal(m.Res, &res); err != nil {
					t.Fatalf("reload reply body: %v", err)
				}
			}
			out.pad = res.Pad
			return out
		}
	}
}

func (f *reloadFixture) rects() map[string]layout.Rect {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	return f.s.paneRectsLocked()
}

func (f *reloadFixture) snap() *wire.Snapshot {
	return snapshot(f.s)
}

// TestReloadConfigChangesPadding proves a reload re-reads the file, resyncs
// every pane rect and publishes the new padding in the snapshot and the reply.
func TestReloadConfigChangesPadding(t *testing.T) {
	f := newReloadFixture(t, "reloadpad")
	before := f.rects()
	if len(before) != 2 {
		t.Fatalf("fixture has %d panes, want 2", len(before))
	}

	writeReloadConfig(t, f.path, 2)
	r := f.reload(t)
	if !r.ok || r.msg != "" {
		t.Fatalf("reload failed: ok=%v error=%q", r.ok, r.msg)
	}
	if r.pad != 2 {
		t.Fatalf("reply pad = %d, want 2", r.pad)
	}
	if r.snap == nil || r.snap.Pad != 2 {
		t.Fatalf("broadcast snapshot pad = %+v, want 2", r.snap)
	}
	if got := f.snap().Pad; got != 2 {
		t.Fatalf("snapshotPad = %d, want 2", got)
	}
	for id, old := range before {
		got := f.rects()[id]
		if got == old {
			t.Fatalf("%s rect did not move: %+v", id, got)
		}
		if got.X <= old.X || got.Y <= old.Y || got.W >= old.W || got.H >= old.H {
			t.Fatalf("%s rect = %+v, want every edge pulled in from %+v", id, got, old)
		}
	}
}

// TestReloadConfigBadFileChangesNothing proves a malformed config leaves the
// rects and the published padding alone and carries the error back.
func TestReloadConfigBadFileChangesNothing(t *testing.T) {
	f := newReloadFixture(t, "reloadbad")
	writeReloadConfig(t, f.path, 2)
	if r := f.reload(t); !r.ok {
		t.Fatalf("first reload failed: %q", r.msg)
	}
	good := f.rects()

	if err := os.WriteFile(f.path, []byte("padding nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := f.reload(t)
	if r.ok || r.msg == "" {
		t.Fatalf("malformed reload: ok=%v error=%q, want an error", r.ok, r.msg)
	}
	if got := f.snap().Pad; got != 2 {
		t.Fatalf("snapshot pad after a bad read = %d, want 2", got)
	}
	for id, want := range good {
		if got := f.rects()[id]; got != want {
			t.Fatalf("%s rect after a bad read = %+v, want %+v", id, got, want)
		}
	}
}

// TestReloadConfigRestoresPadding proves a second reload that puts the old
// padding back restores every pane rect.
func TestReloadConfigRestoresPadding(t *testing.T) {
	f := newReloadFixture(t, "reloadrestore")
	writeReloadConfig(t, f.path, 0)
	before := f.rects()

	writeReloadConfig(t, f.path, 2)
	if r := f.reload(t); !r.ok || r.pad != 2 {
		t.Fatalf("grow reload: ok=%v pad=%d error=%q", r.ok, r.pad, r.msg)
	}
	writeReloadConfig(t, f.path, 0)
	r := f.reload(t)
	if !r.ok || r.pad != 0 {
		t.Fatalf("restore reload: ok=%v pad=%d error=%q", r.ok, r.pad, r.msg)
	}
	if r.snap == nil || r.snap.Pad != 0 {
		t.Fatalf("restored snapshot pad = %+v, want 0", r.snap)
	}
	for id, want := range before {
		if got := f.rects()[id]; got != want {
			t.Fatalf("%s rect after restore = %+v, want %+v", id, got, want)
		}
	}
}
