package layoutdoc

import (
	"reflect"
	"testing"

	"dm/internal/box"
	"dm/internal/layout"
	"dm/internal/session"
)

// projectState is a live state with two workspaces, a nested tree, launch data
// and a pane no window holds.
func projectState() *session.State {
	return &session.State{
		Name:   "two",
		Active: 1,
		Workspaces: []*session.Workspace{
			{
				ID: "s1", Name: "development", Project: "go-backend", Active: 0, Unfolded: true,
				Windows: []*session.Window{{
					ID:    "w1",
					Name:  "api",
					Focus: "w1p2",
					Root: &layout.Node{Dir: "col", Sizes: []float64{0.6, 0.4}, Kids: []*layout.Node{
						{Pane: "w1p1"},
						{Dir: "row", Sizes: []float64{0.5, 0.5}, Kids: []*layout.Node{
							{Pane: "w1p2"}, {Pane: "w1p3"},
						}},
					}},
				}},
			},
			{
				ID: "s2", Name: "notes", Active: 0, Unfolded: true,
				Windows: []*session.Window{{
					ID: "w2", Name: "scratch", Focus: "w2p1", Root: &layout.Node{Pane: "w2p1"},
				}},
			},
		},
		Panes: map[string]*session.PaneState{
			"w1p1": {Cwd: "/work/api", Argv: []string{"pi"}, Shell: "/bin/zsh", Cols: 101, Rows: 31,
				App: "pi", Name: "agent-a", Reported: true},
			"w1p2": {Cwd: "/work/api", Shell: "/bin/zsh", Cols: 40, Rows: 31},
			"w1p3": {Cwd: "/work/api", Argv: []string{"tail", "-f", "app.log"}},
			"w2p1": {Cwd: "/work/notes"},
			// No window holds this one: a layout has nothing to replay it at.
			"w9p9": {Cwd: "/work/gone"},
		},
		Box:     []box.Entry{{Seq: 7, From: "w1p1", Key: "lane/w1", Msg: "keep"}},
		Rail:    40,
		RailSet: true,
	}
}

// TestProject pins the projection: the shape in state order, the launch data of
// the referenced panes, nothing the layout must not carry, and no stray pane.
func TestProject(t *testing.T) {
	t.Parallel()
	st := projectState()
	doc := Project(st)
	if doc == nil {
		t.Fatal("Project returned no document")
	}
	if doc.Version != Version || doc.Name != "two" {
		t.Fatalf("doc header = version %d name %q, want %d two", doc.Version, doc.Name, Version)
	}
	if len(doc.Workspaces) != 2 {
		t.Fatalf("workspaces = %d, want 2", len(doc.Workspaces))
	}
	one, two := doc.Workspaces[0], doc.Workspaces[1]
	if one.Name != "development" || one.Project != "go-backend" || one.Active != 0 {
		t.Errorf("workspace one = %+v, want development/go-backend active 0", one)
	}
	if two.Name != "notes" || two.Project != "" || len(two.Windows) != 1 {
		t.Errorf("workspace two = %+v, want notes with one window", two)
	}
	w := one.Windows[0]
	if w.Name != "api" || w.Focus != "w1p2" {
		t.Errorf("window = %+v, want api focus w1p2", w)
	}
	if w.Root.Dir != "col" || !reflect.DeepEqual(w.Root.Sizes, []float64{0.6, 0.4}) {
		t.Fatalf("root = %+v, want a col split 0.6/0.4", w.Root)
	}
	if got := layout.AllPanes(w.Root); !reflect.DeepEqual(got, []string{"w1p1", "w1p2", "w1p3"}) {
		t.Errorf("panes = %v, want w1p1,w1p2,w1p3 in tree order", got)
	}
	if kid := w.Root.Kids[1]; kid.Dir != "row" || len(kid.Kids) != 2 {
		t.Errorf("nested row = %+v, want a row over two leaves", kid)
	}
	wantPanes := map[string]Pane{
		"w1p1": {Cwd: "/work/api", Argv: []string{"pi"}},
		"w1p2": {Cwd: "/work/api"},
		"w1p3": {Cwd: "/work/api", Argv: []string{"tail", "-f", "app.log"}},
		"w2p1": {Cwd: "/work/notes"},
	}
	if !reflect.DeepEqual(doc.Panes, wantPanes) {
		t.Fatalf("panes = %+v, want %+v", doc.Panes, wantPanes)
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("a live state projected to a document Validate refuses: %v", err)
	}
}

// TestProjectCopies proves the document holds its own tree and argv, so the
// server can marshal the reply after it drops its lock.
func TestProjectCopies(t *testing.T) {
	t.Parallel()
	st := projectState()
	doc := Project(st)
	root := doc.Workspaces[0].Windows[0].Root
	st.Workspaces[0].Windows[0].Root.Kids[0].Pane = "changed"
	st.Panes["w1p1"].Argv[0] = "changed"
	st.Workspaces[0].Name = "changed"
	if root.Kids[0].Pane != "w1p1" {
		t.Errorf("the document's tree followed the state: %q", root.Kids[0].Pane)
	}
	if got := doc.Panes["w1p1"].Argv; !reflect.DeepEqual(got, []string{"pi"}) {
		t.Errorf("the document's argv followed the state: %v", got)
	}
	if doc.Workspaces[0].Name != "development" {
		t.Errorf("the document's workspace name followed the state: %q", doc.Workspaces[0].Name)
	}
}
