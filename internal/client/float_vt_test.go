package client

import (
	"bytes"
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

func TestFloatingPaneInBands(t *testing.T) {
	tui := &tui{
		snap:   &wire.Snapshot{Cols: 80, Rows: 24},
		vts:    map[string]*vt.Term{},
		floats: []*panel.FloatingPane{},
	}
	fp := &panel.FloatingPane{
		ID:     "float1",
		Rect:   layout.Rect{X: 10, Y: 5, W: 20, H: 5},
		VT:     vt.NewTerm(20, 5),
		Active: true,
	}
	tui.AddFloatingPane(fp)
	if len(tui.floats) != 1 {
		t.Fatalf("floats = %d, want 1", len(tui.floats))
	}
	tui.RemoveFloatingPane("float1")
	if len(tui.floats) != 0 {
		t.Fatalf("floats = %d, want 0", len(tui.floats))
	}
}

// TestHelpBoxFloatVTPath proves a float with a terminal paints the terminal's
// cells through the caller's painter and its border through the panel.
func TestHelpBoxFloatVTPath(t *testing.T) {
	vt := vt.NewTerm(8, 2)
	vt.Write([]byte("VT-CELLS"))
	vt.Flush()
	fp := &panel.FloatingPane{
		Rect: layout.Rect{X: 1, Y: 1, W: 8, H: 2},
		VT:   vt,
	}
	fp.Paint = floatVTPaint(fp.VT, nil)
	var buf bytes.Buffer
	fp.Draw(&buf, theme.DefaultTheme())
	out := buf.String()
	for _, s := range []string{"VT-CELLS", "┌", "└"} {
		if !strings.Contains(out, s) {
			t.Fatalf("output is missing %q: %q", s, out)
		}
	}
}
