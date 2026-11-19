package server

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dm/internal/box"
	"dm/internal/layout"
	"dm/internal/layoutdoc"
	"dm/internal/session"
	"dm/internal/wire"
)

// captureState is a live session with two workspaces, three windows and split
// trees, plus everything a layout must not carry.
func captureState(t *testing.T) *session.State {
	t.Helper()
	st := session.NewStateAt(t.TempDir(), "cap")
	st.Workspaces = []*session.Workspace{
		{
			ID: "s1", Name: "development", Project: "go-backend", Active: 1, Unfolded: true,
			Windows: []*session.Window{
				{
					ID: "w1", Name: "api", Named: true, Focus: "w1p2",
					Root: &layout.Node{Dir: "row", Sizes: []float64{0.7, 0.3}, Kids: []*layout.Node{
						{Pane: "w1p1"}, {Pane: "w1p2"},
					}},
				},
				{ID: "w2", Name: "logs", Focus: "w2p1", Root: &layout.Node{Pane: "w2p1"}},
			},
		},
		{
			ID: "s2", Name: "side", Active: 0, Unfolded: true,
			Windows: []*session.Window{{
				ID: "w3", Name: "notes", Focus: "w3p1",
				Root: &layout.Node{Dir: "col", Sizes: []float64{0.5, 0.5}, Kids: []*layout.Node{
					{Pane: "w3p1"}, {Pane: "w3p2"},
				}},
			}},
		},
	}
	st.Panes = map[string]*session.PaneState{
		"w1p1": {Cwd: "/work/api", Argv: []string{"pi"}, Shell: "/bin/zsh", Cols: 101, Rows: 31, App: "pi", Name: "agent-a", Reported: true},
		"w1p2": {Cwd: "/work/api", Shell: "/bin/zsh", Cols: 40, Rows: 31},
		"w2p1": {Cwd: "/work/logs", Argv: []string{"tail", "-f", "app.log"}, Shell: "/bin/zsh"},
		"w3p1": {Cwd: "/work/notes", Shell: "/bin/zsh"},
		"w3p2": {Cwd: "/work/notes", Shell: "/bin/zsh"},
		// No window holds this one, so it must not reach the document.
		"w9p9": {Cwd: "/work/gone", Shell: "/bin/zsh"},
	}
	st.Active = 0
	st.NextWorkspace, st.NextWindow = 2, 3
	st.Box = []box.Entry{{Seq: 7, From: "w1p1", Key: "lane/w1", Msg: "not in a layout"}}
	st.Secrets = []*session.Secret{{Name: "gh", Tool: session.ToolOp, Reference: "op://v/i/f"}}
	st.Rail, st.RailSet = 40, true
	st.ProjectFold = map[string]bool{"go-backend": true}
	st.ProjectAccent = map[string]int{"go-backend": 1}
	return st
}

// TestLayoutRPCCapture proves the layout action replies with the live shape:
// names, projects, active indices, trees, focus and the pane launch data, and
// nothing the session must keep to itself.
func TestLayoutRPCCapture(t *testing.T) {
	t.Parallel()
	st := captureState(t)
	s := railServer(st)
	cl, conn := boxConn(t)
	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "layout", ID: 1})
	if !m.OK {
		t.Fatalf("layout: %s", m.Error)
	}
	var res struct {
		Layout layoutdoc.Doc `json:"layout"`
	}
	if err := json.Unmarshal(m.Res, &res); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	doc := res.Layout
	if doc.Version != layoutdoc.Version || doc.Name != "cap" {
		t.Errorf("version/name = %d/%q, want %d/cap", doc.Version, doc.Name, layoutdoc.Version)
	}
	if len(doc.Workspaces) != 2 {
		t.Fatalf("got %d workspaces, want 2", len(doc.Workspaces))
	}
	dev := doc.Workspaces[0]
	if dev.Name != "development" || dev.Project != "go-backend" || dev.Active != 1 {
		t.Errorf("workspace 0 = %+v, want development/go-backend/1", dev)
	}
	if len(dev.Windows) != 2 {
		t.Fatalf("got %d windows in workspace 0, want 2", len(dev.Windows))
	}
	if w := dev.Windows[0]; w.Name != "api" || w.Focus != "w1p2" {
		t.Errorf("window 0 = %+v, want api focus w1p2", w)
	}
	w := dev.Windows[0]
	if w.Root == nil || w.Root.Dir != "row" || len(w.Root.Sizes) != 2 || w.Root.Sizes[0] != 0.7 || w.Root.Sizes[1] != 0.3 {
		t.Fatalf("tree = %+v, want row 0.7/0.3", w.Root)
	}
	if ids := layout.AllPanes(w.Root); len(ids) != 2 || ids[0] != "w1p1" || ids[1] != "w1p2" {
		t.Errorf("tree panes = %v, want the live ids in tree order", ids)
	}
	if got := doc.Workspaces[1]; got.Name != "side" || got.Project != "" || got.Active != 0 {
		t.Errorf("workspace 1 = %+v, want side with no project and active 0", got)
	}
	if got := doc.Workspaces[1].Windows[0].Root; got == nil || got.Dir != "col" {
		t.Errorf("workspace 1 tree = %+v, want a col split", got)
	}
	if p, ok := doc.Panes["w1p1"]; !ok || p.Cwd != "/work/api" || len(p.Argv) != 1 || p.Argv[0] != "pi" {
		t.Errorf("pane w1p1 = %+v, want /work/api with argv [pi]", p)
	}
	if p := doc.Panes["w2p1"]; p.Cwd != "/work/logs" || strings.Join(p.Argv, " ") != "tail -f app.log" {
		t.Errorf("pane w2p1 = %+v, want /work/logs with its argv", p)
	}
	if _, ok := doc.Panes["w9p9"]; ok {
		t.Error("a pane no window holds reached the document")
	}
	// The document must apply as it stands: capture never writes a shape that
	// Load would refuse.
	if err := doc.Validate(); err != nil {
		t.Fatalf("captured document is invalid: %v", err)
	}
	// Only the shape and the launch data travel.
	raw := string(m.Res)
	for _, key := range []string{
		"box", "boxSeq", "secrets", "rail", "railSet", "projectFold", "projectAccent",
		"reported", "app", "plugin_env", "shell", "cols", "rows", "upstream", "header",
	} {
		if strings.Contains(raw, `"`+key+`":`) {
			t.Errorf("the reply carries %q: %s", key, raw)
		}
	}
}

// TestFallbackCwd proves the seed rule for a saved folder that is gone: the
// nearest live ancestor, else the home directory, else nothing invented. A pane
// whose cwd is fine and a pane with no cwd are left alone, and one log line per
// moved pane names the pane and both directories.
func TestFallbackCwd(t *testing.T) {
	home := t.TempDir()
	live := t.TempDir()
	gone := filepath.Join(live, "gone")                         // never created
	orphan := filepath.Join("/", "dm-no-such-root-xyz", "gone") // only the root above it
	st := &session.State{Panes: map[string]*session.PaneState{
		"w1p1": {Cwd: live},
		"w1p2": {Cwd: gone},
		"w1p3": {Cwd: orphan},
		"w1p4": {Cwd: ""},
	}}

	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)

	fallbackCwd(st, home)

	if got := st.Panes["w1p1"].Cwd; got != live {
		t.Errorf("a live cwd moved: %q, want %q", got, live)
	}
	if got := st.Panes["w1p2"].Cwd; got != live {
		t.Errorf("deleted cwd = %q, want the nearest ancestor %q", got, live)
	}
	if got := st.Panes["w1p3"].Cwd; got != home {
		t.Errorf("orphan cwd = %q, want the home directory %q", got, home)
	}
	if got := st.Panes["w1p4"].Cwd; got != "" {
		t.Errorf("an empty cwd became %q, want it left empty", got)
	}
	out := logs.String()
	for _, want := range []string{"w1p2", gone, live, "w1p3", orphan, home} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q does not name %q", out, want)
		}
	}
	if n := strings.Count(out, "\n"); n != 2 {
		t.Errorf("logged %d lines, want one per moved pane: %q", n, out)
	}
}

// TestFallbackCwdNoHome proves the fallback invents nothing when the
// environment has no home directory: the pane keeps an empty cwd and starts
// where the server did.
func TestFallbackCwdNoHome(t *testing.T) {
	st := &session.State{Panes: map[string]*session.PaneState{
		"w1p1": {Cwd: filepath.Join("/", "dm-no-such-root-xyz", "gone")},
	}}
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)

	fallbackCwd(st, "")

	if got := st.Panes["w1p1"].Cwd; got != "" {
		t.Fatalf("cwd = %q, want it left empty with no home directory", got)
	}
	if !strings.Contains(logs.String(), "w1p1") {
		t.Errorf("log %q does not name the pane", logs.String())
	}
}

// seedDoc is a two-pane column over doc-local ids, with a cwd and an argv on
// each pane. The cwds are real directories, so the seed path leaves them alone:
// TestFallbackCwd owns the deleted-directory case.
func seedDoc(t *testing.T) *layoutdoc.Doc {
	t.Helper()
	api, logs := t.TempDir(), t.TempDir()
	return &layoutdoc.Doc{
		Version: layoutdoc.Version,
		Name:    "seed",
		Workspaces: []layoutdoc.Workspace{{
			Name:   "work",
			Active: 0,
			Windows: []layoutdoc.Window{{
				Name:  "api",
				Focus: "one",
				Root: &layout.Node{Dir: "col", Sizes: []float64{0.6, 0.4}, Kids: []*layout.Node{
					{Pane: "one"}, {Pane: "two"},
				}},
			}},
		}},
		Panes: map[string]layoutdoc.Pane{
			"one": {Cwd: api, Argv: []string{"pi"}},
			"two": {Cwd: logs, Argv: []string{"tail", "-f", "app.log"}},
		},
	}
}

// TestLoadStateSeedsFromLayout proves a session with no state file starts as
// the layout: ids from fresh counters in tree order, the launch data intact and
// the state on disk before anything spawns.
func TestLoadStateSeedsFromLayout(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "seed.json")
	seed := seedDoc(t)
	if err := layoutdoc.Save(path, seed); err != nil {
		t.Fatalf("Save: %v", err)
	}
	st, fromDisk, err := loadState(root, "seeded", path)
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	if fromDisk {
		t.Error("loadState reported a state file the session does not have")
	}
	if len(st.Workspaces) != 1 || st.Workspaces[0].Name != "work" {
		t.Fatalf("workspaces = %+v, want one called work", st.Workspaces)
	}
	w := st.Workspaces[0].Windows[0]
	if ids := layout.AllPanes(w.Root); strings.Join(ids, ",") != "w1p1,w1p2" {
		t.Fatalf("pane ids = %v, want w1p1,w1p2 in tree order", ids)
	}
	if w.Focus != "w1p1" {
		t.Errorf("focus = %q, want w1p1", w.Focus)
	}
	if p := st.Panes["w1p1"]; p == nil || p.Cwd != seed.Panes["one"].Cwd || strings.Join(p.Argv, " ") != "pi" {
		t.Errorf("w1p1 = %+v, want %q with argv pi", st.Panes["w1p1"], seed.Panes["one"].Cwd)
	}
	if p := st.Panes["w1p2"]; p == nil || p.Cwd != seed.Panes["two"].Cwd || strings.Join(p.Argv, " ") != "tail -f app.log" {
		t.Errorf("w1p2 = %+v, want %q with its argv", st.Panes["w1p2"], seed.Panes["two"].Cwd)
	}
	for id, p := range st.Panes {
		if p.Shell == "" || p.Cols != 80 || p.Rows != 24 {
			t.Errorf("pane %s = %+v, want the default shell and an 80x24 start", id, p)
		}
	}
	if st.NextWorkspace != 1 || st.NextWindow != 1 {
		t.Errorf("counters = %d/%d, want 1/1", st.NextWorkspace, st.NextWindow)
	}
	// The shape is on disk before any pane spawns.
	saved, err := session.LoadStateAt(root, "seeded")
	if err != nil {
		t.Fatalf("load the saved state: %v", err)
	}
	if ids := layout.AllPanes(saved.Workspaces[0].Windows[0].Root); strings.Join(ids, ",") != "w1p1,w1p2" {
		t.Fatalf("saved pane ids = %v, want w1p1,w1p2", ids)
	}
}

// TestLoadStateIgnoresLayoutWhenStateExists proves a live state wins over the
// flag, with one log line saying so.
func TestLoadStateIgnoresLayoutWhenStateExists(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "seed.json")
	if err := layoutdoc.Save(path, seedDoc(t)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, _, err := loadState(root, "seeded", path); err != nil {
		t.Fatalf("first boot: %v", err)
	}
	// Replace the file with a shape that would be obvious if it applied.
	other := &layoutdoc.Doc{
		Version: layoutdoc.Version,
		Name:    "other",
		Workspaces: []layoutdoc.Workspace{{
			Name:    "replaced",
			Windows: []layoutdoc.Window{{Name: "solo", Focus: "solo", Root: &layout.Node{Pane: "solo"}}},
		}},
		Panes: map[string]layoutdoc.Pane{"solo": {Cwd: "/elsewhere"}},
	}
	if err := layoutdoc.Save(path, other); err != nil {
		t.Fatalf("Save: %v", err)
	}
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)

	st, fromDisk, err := loadState(root, "seeded", path)
	if err != nil {
		t.Fatalf("second boot: %v", err)
	}
	if !fromDisk {
		t.Error("second boot did not read the state file")
	}
	if got := st.Workspaces[0]; got.Name != "work" {
		t.Fatalf("workspace = %q, want work from the state file, not the layout", got.Name)
	}
	if ids := layout.AllPanes(st.Workspaces[0].Windows[0].Root); strings.Join(ids, ",") != "w1p1,w1p2" {
		t.Fatalf("pane ids = %v, want the saved w1p1,w1p2", ids)
	}
	if !strings.Contains(logs.String(), "ignored") || !strings.Contains(logs.String(), path) {
		t.Fatalf("log = %q, want one line naming the ignored file", logs.String())
	}
}

// TestRunFailsOnBadLayout proves a document that does not load stops Run with
// an error naming the file and the field, before the socket is served.
func TestRunFailsOnBadLayout(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	path := filepath.Join(t.TempDir(), "bad.json")
	body := `{"version":1,"name":"bad","workspaces":[{"name":"w","active":0,"windows":[{"name":"api","focus":"p","root":{"dir":"row"}}]}],"panes":{}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Run([]string{"-s", "badlayout", "-layout", path})
	if err == nil {
		t.Fatal("Run accepted a window whose tree has no leaf")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the file", err)
	}
	if !strings.Contains(err.Error(), "no pane leaf") {
		t.Errorf("error %q does not name the field", err)
	}
	if _, statErr := os.Stat(session.NewStateAt(os.Getenv("DM_STATE"), "badlayout").StatePath()); !os.IsNotExist(statErr) {
		t.Errorf("a refused layout left a state file: %v", statErr)
	}
}
