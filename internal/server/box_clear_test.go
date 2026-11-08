package server

import (
	"encoding/json"
	"strings"
	"testing"

	"dm/internal/box"
)

// TestBoxClearSaveErrorReplies: a failed state save rides out on the clear
// reply, and the count reports what the in-memory log lost.
func TestBoxClearSaveErrorReplies(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)
	boxPost(t, s, cl, conn, "lane/a", "done", "gate green")
	readOnlySession(t, s)

	reply := boxCall(t, s, cl, conn, "msg-clear", `{"key":"lane/a"}`)
	if reply.OK {
		t.Fatal("clear with a failing save replied ok, want an error")
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
	if len(rep.Entries) != 0 {
		t.Fatalf("entries = %+v, want the log cleared in memory", rep.Entries)
	}
}

// TestBoxClearErrorRepliesWithCount calls the clear under a failing save
// directly, so the count still reports the removal that the reply drops.
func TestBoxClearErrorRepliesWithCount(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)
	boxPost(t, s, cl, conn, "lane/a", "done", "gate green")
	boxPost(t, s, cl, conn, "lane/b", "done", "gate green")
	readOnlySession(t, s)

	n, err := s.boxClear(&box.ClearReq{Key: "lane/a"})
	if err == nil {
		t.Fatal("clear with a failing save returned no error")
	}
	if n != 1 {
		t.Fatalf("cleared = %d, want 1", n)
	}
}
