package session

import (
	"os"
	"testing"

	"dm/internal/layout"
	"dm/internal/paths"
)

func TestIDsAreAddressable(t *testing.T) {
	st := NewState("x")
	ws := st.ActiveWorkspace()
	w := st.ActiveWindow()
	if ws.ID != "s1" || w.ID != "w1" {
		t.Fatalf("initial ids %q %q", ws.ID, w.ID)
	}
	if p := w.NewPaneID(); p != "w1p1" {
		t.Fatalf("first pane %q", p)
	}
	if p := w.NewPaneID(); p != "w1p2" {
		t.Fatalf("second pane %q", p)
	}
	if id := st.NewWindowID(); id != "w2" {
		t.Fatalf("next window %q", id)
	}
	if id := st.NewWorkspaceID(); id != "s2" {
		t.Fatalf("next workspace %q", id)
	}

	// A state from the random scheme migrates to the same shape.
	old := &State{
		Name: "x", Panes: map[string]*PaneState{"abc12": {}},
		Workspaces: []*Workspace{{ID: "sz6z50", Name: "main", Windows: []*Window{{
			ID: "w2e8y4", Focus: "abc12",
			Root: &layout.Node{Dir: "row", Kids: []*layout.Node{{Pane: "abc12"}, {Pane: "def34"}}},
		}}}},
	}
	old.MigrateIDs()
	if old.Workspaces[0].ID != "s1" || old.Workspaces[0].Windows[0].ID != "w1" {
		t.Fatalf("migrated ids %q %q", old.Workspaces[0].ID, old.Workspaces[0].Windows[0].ID)
	}
	if got := layout.AllPanes(old.Workspaces[0].Windows[0].Root); got[0] != "w1p1" || got[1] != "w1p2" {
		t.Fatalf("migrated panes %v", got)
	}
	if old.Workspaces[0].Windows[0].Focus != "w1p1" {
		t.Fatalf("migrated focus %q", old.Workspaces[0].Windows[0].Focus)
	}
	if _, ok := old.Panes["w1p1"]; !ok {
		t.Fatalf("pane state not remapped: %v", old.Panes)
	}
}

// TestWindowNameMigration renames the old positional window labels on load.
func TestWindowNameMigration(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	st := NewState("mig")
	w := st.Workspaces[0].Windows[0]
	w.Name = "1"
	w.Focus = "w1p1"
	st.Panes["w1p1"] = &PaneState{Cwd: "/code/dm"}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState("mig")
	if err != nil {
		t.Fatal(err)
	}
	if name := got.Workspaces[0].Windows[0].Name; name != "dm" {
		t.Fatalf("migrated window name = %q, want dm", name)
	}

	// A name that is not positional survives the load.
	got.Workspaces[0].Windows[0].Name = "tests"
	if err := got.Save(); err != nil {
		t.Fatal(err)
	}
	again, err := LoadState("mig")
	if err != nil {
		t.Fatal(err)
	}
	if name := again.Workspaces[0].Windows[0].Name; name != "tests" {
		t.Fatalf("named window = %q, want tests", name)
	}
}

// TestWorkspaceFoldDefault proves a workspace starts unfolded and an older
// state file with no workspace fold key loads unfolded.
func TestWorkspaceFoldDefault(t *testing.T) {
	st := NewState("fold")
	if !st.Workspaces[0].Unfolded {
		t.Fatal("new workspace is folded, want unfolded")
	}

	t.Setenv("DM_STATE", t.TempDir())
	const raw = `{
  "name": "absent",
  "workspaces": [
    {"id": "s1", "name": "main", "windows": [], "active": 0}
  ],
  "active": 0,
  "cols": 80,
  "rows": 24,
  "panes": {},
  "nextWorkspace": 1,
  "nextWindow": 1,
  "idVersion": 2,
  "rail": 30,
  "railSet": true
}`
	if err := os.MkdirAll(paths.SessionDir("absent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.StatePath("absent"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState("absent")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Workspaces[0].Unfolded {
		t.Fatal("workspace with no fold key loaded folded, want unfolded")
	}
}

// TestWorkspaceFoldRoundTrip proves a folded workspace stays folded across a
// save and load, and an unfolded one stays unfolded.
func TestWorkspaceFoldRoundTrip(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	st := NewState("fold")
	st.Workspaces[0].Unfolded = false
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState("fold")
	if err != nil {
		t.Fatal(err)
	}
	if got.Workspaces[0].Unfolded {
		t.Fatal("folded workspace loaded unfolded")
	}

	got.Workspaces[0].Unfolded = true
	if err := got.Save(); err != nil {
		t.Fatal(err)
	}
	again, err := LoadState("fold")
	if err != nil {
		t.Fatal(err)
	}
	if !again.Workspaces[0].Unfolded {
		t.Fatal("unfolded workspace loaded folded")
	}
}
