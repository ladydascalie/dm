package server

// The body tests: the server stores the body, the reads carry it, and the
// caps hold at the wire.

import (
	"encoding/json"
	"strings"
	"testing"

	"dm/internal/box"
)

// TestBoxBodyRoundTrip: a post with a body echoes it, and the list and the
// wait carry the same body back.
func TestBoxBodyRoundTrip(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)

	reply := boxCall(t, s, cl, conn, "msg-post",
		`{"key":"lane/a","state":"done","msg":"gate green","body":"long\ntext\nwith lines"}`)
	if !reply.OK {
		t.Fatalf("post with a body failed: %s", reply.Error)
	}
	var posted struct {
		Entry box.Entry `json:"entry"`
	}
	if err := json.Unmarshal(reply.Res, &posted); err != nil {
		t.Fatalf("post reply: %v", err)
	}
	if posted.Entry.Body != "long\ntext\nwith lines" {
		t.Fatalf("post entry body = %q", posted.Entry.Body)
	}

	list := boxCall(t, s, cl, conn, "msg-list", `{"key":"lane/a","limit":0}`)
	var listed box.ListReply
	if err := json.Unmarshal(list.Res, &listed); err != nil {
		t.Fatalf("list reply: %v", err)
	}
	if len(listed.Entries) != 1 || listed.Entries[0].Body != posted.Entry.Body {
		t.Fatalf("list body = %+v, want the posted body", listed.Entries)
	}

	wait := boxCall(t, s, cl, conn, "msg-wait", `{"key":"lane/a","state":"done","timeoutMs":1000}`)
	var waited box.WaitReply
	if err := json.Unmarshal(wait.Res, &waited); err != nil {
		t.Fatalf("wait reply: %v", err)
	}
	if waited.Entry.Body != posted.Entry.Body {
		t.Fatalf("wait body = %q, want the posted body", waited.Entry.Body)
	}
}

// TestBoxBodyCaps: a body over the cap and a body with a control character
// are refused at the wire; a multi-line body goes through.
func TestBoxBodyCaps(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)

	long := `{"key":"lane/a","state":"done","msg":"m","body":"` + strings.Repeat("x", box.MaxBody+1) + `"}`
	reply := boxCall(t, s, cl, conn, "msg-post", long)
	if reply.OK {
		t.Fatal("a body over the cap was accepted")
	}
	if !strings.Contains(reply.Error, "cap is") {
		t.Fatalf("cap error = %q, want the cap text", reply.Error)
	}

	reply = boxCall(t, s, cl, conn, "msg-post", `{"key":"lane/a","state":"done","msg":"m","body":"tab\there"}`)
	if reply.OK {
		t.Fatal("a body with a control character was accepted")
	}

	// A multi-line body is not a control-character violation.
	reply = boxCall(t, s, cl, conn, "msg-post", `{"key":"lane/a","state":"done","msg":"m","body":"one\ntwo"}`)
	if !reply.OK {
		t.Fatalf("a multi-line body was refused: %s", reply.Error)
	}
}

// TestBoxBodyEmptyKeepsShape: a post without a body stores the empty body,
// and the entry JSON still names the field, so a reader can tell an empty
// body from a missing one.
func TestBoxBodyEmptyKeepsShape(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)

	reply := boxCall(t, s, cl, conn, "msg-post", `{"key":"lane/a","state":"done","msg":"gate green"}`)
	if !reply.OK {
		t.Fatalf("post without a body failed: %s", reply.Error)
	}
	if !strings.Contains(string(reply.Res), `"body":""`) {
		t.Fatalf("post reply = %s, want an explicit empty body", reply.Res)
	}
}
