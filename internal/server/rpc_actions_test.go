package server

import (
	"encoding/json"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dm/internal/layout"
	"dm/internal/session"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// rpcServer is the queue fixture plus a client whose replies land on a pipe
// the test reads. The panes are not adopted: a test wires the conns it needs,
// so a request path that adopts through the socket stays reachable.
func rpcServer(t *testing.T, name string) (*server, *client, net.Conn, string, string) {
	t.Helper()
	s, a, b := queueServer(t, name)
	cl, conn := boxConn(t)
	return s, cl, conn, a, b
}

// rpcCall sends one request through handleReq and returns its reply.
func rpcCall(t *testing.T, s *server, cl *client, conn net.Conn, m wire.Msg) wire.Msg {
	t.Helper()
	s.handleReq(cl, m)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, err := wire.ReadMsg(conn)
	if err != nil {
		t.Fatalf("%s reply: %v", m.Action, err)
	}
	return got
}

// wantErr fails the test when a reply should have failed and returns why.
func wantErr(t *testing.T, action, wantIn string, m wire.Msg) {
	t.Helper()
	if m.OK {
		t.Fatalf("%s = OK, want an error naming %q", action, wantIn)
	}
	if wantIn != "" && !strings.Contains(m.Error, wantIn) {
		t.Fatalf("%s error = %q, want it to name %q", action, m.Error, wantIn)
	}
}

// spyPane registers a pane conn whose socket the test can read, so an action
// asserts what the pane actually received.
func spyPane(t *testing.T, s *server, id string) chan wire.Msg {
	t.Helper()
	srv, cli := net.Pipe()
	ch := make(chan wire.Msg, 16)
	s.mu.Lock()
	s.panes[id] = &paneConn{id: id, c: srv}
	s.mu.Unlock()
	go func() {
		for {
			m, err := wire.ReadMsg(cli)
			if err != nil {
				return
			}
			ch <- m
		}
	}()
	t.Cleanup(func() { _ = srv.Close(); _ = cli.Close() })
	return ch
}

// readSpy takes one message a pane received, or fails on the deadline.
func readSpy(t *testing.T, ch chan wire.Msg) wire.Msg {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("the pane received no message")
		return wire.Msg{}
	}
}

// readSpyUntil takes the next message of one action a pane received, dropping
// frames the test does not care about. A failed mutation still resyncs, so a
// resize can arrive before the frame the test waits for.
func readSpyUntil(t *testing.T, ch chan wire.Msg, action string) wire.Msg {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case m := <-ch:
			if m.Action == action {
				return m
			}
		case <-deadline:
			t.Fatalf("the pane never received %q", action)
			return wire.Msg{}
		}
	}
}

// spyEmpty proves a pane received nothing.
func spyEmpty(t *testing.T, ch chan wire.Msg) {
	t.Helper()
	select {
	case m := <-ch:
		t.Fatalf("the pane received %+v, want nothing", m)
	case <-time.After(200 * time.Millisecond):
	}
}

// talkPane answers one action on the pane socket with a fixed reply, the way a
// real pane daemon answers a read or a cwd request.
func talkPane(t *testing.T, sess *session.State, id string, reply wire.Msg) {
	t.Helper()
	sock := sess.PaneSock(id)
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
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
					out := reply
					out.ID = m.ID
					_ = wire.WriteMsg(c, out)
				}
			}(c)
		}
	}()
}

// TestSplitActionErrors pins the split guard rails: a direction the layout
// cannot take and a target that names no pane both fail without touching the
// tree.
func TestSplitActionErrors(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, _ := rpcServer(t, "rpcsplit")

	wantErr(t, "split", "dir must be", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "split", Target: a, Dir: "sideways", ID: 1}))
	wantErr(t, "split", "no pane", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "split", Target: "ghost", Dir: "right", ID: 2}))

	s.mu.Lock()
	before := layout.AllPanes(s.st.ActiveWindow().Root)
	focus := s.st.ActiveWindow().Focus
	s.mu.Unlock()
	if len(before) != 2 {
		t.Fatalf("a refused split changed the tree: %v", before)
	}
	if focus != a {
		t.Fatalf("focus moved to %q on a refused split", focus)
	}
}

// TestSplitActionSplitsAndFocuses pins the happy path: the split adds a pane
// beside the target, the new pane takes the focus, and a no-focus split leaves
// the focus alone.
func TestSplitActionSplitsAndFocuses(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, b := rpcServer(t, "rpcsplitwin")
	screenPane(t, s.st, "w1p3")
	screenPane(t, s.st, "w1p4")

	m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "split", Target: a, Dir: "right", ID: 1})
	if !m.OK {
		t.Fatalf("split: %s", m.Error)
	}
	var rep struct {
		Pane string `json:"pane"`
	}
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("decode split reply: %v", err)
	}
	if rep.Pane != "w1p3" {
		t.Fatalf("split pane = %q, want w1p3", rep.Pane)
	}
	s.mu.Lock()
	win := s.st.ActiveWindow()
	ids := layout.AllPanes(win.Root)
	focus := win.Focus
	s.mu.Unlock()
	if len(ids) != 3 || focus != "w1p3" {
		t.Fatalf("after split panes = %v focus = %q, want three panes focused on the new one", ids, focus)
	}

	m = rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "split", Target: b, Dir: "down", NoFocus: true, ID: 2})
	if !m.OK {
		t.Fatalf("no-focus split: %s", m.Error)
	}
	s.mu.Lock()
	focus = s.st.ActiveWindow().Focus
	s.mu.Unlock()
	if focus != "w1p3" {
		t.Fatalf("a no-focus split moved the focus to %q", focus)
	}
}

// TestSplitActionEmptyTargetUsesFocus pins the default target: a split with no
// target opens beside the focused pane.
func TestSplitActionEmptyTargetUsesFocus(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _ := rpcServer(t, "rpcsplitfocus")
	screenPane(t, s.st, "w1p3")

	m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "split", Dir: "down", ID: 1})
	if !m.OK {
		t.Fatalf("split with no target: %s", m.Error)
	}
	s.mu.Lock()
	win := s.st.ActiveWindow()
	ids := layout.AllPanes(win.Root)
	focus := win.Focus
	s.mu.Unlock()
	if len(ids) != 3 || focus != "w1p3" {
		t.Fatalf("panes = %v focus = %q, want the split beside the focus", ids, focus)
	}
}

// TestResizeSplitAction pins the ratio guards and the win: a bad ratio is
// refused, two panes under one split get the asked weights, a zero ratio
// clamps so both panes keep their floor, and panes without a common split are
// refused.
func TestResizeSplitAction(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, b := rpcServer(t, "rpcresize")

	wantErr(t, "resize-split", "bad ratio", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "resize-split", Target: a, Other: b, Ratio: math.NaN(), ID: 1}))
	wantErr(t, "resize-split", "two panes", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "resize-split", Target: a, Other: a, Ratio: 0.5, ID: 2}))
	wantErr(t, "resize-split", "no pane", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "resize-split", Target: "ghost", Other: b, Ratio: 0.5, ID: 3}))

	m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "resize-split", Target: a, Other: b, Ratio: 0.75, ID: 4})
	if !m.OK {
		t.Fatalf("resize-split: %s", m.Error)
	}
	s.mu.Lock()
	sizes := s.st.ActiveWindow().Root.Sizes
	pad := s.cfg.Padding()
	s.mu.Unlock()
	if len(sizes) != 2 || sizes[0] < 0.74 || sizes[0] > 0.76 || sizes[0]+sizes[1] > 1.01 {
		t.Fatalf("sizes = %v, want about 0.75 and 0.25", sizes)
	}

	// A ratio of zero clamps to the floor: both panes keep at least the
	// smallest draggable box.
	m = rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "resize-split", Target: a, Other: b, Ratio: 0, ID: 5})
	if !m.OK {
		t.Fatalf("resize-split at the floor: %s", m.Error)
	}
	s.mu.Lock()
	sizes = s.st.ActiveWindow().Root.Sizes
	s.mu.Unlock()
	minBox := float64(5 + 2*pad)
	area := layout.PaneAreaForWithTabBar(s.cols, s.rows, s.railLocked())
	if minBox/sizes[1] > float64(area.W) {
		t.Fatalf("sizes = %v, the second pane lost its floor at width %d", sizes, area.W)
	}

	// Panes in different windows share no split.
	s.mu.Lock()
	s.st.Workspaces[0].Windows = append(s.st.Workspaces[0].Windows,
		&session.Window{ID: "w2", Name: "two", Root: &layout.Node{Pane: "w2p1"}})
	s.st.Panes["w2p1"] = &session.PaneState{}
	s.mu.Unlock()
	wantErr(t, "resize-split", "no pane", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "resize-split", Target: a, Other: "w2p1", Ratio: 0.5, ID: 6}))
}

// TestFocusActions pins focus movement: focus names a pane anywhere, a wrong
// name fails, focus-dir walks to the neighbour and refuses when there is none,
// and next-pane and prev-pane wrap around the window.
func TestFocusActions(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, b := rpcServer(t, "rpcfocus")

	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "focus", Target: b, ID: 1}); !m.OK {
		t.Fatalf("focus: %s", m.Error)
	}
	s.mu.Lock()
	focus := s.st.ActiveWindow().Focus
	s.mu.Unlock()
	if focus != b {
		t.Fatalf("focus = %q, want %q", focus, b)
	}
	wantErr(t, "focus", "no pane", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "focus", Target: "ghost", ID: 2}))

	// The left neighbour of the left pane does not exist.
	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "focus", Target: a, ID: 3}); !m.OK {
		t.Fatalf("focus back: %s", m.Error)
	}
	wantErr(t, "focus-dir", "no pane left of", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "focus-dir", Dir: "left", ID: 4}))
	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "focus-dir", Dir: "right", ID: 5}); !m.OK {
		t.Fatalf("focus-dir right: %s", m.Error)
	}
	s.mu.Lock()
	focus = s.st.ActiveWindow().Focus
	s.mu.Unlock()
	if focus != b {
		t.Fatalf("focus-dir right landed on %q, want %q", focus, b)
	}

	// next and prev wrap around the two panes.
	for _, step := range []struct {
		action string
		want   string
	}{{"next-pane", a}, {"prev-pane", b}, {"prev-pane", a}} {
		if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: step.action, ID: 6}); !m.OK {
			t.Fatalf("%s: %s", step.action, m.Error)
		}
		s.mu.Lock()
		focus = s.st.ActiveWindow().Focus
		s.mu.Unlock()
		if focus != step.want {
			t.Fatalf("%s left the focus on %q, want %q", step.action, focus, step.want)
		}
	}
}

// TestClosePaneAction pins the two close paths: one pane of many leaves the
// tree and the session with a kill on its socket, the last pane of a window is
// killed but stays until its shell exits, and an unknown target fails.
func TestClosePaneAction(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, b := rpcServer(t, "rpcclosepane")
	spyB := spyPane(t, s, b)
	spyA := spyPane(t, s, a)

	wantErr(t, "close-pane", "no pane", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "close-pane", Target: "ghost", ID: 1}))

	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "close-pane", Target: b, ID: 2}); !m.OK {
		t.Fatalf("close-pane: %s", m.Error)
	}
	if got := readSpyUntil(t, spyB, "kill"); got.Action != "kill" {
		t.Fatalf("the closed pane got %+v, want a kill", got)
	}
	s.mu.Lock()
	win := s.st.ActiveWindow()
	ids := layout.AllPanes(win.Root)
	focus := win.Focus
	_, hadConn := s.panes[b]
	_, hadState := s.st.Panes[b]
	s.mu.Unlock()
	if len(ids) != 1 || ids[0] != a || focus != a {
		t.Fatalf("after close panes = %v focus = %q, want only %q focused", ids, focus, a)
	}
	if hadConn || hadState {
		t.Fatal("the closed pane survived in the session")
	}

	// The last pane cannot leave on its own: the kill asks the shell to exit
	// and the exit drives the normal teardown.
	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "close-pane", Target: a, ID: 3}); !m.OK {
		t.Fatalf("close-pane of the last pane: %s", m.Error)
	}
	if got := readSpyUntil(t, spyA, "kill"); got.Action != "kill" {
		t.Fatalf("the last pane got %+v, want a kill", got)
	}
	s.mu.Lock()
	ids = layout.AllPanes(s.st.ActiveWindow().Root)
	s.mu.Unlock()
	if len(ids) != 1 || ids[0] != a {
		t.Fatalf("the last pane left the tree: %v", ids)
	}
}

// TestClosePaneLastPaneUnknownFails pins the guard on an unadopted last pane:
// there is nothing to kill, so the request fails instead of pretending.
func TestClosePaneLastPaneUnknownFails(t *testing.T) {
	t.Parallel()
	st := session.NewStateAt(t.TempDir(), "rpccloselast")
	id := testutil.RootPane(st)
	st.Workspaces[0].Windows[0].Root = &layout.Node{Pane: id}
	s := railServer(st)
	cl, conn := boxConn(t)

	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "close-pane", Target: id, ID: 1})
	wantErr(t, "close-pane", "no pane", m)
}

// TestWindowActions pins window focusing, cycling and selection, including the
// stale-focus repair: focusing a window whose saved focus names no pane puts
// the focus on a real pane.
func TestWindowActions(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _ := rpcServer(t, "rpcwindows")
	s.mu.Lock()
	s.st.Workspaces[0].Windows = append(s.st.Workspaces[0].Windows,
		&session.Window{ID: "w2", Name: "two", Root: &layout.Node{Pane: "w2p1"}, Focus: "gone"})
	s.st.Panes["w2p1"] = &session.PaneState{}
	s.mu.Unlock()

	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "focus-window", Target: "w2", ID: 1}); !m.OK {
		t.Fatalf("focus-window: %s", m.Error)
	}
	s.mu.Lock()
	ws := s.st.Workspaces[0]
	focus := ws.Windows[1].Focus
	active := ws.Active
	s.mu.Unlock()
	if active != 1 || focus != "w2p1" {
		t.Fatalf("focus-window left active %d focus %q, want window two focused on w2p1", active, focus)
	}
	wantErr(t, "focus-window", "no window", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "focus-window", Target: "ghost", ID: 2}))

	// next-window wraps on the last window; prev-window comes back.
	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "next-window", ID: 3}); !m.OK {
		t.Fatalf("next-window: %s", m.Error)
	}
	s.mu.Lock()
	active = s.st.Workspaces[0].Active
	s.mu.Unlock()
	if active != 0 {
		t.Fatalf("next-window past the last window = %d, want the wrap to 0", active)
	}
	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "prev-window", ID: 4}); !m.OK {
		t.Fatalf("prev-window: %s", m.Error)
	}
	s.mu.Lock()
	active = s.st.Workspaces[0].Active
	s.mu.Unlock()
	if active != 1 {
		t.Fatalf("prev-window = %d, want 1", active)
	}

	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "select-window", Index: 0, ID: 5}); !m.OK {
		t.Fatalf("select-window: %s", m.Error)
	}
	s.mu.Lock()
	active = s.st.Workspaces[0].Active
	s.mu.Unlock()
	if active != 0 {
		t.Fatalf("select-window 0 = %d", active)
	}
	wantErr(t, "select-window", "does not exist", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "select-window", Index: 5, ID: 6}))
}

// TestRenameWindowAction pins the rename: the name and the named flag change,
// nothing else, and an empty name or an unknown window fails.
func TestRenameWindowAction(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _ := rpcServer(t, "rpcrename")

	wantErr(t, "rename-window", "name required", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "rename-window", Target: "w1", ID: 1}))
	wantErr(t, "rename-window", "no window", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "rename-window", Target: "ghost", Name: "x", ID: 2}))

	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "rename-window", Target: "w1", Name: "big", ID: 3}); !m.OK {
		t.Fatalf("rename-window: %s", m.Error)
	}
	s.mu.Lock()
	w := s.st.Workspaces[0].Windows[0]
	name, named := w.Name, w.Named
	s.mu.Unlock()
	if name != "big" || !named {
		t.Fatalf("window name = %q named = %v, want big true", name, named)
	}
}

// TestNewWindowAction pins window creation: the window lands in the asked
// workspace with the asked name, the pane adopts through its socket, and a
// no-focus window leaves the active window alone.
func TestNewWindowAction(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _ := rpcServer(t, "rpcnewwin")
	screenPane(t, s.st, "w2p1")
	screenPane(t, s.st, "w3p1")

	m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "new-window", Name: "test", Cwd: "/tmp", ID: 1})
	if !m.OK {
		t.Fatalf("new-window: %s", m.Error)
	}
	var rep struct {
		Window string `json:"window"`
		Pane   string `json:"pane"`
	}
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("decode new-window reply: %v", err)
	}
	if rep.Window != "w2" || rep.Pane != "w2p1" {
		t.Fatalf("new-window ids = %q/%q, want w2/w2p1", rep.Window, rep.Pane)
	}
	s.mu.Lock()
	ws := s.st.Workspaces[0]
	active, name := ws.Active, ws.Windows[1].Name
	_, adopted := s.panes["w2p1"]
	s.mu.Unlock()
	if active != 1 || name != "test" {
		t.Fatalf("new window active = %d name = %q, want 1 and test", active, name)
	}
	if !adopted {
		t.Fatal("the new pane never adopted its daemon")
	}

	// A pane target opens the window in the pane's workspace and inherits its
	// folder when the request carries none.
	s.mu.Lock()
	s.st.Panes["w1p1"].Cwd = "/tmp"
	s.mu.Unlock()
	m = rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "new-window", Target: "w1p1", NoFocus: true, ID: 2})
	if !m.OK {
		t.Fatalf("new-window from a pane: %s", m.Error)
	}
	s.mu.Lock()
	ws = s.st.Workspaces[0]
	name = ws.Windows[2].Name
	active = ws.Active
	s.mu.Unlock()
	if name != "tmp" || active != 1 {
		t.Fatalf("pane-target window name = %q active = %d, want tmp and 1", name, active)
	}

	wantErr(t, "new-window", "no workspace or pane", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "new-window", Target: "ghost", ID: 3}))
}

// TestCloseWindowAction pins the closes: a window among many leaves with its
// panes, the last window of the last workspace is refused, and an unknown
// window fails.
func TestCloseWindowAction(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _ := rpcServer(t, "rpcclosewin")
	s.mu.Lock()
	s.st.Workspaces[0].Windows = append(s.st.Workspaces[0].Windows,
		&session.Window{ID: "w2", Name: "two", Root: &layout.Node{Pane: "w2p1"}})
	s.st.Panes["w2p1"] = &session.PaneState{}
	s.mu.Unlock()
	spyW2 := spyPane(t, s, "w2p1")

	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "close-window", Target: "w2", ID: 1}); !m.OK {
		t.Fatalf("close-window: %s", m.Error)
	}
	if got := readSpyUntil(t, spyW2, "kill"); got.Action != "kill" {
		t.Fatalf("the closed window's pane got %+v, want a kill", got)
	}
	s.mu.Lock()
	ws := s.st.Workspaces[0]
	windows, active := len(ws.Windows), ws.Active
	_, hadState := s.st.Panes["w2p1"]
	s.mu.Unlock()
	if windows != 1 || active != 0 || hadState {
		t.Fatalf("after close windows = %d active = %d pane kept = %v", windows, active, hadState)
	}

	wantErr(t, "close-window", "cannot close the last window", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "close-window", Target: "w1", ID: 2}))
	wantErr(t, "close-window", "no window", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "close-window", Target: "ghost", ID: 3}))
}

// TestCloseWindowTakesWorkspace pins the rule that a window alone in its
// workspace takes the workspace with it, and the active index lands on a real
// workspace.
func TestCloseWindowTakesWorkspace(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _ := rpcServer(t, "rpcclosewswin")
	s.mu.Lock()
	s.st.Workspaces = append(s.st.Workspaces, &session.Workspace{
		ID: "s2", Name: "work", Unfolded: true,
		Windows: []*session.Window{{ID: "w2", Name: "two", Root: &layout.Node{Pane: "w2p1"}}},
	})
	s.st.Panes["w2p1"] = &session.PaneState{}
	s.mu.Unlock()

	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "close-window", Target: "w2", ID: 1}); !m.OK {
		t.Fatalf("close-window: %s", m.Error)
	}
	s.mu.Lock()
	n := len(s.st.Workspaces)
	active := s.st.Active
	s.mu.Unlock()
	if n != 1 || active != 0 {
		t.Fatalf("workspaces = %d active = %d, want the lone workspace gone and s1 active", n, active)
	}
}

// TestWorkspaceActions pins workspace creation, cycling, focusing, renaming,
// closing and listing.
func TestWorkspaceActions(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _ := rpcServer(t, "rpcws")
	screenPane(t, s.st, "w2p1")

	m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "new-workspace", Cwd: "/tmp", ID: 1})
	if !m.OK {
		t.Fatalf("new-workspace: %s", m.Error)
	}
	var rep struct {
		Workspace string `json:"workspace"`
		Window    string `json:"window"`
		Pane      string `json:"pane"`
	}
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("decode new-workspace reply: %v", err)
	}
	if rep.Workspace != "s2" || rep.Window != "w2" || rep.Pane != "w2p1" {
		t.Fatalf("new-workspace ids = %s/%s/%s, want s2/w2/w2p1", rep.Workspace, rep.Window, rep.Pane)
	}
	s.mu.Lock()
	active := s.st.Active
	wsName := s.st.Workspaces[1].Name
	winName := s.st.Workspaces[1].Windows[0].Name
	s.mu.Unlock()
	if active != 1 || wsName != "ws2" || winName != "tmp" {
		t.Fatalf("new workspace active = %d name = %q window = %q, want 1, ws2, tmp", active, wsName, winName)
	}

	// Cycling wraps in both directions.
	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "next-workspace", ID: 2}); !m.OK {
		t.Fatalf("next-workspace: %s", m.Error)
	}
	s.mu.Lock()
	active = s.st.Active
	s.mu.Unlock()
	if active != 0 {
		t.Fatalf("next-workspace = %d, want the wrap to 0", active)
	}
	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "prev-workspace", ID: 3}); !m.OK {
		t.Fatalf("prev-workspace: %s", m.Error)
	}
	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "prev-workspace", ID: 4}); !m.OK {
		t.Fatalf("prev-workspace again: %s", m.Error)
	}
	s.mu.Lock()
	active = s.st.Active
	s.mu.Unlock()
	if active != 0 {
		t.Fatalf("two prev-workspace steps from 1 = %d, want the wrap to 0", active)
	}

	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "focus-workspace", Target: "s2", ID: 5}); !m.OK {
		t.Fatalf("focus-workspace: %s", m.Error)
	}
	wantErr(t, "focus-workspace", "no workspace", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "focus-workspace", Target: "ghost", ID: 6}))

	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "rename-workspace", Target: "s2", Name: "ops", ID: 7}); !m.OK {
		t.Fatalf("rename-workspace: %s", m.Error)
	}
	s.mu.Lock()
	wsName = s.st.Workspaces[1].Name
	s.mu.Unlock()
	if wsName != "ops" {
		t.Fatalf("renamed workspace = %q, want ops", wsName)
	}
	wantErr(t, "rename-workspace", "name required", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "rename-workspace", Target: "s2", ID: 8}))
	wantErr(t, "rename-workspace", "no workspace", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "rename-workspace", Target: "ghost", Name: "x", ID: 9}))

	m = rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "list-workspaces", ID: 10})
	if !m.OK {
		t.Fatalf("list-workspaces: %s", m.Error)
	}
	var list []wsInfo
	if err := json.Unmarshal(m.Res, &list); err != nil {
		t.Fatalf("decode list-workspaces: %v", err)
	}
	if len(list) != 2 || list[0].ID != "s1" || list[0].Active || !list[1].Active {
		t.Fatalf("workspace list = %+v, want s1 idle and s2 active", list)
	}

	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "close-workspace", Target: "s2", ID: 11}); !m.OK {
		t.Fatalf("close-workspace: %s", m.Error)
	}
	s.mu.Lock()
	n := len(s.st.Workspaces)
	active = s.st.Active
	s.mu.Unlock()
	if n != 1 || active != 0 {
		t.Fatalf("after close workspaces = %d active = %d, want s1 only", n, active)
	}
	wantErr(t, "close-workspace", "cannot close the last workspace", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "close-workspace", Target: "s1", ID: 12}))
	wantErr(t, "close-workspace", "no workspace", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "close-workspace", Target: "ghost", ID: 13}))
}

// TestSetProjectAction pins the project tag: the workspace carries the name
// and moves to the end of its project block, an empty name clears the tag, and
// an unknown workspace fails.
func TestSetProjectAction(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _ := rpcServer(t, "rpcproject")
	s.mu.Lock()
	s.st.Workspaces = append(s.st.Workspaces, &session.Workspace{
		ID: "s2", Name: "work", Unfolded: true,
		Windows: []*session.Window{{ID: "w2", Name: "two", Root: &layout.Node{Pane: "w2p1"}}},
	})
	s.st.Panes["w2p1"] = &session.PaneState{}
	s.mu.Unlock()

	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "set-project", Target: "s1", Name: "dm", ID: 1}); !m.OK {
		t.Fatalf("set-project: %s", m.Error)
	}
	s.mu.Lock()
	order := []string{}
	for _, ws := range s.st.Workspaces {
		order = append(order, ws.ID+":"+ws.Project)
	}
	activeID := s.st.ActiveWorkspace().ID
	s.mu.Unlock()
	if len(order) != 2 || order[1] != "s1:dm" || activeID != "s1" {
		t.Fatalf("after set-project order = %v active = %s, want s1 tagged, moved last and still active", order, activeID)
	}

	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "set-project", Target: "s1", ID: 2}); !m.OK {
		t.Fatalf("clear project: %s", m.Error)
	}
	s.mu.Lock()
	project := s.st.Workspaces[1].Project
	s.mu.Unlock()
	if project != "" {
		t.Fatalf("project = %q, want it cleared", project)
	}
	wantErr(t, "set-project", "no workspace", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "set-project", Target: "ghost", Name: "dm", ID: 3}))
}

// TestPaneQueries pins the read-only pane answers: list-panes reports the
// whole fleet, pane locates one anywhere, status answers one or all, and every
// miss fails with the name.
func TestPaneQueries(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, _ := rpcServer(t, "rpcquery")
	s.mu.Lock()
	s.st.Panes[a].Name = "alpha"
	s.panes[a] = &paneConn{id: a, st: wire.Status{State: "working", Message: "on it"}}
	s.st.Workspaces[0].Windows = append(s.st.Workspaces[0].Windows,
		&session.Window{ID: "w2", Name: "two", Root: &layout.Node{Pane: "w2p1"}})
	s.st.Panes["w2p1"] = &session.PaneState{App: "pi"}
	s.mu.Unlock()

	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "list-panes", ID: 1})
	if !m.OK {
		t.Fatalf("list-panes: %s", m.Error)
	}
	var list struct {
		Panes []paneListInfo `json:"panes"`
	}
	if err := json.Unmarshal(m.Res, &list); err != nil {
		t.Fatalf("decode list-panes: %v", err)
	}
	if len(list.Panes) != 3 {
		t.Fatalf("list-panes = %d rows, want one per pane across windows", len(list.Panes))
	}
	first := list.Panes[0]
	if first.ID != a || first.Name != "alpha" || first.Status != "working" ||
		first.Message != "on it" || first.Workspace != "s1" || first.Window != "w1" {
		t.Fatalf("first row = %+v", first)
	}
	last := list.Panes[2]
	if last.ID != "w2p1" || last.App != "pi" || last.WorkspaceName != "main" {
		t.Fatalf("background window row = %+v", last)
	}

	m = rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "pane", Target: "w2p1", ID: 2})
	if !m.OK {
		t.Fatalf("pane: %s", m.Error)
	}
	var loc map[string]any
	if err := json.Unmarshal(m.Res, &loc); err != nil {
		t.Fatalf("decode pane reply: %v", err)
	}
	if loc["id"] != "w2p1" || loc["workspace"] != "s1" || loc["window"] != "w2" || loc["windowName"] != "two" {
		t.Fatalf("pane reply = %v, want the location of the background pane", loc)
	}
	wantErr(t, "pane", "no pane", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "pane", Target: "ghost", ID: 3}))

	m = rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "status", Target: "", ID: 4})
	if !m.OK {
		t.Fatalf("status all: %s", m.Error)
	}
	var statuses map[string]wire.Status
	if err := json.Unmarshal(m.Res, &statuses); err != nil {
		t.Fatalf("decode statuses: %v", err)
	}
	if len(statuses) != 1 || statuses[a].State != "working" {
		t.Fatalf("statuses = %v, want the one wired pane working", statuses)
	}

	m = rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "status", Target: a, ID: 5})
	if !m.OK {
		t.Fatalf("status one: %s", m.Error)
	}
	var st wire.Status
	if err := json.Unmarshal(m.Res, &st); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if st.State != "working" || st.Message != "on it" {
		t.Fatalf("status = %+v, want the pane's own report", st)
	}
	wantErr(t, "status", "no pane", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "status", Target: "ghost", ID: 6}))
}

// TestSetPaneNameAction pins the rename through the rpc path: the name lands
// in the state and an unknown pane fails.
func TestSetPaneNameAction(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, _ := rpcServer(t, "rpcpanename")

	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "set-pane-name", Target: a, Name: "builder", ID: 1}); !m.OK {
		t.Fatalf("set-pane-name: %s", m.Error)
	}
	s.mu.Lock()
	name := s.st.Panes[a].Name
	s.mu.Unlock()
	if name != "builder" {
		t.Fatalf("pane name = %q, want builder", name)
	}
	wantErr(t, "set-pane-name", "no pane", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "set-pane-name", Target: "ghost", Name: "x", ID: 2}))
}

// TestToggleFoldAction pins the fold toggle through the rpc path: the reply
// carries the target and the new state.
func TestToggleFoldAction(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _ := rpcServer(t, "rpcfold")

	m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "toggle-fold", Target: "w1", ID: 1})
	if !m.OK {
		t.Fatalf("toggle-fold: %s", m.Error)
	}
	var rep struct {
		Target   string `json:"target"`
		Unfolded bool   `json:"unfolded"`
	}
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("decode toggle-fold reply: %v", err)
	}
	if rep.Target != "w1" || !rep.Unfolded {
		t.Fatalf("toggle-fold reply = %+v, want w1 unfolded", rep)
	}
	s.mu.Lock()
	unfolded := s.st.Workspaces[0].Windows[0].Unfolded
	s.mu.Unlock()
	if !unfolded {
		t.Fatal("the window did not unfold")
	}
}

// TestWriteAndKeys pins the keyboard paths: write-to and exec land on the
// pane, exec adds the Enter, keys carry the bytes, and an unknown pane fails.
func TestWriteAndKeys(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, _ := rpcServer(t, "rpcwrite")
	spy := spyPane(t, s, a)

	wantErr(t, "write-to", "no pane", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "write-to", Target: "ghost", Msg: "hi", ID: 1}))

	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "write-to", Target: a, Msg: "ls", ID: 2}); !m.OK {
		t.Fatalf("write-to: %s", m.Error)
	}
	m := readSpy(t, spy)
	if m.Action != "write" || m.Msg != "ls" || m.Enter {
		t.Fatalf("the pane got %+v, want write ls without Enter", m)
	}
	spyEmpty(t, spy)

	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "exec", Target: a, Msg: "ls", ID: 3}); !m.OK {
		t.Fatalf("exec: %s", m.Error)
	}
	m = readSpy(t, spy)
	if !m.Enter {
		t.Fatalf("exec reached the pane without Enter: %+v", m)
	}

	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "keys", Target: a, Keys: []byte("ctrl+a"), ID: 4}); !m.OK {
		t.Fatalf("keys: %s", m.Error)
	}
	m = readSpy(t, spy)
	if string(m.Keys) != "ctrl+a" {
		t.Fatalf("the pane got keys %q, want ctrl+a", m.Keys)
	}
}

// TestWriteToAdoptsPane pins the adoption: a write to a pane the server has no
// conn for dials the daemon, and the write lands.
func TestWriteToAdoptsPane(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, _ := rpcServer(t, "rpcadopt")

	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "write-to", Target: a, Msg: "echo hi", ID: 1}); !m.OK {
		t.Fatalf("write-to an unadopted pane: %s", m.Error)
	}
	s.mu.Lock()
	pc := s.panes[a]
	s.mu.Unlock()
	if pc == nil {
		t.Fatal("the pane was not adopted")
	}
}

// TestKeysUnknownPaneFails pins the keys guard: keys for a pane the server
// does not know fail instead of vanishing.
func TestKeysUnknownPaneFails(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _ := rpcServer(t, "rpckeys")
	wantErr(t, "keys", "no pane", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "keys", Target: "ghost", ID: 1}))
}

// TestReadAction pins the read: the pane's lines and its status ride the
// reply.
func TestReadAction(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _ := rpcServer(t, "rpcread")
	talkPane(t, s.st, "w1p3", wire.Msg{
		Type: "resp", OK: true, Lines: []string{"hello", "world"},
		St: &wire.Status{State: "idle"},
	})

	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "read", Target: "w1p3", ID: 1})
	if !m.OK {
		t.Fatalf("read: %s", m.Error)
	}
	var rep struct {
		Lines  []string    `json:"lines"`
		Status wire.Status `json:"status"`
	}
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("decode read reply: %v", err)
	}
	if len(rep.Lines) != 2 || rep.Lines[0] != "hello" || rep.Status.State != "idle" {
		t.Fatalf("read reply = %+v, want two lines and an idle status", rep)
	}
}

// TestHistoryAction pins the history guards and the win: a non-positive
// count, an unknown pane, a pane whose ring the server cannot reach and a pane
// on the alternate screen all fail with the reason, and a real ring answers
// with rows that fit the count.
func TestHistoryAction(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, _ := rpcServer(t, "rpchistory")

	wantErr(t, "history", "history needs lines > 0", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "history", Target: a, Count: 0, ID: 1}))
	wantErr(t, "history", "no pane", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "history", Target: "ghost", Count: 10, ID: 2}))

	// A pane known to the state but with no ring daemon behind it.
	s.mu.Lock()
	s.st.Panes["w1p9"] = &session.PaneState{Cols: 80, Rows: 24}
	s.mu.Unlock()
	wantErr(t, "history", "no pane", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "history", Target: "w1p9", Count: 10, ID: 3}))

	// A pane on the alternate screen has no scrollback to replay.
	s.mu.Lock()
	s.st.Panes["w1p8"] = &session.PaneState{Cols: 80, Rows: 24}
	s.mu.Unlock()
	talkPane(t, s.st, "w1p8", wire.Msg{
		Type: "replay", Data: []byte("alt screen"),
		Res: json.RawMessage(`{"alt":true}`),
	})
	wantErr(t, "history", "alternate screen", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "history", Target: "w1p8", Count: 10, ID: 4}))

	// The live pane replays its ring: the rows come back within the count.
	s.mu.Lock()
	s.st.Panes[a].Cols, s.st.Panes[a].Rows = 80, 24
	s.mu.Unlock()
	m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "history", Target: a, Count: 10, ID: 5})
	if !m.OK {
		t.Fatalf("history of a live ring: %s", m.Error)
	}
	var rep struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("decode history reply: %v", err)
	}
	if len(rep.Lines) > 10 {
		t.Fatalf("history returned %d rows, want at most the 10 asked for", len(rep.Lines))
	}
}

// TestListAction pins the listing: the reply carries the snapshot and the
// active window's tree.
func TestListAction(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, _ := rpcServer(t, "rpclist")

	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "list", ID: 1})
	if !m.OK {
		t.Fatalf("list: %s", m.Error)
	}
	var rep struct {
		Snapshot struct {
			Focus string `json:"focus"`
		} `json:"snapshot"`
		Tree json.RawMessage `json:"tree"`
	}
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("decode list reply: %v", err)
	}
	if rep.Snapshot.Focus != a {
		t.Fatalf("list snapshot focus = %q, want %q", rep.Snapshot.Focus, a)
	}
	if len(rep.Tree) == 0 || string(rep.Tree) == "null" {
		t.Fatal("list reply carried no tree")
	}
}

// TestZoomPaneAction pins the zoom toggle through the rpc path: two panes
// toggle the zoom on and off.
func TestZoomPaneAction(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, _ := rpcServer(t, "rpczoom")

	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "zoom-pane", ID: 1})
	if !m.OK {
		t.Fatalf("zoom-pane: %s", m.Error)
	}
	var rep struct {
		Zoom string `json:"zoom"`
	}
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("decode zoom reply: %v", err)
	}
	if rep.Zoom != a {
		t.Fatalf("zoom = %q, want %q", rep.Zoom, a)
	}
	s.mu.Lock()
	zoom := s.zoom
	s.mu.Unlock()
	if zoom != a {
		t.Fatalf("server zoom = %q, want %q", zoom, a)
	}

	m = rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "zoom-pane", ID: 2})
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("decode second zoom reply: %v", err)
	}
	if rep.Zoom != "" {
		t.Fatalf("second zoom = %q, want the zoom cleared", rep.Zoom)
	}
}

// TestZoomPaneSinglePaneIsNoop pins the no-op: a window with one pane already
// fills the area, so the reply names no zoom.
func TestZoomPaneSinglePaneIsNoop(t *testing.T) {
	t.Parallel()
	st := session.NewStateAt(t.TempDir(), "rpczoomone")
	id := testutil.RootPane(st)
	st.Workspaces[0].Windows[0].Root = &layout.Node{Pane: id}
	s := railServer(st)
	cl, conn := boxConn(t)

	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "zoom-pane", ID: 1})
	if !m.OK {
		t.Fatalf("zoom-pane: %s", m.Error)
	}
	var rep struct {
		Zoom string `json:"zoom"`
	}
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("decode zoom reply: %v", err)
	}
	if rep.Zoom != "" {
		t.Fatalf("single-pane zoom = %q, want empty", rep.Zoom)
	}
}

// TestUnknownActionFails pins the default arm: an action the server does not
// know is named in the error.
func TestUnknownActionFails(t *testing.T) {
	t.Parallel()
	s, cl, conn, _, _ := rpcServer(t, "rpcunknown")
	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "bogus", ID: 1})
	wantErr(t, "bogus", "unknown action", m)
}

// splitHolding returns the split node whose Kids directly hold the leaf id.
func splitHolding(n *layout.Node, id string) *layout.Node {
	if n == nil || n.Pane != "" {
		return nil
	}
	for _, k := range n.Kids {
		if k.Pane == id {
			return n
		}
	}
	for _, k := range n.Kids {
		if p := splitHolding(k, id); p != nil {
			return p
		}
	}
	return nil
}

// noSingleKid walks the tree and fails when any split holds exactly one kid.
func noSingleKid(t *testing.T, n *layout.Node) {
	t.Helper()
	if n == nil || n.Pane != "" {
		return
	}
	if len(n.Kids) < 2 {
		t.Fatalf("a split holds %d kid", len(n.Kids))
	}
	for _, k := range n.Kids {
		noSingleKid(t, k)
	}
}

// paneRects lays the window out with the server's own geometry.
func paneRects(t *testing.T, s *server, win *session.Window) map[string]layout.Rect {
	t.Helper()
	out := map[string]layout.Rect{}
	layout.Layout(win.Root, layout.PaneAreaForWithTabBar(s.cols, s.rows, s.railLocked()), s.cfg.Padding(), out)
	return out
}

// TestSwapPanesAction pins the swap: the ids trade places, every rectangle
// keeps its shape, focus stays on the focused pane, and a pair that is not
// in the same tree fails without touching it.
func TestSwapPanesAction(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, b := rpcServer(t, "rpcswap")
	screenPane(t, s.st, "w1p3")

	// Deepen the tree to row(a, col(b, c)) with focus on a.
	s.mu.Lock()
	s.st.Workspaces[0].Windows[0].Root = &layout.Node{Dir: "row", Kids: []*layout.Node{
		{Pane: a},
		{Dir: "col", Kids: []*layout.Node{{Pane: b}, {Pane: "w1p3"}}},
	}}
	s.st.Panes["w1p3"] = &session.PaneState{}
	win := s.st.Workspaces[0].Windows[0]
	before := paneRects(t, s, win)
	s.mu.Unlock()

	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "swap-panes", Target: b, Other: "w1p3", ID: 1}); !m.OK {
		t.Fatalf("swap-panes: %s", m.Error)
	}
	s.mu.Lock()
	win = s.st.Workspaces[0].Windows[0]
	ids := layout.AllPanes(win.Root)
	focus := win.Focus
	after := paneRects(t, s, win)
	s.mu.Unlock()
	if len(ids) != 3 || focus != a {
		t.Fatalf("after swap panes = %v focus = %q, want three panes focused on %q", ids, focus, a)
	}
	if after["w1p3"] != before[b] || after[b] != before["w1p3"] {
		t.Fatalf("a swap moved rectangles: before %v after %v", before, after)
	}
	if after[a] != before[a] {
		t.Fatalf("the untouched pane moved: %v -> %v", before[a], after[a])
	}

	// A swap naming a pane outside the tree fails and leaves it alone.
	wantErr(t, "swap-panes", "no pane", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "swap-panes", Target: a, Other: "ghost", ID: 2}))
	s.mu.Lock()
	ids = layout.AllPanes(s.st.ActiveWindow().Root)
	s.mu.Unlock()
	if len(ids) != 3 {
		t.Fatalf("a refused swap changed the tree: %v", ids)
	}
}

// TestMovePaneAction pins the move: the leaf lands on the asked edge of the
// target, the collapse leaves no single-kid split, the moved pane takes the
// focus, and a pane outside the tree or a bad dir is refused.
func TestMovePaneAction(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, b := rpcServer(t, "rpcmove")
	screenPane(t, s.st, "w1p3")

	// Deep tree row(a, col(b, c)); move the deep leaf c right of a.
	s.mu.Lock()
	s.st.Workspaces[0].Windows[0].Root = &layout.Node{Dir: "row", Kids: []*layout.Node{
		{Pane: a},
		{Dir: "col", Kids: []*layout.Node{{Pane: b}, {Pane: "w1p3"}}},
	}}
	s.st.Panes["w1p3"] = &session.PaneState{}
	s.mu.Unlock()

	m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "move-pane", Target: "w1p3", Other: a, Dir: "right", ID: 1})
	if !m.OK {
		t.Fatalf("move-pane: %s", m.Error)
	}
	s.mu.Lock()
	win := s.st.Workspaces[0].Windows[0]
	ids := layout.AllPanes(win.Root)
	focus := win.Focus
	root := win.Root
	s.mu.Unlock()
	if len(ids) != 3 {
		t.Fatalf("after move panes = %v, want three", ids)
	}
	if focus != "w1p3" {
		t.Fatalf("move-pane left focus on %q, want the moved pane %q", focus, "w1p3")
	}
	noSingleKid(t, root)
	p := splitHolding(root, "w1p3")
	if p == nil || p.Dir != "row" || len(p.Kids) != 2 ||
		p.Kids[0].Pane != a || p.Kids[1].Pane != "w1p3" {
		t.Fatalf("the moved pane did not land right of %q: %+v", a, p)
	}

	// A bad direction and a pane outside the tree both fail.
	wantErr(t, "move-pane", "dir must be", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "move-pane", Target: "w1p3", Other: a, Dir: "sideways", ID: 2}))
	wantErr(t, "move-pane", "no pane", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "move-pane", Target: "ghost", Other: a, Dir: "right", ID: 3}))
	wantErr(t, "move-pane", "no pane", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "move-pane", Target: "w1p3", Other: "ghost", Dir: "right", ID: 4}))

	// A target in another window is out of scope.
	s.mu.Lock()
	s.st.Workspaces[0].Windows = append(s.st.Workspaces[0].Windows,
		&session.Window{ID: "w2", Name: "two", Root: &layout.Node{Pane: "w2p1"}})
	s.st.Panes["w2p1"] = &session.PaneState{}
	s.mu.Unlock()
	wantErr(t, "move-pane", "no pane", rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "move-pane", Target: "w1p3", Other: "w2p1", Dir: "right", ID: 5}))
}
