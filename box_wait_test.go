package main

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"dm/internal/box"
	"dm/internal/session"
	"dm/internal/wire"
)

// boxSession is a session server on a real unix socket, with one pane in the
// tree so a post can name a pane.
func boxSession(t *testing.T) (*server, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DM_STATE", dir)
	st := session.NewState("boxwait")
	rootPane(st)
	s := railServer(st)
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s, sock
}

// boxPeer is one connection to the session socket.
type boxPeer struct {
	t *testing.T
	c net.Conn
}

func dialBox(t *testing.T, sock string) *boxPeer {
	t.Helper()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return &boxPeer{t: t, c: c}
}

func (p *boxPeer) send(action, res string) {
	p.t.Helper()
	if err := wire.WriteMsg(p.c, wire.Msg{Type: "req", Action: action, Res: json.RawMessage(res)}); err != nil {
		p.t.Fatalf("%s send: %v", action, err)
	}
}

func (p *boxPeer) recv(action string) wire.Msg {
	p.t.Helper()
	_ = p.c.SetReadDeadline(time.Now().Add(5 * time.Second))
	m, err := wire.ReadMsg(p.c)
	if err != nil {
		p.t.Fatalf("%s recv: %v", action, err)
	}
	return m
}

func (p *boxPeer) call(action, res string) wire.Msg {
	p.t.Helper()
	p.send(action, res)
	return p.recv(action)
}

// awaitWaiters waits until the server holds want blocked msg-wait calls.
func awaitWaiters(t *testing.T, s *server, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		got := len(s.boxWaiters)
		s.mu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiters = %d, want %d", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// postEntry posts one entry and returns it.
func postEntry(t *testing.T, p *boxPeer, key, state string) box.Entry {
	t.Helper()
	m := p.call("msg-post", `{"key":"`+key+`","state":"`+state+`"}`)
	if !m.OK {
		t.Fatalf("post %s %s: %s", key, state, m.Error)
	}
	var rep box.PostReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("post reply: %v", err)
	}
	return rep.Entry
}

func waitReplyOf(t *testing.T, m wire.Msg) box.Entry {
	t.Helper()
	var rep box.WaitReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("wait reply: %v", err)
	}
	return rep.Entry
}

// TestBoxWaitForDone is the driver and lane case: the driver waits on lane/a
// while a second connection posts blocked, then done.
func TestBoxWaitForDone(t *testing.T) {
	s, sock := boxSession(t)
	driver := dialBox(t, sock)
	lane := dialBox(t, sock)

	driver.send("msg-wait", `{"key":"lane/a","state":"done","timeoutMs":5000}`)
	awaitWaiters(t, s, 1)
	blocked := postEntry(t, lane, "lane/a", "blocked")
	done := postEntry(t, lane, "lane/a", "done")

	got := waitReplyOf(t, driver.recv("msg-wait"))
	if got.Seq != done.Seq || got.State != box.StateDone || got.Key != "lane/a" {
		t.Fatalf("wait returned %+v, want the done entry %d (blocked was %d)", got, done.Seq, blocked.Seq)
	}
	awaitWaiters(t, s, 0)
}

// TestBoxWaitAlreadyDone is the lane that posted done before the driver waited.
func TestBoxWaitAlreadyDone(t *testing.T) {
	s, sock := boxSession(t)
	done := postEntry(t, dialBox(t, sock), "lane/a", "done")

	driver := dialBox(t, sock)
	start := time.Now()
	m := driver.call("msg-wait", `{"key":"lane/a","state":"done","timeoutMs":5000}`)
	if !m.OK {
		t.Fatalf("wait on a done entry: %s", m.Error)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("already done wait took %v, want at once", took)
	}
	if got := waitReplyOf(t, m); got.Seq != done.Seq {
		t.Fatalf("wait returned seq %d, want %d", got.Seq, done.Seq)
	}
	awaitWaiters(t, s, 0)
}

// TestBoxWaitTimeout checks the timeout error and the last entry the wait saw.
func TestBoxWaitTimeout(t *testing.T) {
	s, sock := boxSession(t)
	lane := dialBox(t, sock)
	postEntry(t, lane, "lane/a", "blocked")
	last := postEntry(t, lane, "lane/b", "blocked")

	driver := dialBox(t, sock)
	start := time.Now()
	m := driver.call("msg-wait", `{"key":"lane/b","state":"done","timeoutMs":150}`)
	took := time.Since(start)
	if m.OK {
		t.Fatalf("wait returned %+v, want a timeout", m.Res)
	}
	if m.Error != "box: wait timed out" {
		t.Fatalf("timeout error = %q", m.Error)
	}
	if took < 150*time.Millisecond {
		t.Fatalf("wait gave up after %v, before its deadline", took)
	}
	got := waitReplyOf(t, m)
	if got.Seq != last.Seq || got.State != box.StateBlocked {
		t.Fatalf("timeout carried %+v, want the newest entry %d", got, last.Seq)
	}
	awaitWaiters(t, s, 0)
}

// TestBoxWaitOtherKey checks an entry for another key never satisfies a wait.
func TestBoxWaitOtherKey(t *testing.T) {
	s, sock := boxSession(t)
	lane := dialBox(t, sock)
	postEntry(t, lane, "lane/b", "done")

	driver := dialBox(t, sock)
	m := driver.call("msg-wait", `{"key":"lane/a","state":"done","timeoutMs":150}`)
	if m.OK {
		t.Fatalf("a done entry for lane/b satisfied a wait on lane/a: %+v", m.Res)
	}
	if m.Error != "box: wait timed out" {
		t.Fatalf("error = %q, want the timeout", m.Error)
	}

	// A post for lane/a does satisfy the next wait.
	driver.send("msg-wait", `{"key":"lane/a","state":"done","timeoutMs":5000}`)
	awaitWaiters(t, s, 1)
	want := postEntry(t, lane, "lane/a", "done")
	if got := waitReplyOf(t, driver.recv("msg-wait")); got.Seq != want.Seq {
		t.Fatalf("wait returned seq %d, want %d", got.Seq, want.Seq)
	}
	awaitWaiters(t, s, 0)
}

// TestBoxWaitDisconnectReleases closes the waiting client and checks the
// waiter does not outlive it.
func TestBoxWaitDisconnectReleases(t *testing.T) {
	s, sock := boxSession(t)
	driver := dialBox(t, sock)
	driver.send("msg-wait", `{"key":"lane/a","state":"done","timeoutMs":60000}`)
	awaitWaiters(t, s, 1)

	if err := driver.c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	awaitWaiters(t, s, 0)
}

// TestBoxWaitShellInPane drives the pair as intended: the shell in a pane posts
// its state with its own pane id, and the driver waits for the match, both over
// the session socket.
func TestBoxWaitShellInPane(t *testing.T) {
	s, sock := boxSession(t)
	s.mu.Lock()
	pane := s.st.ActiveWindow().Focus
	s.mu.Unlock()
	if pane == "" {
		t.Fatal("the session has no pane")
	}

	driver := dialBox(t, sock)
	shell := dialBox(t, sock)
	driver.send("msg-wait", `{"key":"lane/a","state":"done","timeoutMs":5000}`)
	awaitWaiters(t, s, 1)

	from := `,"from":"` + pane + `"}`
	m := shell.call("msg-post", `{"key":"lane/a","state":"blocked","msg":"gate red"`+from)
	if !m.OK {
		t.Fatalf("shell post blocked: %s", m.Error)
	}
	m = shell.call("msg-post", `{"key":"lane/a","state":"done","msg":"gate green"`+from)
	if !m.OK {
		t.Fatalf("shell post done: %s", m.Error)
	}

	got := waitReplyOf(t, driver.recv("msg-wait"))
	if got.State != box.StateDone || got.From != pane || got.Msg != "gate green" || got.Key != "lane/a" {
		t.Fatalf("driver got %+v, want done from %s", got, pane)
	}
	awaitWaiters(t, s, 0)
}
