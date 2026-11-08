package vt

import "testing"

// TestDECSpecialGraphicsTable pins the whole DEC special graphics set. The
// right-hand rune is the VT100 line-drawing glyph for the left-hand byte.
func TestDECSpecialGraphicsTable(t *testing.T) {
	want := map[rune]rune{
		'`': '◆',
		'a': '▒',
		'b': '␉',
		'c': '␌',
		'd': '␍',
		'e': '␊',
		'f': '°',
		'g': '±',
		'h': '␤',
		'i': '␋',
		'j': '┘',
		'k': '┐',
		'l': '┌',
		'm': '└',
		'n': '┼',
		'o': '⎺',
		'p': '⎻',
		'q': '─',
		'r': '⎼',
		's': '⎽',
		't': '├',
		'u': '┤',
		'v': '┴',
		'w': '┬',
		'x': '│',
		'y': '≤',
		'z': '≥',
		'{': 'π',
		'|': '≠',
		'}': '£',
		'~': '·',
	}
	if len(decSpecialGraphics) != len(want) {
		t.Fatalf("table has %d entries, want %d", len(decSpecialGraphics), len(want))
	}
	for from, to := range want {
		if got := decSpecialGraphics[from]; got != to {
			t.Errorf("%q maps to %q, want %q", from, got, to)
		}
	}
}

// TestTranslateCharset pins the GL selection and the two mapped sets.
func TestTranslateCharset(t *testing.T) {
	cases := []struct {
		name    string
		charset Charset
		in, out string
	}{
		{"ASCII leaves text alone", Charset{G0: CharsetASCII}, "hello", "hello"},
		{"DEC special maps the byte", Charset{G0: CharsetDECSpecial}, "q", "─"},
		{"DEC special maps the corner", Charset{G0: CharsetDECSpecial}, "j", "┘"},
		{"DEC special leaves other bytes alone", Charset{G0: CharsetDECSpecial}, "A", "A"},
		{"GL 1 uses G1", Charset{G0: CharsetASCII, G1: CharsetDECSpecial, GL: 1}, "x", "│"},
		{"SI uses G0", Charset{G0: CharsetDECSpecial, G1: CharsetASCII}, "x", "│"},
		{"UK maps the hash", Charset{G0: CharsetUK}, "#", "£"},
		{"UK leaves other bytes alone", Charset{G0: CharsetUK}, "a", "a"},
		{"cluster keeps the mark", Charset{G0: CharsetDECSpecial}, "q\u0301", "─\u0301"},
		{"empty stays empty", Charset{G0: CharsetDECSpecial}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TranslateCharset(tc.charset, tc.in); got != tc.out {
				t.Errorf("TranslateCharset(%+v, %q) = %q, want %q", tc.charset, tc.in, got, tc.out)
			}
		})
	}
}

// TestIndexHandler pins IND: down one row, scrolling at the bottom margin.
func TestIndexHandler(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
		x, y  int
	}{
		{
			name:  "moves down one row",
			input: "\x1b[3;5H\x1bD",
			want:  []string{"", "", "", ""},
			x:     4, y: 3,
		},
		{
			name:  "scrolls at the bottom",
			input: "a\r\nb\r\nc\x1b[3;1H\x1bD",
			want:  []string{"b", "c"},
			x:     0, y: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term := NewTerm(10, 5)
			if tc.name == "scrolls at the bottom" {
				term = NewTerm(4, 3)
			}
			term.Write([]byte(tc.input))
			term.Flush()
			checkGrid(t, term, tc.want, tc.x, tc.y)
		})
	}
}

// TestReverseIndexHandler pins RI: up one row, scrolling at the top margin.
func TestReverseIndexHandler(t *testing.T) {
	term := NewTerm(4, 3)
	term.Write([]byte("a\r\nb\r\nc\x1b[1;1H\x1bM"))
	term.Flush()
	checkGrid(t, term, []string{"", "a", "b"}, 0, 0)
}

// TestNextLineHandler pins NEL: down one row, then to the left margin.
func TestNextLineHandler(t *testing.T) {
	term := NewTerm(10, 5)
	term.Write([]byte("\x1b[3;5H\x1bE"))
	term.Flush()
	checkGrid(t, term, nil, 0, 3)
}

// TestLineMoveMarginTable pins that IND, NEL and RI scroll only inside the
// left and right margins, and how NEL chooses its home column.
func TestLineMoveMarginTable(t *testing.T) {
	const cols, rows = 20, 10
	region := ScrollRegion{Top: 1, Bottom: 3, Left: 2, Right: 6}
	cases := []struct {
		name  string
		input string
		want  []string
		x, y  int
	}{
		{
			name:  "IND scrolls inside the margins",
			input: "\x1b[4;4Hx\x1b[4;4H\x1bD",
			want:  []string{"", "", "   x"},
			x:     3, y: 3,
		},
		{
			name:  "IND does not scroll right of the margins",
			input: "\x1b[4;8Hx\x1b[4;8H\x1bD",
			want:  []string{"", "", "", "       x"},
			x:     7, y: 3,
		},
		{
			name:  "IND does not scroll left of the margins",
			input: "\x1b[4;1Hx\x1b[4;1H\x1bD",
			want:  []string{"", "", "", "x"},
			x:     0, y: 3,
		},
		{
			name:  "RI scrolls inside the margins",
			input: "\x1b[2;4Hx\x1b[2;4H\x1bM",
			want:  []string{"", "", "   x"},
			x:     3, y: 1,
		},
		{
			name:  "RI does not scroll right of the margins",
			input: "\x1b[2;8Hx\x1b[2;8H\x1bM",
			want:  []string{"", "       x"},
			x:     7, y: 1,
		},
		{
			name:  "NEL homes to the left margin",
			input: "\x1b[4;8H\x1bE",
			want:  nil,
			x:     2, y: 3,
		},
		{
			name:  "NEL keeps a column left of the left margin",
			input: "\x1b[4;1H\x1bE",
			want:  nil,
			x:     0, y: 3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term := NewTerm(cols, rows)
			term.grid.SetScrollRegion(region)
			term.Write([]byte(tc.input))
			term.Flush()
			checkGrid(t, term, tc.want, tc.x, tc.y)
		})
	}
}

// TestLineMoveStopsOutsideTheRegion pins that IND and RI stop at the screen
// edge when the cursor starts outside the scroll region.
func TestLineMoveStopsOutsideTheRegion(t *testing.T) {
	term := NewTerm(10, 5)
	term.grid.SetScrollRegion(ScrollRegion{Top: 1, Bottom: 3, Left: 0, Right: 9})
	term.Write([]byte("\x1b[1;2H\x1bD\x1b[5;2H\x1bM"))
	term.Flush()
	checkGrid(t, term, nil, 1, 3)
}

// TestLineMoveSequenceGrids is the table of line-move sequences and the grids
// they produce.
func TestLineMoveSequenceGrids(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
		x, y  int
	}{
		{"IND", "\x1b[1;3Ha\x1bD", []string{"  a"}, 3, 1},
		{"NEL", "\x1b[1;3Ha\x1bE", []string{"  a"}, 0, 1},
		{"RI", "\x1b[3;3Ha\x1bM", []string{"", "", "  a"}, 3, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term := NewTerm(10, 4)
			term.Write([]byte(tc.input))
			term.Flush()
			checkGrid(t, term, tc.want, tc.x, tc.y)
		})
	}
}

// checkGrid compares a terminal's visible rows and cursor with want.
func checkGrid(t *testing.T, term *Term, want []string, x, y int) {
	t.Helper()
	_, rows := term.grid.Size()
	for row := 0; row < rows; row++ {
		wantRow := ""
		if row < len(want) {
			wantRow = want[row]
		}
		if got := term.grid.Text(row); got != wantRow {
			t.Errorf("row %d = %q, want %q", row, got, wantRow)
		}
	}
	cur := term.grid.Cursor()
	if cur.X != x || cur.Y != y {
		t.Errorf("cursor = %d,%d, want %d,%d", cur.X, cur.Y, x, y)
	}
}
