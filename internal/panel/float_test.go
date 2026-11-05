package panel

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/theme"
)

func TestFloatingPaneDraw(t *testing.T) {
	fp := &FloatingPane{
		ID:     "float1",
		Rect:   layout.Rect{X: 2, Y: 2, W: 20, H: 5},
		Active: true,
		Paint: func(buf *bytes.Buffer, inner layout.Rect) {
			fmt.Fprintf(buf, "\x1b[%d;%dHHello, Float!", inner.Y+1, inner.X+1)
		},
	}
	var buf bytes.Buffer
	fp.Draw(&buf, theme.DefaultTheme())
	out := buf.String()
	if !strings.Contains(out, "Hello, Float!") {
		t.Fatalf("missing text in output: %q", out)
	}
	if !strings.Contains(out, "┌") || !strings.Contains(out, "┐") {
		t.Fatalf("missing border in output: %q", out)
	}
}

// TestHelpBoxFloatPaint proves a float with Paint set paints its content, its
// border, its title and its hint.
func TestHelpBoxFloatPaint(t *testing.T) {
	want := layout.Rect{X: 3, Y: 2, W: 10, H: 2}
	calls := 0
	fp := &FloatingPane{
		ID:    "help",
		Rect:  want,
		Title: "prefix",
		Hint:  "esc",
		Paint: func(buf *bytes.Buffer, inner layout.Rect) {
			calls++
			if inner != want {
				t.Fatalf("Paint got %+v, want %+v", inner, want)
			}
			buf.WriteString("\x1b[3;4Hhi        ")
		},
	}
	var buf bytes.Buffer
	fp.Draw(&buf, theme.DefaultTheme())
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

// TestFloatingPaneNoPaint proves a float with no Paint paints its border and
// nothing inside it.
func TestFloatingPaneNoPaint(t *testing.T) {
	fp := &FloatingPane{
		Rect: layout.Rect{X: 1, Y: 1, W: 8, H: 2},
	}
	var buf bytes.Buffer
	fp.Draw(&buf, theme.DefaultTheme())
	out := buf.String()
	for _, s := range []string{"┌", "└"} {
		if !strings.Contains(out, s) {
			t.Fatalf("output is missing %q: %q", s, out)
		}
	}
}
