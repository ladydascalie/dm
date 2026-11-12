package pane

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"dm/internal/vt"
	"dm/internal/wire"
)

// newReflowPane is a paneProc with the fields the resize path touches and
// nothing that needs a PTY. The socket path is driven directly, as the other
// pane tests do, so no shell is spawned.
func newReflowPane(cols, rows int) *paneProc {
	return &paneProc{
		id:   "w1p1",
		vt:   vt.NewTerm(cols, rows),
		ring: NewRing(ringDefault),
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

// resizePane sends one resize request and waits for the answer to the screen
// request that follows it, so a test never races the resize.
func resizePane(t *testing.T, cli net.Conn, p *paneProc, cols, rows int) *vt.Term {
	t.Helper()
	if err := wire.WriteMsg(cli, wire.Msg{Type: "req", Action: "resize", Cols: cols, Rows: rows}); err != nil {
		t.Fatalf("send resize request: %v", err)
	}
	if err := wire.WriteMsg(cli, wire.Msg{Type: "req", Action: "screen", ID: 1}); err != nil {
		t.Fatalf("send screen request: %v", err)
	}
	reply, err := wire.ReadMsg(cli)
	if err != nil {
		t.Fatalf("read screen reply: %v", err)
	}
	return restoreSnapshot(t, reply)
}

// TestResizeRewrapsScreen feeds a pane a stream wider than its 10-column
// screen, resizes it to 20 columns, and checks the screen the resize built:
// the same content re-wrapped, with the cursor a direct write at the new width
// produces.
func TestResizeRewrapsScreen(t *testing.T) {
	t.Parallel()
	p := newReflowPane(10, 6)
	feedReflow(p, "0123456789abcdefghij0123456789abcdefghij")
	ringBefore := string(p.ring.Bytes())

	serve, cli := net.Pipe()
	defer func() { _ = cli.Close() }()
	go p.serve(serve)
	_ = cli.SetDeadline(time.Now().Add(5 * time.Second))

	term := resizePane(t, cli, p, 20, 6)
	if cols, rows := term.Grid().Size(); cols != 20 || rows != 6 {
		t.Fatalf("resized size %dx%d, want 20x6", cols, rows)
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
	// A resize is a geometry change, not output: the ring is not touched.
	if got := string(p.ring.Bytes()); got != ringBefore {
		t.Errorf("ring changed by resize: %d bytes, want the original %d", len(got), len(ringBefore))
	}
}

// TestResizeNeverResurrectsHistory pins the rule the ring replay broke: a pane
// whose screen the app cleared holds only what the app painted, and a wider
// screen brings none of the scrolled output back.
func TestResizeNeverResurrectsHistory(t *testing.T) {
	t.Parallel()
	p := newReflowPane(10, 6)
	feedReflow(p, strings.Repeat("old line\r\n", 20))
	feedReflow(p, "\x1b[2J\x1b[Hprompt only")

	serve, cli := net.Pipe()
	defer func() { _ = cli.Close() }()
	go p.serve(serve)
	_ = cli.SetDeadline(time.Now().Add(5 * time.Second))

	term := resizePane(t, cli, p, 30, 10)
	joined := ""
	for y := 0; y < 10; y++ {
		joined += term.Grid().Text(y) + "\n"
	}
	if strings.Contains(joined, "old line") {
		t.Errorf("scrollback leaked onto the screen:\n%s", joined)
	}
	if got := term.Grid().Text(0); got != "prompt only" {
		t.Errorf("row 0 = %q, want the prompt alone", got)
	}
}

// TestResizeAltScreenKeepsGrid leaves an alternate-screen pane to the app: the
// plain resize keeps the content it had, and the screen request reports the
// alternate flag so the caller knows the app repaints itself.
func TestResizeAltScreenKeepsGrid(t *testing.T) {
	t.Parallel()
	p := newReflowPane(10, 6)
	feedReflow(p, "0123456789abcdefghij0123456789abcdefghij")
	p.mu.Lock()
	p.vt.Write([]byte("\x1b[?1049h\x1b[2J\x1b[Happ"))
	p.vt.Flush()
	p.mu.Unlock()

	serve, cli := net.Pipe()
	defer func() { _ = cli.Close() }()
	go p.serve(serve)
	_ = cli.SetDeadline(time.Now().Add(5 * time.Second))

	term := resizePane(t, cli, p, 20, 6)
	if got := term.Grid().Text(0); got != "app" {
		t.Fatalf("alt-screen row 0 = %q, want %q", got, "app")
	}
	if got := term.Grid().Text(1); got != "" {
		t.Errorf("alt-screen row 1 = %q, want blank", got)
	}
	var res struct {
		Alt bool `json:"alt"`
	}
	// The screen reply above is the one resizePane read; ask again for the
	// modes the reply carries.
	if err := wire.WriteMsg(cli, wire.Msg{Type: "req", Action: "screen", ID: 2}); err != nil {
		t.Fatalf("send second screen request: %v", err)
	}
	reply, err := wire.ReadMsg(cli)
	if err != nil {
		t.Fatalf("read second screen reply: %v", err)
	}
	if err := json.Unmarshal(reply.Res, &res); err != nil {
		t.Fatalf("decode Res: %v", err)
	}
	if !res.Alt {
		t.Error("reply did not report the alternate screen")
	}
}

// TestReflowAnswersCurrentPicture pins the handshake: after the resize did the
// rewrap, a reflow request answers with the screen as it is and changes
// nothing.
func TestReflowAnswersCurrentPicture(t *testing.T) {
	t.Parallel()
	p := newReflowPane(10, 6)
	feedReflow(p, "0123456789abcdefghij0123456789abcdefghij")

	serve, cli := net.Pipe()
	defer func() { _ = cli.Close() }()
	go p.serve(serve)
	_ = cli.SetDeadline(time.Now().Add(5 * time.Second))

	first := resizePane(t, cli, p, 20, 6)
	ringBefore := string(p.ring.Bytes())

	if err := wire.WriteMsg(cli, wire.Msg{Type: "req", Action: "reflow", Cols: 20, Rows: 6}); err != nil {
		t.Fatalf("send reflow request: %v", err)
	}
	reply, err := wire.ReadMsg(cli)
	if err != nil {
		t.Fatalf("read reflow reply: %v", err)
	}
	second := restoreSnapshot(t, reply)
	if cols, rows := second.Grid().Size(); cols != 20 || rows != 6 {
		t.Fatalf("reflow size %dx%d, want 20x6", cols, rows)
	}
	for y := 0; y < 3; y++ {
		if got, want := second.Grid().Text(y), first.Grid().Text(y); got != want {
			t.Errorf("row %d = %q, want the resize answer %q", y, got, want)
		}
	}
	if got, want := second.Grid().Cursor(), first.Grid().Cursor(); got != want {
		t.Errorf("cursor = %+v, want the resize answer %+v", got, want)
	}
	if got := string(p.ring.Bytes()); got != ringBefore {
		t.Errorf("ring changed by reflow: %d bytes, want the original %d", len(got), len(ringBefore))
	}
}

// TestReflowBadSizeIgnored sends a size below 2: the pane answers nothing and
// keeps its grid.
func TestReflowBadSizeIgnored(t *testing.T) {
	t.Parallel()
	p := newReflowPane(10, 6)
	feedReflow(p, "hello")

	serve, cli := net.Pipe()
	defer func() { _ = cli.Close() }()
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
