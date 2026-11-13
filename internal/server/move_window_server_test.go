package server

import (
	"strings"
	"testing"

	"dm/internal/session"
)

// moveWindow swaps the named window with its neighbour in the Windows slice,
// and leaves the focus and the active window alone.
func TestMoveWindowSwapsNeighbour(t *testing.T) {
	t.Parallel()
	s, st := windowServer(t)
	ws := st.ActiveWorkspace()
	w1 := ws.Windows[0]
	w2, _ := addWindow(st, "two")
	w3, _ := addWindow(st, "three")

	if err := s.moveWindow(w3.ID, "up"); err != nil {
		t.Fatalf("moveWindow(up): %v", err)
	}
	want := []*session.Window{w1, w3, w2}
	for i, w := range want {
		if ws.Windows[i] != w {
			t.Fatalf("windows[%d] = %q, want %q", i, ws.Windows[i].Name, w.Name)
		}
	}
	if ws.Active != 0 {
		t.Fatalf("active window = %d, want 0", ws.Active)
	}
	if err := s.moveWindow(w3.ID, "down"); err != nil {
		t.Fatalf("moveWindow(down): %v", err)
	}
	if ws.Windows[1] != w2 || ws.Windows[2] != w3 {
		t.Fatalf("down did not undo up: %q %q", ws.Windows[1].Name, ws.Windows[2].Name)
	}
}

// A move at either edge of the workspace is a no-op, not an error.
func TestMoveWindowEdgesNoop(t *testing.T) {
	t.Parallel()
	s, st := windowServer(t)
	ws := st.ActiveWorkspace()
	w1 := ws.Windows[0]
	w2, _ := addWindow(st, "two")
	before := append([]*session.Window(nil), ws.Windows...)

	if err := s.moveWindow(w1.ID, "up"); err != nil {
		t.Fatalf("moveWindow(up) at the top: %v", err)
	}
	if err := s.moveWindow(w2.ID, "down"); err != nil {
		t.Fatalf("moveWindow(down) at the bottom: %v", err)
	}
	for i := range before {
		if ws.Windows[i] != before[i] {
			t.Fatalf("edge move reordered the block: %q at %d", ws.Windows[i].Name, i)
		}
	}
}

// A workspace block never mixes with another: a move inside the second
// workspace stays inside it.
func TestMoveWindowStaysInWorkspace(t *testing.T) {
	t.Parallel()
	s, st := windowServer(t)
	first := st.ActiveWorkspace()
	other, _, _ := addWorkspace(st, "other")
	w1 := first.Windows[0]
	_, _ = addWindow(st, "second")

	if err := s.moveWindow(other.Windows[0].ID, "up"); err != nil {
		t.Fatalf("moveWindow(up): %v", err)
	}
	if len(first.Windows) != 2 || first.Windows[0] != w1 {
		t.Fatalf("the move left its workspace: %v", first.Windows)
	}
}

// An unknown window and a bad direction each return the error.
func TestMoveWindowRefusals(t *testing.T) {
	t.Parallel()
	s, st := windowServer(t)
	w1 := st.ActiveWorkspace().Windows[0]

	if err := s.moveWindow("w99", "up"); err == nil || !strings.Contains(err.Error(), `no window "w99"`) {
		t.Fatalf("unknown window error = %v, want no window \"w99\"", err)
	}
	if err := s.moveWindow(w1.ID, "sideways"); err == nil || !strings.Contains(err.Error(), "dir must be up or down") {
		t.Fatalf("bad dir error = %v, want dir must be up or down", err)
	}
}
