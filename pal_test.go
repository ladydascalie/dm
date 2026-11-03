package main

import "testing"

func TestPalettePick(t *testing.T) {
	cases := []struct {
		name string
		tail string
		want string
	}{
		{"entry after the alt screen exit", "\x1b[?1049lw1p2\r\n", "w1p2"},
		{"noise before the exit is dropped", "\x1b[2Jframe\x1b[?1049l\x1b[?25hs1\r\n", "s1"},
		{"a cancel carries no entry", "\x1b[?1049l\x1b[?25h", ""},
		{"the last line is the answer", "\x1b[?1049lstale\r\nw1\r\n", "w1"},
		{"no exit marker at all", "w2p1\r\n", "w2p1"},
	}
	for _, c := range cases {
		if got := palettePick([]byte(c.tail)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestPaletteRectFits(t *testing.T) {
	cases := [][2]int{{200, 60}, {120, 40}, {80, 24}, {40, 12}, {20, 8}, {12, 5}}
	for _, c := range cases {
		cols, rows := c[0], c[1]
		r := paletteRect(cols, rows)
		if r.W < 1 || r.H < 1 {
			t.Errorf("%dx%d: empty rect %+v", cols, rows, r)
		}
		if r.X < 0 || r.Y < 0 || r.X+r.W > cols || r.Y+r.H > rows {
			t.Errorf("%dx%d: %+v does not fit", cols, rows, r)
		}
	}
}
