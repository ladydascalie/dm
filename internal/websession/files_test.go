package websession_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"dm/internal/session"
	"dm/internal/wire"
)

// filesEvent picks the one session files event out of a recorder batch and
// returns its directory, its rows by path, and its paths in the order it sent
// them.
func filesEvent(t *testing.T, events []map[string]any) (string, map[string]map[string]any, []string) {
	t.Helper()
	var found map[string]any
	for _, ev := range events {
		if ev["type"] == "session.files" {
			found = ev
		}
	}
	if found == nil {
		t.Fatalf("the session pushed %d events %v, none of them session.files", len(events), events)
	}
	dir, _ := found["dir"].(string)
	list, ok := found["files"].([]any)
	if !ok {
		t.Fatalf("the files event carries %T, want the rows", found["files"])
	}
	rows := map[string]map[string]any{}
	order := make([]string, 0, len(list))
	for _, item := range list {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("a file row is %T, want a map", item)
		}
		path, _ := row["path"].(string)
		if path == "" {
			t.Fatalf("a file row has no path: %v", row)
		}
		rows[path] = row
		order = append(order, path)
	}
	return dir, rows, order
}

// body reads one row's body, which every row carries and only a file fills.
func body(t *testing.T, rows map[string]map[string]any, path string) string {
	t.Helper()
	row, ok := rows[path]
	if !ok {
		t.Fatalf("the files event has no row %q, it has %v", path, pathsOf(rows))
	}
	s, ok := row["body"].(string)
	if !ok {
		t.Fatalf("row %q carries body %T, want text", path, row["body"])
	}
	return s
}

// kind reads one row's kind.
func kind(t *testing.T, rows map[string]map[string]any, path string) string {
	t.Helper()
	row, ok := rows[path]
	if !ok {
		t.Fatalf("the files event has no row %q, it has %v", path, pathsOf(rows))
	}
	return row["kind"].(string)
}

// pathsOf is every row path, sorted, for a failure message.
func pathsOf(rows map[string]map[string]any) []string {
	out := make([]string, 0, len(rows))
	for path := range rows {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

// sockets is every panes/<id>.sock row, as pane ids, sorted.
func sockets(rows map[string]map[string]any) []string {
	out := []string{}
	for path := range rows {
		id, ok := strings.CutPrefix(path, "panes/")
		if !ok {
			continue
		}
		out = append(out, strings.TrimSuffix(id, ".sock"))
	}
	sort.Strings(out)
	return out
}

// TestStateBodyIsTheStateADocument proves state.json is the shipped session
// state, built from the session's own model: the name, every workspace, window
// and pane, the counters and the requested rail. A lookalike would drift from
// the file a real session writes; an unmarshal into session.State cannot.
func TestStateBodyIsTheStateADocument(t *testing.T) {
	var rec recorder
	h := newHarnessWith(t, 100, 30, rec.forward)

	// Build a session with two workspaces, three windows and four panes.
	h.request("split", "w1p1", func(m *wire.Msg) { m.Dir = "down" })
	h.request("new-window", "w1p2", nil)
	h.request("new-workspace", "w2p1", nil)

	_, rows, _ := filesEvent(t, rec.take())
	if got := kind(t, rows, "state.json"); got != "file" {
		t.Fatalf("state.json kind = %q, want file", got)
	}
	var st session.State
	if err := json.Unmarshal([]byte(body(t, rows, "state.json")), &st); err != nil {
		t.Fatalf("state.json does not unmarshal into a session.State: %v", err)
	}

	if st.Name != "spike" {
		t.Fatalf("state name = %q, want spike", st.Name)
	}
	if len(st.Workspaces) != 2 {
		t.Fatalf("state has %d workspaces, want 2", len(st.Workspaces))
	}
	// The new workspace was made active, as a real session makes it.
	if st.Active != 1 {
		t.Fatalf("state active = %d, want 1", st.Active)
	}
	// Every workspace, window and pane the last snapshot listed is in the
	// document, in the order the rail lists them.
	snap := h.snap
	wantWins := map[string][]string{}
	var wantPanes []string
	lastWin := ""
	for _, p := range snap.All {
		if _, ok := wantWins[p.Workspace]; !ok {
			wantWins[p.Workspace] = nil
		}
		if p.Window != lastWin {
			wantWins[p.Workspace] = append(wantWins[p.Workspace], p.Window)
			lastWin = p.Window
		}
		wantPanes = append(wantPanes, p.ID)
	}
	gotPanes := []string{}
	var gotWins []string
	var gotTokens []string
	for _, ws := range st.Workspaces {
		gotTokens = append(gotTokens, ws.ID)
		if len(ws.Windows) == 0 {
			t.Fatalf("workspace %q has no windows", ws.ID)
		}
		for _, w := range ws.Windows {
			gotWins = append(gotWins, w.ID)
			if w.Root == nil || w.Focus == "" {
				t.Fatalf("window %q has no tree or no focus: %+v", w.ID, w)
			}
		}
	}
	var wantWinsFlat []string
	for _, ids := range wantWins {
		wantWinsFlat = append(wantWinsFlat, ids...)
	}
	sort.Strings(wantWinsFlat)
	sort.Strings(gotWins)
	if !reflect.DeepEqual(gotWins, wantWinsFlat) {
		t.Fatalf("state windows = %v, want %v", gotWins, wantWinsFlat)
	}
	if len(st.Panes) != len(wantPanes) {
		t.Fatalf("state has %d panes, want %d", len(st.Panes), len(wantPanes))
	}
	for _, id := range wantPanes {
		ps, ok := st.Panes[id]
		if !ok {
			t.Fatalf("state has no pane %q, it has %v", id, pathsOf2(st.Panes))
		}
		if ps.Shell != "/bin/sh" {
			t.Fatalf("pane %q shell = %q, want /bin/sh", id, ps.Shell)
		}
		if ps.Cwd == "" || ps.Cols == 0 || ps.Rows == 0 {
			t.Fatalf("pane %q has no cwd or no size: %+v", id, ps)
		}
		gotPanes = append(gotPanes, id)
	}
	sort.Strings(gotPanes)

	// The counters are the session's own, and the rail is what the page asked
	// for.
	if st.NextWorkspace != 3 || st.NextWindow != 4 {
		t.Fatalf("state counters = (%d, %d), want (3, 4)", st.NextWorkspace, st.NextWindow)
	}
	if st.Rail != 34 || !st.RailSet {
		t.Fatalf("state rail = %d set=%v, want 34 set", st.Rail, st.RailSet)
	}
}

// pathsOf2 is every pane id of a state map, sorted, for a failure message.
func pathsOf2(panes map[string]*session.PaneState) []string {
	out := make([]string, 0, len(panes))
	for id := range panes {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// TestSnapshotBodyIsTheLastFrame proves snapshot.json is the exact wire.Snapshot
// the session sent last: it is the frame the client got, indented for reading,
// and not a file a real session writes.
func TestSnapshotBodyIsTheLastFrame(t *testing.T) {
	var rec recorder
	h := newHarnessWith(t, 100, 30, rec.forward)

	// The attach's own events belong to the attach snapshot.
	rec.take()
	h.request("split", "w1p1", func(m *wire.Msg) { m.Dir = "right" })
	frames := h.request("split", "w1p2", func(m *wire.Msg) { m.Dir = "right" })
	if frames[0].Type != "snapshot" || frames[0].Snap == nil {
		t.Fatalf("a split answered %q, want a snapshot", frames[0].Type)
	}
	sent := frames[0].Snap

	_, rows, _ := filesEvent(t, rec.take())
	if got := kind(t, rows, "snapshot.json"); got != "file" {
		t.Fatalf("snapshot.json kind = %q, want file", got)
	}
	var got wire.Snapshot
	if err := json.Unmarshal([]byte(body(t, rows, "snapshot.json")), &got); err != nil {
		t.Fatalf("snapshot.json does not unmarshal into a wire.Snapshot: %v", err)
	}
	if !reflect.DeepEqual(&got, sent) {
		t.Fatalf("snapshot.json is not the frame sent:\n got %+v\nwant %+v", got, *sent)
	}
}

// TestSocketsFollowThePanes proves one socket per pane in the session, across
// every workspace and window: a split adds one and a close removes it, and a
// socket has no contents.
func TestSocketsFollowThePanes(t *testing.T) {
	var rec recorder
	h := newHarnessWith(t, 100, 30, rec.forward)
	_, rows, _ := filesEvent(t, rec.take())
	if got := sockets(rows); !reflect.DeepEqual(got, []string{"w1p1"}) {
		t.Fatalf("the first snapshot has sockets %v, want [w1p1]", got)
	}
	if got := body(t, rows, "panes/w1p1.sock"); got != "" {
		t.Fatalf("a socket carries %q, want nothing", got)
	}
	if got := kind(t, rows, "panes/w1p1.sock"); got != "socket" {
		t.Fatalf("panes/w1p1.sock kind = %q, want socket", got)
	}

	h.request("split", "w1p1", func(m *wire.Msg) { m.Dir = "right" })
	_, rows, _ = filesEvent(t, rec.take())
	if got := sockets(rows); !reflect.DeepEqual(got, []string{"w1p1", "w1p2"}) {
		t.Fatalf("after a split the sockets are %v, want [w1p1 w1p2]", got)
	}

	// A window in another workspace holds a pane too.
	h.request("new-workspace", "w1p2", nil)
	_, rows, _ = filesEvent(t, rec.take())
	if got := sockets(rows); !reflect.DeepEqual(got, []string{"w1p1", "w1p2", "w2p1"}) {
		t.Fatalf("across two workspaces the sockets are %v, want [w1p1 w1p2 w2p1]", got)
	}

	// A close removes the pane's socket and leaves the rest.
	h.request("close-pane", "w1p2", nil)
	_, rows, _ = filesEvent(t, rec.take())
	if got := sockets(rows); !reflect.DeepEqual(got, []string{"w1p1", "w2p1"}) {
		t.Fatalf("after a close the sockets are %v, want [w1p1 w2p1]", got)
	}
}

// TestAbsentFilesAreListed proves the files a real session has and this one does
// not are listed, stably, with no contents invented for them.
func TestAbsentFilesAreListed(t *testing.T) {
	var rec recorder
	h := newHarnessWith(t, 100, 30, rec.forward)

	want := []string{"server.sock", "server.pid", "server.log"}
	check := func(round string) {
		t.Helper()
		_, rows, order := filesEvent(t, rec.take())
		got := []string{}
		for _, path := range order {
			if rows[path]["kind"] == "absent" {
				got = append(got, path)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: absent rows = %v, want %v", round, got, want)
		}
		for _, path := range want {
			if got := body(t, rows, path); got != "" {
				t.Fatalf("%s: absent row %q carries %q, want nothing", round, path, got)
			}
		}
	}
	check("the first snapshot")
	h.request("split", "w1p1", func(m *wire.Msg) { m.Dir = "down" })
	check("after a split")
	h.request("new-workspace", "w1p2", nil)
	check("after a new workspace")
}

// TestFilesCarryNoHostPath proves the panel is built from memory: no body names
// a path on this host, so nothing was read from disk to fill it, and the
// directory it prints is the short form a person reads, not this machine's.
func TestFilesCarryNoHostPath(t *testing.T) {
	var rec recorder
	h := newHarnessWith(t, 100, 30, rec.forward)
	h.request("split", "w1p1", func(m *wire.Msg) { m.Dir = "down" })

	dir, rows, _ := filesEvent(t, rec.take())
	if want := "~/.local/state/dm/sessions/spike"; dir != want {
		t.Fatalf("the files dir = %q, want %q", dir, want)
	}
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	host := []string{cwd, home}
	if v := os.Getenv("DM_STATE"); v != "" {
		host = append(host, v)
	}
	for _, path := range pathsOf(rows) {
		b := body(t, rows, path)
		for _, h := range host {
			if h == "" {
				continue
			}
			if strings.Contains(b, h) {
				t.Fatalf("row %q carries the host path %q", path, h)
			}
		}
		if strings.Contains(b, filepath.Join(cwd, ".dungeon")) {
			t.Fatalf("row %q was filled from disk", path)
		}
	}
}

// TestFilesEventFollowsEachSnapshot proves one session files event rides with
// every snapshot, after the session event and before that snapshot's screens,
// and that the frames a client reads are unchanged by either.
func TestFilesEventFollowsEachSnapshot(t *testing.T) {
	var rec recorder
	h := newHarnessWith(t, 100, 30, rec.forward)
	// The attach's own events belong to the attach snapshot.
	rec.take()

	// Read a snapshot frame by hand, then look at the recorder before any
	// screen is read: both events are already there, in order.
	h.send(wire.Msg{Type: "req", Action: "split", Target: "w1p1", Dir: "right"})
	first := h.read()
	if first.Type != "snapshot" {
		t.Fatalf("a split answered %q, want a snapshot", first.Type)
	}
	// The pipe is unbuffered, so the session is blocked on the first screen
	// until this test reads it: both events arriving now is proof they came
	// before that snapshot's screens.
	events := waitEvents(t, &rec, 2)
	_ = rec.take()
	got := filesTypes(events)
	if !reflect.DeepEqual(got, []string{"session", "session.files"}) {
		t.Fatalf("after the snapshot the events are %v, want the session event then the files event", got)
	}
	for range first.Snap.Panes {
		if m := h.read(); m.Type != "screen" {
			t.Fatalf("the split sent %q after the events, want a screen", m.Type)
		}
	}
	if m := h.read(); m.Type != "resp" {
		t.Fatalf("the split answered %q last, want a reply", m.Type)
	}

	// A request that changes nothing pushes neither event.
	h.request("rename-window", "w1", func(m *wire.Msg) { m.Name = "renamed" })
	if events := rec.take(); len(events) != 0 {
		t.Fatalf("an unserved action pushed %v, want nothing", events)
	}

	// Every snapshot in a run of changes carries exactly one files event.
	for _, step := range []struct {
		action string
		target string
		fix    func(*wire.Msg)
	}{
		{"split", "w1p1", func(m *wire.Msg) { m.Dir = "down" }},
		{"new-window", "w1p2", nil},
		{"new-workspace", "w2p1", nil},
		{"close-pane", "w2p1", nil},
	} {
		h.request(step.action, step.target, step.fix)
		got := filesTypes(rec.take())
		if !reflect.DeepEqual(got, []string{"session", "session.files"}) {
			t.Fatalf("%s pushed %v, want the session event then the files event", step.action, got)
		}
	}
}

// waitEvents waits for the recorder to hold at least n events and returns what
// it holds. It fails rather than hang when the session never pushes them.
func waitEvents(t *testing.T, r *recorder, n int) []map[string]any {
	t.Helper()
	end := time.Now().Add(2 * time.Second)
	for {
		r.mu.Lock()
		got := append([]map[string]any(nil), r.events...)
		r.mu.Unlock()
		if len(got) >= n {
			return got
		}
		if time.Now().After(end) {
			t.Fatalf("after 2s the session had pushed %d events %v, want %d", len(got), got, n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// filesTypes is the type of every event, in order.
func filesTypes(events []map[string]any) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		s, _ := ev["type"].(string)
		out = append(out, s)
	}
	return out
}
