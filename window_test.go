package main

import (
	"net"
	"testing"
	"time"
)

// windowServer is an in-process server with no clients. closeWindow only needs
// the state and the pane table.
func windowServer(t *testing.T) (*server, *State) {
	t.Helper()
	t.Setenv("DM_STATE", t.TempDir())
	st := newState("win")
	return railServer(st), st
}

// addWindow appends a window with one focused pane to the active workspace.
func addWindow(st *State, name string) (*Window, string) {
	w := &Window{ID: st.newWindowID(), Name: name}
	id := w.newPaneID()
	w.Root = &Node{Pane: id}
	w.Focus = id
	st.Panes[id] = &PaneState{}
	st.ws().Windows = append(st.ws().Windows, w)
	return w, id
}

// paneProbe attaches a fake pane conn and records what the server sends it.
type paneProbe struct {
	msgs chan Msg
	done chan struct{}
}

func probePane(t *testing.T, s *server, id string) *paneProbe {
	t.Helper()
	a, b := net.Pipe()
	s.panes[id] = &paneConn{id: id, c: a}
	p := &paneProbe{msgs: make(chan Msg, 8), done: make(chan struct{})}
	t.Cleanup(func() { _ = b.Close() })
	go func() {
		defer close(p.done)
		for {
			m, err := readMsg(b)
			if err != nil {
				return
			}
			p.msgs <- m
		}
	}()
	return p
}

// waitKill waits for the probe's conn to close and returns the messages it saw.
func (p *paneProbe) waitKill(t *testing.T) []Msg {
	t.Helper()
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
		t.Fatal("pane conn did not close")
	}
	var out []Msg
	for {
		select {
		case m := <-p.msgs:
			out = append(out, m)
		default:
			return out
		}
	}
}

func TestCloseWindowKillsPanesAndKeepsTheRest(t *testing.T) {
	s, st := windowServer(t)
	ws := st.ws()
	w1 := ws.Windows[0]
	p1 := rootPane(st)
	w2, p2 := addWindow(st, "two")
	ws.Active = 0

	probe1 := probePane(t, s, p1)
	probe2 := probePane(t, s, p2)

	if err := s.closeWindow(w2.ID); err != nil {
		t.Fatalf("closeWindow(%q): %v", w2.ID, err)
	}

	if len(ws.Windows) != 1 || ws.Windows[0].ID != w1.ID {
		t.Fatalf("windows after close = %+v, want only %q", ws.Windows, w1.ID)
	}
	if _, ok := st.Panes[p2]; ok {
		t.Fatalf("pane state %q survived the close", p2)
	}
	if _, ok := s.panes[p2]; ok {
		t.Fatalf("pane conn %q survived the close", p2)
	}
	if _, ok := st.Panes[p1]; !ok {
		t.Fatalf("pane state %q was killed with the other window", p1)
	}
	if _, ok := s.panes[p1]; !ok {
		t.Fatalf("pane conn %q was killed with the other window", p1)
	}

	msgs := probe2.waitKill(t)
	if len(msgs) != 1 || msgs[0].Action != "kill" {
		t.Fatalf("closed window pane got %+v, want one kill", msgs)
	}
	select {
	case m := <-probe1.msgs:
		t.Fatalf("kept window pane got %+v, want nothing", m)
	default:
	}

	if ws.Active != 0 || st.win().ID != w1.ID {
		t.Fatalf("active window = %d %q, want 0 %q", ws.Active, st.win().ID, w1.ID)
	}
}

func TestCloseWindowRefusesTheLastWindow(t *testing.T) {
	s, st := windowServer(t)
	ws := st.ws()
	w1 := ws.Windows[0]
	p1 := rootPane(st)
	probe := probePane(t, s, p1)

	err := s.closeWindow(w1.ID)
	if err == nil || err.Error() != "cannot close the last window" {
		t.Fatalf("closeWindow(last) = %v, want cannot close the last window", err)
	}
	if len(ws.Windows) != 1 || ws.Windows[0].ID != w1.ID {
		t.Fatalf("windows after refusal = %+v", ws.Windows)
	}
	if _, ok := st.Panes[p1]; !ok {
		t.Fatalf("pane state %q dropped by a refused close", p1)
	}
	if _, ok := s.panes[p1]; !ok {
		t.Fatalf("pane conn %q dropped by a refused close", p1)
	}
	select {
	case m := <-probe.msgs:
		t.Fatalf("refused close sent %+v", m)
	default:
	}
}

func TestCloseWindowUnknownID(t *testing.T) {
	s, _ := windowServer(t)
	err := s.closeWindow("nope")
	if err == nil || err.Error() != `no window "nope"` {
		t.Fatalf("closeWindow(unknown) = %v, want no window \"nope\"", err)
	}
}

func TestCloseWindowBackgroundKeepsActive(t *testing.T) {
	s, st := windowServer(t)
	ws := st.ws()
	w1 := ws.Windows[0]
	rootPane(st)
	w2, _ := addWindow(st, "two")
	w3, _ := addWindow(st, "three")
	ws.Active = 2

	if err := s.closeWindow(w2.ID); err != nil {
		t.Fatalf("closeWindow(%q): %v", w2.ID, err)
	}
	if ws.Active != 1 || st.win().ID != w3.ID {
		t.Fatalf("active window = %d %q, want 1 %q", ws.Active, st.win().ID, w3.ID)
	}
	if len(ws.Windows) != 2 || ws.Windows[0].ID != w1.ID || ws.Windows[1].ID != w3.ID {
		t.Fatalf("windows after background close = %+v", ws.Windows)
	}
}

func TestCloseWindowBelowActiveDecrements(t *testing.T) {
	s, st := windowServer(t)
	ws := st.ws()
	w1 := ws.Windows[0]
	rootPane(st)
	w2, _ := addWindow(st, "two")
	w3, _ := addWindow(st, "three")
	ws.Active = 1

	if err := s.closeWindow(w1.ID); err != nil {
		t.Fatalf("closeWindow(%q): %v", w1.ID, err)
	}
	if ws.Active != 0 || st.win().ID != w2.ID {
		t.Fatalf("active window = %d %q, want 0 %q", ws.Active, st.win().ID, w2.ID)
	}
	if len(ws.Windows) != 2 || ws.Windows[0].ID != w2.ID || ws.Windows[1].ID != w3.ID {
		t.Fatalf("windows after close = %+v", ws.Windows)
	}
}

func TestCloseWindowActiveClampsToLast(t *testing.T) {
	s, st := windowServer(t)
	ws := st.ws()
	w1 := ws.Windows[0]
	rootPane(st)
	w2, _ := addWindow(st, "two")
	ws.Active = 0

	if err := s.closeWindow(w1.ID); err != nil {
		t.Fatalf("closeWindow(%q): %v", w1.ID, err)
	}
	if ws.Active != 0 || st.win().ID != w2.ID {
		t.Fatalf("active window = %d %q, want 0 %q", ws.Active, st.win().ID, w2.ID)
	}
}

// TestCloseWindowActionSendsWindowID proves X resolves the focused pane to its
// window and never sends the pane id.
func TestCloseWindowActionSendsWindowID(t *testing.T) {
	tui, got := clientTUI(t, nil)
	tui.snap.Focus = "w1p1"
	tui.snap.All = []AllPane{
		{ID: "w1p1", Window: "w1"},
		{ID: "w2p1", Window: "w2"},
	}
	tui.armed = true

	tui.runAction("close-window")

	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "close-window" || m.Target != "w1" {
		t.Fatalf("close-window sent %+v, want a req for w1", m)
	}
	if m.Target == "w1p1" {
		t.Fatal("close-window sent the pane id")
	}
	if tui.armed {
		t.Fatal("close-window left the client armed")
	}
}

// TestCloseWindowActionUnknownPaneSendsNothing proves a focused pane outside
// the snapshot sends no request and raises a toast.
func TestCloseWindowActionUnknownPaneSendsNothing(t *testing.T) {
	tui, got := clientTUI(t, nil)
	tui.snap.Focus = "w9p9"
	tui.snap.All = []AllPane{{ID: "w1p1", Window: "w1"}}

	tui.runAction("close-window")

	select {
	case m := <-got:
		t.Fatalf("close-window sent %+v for an unknown pane", m)
	case <-time.After(50 * time.Millisecond):
	}
	if n := len(tui.toasts); n == 0 || tui.toasts[n-1].text != "no window for w9p9" {
		t.Fatalf("toasts = %+v, want no window for w9p9", tui.toasts)
	}
}
