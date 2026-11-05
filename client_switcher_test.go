package main

import (
	"bytes"
	"encoding/json"
	"net"
	"testing"
	"time"

	"dm/internal/layout"

	"github.com/hinshun/vt10x"
)

// switcherTUI is a client over a real net.Pipe: keys go in through processInput,
// and every request it sends lands on the returned channel. The snapshot has one
// workspace and one window over two panes, so the switcher's tree has rows to
// narrow and a pane to preview.
func switcherTUI(t *testing.T) (*tui, chan Msg) {
	t.Helper()
	root := &layout.Node{Pane: "w1p1"}
	snap := &Snapshot{Cols: 100, Rows: 30, Rail: 34, Root: root, Focus: "w1p1"}
	rects := map[string]layout.Rect{}
	layout.Layout(root, layout.PaneAreaFor(100, 30, 34), rects)
	snap.Panes = []PaneInfo{{ID: "w1p1", Rect: rects["w1p1"]}}
	snap.All = []AllPane{
		{
			ID: "w1p1", Name: "alpha", Workspace: "s1", WorkspaceName: "main",
			Window: "w1", WindowName: "one", Focus: true, WorkspaceActive: true,
			WindowActive: true, WindowUnfolded: true,
		},
		{
			ID: "w1p2", Name: "beta", Workspace: "s1", WorkspaceName: "main",
			Window: "w1", WindowName: "one", WindowUnfolded: true,
		},
	}
	tui := &tui{snap: snap, vts: map[string]vt10x.Terminal{}, out: &bytes.Buffer{}}
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	tui.conn = &conn{Conn: a}
	got := make(chan Msg, 32)
	go func() {
		defer close(got)
		for {
			m, err := readMsg(b)
			if err != nil {
				return
			}
			got <- m
		}
	}()
	return tui, got
}

// feed hands bytes to processInput the way a read would.
func feed(t *tui, b string) {
	t.inbuf = append(t.inbuf, b...)
	t.processInput()
}

// TestSwitcherPrefixToggles: prefix a opens the float and a second prefix press
// closes it. Neither sends a request.
func TestSwitcherPrefixToggles(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	if tui.sw == nil {
		t.Fatal("prefix a did not open the switcher")
	}
	feed(tui, "\x02")
	if tui.sw != nil {
		t.Fatal("a second prefix press did not close the switcher")
	}
	noMsg(t, got)
}

// TestSwitcherCtrlNPMoves: the contract binds down/up and ctrl+n/p to the same
// walk through selectable rows. The arrows are covered by the move tests; this
// holds the ctrl keys to the same step.
func TestSwitcherCtrlNPMoves(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	if it, ok := tui.sw.selected(); !ok || it.pane != "w1p1" {
		t.Fatalf("empty query selects %+v, want the first pane w1p1", it)
	}
	feed(tui, "\x0e") // ctrl+n, down
	if it, ok := tui.sw.selected(); !ok || it.pane != "w1p2" {
		t.Fatalf("ctrl+n selects %+v, want the next pane w1p2", it)
	}
	feed(tui, "\x10") // ctrl+p, up
	if it, ok := tui.sw.selected(); !ok || it.pane != "w1p1" {
		t.Fatalf("ctrl+p selects %+v, want the pane above w1p1", it)
	}
	noMsg(t, got)
}

// TestSwitcherTypeAndEnter: typing narrows the rows and enter sends a focus
// request for the selected pane, then closes the float.
func TestSwitcherTypeAndEnter(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	full := len(tui.sw.rows)
	feed(tui, "beta")
	if n := len(tui.sw.rows); n >= full {
		t.Fatalf("typing narrowed %d rows to %d", full, n)
	}
	if it, ok := tui.sw.selected(); !ok || it.pane != "w1p2" {
		t.Fatalf("selected = %+v, want the beta pane", it)
	}
	feed(tui, "\r")
	if tui.sw != nil {
		t.Fatal("enter left the switcher open")
	}
	select {
	case m := <-got:
		if m.Type != "req" || m.Action != "focus" || m.Target != "w1p2" {
			t.Fatalf("enter sent %+v, want a focus request for w1p2", m)
		}
	case <-time.After(time.Second):
		t.Fatal("enter sent nothing")
	}
	noMsg(t, got)
}

// TestSwitcherEscSendsNothing: esc closes the float without a request.
func TestSwitcherEscSendsNothing(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	feed(tui, "\x1b")
	if !tui.pending {
		t.Fatal("esc did not wait for the pending timer")
	}
	tui.escAt = time.Now().Add(-time.Second)
	tui.expireSwitcher(time.Now())
	if tui.sw != nil {
		t.Fatal("the pending esc did not close the switcher")
	}
	noMsg(t, got)
}

// TestSwitcherCtrlURestores: ctrl+u clears the query and brings the rows back.
func TestSwitcherCtrlURestores(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	full := len(tui.sw.rows)
	feed(tui, "beta")
	if len(tui.sw.rows) == full {
		t.Fatal("typing dropped no rows")
	}
	feed(tui, "\x15")
	if n := len(tui.sw.rows); n != full {
		t.Fatalf("ctrl+u left %d rows, want %d", n, full)
	}
	if q := tui.sw.q(); q != "" {
		t.Fatalf("ctrl+u left the query %q", q)
	}
	noMsg(t, got)
}

// TestSwitcherPreviewReplies: a read reply with the wrong id is dropped, one
// with the current id fills the preview, and a failure stays silent.
func TestSwitcherPreviewReplies(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	feed(tui, "beta")
	tui.draw()

	var read Msg
	select {
	case m := <-got:
		if m.Type != "req" || m.Action != "read" || m.Target != "w1p2" {
			t.Fatalf("draw sent %+v, want a read request for w1p2", m)
		}
		if m.ID <= 0 {
			t.Fatalf("read request carries no id: %+v", m)
		}
		read = m
	case <-time.After(time.Second):
		t.Fatal("draw sent no preview request")
	}

	// A second draw asks for nothing: the pane is already asked for.
	tui.draw()
	noMsg(t, got)

	// A reply with another id is stale and stays out of the band. A failure
	// with a stale id stays silent too.
	tui.apply(Msg{Type: "resp", ID: read.ID + 1, OK: true, Res: json.RawMessage(`{"lines":["stale"]}`)})
	if len(tui.sw.lines) != 0 {
		t.Fatalf("a stale reply filled the preview: %q", tui.sw.lines)
	}
	tui.apply(Msg{Type: "resp", ID: read.ID + 2, Error: "gone"})
	if len(tui.toasts) != 0 {
		t.Fatalf("a stale preview failure raised a toast: %+v", tui.toasts)
	}

	// The reply to the request fills the band.
	tui.apply(Msg{Type: "resp", ID: read.ID, OK: true, Res: json.RawMessage(`{"lines":["live"]}`)})
	if got := tui.sw.lines; len(got) != 1 || got[0] != "live" {
		t.Fatalf("the current reply filled %q, want [live]", got)
	}
}

// TestSwitcherMouseSwallowed: a mouse press while the float is open sends no
// keys to the pane and leaves the float up.
func TestSwitcherMouseSwallowed(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	feed(tui, "\x1b[<0;10;5M")
	if tui.sw == nil {
		t.Fatal("a mouse press closed the switcher")
	}
	noMsg(t, got)
	feed(tui, "\x1b[<0;10;5m")
	if tui.sw == nil {
		t.Fatal("a mouse release closed the switcher")
	}
	noMsg(t, got)
}
