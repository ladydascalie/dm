package server

import (
	"encoding/json"
	"testing"
	"time"

	"dm/internal/wire"
)

// TestAllPaneSession proves a session a pane reports on its control socket
// reaches the row the rail reads, through the server's own snapshot.
func TestAllPaneSession(t *testing.T) {
	t.Parallel()
	f := newRowsFixture(t, "session")
	f.send(t, wire.Msg{Type: "session", Res: json.RawMessage(
		`{"sessionFile":"/home/me/.pi/sessions/9f2.jsonl","sessionID":"9f2"}`)})
	snap := f.waitSnapshot(t, "the session", func(s wire.Snapshot) bool {
		row := findRow(s, f.id)
		return row != nil && row.SessionFile == "/home/me/.pi/sessions/9f2.jsonl"
	})
	row := findRow(snap, f.id)
	if row.SessionID != "9f2" {
		t.Fatalf("SessionID = %q, want 9f2", row.SessionID)
	}
}

// TestAllPaneSessionDroppedOnExit proves the session belongs to the running
// pane: the exit drops it with the pane, so no row keeps a file for an id that
// is gone.
func TestAllPaneSessionDroppedOnExit(t *testing.T) {
	// Serial: the notify gate reads DM_NOTIFY, env t.Setenv cannot
	// parallelise.
	f := newRowsExitFixture(t, "session-exit")
	f.send(t, wire.Msg{Type: "session", Res: json.RawMessage(
		`{"sessionFile":"/home/me/.pi/sessions/9f2.jsonl","sessionID":"9f2"}`)})
	f.waitSnapshot(t, "the session", func(s wire.Snapshot) bool {
		row := findRow(s, f.id)
		return row != nil && row.SessionFile == "/home/me/.pi/sessions/9f2.jsonl"
	})

	f.send(t, wire.Msg{Type: "exit"})
	snap := f.waitSnapshot(t, "the exited pane dropped", func(s wire.Snapshot) bool {
		return len(s.All) == 1 && findRow(s, f.id) == nil
	})
	for _, row := range snap.All {
		if row.SessionFile != "" || row.SessionID != "" {
			t.Fatalf("a row kept a session after the exit: %+v", row)
		}
	}

	// The conn is where the value lived, so it goes with the pane.
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.s.mu.Lock()
		_, left := f.s.panes[f.id]
		f.s.mu.Unlock()
		if !left {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the exited pane kept its conn")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// findRow returns the AllPane row for id, nil when the snapshot has none.
func findRow(s wire.Snapshot, id string) *wire.AllPane {
	for i := range s.All {
		if s.All[i].ID == id {
			return &s.All[i]
		}
	}
	return nil
}
