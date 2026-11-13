package server

import (
	"strings"
	"testing"

	"dm/internal/session"
)

// moveWorkspace swaps the named workspace with its neighbour inside its
// project group, and Active follows the workspace it named.
func TestMoveWorkspaceSwapsNeighbour(t *testing.T) {
	t.Parallel()
	s, st := windowServer(t)
	first := st.ActiveWorkspace()
	ws2, _, _ := addWorkspace(st, "second")
	ws3, _, _ := addWorkspace(st, "third")
	st.Active = 1

	if err := s.moveWorkspace(ws3.ID, "up"); err != nil {
		t.Fatalf("moveWorkspace(up): %v", err)
	}
	want := []*session.Workspace{first, ws3, ws2}
	for i, ws := range want {
		if st.Workspaces[i] != ws {
			t.Fatalf("workspaces[%d] = %q, want %q", i, st.Workspaces[i].Name, ws.Name)
		}
	}
	if st.Active != 2 || st.ActiveWorkspace().ID != ws2.ID {
		t.Fatalf("active = %d %q, want 2 %q", st.Active, st.ActiveWorkspace().ID, ws2.ID)
	}
	if err := s.moveWorkspace(ws3.ID, "down"); err != nil {
		t.Fatalf("moveWorkspace(down): %v", err)
	}
	if st.Workspaces[1] != ws2 || st.Workspaces[2] != ws3 {
		t.Fatalf("down did not undo up: %q %q", st.Workspaces[1].Name, st.Workspaces[2].Name)
	}
}

// The edges of the slice are no-ops, and so is the edge of a project group:
// a workspace never interleaves with another group, because the next load
// would pull the groups apart again.
func TestMoveWorkspaceEdges(t *testing.T) {
	t.Parallel()
	s, st := windowServer(t)
	first := st.ActiveWorkspace()
	tagged, _, _ := addWorkspace(st, "tagged")
	tagged.Project = "dm"
	tagged2, _, _ := addWorkspace(st, "tagged two")
	tagged2.Project = "dm"

	before := append([]*session.Workspace(nil), st.Workspaces...)
	if err := s.moveWorkspace(first.ID, "up"); err != nil {
		t.Fatalf("moveWorkspace(up) at the top: %v", err)
	}
	if err := s.moveWorkspace(tagged2.ID, "down"); err != nil {
		t.Fatalf("moveWorkspace(down) at the bottom: %v", err)
	}
	if err := s.moveWorkspace(tagged.ID, "up"); err != nil {
		t.Fatalf("moveWorkspace(up) across the group edge: %v", err)
	}
	for i := range before {
		if st.Workspaces[i] != before[i] {
			t.Fatalf("a refused edge reordered the slice: %q at %d", st.Workspaces[i].Name, i)
		}
	}
}

// Two untagged workspaces are their own loose run and swap like any other
// pair, and the order survives the regroup a load performs.
func TestMoveWorkspaceUntaggedRun(t *testing.T) {
	t.Parallel()
	s, st := windowServer(t)
	st.ActiveWorkspace()
	a, _, _ := addWorkspace(st, "loose a")
	b, _, _ := addWorkspace(st, "loose b")

	if err := s.moveWorkspace(b.ID, "up"); err != nil {
		t.Fatalf("moveWorkspace(up): %v", err)
	}
	if st.Workspaces[len(st.Workspaces)-2] != b || st.Workspaces[len(st.Workspaces)-1] != a {
		t.Fatalf("the untagged run did not swap: %q %q",
			st.Workspaces[len(st.Workspaces)-2].Name, st.Workspaces[len(st.Workspaces)-1].Name)
	}
	st.GroupWorkspaces()
	if st.Workspaces[len(st.Workspaces)-2] != b {
		t.Fatal("the regroup undid the untagged swap")
	}
}

// An unknown workspace and a bad direction each return the error.
func TestMoveWorkspaceRefusals(t *testing.T) {
	t.Parallel()
	s, st := windowServer(t)
	first := st.ActiveWorkspace()

	if err := s.moveWorkspace("s99", "up"); err == nil || !strings.Contains(err.Error(), `no workspace "s99"`) {
		t.Fatalf("unknown workspace error = %v, want no workspace \"s99\"", err)
	}
	if err := s.moveWorkspace(first.ID, "sideways"); err == nil || !strings.Contains(err.Error(), "dir must be up or down") {
		t.Fatalf("bad dir error = %v, want dir must be up or down", err)
	}
}
