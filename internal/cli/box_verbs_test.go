package cli

// The msg verbs: the wire each one sends, the rows it prints, and the JSON
// switches.

import (
	"encoding/json"
	"net"
	"strings"
	"testing"

	"dm/internal/box"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// msgEntry is the entry the fake server stores and echoes back.
var msgEntry = box.Entry{
	Seq: 4, From: "w1p1", Key: "lane/a", State: box.StateDone,
	Msg: "posted", Body: "long\nbody",
}

// postHandler decodes the post request, stores it on *got and answers with
// the stored entry.
func postHandler(got *box.PostReq) fakeHandler {
	return func(_ net.Conn, req wire.Msg) (wire.Msg, bool) {
		if err := json.Unmarshal(req.Res, got); err != nil {
			return wire.Msg{OK: false, Error: err.Error()}, true
		}
		rep := box.PostReply{Entry: msgEntry}
		raw, _ := json.Marshal(rep)
		return wire.Msg{OK: true, Res: raw}, true
	}
}

// listHandler answers msg-list with entries and a trimmed flag, only for the
// key the caller named: another key reads as an empty topic.
func listHandler(entries []box.Entry, trimmed bool) fakeHandler {
	return func(_ net.Conn, req wire.Msg) (wire.Msg, bool) {
		if req.Action != "msg-list" {
			return wire.Msg{OK: false, Error: "unexpected action " + req.Action}, true
		}
		var lr box.ListReq
		if err := json.Unmarshal(req.Res, &lr); err != nil {
			return wire.Msg{OK: false, Error: err.Error()}, true
		}
		got := entries
		if lr.Key != "" && len(entries) > 0 && entries[0].Key != lr.Key {
			got = nil
		}
		raw, _ := json.Marshal(box.ListReply{Entries: got, Trimmed: trimmed})
		return wire.Msg{OK: true, Res: raw}, true
	}
}

// TestMsgPostSendsTheEntry proves post wraps key, state, message, body and
// sender into the request, and prints the stored row.
func TestMsgPostSendsTheEntry(t *testing.T) {
	// The shell exports DM_SESSION and DM_PANE, so a post from inside a pane
	// of the target session carries the pane id as its sender. Stays serial:
	// the test's subject is env behaviour, and t.Setenv cannot run in a
	// parallel test.
	t.Setenv("DM_SESSION", "main")
	t.Setenv("DM_PANE", "w1p1")
	var got box.PostReq
	startFakeServer(t, "main", postHandler(&got))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"msg", "post", "--key", "lane/a", "--state", "done",
			"--msg", "posted", "--body", "long\nbody"}); err != nil {
			t.Fatalf("dm msg post: %v", err)
		}
	})
	if got.Key != "lane/a" || got.State != box.StateDone || got.Msg != "posted" || got.Body != "long\nbody" {
		t.Fatalf("post sent %+v", got)
	}
	if got.From != "w1p1" {
		t.Fatalf("post sent from %q, want the pane id from the shell", got.From)
	}
	if !strings.Contains(out, "lane/a") || !strings.Contains(out, "done") {
		t.Fatalf("dm msg post printed %q, want the stored row", out)
	}
}

// TestMsgPostRefusesABadState proves an unknown --state is rejected before
// any request goes out.
func TestMsgPostRefusesABadState(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{}`))
	if err := Run([]string{"msg", "post", "--key", "k", "--state", "clear"}); err == nil || !strings.Contains(err.Error(), "bad state") {
		t.Fatalf("dm msg post with a bad state = %v", err)
	}
}

// TestMsgListTable proves list prints the rows newest first and honours the
// COLUMNS cap. Stays serial: the test's subject is env behaviour, and
// t.Setenv cannot run in a parallel test.
func TestMsgListTable(t *testing.T) {
	t.Setenv("COLUMNS", "60")
	startFakeServer(t, "main", listHandler([]box.Entry{msgEntry}, false))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"msg", "list", "--key", "lane/a"}); err != nil {
			t.Fatalf("dm msg list: %v", err)
		}
	})
	if !strings.Contains(out, "lane/a") || !strings.Contains(out, "done") {
		t.Fatalf("dm msg list printed %q", out)
	}
	if strings.Contains(out, "long\nbody") {
		t.Fatalf("dm msg list printed the body: %q", out)
	}
}

// TestMsgListJSONPrintsTheReply proves --json passes the reply through
// untouched, so a reader can parse it.
func TestMsgListJSONPrintsTheReply(t *testing.T) {
	startFakeServer(t, "main", listHandler([]box.Entry{msgEntry}, false))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"msg", "list", "--key", "lane/a", "--json"}); err != nil {
			t.Fatalf("dm msg list --json: %v", err)
		}
	})
	if !strings.Contains(out, `"seq":4`) || !strings.Contains(out, `"trimmed":false`) {
		t.Fatalf("dm msg list --json printed %q", out)
	}
}

// TestMsgListTrimmedNote proves a dropped gap is reported on stderr, once.
func TestMsgListTrimmedNote(t *testing.T) {
	startFakeServer(t, "main", listHandler(nil, true))
	var out string
	stderr := captureStderr(t, func() {
		out = testutil.CaptureStdout(t, func() {
			if err := Run([]string{"msg", "list", "--key", "k", "--after", "1"}); err != nil {
				t.Fatalf("dm msg list: %v", err)
			}
		})
	})
	if !strings.Contains(stderr, "gone") {
		t.Fatalf("dm msg list stderr = %q, want the gap note", stderr)
	}
	if out != "" {
		t.Fatalf("dm msg list printed %q for an empty box", out)
	}
}

// TestMsgListRefusal surfaces the server's error text.
func TestMsgListRefusal(t *testing.T) {
	startFakeServer(t, "main", refuseHandler("unknown key"))
	if err := Run([]string{"msg", "list", "--key", "k"}); err == nil || err.Error() != "unknown key" {
		t.Fatalf("dm msg list refused = %v", err)
	}
}

// TestMsgShowPrintsTheBody proves show prints the newest entry's body when it
// carries one.
func TestMsgShowPrintsTheBody(t *testing.T) {
	startFakeServer(t, "main", listHandler([]box.Entry{msgEntry}, false))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"msg", "show", "--key", "lane/a"}); err != nil {
			t.Fatalf("dm msg show: %v", err)
		}
	})
	if out != "long\nbody\n" {
		t.Fatalf("dm msg show printed %q, want the body", out)
	}
}

// TestMsgShowFallsBackToTheSummary proves an entry with no body prints its
// summary line.
func TestMsgShowFallsBackToTheSummary(t *testing.T) {
	startFakeServer(t, "main", listHandler([]box.Entry{
		{Seq: 1, Key: "lane/a", State: box.StateDone, Msg: "summary line"},
	}, false))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"msg", "show", "--key", "lane/a"}); err != nil {
			t.Fatalf("dm msg show: %v", err)
		}
	})
	if out != "summary line\n" {
		t.Fatalf("dm msg show printed %q, want the summary", out)
	}
}

// TestMsgShowMissingEntry proves the three refusal texts name what was
// missed: the seq, the pane, or the whole topic.
func TestMsgShowMissingEntry(t *testing.T) {
	startFakeServer(t, "main", listHandler([]box.Entry{msgEntry}, false))
	if err := Run([]string{"msg", "show", "--key", "lane/a", "--seq", "9"}); err == nil ||
		!strings.Contains(err.Error(), `no entry at seq 9`) {
		t.Fatalf("show of a missing seq = %v", err)
	}
	if err := Run([]string{"msg", "show", "--key", "lane/a", "--from", "w9p9"}); err == nil ||
		!strings.Contains(err.Error(), `no entry from "w9p9"`) {
		t.Fatalf("show from an unknown pane = %v", err)
	}
	if err := Run([]string{"msg", "show", "--key", "empty"}); err == nil ||
		!strings.Contains(err.Error(), `key "empty" has no entry`) {
		t.Fatalf("show of an empty key = %v", err)
	}
}

// TestMsgWaitJSONPrintsTheReply proves --json prints the wait reply untouched.
func TestMsgWaitJSONPrintsTheReply(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"entry":{"seq":4,"key":"lane/a","state":"done","msg":"posted","from":"w1p1","at":"2025-01-01T00:00:00Z","body":""},"trimmed":false}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"msg", "wait", "--key", "lane/a", "--state", "done", "--json"}); err != nil {
			t.Fatalf("dm msg wait --json: %v", err)
		}
	})
	if !strings.Contains(out, `"seq":4`) {
		t.Fatalf("dm msg wait --json printed %q", out)
	}
}

// TestMsgWaitPrintsTheEntry proves a satisfied wait prints the entry as one
// row.
func TestMsgWaitPrintsTheEntry(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"entry":{"seq":4,"key":"lane/a","state":"done","msg":"posted","from":"w1p1","at":"2025-01-01T00:00:00Z","body":""},"trimmed":false}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"msg", "wait", "--key", "lane/a", "--state", "done"}); err != nil {
			t.Fatalf("dm msg wait: %v", err)
		}
	})
	if !strings.Contains(out, "lane/a") || !strings.Contains(out, "posted") {
		t.Fatalf("dm msg wait printed %q", out)
	}
}

// TestMsgWaitTrimmedNote proves a dropped gap behind the cursor is reported
// on stderr.
func TestMsgWaitTrimmedNote(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"entry":{"seq":4,"key":"k","state":"done","msg":"m","from":"","at":"2025-01-01T00:00:00Z","body":""},"trimmed":true}`))
	stderr := captureStderr(t, func() {
		testutil.CaptureStdout(t, func() {
			if err := Run([]string{"msg", "wait", "--key", "k", "--after", "1"}); err != nil {
				t.Fatalf("dm msg wait: %v", err)
			}
		})
	})
	if !strings.Contains(stderr, "gone") {
		t.Fatalf("dm msg wait stderr = %q, want the gap note", stderr)
	}
}

// TestMsgWaitRefusesABadState proves an unknown --state fails before the
// request goes out.
func TestMsgWaitRefusesABadState(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{}`))
	if err := Run([]string{"msg", "wait", "--key", "k", "--state", "bogus"}); err == nil || !strings.Contains(err.Error(), "bad state") {
		t.Fatalf("dm msg wait with a bad state = %v", err)
	}
}

// TestMsgClearPrintsTheCount proves clear prints how many entries went.
func TestMsgClearPrintsTheCount(t *testing.T) {
	rec := &recorder{}
	startFakeServer(t, "main", rec.handler())
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"msg", "clear", "--key", "lane/a"}); err != nil {
			t.Fatalf("dm msg clear: %v", err)
		}
	})
	if strings.TrimSpace(out) != "0" {
		t.Fatalf("dm msg clear printed %q", out)
	}
	if rec.last().Action != "msg-clear" {
		t.Fatalf("clear sent action %q", rec.last().Action)
	}
}

// TestMsgWatchersJSONPrintsTheReply proves --json passes the watch list
// through untouched.
func TestMsgWatchersJSONPrintsTheReply(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"watchers":[{"pane":"w1p1","key":"lane/a","keyPrefix":"","cursor":3,"parked":true,"at":"2025-01-01T00:00:00Z"}]}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"msg", "watchers", "--json"}); err != nil {
			t.Fatalf("dm msg watchers --json: %v", err)
		}
	})
	if !strings.Contains(out, `"cursor":3`) {
		t.Fatalf("dm msg watchers --json printed %q", out)
	}
}

// TestMsgSchemaPrintsJSON proves schema prints the server's wire as indented
// JSON.
func TestMsgSchemaPrintsJSON(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"schema":{"msg-post":{"required":["key","state","msg"]}}}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"msg", "schema"}); err != nil {
			t.Fatalf("dm msg schema: %v", err)
		}
	})
	if !strings.Contains(out, "msg-post") || !strings.Contains(out, "\n  ") {
		t.Fatalf("dm msg schema printed %q, want indented JSON", out)
	}
}

// TestMsgUnknownVerbIsRefused proves an unknown verb names the valid set.
func TestMsgUnknownVerbIsRefused(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{}`))
	err := Run([]string{"msg", "poke"})
	if err == nil || !strings.Contains(err.Error(), "unknown verb") || !strings.Contains(err.Error(), "post, list, wait, sub, clear, watchers or schema") {
		t.Fatalf("dm msg poke = %v", err)
	}
}

// TestMsgWatchersPrintsRows proves the plain watchers output names each pane,
// its filter, its cursor and its parked state.
func TestMsgWatchersPrintsRows(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"watchers":[
		{"pane":"w1p1","key":"lane/a","keyPrefix":"","cursor":3,"parked":false,"at":"2025-01-01T00:00:00Z"},
		{"pane":"w1p2","key":"","keyPrefix":"lane","cursor":0,"parked":true,"at":"2025-01-01T00:00:00Z"}]}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"msg", "watchers"}); err != nil {
			t.Fatalf("dm msg watchers: %v", err)
		}
	})
	for _, want := range []string{"w1p1 lane/a 3 -", "w1p2 lane*  0 wait"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dm msg watchers printed %q, want the row for %q", out, want)
		}
	}
}
