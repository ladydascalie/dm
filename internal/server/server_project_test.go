package server

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"dm/internal/layout"
	"dm/internal/paths"
	"dm/internal/session"
	"dm/internal/wire"
)

// projectState builds a state with three workspaces: s1 and s3 in project a,
// s2 in project b. The slice is interleaved on purpose.
func projectState(t *testing.T) *session.State {
	t.Helper()
	t.Setenv("DM_STATE", t.TempDir())
	st := session.NewState("project")
	st.Workspaces = []*session.Workspace{
		{ID: "s1", Name: "one", Project: "a", Unfolded: true, Windows: []*session.Window{{ID: "w1", Name: "1"}}},
		{ID: "s2", Name: "two", Project: "b", Unfolded: true, Windows: []*session.Window{{ID: "w2", Name: "2"}}},
		{ID: "s3", Name: "three", Project: "a", Unfolded: true, Windows: []*session.Window{{ID: "w3", Name: "3"}}},
	}
	st.NextWorkspace = 3
	st.NextWindow = 3
	return st
}

func workspaceIDs(st *session.State) []string {
	out := make([]string, 0, len(st.Workspaces))
	for _, ws := range st.Workspaces {
		out = append(out, ws.ID)
	}
	return out
}

func wantIDs(t *testing.T, st *session.State, want ...string) {
	t.Helper()
	got := workspaceIDs(st)
	if len(got) != len(want) {
		t.Fatalf("workspace order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("workspace order = %v, want %v", got, want)
		}
	}
}

// TestSetProjectTagsAndMoves proves set-project tags a workspace and moves it
// to the end of its new project's block.
func TestSetProjectTagsAndMoves(t *testing.T) {
	st := projectState(t)
	s := railServer(st)

	if err := s.setProject("s1", "b"); err != nil {
		t.Fatalf("setProject: %v", err)
	}
	if got := st.Workspaces[1].Project; got != "b" {
		t.Fatalf("s1 project = %q, want b", got)
	}
	wantIDs(t, st, "s2", "s1", "s3")
}

// TestSetProjectClears proves an empty name clears the project and moves the
// workspace out of its old block.
func TestSetProjectClears(t *testing.T) {
	st := projectState(t)
	s := railServer(st)

	if err := s.setProject("s2", ""); err != nil {
		t.Fatalf("setProject: %v", err)
	}
	if got := st.Workspaces[2].Project; got != "" {
		t.Fatalf("s2 project = %q, want empty", got)
	}
	wantIDs(t, st, "s1", "s3", "s2")
}

// TestSetProjectKeepsActive proves moving a workspace does not change which
// workspace is active.
func TestSetProjectKeepsActive(t *testing.T) {
	st := projectState(t)
	st.Active = 2 // s3
	s := railServer(st)

	if err := s.setProject("s1", "b"); err != nil {
		t.Fatalf("setProject: %v", err)
	}
	if got := st.ActiveWorkspace().ID; got != "s3" {
		t.Fatalf("active workspace = %q, want s3", got)
	}
}

// TestNewWorkspaceInheritsProject proves a new workspace takes the active
// workspace's project and lands at the end of that project's block.
func TestNewWorkspaceInheritsProject(t *testing.T) {
	st := projectState(t)
	st.Active = 2 // s3, project a
	s := railServer(st)
	fakePane(t, st, "w4p1")

	wsid, _, _, err := s.newWorkspace("", "", "/tmp", "", false)
	if err != nil {
		t.Fatalf("newWorkspace: %v", err)
	}
	if wsid != "s4" {
		t.Fatalf("workspace id = %q, want s4", wsid)
	}
	if got := st.Workspaces[2].Project; got != "a" {
		t.Fatalf("new workspace project = %q, want a", got)
	}
	wantIDs(t, st, "s1", "s3", "s4", "s2")
	if got := st.ActiveWorkspace().ID; got != "s4" {
		t.Fatalf("active workspace = %q, want s4", got)
	}
}

// TestNewWorkspaceExplicitProject proves an explicit project wins over the
// active workspace's project and lands at the end of that block.
func TestNewWorkspaceExplicitProject(t *testing.T) {
	st := projectState(t)
	st.Active = 2 // s3, project a
	s := railServer(st)
	fakePane(t, st, "w4p1")

	if _, _, _, err := s.newWorkspace("", "", "/tmp", "b", false); err != nil {
		t.Fatalf("newWorkspace: %v", err)
	}
	if got := st.Workspaces[2].Project; got != "b" {
		t.Fatalf("new workspace project = %q, want b", got)
	}
	wantIDs(t, st, "s1", "s3", "s2", "s4")
}

// TestNewWorkspaceNoFocusKeepsActive proves an insert before the active
// workspace shifts the active index with it.
func TestNewWorkspaceNoFocusKeepsActive(t *testing.T) {
	st := projectState(t)
	// Grouped order, so the insert lands before the active workspace.
	st.Workspaces = []*session.Workspace{st.Workspaces[0], st.Workspaces[2], st.Workspaces[1]}
	st.Active = 2 // s2, project b
	s := railServer(st)
	fakePane(t, st, "w4p1")

	if _, _, _, err := s.newWorkspace("", "", "/tmp", "a", true); err != nil {
		t.Fatalf("newWorkspace: %v", err)
	}
	wantIDs(t, st, "s1", "s3", "s4", "s2")
	if got := st.ActiveWorkspace().ID; got != "s2" {
		t.Fatalf("active workspace = %q, want s2", got)
	}
}

// TestSnapshotCarriesProject proves the snapshot's pane entries carry the
// project of their workspace.
func TestSnapshotCarriesProject(t *testing.T) {
	st := projectState(t)
	ws := st.Workspaces[0]
	ws.Windows[0].Root = &layout.Node{Pane: "w1p1"}
	ws.Windows[0].Focus = "w1p1"
	st.Panes["w1p1"] = &session.PaneState{Cwd: "/tmp"}
	s := railServer(st)

	snap := s.snapshotLocked()
	if len(snap.All) != 1 {
		t.Fatalf("panes = %d, want 1", len(snap.All))
	}
	if got := snap.All[0].Project; got != "a" {
		t.Fatalf("AllPane.Project = %q, want a", got)
	}

	list := s.workspaceList()
	if got := list[0].Project; got != "a" {
		t.Fatalf("wsInfo.Project = %q, want a", got)
	}
}

// fakePane listens on the pane socket and answers every request, so
// newWorkspace adopts a pane instead of spawning a shell.
func fakePane(t *testing.T, st *session.State, paneID string) {
	t.Helper()
	sock := paths.PaneSock(st.Name, paneID)
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				for {
					m, err := wire.ReadMsg(c)
					if err != nil {
						return
					}
					if m.Type == "req" {
						_ = wire.WriteMsg(c, wire.Msg{Type: "resp", OK: true, ID: m.ID})
					}
				}
			}(conn)
		}
	}()
}
