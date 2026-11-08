package vt

import "testing"

// control writes one byte stream to a fresh terminal and flushes it.
func control(cols, rows int, input string) *Term {
	term := NewTerm(cols, rows)
	term.Write([]byte(input))
	term.Flush()
	return term
}

// TestSequenceTable runs one sequence per registered handler and checks the
// grid and the cursor.
func TestSequenceTable(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
		x, y  int
	}{
		{"CUU", "\x1b[3;2H\x1b[2A", nil, 1, 0},
		{"CUD", "\x1b[1;1H\x1b[3B", nil, 0, 3},
		{"CUF", "\x1b[4C", nil, 4, 0},
		{"CUB", "\x1b[1;6H\x1b[3D", nil, 2, 0},
		{"CNL", "\x1b[2;4H\x1b[2E", nil, 0, 3},
		{"CPL", "\x1b[4;4H\x1b[2F", nil, 0, 1},
		{"CHA", "\x1b[1;5H\x1b[3G", nil, 2, 0},
		{"HPA", "\x1b[1;5H\x1b[3`", nil, 2, 0},
		{"CUP", "\x1b[3;4H", nil, 3, 2},
		{"HVP", "\x1b[3;4f", nil, 3, 2},
		{"VPA", "\x1b[1;5H\x1b[2d", nil, 4, 1},
		{"CHT", "\x1b[I", nil, 8, 0},
		{"CBT", "\x1b[1;10H\x1b[Z", nil, 8, 0},
		{"ED 0", "abc\r\nx\x1b[1;3H\x1b[J", []string{"ab"}, 2, 0},
		{"ED 1", "abc\x1b[2;2H\x1b[1J", []string{"", ""}, 1, 1},
		{"ED 2", "ab\r\ncd\x1b[2J", []string{"", ""}, 2, 1},
		{"EL 0", "abc\x1b[1;2H\x1b[K", []string{"a"}, 1, 0},
		{"EL 1", "abc\x1b[1;2H\x1b[1K", []string{"  c"}, 1, 0},
		{"EL 2", "abc\x1b[1;2H\x1b[2K", []string{""}, 1, 0},
		{"ECH", "abcdef\x1b[1;2H\x1b[2X", []string{"a  def"}, 1, 0},
		{"ICH", "abcdef\x1b[1;2H\x1b[2@", []string{"a  bcdef"}, 1, 0},
		{"DCH", "abcdef\x1b[1;2H\x1b[2P", []string{"adef"}, 1, 0},
		{"IL", "a\r\nb\r\nc\x1b[2;1H\x1b[L", []string{"a", "", "b", "c"}, 0, 1},
		{"DL", "a\r\nb\r\nc\x1b[1;1H\x1b[M", []string{"b", "c"}, 0, 0},
		{"SU", "a\r\nb\r\nc\x1b[S", []string{"b", "c"}, 1, 2},
		{"SD", "a\r\nb\r\nc\x1b[T", []string{"", "a", "b", "c"}, 1, 2},
		{"HTS", "\x1b[1;3H\x1bH\x1b[1;1H\tx", []string{"  x"}, 3, 0},
		{"TBC 0", "\x1b[1;9H\x1b[g\x1b[1;1H\t", nil, 9, 0},
		{"TBC 3", "\x1b[3g\x1b[1;1H\t", nil, 9, 0},
		{"unknown", "\x1b[999zab", []string{"ab"}, 2, 0},
		{"private not core", "\x1b[?5Jab", []string{"ab"}, 2, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term := control(10, 5, tc.input)
			for y := 0; y < 5; y++ {
				want := ""
				if y < len(tc.want) {
					want = tc.want[y]
				}
				if got := term.grid.Text(y); got != want {
					t.Errorf("row %d = %q, want %q", y, got, want)
				}
			}
			if cur := term.grid.Cursor(); cur.X != tc.x || cur.Y != tc.y {
				t.Errorf("Cursor() = %+v, want %d,%d", cur, tc.x, tc.y)
			}
		})
	}
}

func TestCursorStopsAtScrollMargins(t *testing.T) {
	term := NewTerm(10, 6)
	term.grid.SetScrollRegion(ScrollRegion{Top: 2, Bottom: 4, Left: 0, Right: 9})

	term.Write([]byte("\x1b[3;3H\x1b[9A"))
	if cur := term.grid.Cursor(); cur.Y != 2 {
		t.Errorf("CUU inside the region stopped at %d, want the top margin 2", cur.Y)
	}

	term.Write([]byte("\x1b[4;3H\x1b[9B"))
	if cur := term.grid.Cursor(); cur.Y != 4 {
		t.Errorf("CUD inside the region stopped at %d, want the bottom margin 4", cur.Y)
	}
}

func TestEraseWideCellBlanksPartner(t *testing.T) {
	term := control(10, 1, "a世b\x1b[1;3H\x1b[J")
	if got := term.grid.Text(0); got != "a" {
		t.Errorf("Text(0) = %q, want %q", got, "a")
	}
	for x, c := range term.grid.lines[0] {
		if c.IsContinuation() {
			t.Errorf("slot %d is an orphaned continuation", x)
		}
	}
}

func TestEditWideCellKeepsPairsValid(t *testing.T) {
	term := control(10, 1, "a世b\x1b[1;2H\x1b[2@")
	for x, c := range term.grid.lines[0] {
		if c.IsContinuation() && (x == 0 || term.grid.lines[0][x-1].Width != WidthWide) {
			t.Errorf("slot %d is an orphaned continuation", x)
		}
		if c.Width == WidthWide && (x+1 >= 10 || term.grid.lines[0][x+1].Width != WidthContinuation) {
			t.Errorf("slot %d is a wide cell with no continuation", x)
		}
	}
}

func TestInsertDeleteLinesRespectRegion(t *testing.T) {
	term := control(10, 4, "a\r\nb\r\nc\r\nd")
	term.grid.SetScrollRegion(ScrollRegion{Top: 1, Bottom: 2, Left: 0, Right: 9})

	// Row 0 is above the region, so IL and DL there do nothing.
	term.Write([]byte("\x1b[1;1H\x1b[L\x1b[M"))
	if got := term.grid.Text(0); got != "a" {
		t.Errorf("after IL/DL outside the region, row 0 = %q, want %q", got, "a")
	}

	term.Write([]byte("\x1b[2;1H\x1b[L"))
	if got := term.grid.Text(2); got != "b" {
		t.Errorf("after IL at the region top, row 2 = %q, want %q", got, "b")
	}
	if got := term.grid.Text(3); got != "d" {
		t.Errorf("after IL at the region top, row 3 = %q, want the row below the region", got)
	}
}

func TestFamiliesRegisterTheirKeys(t *testing.T) {
	for _, key := range []csiKey{{Final: 'm'}, {Final: 'A'}, {Final: 'J'}, {Final: 'K'}} {
		if _, ok := csiTable[key]; !ok {
			t.Errorf("CSI %q is not registered", key.Final)
		}
	}
	if _, ok := escTable[escKey{Final: 'H'}]; !ok {
		t.Error("HTS is not registered")
	}
}
