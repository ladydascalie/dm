package session

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"dm/internal/ask"
	"dm/internal/layout"
	"dm/internal/paths"
)

func TestIDsAreAddressable(t *testing.T) {
	t.Parallel()
	st := NewStateAt(t.TempDir(), "x")
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
	t.Parallel()
	st := NewStateAt(t.TempDir(), "mig")
	w := st.Workspaces[0].Windows[0]
	w.Name = "1"
	w.Focus = "w1p1"
	st.Panes["w1p1"] = &PaneState{Cwd: "/code/dm"}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStateAt(st.Dir(), "mig")
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
	again, err := LoadStateAt(st.Dir(), "mig")
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
	t.Parallel()
	root := t.TempDir()
	st := NewStateAt(root, "fold")
	if !st.Workspaces[0].Unfolded {
		t.Fatal("new workspace is folded, want unfolded")
	}

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
	if err := os.MkdirAll(paths.SessionDirAt(root, "absent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.StatePathAt(root, "absent"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStateAt(root, "absent")
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
	t.Parallel()
	st := NewStateAt(t.TempDir(), "fold")
	st.Workspaces[0].Unfolded = false
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStateAt(st.Dir(), "fold")
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
	again, err := LoadStateAt(st.Dir(), "fold")
	if err != nil {
		t.Fatal(err)
	}
	if !again.Workspaces[0].Unfolded {
		t.Fatal("unfolded workspace loaded folded")
	}
}

// TestArgvRoundTrip proves a pane's argv survives the state file: a server
// restart reads the same program the pane was born with, and a pane without
// one stays a shell.
func TestArgvRoundTrip(t *testing.T) {
	t.Parallel()
	st := NewStateAt(t.TempDir(), "argv")
	st.Panes["w1p1"] = &PaneState{Cwd: "/tmp", Shell: "/bin/sh", Cols: 80, Rows: 24,
		Argv: []string{"htop", "-d", "5"}}
	st.Panes["w1p2"] = &PaneState{Cwd: "/tmp", Shell: "/bin/sh", Cols: 80, Rows: 24}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStateAt(st.Dir(), "argv")
	if err != nil {
		t.Fatal(err)
	}
	ps := got.Panes["w1p1"]
	if ps == nil {
		t.Fatal("the argv pane did not load")
	}
	if len(ps.Argv) != 3 || ps.Argv[0] != "htop" || ps.Argv[1] != "-d" || ps.Argv[2] != "5" {
		t.Fatalf("argv = %v, want [htop -d 5]", ps.Argv)
	}
	if got := got.Panes["w1p2"].Argv; len(got) != 0 {
		t.Fatalf("shell pane argv = %v, want empty", got)
	}
}

// TestSaveConcurrent runs many saves over one state at once. It is a
// regression guard, not a reliable red on the old fixed-name temp file: the
// overlap window is real but the scheduler decides whether it trips. It
// requires every save to succeed and the file to hold the saved content after.
func TestSaveConcurrent(t *testing.T) {
	t.Parallel()
	st := NewStateAt(t.TempDir(), "race")
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
	got, err := LoadStateAt(st.Dir(), "race")
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
	if tmp, _ := filepath.Glob(filepath.Join(st.SessionDir(), "*.tmp")); len(tmp) != 0 {
		t.Fatalf("temp files left behind: %v", tmp)
	}
}

// TestSaveFailureLeavesNoTemp makes the write fail with a read-only session
// directory and requires the error to surface with no temp file behind it.
func TestSaveFailureLeavesNoTemp(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	st := NewStateAt(t.TempDir(), "ro")
	dir := st.SessionDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

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

// wsNames reads the workspace names in slice order.
func wsNames(st *State) []string {
	names := make([]string, len(st.Workspaces))
	for i, ws := range st.Workspaces {
		names[i] = ws.Name
	}
	return names
}

func workspace(id, name, project string) *Workspace {
	return &Workspace{ID: id, Name: name, Project: project, Unfolded: true}
}

// TestGroupWorkspaces proves a stable sort by project: first appearance
// decides the group's position and the order inside a group holds.
func TestGroupWorkspaces(t *testing.T) {
	t.Parallel()
	st := &State{Workspaces: []*Workspace{
		workspace("s1", "a1", "alpha"),
		workspace("s2", "b1", "beta"),
		workspace("s3", "a2", "alpha"),
		workspace("s4", "b2", "beta"),
		workspace("s5", "a3", "alpha"),
	}}
	st.GroupWorkspaces()
	want := []string{"a1", "a2", "a3", "b1", "b2"}
	if got := wsNames(st); !equalStrings(got, want) {
		t.Fatalf("grouped = %v, want %v", got, want)
	}
}

// TestGroupWorkspacesIdempotent proves a second call changes nothing.
func TestGroupWorkspacesIdempotent(t *testing.T) {
	t.Parallel()
	st := &State{Workspaces: []*Workspace{
		workspace("s1", "b1", "beta"),
		workspace("s2", "a1", "alpha"),
		workspace("s3", "b2", "beta"),
	}}
	st.GroupWorkspaces()
	first := wsNames(st)
	st.GroupWorkspaces()
	if got := wsNames(st); !equalStrings(got, first) {
		t.Fatalf("second group = %v, want %v", got, first)
	}
}

// TestGroupWorkspacesKeepsUntaggedApart proves untagged workspaces never merge
// into one group.
func TestGroupWorkspacesKeepsUntaggedApart(t *testing.T) {
	t.Parallel()
	st := &State{Workspaces: []*Workspace{
		workspace("s1", "loose1", ""),
		workspace("s2", "p1", "alpha"),
		workspace("s3", "loose2", ""),
		workspace("s4", "p2", "alpha"),
		workspace("s5", "loose3", ""),
	}}
	st.GroupWorkspaces()
	want := []string{"loose1", "p1", "p2", "loose2", "loose3"}
	if got := wsNames(st); !equalStrings(got, want) {
		t.Fatalf("grouped = %v, want %v", got, want)
	}
	if st.Workspaces[0].Project != "" || st.Workspaces[3].Project != "" {
		t.Fatal("untagged workspace gained a project")
	}
}

// TestLoadStateGroupsInterleaved proves a hand-edited, interleaved state file
// loads grouped.
func TestLoadStateGroupsInterleaved(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const raw = `{
  "name": "grouped",
  "workspaces": [
    {"id": "s1", "name": "a1", "project": "alpha", "windows": [], "active": 0},
    {"id": "s2", "name": "b1", "project": "beta", "windows": [], "active": 0},
    {"id": "s3", "name": "a2", "project": "alpha", "windows": [], "active": 0},
    {"id": "s4", "name": "loose", "windows": [], "active": 0}
  ],
  "active": 0,
  "cols": 80,
  "rows": 24,
  "panes": {},
  "nextWorkspace": 4,
  "nextWindow": 0,
  "idVersion": 2,
  "rail": 30,
  "railSet": true
}`
	if err := os.MkdirAll(paths.SessionDirAt(root, "grouped"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.StatePathAt(root, "grouped"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStateAt(root, "grouped")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a1", "a2", "b1", "loose"}
	if names := wsNames(got); !equalStrings(names, want) {
		t.Fatalf("loaded = %v, want %v", names, want)
	}
	if got.Workspaces[0].Project != "alpha" {
		t.Fatalf("project = %q, want alpha", got.Workspaces[0].Project)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestGroupWorkspacesKeepsActive proves a load of an interleaved file leaves the
// active workspace the same workspace, not the same index.
func TestGroupWorkspacesKeepsActive(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const raw = `{
  "name": "active",
  "workspaces": [
    {"id": "s1", "name": "a1", "project": "a", "windows": [], "active": 0},
    {"id": "s2", "name": "b1", "project": "b", "windows": [], "active": 0},
    {"id": "s3", "name": "a2", "project": "a", "windows": [], "active": 0}
  ],
  "active": 2,
  "cols": 80,
  "rows": 24,
  "panes": {},
  "nextWorkspace": 3,
  "nextWindow": 0,
  "idVersion": 2,
  "rail": 30,
  "railSet": true
}`
	if err := os.MkdirAll(paths.SessionDirAt(root, "active"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.StatePathAt(root, "active"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStateAt(root, "active")
	if err != nil {
		t.Fatal(err)
	}
	if names := wsNames(got); !equalStrings(names, []string{"a1", "a2", "b1"}) {
		t.Fatalf("loaded = %v, want [a1 a2 b1]", names)
	}
	ws := got.ActiveWorkspace()
	if ws.ID != "s3" {
		t.Fatalf("active = %q, want s3", ws.ID)
	}
}

// TestGroupWorkspacesOutOfRangeActive proves grouping leaves an out-of-range
// Active out of range instead of panicking or inventing a workspace.
func TestGroupWorkspacesOutOfRangeActive(t *testing.T) {
	t.Parallel()
	for _, active := range []int{5, -1} {
		st := &State{
			Active: active,
			Workspaces: []*Workspace{
				workspace("s1", "a1", "a"),
				workspace("s2", "b1", "b"),
				workspace("s3", "a2", "a"),
			},
		}
		st.GroupWorkspaces()
		if st.Active != active {
			t.Fatalf("active = %d, want %d left alone", st.Active, active)
		}
	}
}

// TestStatePathsAt proves the state's path methods come from the root it was
// built with, not from the environment.
func TestStatePathsAt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	st := NewStateAt(root, "paths")
	if st.Dir() != root {
		t.Fatalf("dir = %q, want %q", st.Dir(), root)
	}
	if got, want := st.SessionDir(), filepath.Join(root, "sessions", "paths"); got != want {
		t.Fatalf("session dir = %q, want %q", got, want)
	}
	if got, want := st.ServerSock(), filepath.Join(root, "sessions", "paths", "server.sock"); got != want {
		t.Fatalf("server sock = %q, want %q", got, want)
	}
	if got, want := st.PaneSock("w1p1"), filepath.Join(root, "sessions", "paths", "panes", "w1p1.sock"); got != want {
		t.Fatalf("pane sock = %q, want %q", got, want)
	}
	if got, want := st.StatePath(), filepath.Join(root, "sessions", "paths", "state.json"); got != want {
		t.Fatalf("state path = %q, want %q", got, want)
	}
}

// TestEnvFallback is the one serial test. Its subject is the environment
// fallback: NewState and LoadState with no explicit root must follow DM_STATE.
// t.Setenv mutates process env, so the test cannot run in parallel with the
// others.
func TestEnvFallback(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DM_STATE", root)
	st := NewState("fb")
	if st.Dir() != root {
		t.Fatalf("new state dir = %q, want %q", st.Dir(), root)
	}
	st.Workspaces[0].Unfolded = false
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState("fb")
	if err != nil {
		t.Fatal(err)
	}
	if got.Dir() != root {
		t.Fatalf("loaded state dir = %q, want %q", got.Dir(), root)
	}
	if got.Workspaces[0].Unfolded {
		t.Fatal("env-loaded workspace is unfolded, want folded")
	}
}

// TestWindowSyncRoundTrip proves the window sync flag survives a save and a
// reload, and that a state file written before the flag existed loads with it
// off.
func TestWindowSyncRoundTrip(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	st := NewStateAt(root, "sync")
	st.ActiveWindow().Sync = true
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStateAt(root, "sync")
	if err != nil {
		t.Fatal(err)
	}
	if !got.ActiveWindow().Sync {
		t.Fatal("a synced window loaded unsynced")
	}

	// An older state file carries no sync key.
	const raw = `{
  "name": "oldsync",
  "workspaces": [
    {"id": "s1", "name": "main",
     "windows": [{"id": "w1", "name": "one", "root": {"pane": "w1p1"}, "focus": "w1p1"}],
     "active": 0}
  ],
  "active": 0,
  "cols": 80,
  "rows": 24,
  "panes": {},
  "nextWorkspace": 1,
  "nextWindow": 1,
  "idVersion": 2
}`
	if err := os.MkdirAll(paths.SessionDirAt(root, "oldsync"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.StatePathAt(root, "oldsync"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = LoadStateAt(root, "oldsync")
	if err != nil {
		t.Fatal(err)
	}
	if got.ActiveWindow().Sync {
		t.Fatal("a window with no sync key loaded synced")
	}
}

// TestShelfDefaultsEmpty proves the shelf is zero on a state file written
// before it existed: no members, no active member, unfolded.
func TestShelfDefaultsEmpty(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const raw = `{
  "name": "oldshelf",
  "workspaces": [
    {"id": "s1", "name": "main",
     "windows": [{"id": "w1", "name": "one", "root": {"pane": "w1p1"}, "focus": "w1p1"}],
     "active": 0}
  ],
  "active": 0,
  "cols": 80,
  "rows": 24,
  "panes": {"w1p1": {"cols": 20, "rows": 10}},
  "nextWorkspace": 1,
  "nextWindow": 1,
  "idVersion": 2
}`
	if err := os.MkdirAll(paths.SessionDirAt(root, "oldshelf"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.StatePathAt(root, "oldshelf"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStateAt(root, "oldshelf")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Shelf) != 0 || got.ShelfActive != 0 || got.ShelfFolded {
		t.Fatalf("old state loaded shelf %v active %d folded %v, want an empty shelf",
			got.Shelf, got.ShelfActive, got.ShelfFolded)
	}
	if got.Shelved("w1p1") {
		t.Fatal("a tree pane loaded shelved")
	}
	if idx := got.ShelfIndex("w1p1"); idx != -1 {
		t.Fatalf("ShelfIndex of a tree pane = %d, want -1", idx)
	}
}

// TestShelfAddDrop is the shelf index contract in a table: an add keeps the
// active member where it was, a drop keeps the index on the member it named,
// and both ends of the slice stay in range.
func TestShelfAddDrop(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		shelf  []string
		active int
		op     string
		id     string
		want   []string
		at     int
	}{
		{"add to an empty shelf activates it", nil, 3, "add", "a", []string{"a"}, 0},
		{"add keeps the active member", []string{"a"}, 0, "add", "b", []string{"a", "b"}, 0},
		{"add refuses a duplicate", []string{"a"}, 0, "add", "a", []string{"a"}, 0},
		{"drop the active first member", []string{"a", "b"}, 0, "drop", "a", []string{"b"}, 0},
		{"drop before the active member steps it back", []string{"a", "b", "c"}, 2, "drop", "a", []string{"b", "c"}, 1},
		{"drop after the active member leaves it", []string{"a", "b", "c"}, 0, "drop", "c", []string{"a", "b"}, 0},
		{"drop the last member empties the shelf", []string{"a"}, 0, "drop", "a", nil, 0},
		{"drop a stranger changes nothing", []string{"a"}, 0, "drop", "z", []string{"a"}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := &State{Shelf: append([]string(nil), tc.shelf...), ShelfActive: tc.active}
			switch tc.op {
			case "add":
				st.AddShelf(tc.id)
			case "drop":
				st.DropShelf(tc.id)
			}
			if len(st.Shelf) != len(tc.want) {
				t.Fatalf("shelf = %v, want %v", st.Shelf, tc.want)
			}
			for i := range st.Shelf {
				if st.Shelf[i] != tc.want[i] {
					t.Fatalf("shelf = %v, want %v", st.Shelf, tc.want)
				}
			}
			if st.ShelfActive != tc.at {
				t.Fatalf("active = %d, want %d", st.ShelfActive, tc.at)
			}
		})
	}
}

// TestShelfClampAndPrune proves the index never leaves the slice and a member
// whose pane the session no longer carries is dropped on load.
func TestShelfClampAndPrune(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		shelf []string
		at    int
		want  int
	}{
		{"empty shelf", nil, 4, 0},
		{"past the end", []string{"a", "b"}, 9, 1},
		{"negative", []string{"a", "b"}, -3, 0},
		{"inside", []string{"a", "b"}, 1, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := &State{Shelf: tc.shelf, ShelfActive: tc.at}
			st.ClampShelfActive()
			if st.ShelfActive != tc.want {
				t.Fatalf("active = %d, want %d", st.ShelfActive, tc.want)
			}
		})
	}
	// A dead member before the active one steps the index back with it, so the
	// prune keeps the same member active: x at 0, then a at 1, b at 2, c at 3.
	st := &State{
		Shelf:       []string{"x", "a", "b", "c"},
		ShelfActive: 2,
		Panes:       map[string]*PaneState{"a": {}, "b": {}, "c": {}},
	}
	if !st.PruneShelf() {
		t.Fatal("PruneShelf dropped nothing, want the dead member gone")
	}
	if len(st.Shelf) != 3 || st.Shelf[0] != "a" || st.Shelf[1] != "b" || st.Shelf[2] != "c" {
		t.Fatalf("shelf = %v, want a, b then c", st.Shelf)
	}
	if st.ShelfActive != 1 || st.Shelf[st.ShelfActive] != "b" {
		t.Fatalf("active = %d (%q), want 1 and b", st.ShelfActive, st.Shelf[st.ShelfActive])
	}
	if st.PruneShelf() {
		t.Fatal("a second prune dropped something")
	}
	// A dead member after the active one leaves the index alone.
	st = &State{
		Shelf:       []string{"a", "b", "gone"},
		ShelfActive: 0,
		Panes:       map[string]*PaneState{"a": {}, "b": {}},
	}
	if !st.PruneShelf() {
		t.Fatal("PruneShelf dropped nothing, want the tail member gone")
	}
	if st.ShelfActive != 0 || st.Shelf[0] != "a" {
		t.Fatalf("active = %d (%q), want 0 and a", st.ShelfActive, st.Shelf[0])
	}
}

// askState is a well-formed ask for the store tests.
func askState(id, toolCall string) ask.Request {
	return ask.Request{
		ID: id, Pane: "w1p1", ToolCallID: toolCall,
		Questions: []ask.Question{{
			Question: "Which base?", Header: "base",
			Options: []ask.Option{{Label: "main"}, {Label: "next"}},
		}},
	}
}

// TestAskDefaultsEmpty proves a state file written before asks existed loads
// with an empty store and a zero counter.
func TestAskDefaultsEmpty(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const raw = `{
  "name": "oldask",
  "workspaces": [{"id": "s1", "name": "main",
    "windows": [{"id": "w1", "name": "one", "root": {"pane": "w1p1"}, "focus": "w1p1"}],
    "active": 0}],
  "active": 0,
  "cols": 80, "rows": 24,
  "panes": {"w1p1": {}},
  "nextWorkspace": 1, "nextWindow": 1, "idVersion": 2
}`
	if err := os.MkdirAll(paths.SessionDirAt(root, "oldask"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.StatePathAt(root, "oldask"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStateAt(root, "oldask")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Ask) != 0 || got.AskSeq != 0 {
		t.Fatalf("old state loaded asks %v seq %d, want an empty store", got.Ask, got.AskSeq)
	}
	if id := got.NewAskID(); id != "a1" {
		t.Fatalf("first ask id = %q", id)
	}
}

// TestAskRoundTripAndSeq proves an ask survives a save and load, the index
// finds it, and the counter moves past a hand-edited id so no id is reused.
func TestAskRoundTripAndSeq(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	st := NewStateAt(root, "askstore")
	st.Ask = []ask.Request{askState("a4", "toolu_1")}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStateAt(root, "askstore")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Ask) != 1 || got.Ask[0].Questions[0].Question != "Which base?" {
		t.Fatalf("loaded asks = %+v", got.Ask)
	}
	if idx := got.AskIndex("a4"); idx != 0 {
		t.Fatalf("AskIndex(a4) = %d, want 0", idx)
	}
	if idx := got.AskIndex("a9"); idx != -1 {
		t.Fatalf("AskIndex(a9) = %d, want -1", idx)
	}
	// The counter loads past the listed id, so the next open cannot reuse a4.
	if id := got.NewAskID(); id != "a5" {
		t.Fatalf("next ask id = %q, want a5", id)
	}
}

// TestLoadStateDropsBadAsks proves a hand-edited store costs its bad requests,
// not the whole state file.
func TestLoadStateDropsBadAsks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	st := NewStateAt(root, "askbad")
	st.Ask = []ask.Request{askState("a1", "toolu_1"), askState("", "toolu_2")}
	// An ask with no questions cannot pass Validate.
	st.Ask[1].Questions = nil
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStateAt(root, "askbad")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Ask) != 1 || got.Ask[0].ID != "a1" {
		t.Fatalf("loaded asks = %+v, want only a1", got.Ask)
	}
}

// writeRawState writes body as the state file for name, so a test can hand the
// loader a shape no State literal produces: encoding/json turns a null element
// into a nil pointer.
func writeRawState(t *testing.T, root, name, body string) {
	t.Helper()
	if err := os.MkdirAll(paths.SessionDirAt(root, name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.StatePathAt(root, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestLoadStateRejectsNullElements proves the loader refuses a null workspace,
// window or pane instead of panicking: a hand edit or a foreign writer gets a
// diagnostic that names the collection and the index.
func TestLoadStateRejectsNullElements(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want string
	}{
		{"workspace no ids", `{"workspaces":[null]}`, "workspaces[0]"},
		{"workspace v2", `{"idVersion":2,"workspaces":[null]}`, "workspaces[0]"},
		{"window no ids", `{"workspaces":[{"id":"s1","name":"main","windows":[null]}]}`, "workspaces[0].windows[0]"},
		{"window v2", `{"idVersion":2,"workspaces":[{"id":"s1","name":"main","windows":[null]}]}`, "workspaces[0].windows[0]"},
		{"pane no ids", `{"panes":{"w1p1":null}}`, `panes["w1p1"]`},
		{"pane v2", `{"idVersion":2,"panes":{"w1p1":null}}`, `panes["w1p1"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeRawState(t, root, "nullified", tc.body)
			st, err := LoadStateAt(root, "nullified")
			if err == nil {
				t.Fatalf("LoadStateAt succeeded with %+v, want an error", st)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to name %s", err, tc.want)
			}
		})
	}
}
