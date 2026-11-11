package server

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dm/internal/session"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// TestNormaliseTermColour accepts the two forms a terminal answers with and
// rejects everything else, naming the value it refused.
func TestNormaliseTermColour(t *testing.T) {
	t.Parallel()
	ok := []struct{ in, want string }{
		{"rgb:ffff/0000/1234", "rgb:ffff/0000/1234"},
		{"rgb:FFFF/0000/ABCD", "rgb:ffff/0000/abcd"},
		{"#aabbcc", "rgb:aaaa/bbbb/cccc"},
		{"#AABBCC", "rgb:aaaa/bbbb/cccc"},
	}
	for _, c := range ok {
		got, err := normaliseTermColour(c.in)
		if err != nil {
			t.Fatalf("normaliseTermColour(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("normaliseTermColour(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	bad := []string{
		"rgb:fff/0000/0000",       // short channel
		"rgb:ffff/000/0000",       // short middle channel
		"rgb:ffff/0000",           // missing channel
		"rgb:ffff/0000/0000/0000", // extra channel
		"#abc",                    // short hex
		"#gggggg",                 // not hex
		"",                        // empty
		"nonsense",                // no prefix
	}
	for _, in := range bad {
		got, err := normaliseTermColour(in)
		if err == nil {
			t.Fatalf("normaliseTermColour(%q) = %q, want an error", in, got)
		}
		if in != "" && !strings.Contains(err.Error(), in) {
			t.Fatalf("error %q does not name the value %q", err, in)
		}
	}
}

// TestTermColoursFansOut proves a report reaches every live pane conn with the
// normalised pair and that the client gets an OK reply.
func TestTermColoursFansOut(t *testing.T) {
	t.Parallel()
	s := boxServer(t)
	cl, conn := boxConn(t)
	p1 := probePane(t, s, "w1p1")
	p2 := probePane(t, s, "w1p2")

	s.handleReq(cl, wire.Msg{Type: "req", Action: "term-colours", ID: 4,
		FG: "#AABBCC", BG: "rgb:1111/2222/3333"})

	for _, p := range []*paneProbe{p1, p2} {
		select {
		case m := <-p.msgs:
			if m.Action != "term-colours" {
				t.Fatalf("pane got action %q, want term-colours", m.Action)
			}
			if m.FG != "rgb:aaaa/bbbb/cccc" || m.BG != "rgb:1111/2222/3333" {
				t.Fatalf("pane got %q/%q, want the normalised pair", m.FG, m.BG)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("pane got no term-colours action")
		}
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	reply, err := wire.ReadMsg(conn)
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if !reply.OK || reply.Error != "" {
		t.Fatalf("reply = %+v, want ok", reply)
	}
}

// TestTermColoursSecondReportReplaces proves a later report overwrites the
// stored pair and fans the new one out.
func TestTermColoursSecondReportReplaces(t *testing.T) {
	t.Parallel()
	s := boxServer(t)
	cl, conn := boxConn(t)
	p := probePane(t, s, "w1p1")

	s.handleReq(cl, wire.Msg{Type: "req", Action: "term-colours", ID: 1,
		FG: "rgb:1111/1111/1111", BG: "rgb:2222/2222/2222"})
	<-p.msgs
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := wire.ReadMsg(conn); err != nil {
		t.Fatalf("first reply: %v", err)
	}

	s.handleReq(cl, wire.Msg{Type: "req", Action: "term-colours", ID: 2,
		FG: "rgb:3333/3333/3333", BG: "rgb:4444/4444/4444"})
	select {
	case m := <-p.msgs:
		if m.FG != "rgb:3333/3333/3333" || m.BG != "rgb:4444/4444/4444" {
			t.Fatalf("second fan-out = %q/%q, want the new pair", m.FG, m.BG)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second report did not fan out")
	}

	s.mu.Lock()
	tc := s.termColours
	s.mu.Unlock()
	if tc == nil || tc.fg != "rgb:3333/3333/3333" || tc.bg != "rgb:4444/4444/4444" {
		t.Fatalf("stored pair = %+v, want the second report", tc)
	}
}

// TestTermColoursBadValueChangesNothing proves a bad value replies with the
// error, leaves the stored pair alone and fans nothing out.
func TestTermColoursBadValueChangesNothing(t *testing.T) {
	t.Parallel()
	s := boxServer(t)
	cl, conn := boxConn(t)
	p := probePane(t, s, "w1p1")

	s.handleReq(cl, wire.Msg{Type: "req", Action: "term-colours", ID: 1,
		FG: "rgb:1111/1111/1111", BG: "rgb:2222/2222/2222"})
	<-p.msgs
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := wire.ReadMsg(conn); err != nil {
		t.Fatalf("first reply: %v", err)
	}

	s.handleReq(cl, wire.Msg{Type: "req", Action: "term-colours", ID: 2,
		FG: "rgb:333/3333/3333", BG: "rgb:4444/4444/4444"})
	reply, err := wire.ReadMsg(conn)
	if err != nil {
		t.Fatalf("bad-value reply: %v", err)
	}
	if reply.OK || !strings.Contains(reply.Error, "rgb:333/3333/3333") {
		t.Fatalf("reply = %+v, want an error naming the bad value", reply)
	}

	select {
	case m := <-p.msgs:
		t.Fatalf("bad value fanned out %+v", m)
	case <-time.After(100 * time.Millisecond):
	}

	s.mu.Lock()
	tc := s.termColours
	s.mu.Unlock()
	if tc == nil || tc.fg != "rgb:1111/1111/1111" || tc.bg != "rgb:2222/2222/2222" {
		t.Fatalf("stored pair = %+v, want the first report kept", tc)
	}
}

// TestTermColoursSpawnCarriesFlags proves a spawn after a report builds the
// command line with both flags. The spawn is captured, not started.
func TestTermColoursSpawnCarriesFlags(t *testing.T) {
	t.Parallel()
	st := session.NewStateAt(t.TempDir(), "spawn")
	id := testutil.RootPane(st)
	s := railServer(st)
	if err := os.MkdirAll(filepath.Join(st.SessionDir(), "panes"), 0o700); err != nil {
		t.Fatal(err)
	}

	cl, conn := boxConn(t)
	s.handleReq(cl, wire.Msg{Type: "req", Action: "term-colours", ID: 1,
		FG: "rgb:1111/2222/3333", BG: "rgb:4444/5555/6666"})
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := wire.ReadMsg(conn); err != nil {
		t.Fatalf("reply: %v", err)
	}

	var got []string
	s.spawn = func(c *exec.Cmd) error {
		got = append([]string(nil), c.Args...)
		return errors.New("captured")
	}
	if err := s.ensurePane(id); err == nil {
		t.Fatal("ensurePane returned nil, want the captured spawn error")
	}
	if !hasFlag(got, "--fg", "rgb:1111/2222/3333") || !hasFlag(got, "--bg", "rgb:4444/5555/6666") {
		t.Fatalf("spawn args = %v, want --fg and --bg", got)
	}
}

// TestTermColoursAdoptedPaneGetsAction proves a pane that outlived a server is
// sent the stored pair after the dial succeeds.
func TestTermColoursAdoptedPaneGetsAction(t *testing.T) {
	t.Parallel()
	st := session.NewStateAt(t.TempDir(), "adopt")
	id := testutil.RootPane(st)
	s := railServer(st)
	s.termColours = &termColours{fg: "rgb:1111/2222/3333", bg: "rgb:4444/5555/6666"}

	sock := st.PaneSock(id)
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	got := make(chan wire.Msg, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		m, err := wire.ReadMsg(c)
		_ = c.Close()
		if err == nil {
			got <- m
		}
		// liveCwd asks the same socket for the cwd. Answer it so ensurePane
		// does not sit on the read deadline.
		c2, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c2.Close() }()
		if m, err := wire.ReadMsg(c2); err == nil {
			_ = wire.WriteMsg(c2, wire.Msg{Type: "resp", ID: m.ID, OK: true, Cwd: "/tmp"})
		}
	}()

	if err := s.ensurePane(id); err != nil {
		t.Fatalf("ensurePane: %v", err)
	}
	select {
	case m := <-got:
		if m.Action != "term-colours" || m.FG != "rgb:1111/2222/3333" || m.BG != "rgb:4444/5555/6666" {
			t.Fatalf("adopted pane got %+v, want the stored pair", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("adopted pane got no term-colours action")
	}
}

// hasFlag reports whether args carries name followed by value.
func hasFlag(args []string, name, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name && args[i+1] == value {
			return true
		}
	}
	return false
}
