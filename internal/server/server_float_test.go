package server

import (
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"dm/internal/keymap"
	"dm/internal/session"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// paneServer is an in-process server with one pane whose shell stands in dir.
func paneServer(t *testing.T, name, dir string) (*server, string) {
	t.Helper()
	t.Setenv("DM_STATE", t.TempDir())
	st := session.NewState(name)
	id := testutil.RootPane(st)
	st.Panes[id].Cwd = dir
	return railServer(st), id
}

// floatServer is a server with one pane in dir and a config whose diff-command
// is command. The float process is a real /bin/sh.
func floatServer(t *testing.T, name, dir, command string) (*server, string) {
	t.Helper()
	s, id := paneServer(t, name, dir)
	cfg, err := keymap.ParseConfig(`diff-command "` + command + `"`)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	s.cfg = cfg
	return s, id
}

// floatClient is a registered client on a pipe, the way a request arriving
// over the socket sees one.
func floatClient(t *testing.T, s *server) (*client, net.Conn) {
	t.Helper()
	srv, cli := net.Pipe()
	t.Cleanup(func() { _ = cli.Close(); _ = srv.Close() })
	cl := newClient(srv)
	s.mu.Lock()
	s.clients[cl] = true
	s.mu.Unlock()
	t.Cleanup(func() {
		s.mu.Lock()
		delete(s.clients, cl)
		s.mu.Unlock()
		cl.shutdown()
	})
	return cl, cli
}

// readFloatFrame reads frames until want returns true and returns that frame.
// A frame the test does not care about is dropped.
func readFloatFrame(t *testing.T, c net.Conn, want func(wire.Msg) bool) wire.Msg {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		m, err := wire.ReadMsg(c)
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		if want(m) {
			return m
		}
	}
}

// openFloatAt sends float-open for one pane and returns the reply it collected
// on the way to the exit frame.
func openFloatAt(t *testing.T, s *server, cl *client, cli net.Conn, pane, id string) wire.Msg {
	t.Helper()
	s.handleReq(cl, wire.Msg{
		Type: "req", Action: "float-open", ID: 1, Target: pane, Float: id,
		Cols: 80, Rows: 24,
	})
	return readFloatFrame(t, cli, func(m wire.Msg) bool { return m.Type == "resp" })
}

// waitFloatGone waits for a float to leave the server's map.
func waitFloatGone(t *testing.T, s *server, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.floatFor(id) == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("float %q is still registered", id)
}

// waitPidGone waits for a process to be reaped.
func waitPidGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d is still alive", pid)
}

// TestFloatOpenEchoesToOwner: `echo hi` reaches the owning client as bytes and
// one exit frame follows, naming the float.
func TestFloatOpenEchoesToOwner(t *testing.T) {
	dir := t.TempDir()
	s, pane := floatServer(t, "floatecho", dir, "echo hi")
	cl, cli := floatClient(t, s)

	s.handleReq(cl, wire.Msg{
		Type: "req", Action: "float-open", ID: 7, Target: pane, Float: "f1",
		Cols: 80, Rows: 24,
	})
	var reply *wire.Msg
	var out []byte
	readFloatFrame(t, cli, func(m wire.Msg) bool {
		switch m.Type {
		case "resp":
			got := m
			reply = &got
		case "bytes":
			if m.Target != "f1" {
				t.Fatalf("bytes target = %q, want f1", m.Target)
			}
			out = append(out, m.Data...)
		case "exit":
			if m.Target != "f1" {
				t.Fatalf("exit target = %q, want f1", m.Target)
			}
			return true
		}
		return false
	})
	if reply == nil {
		t.Fatal("no reply to float-open")
	}
	if !reply.OK || reply.Action != "float-open" || reply.ID != 7 || reply.Float != "f1" {
		t.Fatalf("reply = %+v, want a good float-open resp for id 7 and float f1", reply)
	}
	if !strings.Contains(string(reply.Res), "echo hi") {
		t.Fatalf("reply res = %s, want the command", reply.Res)
	}
	if !strings.Contains(string(out), "hi") {
		t.Fatalf("output = %q, want the echo", out)
	}
	waitFloatGone(t, s, "f1")
}

// TestFloatOpenNeedsCwd: a pane with no folder is refused with the pane named,
// and no process starts.
func TestFloatOpenNeedsCwd(t *testing.T) {
	s, pane := paneServer(t, "floatnocwd", "")
	cl, cli := floatClient(t, s)
	reply := openFloatAt(t, s, cl, cli, pane, "f1")
	if reply.OK || !strings.Contains(reply.Error, "has no working directory") {
		t.Fatalf("reply = %+v, want a missing working directory error", reply)
	}
	if s.floatFor("f1") != nil {
		t.Fatal("a float started without a working directory")
	}
}

// TestFloatDuplicateId: the client picks the id, so a second float on the same
// id is refused instead of replacing the first.
func TestFloatDuplicateId(t *testing.T) {
	s, pane := floatServer(t, "floatdup", t.TempDir(), "sleep 30")
	cl, cli := floatClient(t, s)
	if reply := openFloatAt(t, s, cl, cli, pane, "f1"); !reply.OK {
		t.Fatalf("first float-open = %+v, want OK", reply)
	}
	t.Cleanup(func() { s.closeFloat("f1") })
	reply := openFloatAt(t, s, cl, cli, pane, "f1")
	if reply.OK || !strings.Contains(reply.Error, "already exists") {
		t.Fatalf("second float-open = %+v, want a duplicate id error", reply)
	}
}

// TestFloatKeysReachChild: a keys frame whose target is a float goes to the
// child's pty rather than to a pane. `cat` echoes it back.
func TestFloatKeysReachChild(t *testing.T) {
	s, pane := floatServer(t, "floatkeys", t.TempDir(), "cat")
	srv, cli := net.Pipe()
	go s.serve(srv)
	t.Cleanup(func() { _ = cli.Close() })

	if err := wire.WriteMsg(cli, wire.Msg{
		Type: "req", Action: "float-open", ID: 1, Target: pane, Float: "f1",
		Cols: 80, Rows: 24,
	}); err != nil {
		t.Fatalf("float-open: %v", err)
	}
	readFloatFrame(t, cli, func(m wire.Msg) bool { return m.Type == "resp" })
	if err := wire.WriteMsg(cli, wire.Msg{Type: "keys", Target: "f1", Keys: []byte("hello\n")}); err != nil {
		t.Fatalf("keys: %v", err)
	}
	var out []byte
	readFloatFrame(t, cli, func(m wire.Msg) bool {
		if m.Type == "bytes" {
			out = append(out, m.Data...)
		}
		return strings.Contains(string(out), "hello")
	})
}

// TestFloatCloseKillsChild: float-close ends the child and the exit frame names
// the float.
func TestFloatCloseKillsChild(t *testing.T) {
	s, pane := floatServer(t, "floatclose", t.TempDir(), "sleep 30")
	cl, cli := floatClient(t, s)
	if reply := openFloatAt(t, s, cl, cli, pane, "f1"); !reply.OK {
		t.Fatalf("float-open = %+v, want OK", reply)
	}
	f := s.floatFor("f1")
	if f == nil {
		t.Fatal("float f1 did not open")
	}
	pid := f.cmd.Process.Pid

	s.handleReq(cl, wire.Msg{Type: "req", Action: "float-close", ID: 2, Float: "f1"})
	var closeReply *wire.Msg
	exit := readFloatFrame(t, cli, func(m wire.Msg) bool {
		switch m.Type {
		case "resp":
			got := m
			closeReply = &got
		case "exit":
			return true
		}
		return false
	})
	if closeReply == nil || !closeReply.OK || closeReply.Float != "f1" {
		t.Fatalf("float-close reply = %+v, want OK for f1", closeReply)
	}
	if exit.Target != "f1" {
		t.Fatalf("exit target = %q, want f1", exit.Target)
	}
	waitFloatGone(t, s, "f1")
	waitPidGone(t, pid)
}

// TestFloatCloseWrongOwner: a client cannot close another client's float, and
// the owner still can.
func TestFloatCloseWrongOwner(t *testing.T) {
	s, pane := floatServer(t, "floatowner", t.TempDir(), "sleep 30")
	owner, ownerConn := floatClient(t, s)
	other, otherConn := floatClient(t, s)
	if reply := openFloatAt(t, s, owner, ownerConn, pane, "f1"); !reply.OK {
		t.Fatalf("float-open = %+v, want OK", reply)
	}
	f := s.floatFor("f1")
	if f == nil {
		t.Fatal("float f1 did not open")
	}
	pid := f.cmd.Process.Pid

	s.handleReq(other, wire.Msg{Type: "req", Action: "float-close", ID: 2, Float: "f1"})
	reply := readFloatFrame(t, otherConn, func(m wire.Msg) bool { return m.Type == "resp" })
	if !reply.OK {
		t.Fatalf("float-close by a stranger = %+v, want OK and no effect", reply)
	}
	if s.floatFor("f1") == nil {
		t.Fatal("a stranger closed the float")
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("child %d died on a stranger's close: %v", pid, err)
	}

	s.handleReq(owner, wire.Msg{Type: "req", Action: "float-close", ID: 3, Float: "f1"})
	readFloatFrame(t, ownerConn, func(m wire.Msg) bool { return m.Type == "exit" })
	waitFloatGone(t, s, "f1")
}

// TestFloatCloseUnknownIsOK: an id the server never saw is not an error.
func TestFloatCloseUnknownIsOK(t *testing.T) {
	s, _ := floatServer(t, "floatnoid", t.TempDir(), "sleep 30")
	cl, cli := floatClient(t, s)
	s.handleReq(cl, wire.Msg{Type: "req", Action: "float-close", ID: 4, Float: "nope"})
	reply := readFloatFrame(t, cli, func(m wire.Msg) bool { return m.Type == "resp" })
	if !reply.OK {
		t.Fatalf("float-close of an unknown id = %+v, want OK", reply)
	}
}

// TestFloatDieWithClient: a client that goes away takes its float child with
// it, and no other client's float is touched.
func TestFloatDieWithClient(t *testing.T) {
	dir := t.TempDir()
	s, pane := floatServer(t, "floatgone", dir, "sleep 30")
	goClient, goConn := floatClient(t, s)
	bystander, bystanderConn := floatClient(t, s)
	if reply := openFloatAt(t, s, goClient, goConn, pane, "mine"); !reply.OK {
		t.Fatalf("float-open = %+v, want OK", reply)
	}
	if reply := openFloatAt(t, s, bystander, bystanderConn, pane, "theirs"); !reply.OK {
		t.Fatalf("float-open = %+v, want OK", reply)
	}
	t.Cleanup(func() { s.closeFloat("theirs") })
	mine := s.floatFor("mine")
	if mine == nil {
		t.Fatal("float mine did not open")
	}
	pid := mine.cmd.Process.Pid

	s.killClientFloats(goClient)
	waitFloatGone(t, s, "mine")
	waitPidGone(t, pid)
	if s.floatFor("theirs") == nil {
		t.Fatal("another client's float was killed")
	}
}
