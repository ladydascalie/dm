package cli

// The wait verb: the request it dials the pane socket with, the bytes it
// streams to stdout, the status line it ends on and the exit code it maps to.
// The tests drive runWait directly wherever the ending is nonzero, because
// Run hands a nonzero code to os.Exit.

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dm/internal/paths"
	"dm/internal/wire"
)

// startFakePane listens on one pane's socket under the given state root, so a
// wait dials the test instead of a real pane daemon.
func startFakePane(t *testing.T, root, session, id string, h fakeHandler) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(paths.PaneSockAt(root, session, id)), 0o700); err != nil {
		t.Fatal(err)
	}
	sock := paths.PaneSockAt(root, session, id)
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
			go serveFake(c, h)
		}
	}()
}

// waitRun is one wait test's harness: the flags runWait parses and buffers
// standing in for the streams the verb writes.
type waitRun struct {
	c      *cmdFlags
	out    *bytes.Buffer
	errOut *bytes.Buffer
}

// newWaitRun parses argv under the given state root and pairs the flags with
// buffers, so a parallel test captures output without touching the process
// streams.
func newWaitRun(root string, argv ...string) waitRun {
	c := newFlagsAt(root, "wait")
	c.parse(argv)
	return waitRun{c: c, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}}
}

// run runs the wait and returns its exit code and error.
func (w waitRun) run() (int, error) { return runWaitAt(w.out, w.errOut, w.c) }

// waitStream is the frame sequence one wait test plays: the tail resp, any
// bytes frames in order, then the final reply. Holding keeps the connection
// open and silent, so a read deadline runs out instead of hitting an end of
// file.
type waitStream struct {
	tail    bool
	bytes   [][]byte
	final   wire.Msg
	holding time.Duration
}

// waitPlayer turns a waitStream into a fakeHandler. It writes its frames
// itself and leaves the framing to the pane's own serving shape.
func waitPlayer(s waitStream) fakeHandler {
	return func(c net.Conn, req wire.Msg) (wire.Msg, bool) {
		if s.tail {
			tail := wire.Msg{Type: "resp", ID: req.ID, OK: true,
				Res: json.RawMessage(`{"lines":[],"start":0,"rows":0,"anchor":true}`)}
			if err := wire.WriteMsg(c, tail); err != nil {
				return wire.Msg{}, false
			}
		}
		for _, b := range s.bytes {
			if err := wire.WriteMsg(c, wire.Msg{Type: "bytes", Data: b}); err != nil {
				return wire.Msg{}, false
			}
		}
		if s.holding > 0 {
			time.Sleep(s.holding)
			return wire.Msg{}, false
		}
		if s.final.Type == "" && s.final.OK {
			final := s.final
			final.Type, final.ID = "resp", req.ID
			if err := wire.WriteMsg(c, final); err != nil {
				return wire.Msg{}, false
			}
		}
		return wire.Msg{}, false
	}
}

// exitOf lifts an int into the pointer the wire carries.
func exitOf(n int) *int { return &n }

// TestWaitNeedsAPane proves wait checks its argument before anything dials.
func TestWaitNeedsAPane(t *testing.T) {
	t.Parallel()
	if err := Run([]string{"wait"}); err == nil || !strings.Contains(err.Error(), "wait needs a pane id") {
		t.Fatalf("dm wait without arguments = %v", err)
	}
}

// TestWaitDialsThePaneSocket proves wait sends its request straight to the
// pane socket, honouring -s for the session, and ends on the exit line.
func TestWaitDialsThePaneSocket(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	startFakePane(t, root, "other", "w1p1", waitPlayer(waitStream{
		final: wire.Msg{OK: true, Exit: exitOf(0), WaitReason: "mark"},
	}))
	w := newWaitRun(root, "w1p1", "-s", "other")
	_, err := w.run()
	if err != nil {
		t.Fatalf("dm wait: %v", err)
	}
	if got := w.out.String(); got != "" {
		t.Fatalf("plain wait printed %q to stdout, want nothing", got)
	}
	if got := w.errOut.String(); got != "* done, exit 0\n" {
		t.Fatalf("wait printed %q to stderr, want '* done, exit 0'", got)
	}
}

// TestWaitStreamsAndNamesTheExit proves the bytes reach stdout in order, raw,
// and the final resp ends the wait with the command's exit code.
func TestWaitStreamsAndNamesTheExit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	startFakePane(t, root, "main", "w1p1", waitPlayer(waitStream{
		tail:  true,
		bytes: [][]byte{[]byte("buil"), []byte("ding\n")},
		final: wire.Msg{OK: true, Exit: exitOf(3), WaitReason: "mark"},
	}))
	w := newWaitRun(root, "w1p1")
	code, err := w.run()
	if err != nil {
		t.Fatalf("dm wait: %v", err)
	}
	if code != 3 {
		t.Fatalf("wait returned %d, want the command's exit 3", code)
	}
	if got := w.out.String(); got != "building\n" {
		t.Fatalf("wait streamed %q, want the raw bytes in order", got)
	}
	if got := w.errOut.String(); got != "* done, exit 3\n" {
		t.Fatalf("wait printed %q, want '* done, exit 3'", got)
	}
}

// TestWaitPrintsTheTailBeforeTheBytes proves the tail resp reaches stdout:
// the output between the Enter anchor and the wait's connect, printed before
// the first bytes frame, so a fast command observed late prints its output
// and the status line in one call.
func TestWaitPrintsTheTailBeforeTheBytes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	startFakePane(t, root, "main", "w1p1", func(c net.Conn, req wire.Msg) (wire.Msg, bool) {
		tail := wire.Msg{Type: "resp", ID: req.ID, OK: true,
			Res: json.RawMessage(`{"lines":["$ go build","error: nope","",""],"start":1,"rows":3,"anchor":true}`)}
		if err := wire.WriteMsg(c, tail); err != nil {
			return wire.Msg{}, false
		}
		if err := wire.WriteMsg(c, wire.Msg{Type: "bytes", Data: []byte("more\n")}); err != nil {
			return wire.Msg{}, false
		}
		final := wire.Msg{Type: "resp", ID: req.ID, OK: true, Exit: exitOf(1), WaitReason: "mark"}
		if err := wire.WriteMsg(c, final); err != nil {
			return wire.Msg{}, false
		}
		return wire.Msg{}, false
	})
	w := newWaitRun(root, "w1p1")
	code, err := w.run()
	if err != nil {
		t.Fatalf("dm wait: %v", err)
	}
	if code != 1 {
		t.Fatalf("wait returned %d, want 1", code)
	}
	// start 1 puts the echoed command line before the anchor: the tail
	// is the output the command produced, not the echo of it.
	if got, want := w.out.String(), "error: nope\nmore\n"; got != want {
		t.Fatalf("wait printed %q, want %q: the tail ahead of the bytes", got, want)
	}
	if got := w.errOut.String(); got != "* done, exit 1\n" {
		t.Fatalf("wait printed %q, want '* done, exit 1'", got)
	}
}

// TestWaitDiscardsPreTailBytes proves the pre-connect window prints once:
// the pane broadcasts bytes frames before the tail resp lands and carries the
// same window in the tail, so the stream stays silent until the tail is on
// stdout, then prints every byte that follows.
func TestWaitDiscardsPreTailBytes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	startFakePane(t, root, "main", "w1p1", func(c net.Conn, req wire.Msg) (wire.Msg, bool) {
		// The window the tail resp carries also went out as broadcast bytes
		// frames ahead of it, the shape review found.
		for _, b := range [][]byte{[]byte("early line\n"), []byte("early too\n")} {
			if err := wire.WriteMsg(c, wire.Msg{Type: "bytes", Data: b}); err != nil {
				return wire.Msg{}, false
			}
		}
		tail := wire.Msg{Type: "resp", ID: req.ID, OK: true,
			Res: json.RawMessage(`{"lines":["early line","early too"],"start":0,"rows":2,"anchor":true}`)}
		if err := wire.WriteMsg(c, tail); err != nil {
			return wire.Msg{}, false
		}
		if err := wire.WriteMsg(c, wire.Msg{Type: "bytes", Data: []byte("late line\n")}); err != nil {
			return wire.Msg{}, false
		}
		final := wire.Msg{Type: "resp", ID: req.ID, OK: true, Exit: exitOf(0), WaitReason: "mark"}
		if err := wire.WriteMsg(c, final); err != nil {
			return wire.Msg{}, false
		}
		return wire.Msg{}, false
	})
	w := newWaitRun(root, "w1p1")
	code, err := w.run()
	if err != nil {
		t.Fatalf("dm wait: %v", err)
	}
	if code != 0 {
		t.Fatalf("wait returned %d, want 0", code)
	}
	if got, want := w.out.String(), "early line\nearly too\nlate line\n"; got != want {
		t.Fatalf("wait printed %q, want %q: the window once, not twice", got, want)
	}
}

// TestWaitRefusesAMalformedTail proves a tail whose window does not fit the
// lines fails the wait instead of printing whatever slice out of range.
func TestWaitRefusesAMalformedTail(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	startFakePane(t, root, "main", "w1p1", func(c net.Conn, req wire.Msg) (wire.Msg, bool) {
		tail := wire.Msg{Type: "resp", ID: req.ID, OK: true,
			Res: json.RawMessage(`{"lines":["one"],"start":0,"rows":5,"anchor":true}`)}
		_ = wire.WriteMsg(c, tail)
		return wire.Msg{}, false
	})
	w := newWaitRun(root, "w1p1")
	if _, err := w.run(); err == nil || !strings.Contains(err.Error(), "tail reply out of range") {
		t.Fatalf("malformed tail = %v, want a range error", err)
	}
}

// TestWaitSurvivesALongStream proves the deadline extends on every frame: the
// total stream runs past --timeout, yet the quiet-idle wait reaches the final
// resp instead of cutting the stream off.
func TestWaitSurvivesALongStream(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	player := waitPlayer(waitStream{final: wire.Msg{OK: true, Exit: exitOf(0), WaitReason: "mark"}})
	streaming := func(c net.Conn, req wire.Msg) (wire.Msg, bool) {
		for i := 0; i < 12; i++ {
			if err := wire.WriteMsg(c, wire.Msg{Type: "bytes", Data: []byte("tick\n")}); err != nil {
				return wire.Msg{}, false
			}
			time.Sleep(25 * time.Millisecond)
		}
		return player(c, req)
	}
	startFakePane(t, root, "main", "w1p1", streaming)
	w := newWaitRun(root, "w1p1", "--timeout", "200ms")
	code, err := w.run()
	if err != nil {
		t.Fatalf("dm wait: %v", err)
	}
	if code != 0 {
		t.Fatalf("a stream that outlasted --timeout returned %d, want 0", code)
	}
}

// TestWaitTimesOutOnSilence proves a quiet socket past --timeout ends the
// wait with 124 and the timeout status line.
func TestWaitTimesOutOnSilence(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	startFakePane(t, root, "main", "w1p1", waitPlayer(waitStream{holding: 250 * time.Millisecond}))
	w := newWaitRun(root, "w1p1", "--timeout", "30ms")
	code, err := w.run()
	if err != nil {
		t.Fatalf("dm wait: %v", err)
	}
	if code != 124 {
		t.Fatalf("timed-out wait returned %d, want 124", code)
	}
	if got := w.errOut.String(); got != "* timeout\n" {
		t.Fatalf("timed-out wait printed %q, want '* timeout'", got)
	}
}

// TestWaitUnknownExitIsZero proves the fallback endings keep the CLI exit at
// zero: the status line carries the reason, the code carries nothing.
func TestWaitUnknownExitIsZero(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		reason string
		want   string
	}{
		{"children", "* done, exit unknown (children)\n"},
		{"idle", "* done, exit unknown (idle)\n"},
		{"dead", "* dead\n"},
	} {
		tc := tc
		t.Run(tc.reason, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			startFakePane(t, root, "main", "w1p1", waitPlayer(waitStream{
				final: wire.Msg{OK: true, WaitReason: tc.reason},
			}))
			w := newWaitRun(root, "w1p1")
			code, err := w.run()
			if err != nil {
				t.Fatalf("dm wait: %v", err)
			}
			if code != 0 {
				t.Fatalf("reason %q returned %d, want 0", tc.reason, code)
			}
			if got := w.errOut.String(); got != tc.want {
				t.Fatalf("wait printed %q, want %q", got, tc.want)
			}
		})
	}
}

// TestWaitJsonPrintsTheAnswer proves --json swaps the status line for one
// JSON object with the exit, the reason and the timeout flag.
func TestWaitJsonPrintsTheAnswer(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	startFakePane(t, root, "main", "w1p1", waitPlayer(waitStream{
		final: wire.Msg{OK: true, Exit: exitOf(3), WaitReason: "children"},
	}))
	w := newWaitRun(root, "w1p1", "--json")
	code, err := w.run()
	if err != nil {
		t.Fatalf("dm wait --json: %v", err)
	}
	if code != 3 {
		t.Fatalf("wait returned %d, want 3", code)
	}
	if got := w.out.String(); got != `{"exit":3,"reason":"children","timed_out":false}`+"\n" {
		t.Fatalf("--json printed %q, want the wait answer object", got)
	}
}

// TestWaitStatusShapes proves the status line renders each ending: a known
// code, an unknown one, the quiet-idle timeout and a dead pane.
func TestWaitStatusShapes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		res  waitResult
		want string
	}{
		{waitResult{Exit: exitOf(0), Reason: "mark"}, "* done, exit 0"},
		{waitResult{Exit: exitOf(130), Reason: "mark"}, "* done, exit 130"},
		{waitResult{Reason: "children"}, "* done, exit unknown (children)"},
		{waitResult{Reason: "idle"}, "* done, exit unknown (idle)"},
		{waitResult{Reason: "timeout", TimedOut: true}, "* timeout"},
		{waitResult{Reason: "dead"}, "* dead"},
	} {
		if got := waitStatus(tc.res); got != tc.want {
			t.Errorf("waitStatus(%+v) = %q, want %q", tc.res, got, tc.want)
		}
	}
}

// TestWaitExitMapping proves the CLI exit code: the command's code, 124 after
// the timeout, and 0 when no code was observed.
func TestWaitExitMapping(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		exit     *int
		timedOut bool
		want     int
	}{
		{exitOf(0), false, 0},
		{exitOf(3), false, 3},
		{exitOf(130), false, 130},
		{nil, false, 0},
		{nil, true, 124},
		{exitOf(3), true, 124},
	} {
		if got := waitExit(tc.exit, tc.timedOut); got != tc.want {
			t.Errorf("waitExit(%v, %v) = %d, want %d", tc.exit, tc.timedOut, got, tc.want)
		}
	}
}

// TestWaitRefusesASecondWait proves the pane's refusal surfaces as an error,
// so the CLI exits nonzero on a failed wait.
func TestWaitRefusesASecondWait(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	startFakePane(t, root, "main", "w1p1", refuseHandler("a wait is already running"))
	w := newWaitRun(root, "w1p1")
	if _, err := w.run(); err == nil || err.Error() != "a wait is already running" {
		t.Fatalf("dm wait refused = %v, want the pane's refusal", err)
	}
}

// TestWaitWithoutASocket proves a missing pane socket refuses cleanly.
func TestWaitWithoutASocket(t *testing.T) {
	t.Parallel()
	w := newWaitRun(t.TempDir(), "nosuch")
	_, err := w.run()
	if err == nil {
		t.Fatal("dm wait without a pane socket was accepted")
	}
	if !strings.Contains(err.Error(), "nosuch") {
		t.Fatalf("dm wait without a socket = %v, want the pane named", err)
	}
}
