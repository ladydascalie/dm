package vt

import "testing"

// cursorTerm returns a terminal with the given scroll region and origin mode.
func cursorTerm(cols, rows int, region ScrollRegion, origin bool) *Term {
	term := NewTerm(cols, rows)
	term.grid.SetScrollRegion(region)
	m := term.grid.Modes()
	m.Origin = origin
	term.grid.SetModes(m)
	return term
}

func fullRegion(cols, rows int) ScrollRegion {
	return ScrollRegion{Top: 0, Bottom: rows - 1, Left: 0, Right: cols - 1}
}

// TestCursorHandlerTable runs one sequence per cursor handler and checks the
// cursor position.
func TestCursorHandlerTable(t *testing.T) {
	const cols, rows = 10, 5
	cases := []struct {
		name  string
		input string
		x, y  int
	}{
		{"CUU", "\x1b[5;5H\x1b[2A", 4, 2},
		{"CUD", "\x1b[1;5H\x1b[2B", 4, 2},
		{"CUF", "\x1b[1;5H\x1b[99C", 9, 0},
		{"CUB", "\x1b[1;5H\x1b[99D", 0, 0},
		{"CNL", "\x1b[2;4H\x1b[2E", 0, 3},
		{"CPL", "\x1b[4;4H\x1b[2F", 0, 1},
		{"CHA", "\x1b[3;4H\x1b[2G", 1, 2},
		{"HPA", "\x1b[3;4H\x1b[2`", 1, 2},
		{"CUP", "\x1b[4;4H", 3, 3},
		{"HVP", "\x1b[2;2f", 1, 1},
		{"VPA", "\x1b[3;4H\x1b[2d", 3, 1},
		{"HPR", "\x1b[1;5H\x1b[2a", 6, 0},
		{"VPR", "\x1b[1;5H\x1b[2e", 4, 2},
		{"CHT", "\x1b[I", 8, 0},
		{"CBT", "\x1b[1;10H\x1b[Z", 8, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term := cursorTerm(cols, rows, fullRegion(cols, rows), false)
			term.Write([]byte(tc.input))
			term.Flush()
			cur := term.grid.Cursor()
			if cur.X != tc.x || cur.Y != tc.y {
				t.Errorf("cursor = %d,%d, want %d,%d", cur.X, cur.Y, tc.x, tc.y)
			}
		})
	}
}

// TestCursorMarginTable pins the margin rules: a move that starts inside the
// scroll region stops at its margins, and one that starts outside stops at the
// screen edge.
func TestCursorMarginTable(t *testing.T) {
	const cols, rows = 20, 10
	region := ScrollRegion{Top: 2, Bottom: 7, Left: 3, Right: 12}
	cases := []struct {
		name  string
		input string
		x, y  int
	}{
		// CUF and HPR stop at the right margin, or at the screen edge.
		{"CUF inside right margin", "\x1b[1;5H\x1b[99C", 12, 0},
		{"CUF right of right margin", "\x1b[1;15H\x1b[99C", 19, 0},
		{"HPR inside right margin", "\x1b[1;5H\x1b[99a", 12, 0},
		// CUB stops at the left margin, or at the screen edge.
		{"CUB inside left margin", "\x1b[1;8H\x1b[99D", 3, 0},
		{"CUB left of left margin", "\x1b[1;2H\x1b[99D", 0, 0},
		// CUU stops at the top margin, or at the top of the screen.
		{"CUU inside top margin", "\x1b[5;5H\x1b[99A", 4, 2},
		{"CUU above top margin", "\x1b[1;5H\x1b[99A", 4, 0},
		// CUD stops at the bottom margin, or at the bottom of the screen.
		{"CUD inside bottom margin", "\x1b[4;5H\x1b[99B", 4, 7},
		{"CUD below bottom margin", "\x1b[9;5H\x1b[99B", 4, 9},
		// CNL and CPL move to the left margin and stop at the margins.
		{"CNL to left margin", "\x1b[3;8H\x1b[99E", 3, 7},
		{"CPL to left margin", "\x1b[6;8H\x1b[99F", 3, 2},
		// CHT stops at the right margin; CBT ignores the margins.
		{"CHT stops at right margin", "\x1b[1;5H\x1b[99I", 12, 0},
		{"CHT right of right margin", "\x1b[1;15H\x1b[I", 16, 0},
		{"CBT ignores right margin", "\x1b[1;13H\x1b[99Z", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term := cursorTerm(cols, rows, region, false)
			term.Write([]byte(tc.input))
			term.Flush()
			cur := term.grid.Cursor()
			if cur.X != tc.x || cur.Y != tc.y {
				t.Errorf("cursor = %d,%d, want %d,%d", cur.X, cur.Y, tc.x, tc.y)
			}
		})
	}
}

// TestCursorOriginModeTable pins which sequences respect origin mode: CHA,
// CUP and HVP are relative to the margins; HPA and VPA are absolute.
func TestCursorOriginModeTable(t *testing.T) {
	const cols, rows = 20, 10
	region := ScrollRegion{Top: 2, Bottom: 7, Left: 3, Right: 12}
	cases := []struct {
		name  string
		input string
		// origin selects origin mode, so the table covers both halves of the
		// rule for the sequences that differ.
		origin bool
		x, y   int
	}{
		{"CHA stays at the left margin", "\x1b[1;1H\x1b[1G", true, 3, 2},
		{"CHA stops at the right margin", "\x1b[1;1H\x1b[99G", true, 12, 2},
		{"CHA ignores margins without origin", "\x1b[1;1H\x1b[1G", false, 0, 0},
		{"HPA ignores origin mode", "\x1b[1;1H\x1b[2`", true, 1, 2},
		{"VPA ignores origin mode", "\x1b[1;1H\x1b[2d", true, 3, 1},
		{"CUP is relative to the margins", "\x1b[1;1H", true, 3, 2},
		{"CUP stops at the margins", "\x1b[99;99H", true, 12, 7},
		{"HVP is relative to the margins", "\x1b[1;1f", true, 3, 2},
		{"CUP ignores margins without origin", "\x1b[1;1H", false, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term := cursorTerm(cols, rows, region, tc.origin)
			term.Write([]byte(tc.input))
			term.Flush()
			cur := term.grid.Cursor()
			if cur.X != tc.x || cur.Y != tc.y {
				t.Errorf("cursor = %d,%d, want %d,%d", cur.X, cur.Y, tc.x, tc.y)
			}
		})
	}
}

// TestCursorSequenceGrids writes a cursor sequence and a glyph, then checks the
// grid each sequence produces.
func TestCursorSequenceGrids(t *testing.T) {
	const cols, rows = 10, 5
	region := ScrollRegion{Top: 1, Bottom: 3, Left: 2, Right: 6}
	cases := []struct {
		name   string
		input  string
		region ScrollRegion
		origin bool
		want   []string
		x, y   int
	}{
		{
			name:   "CUP places a glyph",
			input:  "\x1b[3;5HX",
			region: fullRegion(cols, rows),
			want:   []string{"", "", "    X"},
			x:      5, y: 2,
		},
		{
			name:   "CNL homes to column zero",
			input:  "\x1b[1;5H\x1b[E\x1b[1GX",
			region: fullRegion(cols, rows),
			want:   []string{"", "X"},
			x:      1, y: 1,
		},
		{
			name:   "origin mode CUP lands on the top left margin",
			input:  "\x1b[1;1HX",
			region: region,
			origin: true,
			want:   []string{"", "  X"},
			x:      3, y: 1,
		},
		{
			name:   "CUF stops at the right margin",
			input:  "\x1b[1;3H\x1b[99CX",
			region: region,
			want:   []string{"      X"},
			// The X lands on the right margin, and the cursor stays there with
			// a pending wrap, as it does at the screen edge.
			x: 6, y: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term := cursorTerm(cols, rows, tc.region, tc.origin)
			term.Write([]byte(tc.input))
			term.Flush()
			for y := 0; y < rows; y++ {
				want := ""
				if y < len(tc.want) {
					want = tc.want[y]
				}
				if got := term.grid.Text(y); got != want {
					t.Errorf("row %d = %q, want %q", y, got, want)
				}
			}
			cur := term.grid.Cursor()
			if cur.X != tc.x || cur.Y != tc.y {
				t.Errorf("cursor = %d,%d, want %d,%d", cur.X, cur.Y, tc.x, tc.y)
			}
		})
	}
}

// TestCursorMoveClearsPendingWrap pins that a cursor sequence cancels a pending
// wrap, as xterm does.
func TestCursorMoveClearsPendingWrap(t *testing.T) {
	term := NewTerm(4, 2)
	term.Write([]byte("abcd"))
	term.Flush()
	if !term.grid.Cursor().WrapNext {
		t.Fatal("expected a pending wrap after a full row")
	}
	term.Write([]byte("\x1b[1;1H"))
	term.Flush()
	if term.grid.Cursor().WrapNext {
		t.Error("CUP left the pending wrap set")
	}
}
