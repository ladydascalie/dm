package server

import (
	"testing"

	"dm/internal/layout"
	"dm/internal/session"
	"dm/internal/testutil"
)

func TestAllPanesWorkspaceUnfolded(t *testing.T) {
	t.Parallel()
	st := session.NewStateAt(t.TempDir(), "unfolded")
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

// TestAllPanesCarriesTheShelf pins the shelf's rows in the fleet list: one row
// per shelf member after the window rows, in the shelf's own order, carrying the
// pane's name and status and no workspace or window. A live pane carries no
// shelf mark.
func TestAllPanesCarriesTheShelf(t *testing.T) {
	t.Parallel()
	st := session.NewStateAt(t.TempDir(), "shelfrows")
	live := testutil.RootPane(st)
	st.Panes[live].Name = "main"
	first := "shelfrows-p2"
	second := "shelfrows-p3"
	st.Panes[first] = &session.PaneState{Name: "set aside", Cwd: t.TempDir(), Reported: true}
	st.Panes[second] = &session.PaneState{Cwd: t.TempDir()}
	st.AddShelf(first)
	st.AddShelf(second)
	s := railServer(st)

	panes := s.allPanesLocked()
	if len(panes) != 3 {
		t.Fatalf("%d rows, want 3: %+v", len(panes), panes)
	}
	if row := panes[0]; row.ID != live || row.Shelf {
		t.Fatalf("first row %+v, want the live pane %s with no shelf mark", row, live)
	}
	for i, want := range []string{first, second} {
		row := panes[i+1]
		if row.ID != want || !row.Shelf {
			t.Fatalf("shelf row %d is %+v, want %s with the shelf mark", i, row, want)
		}
		if row.Workspace != "" || row.Window != "" || row.WorkspaceName != "" || row.WindowName != "" {
			t.Fatalf("shelf row %s invented a place: %+v", row.ID, row)
		}
	}
	if row := panes[1]; row.Name != "set aside" || !row.Agent {
		t.Fatalf("shelf row lost its facts: %+v", row)
	}
}

// TestPaneRectsReserveTabBarRow pins the server side of the reserve: every
// active-window pane box starts below the tab bar row and stops above the
// status bar row. The zoomed pane fills the reserved area.
func TestPaneRectsReserveTabBarRow(t *testing.T) {
	t.Parallel()
	st := session.NewStateAt(t.TempDir(), "tabbar")
	id := testutil.RootPane(st)
	s := railServer(st)

	area := layout.PaneAreaForWithTabBar(s.cols, s.rows, s.railLocked())
	if area.Y != layout.TabBarRows {
		t.Fatalf("pane area starts at row %d, want %d", area.Y, layout.TabBarRows)
	}
	if got, want := s.paneRectsLocked()[id], layout.InsetBox(area, s.cfg.Padding()); got != want {
		t.Fatalf("pane rect %+v, want %+v", got, want)
	}

	// A split keeps each box inside the reserved area too.
	w := st.Workspaces[0].Windows[0]
	w.Root = &layout.Node{Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*layout.Node{{Pane: id}, {Pane: "tabbar-p2"}}}
	st.Panes["tabbar-p2"] = &session.PaneState{}
	for pid, r := range s.paneRectsLocked() {
		if r.Y <= layout.TabBarRows {
			t.Fatalf("%s starts at row %d, want below the tab bar row %d", pid, r.Y, layout.TabBarRows)
		}
		if r.Y+r.H > s.rows-1 {
			t.Fatalf("%s reaches row %d, want above the status bar row %d", pid, r.Y+r.H, s.rows-1)
		}
	}

	// Zoom fills the area below the tab bar.
	s.zoom = "tabbar-p2"
	if got, want := s.paneRectsLocked()["tabbar-p2"], layout.InsetBox(area, s.cfg.Padding()); got != want {
		t.Fatalf("zoomed rect %+v, want %+v", got, want)
	}
}
