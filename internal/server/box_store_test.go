package server

import (
	"bytes"
	"encoding/json"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dm/internal/box"
	"dm/internal/layout"
	"dm/internal/paths"
	"dm/internal/session"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// boxServer is an in-process server over a temp state dir with one pane in its
// tree, so a post can name a pane that exists.
func boxServer(t *testing.T) *server {
	t.Helper()
	t.Setenv("DM_STATE", t.TempDir())
	st := session.NewState("boxstore")
	testutil.RootPane(st)
	s := railServer(st)
	// The registry is built with the server, never loaded from state.
	s.watches = map[boxWatchKey]box.Watch{}
	return s
}

// boxConn is a client whose replies land on a pipe the test reads.
func boxConn(t *testing.T) (*client, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() {
		_ = a.Close()
		_ = b.Close()
	})
	return &client{c: a, cols: 80, rows: 24}, b
}

// boxCall sends one box request and returns its reply.
func boxCall(t *testing.T, s *server, cl *client, conn net.Conn, action, res string) wire.Msg {
	t.Helper()
	s.handleReq(cl, wire.Msg{Type: "req", Action: action, Res: json.RawMessage(res)})
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	m, err := wire.ReadMsg(conn)
	if err != nil {
		t.Fatalf("%s reply: %v", action, err)
	}
	return m
}

// boxPost posts one entry and returns it, failing the test on any error.
func boxPost(t *testing.T, s *server, cl *client, conn net.Conn, key, state, msg string) box.Entry {
	t.Helper()
	res := `{"key":"` + key + `","state":"` + state + `","msg":"` + msg + `"}`
	m := boxCall(t, s, cl, conn, "msg-post", res)
	if !m.OK {
		t.Fatalf("post %s: %s", res, m.Error)
	}
	var rep box.PostReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("post reply: %v", err)
	}
	return rep.Entry
}

func TestBoxSeqStartsAtOneAndNeverRepeats(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)

	first := boxPost(t, s, cl, conn, "lane/a", "working", "first")
	second := boxPost(t, s, cl, conn, "lane/b", "note", "second")
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("seqs = %d, %d, want 1, 2", first.Seq, second.Seq)
	}
	if first.At.IsZero() || !first.At.Equal(first.At.Truncate(time.Second)) || first.At.Location() != time.UTC {
		t.Fatalf("at = %v, want a whole second in UTC", first.At)
	}

	m := boxCall(t, s, cl, conn, "msg-clear", `{"key":"lane/a"}`)
	if !m.OK {
		t.Fatalf("clear: %s", m.Error)
	}
	var cleared box.ClearReply
	if err := json.Unmarshal(m.Res, &cleared); err != nil || cleared.Cleared != 1 {
		t.Fatalf("clear reply = %s (%v)", m.Res, err)
	}

	third := boxPost(t, s, cl, conn, "lane/c", "done", "third")
	if third.Seq != 3 {
		t.Fatalf("post after a clear got seq %d, want 3", third.Seq)
	}

	m = boxCall(t, s, cl, conn, "msg-clear", `{}`)
	if !m.OK {
		t.Fatalf("clear all: %s", m.Error)
	}
	if err := json.Unmarshal(m.Res, &cleared); err != nil || cleared.Cleared != 2 {
		t.Fatalf("clear all reply = %s (%v), want 2", m.Res, err)
	}
	fourth := boxPost(t, s, cl, conn, "lane/d", "blocked", "fourth")
	if fourth.Seq != 4 {
		t.Fatalf("post after a clear all got seq %d, want 4", fourth.Seq)
	}
}

func TestBoxRestartKeepsEveryEntryInOrder(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)
	posted := []box.Entry{
		boxPost(t, s, cl, conn, "lane/a", "working", "first"),
		boxPost(t, s, cl, conn, "lane/b", "blocked", "second"),
		boxPost(t, s, cl, conn, "lane/a", "done", "third"),
	}

	st, err := session.LoadState("boxstore")
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if len(st.Box) != len(posted) {
		t.Fatalf("reloaded %d entries, want %d", len(st.Box), len(posted))
	}
	for i, want := range posted {
		got := st.Box[i]
		if got.Seq != want.Seq || got.From != want.From || got.Key != want.Key ||
			got.State != want.State || got.Msg != want.Msg || !got.At.Equal(want.At) {
			t.Fatalf("entry %d after restart = %+v, want %+v", i, got, want)
		}
	}
	if st.BoxSeq != posted[len(posted)-1].Seq {
		t.Fatalf("reloaded BoxSeq = %d, want %d", st.BoxSeq, posted[len(posted)-1].Seq)
	}
}

func TestBoxEntryCapDropsOldest(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)

	at := time.Now().UTC().Truncate(time.Second)
	s.mu.Lock()
	for i := 1; i <= box.MaxEntries; i++ {
		s.st.Box = append(s.st.Box, box.Entry{Seq: i, At: at, Key: "lane/a", State: box.StateNote, Msg: "filler"})
	}
	s.st.BoxSeq = box.MaxEntries
	s.mu.Unlock()

	newest := boxPost(t, s, cl, conn, "lane/b", "done", "newest")
	if newest.Seq != box.MaxEntries+1 {
		t.Fatalf("seq after a full box = %d, want %d", newest.Seq, box.MaxEntries+1)
	}
	s.mu.Lock()
	kept := append([]box.Entry(nil), s.st.Box...)
	s.mu.Unlock()
	if len(kept) != box.MaxEntries {
		t.Fatalf("box holds %d entries, want %d", len(kept), box.MaxEntries)
	}
	if kept[0].Seq != 2 || kept[len(kept)-1].Seq != newest.Seq {
		t.Fatalf("box keeps seq %d to %d, want 2 to %d", kept[0].Seq, kept[len(kept)-1].Seq, newest.Seq)
	}
	for i := 1; i < len(kept); i++ {
		if kept[i].Seq != kept[i-1].Seq+1 {
			t.Fatalf("box order breaks at %d: %d then %d", i, kept[i-1].Seq, kept[i].Seq)
		}
	}
}

func TestBoxByteCapDropsOldest(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)

	at := time.Now().UTC().Truncate(time.Second)
	big := strings.Repeat("x", box.MaxMsg)
	s.mu.Lock()
	seq := 0
	for boxJSONBytes(s.st.Box) < box.MaxBytes {
		seq++
		s.st.Box = append(s.st.Box, box.Entry{Seq: seq, At: at, Key: "lane/a", State: box.StateNote, Msg: big})
	}
	s.st.BoxSeq = seq
	oldest := s.st.Box[0].Seq
	s.mu.Unlock()

	newest := boxPost(t, s, cl, conn, "lane/b", "done", big)
	s.mu.Lock()
	kept := append([]box.Entry(nil), s.st.Box...)
	size := boxJSONBytes(kept)
	s.mu.Unlock()
	if size > box.MaxBytes {
		t.Fatalf("box is %d bytes, cap is %d", size, box.MaxBytes)
	}
	if kept[0].Seq <= oldest {
		t.Fatalf("oldest kept seq = %d, want past %d", kept[0].Seq, oldest)
	}
	if kept[len(kept)-1].Seq != newest.Seq {
		t.Fatalf("newest kept seq = %d, want %d", kept[len(kept)-1].Seq, newest.Seq)
	}
}

// boxJSONBytes is the JSON size of a box, the measure trimBox uses.
func boxJSONBytes(kept []box.Entry) int {
	b, err := json.Marshal(kept)
	if err != nil {
		return 0
	}
	return len(b)
}

func TestBoxClearCounts(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)
	boxPost(t, s, cl, conn, "lane/a", "working", "one")
	boxPost(t, s, cl, conn, "lane/a", "done", "two")
	boxPost(t, s, cl, conn, "lane/b", "blocked", "three")

	clear := func(res string) int {
		t.Helper()
		m := boxCall(t, s, cl, conn, "msg-clear", res)
		if !m.OK {
			t.Fatalf("clear %s: %s", res, m.Error)
		}
		var rep box.ClearReply
		if err := json.Unmarshal(m.Res, &rep); err != nil {
			t.Fatalf("clear reply: %v", err)
		}
		return rep.Cleared
	}
	if n := clear(`{"key":"lane/a"}`); n != 2 {
		t.Fatalf("clear lane/a = %d, want 2", n)
	}
	if n := clear(`{"key":"lane/zzz"}`); n != 0 {
		t.Fatalf("clear an absent key = %d, want 0", n)
	}
	if n := clear(`{}`); n != 1 {
		t.Fatalf("clear all = %d, want 1", n)
	}
	s.mu.Lock()
	left := len(s.st.Box)
	s.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d entries left after clear all", left)
	}
}

func TestBoxListNewestFirstAndFiltered(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)
	boxPost(t, s, cl, conn, "lane/a", "note", "one")
	boxPost(t, s, cl, conn, "lane/b", "working", "two")
	boxPost(t, s, cl, conn, "lane/a", "done", "three")
	boxPost(t, s, cl, conn, "lane/b", "blocked", "fourth")

	list := func(res string) box.ListReply {
		t.Helper()
		m := boxCall(t, s, cl, conn, "msg-list", res)
		if !m.OK {
			t.Fatalf("list %s: %s", res, m.Error)
		}
		var rep box.ListReply
		if err := json.Unmarshal(m.Res, &rep); err != nil {
			t.Fatalf("list reply: %v", err)
		}
		return rep
	}
	seqs := func(es []box.Entry) []int {
		out := make([]int, len(es))
		for i, e := range es {
			out[i] = e.Seq
		}
		return out
	}
	cases := []struct {
		res  string
		want []int
	}{
		{`{}`, []int{4, 3, 2, 1}},
		{`{"key":"lane/a"}`, []int{3, 1}},
		{`{"key":"lane/b"}`, []int{4, 2}},
		{`{"limit":2}`, []int{4, 3}},
		{`{"key":"lane/a","limit":1}`, []int{3}},
		{`{"key":"lane/zzz"}`, nil},
		{`{"keyPrefix":"lane/"}`, []int{4, 3, 2, 1}},
		{`{"keyPrefix":"lane/a"}`, []int{3, 1}},
		{`{"afterSeq":2}`, []int{4, 3}},
		{`{"afterSeq":2,"keyPrefix":"lane/a"}`, []int{3}},
		{`{"afterSeq":4}`, nil},
		{`{"afterSeq":0,"limit":1}`, []int{4}},
	}
	for _, c := range cases {
		got := seqs(list(c.res).Entries)
		if len(got) != len(c.want) {
			t.Fatalf("list %s = %v, want %v", c.res, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("list %s = %v, want %v", c.res, got, c.want)
			}
		}
	}
	// An empty list is [], not null: the frozen reply carries the field.
	m := boxCall(t, s, cl, conn, "msg-list", `{"key":"lane/zzz"}`)
	if !strings.Contains(string(m.Res), `"entries":[]`) {
		t.Fatalf("empty list reply = %s", m.Res)
	}
}

// TestBoxPubSubRead proves the cursor read: entries come back in order, once
// each, so a subscriber sees the whole topic stream.
func TestBoxPubSubRead(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)
	boxPost(t, s, cl, conn, "msg/broadcast", "note", "read tasklist.md")
	boxPost(t, s, cl, conn, "msg/broadcast", "working", "backend started")
	boxPost(t, s, cl, conn, "msg/frontend", "done", "frontend green")

	wait := func(res string) box.WaitReply {
		t.Helper()
		m := boxCall(t, s, cl, conn, "msg-wait", res)
		if !m.OK {
			t.Fatalf("wait %s: %s", res, m.Error)
		}
		var rep box.WaitReply
		if err := json.Unmarshal(m.Res, &rep); err != nil {
			t.Fatalf("wait reply: %v", err)
		}
		return rep
	}

	// A cursor returns the oldest unseen entry, not the newest: the first
	// message on the branch survives the second.
	if got := wait(`{"keyPrefix":"msg/","afterSeq":0,"timeoutMs":1000}`); got.Entry.Seq != 1 || got.Entry.Msg != "read tasklist.md" {
		t.Fatalf("first stream read = %+v, want seq 1", got.Entry)
	}
	if got := wait(`{"keyPrefix":"msg/","afterSeq":1,"timeoutMs":1000}`); got.Entry.Seq != 2 {
		t.Fatalf("second stream read = %+v, want seq 2", got.Entry)
	}
	// The state filter skips what the subscriber does not want.
	if got := wait(`{"keyPrefix":"msg/","afterSeq":0,"state":"done","timeoutMs":1000}`); got.Entry.Seq != 3 {
		t.Fatalf("done read = %+v, want seq 3", got.Entry)
	}
	// A wait without a state matches any entry.
	if got := wait(`{"keyPrefix":"msg/","afterSeq":2,"timeoutMs":1000}`); got.Entry.Seq != 3 {
		t.Fatalf("any-state read = %+v, want seq 3", got.Entry)
	}
	// The exclusive topic read still works.
	if got := wait(`{"key":"msg/broadcast","afterSeq":1,"timeoutMs":1000}`); got.Entry.Seq != 2 {
		t.Fatalf("exact read = %+v, want seq 2", got.Entry)
	}
	// A parked cursor read wakes on the next post and returns it.
	done := make(chan box.WaitReply, 1)
	go func() {
		m := boxCall(t, s, cl, conn, "msg-wait", `{"keyPrefix":"msg/","afterSeq":3,"timeoutMs":2000}`)
		var rep box.WaitReply
		_ = json.Unmarshal(m.Res, &rep)
		done <- rep
	}()
	time.Sleep(20 * time.Millisecond)
	boxPost(t, s, cl, conn, "msg/integration", "done", "integration green")
	select {
	case rep := <-done:
		if rep.Entry.Seq != 4 || rep.Entry.Key != "msg/integration" {
			t.Fatalf("woken read = %+v, want seq 4 on integration", rep.Entry)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the parked read never returned")
	}
}

// TestBoxTrimmedCursor proves a cursor behind the retained log reports the gap
// instead of silently starting at the oldest kept row.
func TestBoxTrimmedCursor(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)
	boxPost(t, s, cl, conn, "msg/a", "note", "one")
	boxPost(t, s, cl, conn, "msg/a", "note", "two")
	m := boxCall(t, s, cl, conn, "msg-clear", `{}`)
	if !m.OK {
		t.Fatalf("clear: %s", m.Error)
	}
	boxPost(t, s, cl, conn, "msg/a", "note", "three")

	m = boxCall(t, s, cl, conn, "msg-list", `{"afterSeq":0}`)
	var list box.ListReply
	if err := json.Unmarshal(m.Res, &list); err != nil {
		t.Fatalf("list reply: %v", err)
	}
	if !list.Trimmed || len(list.Entries) != 1 || list.Entries[0].Seq != 3 {
		t.Fatalf("trimmed list = %+v, want the gap reported and seq 3", list)
	}
	// A cursor on the oldest kept row has no gap.
	m = boxCall(t, s, cl, conn, "msg-list", `{"afterSeq":2}`)
	if err := json.Unmarshal(m.Res, &list); err != nil {
		t.Fatalf("list reply: %v", err)
	}
	if list.Trimmed {
		t.Fatalf("cursor one before the oldest row reported a gap: %+v", list)
	}
	// A stream wait reports the same gap with the entry it returns.
	m = boxCall(t, s, cl, conn, "msg-wait", `{"afterSeq":0,"timeoutMs":1000}`)
	var wait box.WaitReply
	if err := json.Unmarshal(m.Res, &wait); err != nil {
		t.Fatalf("wait reply: %v", err)
	}
	if !m.OK || !wait.Trimmed || wait.Entry.Seq != 3 {
		t.Fatalf("trimmed wait = %s (%v), want the gap and seq 3", m.Res, m.Error)
	}
}

// TestBoxTopicFieldsExclusive proves a read names an exact topic or a branch,
// not both, and that a negative cursor is refused.
func TestBoxTopicFieldsExclusive(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)
	for _, c := range []struct {
		action string
		res    string
		want   string
	}{
		{"msg-list", `{"key":"a","keyPrefix":"b"}`, "exclusive"},
		{"msg-wait", `{"key":"a","keyPrefix":"b","timeoutMs":1000}`, "exclusive"},
		{"msg-list", `{"afterSeq":-1}`, "afterSeq"},
		{"msg-wait", `{"afterSeq":-1,"timeoutMs":1000}`, "afterSeq"},
	} {
		m := boxCall(t, s, cl, conn, c.action, c.res)
		if m.OK {
			t.Fatalf("%s %s accepted", c.action, c.res)
		}
		if !strings.Contains(m.Error, c.want) {
			t.Fatalf("%s %s error = %q, want %q", c.action, c.res, m.Error, c.want)
		}
	}
}

func TestBoxPostFromPane(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)

	m := boxCall(t, s, cl, conn, "msg-post", `{"state":"done","from":"w9p9"}`)
	if m.OK {
		t.Fatalf("post from a pane outside the tree accepted: %s", m.Res)
	}
	if !strings.Contains(m.Error, "w9p9") {
		t.Fatalf("error %q does not name the pane", m.Error)
	}

	m = boxCall(t, s, cl, conn, "msg-post", `{"state":"done","from":"w1p1"}`)
	if !m.OK {
		t.Fatalf("post from a pane in the tree: %s", m.Error)
	}
	var rep box.PostReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("post reply: %v", err)
	}
	if rep.Entry.From != "w1p1" {
		t.Fatalf("stored from = %q, want w1p1", rep.Entry.From)
	}

	m = boxCall(t, s, cl, conn, "msg-post", `{"state":"done"}`)
	if !m.OK {
		t.Fatalf("post with no from: %s", m.Error)
	}
}

func TestBoxSchemaActionAnswers(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)
	m := boxCall(t, s, cl, conn, "msg-schema", `{}`)
	if !m.OK {
		t.Fatalf("schema: %s", m.Error)
	}
	var rep box.SchemaReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("schema reply: %v", err)
	}
	actions, ok := rep.Schema["actions"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no actions: %s", m.Res)
	}
	if len(actions) != len(box.Wire) {
		t.Fatalf("schema has %d actions, want %d", len(actions), len(box.Wire))
	}
	for _, action := range box.ActionNames() {
		if _, ok := actions[action]; !ok {
			t.Fatalf("schema has no action %q", action)
		}
	}
	// The frozen enum reaches the wire.
	for _, name := range box.StateNames() {
		if !strings.Contains(string(m.Res), `"`+name+`"`) {
			t.Fatalf("schema does not carry state %q", name)
		}
	}
}

func TestBoxStateFileWithoutBoxLoadsEmpty(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	if err := os.MkdirAll(paths.SessionDir("oldbox"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	old := `{"name":"oldbox","workspaces":[{"id":"s1","name":"main","windows":[{"id":"w1","name":"main","root":{"pane":"w1p1"},"focus":"w1p1","nextPane":1}],"active":0}],"active":0,"cols":80,"rows":24,"panes":{"w1p1":{}},"nextWorkspace":1,"nextWindow":1,"idVersion":2,"rail":34,"railSet":true}`
	if err := os.WriteFile(paths.StatePath("oldbox"), []byte(old), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	st, err := session.LoadState("oldbox")
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if len(st.Box) != 0 || st.BoxSeq != 0 {
		t.Fatalf("old state loaded box %v, seq %d, want empty", st.Box, st.BoxSeq)
	}
}

func TestBoxLoadDropsBadEntryAndKeepsSeq(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	st := session.NewState("badbox")
	good := box.Entry{Seq: 1, At: time.Now().UTC().Truncate(time.Second), Key: "lane/a", State: box.StateDone, Msg: "ok"}
	bad := good
	bad.Seq = 2
	bad.State = "clear"
	st.Box = []box.Entry{good, bad}
	st.BoxSeq = 2
	if err := st.Save(); err != nil {
		t.Fatalf("save state: %v", err)
	}

	old := log.Writer()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	got, err := session.LoadState("badbox")
	log.SetOutput(old)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if len(got.Box) != 1 || got.Box[0].Seq != 1 {
		t.Fatalf("loaded box = %+v, want the one good entry", got.Box)
	}
	if got.BoxSeq != 2 {
		t.Fatalf("loaded BoxSeq = %d, want 2", got.BoxSeq)
	}
	if n := strings.Count(buf.String(), "\n"); n != 1 {
		t.Fatalf("log has %d lines, want 1: %q", n, buf.String())
	}
	if !strings.Contains(buf.String(), "dropped 1 invalid entries") {
		t.Fatalf("log does not count the drop: %q", buf.String())
	}
}

// boxPane is the first pane in the test server's tree, the id a read names.
func boxPane(t *testing.T, s *server) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.st.ActiveWindow().Focus
	if id == "" {
		t.Fatal("the session has no pane")
	}
	return id
}

// boxAttach puts a client on the server's push list and returns its pipe, so a
// test reads the frames a broadcast sends.
func boxAttach(t *testing.T, s *server) (*client, net.Conn) {
	t.Helper()
	cl, conn := boxConn(t)
	s.mu.Lock()
	s.clients[cl] = true
	s.mu.Unlock()
	t.Cleanup(func() {
		s.mu.Lock()
		delete(s.clients, cl)
		s.mu.Unlock()
		cl.shutdown()
	})
	return cl, conn
}

// boxWatcherList asks the server for its live watches.
func boxWatcherList(t *testing.T, s *server, cl *client, conn net.Conn) []box.Watch {
	t.Helper()
	m := boxCall(t, s, cl, conn, "msg-watchers", `{}`)
	if !m.OK {
		t.Fatalf("watchers: %s", m.Error)
	}
	if !strings.Contains(string(m.Res), `"watchers":[`) {
		t.Fatalf("watchers reply = %s, want the frozen wrapper", m.Res)
	}
	var rep box.WatchersReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("watchers reply: %v", err)
	}
	return rep.Watchers
}

// boxAwaitWatchers polls the watcher list until done holds, so a parked wait or
// a finished post has time to settle on the server.
func boxAwaitWatchers(t *testing.T, s *server, cl *client, conn net.Conn, why string, done func([]box.Watch) bool) []box.Watch {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ws := boxWatcherList(t, s, cl, conn)
		if done(ws) {
			return ws
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: watchers = %+v", why, ws)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// boxPark starts a msg-wait on its own connection and returns the reader for
// the reply, so the test can keep asking the server questions meanwhile.
func boxPark(t *testing.T, s *server, res string) func(t *testing.T) wire.Msg {
	t.Helper()
	cl, conn := boxConn(t)
	go s.handleReq(cl, wire.Msg{Type: "req", Action: "msg-wait", Res: json.RawMessage(res)})
	return func(t *testing.T) wire.Msg {
		t.Helper()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		m, err := wire.ReadMsg(conn)
		if err != nil {
			t.Fatalf("wait reply: %v", err)
		}
		return m
	}
}

// boxPushIs reads pushes until one carries the wanted type. A post pushes its
// own box frame, so a test that only cares about watches skips over it.
func boxPushIs(t *testing.T, conn net.Conn, want, why string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("%s: no %s push arrived", why, want)
		}
		if m := boxNextPush(t, conn); m.Type == want {
			return
		}
	}
}

// boxNextPush reads one frame the server pushed to an attached client.
func boxNextPush(t *testing.T, conn net.Conn) wire.Msg {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	m, err := wire.ReadMsg(conn)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	return m
}

// boxNoPush fails when a frame arrives within the quiet window.
func boxNoPush(t *testing.T, conn net.Conn, why string) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Millisecond))
	m, err := wire.ReadMsg(conn)
	if err == nil {
		t.Fatalf("%s: pushed %q", why, m.Type)
	}
	_ = conn.SetReadDeadline(time.Time{})
}

// TestBoxWatchParkedWaitAppears: a wait that names its pane is a watch the
// server lists, parked at cursor 0, before any post exists.
func TestBoxWatchParkedWaitAppears(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)
	pane := boxPane(t, s)

	boxPark(t, s, `{"key":"lane/a","from":"`+pane+`","timeoutMs":5000}`)
	ws := boxAwaitWatchers(t, s, cl, conn, "the parked wait never appeared", func(ws []box.Watch) bool {
		return len(ws) == 1 && ws[0].Parked
	})
	s.mu.Lock()
	posts := len(s.st.Box)
	s.mu.Unlock()
	if posts != 0 {
		t.Fatalf("the box holds %d entries, want none", posts)
	}
	w := ws[0]
	if w.Pane != pane || w.Key != "lane/a" || w.KeyPrefix != "" || w.Cursor != 0 {
		t.Fatalf("watch = %+v, want a parked lane/a watch on %s at cursor 0", w, pane)
	}
	if w.At.Location() != time.UTC || !w.At.Equal(w.At.Truncate(time.Second)) {
		t.Fatalf("at = %v, want a whole second in UTC", w.At)
	}
}

// TestBoxWatchServedWaitAdvancesAndUnparks: the post that serves a parked wait
// leaves the watch at the seq it served and no longer parked.
func TestBoxWatchServedWaitAdvancesAndUnparks(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)
	pane := boxPane(t, s)

	reply := boxPark(t, s, `{"key":"lane/a","from":"`+pane+`","timeoutMs":5000}`)
	boxAwaitWatchers(t, s, cl, conn, "the wait never parked", func(ws []box.Watch) bool {
		return len(ws) == 1 && ws[0].Parked
	})

	post := boxPost(t, s, cl, conn, "lane/a", "done", "gate green")
	m := reply(t)
	if !m.OK {
		t.Fatalf("wait: %s", m.Error)
	}
	var rep box.WaitReply
	if err := json.Unmarshal(m.Res, &rep); err != nil || rep.Entry.Seq != post.Seq {
		t.Fatalf("wait reply = %s (%v), want seq %d", m.Res, err, post.Seq)
	}

	ws := boxAwaitWatchers(t, s, cl, conn, "the served watch never settled", func(ws []box.Watch) bool {
		return len(ws) == 1 && !ws[0].Parked && ws[0].Cursor == post.Seq
	})
	if ws[0].Key != "lane/a" || ws[0].Pane != pane {
		t.Fatalf("watch = %+v, want the lane/a watch on %s", ws[0], pane)
	}
}

// TestBoxWatchListAdvances: a list that names its pane roots a watch at the
// highest seq it served, and a later read never lowers it.
func TestBoxWatchListAdvances(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)
	pane := boxPane(t, s)
	boxPost(t, s, cl, conn, "lane/a", "note", "one")
	boxPost(t, s, cl, conn, "lane/b", "note", "two")
	third := boxPost(t, s, cl, conn, "lane/a", "note", "three")

	for _, c := range []struct {
		res  string
		want int
	}{
		{`{"keyPrefix":"lane/","from":"` + pane + `"}`, third.Seq},
		{`{"keyPrefix":"lane/","afterSeq":2,"from":"` + pane + `"}`, third.Seq},
		{`{"keyPrefix":"lane/","afterSeq":3,"from":"` + pane + `"}`, third.Seq},
	} {
		if m := boxCall(t, s, cl, conn, "msg-list", c.res); !m.OK {
			t.Fatalf("list %s: %s", c.res, m.Error)
		}
		ws := boxAwaitWatchers(t, s, cl, conn, "cursor after "+c.res, func(ws []box.Watch) bool {
			return len(ws) == 1 && ws[0].Cursor == c.want
		})
		if ws[0].Parked {
			t.Fatalf("list %s left the watch parked", c.res)
		}
	}

	// Two filters on one pane are two watches.
	boxCall(t, s, cl, conn, "msg-list", `{"key":"lane/a","from":"`+pane+`"}`)
	ws := boxWatcherList(t, s, cl, conn)
	if len(ws) != 2 {
		t.Fatalf("watches = %+v, want one per filter", ws)
	}
}

// TestBoxWatchClosedPaneDrops: a watch whose pane left the tree is dropped
// before the next watcher reply.
func TestBoxWatchClosedPaneDrops(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)
	pane := boxPane(t, s)

	boxCall(t, s, cl, conn, "msg-list", `{"from":"`+pane+`"}`)
	if ws := boxWatcherList(t, s, cl, conn); len(ws) != 1 {
		t.Fatalf("watches = %+v, want the one the list left", ws)
	}

	s.mu.Lock()
	w := s.st.ActiveWindow()
	w.Root = &layout.Node{Pane: w.NewPaneID()}
	w.Focus = w.Root.Pane
	s.mu.Unlock()

	if ws := boxWatcherList(t, s, cl, conn); len(ws) != 0 {
		t.Fatalf("watches = %+v, want none after the pane left", ws)
	}
}

// TestBoxWatchStaleUnparkedDrops: an unparked watch older than the ttl goes,
// and a parked one of the same age stays.
func TestBoxWatchStaleUnparkedDrops(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)
	pane := boxPane(t, s)
	boxPost(t, s, cl, conn, "lane/a", "note", "one")
	boxCall(t, s, cl, conn, "msg-list", `{"key":"lane/a","from":"`+pane+`"}`)

	stale := time.Now().UTC().Truncate(time.Second).Add(-11 * time.Minute)
	s.mu.Lock()
	for k, w := range s.watches {
		w.At = stale
		s.watches[k] = w
	}
	s.mu.Unlock()
	if ws := boxWatcherList(t, s, cl, conn); len(ws) != 0 {
		t.Fatalf("watches = %+v, want the stale one dropped", ws)
	}

	s.mu.Lock()
	s.watches[boxWatchKeyOf(pane, "lane/b", "")] = box.Watch{
		Pane: pane, Key: "lane/b", Cursor: 1, Parked: true, At: stale}
	s.mu.Unlock()
	ws := boxWatcherList(t, s, cl, conn)
	if len(ws) != 1 || !ws[0].Parked {
		t.Fatalf("watches = %+v, want the parked one kept whatever its age", ws)
	}
}

// TestBoxWatchersOrder: the reply is sorted by pane, key and keyPrefix.
func TestBoxWatchersOrder(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)
	at := time.Now().UTC().Truncate(time.Second)
	s.mu.Lock()
	w := s.st.ActiveWindow()
	first, second := w.Focus, w.NewPaneID()
	w.Root = &layout.Node{Dir: "right", Kids: []*layout.Node{{Pane: first}, {Pane: second}},
		Sizes: []float64{0.5, 0.5}}
	s.watches[boxWatchKeyOf(second, "lane/b", "")] = box.Watch{Pane: second, Key: "lane/b", Cursor: 2, At: at}
	s.watches[boxWatchKeyOf(first, "lane/b", "")] = box.Watch{Pane: first, Key: "lane/b", Cursor: 3, At: at}
	s.watches[boxWatchKeyOf(first, "", "lane/")] = box.Watch{Pane: first, KeyPrefix: "lane/", Cursor: 1, At: at}
	s.watches[boxWatchKeyOf(first, "lane/a", "")] = box.Watch{Pane: first, Key: "lane/a", Cursor: 4, At: at}
	s.mu.Unlock()

	ws := boxWatcherList(t, s, cl, conn)
	want := []struct{ pane, key, prefix string }{
		{first, "", "lane/"},
		{first, "lane/a", ""},
		{first, "lane/b", ""},
		{second, "lane/b", ""},
	}
	if len(ws) != len(want) {
		t.Fatalf("watches = %+v, want %d", ws, len(want))
	}
	for i, w := range want {
		if ws[i].Pane != w.pane || ws[i].Key != w.key || ws[i].KeyPrefix != w.prefix {
			t.Fatalf("watch %d = %+v, want %s/%s/%s", i, ws[i], w.pane, w.key, w.prefix)
		}
	}
}

// TestBoxWatchPush: one watch push on appear, advance, park, unpark and drop,
// and none on a read that changes nothing.
func TestBoxWatchPush(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)
	pane := boxPane(t, s)
	_, wconn := boxAttach(t, s)

	// A read that appears pushes once.
	boxCall(t, s, cl, conn, "msg-list", `{"from":"`+pane+`"}`)
	boxPushIs(t, wconn, "watch", "the appear")
	// A read that serves nothing new changes nothing and pushes nothing.
	boxCall(t, s, cl, conn, "msg-list", `{"from":"`+pane+`"}`)
	boxNoPush(t, wconn, "a repeat list")

	// Parking pushes, and the post that serves it pushes once. The post pushes
	// its own box frame first, which boxPushIs skips.
	reply := boxPark(t, s, `{"key":"lane/a","from":"`+pane+`","timeoutMs":5000}`)
	boxPushIs(t, wconn, "watch", "the park")
	boxPost(t, s, cl, conn, "lane/a", "done", "gate green")
	if m := reply(t); !m.OK {
		t.Fatalf("wait: %s", m.Error)
	}
	boxPushIs(t, wconn, "watch", "the unpark")

	// The drop of a stale watch pushes once.
	s.mu.Lock()
	for k, w := range s.watches {
		w.At = time.Now().UTC().Add(-11 * time.Minute)
		s.watches[k] = w
	}
	s.mu.Unlock()
	boxWatcherList(t, s, cl, conn)
	boxPushIs(t, wconn, "watch", "the drop")
	boxNoPush(t, wconn, "the settled list")
}

// TestBoxWatchNoPaneFails: a read that names a pane outside the tree fails in
// post's wording and records nothing.
func TestBoxWatchNoPaneFails(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)
	for _, c := range []struct{ action, res string }{
		{"msg-list", `{"from":"w9p9"}`},
		{"msg-wait", `{"from":"w9p9","timeoutMs":1000}`},
	} {
		m := boxCall(t, s, cl, conn, c.action, c.res)
		if m.OK {
			t.Fatalf("%s from a missing pane was accepted", c.action)
		}
		if want := `box: no pane "w9p9"`; m.Error != want {
			t.Fatalf("%s error = %q, want %q", c.action, m.Error, want)
		}
	}
	if ws := boxWatcherList(t, s, cl, conn); len(ws) != 0 {
		t.Fatalf("watches = %+v, want none", ws)
	}
}

// boxSubStart runs one msg-sub handler in its own goroutine, the way the read
// loop runs it, and returns a channel closed when the handler returns.
func boxSubStart(t *testing.T, s *server, cl *client, res string) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleReq(cl, wire.Msg{Type: "req", Action: "msg-sub", Res: json.RawMessage(res)})
	}()
	return done
}

// boxSubFrame reads one stream frame and fails on any other frame.
func boxSubFrame(t *testing.T, conn net.Conn) box.SubFrame {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	m, err := wire.ReadMsg(conn)
	if err != nil {
		t.Fatalf("sub frame: %v", err)
	}
	if m.Type != "msg" {
		t.Fatalf("sub frame type = %q, want msg", m.Type)
	}
	if m.OK || m.Error != "" {
		t.Fatalf("sub frame = %+v, want a plain stream frame", m)
	}
	var f box.SubFrame
	if err := json.Unmarshal(m.Res, &f); err != nil {
		t.Fatalf("sub frame %s: %v", m.Res, err)
	}
	return f
}

// boxSubQuiet fails when a stream frame arrives inside the quiet window, and
// leaves the connection usable for the next read.
func boxSubQuiet(t *testing.T, conn net.Conn, why string) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
	m, err := wire.ReadMsg(conn)
	_ = conn.SetReadDeadline(time.Time{})
	if err == nil {
		t.Fatalf("%s: the stream sent %q", why, m.Type)
	}
}

// boxAwaitWatch polls the watch registry until done holds for k, so a stream
// that runs in its own goroutine has time to settle.
func boxAwaitWatch(t *testing.T, s *server, k boxWatchKey, why string, done func(box.Watch) bool) box.Watch {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		w, ok := s.watches[k]
		s.mu.Unlock()
		if ok && done(w) {
			return w
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: watch = %+v (listed %v)", why, w, ok)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// boxUnixConn is a client on a real unix socket with the test holding the other
// end, so connGone sees a hangup the way it does in a live session.
func boxUnixConn(t *testing.T) (*client, net.Conn) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "sub.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	type accepted struct {
		c   net.Conn
		err error
	}
	got := make(chan accepted, 1)
	go func() {
		c, err := ln.Accept()
		got <- accepted{c, err}
	}()
	peer, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	a := <-got
	if a.err != nil {
		t.Fatalf("accept: %v", a.err)
	}
	t.Cleanup(func() {
		_ = peer.Close()
		_ = a.c.Close()
	})
	return &client{c: a.c, cols: 80, rows: 24}, peer
}

// TestBoxSubBlocksAndStreams: the handler parks on an empty box, then streams
// the two entries that land, in order, and parks again.
func TestBoxSubBlocksAndStreams(t *testing.T) {
	s := boxServer(t)
	sub, conn := boxUnixConn(t)
	done := boxSubStart(t, s, sub, `{"keyPrefix":"lane/","afterSeq":0}`)
	awaitWaiters(t, s, 1)
	select {
	case <-done:
		t.Fatal("the stream returned before any entry")
	default:
	}

	poster, pconn := boxConn(t)
	first := boxPost(t, s, poster, pconn, "lane/a", "note", "one")
	second := boxPost(t, s, poster, pconn, "lane/b", "done", "two")

	got := boxSubFrame(t, conn)
	if got.Entry.Seq != first.Seq || got.Entry.Msg != "one" || got.Trimmed {
		t.Fatalf("first frame = %+v, want seq %d untrimmed", got, first.Seq)
	}
	got = boxSubFrame(t, conn)
	if got.Entry.Seq != second.Seq || got.Entry.Msg != "two" || got.Trimmed {
		t.Fatalf("second frame = %+v, want seq %d untrimmed", got, second.Seq)
	}
	// The stream stays open: it is one parked handler, not two replies.
	awaitWaiters(t, s, 1)
	select {
	case <-done:
		t.Fatal("the stream returned after two entries")
	default:
	}
}

// TestBoxSubStateFilterAdvancesCursor: a state filter ships only the matching
// entry, and the cursor still walks over the one it skipped.
func TestBoxSubStateFilterAdvancesCursor(t *testing.T) {
	s := boxServer(t)
	pane := boxPane(t, s)
	sub, conn := boxUnixConn(t)
	k := boxWatchKeyOf(pane, "lane/a", "")
	boxSubStart(t, s, sub, `{"key":"lane/a","state":"done","from":"`+pane+`"}`)
	boxAwaitWatch(t, s, k, "the stream never parked", func(w box.Watch) bool {
		return w.Parked && w.Cursor == 0
	})

	poster, pconn := boxConn(t)
	note := boxPost(t, s, poster, pconn, "lane/a", "note", "progress")
	boxSubQuiet(t, conn, "a note entry reached a done subscriber")
	boxAwaitWatch(t, s, k, "the skipped entry never moved the cursor", func(w box.Watch) bool {
		return w.Parked && w.Cursor == note.Seq
	})

	gate := boxPost(t, s, poster, pconn, "lane/a", "done", "gate green")
	got := boxSubFrame(t, conn)
	if got.Entry.Seq != gate.Seq || got.Entry.State != box.StateDone {
		t.Fatalf("frame = %+v, want the done entry %d", got, gate.Seq)
	}
	boxAwaitWatch(t, s, k, "the served entry never moved the cursor", func(w box.Watch) bool {
		return w.Parked && w.Cursor == gate.Seq
	})
}

// TestBoxSubGapTrimsOnce: a cursor behind the oldest kept entry starts there and
// says so on the first frame only.
func TestBoxSubGapTrimsOnce(t *testing.T) {
	s := boxServer(t)
	poster, pconn := boxConn(t)
	boxPost(t, s, poster, pconn, "lane/a", "note", "one")
	boxPost(t, s, poster, pconn, "lane/a", "note", "two")
	if m := boxCall(t, s, poster, pconn, "msg-clear", `{}`); !m.OK {
		t.Fatalf("clear: %s", m.Error)
	}
	third := boxPost(t, s, poster, pconn, "lane/a", "note", "three")

	sub, conn := boxUnixConn(t)
	boxSubStart(t, s, sub, `{"key":"lane/a","afterSeq":0}`)
	got := boxSubFrame(t, conn)
	if !got.Trimmed || got.Entry.Seq != third.Seq {
		t.Fatalf("first frame = %+v, want the gap reported on seq %d", got, third.Seq)
	}

	fourth := boxPost(t, s, poster, pconn, "lane/a", "note", "four")
	got = boxSubFrame(t, conn)
	if got.Trimmed || got.Entry.Seq != fourth.Seq {
		t.Fatalf("later frame = %+v, want seq %d with no gap", got, fourth.Seq)
	}
}

// TestBoxSubEmptyLogNoGap: a cursor on an empty log has no gap, so the first
// entry that arrives later carries trimmed false.
func TestBoxSubEmptyLogNoGap(t *testing.T) {
	s := boxServer(t)
	sub, conn := boxUnixConn(t)
	boxSubStart(t, s, sub, `{"key":"lane/a","afterSeq":0}`)
	awaitWaiters(t, s, 1)

	poster, pconn := boxConn(t)
	first := boxPost(t, s, poster, pconn, "lane/a", "note", "one")
	got := boxSubFrame(t, conn)
	if got.Trimmed || got.Entry.Seq != first.Seq {
		t.Fatalf("frame = %+v, want seq %d untrimmed", got, first.Seq)
	}
}

// TestBoxSubWatchParkedForLife: the watch is parked from the first frame to the
// last and its cursor follows the stream.
func TestBoxSubWatchParkedForLife(t *testing.T) {
	s := boxServer(t)
	pane := boxPane(t, s)
	poster, pconn := boxConn(t)
	first := boxPost(t, s, poster, pconn, "lane/a", "note", "one")

	sub, conn := boxUnixConn(t)
	k := boxWatchKeyOf(pane, "", "lane/")
	boxSubStart(t, s, sub, `{"keyPrefix":"lane/","from":"`+pane+`"}`)

	if got := boxSubFrame(t, conn); got.Entry.Seq != first.Seq {
		t.Fatalf("frame = %+v, want seq %d", got, first.Seq)
	}
	w := boxAwaitWatch(t, s, k, "the streaming watch never advanced", func(w box.Watch) bool {
		return w.Parked && w.Cursor == first.Seq
	})
	if w.Pane != pane || w.KeyPrefix != "lane/" {
		t.Fatalf("watch = %+v, want the lane/ branch on %s", w, pane)
	}

	second := boxPost(t, s, poster, pconn, "lane/b", "done", "two")
	if got := boxSubFrame(t, conn); got.Entry.Seq != second.Seq {
		t.Fatalf("frame = %+v, want seq %d", got, second.Seq)
	}
	boxAwaitWatch(t, s, k, "the watch never reached the second entry", func(w box.Watch) bool {
		return w.Parked && w.Cursor == second.Seq
	})
}

// TestBoxSubDisconnectUnparksAndStops: the handler notices a gone connection
// through boxPollWait, unparks its watch and returns.
func TestBoxSubDisconnectUnparksAndStops(t *testing.T) {
	s := boxServer(t)
	pane := boxPane(t, s)
	sub, peer := boxUnixConn(t)
	k := boxWatchKeyOf(pane, "lane/a", "")
	done := boxSubStart(t, s, sub, `{"key":"lane/a","from":"`+pane+`"}`)
	boxAwaitWatch(t, s, k, "the stream never parked", func(w box.Watch) bool { return w.Parked })

	if err := peer.Close(); err != nil {
		t.Fatalf("close the subscriber: %v", err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream outlived its connection")
	}
	boxAwaitWatch(t, s, k, "the watch stayed parked after the disconnect", func(w box.Watch) bool {
		return !w.Parked
	})
	awaitWaiters(t, s, 0)
}

// TestBoxSubBadRequestRepliesWithError: a refused stream answers with a resp
// carrying the error and sends no frame. A from outside the tree records no
// watch.
func TestBoxSubBadRequestRepliesWithError(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxUnixConn(t)
	cases := []struct{ res, want string }{
		{`{"key":"a","keyPrefix":"b"}`, "exclusive"},
		{`{"afterSeq":-1}`, "afterSeq"},
		{`{"state":"clear"}`, "clear"},
		{`{"from":"lane"}`, "not a pane id"},
		{`{"from":"w9p9"}`, `box: no pane "w9p9"`},
	}
	for _, c := range cases {
		m := boxCall(t, s, cl, conn, "msg-sub", c.res)
		if m.OK {
			t.Fatalf("%s accepted", c.res)
		}
		if !strings.Contains(m.Error, c.want) {
			t.Fatalf("%s error = %q, want %q", c.res, m.Error, c.want)
		}
		boxSubQuiet(t, conn, "a refused stream "+c.res)
	}
	if ws := boxWatcherList(t, s, cl, conn); len(ws) != 0 {
		t.Fatalf("watches = %+v, want none after the refused streams", ws)
	}
}
