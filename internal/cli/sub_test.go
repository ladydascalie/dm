package cli

// The subscription: frames print as they arrive, the cursor follows every
// seq, and a refusal is not retried.

import (
	"encoding/json"
	"net"
	"strings"
	"testing"

	"dm/internal/box"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// frameServer answers a msg-sub with the given frames, then ends the
// connection, the way a server restart looks to a subscriber.
func frameServer(t *testing.T, frames ...box.SubFrame) {
	t.Helper()
	startFakeServer(t, "main", func(c net.Conn, req wire.Msg) (wire.Msg, bool) {
		if req.Action != "msg-sub" {
			return wire.Msg{OK: false, Error: "unexpected action " + req.Action}, true
		}
		for _, f := range frames {
			raw, _ := json.Marshal(f)
			if err := wire.WriteMsg(c, wire.Msg{Res: raw}); err != nil {
				break
			}
		}
		return wire.Msg{}, false
	})
}

// TestBoxSubStreamFrames proves one connection prints every frame as a row
// and moves the caller's cursor to the last seq it served.
func TestBoxSubStreamFrames(t *testing.T) {
	frameServer(t,
		box.SubFrame{Entry: box.Entry{Seq: 2, Key: "lane/a", State: box.StateWorking, Msg: "first"}, Trimmed: true},
		box.SubFrame{Entry: box.Entry{Seq: 5, Key: "lane/a", State: box.StateDone, Msg: "second"}},
	)
	req := box.SubReq{Key: "lane/a"}
	out := testutil.CaptureStdout(t, func() {
		live, err := boxSubStream("main", &req, false)
		if !live || err != errBoxSubEnded {
			t.Fatalf("boxSubStream = (%v, %v), want a live stream that ended", live, err)
		}
	})
	for _, want := range []string{"first", "second", "lane/a", "done"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stream printed %q, want the row for %q", out, want)
		}
	}
	if req.AfterSeq == nil || *req.AfterSeq != 5 {
		t.Fatalf("cursor = %v, want 5 after the last frame", req.AfterSeq)
	}
}

// TestBoxSubStreamJSON proves --json prints one entry object per line, so a
// reader can parse the stream as NDJSON.
func TestBoxSubStreamJSON(t *testing.T) {
	frameServer(t, box.SubFrame{
		Entry: box.Entry{Seq: 3, Key: "lane/a", State: box.StateNote, Msg: "frame"},
	})
	req := box.SubReq{Key: "lane/a"}
	out := testutil.CaptureStdout(t, func() {
		if _, err := boxSubStream("main", &req, true); err != errBoxSubEnded {
			t.Fatalf("boxSubStream json = %v", err)
		}
	})
	line := strings.TrimSpace(out)
	var e box.Entry
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		t.Fatalf("json stream printed %q, want one entry object: %v", out, err)
	}
	if e.Seq != 3 || e.Msg != "frame" {
		t.Fatalf("json stream entry = %+v", e)
	}
	if req.AfterSeq == nil || *req.AfterSeq != 3 {
		t.Fatalf("cursor = %v, want 3 after the frame", req.AfterSeq)
	}
}

// TestBoxSubVerbRefusesABadFilter proves a refused subscription is not
// retried, and that the refusal still went out with the filter the reader
// asked for.
func TestBoxSubVerbRefusesABadFilter(t *testing.T) {
	rec := &recorder{}
	startFakeServer(t, "main", func(_ net.Conn, req wire.Msg) (wire.Msg, bool) {
		rec.handler()(nil, req)
		return wire.Msg{OK: false, Error: "bad filter"}, true
	})
	flags := newFlags("msg")
	flags.parse([]string{"sub", "--key", "lane/a", "--state", "done", "--after", "4"})
	if err := boxSubVerb(flags); err == nil || err.Error() != "bad filter" {
		t.Fatalf("boxSubVerb on a refusal = %v, want the refusal", err)
	}
	var req box.SubReq
	if err := json.Unmarshal(rec.last().Res, &req); err != nil {
		t.Fatal(err)
	}
	if req.Key != "lane/a" || req.State != box.StateDone || req.AfterSeq == nil || *req.AfterSeq != 4 {
		t.Fatalf("sub request = %+v, want the key, state and cursor", req)
	}
}
