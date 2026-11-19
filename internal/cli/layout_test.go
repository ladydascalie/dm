package cli

// The layout verbs and the -L attach path: a fake server answers the capture
// action, and the files land under a temp XDG_CONFIG_HOME.

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/layoutdoc"
	"dm/internal/paths"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// captureReply is the JSON body a fake server answers the layout action with:
// one workspace, one window and a two-pane column.
func captureReply(t *testing.T) string {
	t.Helper()
	doc := layoutdoc.Doc{
		Version: layoutdoc.Version,
		Name:    "main",
		Workspaces: []layoutdoc.Workspace{{
			Name:    "development",
			Project: "go-backend",
			Active:  0,
			Windows: []layoutdoc.Window{{
				Name:  "api",
				Focus: "w1p1",
				Root: &layout.Node{Dir: "col", Sizes: []float64{0.6, 0.4}, Kids: []*layout.Node{
					{Pane: "w1p1"}, {Pane: "w1p2"},
				}},
			}},
		}},
		Panes: map[string]layoutdoc.Pane{
			"w1p1": {Cwd: "/work/api", Argv: []string{"pi"}},
			"w1p2": {Cwd: "/work/api"},
		},
	}
	b, err := json.Marshal(map[string]any{"layout": doc})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// captureServer starts the fake server and returns the requests it received.
func captureServer(t *testing.T, session string) *[]wire.Msg {
	t.Helper()
	got := &[]wire.Msg{}
	reply := json.RawMessage(captureReply(t))
	startFakeServer(t, session, func(_ net.Conn, req wire.Msg) (wire.Msg, bool) {
		*got = append(*got, req)
		return wire.Msg{OK: true, Res: reply}, true
	})
	return got
}

// TestLayoutSaveWritesTheCapturedShape proves save asks the session for the
// live shape, writes it as the named layout and prints the path.
func TestLayoutSaveWritesTheCapturedShape(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	got := captureServer(t, "main")
	var err error
	out := testutil.CaptureStdout(t, func() {
		err = Run([]string{"layout", "save", "go-backend"})
	})
	if err != nil {
		t.Fatalf("dm layout save: %v", err)
	}
	if len(*got) != 1 || (*got)[0].Action != "layout" {
		t.Fatalf("save sent %+v, want one layout action", *got)
	}
	path, pathErr := layoutdoc.Path("go-backend")
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	if strings.TrimSpace(out) != path {
		t.Fatalf("save printed %q, want the path %q", out, path)
	}
	doc, loadErr := layoutdoc.Load(path)
	if loadErr != nil {
		t.Fatalf("the saved file does not load: %v", loadErr)
	}
	if doc.Name != "go-backend" {
		t.Errorf("name = %q, want the layout name", doc.Name)
	}
	ws := doc.Workspaces[0]
	if ws.Name != "development" || ws.Project != "go-backend" {
		t.Errorf("workspace = %+v, want development/go-backend", ws)
	}
	w := ws.Windows[0]
	if w.Name != "api" || w.Focus != "w1p1" || w.Root.Dir != "col" {
		t.Errorf("window = %+v, want api focus w1p1 over a col split", w)
	}
	if p := doc.Panes["w1p1"]; p.Cwd != "/work/api" || strings.Join(p.Argv, " ") != "pi" {
		t.Errorf("pane w1p1 = %+v, want /work/api with argv pi", p)
	}
}

// TestLayoutSaveRefusesAnExistingFile proves a save is not a silent overwrite,
// and that --force is.
func TestLayoutSaveRefusesAnExistingFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	captureServer(t, "main")
	path, err := layoutdoc.Path("go-backend")
	if err != nil {
		t.Fatal(err)
	}
	var first error
	testutil.CaptureStdout(t, func() { first = Run([]string{"layout", "save", "go-backend"}) })
	if first != nil {
		t.Fatalf("first save: %v", first)
	}
	// The file is now hand-edited: --force must replace it, and a plain save
	// must leave the edit alone.
	edited := []byte("{\"version\":1,\"name\":\"edited\",\"workspaces\":[],\"panes\":{}}\n")
	if err := os.WriteFile(path, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	var second error
	out := testutil.CaptureStdout(t, func() { second = Run([]string{"layout", "save", "go-backend"}) })
	if second == nil || !strings.Contains(second.Error(), "already exists") || !strings.Contains(second.Error(), "--force") {
		t.Fatalf("second save = %v, want a refusal naming --force", second)
	}
	if out != "" {
		t.Errorf("a refused save printed %q", out)
	}
	kept, err := os.ReadFile(path)
	if err != nil || string(kept) != string(edited) {
		t.Fatalf("a refused save changed the file: %v", err)
	}
	var forced error
	testutil.CaptureStdout(t, func() { forced = Run([]string{"layout", "save", "go-backend", "--force"}) })
	if forced != nil {
		t.Fatalf("save --force: %v", forced)
	}
	doc, err := layoutdoc.Load(path)
	if err != nil {
		t.Fatalf("the forced save does not load: %v", err)
	}
	if doc.Name != "go-backend" {
		t.Fatalf("name = %q, want the saved layout name", doc.Name)
	}
}

// TestLayoutSaveRefusesABadName proves the name rule is checked before the
// session is dialed: no server runs here, so a dial would fail its own way.
func TestLayoutSaveRefusesABadName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DM_STATE", t.TempDir())
	// A leading - never reaches the name rule: the flag parser refuses it
	// first, the way it does for every verb. layoutdoc.ValidName is covered in
	// its own package.
	for _, name := range []string{"has space", "has/slash", ""} {
		err := Run([]string{"layout", "save", name})
		if err == nil {
			t.Fatalf("dm layout save %q was accepted", name)
		}
		if !strings.Contains(err.Error(), "letters, digits, _ or -") {
			t.Errorf("dm layout save %q = %v, want the name rule", name, err)
		}
	}
}

// TestLayoutLs proves the list prints a row per file, with counts for a good
// file and the error in place of the counts for a broken one.
func TestLayoutLs(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"layout", "ls"}); err != nil {
			t.Fatalf("dm layout ls: %v", err)
		}
	})
	if !strings.Contains(out, "no layouts") {
		t.Fatalf("empty list printed %q", out)
	}
	path, err := layoutdoc.Path("go-backend")
	if err != nil {
		t.Fatal(err)
	}
	doc := layoutdoc.Doc{
		Version: layoutdoc.Version,
		Name:    "go-backend",
		Workspaces: []layoutdoc.Workspace{
			{Name: "one", Windows: []layoutdoc.Window{{Name: "a", Focus: "p", Root: &layout.Node{Pane: "p"}}}},
			{Name: "two", Windows: []layoutdoc.Window{
				{Name: "b", Focus: "q", Root: &layout.Node{Pane: "q"}},
				{Name: "c", Focus: "r", Root: &layout.Node{Pane: "r"}},
			}},
		},
		Panes: map[string]layoutdoc.Pane{"p": {}, "q": {}, "r": {}},
	}
	if err := layoutdoc.Save(path, &doc); err != nil {
		t.Fatal(err)
	}
	dir, err := layoutdoc.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	out = testutil.CaptureStdout(t, func() {
		if err := Run([]string{"layout", "ls"}); err != nil {
			t.Fatalf("dm layout ls: %v", err)
		}
	})
	var good, bad string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		switch {
		case strings.HasPrefix(line, "go-backend"):
			good = line
		case strings.HasPrefix(line, "broken"):
			bad = line
		}
	}
	if !strings.Contains(good, "2") || !strings.Contains(good, "3") {
		t.Errorf("good row = %q, want the 2 workspace and 3 window counts", good)
	}
	if !strings.Contains(bad, "broken.json") {
		t.Errorf("broken row = %q, want the decode error in place of the counts", bad)
	}
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "MODIFIED") {
		t.Errorf("list header missing: %q", out)
	}
}

// TestLayoutShowPrintsTheFileUnchanged proves show is a cat: hand formatting
// round-trips byte for byte.
func TestLayoutShowPrintsTheFileUnchanged(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := layoutdoc.Path("hand")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "{\"version\":1,\n  \"name\":\"hand\"}\n\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"layout", "show", "hand"}); err != nil {
			t.Fatalf("dm layout show: %v", err)
		}
	})
	if out != body {
		t.Fatalf("show printed %q, want the file unchanged", out)
	}
}

// TestLayoutRm proves rm deletes the file and reports a name it cannot find.
func TestLayoutRm(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := layoutdoc.Path("gone")
	if err != nil {
		t.Fatal(err)
	}
	doc := layoutdoc.Doc{Version: layoutdoc.Version, Name: "gone", Workspaces: []layoutdoc.Workspace{{
		Name: "one", Windows: []layoutdoc.Window{{Name: "a", Focus: "p", Root: &layout.Node{Pane: "p"}}},
	}}, Panes: map[string]layoutdoc.Pane{"p": {}}}
	if err := layoutdoc.Save(path, &doc); err != nil {
		t.Fatal(err)
	}
	if err := Run([]string{"layout", "rm", "gone"}); err != nil {
		t.Fatalf("dm layout rm: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("rm left the file: %v", err)
	}
	if err := Run([]string{"layout", "rm", "gone"}); err == nil {
		t.Fatal("dm layout rm accepted a missing layout")
	}
}

// TestLayoutBarePrintsTheSubcommands proves the bare verb lists its verbs
// instead of dumping flags or failing.
func TestLayoutBarePrintsTheSubcommands(t *testing.T) {
	t.Parallel()
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"layout"}); err != nil {
			t.Fatalf("dm layout: %v", err)
		}
	})
	for _, want := range []string{"dm layout save", "dm layout ls", "dm layout show", "dm layout rm", "-L"} {
		if !strings.Contains(out, want) {
			t.Errorf("dm layout does not name %q", want)
		}
	}
	err := Run([]string{"layout", "nope"})
	if err == nil || !strings.Contains(err.Error(), "want save, ls, show or rm") {
		t.Fatalf("dm layout nope = %v, want the verb list", err)
	}
	if err := Run([]string{"layout", "show"}); err == nil || !strings.Contains(err.Error(), "needs a name") {
		t.Fatalf("dm layout show without a name = %v", err)
	}
}

// TestAttachLayoutFailsBeforeTheSpawn proves -L resolves and reads the file
// before anything spawns: a bad name or a missing file is the CLI's error and
// the state tree gains no session.
func TestAttachLayoutFailsBeforeTheSpawn(t *testing.T) {
	for _, c := range []struct {
		name  string
		argv  []string
		wants []string
	}{
		{
			name:  "missing file",
			argv:  []string{"-L", "missing"},
			wants: []string{"missing", "missing.json"},
		},
		{
			name:  "bad name",
			argv:  []string{"--layout", "no good"},
			wants: []string{"letters, digits, _ or -"},
		},
		{
			name:  "missing file on new",
			argv:  []string{"new", "-L", "missing"},
			wants: []string{"missing.json"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			state := t.TempDir()
			t.Setenv("DM_STATE", state)
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			err := Run(c.argv)
			if err == nil {
				t.Fatalf("dm %v was accepted", c.argv)
			}
			for _, want := range c.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("dm %v = %v, want it to name %q", c.argv, err, want)
				}
			}
			sessions, statErr := os.ReadDir(filepath.Join(state, "sessions"))
			if statErr == nil && len(sessions) > 0 {
				t.Fatalf("a failed -L left %d session dirs", len(sessions))
			}
			if _, statErr := os.Stat(paths.SessionDir("missing")); !os.IsNotExist(statErr) {
				t.Fatalf("a failed -L created the session dir: %v", statErr)
			}
		})
	}
}

// TestAttachLayoutRefusesAMalformedFile proves the document is validated at the
// CLI, in the CLI's words, and that the server's own error never appears.
func TestAttachLayoutRefusesAMalformedFile(t *testing.T) {
	state := t.TempDir()
	t.Setenv("DM_STATE", state)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := layoutdoc.Path("bad")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"version":1,"name":"bad","workspaces":[{"name":"w","active":0,"windows":[{"name":"api","focus":"p","root":{"dir":"row"}}]}],"panes":{}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	err = Run([]string{"-L", "bad"})
	if err == nil {
		t.Fatal("dm -L bad was accepted")
	}
	for _, want := range []string{"layout bad", path, "no pane leaf"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// TestNewWithLayoutDefaultsTheSessionName proves the layout name names the
// session when -s is absent, so attaching a layout twice reattaches.
func TestNewWithLayoutDefaultsTheSessionName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := layoutdoc.Path("go-backend")
	if err != nil {
		t.Fatal(err)
	}
	doc := layoutdoc.Doc{Version: layoutdoc.Version, Name: "go-backend", Workspaces: []layoutdoc.Workspace{{
		Name: "one", Windows: []layoutdoc.Window{{Name: "a", Focus: "p", Root: &layout.Node{Pane: "p"}}},
	}}, Panes: map[string]layoutdoc.Pane{"p": {Cwd: "/work"}}}
	if err := layoutdoc.Save(path, &doc); err != nil {
		t.Fatal(err)
	}
	// The server is live at the layout's name, so nothing spawns: an existing
	// session is used as it stands.
	startFakeServer(t, "go-backend", okHandler(`{"session":"go-backend"}`))
	var runErr error
	out := testutil.CaptureStdout(t, func() { runErr = Run([]string{"new", "-L", "go-backend"}) })
	if runErr != nil {
		t.Fatalf("dm new -L: %v", runErr)
	}
	if strings.TrimSpace(out) != "go-backend" {
		t.Fatalf("dm new -L printed %q, want the layout name as the session", out)
	}
}

// TestNewWithLayoutHonoursTheSessionFlag proves an explicit -s wins over the
// layout name.
func TestNewWithLayoutHonoursTheSessionFlag(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := layoutdoc.Path("go-backend")
	if err != nil {
		t.Fatal(err)
	}
	doc := layoutdoc.Doc{Version: layoutdoc.Version, Name: "go-backend", Workspaces: []layoutdoc.Workspace{{
		Name: "one", Windows: []layoutdoc.Window{{Name: "a", Focus: "p", Root: &layout.Node{Pane: "p"}}},
	}}, Panes: map[string]layoutdoc.Pane{"p": {}}}
	if err := layoutdoc.Save(path, &doc); err != nil {
		t.Fatal(err)
	}
	startFakeServer(t, "other", okHandler(`{"session":"other"}`))
	var runErr error
	out := testutil.CaptureStdout(t, func() { runErr = Run([]string{"new", "-s", "other", "-L", "go-backend"}) })
	if runErr != nil {
		t.Fatalf("dm new -s other -L: %v", runErr)
	}
	if strings.TrimSpace(out) != "other" {
		t.Fatalf("dm new -s other -L printed %q, want other", out)
	}
}
