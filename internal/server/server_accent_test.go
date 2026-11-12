package server

import (
	"fmt"
	"testing"

	"dm/internal/layout"
	"dm/internal/rail"
	"dm/internal/session"
)

// accentState builds a state with one workspace per project, s1 in a and s2
// in b, and pins both names the way a loaded session would have.
func accentState(t *testing.T) (*session.State, *server) {
	t.Helper()
	st := session.NewStateAt(t.TempDir(), "accent")
	st.Workspaces = []*session.Workspace{
		{ID: "s1", Name: "one", Project: "a", Unfolded: true, Windows: []*session.Window{{ID: "w1", Name: "1"}}},
		{ID: "s2", Name: "two", Project: "b", Unfolded: true, Windows: []*session.Window{{ID: "w2", Name: "2"}}},
	}
	st.NextWorkspace = 2
	st.NextWindow = 2
	s := railServer(st)
	s.mu.Lock()
	s.refreshProjectAccentsLocked()
	s.mu.Unlock()
	return st, s
}

// TestAccentSlotsSurviveJoinAndLeave proves a newcomer takes a free slot
// while the settled projects keep theirs, and that its departure frees the
// slot without moving anyone else.
func TestAccentSlotsSurviveJoinAndLeave(t *testing.T) {
	t.Parallel()
	st, s := accentState(t)
	slotA, slotB := st.ProjectAccent["a"], st.ProjectAccent["b"]

	fakePane(t, st, "w3p1")
	wsid, _, _, err := s.newWorkspace("", "", "/tmp", "c", true, nil)
	if err != nil {
		t.Fatalf("newWorkspace: %v", err)
	}
	slotC, ok := st.ProjectAccent["c"]
	if !ok {
		t.Fatalf("project c got no slot: %v", st.ProjectAccent)
	}
	if slotC == slotA || slotC == slotB {
		t.Fatalf("project c took a held slot %d", slotC)
	}
	if st.ProjectAccent["a"] != slotA || st.ProjectAccent["b"] != slotB {
		t.Fatalf("join moved the settled hues: a=%d b=%d, want %d %d",
			st.ProjectAccent["a"], st.ProjectAccent["b"], slotA, slotB)
	}

	if err := s.closeWorkspace(wsid); err != nil {
		t.Fatalf("closeWorkspace: %v", err)
	}
	if _, ok := st.ProjectAccent["c"]; ok {
		t.Fatalf("closed project c kept its slot: %v", st.ProjectAccent)
	}
	if st.ProjectAccent["a"] != slotA || st.ProjectAccent["b"] != slotB {
		t.Fatalf("leave moved the settled hues: a=%d b=%d, want %d %d",
			st.ProjectAccent["a"], st.ProjectAccent["b"], slotA, slotB)
	}
}

// TestAccentSlotsSurviveRestart proves the slot map persists in the state
// file and a reloaded session keeps every hue without reassigning.
func TestAccentSlotsSurviveRestart(t *testing.T) {
	t.Parallel()
	st, _ := accentState(t)
	slotA, slotB := st.ProjectAccent["a"], st.ProjectAccent["b"]
	if err := st.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	st2, err := session.LoadStateAt(st.Dir(), st.Name)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	s2 := railServer(st2)
	s2.mu.Lock()
	changed := s2.refreshProjectAccentsLocked()
	s2.mu.Unlock()
	if changed {
		t.Fatalf("reload reassigned slots: %v", st2.ProjectAccent)
	}
	if st2.ProjectAccent["a"] != slotA || st2.ProjectAccent["b"] != slotB {
		t.Fatalf("restart moved the hues: a=%d b=%d, want %d %d",
			st2.ProjectAccent["a"], st2.ProjectAccent["b"], slotA, slotB)
	}
}

// TestAccentSlotFreesForLaterProject proves a closed project's slot frees and
// a later project that prefers that slot takes it.
func TestAccentSlotFreesForLaterProject(t *testing.T) {
	t.Parallel()
	st, s := accentState(t)
	slotA := st.ProjectAccent["a"]
	if err := s.closeWorkspace("s1"); err != nil {
		t.Fatalf("closeWorkspace: %v", err)
	}
	if _, ok := st.ProjectAccent["a"]; ok {
		t.Fatalf("closed project a kept its slot: %v", st.ProjectAccent)
	}
	// Find a project name whose preferred slot is the freed one.
	name := ""
	for i := 0; i < 1000; i++ {
		if rail.RailAccentSlot(fmt.Sprintf("proj-%d", i)) == slotA {
			name = fmt.Sprintf("proj-%d", i)
			break
		}
	}
	if name == "" {
		t.Fatalf("found no project name preferring slot %d", slotA)
	}
	fakePane(t, st, "w3p1")
	if _, _, _, err := s.newWorkspace("", "", "/tmp", name, true, nil); err != nil {
		t.Fatalf("newWorkspace: %v", err)
	}
	if got := st.ProjectAccent[name]; got != slotA {
		t.Fatalf("project %q took slot %d, want the freed %d", name, got, slotA)
	}
}

// TestSnapshotCarriesProjectAccent proves the snapshot's rows carry the slot
// the map pinned, and the flag that marks a carried slot zero.
func TestSnapshotCarriesProjectAccent(t *testing.T) {
	t.Parallel()
	st, s := accentState(t)
	// Give each workspace a pane so the snapshot has rows to carry the slot.
	for _, ws := range st.Workspaces {
		id := ws.Windows[0].NewPaneID()
		ws.Windows[0].Root = &layout.Node{Pane: id}
		st.Panes[id] = &session.PaneState{}
	}
	snap := snapshot(s)
	seen := map[string]bool{}
	for _, p := range snap.All {
		if p.Project == "" {
			continue
		}
		want, ok := st.ProjectAccent[p.Project]
		if !ok {
			t.Fatalf("row %+v carries an unpinned project", p)
		}
		if !p.ProjectAccentSet || p.ProjectAccent != want {
			t.Fatalf("row %+v carries slot %d set=%v, want %d", p, p.ProjectAccent, p.ProjectAccentSet, want)
		}
		seen[p.Project] = true
	}
	for _, name := range []string{"a", "b"} {
		if !seen[name] {
			t.Fatalf("snapshot has no rows for project %q: %+v", name, snap.All)
		}
	}
}
