package vt

import "testing"

// sgrCell writes in and returns the style of the cell at the cursor.
func sgrCell(t *testing.T, in string) Style {
	t.Helper()
	term := NewTerm(4, 1)
	writeTerm(term, in)
	return term.grid.Cell(0, 0).Style
}

func TestSGRAttributes(t *testing.T) {
	st := sgrCell(t, "\x1b[1;2;3;5;6;7;8;9mX")
	want := Bold | Faint | Italic | Blink | RapidBlink | Reverse | Conceal | Strikethrough
	if st.Attrs != want {
		t.Fatalf("Attrs = %08b, want %08b", st.Attrs, want)
	}
	st = sgrCell(t, "\x1b[1;2;3;5;6;7;8;9m\x1b[22;23;25;27;28;29mX")
	if st.Attrs != 0 {
		t.Fatalf("Attrs = %08b, want cleared", st.Attrs)
	}
}

func TestSGRUnderlineStyles(t *testing.T) {
	tests := []struct {
		in   string
		want UnderlineStyle
	}{
		{"\x1b[4mX", UnderlineSingle},
		{"\x1b[4:0mX", UnderlineNone},
		{"\x1b[4:1mX", UnderlineSingle},
		{"\x1b[4:2mX", UnderlineDouble},
		{"\x1b[4:3mX", UnderlineCurly},
		{"\x1b[4:4mX", UnderlineDotted},
		{"\x1b[4:5mX", UnderlineDashed},
		{"\x1b[21mX", UnderlineDouble},
		{"\x1b[4;24mX", UnderlineNone},
	}
	for _, tc := range tests {
		if got := sgrCell(t, tc.in).Underline; got != tc.want {
			t.Errorf("%q underline = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestSGRColours(t *testing.T) {
	tests := []struct {
		in   string
		fg   Color
		bg   Color
		ul   Color
		pass bool
	}{
		{in: "\x1b[31;42mX", fg: PaletteColor(1), bg: PaletteColor(2), ul: DefaultColor()},
		{in: "\x1b[91;102mX", fg: PaletteColor(9), bg: PaletteColor(10), ul: DefaultColor()},
		{in: "\x1b[38;5;200;48;5;100mX", fg: PaletteColor(200), bg: PaletteColor(100), ul: DefaultColor()},
		{in: "\x1b[38;2;1;2;3mX", fg: RGBColor(1, 2, 3), bg: DefaultColor(), ul: DefaultColor()},
		{in: "\x1b[48;2;255;0;16mX", fg: DefaultColor(), bg: RGBColor(255, 0, 16), ul: DefaultColor()},
		{in: "\x1b[38:5:7mX", fg: PaletteColor(7), bg: DefaultColor(), ul: DefaultColor()},
		{in: "\x1b[38:2::10:20:30mX", fg: RGBColor(10, 20, 30), bg: DefaultColor(), ul: DefaultColor()},
		{in: "\x1b[38:2:10:20:30mX", fg: RGBColor(10, 20, 30), bg: DefaultColor(), ul: DefaultColor()},
		{in: "\x1b[58;5;9mX", fg: DefaultColor(), bg: DefaultColor(), ul: PaletteColor(9)},
		{in: "\x1b[31;58;5;9;39;59mX", fg: DefaultColor(), bg: DefaultColor(), ul: DefaultColor()},
	}
	for _, tc := range tests {
		st := sgrCell(t, tc.in)
		if st.Fg != tc.fg || st.Bg != tc.bg || st.UnderlineColor != tc.ul {
			t.Errorf("%q style = fg %+v bg %+v ul %+v, want %+v %+v %+v",
				tc.in, st.Fg, st.Bg, st.UnderlineColor, tc.fg, tc.bg, tc.ul)
		}
	}
}

func TestSGRReset(t *testing.T) {
	term := NewTerm(4, 1)
	writeTerm(term, "\x1b[1;4:3;31;48;5;2;58;5;9m\x1b[mX")
	st := term.grid.Cell(0, 0).Style
	if st != DefaultStyle() {
		t.Fatalf("style after reset = %+v, want the default", st)
	}
}

func TestSGRKeepsProtection(t *testing.T) {
	term := NewTerm(4, 1)
	writeTerm(term, "\x1b[1\"q\x1b[1;31mX")
	if !term.grid.Cell(0, 0).Protected {
		t.Fatal("SGR cleared the DECSCA protection")
	}
}
