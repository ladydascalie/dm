package layoutdoc

import (
	"reflect"
	"strings"
	"testing"

	"dm/internal/layout"
)

// sample is a small valid document: one workspace, one window, a two-pane
// column with a 60/40 split.
func sample() *Doc {
	return &Doc{
		Version: Version,
		Name:    "go-backend",
		Workspaces: []Workspace{{
			Name:    "development",
			Project: "go-backend",
			Active:  0,
			Windows: []Window{{
				Name:  "api",
				Focus: "w1p1",
				Root: &layout.Node{
					Dir:   "col",
					Sizes: []float64{0.6, 0.4},
					Kids:  []*layout.Node{{Pane: "w1p1"}, {Pane: "w1p2"}},
				},
			}},
		}},
		Panes: map[string]Pane{
			"w1p1": {Cwd: "/home/b/Code/Personal/go-backend", Argv: []string{"pi"}},
			"w1p2": {Cwd: "/home/b/Code/Personal/go-backend"},
		},
	}
}

func TestValidateAccepts(t *testing.T) {
	t.Parallel()
	if err := sample().Validate(); err != nil {
		t.Fatalf("sample refused: %v", err)
	}
}

func TestValidateRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(*Doc)
		want string
	}{
		{
			name: "unknown version",
			edit: func(d *Doc) { d.Version = 2 },
			want: "version",
		},
		{
			name: "no workspaces",
			edit: func(d *Doc) { d.Workspaces = nil },
			want: "workspaces: empty",
		},
		{
			name: "workspace with no windows",
			edit: func(d *Doc) { d.Workspaces[0].Windows = nil },
			want: "workspaces[0].windows",
		},
		{
			name: "empty workspace name",
			edit: func(d *Doc) { d.Workspaces[0].Name = "" },
			want: "workspaces[0].name",
		},
		{
			name: "empty window name",
			edit: func(d *Doc) { d.Workspaces[0].Windows[0].Name = "" },
			want: "workspaces[0].windows[0].name",
		},
		{
			name: "duplicate workspace name",
			edit: func(d *Doc) {
				second := d.Workspaces[0]
				d.Workspaces = append(d.Workspaces, second)
			},
			want: "duplicate workspace name",
		},
		{
			name: "window root with no leaf",
			edit: func(d *Doc) {
				d.Workspaces[0].Windows[0].Root = &layout.Node{Dir: "row"}
			},
			want: "no pane leaf",
		},
		{
			name: "nil root",
			edit: func(d *Doc) { d.Workspaces[0].Windows[0].Root = nil },
			want: "no pane leaf",
		},
		{
			name: "leaf missing from panes",
			edit: func(d *Doc) { delete(d.Panes, "w1p2") },
			want: `pane "w1p2" is missing from panes`,
		},
		{
			name: "focus is not a leaf",
			edit: func(d *Doc) { d.Workspaces[0].Windows[0].Focus = "w9p9" },
			want: "focus",
		},
		{
			name: "pane in two trees",
			edit: func(d *Doc) {
				d.Workspaces[0].Windows = append(d.Workspaces[0].Windows, Window{
					Name:  "logs",
					Focus: "w1p1",
					Root:  &layout.Node{Pane: "w1p1"},
				})
			},
			want: "already in",
		},
		{
			name: "pane twice in one tree",
			edit: func(d *Doc) {
				d.Workspaces[0].Windows[0].Root.Kids[1] = &layout.Node{Pane: "w1p1"}
			},
			want: "appears twice",
		},
		{
			name: "leaf id with kids",
			edit: func(d *Doc) {
				d.Workspaces[0].Windows[0].Root.Kids[1] = &layout.Node{
					Pane: "w1p2",
					Kids: []*layout.Node{{Pane: "w1p3"}},
				}
				d.Panes["w1p3"] = Pane{Cwd: "/tmp"}
			},
			want: "holds a pane id and kids",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := sample()
			c.edit(d)
			err := d.Validate()
			if err == nil {
				t.Fatalf("Validate accepted a document it should refuse")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not name %q", err, c.want)
			}
		})
	}
}

// TestSaveLoadRoundTrip proves the decoded values survive the file, and that a
// second Save of the loaded document writes the same bytes.
func TestSaveLoadRoundTrip(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/go-backend.json"
	want := sample()
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	first, err := readFile(t, path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip changed the doc:\ngot  %+v\nwant %+v", got, want)
	}
	// The round trip keeps dir, sizes, focus, cwd and argv.
	win := got.Workspaces[0].Windows[0]
	if win.Root.Dir != "col" || !reflect.DeepEqual(win.Root.Sizes, []float64{0.6, 0.4}) {
		t.Fatalf("tree shape changed: %+v", win.Root)
	}
	if win.Focus != "w1p1" {
		t.Fatalf("focus = %q, want w1p1", win.Focus)
	}
	if p := got.Panes["w1p1"]; p.Cwd != "/home/b/Code/Personal/go-backend" || !reflect.DeepEqual(p.Argv, []string{"pi"}) {
		t.Fatalf("pane launch data changed: %+v", p)
	}
	if err := Save(path, got); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	second, err := readFile(t, path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("Save is not byte-stable:\nfirst  %s\nsecond %s", first, second)
	}
}

func TestLoadRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "unknown field",
			body: `{"version":1,"name":"x","workspaces":[],"panes":{},"box":[]}`,
			want: "unknown field",
		},
		{
			name: "bad shape",
			body: `{"version":1,"name":"x","workspaces":[],"panes":{}}`,
			want: "workspaces: empty",
		},
		{
			name: "trailing data",
			body: `{"version":1,"name":"x","workspaces":[],"panes":{}} {}`,
			want: "trailing data",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			path := t.TempDir() + "/broken.json"
			if err := writeFile(path, c.body); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil {
				t.Fatalf("Load accepted %s", c.body)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error %q does not name the file", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not name %q", err, c.want)
			}
		})
	}
}
