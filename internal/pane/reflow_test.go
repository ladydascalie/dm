package pane

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"dm/internal/vt"
	"dm/internal/wire"
)

// newReflowPane is a paneProc with the fields the reflow path touches and
// nothing that needs a PTY. The socket path is driven directly, as the other
// pane tests do, so no shell is spawned.
func newReflowPane(cols, rows int) *paneProc {
	return &paneProc{
		id:   "w1p1",
		vt:   vt.NewTerm(cols, rows),
		ring: &scrollRing{},
		subs: map[*subConn]bool{},
		st:   wire.Status{State: "idle"},
	}
}

// feedReflow writes a stream to the pane's screen and ring, as a pane's output
// does, and flushes it: the tests stand for a pane whose output has gone quiet,
// so the quiet timer has already placed the last cluster.
func feedReflow(p *paneProc, stream string) {
	p.mu.Lock()
	p.vt.Write([]byte(stream))
	p.vt.Flush()
	p.ring.Write([]byte(stream))
	p.mu.Unlock()
}

// restoreSnapshot restores the reply's snapshot into a fresh terminal, as the
// client does, so the test reads the picture the server would broadcast.
func restoreSnapshot(t *testing.T, msg wire.Msg) *vt.Term {
	t.Helper()
	if msg.Type != "screen" {
		t.Fatalf("reply type %q, want screen", msg.Type)
	}
	if msg.VTSnap == nil {
		t.Fatal("reply carried no snapshot")
	}
	term := vt.NewTerm(1, 1)
	if err := term.Restore(msg.VTSnap); err != nil {
		t.Fatalf("restore snapshot: %v", err)
	}
	return term
}

// TestReflowWidensScreen feeds a pane a stream wider than its 10-column screen,
// reflows it to 20 columns, and checks the reply carries the full text and the
// cursor a direct replay at the new width produces.
func TestReflowWidensScreen(t *testing.T) {
	p := newReflowPane(10, 6)
	feedReflow(p, "0123456789abcdefghij0123456789abcdefghij")
	ringBefore := string(p.ring.Bytes())

	serve, cli := net.Pipe()
	defer cli.Close()
	go p.serve(serve)

	_ = cli.SetDeadline(time.Now().Add(5 * time.Second))
	if err := wire.WriteMsg(cli, wire.Msg{Type: "req", Action: "reflow", Cols: 20, Rows: 6}); err != nil {
		t.Fatalf("send reflow request: %v", err)
	}
	reply, err := wire.ReadMsg(cli)
	if err != nil {
		t.Fatalf("read reflow reply: %v", err)
	}

	term := restoreSnapshot(t, reply)
	if cols, rows := term.Grid().Size(); cols != 20 || rows != 6 {
		t.Fatalf("reflowed size %dx%d, want 20x6", cols, rows)
	}
	for _, y := range []int{0, 1} {
		if got := term.Grid().Text(y); got != "0123456789abcdefghij" {
			t.Errorf("row %d = %q, want the full 20 columns", y, got)
		}
	}
	if got := term.Grid().Text(2); got != "" {
		t.Errorf("row 2 = %q, want blank", got)
	}
	if got := term.Grid().Cursor(); got.X != 19 || got.Y != 1 || !got.WrapNext {
		t.Errorf("cursor = %+v, want col 19 row 1 with wrap pending", got)
	}
	// A reflow is a picture, not output: the ring is not touched.
	if got := string(p.ring.Bytes()); got != ringBefore {
		t.Errorf("ring changed by reflow: %d bytes, want the original %d", len(got), len(ringBefore))
	}
}

// TestReflowAltScreenUnchanged leaves an alternate-screen pane alone: the reply
// is the same screen and the alternate flag, so the caller can refuse it.
func TestReflowAltScreenUnchanged(t *testing.T) {
	p := newReflowPane(10, 6)
	feedReflow(p, "0123456789abcdefghij0123456789abcdefghij")
	p.mu.Lock()
	p.vt.Write([]byte("\x1b[?1049h\x1b[2J\x1b[Happ"))
	p.vt.Flush()
	p.mu.Unlock()

	serve, cli := net.Pipe()
	defer cli.Close()
	go p.serve(serve)

	_ = cli.SetDeadline(time.Now().Add(5 * time.Second))
	if err := wire.WriteMsg(cli, wire.Msg{Type: "req", Action: "reflow", Cols: 20, Rows: 6}); err != nil {
		t.Fatalf("send reflow request: %v", err)
	}
	reply, err := wire.ReadMsg(cli)
	if err != nil {
		t.Fatalf("read reflow reply: %v", err)
	}

	term := restoreSnapshot(t, reply)
	if cols, rows := term.Grid().Size(); cols != 10 || rows != 6 {
		t.Fatalf("alt-screen size %dx%d, want the unchanged 10x6", cols, rows)
	}
	if got := term.Grid().Text(0); got != "app" {
		t.Fatalf("alt-screen row 0 = %q, want %q", got, "app")
	}
	var res struct {
		Alt bool `json:"alt"`
	}
	if err := json.Unmarshal(reply.Res, &res); err != nil {
		t.Fatalf("decode Res: %v", err)
	}
	if !res.Alt {
		t.Error("reply did not report the alternate screen")
	}
}

// TestReflowBadSizeIgnored sends a size below 2: the pane answers nothing and
// keeps its grid.
func TestReflowBadSizeIgnored(t *testing.T) {
	p := newReflowPane(10, 6)
	feedReflow(p, "hello")

	serve, cli := net.Pipe()
	defer cli.Close()
	go p.serve(serve)

	_ = cli.SetDeadline(time.Now().Add(5 * time.Second))
	if err := wire.WriteMsg(cli, wire.Msg{Type: "req", Action: "reflow", Cols: 1, Rows: 6}); err != nil {
		t.Fatalf("send reflow request: %v", err)
	}

	// The refused action must not answer.
	_ = cli.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if msg, err := wire.ReadMsg(cli); err == nil {
		t.Fatalf("bad size answered with %+v, want no reply", msg)
	} else if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("read after bad size: %v, want a timeout", err)
	}

	// The pane still serves, and its grid is what it was.
	_ = cli.SetDeadline(time.Now().Add(5 * time.Second))
	if err := wire.WriteMsg(cli, wire.Msg{Type: "req", Action: "screen", ID: 1}); err != nil {
		t.Fatalf("send screen request: %v", err)
	}
	reply, err := wire.ReadMsg(cli)
	if err != nil {
		t.Fatalf("read screen reply: %v", err)
	}
	term := restoreSnapshot(t, reply)
	if cols, rows := term.Grid().Size(); cols != 10 || rows != 6 {
		t.Fatalf("size after bad reflow %dx%d, want 10x6", cols, rows)
	}
	if got := term.Grid().Text(0); got != "hello" {
		t.Fatalf("row 0 after bad reflow = %q, want %q", got, "hello")
	}
}
