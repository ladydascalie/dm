package pane

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dm/internal/vt"
	"dm/internal/wire"
)

// paneReply sends one request and reads its reply of the given type, such as
// the screen and reflow actions, which answer without an ID.
func paneReply(t *testing.T, cli net.Conn, msgs chan wire.Msg, m wire.Msg, typ string) wire.Msg {
	t.Helper()
	paneSend(t, cli, m)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case reply, ok := <-msgs:
			if !ok {
				t.Fatalf("socket closed before the %s reply", typ)
			}
			if reply.Type == typ {
				return reply
			}
		case <-deadline:
			t.Fatalf("no %s reply", typ)
		}
	}
}

// feedPane writes a stream to the pane the way the read loop does: pty-sized
// chunks through publishLocked, so the screen, the ring and the subscribers
// all see it.
func feedPane(p *paneProc, stream []byte) {
	const chunk = 32 << 10
	for len(stream) > 0 {
		n := min(len(stream), chunk)
		p.mu.Lock()
		p.publishLocked(stream[:n])
		p.mu.Unlock()
		stream = stream[n:]
	}
}

// subscriber attaches one connection to the pane and pumps everything the
// pane sends into a channel, so a test can read the broadcasts a method sends
// while the method holds the pane's lock.
func subscriber(p *paneProc) chan wire.Msg {
	serve, cli := net.Pipe()
	sc := &subConn{c: serve}
	p.mu.Lock()
	p.subs[sc] = true
	p.mu.Unlock()
	msgs := make(chan wire.Msg, 64)
	go func() {
		for {
			m, err := wire.ReadMsg(cli)
			if err != nil {
				close(msgs)
				return
			}
			msgs <- m
		}
	}()
	return msgs
}

// readBroadcast waits for one broadcast.
func readBroadcast(t *testing.T, msgs chan wire.Msg) wire.Msg {
	t.Helper()
	select {
	case m, ok := <-msgs:
		if !ok {
			t.Fatal("the subscriber connection closed before a broadcast")
		}
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no broadcast arrived")
		return wire.Msg{}
	}
}

// readNothing proves no broadcast arrives within a short window.
func readNothing(t *testing.T, msgs chan wire.Msg) {
	t.Helper()
	select {
	case m := <-msgs:
		t.Fatalf("unexpected broadcast %+v", m)
	case <-time.After(150 * time.Millisecond):
	}
}

// ptyPipe is a pipe standing in for the pane's pty: writes land on w, the
// pane's read loop reads from r.
func ptyPipe(t *testing.T) (r, w *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})
	return r, w
}

// sleepingShell starts a shell that outlives the test unless it is killed, so
// a test can watch its cwd or hup it.
func sleepingShell(t *testing.T, script string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd
}

// waitFor polls until cond holds or the deadline passes.
func waitFor(t *testing.T, d time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s never happened within %s", what, d)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRunRefusesMissingIdentity proves Run validates the pane's identity
// before it touches a pty or a socket.
func TestRunRefusesMissingIdentity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no id", []string{"--session", "s1"}, "pane needs --id and --session"},
		{"no session", []string{"--id", "p1"}, "pane needs --id and --session"},
		{"no flags", nil, "pane needs --id and --session"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := Run(tc.args)
			if err == nil {
				t.Fatalf("Run(%v) started without an identity", tc.args)
			}
			if err.Error() != tc.want {
				t.Fatalf("error = %q, want %q", err.Error(), tc.want)
			}
		})
	}
}

// TestRunDefaultsResolveToABadShell proves Run resolves the shell from $SHELL
// and the cwd from the process's directory: the defaults reach the command it
// tries to start, and a shell that does not exist stops Run with the start
// error instead of half a daemon.
//
// Serial, not parallel: this test verifies env behaviour, and t.Setenv cannot
// run alongside other tests because it mutates the process environment.
func TestRunDefaultsResolveToABadShell(t *testing.T) {
	t.Setenv("SHELL", "/nonexistent-dm-test-shell")
	err := Run([]string{"--id", "p1", "--session", "s1"})
	if err == nil {
		t.Fatal("Run started with a shell that does not exist")
	}
	if !strings.Contains(err.Error(), "start shell") {
		t.Fatalf("error = %q, want the start shell failure", err.Error())
	}
}

// TestRunRejectsBadFlags proves an unknown flag is an argument error.
func TestRunRejectsBadFlags(t *testing.T) {
	t.Parallel()
	if err := Run([]string{"--definitely-not-a-flag"}); err == nil {
		t.Fatal("Run accepted an unknown flag")
	}
}

// TestReadLoopPublishesAndReportsExit proves the pane's read loop turns the
// pty's bytes into broadcasts, and the pty's end into a dead pane.
func TestReadLoopPublishesAndReportsExit(t *testing.T) {
	t.Parallel()
	r, w := ptyPipe(t)
	p := &paneProc{
		id: "w1p1", vt: vt.NewTerm(20, 5), ring: NewRing(ringDefault),
		subs: map[*subConn]bool{}, st: wire.Status{State: "idle"}, ptmx: r,
	}
	msgs := subscriber(p)
	go p.readLoop()

	if _, err := w.Write([]byte("hello\r\n")); err != nil {
		t.Fatal(err)
	}
	var m wire.Msg
	for {
		m = readBroadcast(t, msgs)
		if m.Type != "mode" {
			break
		}
	}
	if m.Type != "bytes" || string(m.Data) != "hello\r\n" {
		t.Fatalf("broadcast = %+v, want the bytes hello\\r\\n", m)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for {
		m = readBroadcast(t, msgs)
		if m.Type != "mode" {
			break
		}
	}
	if m.Type != "exit" {
		t.Fatalf("broadcast after the pty died = %+v, want exit", m)
	}
	waitFor(t, 2*time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.dead
	}, "the pane's dead flag")
}

// TestReplyTerminalWritesIntoThePty proves a terminal reply travels to the
// application through the pty's input side.
func TestReplyTerminalWritesIntoThePty(t *testing.T) {
	t.Parallel()
	r, w := ptyPipe(t)
	p := &paneProc{ptmx: w}
	p.replyTerminal("\x1b[?25h")
	_ = r.SetReadDeadline(time.Now().Add(5 * time.Second))
	got := make([]byte, 6)
	n, err := io.ReadFull(r, got)
	if err != nil {
		t.Fatalf("read the reply: %v (got %d bytes)", err, n)
	}
	if string(got) != "\x1b[?25h" {
		t.Fatalf("pty received %q, want the reply", got)
	}
}

// TestReplyQueryAnswersTheProbe proves the OSC 7501 probe is echoed back
// into the pty, which is the channel the asking program reads.
func TestReplyQueryAnswersTheProbe(t *testing.T) {
	t.Parallel()
	r, w := ptyPipe(t)
	p := &paneProc{ptmx: w}
	p.replyQuery()
	_ = r.SetReadDeadline(time.Now().Add(5 * time.Second))
	got := make([]byte, 10)
	if _, err := io.ReadFull(r, got); err != nil {
		t.Fatalf("read the probe reply: %v", err)
	}
	if string(got) != "\x1b]7501;?\x1b\\" {
		t.Fatalf("pty received %q, want the probe echo", got)
	}
}

// TestCursorPosReportsTheScreenCursor proves the zero-based cursor the pane
// reports is the one on its screen.
func TestCursorPosReportsTheScreenCursor(t *testing.T) {
	t.Parallel()
	p := &paneProc{vt: vt.NewTerm(20, 5), ring: NewRing(ringDefault), subs: map[*subConn]bool{}}
	p.vt.Write([]byte("hello\r\nworld"))
	p.vt.Flush()
	row, col := p.cursorPos()
	if row != 1 || col != 5 {
		t.Fatalf("cursorPos = (%d, %d), want (1, 5)", row, col)
	}
}

// TestSetStatusBroadcastsChangeOnly proves a pane fans a status out once: a
// sample that repeats the state sends nothing.
func TestSetStatusBroadcastsChangeOnly(t *testing.T) {
	t.Parallel()
	p := &paneProc{vt: vt.NewTerm(20, 5), ring: NewRing(ringDefault), subs: map[*subConn]bool{}}
	msgs := subscriber(p)

	p.setStatus(wire.Status{State: "working", Message: "building"})
	m := readBroadcast(t, msgs)
	if m.Type != "status" || m.St == nil || m.St.State != "working" {
		t.Fatalf("broadcast = %+v, want the working status", m)
	}

	p.setStatus(wire.Status{State: "working", Message: "building"})
	readNothing(t, msgs)
}

// TestSampleCwdReportsMoves proves a pane reports the shell's directory when
// it moved and nothing when it stands still or the shell is gone.
func TestSampleCwdReportsMoves(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := sleepingShell(t, "cd "+want+"; sleep 30")
	waitFor(t, 5*time.Second, func() bool { return shellCwd(cmd.Process.Pid) == want },
		"the shell to reach its directory")

	p := &paneProc{vt: vt.NewTerm(20, 5), ring: NewRing(ringDefault), subs: map[*subConn]bool{}}
	msgs := subscriber(p)

	p.sampleCwd(cmd.Process.Pid)
	m := readBroadcast(t, msgs)
	if m.Type != "cwd" || m.Cwd != want {
		t.Fatalf("broadcast = %+v, want cwd %q", m, want)
	}

	// The shell has not moved: a second sample sends nothing.
	p.sampleCwd(cmd.Process.Pid)
	readNothing(t, msgs)

	// A pid with no directory reports nothing.
	p.sampleCwd(-1)
	readNothing(t, msgs)
}

// TestSampleTitleReportsChanges proves a pane reports the window title when
// it changed and nothing when the program never set one.
func TestSampleTitleReportsChanges(t *testing.T) {
	t.Parallel()
	p := &paneProc{vt: vt.NewTerm(20, 5), ring: NewRing(ringDefault), subs: map[*subConn]bool{}}
	msgs := subscriber(p)

	// A program that never set a title reports nothing.
	p.sampleTitle()
	readNothing(t, msgs)

	p.vt.Write([]byte("\x1b]2;ed: main.go\x07"))
	p.vt.Flush()
	p.sampleTitle()
	m := readBroadcast(t, msgs)
	if m.Type != "title" || !strings.Contains(string(m.Res), "ed: main.go") {
		t.Fatalf("broadcast = %+v, want the title", m)
	}

	// The title has not moved: a second sample sends nothing.
	p.sampleTitle()
	readNothing(t, msgs)
}

// TestWatchShellSamplesTheCwd proves the watcher reports the shell's
// directory on its own, without a request.
func TestWatchShellSamplesTheCwd(t *testing.T) {
	t.Parallel()
	cmd := sleepingShell(t, "sleep 30")
	p := &paneProc{
		id: "w1p1", vt: vt.NewTerm(20, 5), ring: NewRing(ringDefault),
		subs: map[*subConn]bool{}, st: wire.Status{State: "idle"}, cmd: cmd,
	}
	msgs := subscriber(p)
	go p.watchShell()

	m := readBroadcast(t, msgs)
	if m.Type != "cwd" || m.Cwd == "" {
		t.Fatalf("broadcast = %+v, want the shell's cwd", m)
	}
}

// TestWatchShellWithoutAShellReturns proves a pane with no command does not
// watch anything.
func TestWatchShellWithoutAShellReturns(t *testing.T) {
	t.Parallel()
	p := &paneProc{subs: map[*subConn]bool{}}
	done := make(chan struct{})
	go func() { p.watchShell(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchShell did not return without a shell")
	}
}

// TestShutdownClosesThePtyAndHupsTheShell proves a pane's shutdown closes
// the pty, so attached readers see the end, and hangs the shell up.
func TestShutdownClosesThePtyAndHupsTheShell(t *testing.T) {
	t.Parallel()
	r, w := ptyPipe(t)
	cmd := sleepingShell(t, "sleep 30")
	p := &paneProc{vt: vt.NewTerm(20, 5), ring: NewRing(ringDefault), ptmx: w, cmd: cmd}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	p.shutdown()

	_ = r.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("read after shutdown = %v, want EOF", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the shell outlived the hangup")
	}
}

// TestWritePIDRecordsTheDaemon proves the pid file names this process.
func TestWritePIDRecordsTheDaemon(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pane.pid")
	writePID(path)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("%d\n", os.Getpid()); string(b) != want {
		t.Fatalf("pid file = %q, want %q", b, want)
	}
}

// TestSplitLinesTrimsAndDropsTrailingBlanks proves the read reply keeps every
// row with content, trims the padding and drops the trailing blank rows.
func TestSplitLines(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want []string
	}{
		{"one\ntwo\n", []string{"one", "two"}},
		{"one  \x00\x00\ntwo", []string{"one", "two"}},
		{"a\n\n\n", []string{"a"}},
		{"", nil},
		{"   ", []string{}},
	}
	for _, tc := range cases {
		got := splitLines(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("splitLines(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("splitLines(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

// TestReadActionServesTheScreen proves the read action answers with the
// visible rows, the pane's status and whether the shell has died.
func TestReadActionServesTheScreen(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 20, 5)
	p.mu.Lock()
	p.publishLocked([]byte("one\r\ntwo\r\n"))
	p.mu.Unlock()

	reply := paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "read", ID: 1})
	if !reply.OK {
		t.Fatal("read reply was not ok")
	}
	if want := []string{"one", "two"}; !equalLines(reply.Lines, want) {
		t.Fatalf("read lines = %#v, want %#v", reply.Lines, want)
	}
	if reply.St == nil || reply.St.State != "idle" {
		t.Fatalf("read status = %+v, want idle", reply.St)
	}
	var res struct {
		Title string `json:"title"`
		Dead  bool   `json:"dead"`
	}
	if err := json.Unmarshal(reply.Res, &res); err != nil {
		t.Fatalf("decode read reply: %v", err)
	}
	if res.Title != "" || res.Dead {
		t.Fatalf("read reply = %+v, want an empty title and a live pane", res)
	}

	// A dead pane says so in the same reply shape.
	p.mu.Lock()
	p.dead = true
	p.mu.Unlock()
	reply = paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "read", ID: 2})
	if err := json.Unmarshal(reply.Res, &res); err != nil {
		t.Fatalf("decode read reply: %v", err)
	}
	if !res.Dead {
		t.Fatal("read reply did not report the dead pane")
	}
}

// TestStatusAndCwdActionsServeState proves the status and cwd actions answer
// with the pane's sampled state.
func TestStatusAndCwdActionsServeState(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 20, 5)

	reply := paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "status", ID: 1})
	if !reply.OK || reply.St == nil || reply.St.State != "idle" {
		t.Fatalf("status reply = %+v, want ok and idle", reply)
	}

	p.mu.Lock()
	p.cwd = "/tmp/project"
	p.mu.Unlock()
	reply = paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "cwd", ID: 2})
	if !reply.OK || reply.Cwd != "/tmp/project" {
		t.Fatalf("cwd reply = %+v, want /tmp/project", reply)
	}
}

// TestResizeRejectsTinyGeometry proves a size below two columns or rows is
// refused and leaves the screen as it was.
func TestResizeRejectsTinyGeometry(t *testing.T) {
	t.Parallel()
	_, cli, msgs := yankPane(t, 20, 5)
	paneSend(t, cli, wire.Msg{Type: "req", Action: "resize", Cols: 1, Rows: 0})
	paneSync(t, cli, msgs)

	reply := paneReply(t, cli, msgs, wire.Msg{Type: "req", Action: "screen", ID: 1}, "screen")
	if reply.VTSnap == nil {
		t.Fatal("screen reply carried no snapshot")
	}
	if reply.VTSnap.Cols != 20 || reply.VTSnap.Rows != 5 {
		t.Fatalf("size after a refused resize %dx%d, want 20x5",
			reply.VTSnap.Cols, reply.VTSnap.Rows)
	}
}

// TestResizeAppliesTheGeometry proves a real resize changes the screen the
// next read and snapshot report.
func TestResizeAppliesTheGeometry(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 20, 5)
	p.mu.Lock()
	p.publishLocked([]byte("hello\r\nworld\r\n"))
	p.mu.Unlock()

	paneSend(t, cli, wire.Msg{Type: "req", Action: "resize", Cols: 10, Rows: 4})
	paneSync(t, cli, msgs)

	reply := paneReply(t, cli, msgs, wire.Msg{Type: "req", Action: "screen", ID: 1}, "screen")
	if reply.VTSnap.Cols != 10 || reply.VTSnap.Rows != 4 {
		t.Fatalf("size after resize %dx%d, want 10x4", reply.VTSnap.Cols, reply.VTSnap.Rows)
	}
}

// TestBulkPasteClearsTheEcho proves a write longer than echoMax is a pasted
// block, not a command: its echo is dropped, and the next yank keeps the
// output the previous command's echo would have masked.
func TestBulkPasteClearsTheEcho(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 20, 5)
	p.mu.Lock()
	p.publishLocked([]byte("old\r\n"))
	p.mu.Unlock()

	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Msg: "short", Enter: true, ID: 1})
	paneSync(t, cli, msgs)
	bulk := strings.Repeat("x", echoMax+1)
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Msg: bulk, Enter: true, ID: 2})
	paneSync(t, cli, msgs)

	p.mu.Lock()
	p.publishLocked([]byte("short\r\nout\r\n"))
	p.mu.Unlock()
	res := decodeYank(t, paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "yank", ID: 3}))
	if want := []string{"short", "out"}; !equalLines(yankCopy(res), want) {
		t.Fatalf("yank copy = %#v, want %#v", yankCopy(res), want)
	}
}

// TestYankStartsAtTheOldestHeldByteWhenTheRingWrapped proves a yank whose
// anchor fell out of the ring copies from the oldest history the pane still
// holds, not nothing and not a panic.
func TestYankStartsAtTheOldestHeldByteWhenTheRingWrapped(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 80, 24)
	stream := ringStream(t, ringDefault+ringDefault/2)
	feedPane(p, stream)
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Keys: []byte("\r"), ID: 1})
	paneSync(t, cli, msgs)
	after := stream[len(stream)-9*3 : len(stream)-9*3+8]
	feedPane(p, []byte("\r\n"+string(after)))

	res := decodeYank(t, paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "yank", ID: 2}))
	if !res.Anchor {
		t.Fatal("yank lost the anchor across a ring wrap")
	}
	if res.Rows != 24 {
		t.Fatalf("yank rows = %d, want the 24 screen rows", res.Rows)
	}
	if last := lastNonBlank(res.Lines); last != string(after) {
		t.Fatalf("yank ended on %q, want the newest record %q", last, string(after))
	}
}

// TestYankCutsAtThePromptMarkWithoutACommandEnd proves an unmarked pane still
// ends its copy at the prompt-start mark, so the prompt is not copied.
func TestYankCutsAtThePromptMarkWithoutACommandEnd(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 20, 5)
	p.mu.Lock()
	p.publishLocked([]byte("old\r\n"))
	p.mu.Unlock()
	paneSend(t, cli, wire.Msg{Type: "req", Action: "write", Keys: []byte("\r"), ID: 1})
	paneSync(t, cli, msgs)

	p.mu.Lock()
	p.publishLocked([]byte("\r\nthree\r\n\x1b]133;A\x1b\\prompt$ "))
	p.mu.Unlock()
	res := decodeYank(t, paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "yank", ID: 2}))
	if want := []string{"three"}; !equalLines(yankCopy(res), want) {
		t.Fatalf("yank copy = %#v, want %#v", yankCopy(res), want)
	}
}

// TestRingSliceAndIndexByteEdges proves the ring's slice and indexByte hold
// at the edges: clamped ranges, empty ranges and a slice across the wrap.
func TestRingSliceAndIndexByteEdges(t *testing.T) {
	t.Parallel()
	r := NewRing(ringDefault)
	r.Write([]byte("hello world"))

	if i := r.indexByte('w', 0); i != 6 {
		t.Fatalf("indexByte('w') = %d, want 6", i)
	}
	if i := r.indexByte('z', 0); i != -1 {
		t.Fatalf("indexByte('z') = %d, want -1", i)
	}
	if i := r.indexByte('h', r.Len()); i != -1 {
		t.Fatalf("indexByte from the end = %d, want -1", i)
	}

	if got := string(r.slice(6, 11)); got != "world" {
		t.Fatalf("slice(6,11) = %q, want world", got)
	}
	if got := string(r.slice(-5, 3)); got != "hel" {
		t.Fatalf("slice with a negative start = %q, want hel", got)
	}
	if got := string(r.slice(0, 99)); got != "hello world" {
		t.Fatalf("slice past the end = %q, want the whole ring", got)
	}
	if got := r.slice(5, 5); len(got) != 0 {
		t.Fatalf("empty range = %q, want empty", got)
	}
	if got := r.slice(7, 2); len(got) != 0 {
		t.Fatalf("reversed range = %q, want empty", got)
	}

	// A slice across the buffer end comes back oldest first.
	wrapped := NewRing(ringDefault)
	wrapped.Write(ringStream(t, ringDefault-3))
	wrapped.Write([]byte("0123456789"))
	if got := wrapped.slice(0, wrapped.Len()); !bytes.Equal(got, wrapped.Bytes()) {
		t.Fatal("a slice across the wrap differs from Bytes()")
	}
	if i := wrapped.indexByte('9', 0); i < 0 {
		t.Fatal("indexByte lost the wrapped tail")
	}
}

// equalLines compares two line slices.
func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// lastNonBlank is the last line of the reply with content.
func lastNonBlank(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}
