package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// boxServer is an in-process server over a temp state dir with one pane in its
// tree, so a post can name a pane that exists.
func boxServer(t *testing.T) *server {
	t.Helper()
	t.Setenv("DM_STATE", t.TempDir())
	st := newState("boxstore")
	rootPane(st)
	return railServer(st)
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
func boxCall(t *testing.T, s *server, cl *client, conn net.Conn, action, res string) Msg {
	t.Helper()
	s.handleReq(cl, Msg{Type: "req", Action: action, Res: json.RawMessage(res)})
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	m, err := readMsg(conn)
	if err != nil {
		t.Fatalf("%s reply: %v", action, err)
	}
	return m
}

// boxPost posts one entry and returns it, failing the test on any error.
func boxPost(t *testing.T, s *server, cl *client, conn net.Conn, key, state, msg string) entry {
	t.Helper()
	res := `{"key":"` + key + `","state":"` + state + `","msg":"` + msg + `"}`
	m := boxCall(t, s, cl, conn, "msg-post", res)
	if !m.OK {
		t.Fatalf("post %s: %s", res, m.Error)
	}
	var rep postReply
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
	var cleared clearReply
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
	posted := []entry{
		boxPost(t, s, cl, conn, "lane/a", "working", "first"),
		boxPost(t, s, cl, conn, "lane/b", "blocked", "second"),
		boxPost(t, s, cl, conn, "lane/a", "done", "third"),
	}

	st, err := loadState("boxstore")
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
	for i := 1; i <= boxMaxEntries; i++ {
		s.st.Box = append(s.st.Box, entry{Seq: i, At: at, Key: "lane/a", State: stateNote, Msg: "filler"})
	}
	s.st.BoxSeq = boxMaxEntries
	s.mu.Unlock()

	newest := boxPost(t, s, cl, conn, "lane/b", "done", "newest")
	if newest.Seq != boxMaxEntries+1 {
		t.Fatalf("seq after a full box = %d, want %d", newest.Seq, boxMaxEntries+1)
	}
	s.mu.Lock()
	box := append([]entry(nil), s.st.Box...)
	s.mu.Unlock()
	if len(box) != boxMaxEntries {
		t.Fatalf("box holds %d entries, want %d", len(box), boxMaxEntries)
	}
	if box[0].Seq != 2 || box[len(box)-1].Seq != newest.Seq {
		t.Fatalf("box keeps seq %d to %d, want 2 to %d", box[0].Seq, box[len(box)-1].Seq, newest.Seq)
	}
	for i := 1; i < len(box); i++ {
		if box[i].Seq != box[i-1].Seq+1 {
			t.Fatalf("box order breaks at %d: %d then %d", i, box[i-1].Seq, box[i].Seq)
		}
	}
}

func TestBoxByteCapDropsOldest(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)

	at := time.Now().UTC().Truncate(time.Second)
	big := strings.Repeat("x", boxMaxMsg)
	s.mu.Lock()
	seq := 0
	for boxJSONBytes(s.st.Box) < boxMaxBytes {
		seq++
		s.st.Box = append(s.st.Box, entry{Seq: seq, At: at, Key: "lane/a", State: stateNote, Msg: big})
	}
	s.st.BoxSeq = seq
	oldest := s.st.Box[0].Seq
	s.mu.Unlock()

	newest := boxPost(t, s, cl, conn, "lane/b", "done", big)
	s.mu.Lock()
	box := append([]entry(nil), s.st.Box...)
	size := boxJSONBytes(box)
	s.mu.Unlock()
	if size > boxMaxBytes {
		t.Fatalf("box is %d bytes, cap is %d", size, boxMaxBytes)
	}
	if box[0].Seq <= oldest {
		t.Fatalf("oldest kept seq = %d, want past %d", box[0].Seq, oldest)
	}
	if box[len(box)-1].Seq != newest.Seq {
		t.Fatalf("newest kept seq = %d, want %d", box[len(box)-1].Seq, newest.Seq)
	}
}

// boxJSONBytes is the JSON size of a box, the measure trimBox uses.
func boxJSONBytes(box []entry) int {
	b, err := json.Marshal(box)
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
		var rep clearReply
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

	list := func(res string) listReply {
		t.Helper()
		m := boxCall(t, s, cl, conn, "msg-list", res)
		if !m.OK {
			t.Fatalf("list %s: %s", res, m.Error)
		}
		var rep listReply
		if err := json.Unmarshal(m.Res, &rep); err != nil {
			t.Fatalf("list reply: %v", err)
		}
		return rep
	}
	seqs := func(es []entry) []int {
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

	wait := func(res string) waitReply {
		t.Helper()
		m := boxCall(t, s, cl, conn, "msg-wait", res)
		if !m.OK {
			t.Fatalf("wait %s: %s", res, m.Error)
		}
		var rep waitReply
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
	done := make(chan waitReply, 1)
	go func() {
		m := boxCall(t, s, cl, conn, "msg-wait", `{"keyPrefix":"msg/","afterSeq":3,"timeoutMs":2000}`)
		var rep waitReply
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
	var list listReply
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
	var wait waitReply
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
	var rep postReply
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
	var rep schemaReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("schema reply: %v", err)
	}
	actions, ok := rep.Schema["actions"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no actions: %s", m.Res)
	}
	if len(actions) != len(boxWire) {
		t.Fatalf("schema has %d actions, want %d", len(actions), len(boxWire))
	}
	for _, action := range actionNames() {
		if _, ok := actions[action]; !ok {
			t.Fatalf("schema has no action %q", action)
		}
	}
	// The frozen enum reaches the wire.
	for _, name := range stateNames() {
		if !strings.Contains(string(m.Res), `"`+name+`"`) {
			t.Fatalf("schema does not carry state %q", name)
		}
	}
}

func TestBoxStateFileWithoutBoxLoadsEmpty(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	if err := os.MkdirAll(sessionDir("oldbox"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	old := `{"name":"oldbox","workspaces":[{"id":"s1","name":"main","windows":[{"id":"w1","name":"main","root":{"pane":"w1p1"},"focus":"w1p1","nextPane":1}],"active":0}],"active":0,"cols":80,"rows":24,"panes":{"w1p1":{}},"nextWorkspace":1,"nextWindow":1,"idVersion":2,"rail":34,"railSet":true}`
	if err := os.WriteFile(statePath("oldbox"), []byte(old), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	st, err := loadState("oldbox")
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if len(st.Box) != 0 || st.BoxSeq != 0 {
		t.Fatalf("old state loaded box %v, seq %d, want empty", st.Box, st.BoxSeq)
	}
}

func TestBoxLoadDropsBadEntryAndKeepsSeq(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	st := newState("badbox")
	good := entry{Seq: 1, At: time.Now().UTC().Truncate(time.Second), Key: "lane/a", State: stateDone, Msg: "ok"}
	bad := good
	bad.Seq = 2
	bad.State = "clear"
	st.Box = []entry{good, bad}
	st.BoxSeq = 2
	if err := st.save(); err != nil {
		t.Fatalf("save state: %v", err)
	}

	old := log.Writer()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	got, err := loadState("badbox")
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
