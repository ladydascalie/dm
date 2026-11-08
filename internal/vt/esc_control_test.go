package vt

import "testing"

// escTerm returns a terminal of w by h with s written and flushed.
func escTerm(t *testing.T, w, h int, s string) *Term {
	t.Helper()
	return modeTerm(t, w, h, s)
}

func TestDECSTRResetTable(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Term)
		check func(*testing.T, *Term)
	}{
		{
			name:  "origin mode",
			setup: func(term *Term) { writeTerm(term, "\x1b[?6h") },
			check: func(t *testing.T, term *Term) {
				if term.grid.Modes().Origin {
					t.Error("origin mode is still on")
				}
			},
		},
		{
			name:  "insert mode",
			setup: func(term *Term) { writeTerm(term, "\x1b[4h") },
			check: func(t *testing.T, term *Term) {
				if term.grid.Modes().Insert {
					t.Error("insert mode is still on")
				}
			},
		},
		{
			name:  "autowrap comes back on",
			setup: func(term *Term) { writeTerm(term, "\x1b[?7l") },
			check: func(t *testing.T, term *Term) {
				if !term.grid.Modes().Wrap {
					t.Error("autowrap is still off")
				}
			},
		},
		{
			name:  "reverse video",
			setup: func(term *Term) { writeTerm(term, "\x1b[?5h") },
			check: func(t *testing.T, term *Term) {
				if term.grid.Modes().ReverseVideo {
					t.Error("reverse video is still on")
				}
			},
		},
		{
			name:  "reverse wraparound",
			setup: func(term *Term) { writeTerm(term, "\x1b[?45h") },
			check: func(t *testing.T, term *Term) {
				if term.grid.Modes().ReverseWrap {
					t.Error("reverse wraparound is still on")
				}
			},
		},
		{
			name:  "left right margin mode",
			setup: func(term *Term) { writeTerm(term, "\x1b[?69h") },
			check: func(t *testing.T, term *Term) {
				if term.grid.Modes().LeftRightMargin {
					t.Error("left/right margin mode is still on")
				}
			},
		},
		{
			name:  "cursor visible",
			setup: func(term *Term) { writeTerm(term, "\x1b[?25l") },
			check: func(t *testing.T, term *Term) {
				if !term.grid.Cursor().Visible {
					t.Error("the cursor is still hidden")
				}
			},
		},
		{
			name: "charset",
			setup: func(term *Term) {
				term.grid.SetCharset(Charset{G0: CharsetDECSpecial, GL: 1})
			},
			check: func(t *testing.T, term *Term) {
				if cs := term.grid.Charset(); cs != (Charset{}) {
					t.Errorf("Charset() = %+v, want the default", cs)
				}
			},
		},
		{
			name: "margins",
			setup: func(term *Term) {
				term.grid.SetScrollRegion(ScrollRegion{Top: 1, Bottom: 2, Left: 1, Right: 3})
			},
			check: func(t *testing.T, term *Term) {
				want := ScrollRegion{Top: 0, Bottom: 5, Left: 0, Right: 19}
				if got := term.grid.ScrollRegion(); got != want {
					t.Errorf("ScrollRegion() = %+v, want %+v", got, want)
				}
			},
		},
		{
			name: "tab stops",
			setup: func(term *Term) {
				term.grid.ClearTabStops()
				term.grid.SetTabStop(3)
			},
			check: func(t *testing.T, term *Term) {
				for x, stop := range term.grid.TabStops() {
					if want := x > 0 && x%8 == 0; stop != want {
						t.Errorf("tab stop at %d = %v, want %v", x, stop, want)
					}
				}
			},
		},
		{
			name: "saved cursor goes home",
			setup: func(term *Term) {
				writeTerm(term, "\x1b[4;5H\x1b7")
			},
			check: func(t *testing.T, term *Term) {
				writeTerm(term, "\x1b8")
				if c := term.grid.Cursor(); c.X != 0 || c.Y != 0 {
					t.Errorf("after DECRC, Cursor() = %+v, want home", c)
				}
			},
		},
		{
			name:  "rendition",
			setup: func(term *Term) { term.style.Attrs = Bold },
			check: func(t *testing.T, term *Term) {
				if term.style != DefaultStyle() {
					t.Errorf("style = %+v, want the default", term.style)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			term := NewTerm(20, 6)
			tc.setup(term)
			writeTerm(term, "\x1b[!p")
			tc.check(t, term)
		})
	}
}

func TestDECSTRKeepsContentsAndCursor(t *testing.T) {
	term := escTerm(t, 20, 6, "abc\x1b[4;5H")
	writeTerm(term, "\x1b[!p")
	if got := term.grid.Text(0); got != "abc" {
		t.Errorf("Text(0) = %q, want the contents kept", got)
	}
	if c := term.grid.Cursor(); c.X != 4 || c.Y != 3 {
		t.Errorf("Cursor() = %+v, want the position kept at 4,3", c)
	}
}

// TestDECSTRCascade is the case the spec warns about: a margin set by one
// sequence leaks into the next unless DECSTR clears it. DECSTBM writes the
// same scroll region through SetScrollRegion, so the test drives that setter.
func TestDECSTRCascade(t *testing.T) {
	term := escTerm(t, 5, 4, "\x1b[2;1Hr1\x1b[3;1Hr2")
	term.grid.SetScrollRegion(ScrollRegion{Top: 1, Bottom: 2, Left: 0, Right: 4})

	// With the margin in place, LF on the bottom margin scrolls only it.
	writeTerm(term, "\x1b[3;1H\n")
	if got := term.grid.Text(1); got != "r2" {
		t.Fatalf("inside the margin, Text(1) = %q, want r2", got)
	}
	if got := term.grid.Text(0); got != "" {
		t.Fatalf("inside the margin, Text(0) = %q, want untouched", got)
	}

	writeTerm(term, "\x1b[!p")
	if got, want := term.grid.ScrollRegion(), (ScrollRegion{Top: 0, Bottom: 3, Left: 0, Right: 4}); got != want {
		t.Fatalf("after DECSTR, ScrollRegion() = %+v, want %+v", got, want)
	}

	// After the reset, LF on the old bottom margin scrolls the whole screen.
	writeTerm(term, "\x1b[4;1H\n")
	if got := term.grid.Text(0); got != "r2" {
		t.Errorf("after DECSTR, Text(0) = %q, want the screen scrolled", got)
	}
	if got := term.grid.Text(3); got != "" {
		t.Errorf("after DECSTR, Text(3) = %q, want the blank row", got)
	}
}

func TestRISResetsEverything(t *testing.T) {
	term := escTerm(t, 10, 4, "abc\x1b[?25l\x1b[?7l\x1b[?45h\x1b[?1000h\x1b[?2004h\x1b[?47h")
	term.grid.SetCharset(Charset{G0: CharsetDECSpecial, GL: 1})
	term.grid.SetTabStop(3)
	term.grid.SetScrollRegion(ScrollRegion{Top: 1, Bottom: 2, Left: 1, Right: 3})
	term.style.Attrs = Bold

	writeTerm(term, "\x1bc")

	if got := term.grid.Modes(); got != defaultModes() {
		t.Errorf("Modes() = %+v, want the defaults", got)
	}
	if c := term.grid.Cursor(); c.X != 0 || c.Y != 0 || !c.Visible {
		t.Errorf("Cursor() = %+v, want a visible cursor at home", c)
	}
	if got, want := term.grid.ScrollRegion(), (ScrollRegion{Top: 0, Bottom: 3, Left: 0, Right: 9}); got != want {
		t.Errorf("ScrollRegion() = %+v, want %+v", got, want)
	}
	if cs := term.grid.Charset(); cs != (Charset{}) {
		t.Errorf("Charset() = %+v, want the default", cs)
	}
	if term.style != DefaultStyle() {
		t.Errorf("style = %+v, want the default", term.style)
	}
	for y := 0; y < 4; y++ {
		if got := term.grid.Text(y); got != "" {
			t.Errorf("Text(%d) = %q, want the screen blank", y, got)
		}
	}
	for x, stop := range term.grid.TabStops() {
		if want := x > 0 && x%8 == 0; stop != want {
			t.Errorf("tab stop at %d = %v, want %v", x, stop, want)
		}
	}

	// The alternate screen is reset too, not left with the old contents.
	writeTerm(term, "\x1b[?47h")
	if got := term.grid.Text(0); got != "" {
		t.Errorf("the alternate screen Text(0) = %q, want blank", got)
	}
}

func TestRISResetsColumnMode(t *testing.T) {
	term := escTerm(t, 80, 4, "\x1b[?40h\x1b[?3h")
	if cols, _ := term.grid.Size(); cols != 132 {
		t.Fatalf("cols = %d, want 132 before RIS", cols)
	}
	writeTerm(term, "\x1bc")
	if cols, _ := term.grid.Size(); cols != 80 {
		t.Errorf("cols = %d, want 80 after RIS", cols)
	}
	if term.grid.Modes().ColumnMode || term.grid.Modes().Allow80To132 {
		t.Errorf("Modes() = %+v, want column mode and DECSET 40 cleared", term.grid.Modes())
	}
}

func TestDECALN(t *testing.T) {
	term := escTerm(t, 6, 3, "abc")
	term.grid.SetScrollRegion(ScrollRegion{Top: 1, Bottom: 1, Left: 1, Right: 2})

	writeTerm(term, "\x1b#8")

	for y := 0; y < 3; y++ {
		for x := 0; x < 6; x++ {
			if got := term.grid.Cell(x, y).Content; got != "E" {
				t.Fatalf("Cell(%d,%d) = %q, want E", x, y, got)
			}
		}
	}
	if c := term.grid.Cursor(); c.X != 0 || c.Y != 0 {
		t.Errorf("Cursor() = %+v, want home", c)
	}
	if got, want := term.grid.ScrollRegion(), (ScrollRegion{Top: 0, Bottom: 2, Left: 0, Right: 5}); got != want {
		t.Errorf("ScrollRegion() = %+v, want %+v", got, want)
	}
}

func TestDECSCDECRC(t *testing.T) {
	term := escTerm(t, 10, 6, "")
	term.style.Attrs = Bold
	term.grid.SetCharset(Charset{G0: CharsetDECSpecial, GL: 1})
	term.grid.SetScrollRegion(ScrollRegion{Top: 1, Bottom: 4, Left: 0, Right: 9})
	writeTerm(term, "\x1b[?6h\x1b[4;5H\x1b7")

	writeTerm(term, "\x1b[2;2H")
	term.style = DefaultStyle()
	term.grid.SetCharset(Charset{})
	m := term.grid.Modes()
	m.Origin = false
	term.grid.SetModes(m)
	writeTerm(term, "\x1b8")

	if c := term.grid.Cursor(); c.X != 4 || c.Y != 3 {
		t.Errorf("Cursor() = %+v, want the saved 4,3", c)
	}
	if term.style.Attrs != Bold {
		t.Errorf("style = %+v, want the saved bold", term.style)
	}
	if cs := term.grid.Charset(); cs.G0 != CharsetDECSpecial || cs.GL != 1 {
		t.Errorf("Charset() = %+v, want the saved DEC special G0", cs)
	}
	if !term.grid.Modes().Origin {
		t.Error("origin mode is off, want the saved on")
	}
}

func TestDECRCWithoutSaveIsHome(t *testing.T) {
	term := escTerm(t, 20, 6, "\x1b[6;5H\x1b8")
	if c := term.grid.Cursor(); c.X != 0 || c.Y != 0 {
		t.Errorf("Cursor() = %+v, want home", c)
	}
	if !term.grid.Cursor().Visible {
		t.Error("the cursor is hidden after DECRC without DECSC")
	}
}

func TestREP(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"default param repeats once", "a\x1b[b", "aa"},
		{"explicit param", "a\x1b[2b", "aaa"},
		{"wide cluster", "世\x1b[b", "世世"},
		{"no preceding character", "\x1b[b", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			term := writeAll(t, tc.in)
			if got := term.grid.Text(0); got != tc.want {
				t.Errorf("Text(0) = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestREPWraps(t *testing.T) {
	term := escTerm(t, 3, 2, "ab\x1b[2b")
	if got := term.grid.Text(0); got != "abb" {
		t.Errorf("Text(0) = %q, want abb", got)
	}
	if got := term.grid.Text(1); got != "b" {
		t.Errorf("Text(1) = %q, want b", got)
	}
}

func TestSOSSwallowsItsBody(t *testing.T) {
	term := writeAll(t, "\x1bXxyz\x1b\\A")
	if got := term.grid.Text(0); got != "A" {
		t.Errorf("Text(0) = %q, want A", got)
	}
	if c := term.grid.Cursor(); c.X != 1 || c.Y != 0 {
		t.Errorf("Cursor() = %+v, want 1,0", c)
	}
}

func TestPMSwallowsItsBody(t *testing.T) {
	term := writeAll(t, "\x1b^xyz\x1b\\A")
	if got := term.grid.Text(0); got != "A" {
		t.Errorf("Text(0) = %q, want A", got)
	}
	if c := term.grid.Cursor(); c.X != 1 || c.Y != 0 {
		t.Errorf("Cursor() = %+v, want 1,0", c)
	}
}
