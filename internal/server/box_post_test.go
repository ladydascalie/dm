package server

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"dm/internal/box"
	"dm/internal/paths"
	"dm/internal/wire"
)

// TestBoxPostPushesAndEchoes: a post pushes one box frame to every attached
// client and the reply names the action it answers.
func TestBoxPostPushesAndEchoes(t *testing.T) {
	s := boxServer(t)
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

	s.handleReq(cl, wire.Msg{
		Type: "req", Action: "msg-post",
		Res: json.RawMessage(`{"key":"lane/a","state":"done","msg":"gate green"}`),
	})
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	push, err := wire.ReadMsg(conn)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if push.Type != "box" {
		t.Fatalf("first frame = %q, want the box push", push.Type)
	}
	reply, err := wire.ReadMsg(conn)
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if reply.Type != "resp" || !reply.OK {
		t.Fatalf("reply = %+v, want a good resp", reply)
	}
	if reply.Action != "msg-post" {
		t.Fatalf("reply action = %q, want msg-post", reply.Action)
	}
}

// readOnlySession makes the session state directory unwritable, so the next
// State.Save fails. It restores the mode before the temp dir is cleaned up.
func readOnlySession(t *testing.T, s *server) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := paths.SessionDir(s.st.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
}

// TestBoxPostSaveErrorReplies: a failed state save rides out on the reply,
// and the entry it appended is still in the in-memory log.
func TestBoxPostSaveErrorReplies(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)
	readOnlySession(t, s)

	reply := boxCall(t, s, cl, conn, "msg-post", `{"key":"lane/a","state":"done","msg":"gate green"}`)
	if reply.OK {
		t.Fatal("post with a failing save replied ok, want an error")
	}
	if !strings.Contains(reply.Error, "permission denied") {
		t.Fatalf("reply error = %q, want the save error", reply.Error)
	}

	list := boxCall(t, s, cl, conn, "msg-list", `{"key":"lane/a"}`)
	if !list.OK {
		t.Fatalf("list: %s", list.Error)
	}
	var rep box.ListReply
	if err := json.Unmarshal(list.Res, &rep); err != nil {
		t.Fatalf("list reply: %v", err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Msg != "gate green" {
		t.Fatalf("entries = %+v, want the posted entry", rep.Entries)
	}
}
