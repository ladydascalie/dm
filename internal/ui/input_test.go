package ui

import (
	"bytes"
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/theme"
	"dm/internal/vt"
)

// TestInputKeys runs one case per keys table row of the component contract,
// plus the edges where a method must change nothing. A method returns nothing,
// so each case reads the value and the caret back.
func TestInputKeys(t *testing.T) {
	type tc struct {
		name  string
		value string
		caret int
		op    func(*Input)
		want  string
		wantC int
	}
	cases := []tc{
		{"set a value", "old", 0, func(in *Input) { in.SetValue("new") }, "new", 3},
		{"set the empty value", "old", 3, func(in *Input) { in.SetValue("") }, "", 0},
		{"insert a rune at the end", "ab", 2, func(in *Input) { in.InsertRune('c') }, "abc", 3},
		{"insert a rune at the start", "ab", 0, func(in *Input) { in.InsertRune('c') }, "cab", 1},
		{"insert a rune mid text", "ab", 1, func(in *Input) { in.InsertRune('c') }, "acb", 2},
		{"paste mid text", "ab", 1, func(in *Input) { in.InsertString("XY") }, "aXYb", 3},
		{"paste the empty string", "ab", 1, func(in *Input) { in.InsertString("") }, "ab", 1},
		{"backspace mid text", "abc", 2, func(in *Input) { in.Backspace() }, "ac", 1},
		{"backspace at the start", "abc", 0, func(in *Input) { in.Backspace() }, "abc", 0},
		{"backspace on the empty value", "", 0, func(in *Input) { in.Backspace() }, "", 0},
		{"delete mid text", "abc", 1, func(in *Input) { in.Delete() }, "ac", 1},
		{"delete at the end", "abc", 3, func(in *Input) { in.Delete() }, "abc", 3},
		{"delete on the empty value", "", 0, func(in *Input) { in.Delete() }, "", 0},
		{"left moves one rune", "abc", 2, func(in *Input) { in.Left() }, "abc", 1},
		{"left at the start", "abc", 0, func(in *Input) { in.Left() }, "abc", 0},
		{"right moves one rune", "abc", 1, func(in *Input) { in.Right() }, "abc", 2},
		{"right at the end", "abc", 3, func(in *Input) { in.Right() }, "abc", 3},
		{"right on the empty value", "", 0, func(in *Input) { in.Right() }, "", 0},
		{"home", "abc", 3, func(in *Input) { in.Home() }, "abc", 0},
		{"end", "abc", 0, func(in *Input) { in.End() }, "abc", 3},
		{"kill the tail", "abcd", 2, func(in *Input) { in.Kill() }, "ab", 2},
		{"kill at the end", "abc", 3, func(in *Input) { in.Kill() }, "abc", 3},
		{"kill at the start", "abc", 0, func(in *Input) { in.Kill() }, "", 0},
		{"a wide rune is one rune", "a", 1, func(in *Input) { in.InsertRune('名') }, "a名", 2},
		{"a pasted cluster is one rune per rune", "", 0, func(in *Input) { in.InsertString("名前") }, "名前", 2},
	}
	for _, c := range cases {
		in := Input{Value: []rune(c.value), Caret: c.caret}
		c.op(&in)
		if got := in.String(); got != c.want {
			t.Fatalf("%s: value = %q, want %q", c.name, got, c.want)
		}
		if in.Caret != c.wantC {
			t.Fatalf("%s: caret = %d, want %d", c.name, in.Caret, c.wantC)
		}
	}
}

// TestInputClampedCaret guards the exported fields: a caller that sets Value
// and leaves Caret behind gets an edit at the end of the runes, not a panic and
// not a gap.
func TestInputClampedCaret(t *testing.T) {
	in := Input{Value: []rune("ab"), Caret: 9}
	in.InsertRune('c')
	if got := in.String(); got != "abc" {
		t.Fatalf("value = %q, want %q", got, "abc")
	}
	back := Input{Value: []rune("ab"), Caret: -4}
	back.Backspace()
	if got := back.String(); got != "ab" || back.Caret != 0 {
		t.Fatalf("value = %q caret = %d, want %q and 0", got, back.Caret, "ab")
	}
}

// TestInputStringAndEmpty: the accessors read the value alone, and the caret is
// not part of it.
func TestInputStringAndEmpty(t *testing.T) {
	var in Input
	if !in.Empty() || in.String() != "" {
		t.Fatalf("a fresh input reads %q and empty = %v", in.String(), in.Empty())
	}
	in.SetValue("main")
	if in.Empty() || in.String() != "main" {
		t.Fatalf("after SetValue the input reads %q and empty = %v", in.String(), in.Empty())
	}
	in.SetValue("")
	if !in.Empty() {
		t.Fatalf("an emptied input reads %q", in.String())
	}
}

// rowText is one painted row, one cluster per cell it covers. A wide cell is
// written once, so a two cell cluster reads as one rune.
func rowText(g *vt.Grid, y, x, w int) string {
	var b strings.Builder
	for col := x; col < x+w; col = g.Next(col, y) {
		c := g.Cell(col, y)
		if c.Content == "" {
			b.WriteByte(' ')
			continue
		}
		b.WriteString(c.Content)
	}
	return b.String()
}

// drawRow paints the input into a blank terminal of the rect's size and reads
// the row back. It returns the row, the grid and the cell DrawInput parked the
// cursor on.
func drawRow(t *testing.T, p InputProps, in Input) (string, *vt.Grid, int, int) {
	t.Helper()
	var buf bytes.Buffer
	cx, cy := DrawInput(&buf, theme.DefaultTheme(), p, in)
	term := vt.NewTerm(p.Rect.X+p.Rect.W, p.Rect.Y+p.Rect.H)
	term.Write(buf.Bytes())
	term.Flush()
	g := term.Grid()
	return rowText(g, p.Rect.Y, p.Rect.X, p.Rect.W), g, cx, cy
}

// TestDrawInput paints the props and reads the cells back. Each case is one row
// of the props table or of the contract's rules: the prompt, the value, the
// caret cell, the trailing text, the elision, the empty value, the caret at
// either end, and a wide rune cut whole.
func TestDrawInput(t *testing.T) {
	th := theme.DefaultTheme()
	cases := []struct {
		name     string
		rect     layout.Rect
		prompt   string
		trailing string
		value    string
		caret    int
		active   bool
		want     string
		wantCX   int
		wantCY   int
	}{
		{
			// internal/client:promptRow's rename row: the label, the draft
			// and the caret on the rail ground.
			name:   "the rename row",
			rect:   layout.Rect{W: 20, H: 1},
			prompt: " name: ",
			value:  "main",
			caret:  4,
			active: true,
			want:   " name: main▏        ",
			wantCX: 11,
		},
		{
			// internal/client:paintPaletteQuery's row: the prompt, the query
			// and the right-edge signpost, with no caret cell of its own.
			name:     "the palette row",
			rect:     layout.Rect{W: 20, H: 1},
			prompt:   "> ",
			trailing: "  ? help",
			value:    "send",
			caret:    4,
			want:     "> send        ? help",
			wantCX:   6,
		},
		{
			name:   "the palette row with no query",
			rect:   layout.Rect{W: 20, H: 1},
			prompt: "> ",
			caret:  0,
			want:   ">                   ",
			wantCX: 2,
		},
		{
			name:   "the empty value",
			rect:   layout.Rect{W: 8, H: 1},
			caret:  0,
			active: true,
			want:   "▏       ",
			wantCX: 0,
		},
		{
			name:   "an inactive input paints no caret cell",
			rect:   layout.Rect{W: 8, H: 1},
			caret:  0,
			want:   "        ",
			wantCX: 0,
		},
		{
			name:   "the caret at the start",
			rect:   layout.Rect{W: 10, H: 1},
			prompt: "> ",
			value:  "hello",
			caret:  0,
			active: true,
			want:   "> ▏hello  ",
			wantCX: 2,
		},
		{
			name:   "the caret at the end",
			rect:   layout.Rect{W: 10, H: 1},
			prompt: "> ",
			value:  "hello",
			caret:  5,
			active: true,
			want:   "> hello▏  ",
			wantCX: 7,
		},
		{
			name:   "the caret mid text",
			rect:   layout.Rect{W: 10, H: 1},
			prompt: "> ",
			value:  "hello",
			caret:  2,
			active: true,
			want:   "> he▏llo  ",
			wantCX: 4,
		},
		{
			name:   "the head is elided so the caret stays",
			rect:   layout.Rect{W: 20, H: 1},
			prompt: " name: ",
			value:  "a very long workspace name",
			caret:  26,
			active: true,
			want:   " name: …kspace name▏",
			wantCX: 19,
		},
		{
			name:   "a wide rune keeps its cells",
			rect:   layout.Rect{W: 6, H: 1},
			value:  "名前",
			caret:  2,
			active: true,
			want:   "名前▏ ",
			wantCX: 4,
		},
		{
			name:   "a wide rune is cut whole",
			rect:   layout.Rect{W: 6, H: 1},
			value:  "名前名",
			caret:  3,
			active: true,
			want:   "…前名▏",
			wantCX: 5,
		},
		{
			name:   "a rect off the origin",
			rect:   layout.Rect{X: 3, Y: 2, W: 10, H: 1},
			prompt: "> ",
			value:  "ab",
			caret:  2,
			active: true,
			want:   "> ab▏     ",
			wantCX: 7,
			wantCY: 2,
		},
		{
			name:   "a prompt wider than the row leaves room for the caret",
			rect:   layout.Rect{W: 6, H: 1},
			prompt: "prompt: ",
			caret:  0,
			active: true,
			want:   "promp▏",
			wantCX: 5,
		},
		{
			name:     "a trailing text wider than the row is cut",
			rect:     layout.Rect{W: 8, H: 1},
			prompt:   "> ",
			trailing: "? help",
			caret:    0,
			want:     "> ? help",
			wantCX:   2,
		},
		{
			name:   "a full row parks the cursor on its last cell",
			rect:   layout.Rect{W: 6, H: 1},
			prompt: "> ",
			value:  "abcd",
			caret:  4,
			want:   "> abcd",
			wantCX: 5,
		},
	}
	for _, c := range cases {
		p := InputProps{
			Rect:     c.rect,
			Prompt:   c.prompt,
			Trailing: c.trailing,
			Ground:   th.Palette.RailBG,
			Dim:      th.Palette.RailDim,
			Text:     th.Palette.RailText,
			Caret:    th.Palette.BarAccent,
			Active:   c.active,
		}
		got, g, cx, cy := drawRow(t, p, Input{Value: []rune(c.value), Caret: c.caret})
		if got != c.want {
			t.Fatalf("%s: row = %q, want %q", c.name, got, c.want)
		}
		if cx != c.wantCX || cy != c.wantCY {
			t.Fatalf("%s: caret cell = (%d,%d), want (%d,%d)", c.name, cx, cy, c.wantCX, c.wantCY)
		}
		// The row owns every cell of the rect: no cell keeps the ground of
		// the frame before it.
		wantBg := vtColor(p.Ground)
		for col := c.rect.X; col < c.rect.X+c.rect.W; col++ {
			if cell := g.Cell(col, c.rect.Y); cell.Style.Bg != wantBg {
				t.Fatalf("%s: cell %d has bg %v, want the ground %v", c.name, col, cell.Style.Bg, wantBg)
			}
		}
	}
}

// TestDrawInputColours: the prompt and the trailing text paint in Dim, the
// value in Text and the caret cell in Caret, on the row's ground.
func TestDrawInputColours(t *testing.T) {
	th := theme.DefaultTheme()
	p := InputProps{
		Rect:     layout.Rect{W: 16, H: 1},
		Prompt:   " name: ",
		Trailing: " ",
		Ground:   th.Palette.RailBG,
		Dim:      th.Palette.RailDim,
		Text:     th.Palette.RailText,
		Caret:    th.Palette.BarAccent,
		Active:   true,
	}
	_, g, cx, cy := drawRow(t, p, Input{Value: []rune("main"), Caret: 4})
	want := []struct {
		from, to int
		fg       theme.RGB
	}{
		{0, 7, p.Dim},
		{7, 11, p.Text},
		{11, 12, p.Caret},
		{15, 16, p.Dim},
	}
	for _, w := range want {
		for col := w.from; col < w.to; col++ {
			if got := g.Cell(col, 0).Style.Fg; got != vtColor(w.fg) {
				t.Fatalf("cell %d fg = %v, want %v", col, got, vtColor(w.fg))
			}
		}
	}
	if cx != 11 || cy != 0 {
		t.Fatalf("caret cell = (%d,%d), want (11,0)", cx, cy)
	}
	// The caret cell is the only cell painted in the caret colour.
	for col := 0; col < 16; col++ {
		if col == cx {
			continue
		}
		if g.Cell(col, 0).Style.Fg == vtColor(p.Caret) {
			t.Fatalf("cell %d paints the caret colour", col)
		}
	}
}

// TestDrawInputInactiveKeepsTheCaretCell: an inactive input paints no caret
// cell, so the row is the prompt and the value alone.
func TestDrawInputInactiveKeepsTheCaretCell(t *testing.T) {
	th := theme.DefaultTheme()
	p := InputProps{
		Rect:   layout.Rect{W: 10, H: 1},
		Prompt: "> ",
		Ground: th.Palette.RailBG,
		Dim:    th.Palette.RailDim,
		Text:   th.Palette.RailText,
		Caret:  th.Palette.BarAccent,
	}
	row, g, cx, cy := drawRow(t, p, Input{Value: []rune("ab"), Caret: 1})
	if row != "> ab      " {
		t.Fatalf("row = %q, want the value with no caret cell", row)
	}
	if cx != 3 || cy != 0 {
		t.Fatalf("caret cell = (%d,%d), want the cell after the head at (3,0)", cx, cy)
	}
	for col := 0; col < 10; col++ {
		if g.Cell(col, 0).Style.Fg == vtColor(p.Caret) {
			t.Fatalf("cell %d paints the caret colour on an inactive input", col)
		}
	}
}

// TestDrawInputEmptyRect paints nothing and parks nothing, so a caller that
// gives the input no room leaves the frame alone.
func TestDrawInputEmptyRect(t *testing.T) {
	for _, r := range []layout.Rect{{W: 0, H: 1}, {W: 10, H: 0}, {X: 4, Y: 5}} {
		var buf bytes.Buffer
		cx, cy := DrawInput(&buf, theme.DefaultTheme(), InputProps{Rect: r}, Input{Value: []rune("x")})
		if buf.Len() != 0 {
			t.Fatalf("%+v: painted %q", r, buf.String())
		}
		if cx != r.X || cy != r.Y {
			t.Fatalf("%+v: caret cell = (%d,%d), want the rect's origin", r, cx, cy)
		}
	}
}

// vtColor maps a theme colour to the cell colour the terminal records, so a
// test can read a painted cell back.
func vtColor(c theme.RGB) vt.Color {
	return vt.RGBColor(uint8(c.R), uint8(c.G), uint8(c.B))
}
