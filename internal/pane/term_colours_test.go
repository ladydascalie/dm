package pane

import (
	"bytes"
	"net"
	"testing"
	"time"

	"dm/internal/osc"
	"dm/internal/vt"
	"dm/internal/wire"
)

// newTestPane is a paneProc with the fields the term-colours path touches and
// nothing that needs a PTY. The socket path is driven directly, as the other
// pane tests do, so no shell is spawned.
func newTestPane(fg, bg string) *paneProc {
	return &paneProc{
		id:   "w1p1",
		vt:   vt.NewTerm(20, 3),
		ring: NewRing(ringDefault),
		subs: map[*subConn]bool{},
		st:   wire.Status{State: "idle"},
		fg:   fg,
		bg:   bg,
	}
}

// captureReplies returns a filter whose terminal replies are collected, so the
// test reads what the pane would have written into the PTY.
func captureReplies(p *paneProc) (*osc.Filter, *[]string) {
	var replies []string
	f := p.newFilter()
	f.Reply = func(s string) { replies = append(replies, s) }
	return f, &replies
}

// feed runs one byte stream through the filter and drops the clean output.
func feed(f *osc.Filter, b string) {
	var out bytes.Buffer
	f.Feed([]byte(b), &out)
}

// TestTermColoursActionReachesFilter proves a term-colours action stores the
// pair under the pane lock and that the long-lived filter answers OSC 11 with
// the stored value on the next read.
func TestTermColoursActionReachesFilter(t *testing.T) {
	t.Parallel()
	p := newTestPane(osc.TermFG, osc.TermBG)
	filter, replies := captureReplies(p)

	serve, cli := net.Pipe()
	defer func() { _ = cli.Close() }()
	go p.serve(serve)

	_ = cli.SetDeadline(time.Now().Add(5 * time.Second))
	action := wire.Msg{
		Type: "req", Action: "term-colours", ID: 7,
		FG: "rgb:1111/2222/3333",
		BG: "rgb:4444/5555/6666",
	}
	if err := wire.WriteMsg(cli, action); err != nil {
		t.Fatalf("send term-colours: %v", err)
	}
	reply, err := wire.ReadMsg(cli)
	if err != nil {
		t.Fatalf("read term-colours reply: %v", err)
	}
	if reply.Type != "resp" || reply.ID != 7 || !reply.OK {
		t.Fatalf("reply = %+v, want resp id 7 ok", reply)
	}

	// The filter was built with the defaults. It must pick up the action
	// before the next read, as readLoop does.
	p.syncFilter(filter)
	feed(filter, "\x1b]11;?\x1b\\")
	if len(*replies) != 1 {
		t.Fatalf("OSC 11 produced %d replies, want 1", len(*replies))
	}
	if want := "\x1b]11;rgb:4444/5555/6666\x1b\\"; (*replies)[0] != want {
		t.Fatalf("OSC 11 reply = %q, want %q", (*replies)[0], want)
	}

	// The foreground moves on OSC 10. OSC 12 is the cursor colour, which the
	// pane does not track: it answers the dedicated field's fallback, not the
	// foreground.
	feed(filter, "\x1b]10;?\x1b\\")
	feed(filter, "\x1b]12;?\x1b\\")
	if len(*replies) != 3 {
		t.Fatalf("OSC 10 and OSC 12 produced %d more replies, want 2", len(*replies)-1)
	}
	for i, want := range []string{
		"\x1b]10;rgb:1111/2222/3333\x1b\\",
		"\x1b]12;" + osc.TermCursor + "\x1b\\",
	} {
		if got := (*replies)[i+1]; got != want {
			t.Fatalf("reply %d = %q, want %q", i, got, want)
		}
	}
}

// TestTermColoursFromStart proves a pane that started with --fg and --bg
// answers those values before any term-colours action arrives.
func TestTermColoursFromStart(t *testing.T) {
	t.Parallel()
	p := newTestPane("rgb:aaaa/bbbb/cccc", "rgb:dddd/eeee/ffff")
	filter, replies := captureReplies(p)
	feed(filter, "\x1b]11;?\x1b\\")
	if len(*replies) != 1 {
		t.Fatalf("OSC 11 produced %d replies, want 1", len(*replies))
	}
	if want := "\x1b]11;rgb:dddd/eeee/ffff\x1b\\"; (*replies)[0] != want {
		t.Fatalf("OSC 11 reply = %q, want the --bg value %q", (*replies)[0], want)
	}
}

// TestTermColoursEmptyIsNoOp proves an empty field leaves the other half and
// the pane's current value alone rather than erroring or blanking it.
func TestTermColoursEmptyIsNoOp(t *testing.T) {
	t.Parallel()
	p := newTestPane(osc.TermFG, osc.TermBG)
	serve, cli := net.Pipe()
	defer func() { _ = cli.Close() }()
	go p.serve(serve)

	_ = cli.SetDeadline(time.Now().Add(5 * time.Second))
	action := wire.Msg{Type: "req", Action: "term-colours", ID: 3, FG: "", BG: "rgb:1234/5678/9abc"}
	if err := wire.WriteMsg(cli, action); err != nil {
		t.Fatalf("send term-colours: %v", err)
	}
	reply, err := wire.ReadMsg(cli)
	if err != nil {
		t.Fatalf("read term-colours reply: %v", err)
	}
	if !reply.OK || reply.Error != "" {
		t.Fatalf("reply = %+v, want ok with no error", reply)
	}

	p.mu.Lock()
	gotFG, gotBG := p.fg, p.bg
	p.mu.Unlock()
	if gotFG != osc.TermFG {
		t.Fatalf("foreground = %q, want the default kept", gotFG)
	}
	if gotBG != "rgb:1234/5678/9abc" {
		t.Fatalf("background = %q, want the action value", gotBG)
	}
}

// TestRunRefusesEmptyTermColour proves Run rejects an empty colour before it
// starts a shell.
func TestRunRefusesEmptyTermColour(t *testing.T) {
	t.Parallel()
	err := Run([]string{"--id", "p1", "--session", "s1", "--fg", ""})
	if err == nil {
		t.Fatal("Run accepted an empty --fg")
	}
	if want := "pane needs a non-empty --fg and --bg"; err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}
