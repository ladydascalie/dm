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
	fp.Draw(&buf)
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
