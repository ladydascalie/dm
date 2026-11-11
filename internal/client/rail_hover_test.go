package client

import (
	"strings"
	"testing"

	"dm/internal/theme"
)

// TestRailRowHoverTracks follows the pointer over the rail. A motion over an
// item row lights it, a motion off the rail clears it, and a row past the
// last item never lights.
func TestRailRowHoverTracks(t *testing.T) {
	t.Parallel()
	f := newFrameTUI(t, 100, 10, 20)
	// x,y are one-based: x 5 is inside the rail, y 2 is the window row.
	f.tui.handleMouse(mouseEvent{btn: 35, x: 5, y: 2})
	if f.tui.railHover != 1 {
		t.Fatalf("railHover = %d, want 1", f.tui.railHover)
	}
	hoverBG := theme.DefaultTheme().Palette.RailHoverBG.BG()
	if frame := f.frame(); !strings.Contains(frame, hoverBG) {
		t.Fatalf("hovered row is not painted on the hover fill: %q", frame)
	}
	// Leaving the rail clears the hover.
	f.tui.handleMouse(mouseEvent{btn: 35, x: 40, y: 2})
	if f.tui.railHover != -1 {
		t.Fatalf("railHover = %d, want -1 off the rail", f.tui.railHover)
	}
	if frame := f.frame(); strings.Contains(frame, hoverBG) {
		t.Fatalf("hover fill survived leaving the rail: %q", frame)
	}
	// A row past the last item is not an entry.
	f.tui.handleMouse(mouseEvent{btn: 35, x: 5, y: 7})
	if f.tui.railHover != -1 {
		t.Fatalf("railHover = %d, want -1 past the last item", f.tui.railHover)
	}
}

// TestRailRowHoverGates keeps the hover off while a menu is open: a highlight
// under a modal would promise an interaction the modal owns.
func TestRailRowHoverGates(t *testing.T) {
	t.Parallel()
	f := newFrameTUI(t, 100, 10, 20)
	f.tui.handleMouse(mouseEvent{btn: 35, x: 5, y: 2})
	if f.tui.railHover != 1 {
		t.Fatalf("railHover = %d, want 1", f.tui.railHover)
	}
	f.tui.mode = "menu"
	f.tui.handleMouse(mouseEvent{btn: 35, x: 5, y: 3})
	if f.tui.railHover != -1 {
		t.Fatalf("railHover = %d, want -1 while a menu is open", f.tui.railHover)
	}
}
