package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/hinshun/vt10x"
)

func TestFloatingPaneDraw(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(20, 5))
	if _, err := vt.Write([]byte("Hello, Float!")); err != nil {
		t.Fatal(err)
	}
	fp := &FloatingPane{
		ID:     "float1",
		Rect:   Rect{X: 2, Y: 2, W: 20, H: 5},
		VT:     vt,
		Active: true,
	}
	var buf bytes.Buffer
	fp.Draw(&buf, DefaultTheme(), nil)
	out := buf.String()
	if !strings.Contains(out, "Hello, Float!") {
		t.Fatalf("missing text in output: %q", out)
	}
	if !strings.Contains(out, "┌") || !strings.Contains(out, "┐") {
		t.Fatalf("missing border in output: %q", out)
	}
}

func TestFloatingPaneInBands(t *testing.T) {
	tui := &tui{
		snap:   &Snapshot{Cols: 80, Rows: 24},
		vts:    map[string]vt10x.Terminal{},
		floats: []*FloatingPane{},
	}
	fp := &FloatingPane{
		ID:     "float1",
		Rect:   Rect{X: 10, Y: 5, W: 20, H: 5},
		VT:     vt10x.New(vt10x.WithSize(20, 5)),
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

// TestHelpBoxFloatPaint proves a float with Paint set and no terminal paints
// its content, its border, its title and its hint.
func TestHelpBoxFloatPaint(t *testing.T) {
	want := Rect{X: 3, Y: 2, W: 10, H: 2}
	calls := 0
	fp := &FloatingPane{
		ID:    "help",
		Rect:  want,
		Title: "prefix",
		Hint:  "esc",
		Paint: func(buf *bytes.Buffer, inner Rect) {
			calls++
			if inner != want {
				t.Fatalf("Paint got %+v, want %+v", inner, want)
			}
			buf.WriteString("\x1b[3;4Hhi        ")
		},
	}
	var buf bytes.Buffer
	fp.Draw(&buf, DefaultTheme(), nil)
	out := buf.String()

	if calls != 1 {
		t.Fatalf("Paint called %d times, want 1", calls)
	}
	for _, s := range []string{"hi", "┌", "┐", "└", "┘", "prefix", "esc"} {
		if !strings.Contains(out, s) {
			t.Fatalf("output is missing %q: %q", s, out)
		}
	}
}

// TestHelpBoxFloatVTPath proves a float with a terminal and no Paint keeps the
// old behaviour: it paints the terminal's cells and its border.
func TestHelpBoxFloatVTPath(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(8, 2))
	if _, err := vt.Write([]byte("VT-CELLS")); err != nil {
		t.Fatal(err)
	}
	fp := &FloatingPane{
		Rect: Rect{X: 1, Y: 1, W: 8, H: 2},
		VT:   vt,
	}
	var buf bytes.Buffer
	fp.Draw(&buf, DefaultTheme(), nil)
	out := buf.String()
	for _, s := range []string{"VT-CELLS", "┌", "└"} {
		if !strings.Contains(out, s) {
			t.Fatalf("output is missing %q: %q", s, out)
		}
	}
}
