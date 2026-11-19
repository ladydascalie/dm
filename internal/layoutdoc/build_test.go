package layoutdoc

import (
	"reflect"
	"testing"

	"dm/internal/box"
	"dm/internal/layout"
	"dm/internal/session"
)

// TestBuildIntoFresh proves a blank session gets the doc's shape with ids from
// fresh counters, contiguous and in tree order.
func TestBuildIntoFresh(t *testing.T) {
	t.Parallel()
	st := &session.State{}
	if err := BuildInto(st, sample()); err != nil {
		t.Fatalf("BuildInto: %v", err)
	}
	if len(st.Workspaces) != 1 {
		t.Fatalf("got %d workspaces, want 1", len(st.Workspaces))
	}
	ws := st.Workspaces[0]
	if ws.ID != "s1" || ws.Name != "development" || ws.Project != "go-backend" {
		t.Errorf("workspace = %+v, want s1 development/go-backend", ws)
	}
	if ws.Active != 0 || !ws.Unfolded {
		t.Errorf("workspace active/unfolded = %d/%v, want 0/true", ws.Active, ws.Unfolded)
	}
	if len(ws.Windows) != 1 {
		t.Fatalf("got %d windows, want 1", len(ws.Windows))
	}
	w := ws.Windows[0]
	if w.ID != "w1" || w.Name != "api" || !w.Named {
		t.Errorf("window = %+v, want w1 api named", w)
	}
	if ids := layout.AllPanes(w.Root); !reflect.DeepEqual(ids, []string{"w1p1", "w1p2"}) {
		t.Fatalf("tree panes = %v, want [w1p1 w1p2] in tree order", ids)
	}
	if w.Focus != "w1p1" {
		t.Errorf("focus = %q, want w1p1", w.Focus)
	}
	if w.NextPane != 2 {
		t.Errorf("nextPane = %d, want 2", w.NextPane)
	}
	if !reflect.DeepEqual(w.Root.Sizes, []float64{0.6, 0.4}) {
		t.Errorf("sizes = %v, want the doc's", w.Root.Sizes)
	}
	if p := st.Panes["w1p1"]; p == nil || p.Cwd != "/home/b/Code/Personal/go-backend" || !reflect.DeepEqual(p.Argv, []string{"pi"}) {
		t.Fatalf("pane w1p1 = %+v, want the doc's cwd and argv", st.Panes["w1p1"])
	}
	if p := st.Panes["w1p2"]; p == nil || len(p.Argv) != 0 {
		t.Fatalf("pane w1p2 = %+v, want no argv", st.Panes["w1p2"])
	}
	if st.Active != 0 || st.NextWorkspace != 1 || st.NextWindow != 1 {
		t.Errorf("active/counters = %d/%d/%d, want 0/1/1", st.Active, st.NextWorkspace, st.NextWindow)
	}
}

// TestBuildIntoNumbersAcrossTheDoc proves the counters run across workspaces
// and windows in declaration order, with panes numbered per window.
func TestBuildIntoNumbersAcrossTheDoc(t *testing.T) {
	t.Parallel()
	d := &Doc{
		Version: Version,
		Name:    "wide",
		Workspaces: []Workspace{
			{Name: "one", Windows: []Window{
				{Name: "a", Focus: "a1", Root: &layout.Node{Dir: "row", Kids: []*layout.Node{{Pane: "a1"}, {Pane: "a2"}}}},
				{Name: "b", Focus: "b1", Root: &layout.Node{Pane: "b1"}},
			}},
			{Name: "two", Windows: []Window{
				{Name: "c", Focus: "c2", Root: &layout.Node{Dir: "col", Kids: []*layout.Node{{Pane: "c1"}, {Pane: "c2"}}}},
			}},
		},
		Panes: map[string]Pane{
			"a1": {Cwd: "/a"}, "a2": {Cwd: "/a"}, "b1": {Cwd: "/b"},
			"c1": {Cwd: "/c"}, "c2": {Cwd: "/c"},
			// Not referenced by any tree, so it must not reach the session.
			"stray": {Cwd: "/stray"},
		},
	}
	st := &session.State{}
	if err := BuildInto(st, d); err != nil {
		t.Fatalf("BuildInto: %v", err)
	}
	got := map[string][]string{}
	for _, ws := range st.Workspaces {
		for _, w := range ws.Windows {
			got[w.ID+" "+ws.ID] = layout.AllPanes(w.Root)
			if w.Focus != got[w.ID+" "+ws.ID][0] && w.Focus != got[w.ID+" "+ws.ID][1] {
				t.Errorf("%s focus %q is not one of its leaves %v", w.ID, w.Focus, got[w.ID+" "+ws.ID])
			}
		}
	}
	want := map[string][]string{
		"w1 s1": {"w1p1", "w1p2"},
		"w2 s1": {"w2p1"},
		"w3 s2": {"w3p1", "w3p2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trees = %v, want %v", got, want)
	}
	if st.NextWorkspace != 2 || st.NextWindow != 3 {
		t.Errorf("counters = %d/%d, want 2/3", st.NextWorkspace, st.NextWindow)
	}
	if st.Workspaces[1].Windows[0].Focus != "w3p2" {
		t.Errorf("focus = %q, want w3p2", st.Workspaces[1].Windows[0].Focus)
	}
	if _, ok := st.Panes["stray"]; ok {
		t.Error("a pane no tree references reached the session")
	}
	if len(st.Panes) != 5 {
		t.Errorf("got %d panes, want 5", len(st.Panes))
	}
}

// TestBuildIntoTwiceDropsOldPanes proves a second build over a live state
// leaves no old pane id in Panes and keeps Box and Secrets untouched.
func TestBuildIntoTwiceDropsOldPanes(t *testing.T) {
	t.Parallel()
	st := &session.State{}
	if err := BuildInto(st, sample()); err != nil {
		t.Fatalf("first BuildInto: %v", err)
	}
	// The old state carries a pane the first layout has not, plus a log and a
	// secret registry the layout must never touch.
	st.Panes["w9p9"] = &session.PaneState{Cwd: "/old", Argv: []string{"old"}}
	st.Box = []box.Entry{{Seq: 7, From: "w1p1", Key: "lane/w1", Msg: "keep"}}
	st.Secrets = []*session.Secret{{Name: "gh", Tool: session.ToolOp, Reference: "op://v/i/f"}}
	st.Rail = 40
	st.ProjectFold = map[string]bool{"go-backend": true}
	st.ProjectAccent = map[string]int{"go-backend": 2}

	second := &Doc{
		Version: Version,
		Name:    "narrow",
		Workspaces: []Workspace{{
			Name:    "only",
			Windows: []Window{{Name: "shell", Focus: "p", Root: &layout.Node{Pane: "p"}}},
		}},
		Panes: map[string]Pane{"p": {Cwd: "/new"}},
	}
	if err := BuildInto(st, second); err != nil {
		t.Fatalf("second BuildInto: %v", err)
	}
	if len(st.Panes) != 1 {
		t.Fatalf("panes = %v, want only w1p1", st.Panes)
	}
	if _, ok := st.Panes["w1p2"]; ok {
		t.Error("the first layout's w1p2 survived")
	}
	if p := st.Panes["w1p1"]; p == nil || p.Cwd != "/new" || p.Argv != nil {
		t.Fatalf("w1p1 = %+v, want /new with no argv", st.Panes["w1p1"])
	}
	if len(st.Workspaces) != 1 || st.Workspaces[0].ID != "s1" || st.Workspaces[0].Name != "only" {
		t.Fatalf("workspaces = %+v, want one s1 only", st.Workspaces)
	}
	if len(st.Box) != 1 || st.Box[0].Seq != 7 {
		t.Errorf("box = %+v, want the entry untouched", st.Box)
	}
	if len(st.Secrets) != 1 || st.Secrets[0].Name != "gh" {
		t.Errorf("secrets = %+v, want the registry untouched", st.Secrets)
	}
	if st.Rail != 40 || !st.ProjectFold["go-backend"] || st.ProjectAccent["go-backend"] != 2 {
		t.Errorf("rail/fold/accent = %d/%v/%v, want untouched", st.Rail, st.ProjectFold, st.ProjectAccent)
	}
	if st.NextWorkspace != 1 || st.NextWindow != 1 {
		t.Errorf("counters = %d/%d, want 1/1", st.NextWorkspace, st.NextWindow)
	}
}

// TestBuildIntoRefusesInvalid proves BuildInto leaves a live state alone when
// the document is refused.
func TestBuildIntoRefusesInvalid(t *testing.T) {
	t.Parallel()
	st := &session.State{}
	if err := BuildInto(st, sample()); err != nil {
		t.Fatalf("BuildInto: %v", err)
	}
	before := len(st.Panes)
	bad := sample()
	bad.Version = 9
	if err := BuildInto(st, bad); err == nil {
		t.Fatal("BuildInto accepted an unknown version")
	}
	if len(st.Panes) != before {
		t.Error("a refused document touched the live state")
	}
}

// TestBuildIntoNormalisesActive proves an out-of-range window index lands on
// the first window instead of an index the rail would have to guard.
func TestBuildIntoNormalisesActive(t *testing.T) {
	t.Parallel()
	d := sample()
	d.Workspaces[0].Active = 4
	st := &session.State{}
	if err := BuildInto(st, d); err != nil {
		t.Fatalf("BuildInto: %v", err)
	}
	if st.Workspaces[0].Active != 0 {
		t.Fatalf("active = %d, want 0", st.Workspaces[0].Active)
	}
}
