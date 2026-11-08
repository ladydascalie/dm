package vt

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/hinshun/vt10x"
)

// diffCase is one byte stream handed to both terminals. Chunks keeps the
// boundaries, so a chunked stream is compared too.
type diffCase struct {
	Name   string
	Cols   int
	Rows   int
	Chunks []string
}

// diffEntry declares one expected difference between the new VT and vt10x.
// Field is "cell", "cursor.x", "cursor.y", "cursor.visible" or "mode.<name>".
// X and Y are the cell coordinates, or -1 when the field is not a cell.
type diffEntry struct {
	Case   string
	Field  string
	X, Y   int
	New    string
	Old    string
	Reason string
}

// diffCorpus is the streams both terminals parse. It covers plain text, the C0
// controls, the core CSI family, and a few sequences the new VT does not
// implement yet.
var diffCorpus = []diffCase{
	{"plain", 10, 3, []string{"hello"}},
	{"wrap", 10, 3, []string{"abcdefghijk"}},
	{"wrap-two-lines", 5, 3, []string{"abcdefghijk"}},
	{"controls", 10, 3, []string{"ab\r\ncd\te\x08f"}},
	{"scroll-on-lf", 6, 3, []string{"1\r\n2\r\n3\r\n4"}},
	{"cup", 10, 4, []string{"a\x1b[3;4Hb"}},
	{"cuf-cub", 10, 4, []string{"\x1b[1;5H\x1b[2Cx\x1b[3Dy"}},
	{"cuu-cud", 10, 4, []string{"\x1b[3;3H\x1b[Az\x1b[2Bw"}},
	{"cnl-cpl", 10, 5, []string{"\x1b[4;4H\x1b[Ex\x1b[2Fy"}},
	{"cha-vpa", 10, 4, []string{"\x1b[5G\x1b[3dQ"}},
	{"el", 10, 2, []string{"abcdef\x1b[1;3H\x1b[K"}},
	{"ed-below", 10, 3, []string{"abc\r\ndef\x1b[1;2H\x1b[J"}},
	{"ed-above", 10, 4, []string{"abc\r\ndef\x1b[3;2H\x1b[1J"}},
	{"ed-all", 10, 3, []string{"abc\r\ndef\x1b[2J"}},
	{"ech", 10, 1, []string{"abcdef\x1b[1;2H\x1b[2X"}},
	{"ich", 10, 1, []string{"abcdef\x1b[1;2H\x1b[2@"}},
	{"dch", 10, 1, []string{"abcdef\x1b[1;2H\x1b[2P"}},
	{"il", 10, 4, []string{"a\r\nb\r\nc\x1b[2;1H\x1b[L"}},
	{"dl", 10, 4, []string{"a\r\nb\r\nc\x1b[1;1H\x1b[M"}},
	{"su", 6, 3, []string{"a\r\nb\r\nc\x1b[S"}},
	{"sd", 6, 3, []string{"a\r\nb\r\nc\x1b[T"}},
	{"tabs", 20, 2, []string{"a\tb\x1b[1;5H\x1bH\x1b[1;1H\tc"}},
	{"cht-cbt", 20, 2, []string{"\x1b[Ix\x1b[Z"}},
	{"tbc", 20, 2, []string{"\x1b[1;9H\x1b[g\x1b[1;1H\tx"}},
	{"altscreen", 10, 1, []string{"\x1b[?1049hX"}},

	{"sgr-bold-red", 10, 1, []string{"\x1b[1;31mX"}},
	{"sgr-attrs", 10, 1, []string{"\x1b[1;2;3;5;6;7;8;9mX"}},
	{"sgr-palette", 10, 1, []string{"\x1b[38;5;200mX"}},
	{"sgr-rgb", 10, 1, []string{"\x1b[38;2;1;2;3mX"}},
	{"sgr-bg", 10, 1, []string{"\x1b[48;5;100mX"}},
	{"sgr-underline-style", 10, 1, []string{"\x1b[4:3mX"}},
	{"sgr-colon-rgb", 10, 1, []string{"\x1b[38:2::10:20:30mX"}},
	{"sgr-reset", 10, 1, []string{"\x1b[1;31mA\x1b[0mB"}},

	// The new VT does not apply the charset table yet; its difference is
	// declared below.
	{"charset-unimplemented", 10, 1, []string{"\x1b(0q"}},
}

// declaredDiffs lists every difference this corpus is allowed to produce.
var declaredDiffs = []diffEntry{
	{
		Case: "sgr-bold-red", Field: "cell", X: 0, Y: 0,
		New: "X|fg=1 bg=d ul=none at=b",
		Old: "X|fg=9 bg=d ul=none at=b",
		Reason: "vt10x bakes bold into the palette colour, so index 1 becomes " +
			"9. The new VT keeps bold as an attribute and the index as given.",
	},
	{
		Case: "sgr-attrs", Field: "cell", X: 0, Y: 0,
		New: "X|fg=d bg=d ul=none at=bfilLrcs",
		Old: "X|fg=d bg=d ul=none at=bilr",
		Reason: "vt10x has no bit for faint, rapid blink, conceal or " +
			"strikethrough, so it drops them. The new VT keeps all eight.",
	},
	{
		Case: "sgr-underline-style", Field: "cell", X: 0, Y: 0,
		New: "X|fg=d bg=d ul=curly at=",
		Old: "X|fg=d bg=d ul=none at=",
		Reason: "vt10x reads only the leading number of 4:3 and implements " +
			"no underline style. The new VT keeps the style and renders curly.",
	},
	{
		Case: "sgr-colon-rgb", Field: "cell", X: 0, Y: 0,
		New: "X|fg=0a141e bg=d ul=none at=",
		Old: "X|fg=d bg=d ul=none at=",
		Reason: "vt10x does not read the colon form of the extended colour " +
			"parameters, so the colour is lost. The new VT parses it.",
	},
	{
		Case: "sgr-reset", Field: "cell", X: 0, Y: 0,
		New: "A|fg=1 bg=d ul=none at=b",
		Old: "A|fg=9 bg=d ul=none at=b",
		Reason: "The same bold-brightens-the-colour difference as " +
			"sgr-bold-red. The reset itself agrees: B is default in both.",
	},
	{
		Case: "charset-unimplemented", Field: "cell", X: 0, Y: 0,
		New: "q|fg=d bg=d ul=none at=",
		Old: "─|fg=d bg=d ul=none at=",
		Reason: "The DEC special graphics table is not applied yet, so the new " +
			"VT stores q where vt10x draws the line-drawing glyph.",
	},
}

// runDiffCase writes the chunks to both terminals and flushes the new one, so
// the pending cluster is placed before the comparison.
func runDiffCase(c diffCase) (*Term, vt10x.Terminal) {
	newT := NewTerm(c.Cols, c.Rows)
	oldT := vt10x.New(vt10x.WithSize(c.Cols, c.Rows))
	for _, chunk := range c.Chunks {
		newT.Write([]byte(chunk))
		oldT.Write([]byte(chunk))
	}
	newT.Flush()
	return newT, oldT
}

// observe lists every difference between the two terminals.
func observe(c diffCase, newT *Term, oldT vt10x.Terminal) []diffEntry {
	var out []diffEntry
	add := func(field string, x, y int, newVal, oldVal string) {
		if newVal != oldVal {
			out = append(out, diffEntry{Case: c.Name, Field: field, X: x, Y: y, New: newVal, Old: oldVal})
		}
	}

	for y := 0; y < c.Rows; y++ {
		for x := 0; x < c.Cols; x++ {
			add("cell", x, y, newCellString(newT.grid.lines[y][x]), oldCellString(oldT.Cell(x, y)))
		}
	}

	nc, oc := newT.grid.Cursor(), oldT.Cursor()
	add("cursor.x", -1, -1, strconv.Itoa(nc.X), strconv.Itoa(oc.X))
	add("cursor.y", -1, -1, strconv.Itoa(nc.Y), strconv.Itoa(oc.Y))
	add("cursor.visible", -1, -1, onOff(nc.Visible), onOff(oldT.CursorVisible()))

	nm, om := newT.grid.Modes(), oldT.Mode()
	add("mode.wrap", -1, -1, onOff(nm.Wrap), onOff(om&vt10x.ModeWrap != 0))
	add("mode.altscreen", -1, -1, onOff(nm.AltScreen), onOff(om&vt10x.ModeAltScreen != 0))
	add("mode.insert", -1, -1, onOff(nm.Insert), onOff(om&vt10x.ModeInsert != 0))
	add("mode.reverse", -1, -1, onOff(nm.ReverseVideo), onOff(om&vt10x.ModeReverse != 0))

	return out
}

// reconcile sorts observed differences into matched, undeclared and stale.
// A difference matches a declaration on case, field, coordinates and values.
func reconcile(observed, declared []diffEntry) (undeclared, stale []diffEntry) {
	key := func(d diffEntry) string {
		return fmt.Sprintf("%s|%s|%d|%d|%s|%s", d.Case, d.Field, d.X, d.Y, d.New, d.Old)
	}
	// Walk the declarations in order, so a failure reports them predictably.
	matched := make([]bool, len(observed))
	for _, d := range declared {
		k := key(d)
		found := false
		for i, o := range observed {
			if !matched[i] && key(o) == k {
				matched[i], found = true, true
				break
			}
		}
		if !found {
			stale = append(stale, d)
		}
	}
	for i, o := range observed {
		if !matched[i] {
			undeclared = append(undeclared, o)
		}
	}
	return undeclared, stale
}

// newCellString renders a new cell as content and style.
func newCellString(c Cell) string {
	content := c.Content
	if content == " " {
		content = ""
	}
	return content + "|" + newStyleString(c.Style)
}

// oldCellString renders a vt10x cell as content and style, with its zero glyph
// normalised to a blank cell, the same way NormCell does for the old
// serialiser.
func oldCellString(g vt10x.Glyph) string {
	g = NormCell(g)
	content := string(g.Char)
	if content == " " {
		content = ""
	}
	return content + "|" + oldStyleString(g)
}

func newStyleString(s Style) string {
	return "fg=" + newColor(s.Fg) + " bg=" + newColor(s.Bg) +
		" ul=" + newUnderline(s.Underline) + " at=" + newAttrs(s.Attrs)
}

func oldStyleString(g vt10x.Glyph) string {
	ul := "none"
	if g.Mode&AttrUnderline != 0 {
		ul = "single"
	}
	return "fg=" + oldColor(g.FG) + " bg=" + oldColor(g.BG) +
		" ul=" + ul + " at=" + oldAttrs(g.Mode)
}

func newColor(c Color) string {
	switch c.Kind {
	case ColorPalette:
		return strconv.Itoa(int(c.Index))
	case ColorRGB:
		return fmt.Sprintf("%06x", int(c.R)<<16|int(c.G)<<8|int(c.B))
	default:
		return "d"
	}
}

func oldColor(c vt10x.Color) string {
	switch {
	case c == vt10x.DefaultFG || c == vt10x.DefaultBG:
		return "d"
	case c < 256:
		return strconv.Itoa(int(c))
	default:
		return fmt.Sprintf("%06x", int(c)&0xffffff)
	}
}

func newUnderline(u UnderlineStyle) string {
	switch u {
	case UnderlineSingle:
		return "single"
	case UnderlineDouble:
		return "double"
	case UnderlineCurly:
		return "curly"
	case UnderlineDotted:
		return "dotted"
	case UnderlineDashed:
		return "dashed"
	default:
		return "none"
	}
}

// newAttrs renders the new attribute bits, one letter each.
func newAttrs(a uint8) string {
	var s string
	for _, bit := range []struct {
		mask uint8
		name string
	}{
		{Bold, "b"}, {Faint, "f"}, {Italic, "i"}, {Blink, "l"}, {RapidBlink, "L"},
		{Reverse, "r"}, {Conceal, "c"}, {Strikethrough, "s"},
	} {
		if a&bit.mask != 0 {
			s += bit.name
		}
	}
	return s
}

// oldAttrs renders the vt10x attribute bits that map onto the new model.
func oldAttrs(mode int16) string {
	var s string
	for _, bit := range []struct {
		mask int16
		name string
	}{
		{AttrBold, "b"}, {AttrUnderline, "u"}, {16, "i"}, {32, "l"}, {attrReverse, "r"},
	} {
		if mode&bit.mask != 0 {
			s += bit.name
		}
	}
	return s
}

// TestDeclaredDiffsAreComplete checks the table itself: every entry names a
// reason and a case the corpus runs.
func TestDeclaredDiffsAreComplete(t *testing.T) {
	cases := map[string]bool{}
	for _, c := range diffCorpus {
		cases[c.Name] = true
	}
	for _, d := range declaredDiffs {
		if d.Reason == "" {
			t.Errorf("declared difference %s/%s has no reason", d.Case, d.Field)
		}
		if !cases[d.Case] {
			t.Errorf("declared difference names case %q, which the corpus does not run", d.Case)
		}
	}
}

// TestWrongCellFails proves the net bites: a value that differs from the
// declaration is reported as undeclared.
func TestWrongCellFails(t *testing.T) {
	declared := []diffEntry{{
		Case: "case", Field: "cell", X: 0, Y: 0, New: "a|s", Old: "a|s",
		Reason: "declared",
	}}
	wrong := []diffEntry{{
		Case: "case", Field: "cell", X: 0, Y: 0, New: "b|s", Old: "a|s",
	}}
	undeclared, stale := reconcile(wrong, declared)
	if len(undeclared) != 1 {
		t.Errorf("undeclared = %d, want 1 for the wrong cell", len(undeclared))
	}
	if len(stale) != 1 {
		t.Errorf("stale = %d, want the unused declaration to be reported", len(stale))
	}
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

// TestDifferential runs the corpus and fails on any difference that is not
// declared, or on a declaration that matches nothing.
func TestDifferential(t *testing.T) {
	var observed, declared []diffEntry
	for _, c := range diffCorpus {
		newT, oldT := runDiffCase(c)
		observed = append(observed, observe(c, newT, oldT)...)
	}
	declared = append(declared, declaredDiffs...)

	undeclared, stale := reconcile(observed, declared)
	for _, d := range undeclared {
		t.Errorf("undeclared difference: %+v", d)
	}
	for _, d := range stale {
		t.Errorf("declared difference matched nothing: %+v", d)
	}
}
