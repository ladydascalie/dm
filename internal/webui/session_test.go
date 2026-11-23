package webui

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dm/internal/wire"
)

// testdataDir is the fixture tree the session tests read.
func testdataDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// sessionServe starts a handler whose list reply carries all as the session's
// panes, reading session files under sessionsDir.
func sessionServe(t *testing.T, sessionsDir string, all []wire.AllPane) *httptest.Server {
	t.Helper()
	dir := root(t, "main")
	fakeSession(t, dir, "main", wire.Snapshot{Session: "main", All: all}, nil)
	cfg := Config{Session: "main", Root: dir, Token: "t", SessionsDir: sessionsDir}
	srv := httptest.NewServer(handler(cfg))
	t.Cleanup(srv.Close)
	return srv
}

// errorBody decodes the {"error": ...} body a refusal answers.
func errorBody(t *testing.T, res *http.Response) string {
	t.Helper()
	defer func() { _ = res.Body.Close() }()
	var e struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&e); err != nil {
		t.Fatal(err)
	}
	return e.Error
}

// TestFleetCarriesProject proves the fleet row carries the rail's project and
// accent, so the page can group by project instead of a nameless workspace.
func TestFleetCarriesProject(t *testing.T) {
	t.Parallel()
	dir := root(t, "main")
	snap := wire.Snapshot{Session: "main", All: []wire.AllPane{{
		ID: "w1p1", Workspace: "s1", WorkspaceName: "main", Project: "dm", ProjectAccent: 3,
	}}}
	fakeSession(t, dir, "main", snap, nil)
	srv := httptest.NewServer(handler(Config{Session: "main", Root: dir, Token: "t"}))
	defer srv.Close()

	res := get(t, srv.URL, "/api/fleet?t=t")
	defer func() { _ = res.Body.Close() }()
	var got fleetReply
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Panes) != 1 {
		t.Fatalf("%d panes, want 1", len(got.Panes))
	}
	if p := got.Panes[0]; p.Project != "dm" || p.ProjectAccent != 3 {
		t.Fatalf("project = %q accent = %d, want dm and 3", p.Project, p.ProjectAccent)
	}
}

// TestSessionTurns proves the payload renders the fixture's last three turns:
// the step order, the paired results, the error flag, the output cut and the
// summary numbers.
func TestSessionTurns(t *testing.T) {
	t.Parallel()
	file := filepath.Join(testdataDir(t), "session.jsonl")
	srv := sessionServe(t, testdataDir(t), []wire.AllPane{{ID: "w122p1", SessionFile: file}})

	res := get(t, srv.URL, "/api/session?pane=w122p1&t=t")
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var got sessionReply
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Pane != "w122p1" || got.SessionID != "sess-1" || got.Rev == "" || !got.Changed {
		t.Fatalf("reply header = %+v", got)
	}
	if len(got.Turns) != sessionTail {
		t.Fatalf("%d turns, want the tail of %d", len(got.Turns), sessionTail)
	}

	turn := got.Turns[0]
	if turn.At != "2026-11-23T10:01:00Z" || turn.Ask != "list the pi session dir" {
		t.Fatalf("turn header = %+v", turn)
	}
	var kinds []string
	for _, s := range turn.Steps {
		kinds = append(kinds, s.Kind)
	}
	if strings.Join(kinds, ",") != "thinking,text,tool,tool" {
		t.Fatalf("step order = %v", kinds)
	}
	if s := turn.Steps[0]; s.MS != 6000 {
		t.Fatalf("thinking ms = %d, want the assistant duration 6000", s.MS)
	}
	if s := turn.Steps[1]; s.Text != "Running it now.\n" {
		t.Fatalf("text = %q", s.Text)
	}

	bash := turn.Steps[2]
	if bash.Tool != "bash" || bash.Summary != "ls ~/.pi/agent/" || bash.MS != 200 {
		t.Fatalf("bash step = %+v", bash)
	}
	if bash.OK == nil || *bash.OK {
		t.Fatalf("bash ok = %v, want the error result", bash.OK)
	}
	if bash.Exit == nil || *bash.Exit != 1 {
		t.Fatalf("bash exit = %v, want 1 from the command's status line", bash.Exit)
	}
	if bash.Output != "boom\n\nCommand exited with code 1" || bash.More != 0 {
		t.Fatalf("bash output = %q more = %d", bash.Output, bash.More)
	}

	read := turn.Steps[3]
	if read.Tool != "read" || read.Summary != "/home/b/.pi/agent/sessions/s.jsonl" || read.MS != 30 {
		t.Fatalf("read step = %+v", read)
	}
	if read.OK == nil || !*read.OK {
		t.Fatalf("read ok = %v, want true", read.OK)
	}
	if read.Exit != nil {
		t.Fatalf("read exit = %v, want none: a non-bash tool has no exit code", *read.Exit)
	}
	if n := len([]rune(read.Output)); n != stepOutput {
		t.Fatalf("read output = %d chars, want the %d-char cut", n, stepOutput)
	}
	if read.More != 12 {
		t.Fatalf("read more = %d, want the 12 lines left over", read.More)
	}
	if turn.Live != nil {
		t.Fatalf("live = %+v, want none on a settled turn", turn.Live)
	}

	want := sessionSummary{Calls: 2, Errors: 1, ToolMs: 230, Cost: 0.002, Model: "test/model-a"}
	if diff := math.Abs(turn.Summary.Cost - want.Cost); diff > 1e-12 {
		t.Fatalf("cost = %v, want %v", turn.Summary.Cost, want.Cost)
	}
	turn.Summary.Cost = want.Cost
	if turn.Summary != want {
		t.Fatalf("summary = %+v, want %+v", turn.Summary, want)
	}

	// The third turn pairs a write with a clean result and has no exit code.
	write := got.Turns[1]
	if write.Summary.Calls != 1 || write.Summary.Errors != 0 ||
		write.Summary.ToolMs != 50 || write.Summary.Model != "test/model-b" ||
		math.Abs(write.Summary.Cost-0.001) > 1e-12 {
		t.Fatalf("second summary = %+v", write.Summary)
	}
	if len(write.Steps) != 2 || write.Steps[1].Tool != "write" ||
		write.Steps[1].Summary != "/tmp/x" ||
		write.Steps[1].OK == nil || !*write.Steps[1].OK || write.Steps[1].Exit != nil {
		t.Fatalf("write steps = %+v", write.Steps)
	}

	// The newest turn carries a plain string user content and one text step.
	last := got.Turns[2]
	if last.Ask != "thanks" || len(last.Steps) != 1 || last.Steps[0].Text != "done\n" {
		t.Fatalf("last turn = %+v", last)
	}
}

// TestSessionRevTracksGrowth proves rev agrees across two polls of an unchanged
// file and changes when the file grows.
func TestSessionRevTracksGrowth(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fixture, err := os.ReadFile(filepath.Join(testdataDir(t), "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(file, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	srv := sessionServe(t, dir, []wire.AllPane{{ID: "w122p1", SessionFile: file}})

	poll := func(query string) sessionReply {
		t.Helper()
		res := get(t, srv.URL, query)
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status %d", res.StatusCode)
		}
		var out sessionReply
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	first := poll("/api/session?pane=w122p1&t=t")
	if first.Rev == "" || !first.Changed {
		t.Fatalf("first poll = %+v", first)
	}
	again := poll("/api/session?pane=w122p1&t=t&since=" + first.Rev)
	if again.Rev != first.Rev {
		t.Fatalf("rev moved on an unchanged file: %q then %q", first.Rev, again.Rev)
	}
	if again.Changed {
		t.Fatal("changed = true for the rev the caller already has")
	}

	grow := append(fixture, []byte(`{"type":"message","timestamp":"2026-11-23T10:04:00.000Z","message":{"role":"user","content":"one more"}}`+"\n")...)
	if err := os.WriteFile(file, grow, 0o600); err != nil {
		t.Fatal(err)
	}
	after := poll("/api/session?pane=w122p1&t=t&since=" + first.Rev)
	if after.Rev == first.Rev {
		t.Fatal("rev did not move when the file grew")
	}
	if !after.Changed {
		t.Fatal("changed = false after the file grew")
	}
}

// TestSessionRefusals proves every refused read: a pane with no session, a bad
// pane id, a file outside the sessions tree (including through a symlink) and a
// file that is not JSONL.
func TestSessionRefusals(t *testing.T) {
	t.Parallel()
	data := testdataDir(t)

	// A pane the snapshot lists with no session file, and a pane it does not
	// list at all, both answer the same 404.
	for _, tc := range []struct {
		name string
		all  []wire.AllPane
	}{
		{"no file", []wire.AllPane{{ID: "w122p1"}}},
		{"no pane", []wire.AllPane{{ID: "w9p9", SessionFile: "/x/y.jsonl"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := sessionServe(t, data, tc.all)
			res := get(t, srv.URL, "/api/session?pane=w122p1&t=t")
			if res.StatusCode != http.StatusNotFound {
				t.Fatalf("status %d, want 404", res.StatusCode)
			}
			if got := errorBody(t, res); got != "no session" {
				t.Fatalf("error = %q, want no session", got)
			}
		})
	}

	t.Run("bad pane", func(t *testing.T) {
		t.Parallel()
		srv := sessionServe(t, data, []wire.AllPane{{ID: "w122p1"}})
		for _, pane := range []string{"", "bogus", "../../etc/passwd", "w1p1;rm"} {
			res := get(t, srv.URL, "/api/session?pane="+pane+"&t=t")
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("pane %q: status %d, want 400", pane, res.StatusCode)
			}
			_ = errorBody(t, res)
		}
	})

	t.Run("outside tree", func(t *testing.T) {
		t.Parallel()
		outside := filepath.Join(t.TempDir(), "outside.jsonl")
		canary := `{"type":"session","id":"outside"}` + "\n"
		if err := os.WriteFile(outside, []byte(canary), 0o600); err != nil {
			t.Fatal(err)
		}
		srv := sessionServe(t, data, []wire.AllPane{{ID: "w122p1", SessionFile: outside}})
		res := get(t, srv.URL, "/api/session?pane=w122p1&t=t")
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("status %d, want 403", res.StatusCode)
		}
		if got := errorBody(t, res); got != "session file refused" {
			t.Fatalf("error = %q, want the refusal", got)
		}
	})

	t.Run("symlink out", func(t *testing.T) {
		t.Parallel()
		rootDir := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside.jsonl")
		canary := `{"type":"session","id":"outside"}` + "\n"
		if err := os.WriteFile(outside, []byte(canary), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(rootDir, "link.jsonl")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("symlink: %v", err)
		}
		srv := sessionServe(t, rootDir, []wire.AllPane{{ID: "w122p1", SessionFile: link}})
		res := get(t, srv.URL, "/api/session?pane=w122p1&t=t")
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("status %d, want 403 for a link out of the tree", res.StatusCode)
		}
		_ = errorBody(t, res)
	})

	t.Run("not JSONL", func(t *testing.T) {
		t.Parallel()
		file := filepath.Join(data, "bad.jsonl")
		srv := sessionServe(t, data, []wire.AllPane{{ID: "w122p1", SessionFile: file}})
		res := get(t, srv.URL, "/api/session?pane=w122p1&t=t")
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("status %d, want 422", res.StatusCode)
		}
		if got := errorBody(t, res); !strings.HasPrefix(got, "session file is not JSONL") {
			t.Fatalf("error = %q, want the JSONL refusal", got)
		}
	})
}

// TestSessionLiveStep proves the newest tool call without a result is the live
// step, and it carries no ok flag yet.
func TestSessionLiveStep(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "live.jsonl")
	lines := []string{
		`{"type":"session","id":"live-1"}`,
		`{"type":"message","timestamp":"2026-11-23T11:00:00Z","message":{"role":"user","content":"go"}}`,
		`{"type":"message","timestamp":"2026-11-23T11:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"checking"},{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"sleep 60"}}],"model":"test/model-live"}}`,
	}
	if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := sessionServe(t, dir, []wire.AllPane{{ID: "w122p1", SessionFile: file}})

	res := get(t, srv.URL, "/api/session?pane=w122p1&t=t")
	defer func() { _ = res.Body.Close() }()
	var got sessionReply
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Turns) != 1 {
		t.Fatalf("%d turns, want 1", len(got.Turns))
	}
	live := got.Turns[0].Live
	if live == nil || live.Tool != "bash" || live.Summary != "sleep 60" || live.OK != nil {
		t.Fatalf("live = %+v, want the unanswered bash call", live)
	}
	if s := got.Turns[0].Summary; s.Calls != 1 || s.Errors != 0 || s.ToolMs != 0 || s.Model != "test/model-live" {
		t.Fatalf("summary = %+v", s)
	}
}
