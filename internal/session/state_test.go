package session

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
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

// TestSaveConcurrent runs many saves over one state at once. It is a
// regression guard, not a reliable red on the old fixed-name temp file: the
// overlap window is real but the scheduler decides whether it trips. It
// requires every save to succeed and the file to hold the saved content after.
func TestSaveConcurrent(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	st := NewState("race")
	// Enough content that the write window is real, so the saves overlap.
	for i := 0; i < 200; i++ {
		st.Workspaces = append(st.Workspaces, &Workspace{
			ID:       "s" + strconv.Itoa(i+2),
			Name:     "ws" + strconv.Itoa(i),
			Unfolded: true,
			Windows: []*Window{{
				ID:    "w" + strconv.Itoa(i+2),
				Name:  "win" + strconv.Itoa(i),
				Focus: "fill",
				Root:  &layout.Node{Pane: "fill"},
			}},
		})
	}
	const n = 64
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- st.Save()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent save: %v", err)
		}
	}
	got, err := LoadState("race")
	if err != nil {
		t.Fatalf("load after concurrent saves: %v", err)
	}
	if got.Name != "race" || len(got.Workspaces) != len(st.Workspaces) {
		t.Fatalf("loaded name %q with %d workspaces, want race with %d",
			got.Name, len(got.Workspaces), len(st.Workspaces))
	}
	if last := got.Workspaces[len(got.Workspaces)-1]; last.Name != "ws199" {
		t.Fatalf("last workspace %q, want ws199", last.Name)
	}
	if tmp, _ := filepath.Glob(filepath.Join(paths.SessionDir("race"), "*.tmp")); len(tmp) != 0 {
		t.Fatalf("temp files left behind: %v", tmp)
	}
}

// TestSaveFailureLeavesNoTemp makes the write fail with a read-only session
// directory and requires the error to surface with no temp file behind it.
func TestSaveFailureLeavesNoTemp(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	t.Setenv("DM_STATE", t.TempDir())
	dir := paths.SessionDir("ro")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	st := NewState("ro")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	if err := st.Save(); err == nil {
		t.Fatal("save into a read-only directory succeeded, want an error")
	}
	left, err := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("failed save left temp files: %v", left)
	}
}
