package ui

import "testing"

// TestViewportEnsure folds the selection into the list and keeps the window on
// it: the offset stops above the selection, stops below it, and lands on a
// scroll position the list can show whatever the caller left there.
func TestViewportEnsure(t *testing.T) {
	cases := []struct {
		name string
		in   Viewport
		want Viewport
	}{
		{"empty list clears the selection", Viewport{N: 0, H: 3, Sel: 2, Off: 1}, Viewport{N: 0, H: 3, Sel: -1, Off: 0}},
		{"empty list with nothing selected", Viewport{N: 0, H: 3, Sel: -1, Off: 0}, Viewport{N: 0, H: 3, Sel: -1, Off: 0}},
		{"empty list with no height", Viewport{N: 0, H: 0, Sel: -1, Off: 0}, Viewport{N: 0, H: 0, Sel: -1, Off: 0}},
		{"one row", Viewport{N: 1, H: 3, Sel: 0, Off: 0}, Viewport{N: 1, H: 3, Sel: 0, Off: 0}},
		{"one row selects itself", Viewport{N: 1, H: 3, Sel: 9, Off: 0}, Viewport{N: 1, H: 3, Sel: 0, Off: 0}},
		{"nothing selected keeps the window clamped", Viewport{N: 5, H: 3, Sel: -1, Off: 2}, Viewport{N: 5, H: 3, Sel: -1, Off: 2}},
		{"nothing selected above zero", Viewport{N: 5, H: 3, Sel: -4, Off: 9}, Viewport{N: 5, H: 3, Sel: -1, Off: 2}},
		{"selection at the window top", Viewport{N: 10, H: 3, Sel: 5, Off: 5}, Viewport{N: 10, H: 3, Sel: 5, Off: 5}},
		{"selection at the window bottom", Viewport{N: 10, H: 3, Sel: 7, Off: 5}, Viewport{N: 10, H: 3, Sel: 7, Off: 5}},
		{"selection above the window", Viewport{N: 10, H: 3, Sel: 1, Off: 5}, Viewport{N: 10, H: 3, Sel: 1, Off: 1}},
		{"selection below the window", Viewport{N: 10, H: 3, Sel: 8, Off: 5}, Viewport{N: 10, H: 3, Sel: 8, Off: 6}},
		{"selection past the last row", Viewport{N: 10, H: 3, Sel: 12, Off: 0}, Viewport{N: 10, H: 3, Sel: 9, Off: 7}},
		{"offset below zero", Viewport{N: 10, H: 3, Sel: 0, Off: -4}, Viewport{N: 10, H: 3, Sel: 0, Off: 0}},
		{"offset past the end", Viewport{N: 10, H: 3, Sel: 9, Off: 9}, Viewport{N: 10, H: 3, Sel: 9, Off: 7}},
		{"window taller than the list", Viewport{N: 3, H: 10, Sel: 2, Off: 5}, Viewport{N: 3, H: 10, Sel: 2, Off: 0}},
		{"window with no height", Viewport{N: 5, H: 0, Sel: 3, Off: 2}, Viewport{N: 5, H: 0, Sel: 3, Off: 0}},
		{"negative height", Viewport{N: 5, H: -2, Sel: 3, Off: 2}, Viewport{N: 5, H: -2, Sel: 3, Off: 0}},
	}
	for _, c := range cases {
		got := c.in
		got.Ensure()
		if got != c.want {
			t.Fatalf("%s: Ensure = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// TestViewportClamp keeps the offset in [0, max(0, N-H)] and touches nothing
// else, so a view can clamp without moving the selection.
func TestViewportClamp(t *testing.T) {
	cases := []struct {
		name string
		in   Viewport
		want int
	}{
		{"negative offset", Viewport{N: 5, H: 3, Off: -1}, 0},
		{"inside the list", Viewport{N: 5, H: 3, Off: 1}, 1},
		{"last window", Viewport{N: 5, H: 3, Off: 2}, 2},
		{"past the last window", Viewport{N: 5, H: 3, Off: 3}, 2},
		{"empty list", Viewport{N: 0, H: 3, Off: 2}, 0},
		{"window taller than the list", Viewport{N: 3, H: 10, Off: 2}, 0},
		{"window with no height", Viewport{N: 5, H: 0, Off: 2}, 0},
		{"negative height", Viewport{N: 5, H: -1, Off: 2}, 0},
		{"selection untouched", Viewport{N: 5, H: 3, Off: 4, Sel: 1}, 2},
	}
	for _, c := range cases {
		got := c.in
		got.Clamp()
		if got.Off != c.want {
			t.Fatalf("%s: Off = %d, want %d", c.name, got.Off, c.want)
		}
		if got.Sel != c.in.Sel || got.N != c.in.N || got.H != c.in.H {
			t.Fatalf("%s: Clamp touched more than Off: %+v", c.name, got)
		}
	}
}

// TestViewportMove steps the selection and stops at either end of the list. The
// window follows the selection wherever it lands.
func TestViewportMove(t *testing.T) {
	cases := []struct {
		name string
		in   Viewport
		d    int
		want Viewport
	}{
		{"one row down", Viewport{N: 10, H: 3, Sel: 0, Off: 0}, 1, Viewport{N: 10, H: 3, Sel: 1, Off: 0}},
		{"down past the window", Viewport{N: 10, H: 3, Sel: 0, Off: 0}, 3, Viewport{N: 10, H: 3, Sel: 3, Off: 1}},
		{"one row up", Viewport{N: 10, H: 3, Sel: 5, Off: 3}, -1, Viewport{N: 10, H: 3, Sel: 4, Off: 3}},
		{"up above the window", Viewport{N: 10, H: 3, Sel: 5, Off: 5}, -1, Viewport{N: 10, H: 3, Sel: 4, Off: 4}},
		{"stops at the first row", Viewport{N: 10, H: 3, Sel: 0, Off: 0}, -1, Viewport{N: 10, H: 3, Sel: 0, Off: 0}},
		{"stops at the last row", Viewport{N: 10, H: 3, Sel: 0, Off: 0}, 100, Viewport{N: 10, H: 3, Sel: 9, Off: 7}},
		{"a step of zero keeps the row", Viewport{N: 10, H: 3, Sel: 2, Off: 2}, 0, Viewport{N: 10, H: 3, Sel: 2, Off: 2}},
		{"a step of zero keeps the empty selection", Viewport{N: 10, H: 3, Sel: -1, Off: 0}, 0, Viewport{N: 10, H: 3, Sel: -1, Off: 0}},
		{"down from nothing selected", Viewport{N: 10, H: 3, Sel: -1, Off: 0}, 1, Viewport{N: 10, H: 3, Sel: 0, Off: 0}},
		{"up from nothing selected", Viewport{N: 10, H: 3, Sel: -1, Off: 0}, -1, Viewport{N: 10, H: 3, Sel: 0, Off: 0}},
		{"empty list", Viewport{N: 0, H: 3, Sel: -1, Off: 0}, 1, Viewport{N: 0, H: 3, Sel: -1, Off: 0}},
		{"one row list", Viewport{N: 1, H: 3, Sel: 0, Off: 0}, 1, Viewport{N: 1, H: 3, Sel: 0, Off: 0}},
	}
	for _, c := range cases {
		got := c.in
		got.Move(c.d)
		if got != c.want {
			t.Fatalf("%s: Move(%d) = %+v, want %+v", c.name, c.d, got, c.want)
		}
	}
}

// TestViewportPage steps a window at a time, H rows per page, and stops at
// either end of the list exactly as Move does.
func TestViewportPage(t *testing.T) {
	cases := []struct {
		name string
		in   Viewport
		d    int
		want Viewport
	}{
		{"one page down", Viewport{N: 10, H: 3, Sel: 0, Off: 0}, 1, Viewport{N: 10, H: 3, Sel: 3, Off: 1}},
		{"two pages down", Viewport{N: 10, H: 3, Sel: 0, Off: 0}, 2, Viewport{N: 10, H: 3, Sel: 6, Off: 4}},
		{"one page up", Viewport{N: 10, H: 3, Sel: 9, Off: 7}, -1, Viewport{N: 10, H: 3, Sel: 6, Off: 6}},
		{"pages past the last row", Viewport{N: 10, H: 3, Sel: 0, Off: 0}, 10, Viewport{N: 10, H: 3, Sel: 9, Off: 7}},
		{"pages past the first row", Viewport{N: 10, H: 3, Sel: 9, Off: 7}, -10, Viewport{N: 10, H: 3, Sel: 0, Off: 0}},
		{"a page of zero", Viewport{N: 10, H: 3, Sel: 4, Off: 2}, 0, Viewport{N: 10, H: 3, Sel: 4, Off: 2}},
		{"window with no height", Viewport{N: 10, H: 0, Sel: 2, Off: 0}, 1, Viewport{N: 10, H: 0, Sel: 2, Off: 0}},
		{"empty list", Viewport{N: 0, H: 3, Sel: -1, Off: 0}, 1, Viewport{N: 0, H: 3, Sel: -1, Off: 0}},
		{"nothing selected", Viewport{N: 10, H: 3, Sel: -1, Off: 4}, 2, Viewport{N: 10, H: 3, Sel: 5, Off: 4}},
	}
	for _, c := range cases {
		got := c.in
		got.Page(c.d)
		if got != c.want {
			t.Fatalf("%s: Page(%d) = %+v, want %+v", c.name, c.d, got, c.want)
		}
	}
}

// TestViewportTopBottom jumps to either end of the list and brings the window
// with it. An empty list has no end to select.
func TestViewportTopBottom(t *testing.T) {
	top := []struct {
		name string
		in   Viewport
		want Viewport
	}{
		{"from the middle", Viewport{N: 10, H: 3, Sel: 8, Off: 6}, Viewport{N: 10, H: 3, Sel: 0, Off: 0}},
		{"already at the top", Viewport{N: 10, H: 3, Sel: 0, Off: 0}, Viewport{N: 10, H: 3, Sel: 0, Off: 0}},
		{"nothing selected", Viewport{N: 10, H: 3, Sel: -1, Off: 4}, Viewport{N: 10, H: 3, Sel: 0, Off: 0}},
		{"one row list", Viewport{N: 1, H: 1, Sel: 0, Off: 0}, Viewport{N: 1, H: 1, Sel: 0, Off: 0}},
		{"empty list", Viewport{N: 0, H: 3, Sel: 0, Off: 0}, Viewport{N: 0, H: 3, Sel: -1, Off: 0}},
	}
	for _, c := range top {
		got := c.in
		got.Top()
		if got != c.want {
			t.Fatalf("%s: Top = %+v, want %+v", c.name, got, c.want)
		}
	}
	bottom := []struct {
		name string
		in   Viewport
		want Viewport
	}{
		{"from the top", Viewport{N: 10, H: 3, Sel: 0, Off: 0}, Viewport{N: 10, H: 3, Sel: 9, Off: 7}},
		{"already at the end", Viewport{N: 10, H: 3, Sel: 9, Off: 7}, Viewport{N: 10, H: 3, Sel: 9, Off: 7}},
		{"nothing selected", Viewport{N: 10, H: 3, Sel: -1, Off: 0}, Viewport{N: 10, H: 3, Sel: 9, Off: 7}},
		{"one row list", Viewport{N: 1, H: 3, Sel: -1, Off: 0}, Viewport{N: 1, H: 3, Sel: 0, Off: 0}},
		{"empty list", Viewport{N: 0, H: 3, Sel: 0, Off: 0}, Viewport{N: 0, H: 3, Sel: -1, Off: 0}},
	}
	for _, c := range bottom {
		got := c.in
		got.Bottom()
		if got != c.want {
			t.Fatalf("%s: Bottom = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// TestViewportVisible reports the rows the window shows: the window's own rows
// and nothing past the end of the list.
func TestViewportVisible(t *testing.T) {
	cases := []struct {
		name string
		in   Viewport
		i    int
		want bool
	}{
		{"first visible row", Viewport{N: 10, H: 3, Off: 3}, 3, true},
		{"last visible row", Viewport{N: 10, H: 3, Off: 3}, 5, true},
		{"row above the window", Viewport{N: 10, H: 3, Off: 3}, 2, false},
		{"row below the window", Viewport{N: 10, H: 3, Off: 3}, 6, false},
		{"row past the end of the list", Viewport{N: 10, H: 5, Off: 8}, 10, false},
		{"last row of a short window", Viewport{N: 10, H: 5, Off: 8}, 9, true},
		{"empty list", Viewport{N: 0, H: 3, Off: 0}, 0, false},
		{"window with no height", Viewport{N: 10, H: 0, Off: 0}, 0, false},
	}
	for _, c := range cases {
		if got := c.in.Visible(c.i); got != c.want {
			t.Fatalf("%s: Visible(%d) = %v, want %v", c.name, c.i, got, c.want)
		}
	}
}

// TestViewportRowAt maps a screen row to the list row it shows, and answers -1
// for a screen row outside the window and for a window row past the end of the
// list.
func TestViewportRowAt(t *testing.T) {
	cases := []struct {
		name string
		in   Viewport
		y    int
		want int
	}{
		{"top of the window", Viewport{N: 10, H: 3, Off: 3}, 0, 3},
		{"bottom of the window", Viewport{N: 10, H: 3, Off: 3}, 2, 5},
		{"above the window", Viewport{N: 10, H: 3, Off: 3}, -1, -1},
		{"below the window", Viewport{N: 10, H: 3, Off: 3}, 3, -1},
		{"past the end of the list", Viewport{N: 10, H: 3, Off: 8}, 2, -1},
		{"last row of the list", Viewport{N: 10, H: 3, Off: 8}, 1, 9},
		{"empty list", Viewport{N: 0, H: 3, Off: 0}, 0, -1},
		{"window with no height", Viewport{N: 10, H: 0, Off: 0}, 0, -1},
	}
	for _, c := range cases {
		if got := c.in.RowAt(c.y); got != c.want {
			t.Fatalf("%s: RowAt(%d) = %d, want %d", c.name, c.y, got, c.want)
		}
	}
}

// TestViewportKeysFollowTheWindow walks a whole frame: the caller sets the list
// and the height, presses a run of keys, and every frame keeps the selection on
// screen and the window on the list.
func TestViewportKeysFollowTheWindow(t *testing.T) {
	v := Viewport{N: 5, H: 2, Sel: -1}
	steps := []struct {
		name string
		do   func()
		want Viewport
	}{
		{"down selects the first row", func() { v.Move(1) }, Viewport{N: 5, H: 2, Sel: 0, Off: 0}},
		{"page down scrolls", func() { v.Page(1) }, Viewport{N: 5, H: 2, Sel: 2, Off: 1}},
		{"bottom pins the window", func() { v.Bottom() }, Viewport{N: 5, H: 2, Sel: 4, Off: 3}},
		{"up inside the window", func() { v.Move(-1) }, Viewport{N: 5, H: 2, Sel: 3, Off: 3}},
		{"up scrolls the window", func() { v.Move(-1) }, Viewport{N: 5, H: 2, Sel: 2, Off: 2}},
		{"top returns", func() { v.Top() }, Viewport{N: 5, H: 2, Sel: 0, Off: 0}},
		{"a short list keeps one window", func() { v.N = 1; v.Ensure() }, Viewport{N: 1, H: 2, Sel: 0, Off: 0}},
		{"an empty list clears", func() { v.N = 0; v.Ensure() }, Viewport{N: 0, H: 2, Sel: -1, Off: 0}},
	}
	for _, s := range steps {
		s.do()
		if v != s.want {
			t.Fatalf("%s: %+v, want %+v", s.name, v, s.want)
		}
	}
}
