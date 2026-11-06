package server

import (
	"testing"

	"dm/internal/layout"
	"dm/internal/session"
)

func TestAllPanesWorkspaceUnfolded(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	st := session.NewState("unfolded")
	s := railServer(st)
	ws := st.Workspaces[0]
	w := ws.Windows[0]
	w.Root = &layout.Node{Pane: "w1p1"}
	w.Focus = "w1p1"

	// Unfolded workspace
	ws.Unfolded = true
	panes := s.allPanesLocked()
	if len(panes) != 1 {
		t.Fatalf("got %d panes, want 1", len(panes))
	}
	if !panes[0].WorkspaceUnfolded {
		t.Fatal("WorkspaceUnfolded = false, want true")
	}

	// Folded workspace
	ws.Unfolded = false
	panes = s.allPanesLocked()
	if len(panes) != 1 {
		t.Fatalf("got %d panes, want 1", len(panes))
	}
	if panes[0].WorkspaceUnfolded {
		t.Fatal("WorkspaceUnfolded = true, want false")
	}
}
