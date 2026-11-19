package websession_test

import (
	"sync"
	"testing"

	"dm/internal/wire"
)

// recorder hears the session's forward callback. The session pushes events from
// its own goroutine, so a test reads them under the recorder's lock.
type recorder struct {
	mu     sync.Mutex
	events []map[string]any
}

// forward records one event.
func (r *recorder) forward(ev map[string]any) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

// take empties the recorder and returns what it heard.
func (r *recorder) take() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.events
	r.events = nil
	return out
}

// frameTypes is the type of every frame, in order.
func frameTypes(frames []wire.Msg) []string {
	out := make([]string, 0, len(frames))
	for _, m := range frames {
		out = append(out, m.Type)
	}
	return out
}

// checkSession asserts one session event carries these counts. The event is the
// page's coach: it is counted from the session's own model, and it says nothing
// else. Every snapshot carries one of these and one session files event, so a
// batch of two is one snapshot's worth.
func checkSession(t *testing.T, events []map[string]any, workspaces, windows, panes int, focus string) {
	t.Helper()
	var session []map[string]any
	files := 0
	for _, ev := range events {
		switch ev["type"] {
		case "session":
			session = append(session, ev)
		case "session.files":
			files++
		}
	}
	if len(session) != 1 || files != 1 || len(events) != 2 {
		t.Fatalf("the session pushed %d events %v, want one session event and one session files event", len(events), events)
	}
	ev := session[0]
	want := map[string]any{"workspaces": workspaces, "windows": windows, "panes": panes, "focus": focus}
	for key, value := range want {
		if got := ev[key]; got != value {
			t.Fatalf("the session event's %s = %v, want %v (event %v)", key, got, value, ev)
		}
	}
	if len(ev) != 5 {
		t.Fatalf("the session event carries %d fields %v, want type and the four counts", len(ev), ev)
	}
}

// TestEverySnapshotPushesOneSessionEvent proves one session event rides with
// every snapshot and no event rides without one, counted from the session's own
// model, and that the frames a client reads are unchanged by it.
func TestEverySnapshotPushesOneSessionEvent(t *testing.T) {
	var rec recorder
	h := newHarnessWith(t, 100, 30, rec.forward)

	// The attach snapshot: one workspace holding one window with one pane.
	checkSession(t, rec.take(), 1, 1, 1, "w1p1")

	// A split pushes one snapshot, the screens behind it and the reply, and
	// nothing else on the wire.
	frames := h.request("split", "w1p1", func(m *wire.Msg) { m.Dir = "right" })
	only(t, "the frames a split sends", frameTypes(frames), "snapshot", "screen", "screen", "resp")
	only(t, "the split screens", screenTargets(frames), "w1p1", "w1p2")
	checkSession(t, rec.take(), 1, 1, 2, "w1p2")

	// A new window: two windows in the session, three panes in all, and the
	// focus on the new window's pane.
	frames = h.request("new-window", "w1p2", nil)
	only(t, "the frames a new window sends", frameTypes(frames), "snapshot", "screen", "resp")
	checkSession(t, rec.take(), 1, 2, 3, "w2p1")

	// A new workspace: two workspaces, three windows, four panes.
	frames = h.request("new-workspace", "w2p1", nil)
	only(t, "the frames a new workspace sends", frameTypes(frames), "snapshot", "screen", "resp")
	checkSession(t, rec.take(), 2, 3, 4, "w3p1")

	// A focus into another workspace counts the same session.
	frames = h.request("focus", "w1p1", nil)
	only(t, "the frames a focus sends", frameTypes(frames), "snapshot", "screen", "screen", "resp")
	checkSession(t, rec.take(), 2, 3, 4, "w1p1")

	// A request that changes nothing pushes no snapshot and no event.
	frames = h.request("rename-window", "w1", func(m *wire.Msg) { m.Name = "renamed" })
	only(t, "the frames an unserved action sends", frameTypes(frames), "resp")
	if events := rec.take(); len(events) != 0 {
		t.Fatalf("an unserved action pushed %v, want nothing", events)
	}

	// A resize pushes one snapshot and one event, and the counts survive it.
	h.send(wire.Msg{Type: "resize", Cols: 80, Rows: 24})
	first := h.read()
	if first.Type != "snapshot" {
		t.Fatalf("resize answered %q, want a snapshot", first.Type)
	}
	for range first.Snap.Panes {
		if m := h.read(); m.Type != "screen" {
			t.Fatalf("the resize sent %q after the snapshot", m.Type)
		}
	}
	checkSession(t, rec.take(), 2, 3, 4, "w1p1")
}
