package server

import (
	"encoding/json"
	"testing"
	"time"

	"dm/internal/wire"
)

// yankResult is the decoded yank action reply.
type yankResult struct {
	Lines  []string `json:"lines"`
	Start  int      `json:"start"`
	Rows   int      `json:"rows"`
	Anchor bool     `json:"anchor"`
}

// The yank action passes the pane's replayed rows and their row coordinates
// back to the client, which owns the clipboard.
func TestYankActionReturnsPaneRows(t *testing.T) {
	t.Parallel()
	s, ids := histServer(t, "yankrows", 1)
	cl, conn := boxConn(t)
	s.handleReq(cl, wire.Msg{Type: "req", Action: "yank", Target: ids[0], ID: 1})
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	m, err := wire.ReadMsg(conn)
	if err != nil {
		t.Fatalf("yank reply: %v", err)
	}
	if !m.OK {
		t.Fatalf("yank failed: %s", m.Error)
	}
	var res yankResult
	if err := json.Unmarshal(m.Res, &res); err != nil {
		t.Fatalf("decode yank reply: %v", err)
	}
	if !res.Anchor {
		t.Fatal("yank reply dropped the anchor flag")
	}
	if res.Start != 7 || res.Rows != 10 {
		t.Fatalf("yank coordinates = start %d rows %d, want 7 and 10", res.Start, res.Rows)
	}
	if want := []string{"after", "the", "enter"}; len(res.Lines) != len(want) {
		t.Fatalf("yank lines = %#v, want %#v", res.Lines, want)
	}
}

// A yank for a pane that is not there fails instead of returning an empty
// copy.
func TestYankActionMissingPaneFails(t *testing.T) {
	t.Parallel()
	s, _ := histServer(t, "yankmissing", 1)
	cl, conn := boxConn(t)
	s.handleReq(cl, wire.Msg{Type: "req", Action: "yank", Target: "ghost", ID: 2})
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	m, err := wire.ReadMsg(conn)
	if err != nil {
		t.Fatalf("yank reply: %v", err)
	}
	if m.OK {
		t.Fatal("yank succeeded for a missing pane")
	}
}
