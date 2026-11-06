package server

import (
	"strings"
	"testing"
)

// The server renames the named window and leaves the active window alone.
func TestRenameWindowHonoursTarget(t *testing.T) {
	s, st := windowServer(t)
	ws := st.ActiveWorkspace()
	w1 := ws.Windows[0]
	w2, _ := addWindow(st, "two")
	ws.Active = 0

	if err := s.renameWindow(w2.ID, "renamed"); err != nil {
		t.Fatalf("renameWindow(%q): %v", w2.ID, err)
	}
	if w2.Name != "renamed" {
		t.Fatalf("named window name = %q, want renamed", w2.Name)
	}
	if w1.Name == "renamed" {
		t.Fatalf("active window was renamed: %q", w1.Name)
	}
	if ws.Active != 0 || st.ActiveWindow().ID != w1.ID {
		t.Fatalf("active window = %d %q, want 0 %q", ws.Active, st.ActiveWindow().ID, w1.ID)
	}
}

// An unknown window and an empty name each return the error and change
// nothing.
func TestRenameWindowRefusals(t *testing.T) {
	s, st := windowServer(t)
	ws := st.ActiveWorkspace()
	w1 := ws.Windows[0]
	before := w1.Name

	if err := s.renameWindow("w99", "ghost"); err == nil || !strings.Contains(err.Error(), `no window "w99"`) {
		t.Fatalf("unknown window error = %v, want no window \"w99\"", err)
	}
	if err := s.renameWindow(w1.ID, ""); err == nil || !strings.Contains(err.Error(), "name required") {
		t.Fatalf("empty name error = %v, want name required", err)
	}
	if w1.Name != before {
		t.Fatalf("refused rename changed the name: %q -> %q", before, w1.Name)
	}
}
